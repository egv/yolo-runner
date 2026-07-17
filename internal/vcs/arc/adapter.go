package arc

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/egv/yolo-runner/v2/internal/arcanum"
)

type Runner interface {
	Run(name string, args ...string) (string, error)
}

type CommandRunner interface {
	Run(args ...string) (string, error)
}

type ArcCommandAdapter struct {
	runner CommandRunner
}

func NewArcCommandAdapter(runner CommandRunner) *ArcCommandAdapter {
	return &ArcCommandAdapter{runner: runner}
}

func (a *ArcCommandAdapter) Run(name string, args ...string) (string, error) {
	allArgs := append([]string{name}, args...)
	return a.runner.Run(allArgs...)
}

type Adapter struct {
	runner Runner
}

var publishAndVerifyPR = arcanum.PublishAndVerifyPR

var prURLPattern = regexp.MustCompile(`https?://[^\s"']*/review/[^\s"']+`)

func New(runner Runner) *Adapter {
	return &Adapter{runner: runner}
}

func (a *Adapter) EnsureMain(context.Context) error {
	return nil
}

func (a *Adapter) IsDirty(context.Context) (bool, error) {
	output, err := a.runner.Run("arc", "status", "--short")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(output) != "", nil
}

func (a *Adapter) CreateTaskBranch(_ context.Context, taskID string) (string, error) {
	branch := "task/" + taskID
	if _, err := a.runner.Run("arc", "checkout", "-b", branch); err != nil {
		if _, checkoutErr := a.runner.Run("arc", "checkout", branch); checkoutErr != nil {
			return "", errors.Join(err, checkoutErr)
		}
		return branch, nil
	}
	return branch, nil
}

func (a *Adapter) Checkout(_ context.Context, ref string) error {
	_, err := a.runner.Run("arc", "checkout", ref)
	return err
}

// CheckoutPRBranch returns a stable landing identity for a PR checkout. The
// working tree is already prepared by `arc pr checkout`; unlike Git, Arc's
// rev-parse does not support --abbrev-ref, and landing only needs a non-empty
// identity before committing and force-pushing the current checkout.
func (a *Adapter) CheckoutPRBranch(_ context.Context, prID string) (string, error) {
	prID = strings.TrimSpace(prID)
	if prID == "" {
		return "", fmt.Errorf("PR ID is required")
	}
	return "pr/" + prID, nil
}

