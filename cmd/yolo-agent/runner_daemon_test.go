package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

func TestRunnerDaemonOnceClaimsStubHandlerAndWritesResult(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "queue.db")
	store, err := workqueue.Open(dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	item, err := store.Submit(workitem.Submission{
		Kind:           workitem.KindSplit,
		Source:         "test-source",
		SourceRef:      "TASK-1",
		IdempotencyKey: "test-source/TASK-1/split",
		Preset:         "linux",
		Payload:        json.RawMessage(`{"task_id":"TASK-1"}`),
	})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	code := RunMain([]string{
		"runner",
		"--queue", dbPath,
		"--presets", "linux",
		"--runner-id", "runner-test",
		"--once",
	}, func(context.Context, runConfig) error {
		t.Fatalf("legacy run path should not be called")
		return nil
	})
	if code != 0 {
		t.Fatalf("RunMain(runner) exit code = %d, want 0", code)
	}

	store, err = workqueue.Open(dbPath)
	if err != nil {
		t.Fatalf("Open(after run) error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close(after run) error = %v", err)
		}
	})

	results, err := store.ListUnconsumedResults("test-source")
	if err != nil {
		t.Fatalf("ListUnconsumedResults() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("ListUnconsumedResults() len = %d, want 1", len(results))
	}
	got := results[0]
	if got.Item.ID != item.ID {
		t.Fatalf("result item ID = %q, want %q", got.Item.ID, item.ID)
	}
	if got.Item.State != "done" {
		t.Fatalf("item state = %q, want done", got.Item.State)
	}
	if got.Result.Status != workqueue.ResultStatusCompleted {
		t.Fatalf("result status = %q, want completed", got.Result.Status)
	}

	var payload struct {
		Status string `json:"status"`
		Kind   string `json:"kind"`
		ItemID string `json:"item_id"`
	}
	if err := json.Unmarshal(got.Result.Payload, &payload); err != nil {
		t.Fatalf("unmarshal result payload %s: %v", got.Result.Payload, err)
	}
	if payload.Status != "stubbed" || payload.Kind != string(workitem.KindSplit) || payload.ItemID != item.ID {
		t.Fatalf("unexpected result payload: %#v", payload)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	var pid int
	var presets string
	var capacity int
	var startedAt string
	var heartbeatAt string
	if err := db.QueryRow(`
SELECT pid, presets, capacity, started_at, heartbeat_at
FROM runners
WHERE id = ?`, "runner-test").Scan(&pid, &presets, &capacity, &startedAt, &heartbeatAt); err != nil {
		t.Fatalf("read registered runner: %v", err)
	}
	if pid <= 0 {
		t.Fatalf("runner pid = %d, want positive", pid)
	}
	if presets != "linux" {
		t.Fatalf("runner presets = %q, want linux", presets)
	}
	if capacity != 1 {
		t.Fatalf("runner capacity = %d, want 1", capacity)
	}
	if startedAt == "" || heartbeatAt == "" {
		t.Fatalf("runner timestamps not populated: started_at=%q heartbeat_at=%q", startedAt, heartbeatAt)
	}
}
