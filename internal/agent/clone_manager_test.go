package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
