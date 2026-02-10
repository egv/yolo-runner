package contracts

import (
	"strings"
	"testing"
	"time"
)

func TestMarshalEventJSONLStableOrder(t *testing.T) {
	e := Event{
		Type:      EventTypeRunnerFinished,
		TaskID:    "task-42",
		Message:   "runner completed",
		Metadata:  map[string]string{"mode": "implement", "status": "completed"},
		Timestamp: time.Date(2026, 2, 9, 12, 30, 0, 0, time.UTC),
	}

	line, err := MarshalEventJSONL(e)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	expected := `{"type":"runner_finished","task_id":"task-42","message":"runner completed","metadata":{"mode":"implement","status":"completed"},"ts":"2026-02-09T12:30:00Z"}`
	if strings.TrimSpace(line) != expected {
		t.Fatalf("unexpected json line\nexpected: %s\nactual:   %s", expected, strings.TrimSpace(line))
	}
}

func TestMarshalEventJSONLAlwaysEndsWithNewline(t *testing.T) {
	line, err := MarshalEventJSONL(Event{Type: EventTypeTaskStarted, TaskID: "t-1", Timestamp: time.Now().UTC()})
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if !strings.HasSuffix(line, "\n") {
		t.Fatalf("expected JSONL output to end with newline")
	}
}

func TestMarshalEventJSONLIncludesTaskTitleWhenPresent(t *testing.T) {
	e := Event{
		Type:      EventTypeTaskStarted,
		TaskID:    "task-7",
		TaskTitle: "Improve readability",
		Timestamp: time.Date(2026, 2, 10, 13, 0, 0, 0, time.UTC),
	}

	line, err := MarshalEventJSONL(e)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	expected := `{"type":"task_started","task_id":"task-7","task_title":"Improve readability","ts":"2026-02-10T13:00:00Z"}`
	if strings.TrimSpace(line) != expected {
		t.Fatalf("unexpected json line\nexpected: %s\nactual:   %s", expected, strings.TrimSpace(line))
	}
}

func TestMarshalEventJSONLIncludesParallelContextWhenPresent(t *testing.T) {
	e := Event{
		Type:      EventTypeRunnerStarted,
		TaskID:    "task-9",
		TaskTitle: "Parallel execution",
		WorkerID:  "worker-2",
		ClonePath: "/tmp/clones/task-9",
		QueuePos:  3,
		Timestamp: time.Date(2026, 2, 10, 13, 5, 0, 0, time.UTC),
	}

	line, err := MarshalEventJSONL(e)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	expected := `{"type":"runner_started","task_id":"task-9","task_title":"Parallel execution","worker_id":"worker-2","clone_path":"/tmp/clones/task-9","queue_pos":3,"ts":"2026-02-10T13:05:00Z"}`
	if strings.TrimSpace(line) != expected {
		t.Fatalf("unexpected json line\nexpected: %s\nactual:   %s", expected, strings.TrimSpace(line))
	}
}
