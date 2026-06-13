package envpreset

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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

func TestMaterializeArcMountIfNeededBeforeBranchCreation(t *testing.T) {
	ctx := context.Background()
	itemID := "TASK-123"
	arcLog := installFakeArcWithMountList(t, "[]")
	mount := t.TempDir()
	arcSubpath := filepath.Join("project", "service")
	arcPath := filepath.Join(mount, arcSubpath)
	if err := os.MkdirAll(arcPath, 0o755); err != nil {
		t.Fatalf("create arc workspace: %v", err)
	}

	workspace, err := Materialize(ctx, Preset{Workspace: Workspace{
		Strategy: WorkspaceStrategyArcShared,
		Mount:    mount,
		Subpath:  arcSubpath,
	}}, itemID)
	if err != nil {
		t.Fatalf("Materialize(arc-shared) returned error: %v", err)
	}
	if workspace.Cleanup == nil {
		t.Fatal("expected cleanup")
	}
	if err := workspace.Cleanup(); err != nil {
		t.Fatalf("cleanup arc workspace: %v", err)
	}

	content, err := os.ReadFile(arcLog)
	if err != nil {
		t.Fatalf("read arc log: %v", err)
	}
	got := strings.Split(strings.TrimSpace(string(content)), "\n")
	want := []string{
		"mount -l --json",
		"mount -m " + mount,
		"checkout -b task/TASK-123",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("unexpected arc command order:\n got %q\nwant %q", got, want)
	}
}

func TestMaterializeArcMountConcurrentColdStartDoesNotRaceBeforeBranchCreation(t *testing.T) {
	ctx := context.Background()
	arcLog := installRacingFakeArc(t)
	mount := t.TempDir()
	arcSubpath := filepath.Join("project", "service")
	arcPath := filepath.Join(mount, arcSubpath)
	if err := os.MkdirAll(arcPath, 0o755); err != nil {
		t.Fatalf("create arc workspace: %v", err)
	}
	preset := Preset{Workspace: Workspace{
		Strategy: WorkspaceStrategyArcShared,
		Mount:    mount,
		Subpath:  arcSubpath,
	}}

	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, itemID := range []string{"TASK-A", "TASK-B"} {
		itemID := itemID
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			workspace, err := Materialize(ctx, preset, itemID)
			if err != nil {
				errs <- err
				return
			}
			if workspace.Cleanup == nil {
				errs <- errors.New("expected cleanup")
				return
			}
			errs <- workspace.Cleanup()
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Materialize(arc-shared) returned error: %v", err)
		}
	}

	content, err := os.ReadFile(arcLog)
	if err != nil {
		t.Fatalf("read arc log: %v", err)
	}
	log := string(content)
	if strings.Contains(log, "mount-race") {
		t.Fatalf("arc mount raced before branch creation:\n%s", log)
	}
	if got := strings.Count(log, "mount -m "+mount+"\n"); got != 1 {
		t.Fatalf("expected exactly one cold arc mount, got %d:\n%s", got, log)
	}
	for _, branch := range []string{"task/TASK-A", "task/TASK-B"} {
		if !strings.Contains(log, "checkout -b "+branch+"\n") {
			t.Fatalf("expected branch creation for %s, got log:\n%s", branch, log)
		}
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
		"if [ \"$1\" = \"mount\" ] && [ \"$2\" = \"-l\" ] && [ \"$3\" = \"--json\" ]; then\n" +
		"  printf '[]\\n'\n" +
		"fi\n" +
		"exit 0\n"
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake arc: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

func installFakeArcWithMountList(t *testing.T, mountList string) string {
	t.Helper()

	binDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "arc.log")
	script := filepath.Join(binDir, "arc")
	content := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> " + shellQuote(logPath) + "\n" +
		"if [ \"$1\" = \"mount\" ] && [ \"$2\" = \"-l\" ] && [ \"$3\" = \"--json\" ]; then\n" +
		"  printf '%s\\n' " + shellQuote(mountList) + "\n" +
		"fi\n" +
		"exit 0\n"
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake arc: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

func installRacingFakeArc(t *testing.T) string {
	t.Helper()

	binDir := t.TempDir()
	stateDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "arc.log")
	script := filepath.Join(binDir, "arc")
	mountedPath := filepath.Join(stateDir, "mounted")
	mountingPath := filepath.Join(stateDir, "mounting")
	lockDir := filepath.Join(stateDir, "log.lock")
	content := "#!/bin/sh\n" +
		"set -eu\n" +
		"log() {\n" +
		"  while ! mkdir " + shellQuote(lockDir) + " 2>/dev/null; do sleep 0.001; done\n" +
		"  printf '%s\\n' \"$*\" >> " + shellQuote(logPath) + "\n" +
		"  rmdir " + shellQuote(lockDir) + "\n" +
		"}\n" +
		"log \"$*\"\n" +
		"if [ \"$1\" = \"mount\" ] && [ \"$2\" = \"-l\" ] && [ \"$3\" = \"--json\" ]; then\n" +
		"  if [ -f " + shellQuote(mountedPath) + " ]; then\n" +
		"    printf '[{\"status\":\"mounted\",\"mount\":\"%s\"}]\\n' \"$(cat " + shellQuote(mountedPath) + ")\"\n" +
		"  else\n" +
		"    sleep 0.1\n" +
		"    printf '[]\\n'\n" +
		"  fi\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = \"mount\" ] && [ \"$2\" = \"-m\" ]; then\n" +
		"  if [ -f " + shellQuote(mountedPath) + " ] || [ -f " + shellQuote(mountingPath) + " ]; then\n" +
		"    log mount-race \"$3\"\n" +
		"    printf 'concurrent mount\\n' >&2\n" +
		"    exit 43\n" +
		"  fi\n" +
		"  printf '%s\\n' \"$3\" > " + shellQuote(mountingPath) + "\n" +
		"  sleep 0.2\n" +
		"  mv " + shellQuote(mountingPath) + " " + shellQuote(mountedPath) + "\n" +
		"  exit 0\n" +
		"fi\n" +
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
