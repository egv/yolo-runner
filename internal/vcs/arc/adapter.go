package arc

import (
	"context"
	"errors"
	"strings"
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

func New(runner Runner) *Adapter {
	return &Adapter{runner: runner}
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
