package git

import (
	"github.com/egv/yolo-runner/internal/exec"
)

// GitCommandAdapter adapts exec.CommandRunner to the git Runner interface
type GitCommandAdapter struct {
	runner *exec.CommandRunner
}

func NewGitCommandAdapter(runner *exec.CommandRunner) *GitCommandAdapter {
	return &GitCommandAdapter{
		runner: runner,
	}
}

func (g *GitCommandAdapter) Run(name string, args ...string) (string, error) {
	allArgs := append([]string{name}, args...)
	return g.runner.Run(allArgs...)
}
