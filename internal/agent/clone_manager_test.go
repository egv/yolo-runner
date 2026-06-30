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
	"testing"
)

func TestGitCloneManagerClonesRepoPerTaskAndCleansUp(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}

	repoRoot := t.TempDir()
	runGit(t, repoRoot, "init")
	readmePath := filepath.Join(repoRoot, "README.md")
	if err := os.WriteFile(readmePath, []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runGit(t, repoRoot, "add", "README.md")
	runGit(t, repoRoot, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "init")

	manager := NewGitCloneManager(t.TempDir())
	clonePath, err := manager.CloneForTask(context.Background(), "t-1", repoRoot)
	if err != nil {
		t.Fatalf("clone failed: %v", err)
	}
	if clonePath == repoRoot {
		t.Fatalf("expected isolated clone path, got source path %q", clonePath)
	}
	if _, err := os.Stat(filepath.Join(clonePath, ".git")); err != nil {
		t.Fatalf("expected git metadata in clone: %v", err)
	}
	if _, err := os.Stat(filepath.Join(clonePath, "README.md")); err != nil {
		t.Fatalf("expected tracked file in clone: %v", err)
	}

	if err := manager.Cleanup("t-1"); err != nil {
		t.Fatalf("cleanup failed: %v", err)
	}
	if _, err := os.Stat(clonePath); !os.IsNotExist(err) {
		t.Fatalf("expected clone path removed, got err=%v", err)
	}
}

func TestGitCloneManagerSetsCloneOriginToSourceUpstream(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}

	remoteRoot := t.TempDir()
	remotePath := filepath.Join(remoteRoot, "remote.git")
	runGit(t, remoteRoot, "init", "--bare", remotePath)

	repoRoot := t.TempDir()
	runGit(t, repoRoot, "init")
	readmePath := filepath.Join(repoRoot, "README.md")
	if err := os.WriteFile(readmePath, []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runGit(t, repoRoot, "add", "README.md")
	runGit(t, repoRoot, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "init")
	runGit(t, repoRoot, "remote", "add", "origin", remotePath)

	manager := NewGitCloneManager(t.TempDir())
	clonePath, err := manager.CloneForTask(context.Background(), "t-remote", repoRoot)
	if err != nil {
		t.Fatalf("clone failed: %v", err)
	}
	defer func() { _ = manager.Cleanup("t-remote") }()

	originURL := strings.TrimSpace(runGitOutput(t, clonePath, "remote", "get-url", "origin"))
	if originURL != remotePath {
		t.Fatalf("expected clone origin=%q, got %q", remotePath, originURL)
	}
}

func TestGitCloneManagerCreatesIsolatedParallelClones(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}

	repoRoot := t.TempDir()
	runGit(t, repoRoot, "init")
	readmePath := filepath.Join(repoRoot, "README.md")
	if err := os.WriteFile(readmePath, []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runGit(t, repoRoot, "add", "README.md")
	runGit(t, repoRoot, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "init")

	baseDir := t.TempDir()
	manager := NewGitCloneManager(baseDir)

	type cloneResult struct {
		taskID string
		path   string
		err    error
	}

	const taskCount = 4
	results := make(chan cloneResult, taskCount)
	var wg sync.WaitGroup
	for i := 1; i <= taskCount; i++ {
		wg.Add(1)
		taskID := fmt.Sprintf("task-%d", i)
		go func(taskID string) {
			defer wg.Done()
			path, err := manager.CloneForTask(context.Background(), taskID, repoRoot)
			results <- cloneResult{taskID: taskID, path: path, err: err}
		}(taskID)
	}
	wg.Wait()
	close(results)

	clonePaths := map[string]string{}
	for result := range results {
		if result.err != nil {
			t.Fatalf("parallel clone failed: %v", result.err)
		}
		if result.path == repoRoot {
			t.Fatalf("expected isolated clone path, got source path %q", result.path)
		}
		if _, ok := clonePaths[result.path]; ok {
			t.Fatalf("expected unique clone path per task, got shared path %q", result.path)
		}
		clonePaths[result.path] = result.taskID

		if _, err := os.Stat(filepath.Join(result.path, ".git")); err != nil {
			t.Fatalf("expected git metadata in clone for task %q: %v", result.taskID, err)
		}
	}
	if len(clonePaths) != taskCount {
		t.Fatalf("expected %d clone paths, got %d", taskCount, len(clonePaths))
	}

	entries, err := os.ReadDir(baseDir)
	if err != nil {
		t.Fatalf("read clone base dir: %v", err)
	}
	if len(entries) != taskCount {
		t.Fatalf("expected %d clone directories, got %d", taskCount, len(entries))
	}

	for _, taskID := range []string{"task-1", "task-2", "task-3", "task-4"} {
		if err := manager.Cleanup(taskID); err != nil {
			t.Fatalf("cleanup for %q failed: %v", taskID, err)
		}
	}
	for path := range clonePaths {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("expected clone path removed for %q, got err=%v", path, err)
		}
	}
	entries, err = os.ReadDir(baseDir)
	if err != nil {
		t.Fatalf("read clone base dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected clone base dir to be empty after cleanup, got %d entries", len(entries))
	}
}

