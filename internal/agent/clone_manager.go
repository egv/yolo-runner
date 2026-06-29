package agent

import (
	"context"
	"encoding/json"
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
	if err := writeCloneClaudeSettings(clonePath, repoRoot); err != nil {
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

// writeCloneClaudeSettings makes a clone self-contained for the claude backend:
// it forces permissions.defaultMode=bypassPermissions (the version-independent
// replacement for the dead --dangerously-skip-permissions flag) and inherits the
// source repo's z.ai auth env block, while preserving any committed clone settings.
func writeCloneClaudeSettings(clonePath string, repoRoot string) error {
	target := filepath.Join(clonePath, ".claude", "settings.json")

	settings := map[string]any{}
	// Base: a committed .claude/settings.json carried in by the clone, if any.
	if data, err := os.ReadFile(target); err == nil {
		_ = json.Unmarshal(data, &settings)
	}

	// Inherit the source repo's auth env (settings.json or the renamed _settings.json).
	if env := sourceClaudeEnv(repoRoot); len(env) > 0 {
		merged, _ := settings["env"].(map[string]any)
		if merged == nil {
			merged = map[string]any{}
		}
		for key, value := range env {
			if _, exists := merged[key]; !exists {
				merged[key] = value
			}
		}
		settings["env"] = merged
	}

	// Force bypassPermissions so runs don't wedge on permission prompts.
	perms, _ := settings["permissions"].(map[string]any)
	if perms == nil {
		perms = map[string]any{}
	}
	perms["defaultMode"] = "bypassPermissions"
	settings["permissions"] = perms

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(target, data, 0o644)
}

// sourceClaudeEnv reads the env block from the source repo's claude settings.
// It prefers .claude/settings.json and falls back to .claude/_settings.json (the
// deliberately renamed, untracked form used so the host session does not inherit
// the z.ai endpoint).
func sourceClaudeEnv(repoRoot string) map[string]any {
	for _, name := range []string{"settings.json", "_settings.json"} {
		data, err := os.ReadFile(filepath.Join(repoRoot, ".claude", name))
		if err != nil {
			continue
		}
		var parsed map[string]any
		if json.Unmarshal(data, &parsed) != nil {
			continue
		}
		if env, ok := parsed["env"].(map[string]any); ok && len(env) > 0 {
			return env
		}
	}
	return nil
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
