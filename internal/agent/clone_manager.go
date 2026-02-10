package agent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

type GitCloneManager struct {
	baseDir string

	mu     sync.Mutex
	clones map[string]string
}

func NewGitCloneManager(baseDir string) *GitCloneManager {
	if strings.TrimSpace(baseDir) == "" {
		baseDir = filepath.Join(os.TempDir(), "yolo-runner-clones")
	}
	return &GitCloneManager{
		baseDir: baseDir,
		clones:  map[string]string{},
	}
}

func (m *GitCloneManager) CloneForTask(ctx context.Context, taskID string, repoRoot string) (string, error) {
	if strings.TrimSpace(repoRoot) == "" {
		return "", fmt.Errorf("repo root is required")
	}
	if err := os.MkdirAll(m.baseDir, 0o755); err != nil {
		return "", err
	}
	clonePath := filepath.Join(m.baseDir, taskID)
	if err := os.RemoveAll(clonePath); err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, "git", "clone", "--no-hardlinks", repoRoot, clonePath)
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git clone failed: %s: %w", strings.TrimSpace(string(output)), err)
	}

	m.mu.Lock()
	m.clones[taskID] = clonePath
	m.mu.Unlock()

	return clonePath, nil
}

func (m *GitCloneManager) Cleanup(taskID string) error {
	m.mu.Lock()
	clonePath := m.clones[taskID]
	delete(m.clones, taskID)
	m.mu.Unlock()

	if clonePath == "" {
		clonePath = filepath.Join(m.baseDir, taskID)
	}
	return os.RemoveAll(clonePath)
}
