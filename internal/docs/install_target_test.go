package docs

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMakefileHasInstallTargetThatInstallsYoloAgent(t *testing.T) {
	makefile := readRepoFile(t, "Makefile")

	binaries := []string{
		"yolo-agent",
		"yolo-task",
		"yolo-tui",
	}

	required := []string{
		"install:",
		"PREFIX ?=",
		"mkdir -p",
		"chmod 755",
	}
	for _, binary := range binaries {
		required = append(required, "bin/"+binary)
	}

	for _, needle := range required {
		if !strings.Contains(makefile, needle) {
			t.Fatalf("Makefile is missing %q required for install target contract", needle)
		}
	}
}

func TestMakefileInstallTargetHonorsPrefixAndCreatesExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("make install behavior is validated on Unix-family environments only")
	}

	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	prefix := filepath.Join(t.TempDir(), "prefix")
	cmd := exec.Command("make", "-C", repoRoot, "install", "PREFIX="+prefix)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run make install: %v (%s)", err, output)
	}

	binaries := []string{
		"yolo-agent",
		"yolo-task",
		"yolo-tui",
	}

	binDir := filepath.Join(prefix, "bin")
	info, err := os.Stat(binDir)
	if err != nil || !info.IsDir() {
		t.Fatalf("install should create destination directory %q", binDir)
	}

	for _, binary := range binaries {
		target := filepath.Join(binDir, binary)
		_, err = os.Stat(target)
		if err != nil {
			t.Fatalf("installed binary should exist at custom PREFIX location %q: %v", target, err)
		}

		binInfo, err := os.Stat(target)
		if err != nil {
			t.Fatalf("inspect installed binary %q: %v", target, err)
		}
		if binInfo.Mode().Perm() != 0o755 {
			t.Fatalf("installed binary %q should be 0755, got mode %o", target, binInfo.Mode().Perm())
		}
	}

	helpOutput, err := exec.Command(filepath.Join(binDir, "yolo-agent"), "--help").CombinedOutput()
	if err != nil && !strings.Contains(string(helpOutput), "Usage of yolo-agent:") {
		t.Fatalf("installed binary should expose usage text: %v (%s)", err, helpOutput)
	}
	if !strings.Contains(string(helpOutput), "Usage of yolo-agent:") {
		t.Fatalf("installed binary should expose usage text: got %s", helpOutput)
	}

	for _, binary := range []string{
		"yolo-agent",
		"yolo-task",
		"yolo-tui",
	} {
		versionOutput, err := exec.Command(filepath.Join(binDir, binary), "--version").CombinedOutput()
		if err != nil {
			t.Fatalf("%s --version should execute successfully: %v (%s)", binary, err, versionOutput)
		}
		if strings.TrimSpace(string(versionOutput)) == "" {
			t.Fatalf("%s --version should produce output", binary)
		}
	}
}