func (a *Adapter) CommitAll(_ context.Context, message string) (string, error) {
	if err := a.stageAll(); err != nil {
		return "", err
	}
	// --no-verify: project pre-commit hooks (e.g. arctic-husky) expect their
	// toolchain installed in the working copy; runner-prepared per-PR mounts
	// are bare checkouts where the hook scripts don't even exist, so hooks
	// fail with exit 127 and block every landing. Server-side CI checks still
	// run on the pushed revision.
	if _, err := a.runArc("commit", "--no-verify", "-m", message); err != nil {
		if !isNoChangesCommitError(err) {
			return "", err
		}
	}
	sha, err := a.runArc("rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(sha), nil
}

func (a *Adapter) stageAll() error {
	status, err := a.runArc("status", "--short")
	if err != nil {
		return err
	}
	tracked := trackedStatusPaths(status)
	untracked := untrackedStatusPaths(status)

	// Stage with as few arc invocations as possible: per-path `arc add -u
	// <path>` repeatedly wedged inside the FUSE mount in production (hanging
	// until the command timeout killed it), while the pathless and batched
	// forms are arc's common path. Per-path staging remains only as a fallback
	// so a single unstageable path (e.g. deleted after status) skips instead of
	// aborting the landing.
	if len(tracked) > 0 {
		if _, err := a.runArc("add", "-u"); err != nil {
			if err := a.stagePathsIndividually(tracked, true); err != nil {
				return err
			}
		}
	}
	if len(untracked) > 0 {
		if _, err := a.runArc(append([]string{"add"}, untracked...)...); err != nil {
			if err := a.stagePathsIndividually(untracked, false); err != nil {
				return err
			}
		}
	}
	return nil
}

func (a *Adapter) stagePathsIndividually(paths []string, updateOnly bool) error {
	for _, path := range paths {
		args := []string{"add"}
		if updateOnly {
			args = append(args, "-u")
		}
		args = append(args, path)
		if _, err := a.runArc(args...); err != nil {
			if isMissingWorkingTreePathError(err) {
				continue
			}
			return err
		}
	}
	return nil
}

func (a *Adapter) CreatePR(_ context.Context, title string, body string) (string, error) {
	message := strings.TrimSpace(title)
	if trimmedBody := strings.TrimSpace(body); trimmedBody != "" {
		message += "\n\n" + trimmedBody
	}

	output, err := a.runArc("pr", "create", "-m", message, "--json", "--no-edit")
	if err != nil {
		return "", err
	}
	return parsePRURL(output)
}

func (a *Adapter) MergeToMain(context.Context, string) error {
	return nil
}

func (a *Adapter) PushBranch(context.Context, string) error {
	return nil
}

func (a *Adapter) PushMain(context.Context) error {
	return nil
}

// PushPRBranch force-pushes the current branch and publishes the resulting PR
// version. A bare force-push leaves an Arcanum draft version invisible to
// reviewers, so author-mode work is not complete until both operations succeed.
func (a *Adapter) PushPRBranch(ctx context.Context, prID string) error {
	prID = strings.TrimSpace(prID)
	if prID == "" {
		return fmt.Errorf("PR ID is required")
	}
	// Landable-content gate: push only when HEAD actually differs in CONTENT
	// from the remote PR branch. The checkout is rebased locally at prepare
	// time, so HEAD always has fresh SHAs; force-pushing a rebase-only state
	// publishes an Arcanum iteration with zero changes, which re-triggers
	// every automated reviewer watching the PR. A run whose agent produced no
	// fix must leave the remote PR untouched.
	if remoteRef := a.remotePRBranchRef(prID); remoteRef != "" {
		if diffOut, err := a.runArc("diff", "--stat", remoteRef, "HEAD"); err == nil && strings.TrimSpace(diffOut) == "" {
			return fmt.Errorf("no landable changes: HEAD has no content difference from %s (rebase-only state); refusing to publish an empty PR iteration", remoteRef)
		}
		// A diff failure is not a reason to brick the landing: fall through
		// and push.
	}
	if _, err := a.runArc("push", "-f"); err != nil {
		return err
	}
	head, err := a.runArc("rev-parse", "HEAD")
	if err != nil {
		return err
	}
	head = strings.TrimSpace(head)
	if head == "" {
		return fmt.Errorf("read PR %q head: empty revision", prID)
	}
	return publishAndVerifyPR(ctx, prID, func(_ context.Context, prID string) error {
		_, err := a.runArc("pr", "publish", prID)
		return err
	}, func(ctx context.Context, prID string) error {
		return arcanum.VerifyActiveDiffSetPublishedForRevision(ctx, prID, head)
	})
}

// remotePRBranchRef resolves the remote ref of the PR's source branch
// (arcadia/<from_branch>) for the landable-content comparison. Returns ""
// when the branch cannot be determined; callers then push unconditionally.
func (a *Adapter) remotePRBranchRef(prID string) string {
	statusOut, err := a.runArc("pr", "status", "--json", prID)
	if err != nil {
		return ""
	}
	details, err := arcanum.ParsePRDetailsJSON([]byte(statusOut))
	if err != nil {
		return ""
	}
	branch := strings.TrimSpace(details.SourceBranch)
	if branch == "" {
		return ""
	}
	return "arcadia/" + branch
}

func parsePRURL(output string) (string, error) {
	match := prURLPattern.FindString(strings.TrimSpace(output))
	if match == "" {
		return "", fmt.Errorf("arc pr create output did not contain Arcanum PR URL")
	}
	return match, nil
}

func (a *Adapter) runArc(args ...string) (string, error) {
	out, err := a.runner.Run("arc", args...)
	if err == nil {
		return out, nil
	}
	command := "arc " + strings.Join(args, " ")
	details := strings.TrimSpace(out)
	if details == "" {
		return "", fmt.Errorf("%s failed: %w", command, err)
	}
	return "", fmt.Errorf("%s failed: %s: %w", command, details, err)
}

func isNoChangesCommitError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "nothing to commit") ||
		strings.Contains(message, "no changes added to commit") ||
		strings.Contains(message, "no changes to commit") ||
		strings.Contains(message, "working tree clean")
}

func untrackedStatusPaths(status string) []string {
	return statusPaths(status, func(prefix string) bool { return prefix == "??" })
}

func trackedStatusPaths(status string) []string {
	return statusPaths(status, func(prefix string) bool {
		return prefix != "??" && strings.TrimSpace(prefix) != ""
	})
}

func statusPaths(status string, include func(string) bool) []string {
	var paths []string
	for _, line := range strings.Split(status, "\n") {
		line = strings.TrimRight(line, "\r")
		if len(line) < 4 || line[2] != ' ' || !include(line[:2]) {
			continue
		}
		if path := strings.TrimSpace(line[3:]); path != "" {
			paths = append(paths, path)
		}
	}
	return paths
}

func isMissingWorkingTreePathError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "no such file or directory")
}
