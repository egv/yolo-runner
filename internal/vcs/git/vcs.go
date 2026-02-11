package git

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

type VCSAdapter struct {
	runner Runner
}

func NewVCSAdapter(runner Runner) *VCSAdapter {
	return &VCSAdapter{runner: runner}
}

func (a *VCSAdapter) EnsureMain(context.Context) error {
	if _, err := a.runGit("checkout", "main"); err != nil {
		return err
	}
	_, err := a.runGit("pull", "--ff-only", "origin", "main")
	return err
}

func (a *VCSAdapter) CreateTaskBranch(ctx context.Context, taskID string) (string, error) {
	branch := "task/" + taskID
	if err := a.EnsureMain(ctx); err != nil {
		return "", err
	}
	if _, err := a.runGit("checkout", "-b", branch); err != nil {
		if _, checkoutErr := a.runGit("checkout", branch); checkoutErr != nil {
			return "", errors.Join(err, checkoutErr)
		}
		return branch, nil
	}
	return branch, nil
}

func (a *VCSAdapter) Checkout(_ context.Context, ref string) error {
	_, err := a.runGit("checkout", ref)
	return err
}

func (a *VCSAdapter) CommitAll(_ context.Context, message string) (string, error) {
	return a.commitAll(message)
}

func (a *VCSAdapter) MergeToMain(ctx context.Context, sourceBranch string) error {
	if err := a.EnsureMain(ctx); err != nil {
		return err
	}
	_, err := a.runGit("merge", "--no-ff", sourceBranch)
	return err
}

func (a *VCSAdapter) PushBranch(_ context.Context, branch string) error {
	_, err := a.runGit("push", "-u", "origin", branch)
	return err
}

func (a *VCSAdapter) PushMain(context.Context) error {
	_, err := a.runGit("push", "origin", "main")
	return err
}

func (a *VCSAdapter) commitAll(message string) (string, error) {
	if _, err := a.runGit("add", "."); err != nil {
		return "", err
	}
	if _, err := a.runGit("commit", "-m", message); err != nil {
		return "", err
	}
	sha, err := a.runGit("rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(sha), nil
}

func (a *VCSAdapter) runGit(args ...string) (string, error) {
	out, err := a.runner.Run("git", args...)
	if err == nil {
		return out, nil
	}
	command := "git " + strings.Join(args, " ")
	details := strings.TrimSpace(out)
	if details == "" {
		return "", fmt.Errorf("%s failed: %w", command, err)
	}
	return "", fmt.Errorf("%s failed: %s: %w", command, details, err)
}
