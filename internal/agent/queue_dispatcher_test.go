package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

func TestQueueDispatcherSubmitsImplementWorkAndAwaitsResult(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "queue.db")
	dispatcher, err := NewQueueDispatcher(dbPath, QueueDispatcherOptions{
		Preset:       "linux",
		Source:       "run-test",
		Consumer:     "run-test",
		PollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewQueueDispatcher() error = %v", err)
	}
	t.Cleanup(func() {
		if err := dispatcher.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	priority := 7
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	handle, err := dispatcher.Submit(ctx, WorkDispatchRequest{
		Task: contracts.Task{
			ID:          "TASK-1",
			Title:       "Queued implementation",
			Description: "Run through queue dispatcher.",
			ParentID:    "ROOT-1",
			Status:      contracts.TaskStatusOpen,
			Metadata:    map[string]string{"queue": "TEST"},
		},
		Priority: priority,
		Payload: workitem.ImplementPayload{
			TaskID:      "TASK-1",
			Title:       "Queued implementation",
			Description: "Run through queue dispatcher.",
			PromptContext: workitem.ImplementPromptContext{
				ParentID: "ROOT-1",
				Metadata: map[string]string{"queue": "TEST"},
			},
			TDD:         true,
			QualityGate: true,
		},
	})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if handle.ID == "" {
		t.Fatal("Submit() returned empty queue handle ID")
	}

	store, err := workqueue.Open(dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("store Close() error = %v", err)
		}
	})

	item, err := store.Claim("runner-test", []string{"linux"}, time.Minute)
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if item == nil {
		t.Fatal("Claim() returned nil, want queued implement item")
	}
	if item.ID != handle.ID {
		t.Fatalf("claimed item ID = %q, want handle ID %q", item.ID, handle.ID)
	}
	if item.Kind != workitem.KindImplement {
		t.Fatalf("item kind = %q, want %q", item.Kind, workitem.KindImplement)
	}
	if item.Source != "run-test" {
		t.Fatalf("item source = %q, want run-test", item.Source)
	}
	if item.SourceRef != "TASK-1" {
		t.Fatalf("item source ref = %q, want TASK-1", item.SourceRef)
	}
	if item.Preset != "linux" {
		t.Fatalf("item preset = %q, want linux", item.Preset)
	}
	if item.Priority != priority {
		t.Fatalf("item priority = %d, want %d", item.Priority, priority)
	}

	payload, err := workitem.DecodeImplementPayload(item.Payload)
	if err != nil {
		t.Fatalf("DecodeImplementPayload(%s) error = %v", item.Payload, err)
	}
	if payload.TaskID != "TASK-1" || !payload.TDD || !payload.QualityGate {
		t.Fatalf("unexpected implement payload: %#v", payload)
	}

	resultPayload, err := json.Marshal(workitem.ImplementResult{
		Status:    string(contracts.RunnerResultCompleted),
		Branch:    "task/TASK-1",
		CommitSHA: "abc123",
	})
	if err != nil {
		t.Fatalf("marshal implement result: %v", err)
	}
	if err := store.Complete(item.ID, workqueue.Result{Payload: resultPayload}); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	got, err := dispatcher.AwaitResult(ctx, handle)
	if err != nil {
		t.Fatalf("AwaitResult() error = %v", err)
	}
	if got.Status != string(contracts.RunnerResultCompleted) || got.CommitSHA != "abc123" {
		t.Fatalf("unexpected implement result: %#v", got)
	}

	unconsumed, err := store.ListUnconsumedResults("run-test")
	if err != nil {
		t.Fatalf("ListUnconsumedResults() error = %v", err)
	}
	if len(unconsumed) != 0 {
		t.Fatalf("result should be consumed after AwaitResult, got %#v", unconsumed)
	}
}
