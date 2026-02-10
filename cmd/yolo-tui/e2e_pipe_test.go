package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestE2E_StreamPipe_AgentToTUI(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go CLI is required for e2e test")
	}
	if _, err := exec.LookPath("tk"); err != nil {
		t.Skip("tk CLI is required for e2e test")
	}

	repo := t.TempDir()
	rootID := mustCreateTicket(t, repo, "Roadmap", "epic", "0", "")
	mustCreateTicket(t, repo, "Smoke task", "task", "0", rootID)

	projectRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve project root: %v", err)
	}

	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		t.Fatalf("create os pipe: %v", err)
	}
	defer readPipe.Close()

	agent := exec.Command("go", "run", "./cmd/yolo-agent", "--repo", repo, "--root", rootID, "--dry-run", "--stream")
	agent.Dir = projectRoot
	agent.Stdout = writePipe
	var agentErr bytes.Buffer
	agent.Stderr = &agentErr

	tui := exec.Command("go", "run", "./cmd/yolo-tui", "--events-stdin")
	tui.Dir = projectRoot
	tui.Stdin = readPipe
	var tuiOut bytes.Buffer
	var tuiErr bytes.Buffer
	tui.Stdout = &tuiOut
	tui.Stderr = &tuiErr

	if err := tui.Start(); err != nil {
		t.Fatalf("start yolo-tui: %v", err)
	}
	if err := agent.Start(); err != nil {
		t.Fatalf("start yolo-agent: %v", err)
	}
	if err := agent.Wait(); err != nil {
		t.Fatalf("yolo-agent failed: %v stderr=%q", err, agentErr.String())
	}
	if err := writePipe.Close(); err != nil {
		t.Fatalf("close agent pipe writer: %v", err)
	}
	if err := tui.Wait(); err != nil {
		t.Fatalf("yolo-tui failed: %v stderr=%q", err, tuiErr.String())
	}

	if !strings.Contains(tuiOut.String(), "Current Task:") {
		t.Fatalf("expected tui output to include current task, got %q", tuiOut.String())
	}
	if !strings.Contains(tuiOut.String(), "Smoke task") {
		t.Fatalf("expected tui output to include task title, got %q", tuiOut.String())
	}
}

func mustCreateTicket(t *testing.T, dir string, title string, issueType string, priority string, parent string) string {
	t.Helper()
	args := []string{"create", title, "-t", issueType, "-p", priority}
	if parent != "" {
		args = append(args, "--parent", parent)
	}
	cmd := exec.Command("tk", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("tk create failed: %v output=%s", err, string(output))
	}
	return strings.TrimSpace(string(output))
}
