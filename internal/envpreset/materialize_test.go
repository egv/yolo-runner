package envpreset

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMaterializeCreatesWorkspaceForEachStrategyAndCleansUp(t *testing.T) {
	ctx := context.Background()
	itemID := "TASK-123"

	sourceRepo := initMaterializeGitRepo(t)
	gitWorkspace, err := Materialize(ctx, Preset{Workspace: Workspace{
		Strategy:   WorkspaceStrategyGitClone,
		Origin:     sourceRepo,
		BaseBranch: "main",
	}}, itemID)
	if err != nil {
		t.Fatalf("Materialize(git-clone) returned error: %v", err)
	}
	if gitWorkspace.Path == "" || gitWorkspace.Path == sourceRepo {
		t.Fatalf("expected isolated git clone path, got %q", gitWorkspace.Path)
	}
	if _, err := os.Stat(filepath.Join(gitWorkspace.Path, "README.md")); err != nil {
		t.Fatalf("expected cloned README.md: %v", err)
	}
	if gitWorkspace.VCS == nil {
		t.Fatal("expected git-clone materialization to return VCS")
	}
	if gitWorkspace.Cleanup == nil {
		t.Fatal("expected git-clone materialization to return cleanup")
	}
	if err := gitWorkspace.Cleanup(); err != nil {
		t.Fatalf("cleanup git workspace: %v", err)
	}
	if _, err := os.Stat(gitWorkspace.Path); !os.IsNotExist(err) {
		t.Fatalf("expected git clone to be removed, stat error = %v", err)
	}

	arcLog := installFakeArc(t)
	mount := t.TempDir()
	arcSubpath := filepath.Join("project", "service")
	arcPath := filepath.Join(mount, arcSubpath)
	if err := os.MkdirAll(arcPath, 0o755); err != nil {
		t.Fatalf("create arc workspace: %v", err)
	}
	arcWorkspace, err := Materialize(ctx, Preset{Workspace: Workspace{
		Strategy: WorkspaceStrategyArcShared,
		Mount:    mount,
		Subpath:  arcSubpath,
	}}, itemID)
	if err != nil {
		t.Fatalf("Materialize(arc-shared) returned error: %v", err)
	}
	if arcWorkspace.Path != arcPath {
		t.Fatalf("expected arc path %q, got %q", arcPath, arcWorkspace.Path)
	}
	if arcWorkspace.VCS == nil {
		t.Fatal("expected arc-shared materialization to return VCS")
	}
	if arcWorkspace.Cleanup == nil {
		t.Fatal("expected arc-shared materialization to return cleanup")
	}
	assertFileContains(t, arcLog, "checkout -b task/TASK-123")
	if err := arcWorkspace.Cleanup(); err != nil {
		t.Fatalf("cleanup arc workspace: %v", err)
	}

	pathWorkspaceDir := t.TempDir()
	pathWorkspace, err := Materialize(ctx, Preset{Workspace: Workspace{
		Strategy: WorkspaceStrategyPath,
		Path:     pathWorkspaceDir,
	}}, itemID)
	if err != nil {
		t.Fatalf("Materialize(path) returned error: %v", err)
	}
	if pathWorkspace.Path != pathWorkspaceDir {
		t.Fatalf("expected path workspace %q, got %q", pathWorkspaceDir, pathWorkspace.Path)
	}
	if pathWorkspace.VCS != nil {
		t.Fatal("expected path materialization to be read-only without VCS")
	}
	if pathWorkspace.Cleanup == nil {
		t.Fatal("expected path materialization to return cleanup")
	}
	if err := pathWorkspace.Cleanup(); err != nil {
		t.Fatalf("cleanup path workspace: %v", err)
	}
	if _, err := os.Stat(pathWorkspaceDir); err != nil {
		t.Fatalf("path workspace should remain in place: %v", err)
	}
}

func initMaterializeGitRepo(t *testing.T) string {
	t.Helper()

	repo := t.TempDir()
	runMaterializeCommand(t, repo, "git", "init", "-b", "main")
	runMaterializeCommand(t, repo, "git", "config", "user.email", "test@example.com")
	runMaterializeCommand(t, repo, "git", "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("test repo\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runMaterializeCommand(t, repo, "git", "add", "README.md")
	runMaterializeCommand(t, repo, "git", "commit", "-m", "initial")
	return repo
}

func installFakeArc(t *testing.T) string {
	t.Helper()

	binDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "arc.log")
	script := filepath.Join(binDir, "arc")
	content := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> " + shellQuote(logPath) + "\n" +
		"exit 0\n"
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake arc: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

func runMaterializeCommand(t *testing.T, dir string, name string, args ...string) {
	t.Helper()

	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s failed: %s: %v", name, strings.Join(args, " "), strings.TrimSpace(string(output)), err)
	}
}

func assertFileContains(t *testing.T, path string, want string) {
	t.Helper()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !strings.Contains(string(content), want) {
		t.Fatalf("expected %s to contain %q, got %q", path, want, string(content))
	}
}

func shellQuote(path string) string {
	return "'" + strings.ReplaceAll(path, "'", "'\\''") + "'"
}
