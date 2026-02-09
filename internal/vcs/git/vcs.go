package git

import (
	"context"
	"errors"
	"strings"
)

type VCSAdapter struct {
	runner Runner
}

func NewVCSAdapter(runner Runner) *VCSAdapter {
	return &VCSAdapter{runner: runner}
}

func (a *VCSAdapter) EnsureMain(context.Context) error {
	_, err := a.runner.Run("git", "checkout", "main")
	return err
}

func (a *VCSAdapter) CreateTaskBranch(ctx context.Context, taskID string) (string, error) {
	branch := "task/" + taskID
	if err := a.EnsureMain(ctx); err != nil {
		return "", err
	}
	if _, err := a.runner.Run("git", "checkout", "-b", branch); err != nil {
		if _, checkoutErr := a.runner.Run("git", "checkout", branch); checkoutErr != nil {
			return "", errors.Join(err, checkoutErr)
		}
		return branch, nil
	}
	return branch, nil
}

func (a *VCSAdapter) Checkout(_ context.Context, ref string) error {
	_, err := a.runner.Run("git", "checkout", ref)
	return err
}

func (a *VCSAdapter) CommitAll(_ context.Context, message string) (string, error) {
	return a.commitAll(message)
}

func (a *VCSAdapter) MergeToMain(ctx context.Context, sourceBranch string) error {
	if err := a.EnsureMain(ctx); err != nil {
		return err
	}
	_, err := a.runner.Run("git", "merge", "--no-ff", sourceBranch)
	return err
}

func (a *VCSAdapter) PushBranch(_ context.Context, branch string) error {
	_, err := a.runner.Run("git", "push", "-u", "origin", branch)
	return err
}

func (a *VCSAdapter) PushMain(context.Context) error {
	_, err := a.runner.Run("git", "push", "origin", "main")
	return err
}

func (a *VCSAdapter) commitAll(message string) (string, error) {
	if _, err := a.runner.Run("git", "add", "."); err != nil {
		return "", err
	}
	if _, err := a.runner.Run("git", "commit", "-m", message); err != nil {
		return "", err
	}
	sha, err := a.runner.Run("git", "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(sha), nil
}
