package contracts

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFileEventSinkWritesJSONL(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "events.jsonl")
	sink := NewFileEventSink(path)

	err := sink.Emit(context.Background(), Event{Type: EventTypeTaskStarted, TaskID: "task-1", TaskTitle: "Readable task", Message: "started", Timestamp: time.Date(2026, 2, 10, 12, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatalf("emit failed: %v", err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file failed: %v", err)
	}
	if !strings.Contains(string(content), "\"task_id\":\"task-1\"") {
		t.Fatalf("expected task id in sink output, got %q", string(content))
	}
	if !strings.Contains(string(content), "\"task_title\":\"Readable task\"") {
		t.Fatalf("expected task title in sink output, got %q", string(content))
	}
}

func TestStreamEventSinkWritesToWriterAsNDJSON(t *testing.T) {
	buf := &strings.Builder{}
	sink := NewStreamEventSink(buf)

	err := sink.Emit(context.Background(), Event{Type: EventTypeRunnerStarted, TaskID: "task-2", TaskTitle: "Streaming", Timestamp: time.Date(2026, 2, 10, 14, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatalf("emit failed: %v", err)
	}
	if !strings.Contains(buf.String(), `"type":"runner_started"`) {
		t.Fatalf("expected runner_started in stream output, got %q", buf.String())
	}
}

func TestFanoutEventSinkBroadcastsToAllSinks(t *testing.T) {
	left := &strings.Builder{}
	right := &strings.Builder{}
	sink := NewFanoutEventSink(NewStreamEventSink(left), NewStreamEventSink(right))

	err := sink.Emit(context.Background(), Event{Type: EventTypeTaskStarted, TaskID: "task-3", Timestamp: time.Date(2026, 2, 10, 14, 5, 0, 0, time.UTC)})
	if err != nil {
		t.Fatalf("emit failed: %v", err)
	}
	if !strings.Contains(left.String(), `"task_id":"task-3"`) || !strings.Contains(right.String(), `"task_id":"task-3"`) {
		t.Fatalf("expected both sinks to receive event, got left=%q right=%q", left.String(), right.String())
	}
}
