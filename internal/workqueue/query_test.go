package workqueue

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/workitem"
)

func TestPendingAndActiveDepthCountEligibleItemsBySourceAndPreset(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	for _, submission := range []workitem.Submission{
		{
			Kind:           workitem.KindImplement,
			Source:         "source-a",
			SourceRef:      "TASK-1",
			IdempotencyKey: "source-a/TASK-1/implement",
			Preset:         "linux",
			Payload:        json.RawMessage(`{"task_id":"TASK-1"}`),
		},
		{
			Kind:           workitem.KindImplement,
			Source:         "source-a",
			SourceRef:      "TASK-2",
			IdempotencyKey: "source-a/TASK-2/implement",
			Preset:         "linux",
			Payload:        json.RawMessage(`{"task_id":"TASK-2"}`),
		},
		{
			Kind:           workitem.KindImplement,
			Source:         "source-b",
			SourceRef:      "TASK-3",
			IdempotencyKey: "source-b/TASK-3/implement",
			Preset:         "linux",
			Payload:        json.RawMessage(`{"task_id":"TASK-3"}`),
		},
	} {
		if _, err := store.Submit(submission); err != nil {
			t.Fatalf("Submit(%s) error = %v", submission.SourceRef, err)
		}
	}

	claimed, err := store.Claim("runner-a", []string{"linux"}, time.Minute)
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if claimed == nil {
		t.Fatalf("Claim() returned nil")
	}

	pending, err := store.PendingDepth("source-a", []string{"linux"})
	if err != nil {
		t.Fatalf("PendingDepth() error = %v", err)
	}
	if pending != 1 {
		t.Fatalf("PendingDepth() = %d, want 1", pending)
	}

	active, err := store.ActiveDepth("source-a", []string{"linux"})
	if err != nil {
		t.Fatalf("ActiveDepth() error = %v", err)
	}
	if active != 1 {
		t.Fatalf("ActiveDepth() = %d, want 1", active)
	}
}
