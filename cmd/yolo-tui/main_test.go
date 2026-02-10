package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/anomalyco/yolo-runner/internal/contracts"
)

func TestRunMainRendersMonitorViewFromEventsFile(t *testing.T) {
	tempDir := t.TempDir()
	eventsPath := filepath.Join(tempDir, "events.jsonl")
	content := "{\"type\":\"task_started\",\"task_id\":\"task-1\",\"task_title\":\"Readable task\",\"message\":\"started\",\"ts\":\"2026-02-10T12:00:00Z\"}\n" +
		"{\"type\":\"runner_finished\",\"task_id\":\"task-1\",\"message\":\"done\",\"ts\":\"2026-02-10T12:00:05Z\"}\n"
	if err := os.WriteFile(eventsPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write events: %v", err)
	}

	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	code := RunMain([]string{"--events", eventsPath}, out, errOut)
	if code != 0 {
		t.Fatalf("expected code 0, got %d stderr=%q", code, errOut.String())
	}
	if out.String() == "" {
		t.Fatalf("expected rendered view")
	}
	if !contains(out.String(), "Current Task: task-1 - Readable task") {
		t.Fatalf("expected current task in output, got %q", out.String())
	}
}

func TestParseEventIncludesParallelContext(t *testing.T) {
	line := []byte(`{"type":"runner_started","task_id":"task-1","task_title":"Readable task","worker_id":"worker-1","clone_path":"/tmp/clones/task-1","queue_pos":2,"message":"implement","ts":"2026-02-10T12:00:05Z"}`)

	event, err := contracts.ParseEventJSONLLine(line)
	if err != nil {
		t.Fatalf("parse event failed: %v", err)
	}
	if event.WorkerID != "worker-1" {
		t.Fatalf("expected worker id, got %q", event.WorkerID)
	}
	if event.ClonePath != "/tmp/clones/task-1" {
		t.Fatalf("expected clone path, got %q", event.ClonePath)
	}
	if event.QueuePos != 2 {
		t.Fatalf("expected queue pos 2, got %d", event.QueuePos)
	}
}

func TestRenderFromReaderParsesIncrementalEvents(t *testing.T) {
	reader, writer := io.Pipe()
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}

	done := make(chan error, 1)
	go func() {
		done <- renderFromReader(reader, out, errOut)
	}()

	_, _ = writer.Write([]byte("{\"type\":\"task_started\",\"task_id\":\"task-1\",\"task_title\":\"Readable task\",\"ts\":\"2026-02-10T12:00:00Z\"}\n"))
	_, _ = writer.Write([]byte("{\"type\":\"runner_started\",\"task_id\":\"task-1\",\"worker_id\":\"worker-1\",\"queue_pos\":1,\"ts\":\"2026-02-10T12:00:01Z\"}\n"))
	_ = writer.Close()

	if err := <-done; err != nil {
		t.Fatalf("render from reader failed: %v", err)
	}
	if !contains(out.String(), "Current Task: task-1 - Readable task") {
		t.Fatalf("expected current task in output, got %q", out.String())
	}
}

func contains(text string, sub string) bool {
	for i := 0; i+len(sub) <= len(text); i++ {
		if text[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
