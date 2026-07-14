package arcanum

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type PRCheckout struct {
	MountPath      string
	Cleanup        func() error
	CleanupContext func(context.Context) error
}

type PRCheckoutConfig struct {
	ObjectsBaseDir string
	MountsBaseDir  string
	// Rebase rebases the checked-out PR head onto its target branch and
	// force-pushes the result before returning the checkout to the caller.
	Rebase bool
}

var publishAndVerifyPRCheckout = PublishAndVerifyPR

func PreparePRCheckout(prID string) (*PRCheckout, error) {
	return PreparePRCheckoutWithConfig(context.Background(), prID, PRCheckoutConfig{})
}

func PreparePRCheckoutWithConfig(ctx context.Context, prID string, cfg PRCheckoutConfig) (*PRCheckout, error) {
	return preparePRCheckout(ctx, prID, cfg)
}

func preparePRCheckout(ctx context.Context, prID string, cfg PRCheckoutConfig) (*PRCheckout, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	prID = strings.TrimSpace(prID)
	if prID == "" {
		return nil, fmt.Errorf("PR ID is required")
	}
	if prID == "." || prID == ".." || strings.ContainsAny(prID, `/\`) {
		return nil, fmt.Errorf("PR ID %q cannot be used as a mount path name", prID)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home directory: %w", err)
	}

	root := filepath.Join(home, ".yolo-runner")
	objectsBaseDir := strings.TrimSpace(cfg.ObjectsBaseDir)
	if objectsBaseDir == "" {
		objectsBaseDir = filepath.Join(root, "pr-objects")
	}
	mountsBaseDir := strings.TrimSpace(cfg.MountsBaseDir)
	if mountsBaseDir == "" {
		mountsBaseDir = filepath.Join(root, "pr-mounts")
	}
	// Each PR gets its own object store and mount point. A single shared store
	// cannot serve multiple PRs: arc binds a store to the first repository it
	// mounts and rejects subsequent PRs from other branches ("store was
	// previously mounted into different repository"), and a store can only be
	// mounted at one location at a time. Per-PR stores make checkouts fully
	// isolated and parallel-safe.
	objectStore := filepath.Join(objectsBaseDir, prID)
	mountPath := filepath.Join(mountsBaseDir, prID)
	releaseCheckout := acquirePRCheckoutLock(mountPath)
	releaseOnReturn := true
	defer func() {
		if releaseOnReturn {
			releaseCheckout()
		}
	}()

	// A crashed runner leaves a dead FUSE mount ("Device not configured") or a
	// damaged object store behind; neither self-heals, so a stale-state failure
	// gets one full reset (force unmount, drop mount dir and store) and retry
	// before the task is declared failed.
	err = mountAndCheckoutPR(ctx, prID, cfg, objectStore, mountPath)
	if err != nil {
		resetPRCheckoutState(mountPath, objectStore)
		if arcStaleCheckoutError(err) {
			err = mountAndCheckoutPR(ctx, prID, cfg, objectStore, mountPath)
			if err != nil {
				resetPRCheckoutState(mountPath, objectStore)
			}
		}
	}
	if err != nil {
		return nil, err
	}

	cleanup := oncePRCheckoutCleanup(func(ctx context.Context) error {
		defer releaseCheckout()
		unmountArgs := []string{"unmount", "--force", "--forget", mountPath}
		if _, stderr, err := arcExec(ctx, "", "arc", unmountArgs...); err != nil {
			return workspaceArcError(mountPath, unmountArgs, stderr, err)
		}
		if err := os.RemoveAll(mountPath); err != nil {
			return fmt.Errorf("remove arc PR mount %s: %w", mountPath, err)
		}
		if err := os.RemoveAll(objectStore); err != nil {
			return fmt.Errorf("remove arc PR object store %s: %w", objectStore, err)
		}
		return nil
	})
	checkout := &PRCheckout{
		MountPath:      mountPath,
		CleanupContext: cleanup,
		Cleanup: func() error {
			return cleanup(context.Background())
		},
	}
	releaseOnReturn = false
	return checkout, nil
}

// mountAndCheckoutPR runs one full preparation attempt: fresh mount dir,
// arc mount, PR checkout, and the optional rebase-and-publish pass.
func mountAndCheckoutPR(ctx context.Context, prID string, cfg PRCheckoutConfig, objectStore string, mountPath string) error {
	if err := os.MkdirAll(objectStore, 0o755); err != nil {
		return fmt.Errorf("create arc object store %s: %w", objectStore, err)
	}
	// arc mounts onto an existing empty dir; start clean when no prior runner
	// process owns this path. RemoveAll cannot remove an active Arc mount, so a
	// later "already mounted" response is handled by reusing that checkout.
	_ = os.RemoveAll(mountPath)
	if err := os.MkdirAll(mountPath, 0o755); err != nil {
		return fmt.Errorf("create arc PR mount %s: %w", mountPath, err)
	}

	// A fresh isolated arc working copy per PR: `arc mount -m <mount> -S <store>`
	// (verified against the arc CLI; `arc init` is for bare/path-filtered repos
	// and rejects this form).
	mountArgs := []string{"mount", "-m", mountPath, "-S", objectStore}
	if _, stderr, err := arcExec(ctx, "", "arc", mountArgs...); err != nil {
		if !arcMountAlreadyMounted(stderr) {
			return workspaceArcError("", mountArgs, stderr, err)
		}
	}

	// Keep the PR branch attached: `arc push -f` from a detached checkout only
	// uploads a dangling commit and leaves the pull request unchanged. The
	// checkout also doubles as liveness verification for a reused mount: a dead
	// FUSE mount fails here with a stale-state error and triggers the reset.
	checkoutArgs := []string{"pr", "checkout", prID, "--force"}
	if _, stderr, err := arcExec(ctx, mountPath, "arc", checkoutArgs...); err != nil {
		return workspaceArcError(mountPath, checkoutArgs, stderr, err)
	}
	if cfg.Rebase {
		return rebasePRCheckout(ctx, mountPath, prID)
	}
	return nil
}

// resetPRCheckoutState best-effort releases everything a previous attempt may
// have left behind: the FUSE mount (dead or alive), the mount dir, and the
// per-PR object store. Uses a background context so cleanup still runs when
// the caller's context is already cancelled.
func resetPRCheckoutState(mountPath string, objectStore string) {
	_, _, _ = arcExec(context.Background(), "", "arc", "unmount", "--force", "--forget", mountPath)
	_ = os.RemoveAll(mountPath)
	_ = os.RemoveAll(objectStore)
}

// arcStaleCheckoutError reports whether a preparation failure looks like
// leftover mount/store damage from an earlier abnormal termination — the cases
// where wiping the mount and object store and retrying can succeed, as opposed
// to genuine failures (rebase conflicts, unknown PR, auth) that would only
// repeat.
func arcStaleCheckoutError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	staleMarkers := []string{
		"device not configured",
		"transport endpoint is not connected",
		"not a mounted arc repository",
		"abnormal mount termination",
		"previously mounted into different repository",
		"failed to opendir",
		"can't open",
	}
	for _, marker := range staleMarkers {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func arcMountAlreadyMounted(stderr []byte) bool {
	return strings.Contains(strings.ToLower(string(stderr)), "already mounted")
}

func rebasePRCheckout(ctx context.Context, mountPath string, prID string) error {
	statusArgs := []string{"pr", "status", "--json", prID}
	statusOutput, stderr, err := arcExec(ctx, mountPath, "arc", statusArgs...)
	if err != nil {
		return workspaceArcError(mountPath, statusArgs, stderr, err)
	}
	details, err := ParsePRDetailsJSON(statusOutput)
	if err != nil {
		return fmt.Errorf("parse PR %q status before rebase: %w", prID, err)
	}
	targetBranch := strings.TrimSpace(details.TargetBranch)
	if targetBranch == "" {
		return fmt.Errorf("PR %q target branch is required before rebase", prID)
	}

	rebaseArgs := []string{"rebase", targetBranch}
	if _, stderr, err := arcExec(ctx, mountPath, "arc", rebaseArgs...); err != nil {
		return workspaceArcError(mountPath, rebaseArgs, stderr, err)
	}
	pushArgs := []string{"push", "-f"}
	if _, stderr, err := arcExec(ctx, mountPath, "arc", pushArgs...); err != nil {
		return workspaceArcError(mountPath, pushArgs, stderr, err)
	}
	headArgs := []string{"rev-parse", "HEAD"}
	headOutput, stderr, err := arcExec(ctx, mountPath, "arc", headArgs...)
	if err != nil {
		return workspaceArcError(mountPath, headArgs, stderr, err)
	}
	head := strings.TrimSpace(string(headOutput))
	if head == "" {
		return fmt.Errorf("read rebased PR %q head: empty revision", prID)
	}
	return publishAndVerifyPRCheckout(ctx, prID, func(ctx context.Context, prID string) error {
		publishArgs := []string{"pr", "publish", prID}
		if _, stderr, err := arcExec(ctx, mountPath, "arc", publishArgs...); err != nil {
			return workspaceArcError(mountPath, publishArgs, stderr, err)
		}
		return nil
	}, func(ctx context.Context, prID string) error {
		return VerifyActiveDiffSetPublishedForRevision(ctx, prID, head)
	})
}

var prCheckoutLocks sync.Map

func acquirePRCheckoutLock(mountPath string) func() {
	value, _ := prCheckoutLocks.LoadOrStore(mountPath, &sync.Mutex{})
	lock := value.(*sync.Mutex)
	lock.Lock()
	return lock.Unlock
}

func oncePRCheckoutCleanup(cleanup func(context.Context) error) func(context.Context) error {
	var once sync.Once
	var cleanupErr error
	return func(ctx context.Context) error {
		if ctx == nil {
			ctx = context.Background()
		}
		once.Do(func() {
			cleanupErr = cleanup(ctx)
		})
		return cleanupErr
	}
}