func TestGitCloneManagerWritesBypassPermissionsAndInheritsSourceEnv(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}

	repoRoot := t.TempDir()
	runGit(t, repoRoot, "init")
	if err := os.WriteFile(filepath.Join(repoRoot, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runGit(t, repoRoot, "add", "README.md")
	runGit(t, repoRoot, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "init")

	// Source repo carries z.ai auth in .claude/_settings.json (the deliberately
	// renamed, untracked form). The clone must inherit its env block so claude
	// authenticates without relying on the launcher's environment.
	if err := os.MkdirAll(filepath.Join(repoRoot, ".claude"), 0o755); err != nil {
		t.Fatalf("mkdir source .claude: %v", err)
	}
	srcSettings := `{"env":{"ANTHROPIC_AUTH_TOKEN":"tok-123","ANTHROPIC_BASE_URL":"https://api.z.ai/api/anthropic"}}`
	if err := os.WriteFile(filepath.Join(repoRoot, ".claude", "_settings.json"), []byte(srcSettings), 0o644); err != nil {
		t.Fatalf("write source settings: %v", err)
	}

	manager := NewGitCloneManager(t.TempDir())
	clonePath, err := manager.CloneForTask(context.Background(), "t-settings", repoRoot)
	if err != nil {
		t.Fatalf("clone failed: %v", err)
	}
	defer func() { _ = manager.Cleanup("t-settings") }()

	settings := readCloneSettings(t, clonePath)
	perms, _ := settings["permissions"].(map[string]any)
	if perms == nil || perms["defaultMode"] != "bypassPermissions" {
		t.Fatalf("expected permissions.defaultMode=bypassPermissions, got %#v", settings["permissions"])
	}
	env, _ := settings["env"].(map[string]any)
	if env == nil || env["ANTHROPIC_AUTH_TOKEN"] != "tok-123" || env["ANTHROPIC_BASE_URL"] != "https://api.z.ai/api/anthropic" {
		t.Fatalf("expected inherited z.ai env, got %#v", settings["env"])
	}
}

func TestGitCloneManagerPreservesCommittedCloneSettings(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}

	repoRoot := t.TempDir()
	runGit(t, repoRoot, "init")
	// A committed .claude/settings.json with a custom key the clone must keep.
	if err := os.MkdirAll(filepath.Join(repoRoot, ".claude"), 0o755); err != nil {
		t.Fatalf("mkdir .claude: %v", err)
	}
	committed := `{"permissions":{"defaultMode":"acceptEdits"},"customKey":"keep-me"}`
	if err := os.WriteFile(filepath.Join(repoRoot, ".claude", "settings.json"), []byte(committed), 0o644); err != nil {
		t.Fatalf("write committed settings: %v", err)
	}
	runGit(t, repoRoot, "add", "-A")
	runGit(t, repoRoot, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "init")

	manager := NewGitCloneManager(t.TempDir())
	clonePath, err := manager.CloneForTask(context.Background(), "t-preserve", repoRoot)
	if err != nil {
		t.Fatalf("clone failed: %v", err)
	}
	defer func() { _ = manager.Cleanup("t-preserve") }()

	settings := readCloneSettings(t, clonePath)
	if settings["customKey"] != "keep-me" {
		t.Fatalf("expected committed customKey preserved, got %#v", settings["customKey"])
	}
	// bypassPermissions is forced even over a committed defaultMode.
	perms, _ := settings["permissions"].(map[string]any)
	if perms == nil || perms["defaultMode"] != "bypassPermissions" {
		t.Fatalf("expected forced bypassPermissions, got %#v", settings["permissions"])
	}
}

func TestGitCloneManagerExcludesClaudeDirFromGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}

	repoRoot := t.TempDir()
	runGit(t, repoRoot, "init")
	if err := os.WriteFile(filepath.Join(repoRoot, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runGit(t, repoRoot, "add", "README.md")
	runGit(t, repoRoot, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "init")

	manager := NewGitCloneManager(t.TempDir())
	clonePath, err := manager.CloneForTask(context.Background(), "t-exclude", repoRoot)
	if err != nil {
		t.Fatalf("clone failed: %v", err)
	}
	defer func() { _ = manager.Cleanup("t-exclude") }()

	// Verify .git/info/exclude contains .claude/
	excludePath := filepath.Join(clonePath, ".git", "info", "exclude")
	data, err := os.ReadFile(excludePath)
	if err != nil {
		t.Fatalf("read .git/info/exclude: %v", err)
	}
	if !strings.Contains(string(data), ".claude/") {
		t.Fatalf("expected .git/info/exclude to contain .claude/, got: %s", string(data))
	}

	// Verify that a .claude/settings.json file is not considered untracked
	// (i.e., it's actually excluded)
	if err := os.MkdirAll(filepath.Join(clonePath, ".claude"), 0o755); err != nil {
		t.Fatalf("mkdir .claude: %v", err)
	}
	if err := os.WriteFile(filepath.Join(clonePath, ".claude", "settings.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("write .claude/settings.json: %v", err)
	}
	// git status --porcelain should not list .claude/settings.json
	out := runGitOutput(t, clonePath, "status", "--porcelain")
	if strings.Contains(out, ".claude") {
		t.Fatalf("expected .claude/ to be excluded from git, but git status reported: %s", out)
	}
}

func readCloneSettings(t *testing.T, clonePath string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(clonePath, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("read clone settings: %v", err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("parse clone settings: %v", err)
	}
	return settings
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v (%s)", args, err, string(out))
	}
}

func runGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v (%s)", args, err, string(out))
	}
	return string(out)
}
