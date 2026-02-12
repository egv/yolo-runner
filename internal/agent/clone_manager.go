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
	if err := setCloneOriginToSourceOrigin(ctx, repoRoot, clonePath); err != nil {
		return "", err
	}

	m.mu.Lock()
	m.clones[taskID] = clonePath
	m.mu.Unlock()

	return clonePath, nil
}

func setCloneOriginToSourceOrigin(ctx context.Context, repoRoot string, clonePath string) error {
	originURL, err := sourceOriginURL(ctx, repoRoot)
	if err != nil {
		return err
	}
	if originURL == "" {
		return nil
	}
	cmd := exec.CommandContext(ctx, "git", "-C", clonePath, "remote", "set-url", "origin", originURL)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git remote set-url origin failed: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}

func sourceOriginURL(ctx context.Context, repoRoot string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", repoRoot, "remote", "get-url", "origin")
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Repositories used in tests or local bootstrap may not have origin configured.
		return "", nil
	}
	return strings.TrimSpace(string(output)), nil
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
