package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
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

func TestRunnerEventsFileIncludesProcItemIDAndHeartbeatKeepsLease(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dbPath := filepath.Join(t.TempDir(), "queue.db")
	store, err := workqueue.Open(dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	item, err := store.Submit(workitem.Submission{
		Kind:           workitem.KindSplit,
		Source:         "test-source",
		SourceRef:      "TASK-HEARTBEAT",
		IdempotencyKey: "test-source/TASK-HEARTBEAT/split",
		Preset:         "linux",
		Payload:        json.RawMessage(`{"task_id":"TASK-HEARTBEAT"}`),
	})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}

	reaper, err := workqueue.Open(dbPath)
	if err != nil {
		t.Fatalf("Open(reaper) error = %v", err)
	}
	t.Cleanup(func() {
		if err := reaper.Close(); err != nil {
			t.Errorf("Close(reaper) error = %v", err)
		}
	})

	runners, err := openRunnerRegistry(dbPath)
	if err != nil {
		t.Fatalf("openRunnerRegistry() error = %v", err)
	}
	t.Cleanup(func() {
		if err := runners.Close(); err != nil {
			t.Errorf("Close(runners) error = %v", err)
		}
	})
	if err := runners.Register("runner-heartbeat", []string{"linux"}, 1); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	leaseDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open lease db: %v", err)
	}
	leaseDB.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if err := leaseDB.Close(); err != nil {
			t.Errorf("Close(lease db) error = %v", err)
		}
	})

	daemon := runnerDaemon{
		store:   store,
		runners: runners,
		handlers: runnerKindRegistry{
			workitem.KindSplit: func(_ context.Context, got workitem.Item) (workqueue.Result, error) {
				if got.ID != item.ID {
					return workqueue.Result{}, fmt.Errorf("claimed item %q, want %q", got.ID, item.ID)
				}
				if got.LeaseExpiresAt.IsZero() {
					return workqueue.Result{}, fmt.Errorf("claimed item lease_expires_at is zero")
				}
				if _, err := waitForRunnerDaemonLeaseExtension(leaseDB, got.ID, got.LeaseExpiresAt, 2*time.Second); err != nil {
					return workqueue.Result{}, err
				}
				reaped, err := reaper.RequeueStale(got.LeaseExpiresAt.Add(time.Nanosecond))
				if err != nil {
					return workqueue.Result{}, err
				}
				if reaped != 0 {
					return workqueue.Result{}, fmt.Errorf("RequeueStale() reaped %d item(s), want 0 while heartbeat is active", reaped)
				}
				return workqueue.Result{Payload: json.RawMessage(`{"status":"ok"}`)}, nil
			},
		},
		cfg: runnerDaemonCommandConfig{
			queuePath:         dbPath,
			presets:           []string{"linux"},
			runnerID:          "runner-heartbeat",
			once:              true,
			pollInterval:      time.Millisecond,
			heartbeatInterval: 10 * time.Millisecond,
			leaseTTL:          40 * time.Millisecond,
		},
	}

	if err := daemon.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(home, ".yolo-runner", "events", "runner-heartbeat.jsonl"))
	if err != nil {
		t.Fatalf("read runner events file: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) < 2 {
		t.Fatalf("runner events file has %d line(s), want at least start and finish events: %q", len(lines), string(raw))
	}

	seen := map[string]bool{}
	for _, line := range lines {
		var event struct {
			Type   string `json:"type"`
			Proc   string `json:"proc"`
			ItemID string `json:"item_id"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("unmarshal event line %q: %v", line, err)
		}
		if event.Proc != "runner-heartbeat" {
			t.Fatalf("event proc = %q, want runner-heartbeat in line %q", event.Proc, line)
		}
		if event.ItemID != item.ID {
			t.Fatalf("event item_id = %q, want %q in line %q", event.ItemID, item.ID, line)
		}
		seen[event.Type] = true
	}
	for _, eventType := range []contracts.EventType{contracts.EventTypeRunnerStarted, contracts.EventTypeRunnerFinished} {
		if !seen[string(eventType)] {
			t.Fatalf("runner events missing %q; got %q", eventType, string(raw))
		}
	}
}

func waitForRunnerDaemonLeaseExtension(db *sql.DB, itemID string, previous time.Time, timeout time.Duration) (time.Time, error) {
	deadline := time.Now().Add(timeout)
	for {
		leaseExpiresAt, err := readRunnerDaemonLeaseExpiresAt(db, itemID)
		if err != nil {
			return time.Time{}, err
		}
		if leaseExpiresAt.After(previous) {
			return leaseExpiresAt, nil
		}
		if time.Now().After(deadline) {
			return time.Time{}, fmt.Errorf("lease_expires_at for %q stayed at %v, want after %v", itemID, leaseExpiresAt, previous)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func readRunnerDaemonLeaseExpiresAt(db *sql.DB, itemID string) (time.Time, error) {
	var raw string
	if err := db.QueryRow("SELECT lease_expires_at FROM work_items WHERE id = ?", itemID).Scan(&raw); err != nil {
		return time.Time{}, fmt.Errorf("read lease_expires_at for %q: %w", itemID, err)
	}
	if strings.TrimSpace(raw) == "" {
		return time.Time{}, fmt.Errorf("lease_expires_at for %q is empty", itemID)
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse lease_expires_at for %q: %w", itemID, err)
	}
	return parsed, nil
}
