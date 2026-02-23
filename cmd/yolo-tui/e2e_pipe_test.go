package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egv/yolo-runner/internal/contracts"
	tea "github.com/charmbracelet/bubbletea"
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

func TestE2E_FullscreenModelRendersRunningStoppingCompletedTransitions(t *testing.T) {
	tnow := time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC)
	events := []contracts.Event{
		{
			Type:      contracts.EventTypeTaskStarted,
			TaskID:    "task-1",
			TaskTitle: "Smoke task",
			Message:   "started",
			Timestamp: tnow,
		},
		{
			Type:      contracts.EventTypeRunnerStarted,
			TaskID:    "task-1",
			TaskTitle: "Smoke task",
			WorkerID:  "worker-1",
			Message:   "runner started",
			Timestamp: tnow,
		},
		{
			Type:      contracts.EventTypeRunnerFinished,
			TaskID:    "task-1",
			TaskTitle: "Smoke task",
			WorkerID:  "worker-1",
			Message:   "runner completed",
			Timestamp: tnow,
		},
		{
			Type:      contracts.EventTypeTaskFinished,
			TaskID:    "task-1",
			TaskTitle: "Smoke task",
			Message:   "task completed",
			Timestamp: tnow,
		},
	}

	var payload strings.Builder
	for _, event := range events {
		line, err := contracts.MarshalEventJSONL(event)
		if err != nil {
			t.Fatalf("marshal event: %v", err)
		}
		payload.WriteString(line)
	}

	stream := make(chan streamMsg, len(events))
	go decodeEvents(strings.NewReader(payload.String()), stream)
	model := newFullscreenModel(stream, nil, true)

	nextModel, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 22})
	model = nextModel.(fullscreenModel)

	consumeEvent := func() {
		msg := waitForStreamMessage(model.stream)()
		event, ok := msg.(eventMsg)
		if !ok {
			t.Fatalf("expected event message, got %T", msg)
		}
		updated, _ := model.Update(event)
		model = updated.(fullscreenModel)
	}

	consumeEvent()
	consumeEvent()
	view := model.View()
	if !strings.Contains(view, "runner_started") {
		t.Fatalf("expected running phase, got %q", view)
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	model = updated.(fullscreenModel)
	if !strings.Contains(model.View(), "Stopping...") {
		t.Fatalf("expected stopping state after ctrl+c, got %q", model.View())
	}

	updated, _ = model.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	model = updated.(fullscreenModel)
	if !strings.Contains(model.View(), "Stopping...") {
		t.Fatalf("expected stopping state to persist across resize, got %q", model.View())
	}

	consumeEvent()
	if strings.Contains(model.View(), "Stopping...") {
		t.Fatalf("expected stop banner to clear after completion event, got %q", model.View())
	}
	if !strings.Contains(model.View(), "runner_finished") {
		t.Fatalf("expected runner_finished terminal status after runner_finished event, got %q", model.View())
	}

	consumeEvent()
	if strings.Contains(model.View(), "Stopping...") {
		t.Fatalf("expected stop state to remain clear after task_finished event, got %q", model.View())
	}
	if !strings.Contains(model.View(), "task_finished") {
		t.Fatalf("expected task_finished terminal status after task_finished event, got %q", model.View())
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
