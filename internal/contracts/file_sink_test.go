package contracts

import (
	"context"
	"encoding/json"
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

func TestStreamEventSinkCoalescesRunnerOutputByDefault(t *testing.T) {
	buf := &strings.Builder{}
	sink := NewStreamEventSinkWithOptions(buf, StreamEventSinkOptions{
		OutputInterval: 10 * time.Second,
		MaxPending:     2,
	})

	for i := 0; i < 4; i++ {
		if err := sink.Emit(context.Background(), Event{Type: EventTypeRunnerOutput, TaskID: "task-2", Message: "line", Timestamp: time.Date(2026, 2, 10, 14, 0, i, 0, time.UTC)}); err != nil {
			t.Fatalf("emit output %d failed: %v", i, err)
		}
	}
	if err := sink.Emit(context.Background(), Event{Type: EventTypeRunnerFinished, TaskID: "task-2", Timestamp: time.Date(2026, 2, 10, 14, 1, 0, 0, time.UTC)}); err != nil {
		t.Fatalf("emit runner_finished failed: %v", err)
	}

	lines := splitJSONLLines(buf.String())
	if len(lines) != 3 {
		t.Fatalf("expected [first_output coalesced_output runner_finished], got %d lines: %q", len(lines), buf.String())
	}

	var payload struct {
		Type     string            `json:"type"`
		Metadata map[string]string `json:"metadata"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &payload); err != nil {
		t.Fatalf("decode coalesced payload: %v", err)
	}
	if payload.Type != string(EventTypeRunnerOutput) {
		t.Fatalf("expected coalesced runner_output, got %q", payload.Type)
	}
	if payload.Metadata["coalesced_outputs"] != "1" {
		t.Fatalf("expected coalesced_outputs=1, got %#v", payload.Metadata)
	}
	if payload.Metadata["dropped_outputs"] != "1" {
		t.Fatalf("expected dropped_outputs=1, got %#v", payload.Metadata)
	}
}

func TestStreamEventSinkVerboseModeDoesNotCoalesceOutput(t *testing.T) {
	buf := &strings.Builder{}
	sink := NewStreamEventSinkWithOptions(buf, StreamEventSinkOptions{VerboseOutput: true})

	for i := 0; i < 3; i++ {
		if err := sink.Emit(context.Background(), Event{Type: EventTypeRunnerOutput, TaskID: "task-3", Message: "line", Timestamp: time.Date(2026, 2, 10, 15, 0, i, 0, time.UTC)}); err != nil {
			t.Fatalf("emit output %d failed: %v", i, err)
		}
	}

	lines := splitJSONLLines(buf.String())
	if len(lines) != 3 {
		t.Fatalf("expected 3 output lines in verbose mode, got %d: %q", len(lines), buf.String())
	}
}

func splitJSONLLines(content string) []string {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
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
