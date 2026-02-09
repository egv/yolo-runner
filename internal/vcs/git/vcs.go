package git

import (
	"context"
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
		return "", err
	}
	return branch, nil
}

func (a *VCSAdapter) Checkout(context.Context, string) error {
	// Branch switch strategy is explicit through EnsureMain/CreateTaskBranch in v2.
	return nil
}

func (a *VCSAdapter) CommitAll(context.Context, string) (string, error) {
	return "", nil
}

func (a *VCSAdapter) MergeToMain(context.Context, string) error {
	return nil
}

func (a *VCSAdapter) PushBranch(context.Context, string) error {
	return nil
}

func (a *VCSAdapter) PushMain(context.Context) error {
	return nil
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
