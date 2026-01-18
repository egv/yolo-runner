package main

import (
	"os/exec"
	"strings"
	"testing"
)

func TestHelpDocumentsFlags(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	cmd := exec.Command("go", "build", "-o", "yolo-runner-test")
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to build: %v", err)
	}
	defer func() {
		_ = exec.Command("rm", "-f", "yolo-runner-test").Run()
	}()

	cmd = exec.Command("./yolo-runner-test", "--help")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Failed to run --help: %v", err)
	}

	outputStr := string(output)
	expectedFlags := []string{"--repo", "--root", "--max", "--dry-run", "--model"}

	for _, flag := range expectedFlags {
		if !strings.Contains(outputStr, flag) {
			t.Errorf("Help output missing flag %s\nOutput:\n%s", flag, outputStr)
		}
	}
}
