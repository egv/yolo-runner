package workqueue

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/workitem"
)

func TestListRunnersAndCurrentItemForRunner(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "queue.db")

	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	startedA := time.Date(2026, 6, 30, 10, 0, 0, 123, time.UTC)
	heartbeatA := time.Date(2026, 6, 30, 10, 5, 0, 456, time.UTC)
	startedB := time.Date(2026, 6, 30, 11, 0, 0, 789, time.UTC)
	heartbeatB := time.Date(2026, 6, 30, 11, 5, 0, 987, time.UTC)
	seedRunnerRow(t, store, "runner-a", 101, "linux,arc", 2, startedA, heartbeatA)
	seedRunnerRow(t, store, "runner-b", 202, "mac", 1, startedB, heartbeatB)

	claimed := seedRunnerCurrentItem(t, store, "item-claimed", "runner-a", itemStateClaimed)
	running := seedRunnerCurrentItem(t, store, "item-running", "runner-b", "running")
	seedRunnerCurrentItem(t, store, "item-done", "runner-c", itemStateDone)

	gotRunners, err := store.ListRunners()
	if err != nil {
		t.Fatalf("ListRunners() error = %v", err)
	}
	wantRunners := []RunnerRow{
		{ID: "runner-a", Pid: 101, Presets: "linux,arc", Capacity: 2, StartedAt: startedA, HeartbeatAt: heartbeatA},
		{ID: "runner-b", Pid: 202, Presets: "mac", Capacity: 1, StartedAt: startedB, HeartbeatAt: heartbeatB},
	}
	if !reflect.DeepEqual(gotRunners, wantRunners) {
		t.Fatalf("ListRunners() = %#v, want %#v", gotRunners, wantRunners)
	}

	gotClaimed, err := store.CurrentItemForRunner("runner-a")
	if err != nil {
		t.Fatalf("CurrentItemForRunner(runner-a) error = %v", err)
	}
	if gotClaimed == nil || gotClaimed.ID != claimed.ID || gotClaimed.State != itemStateClaimed || gotClaimed.ClaimedBy != "runner-a" {
		t.Fatalf("CurrentItemForRunner(runner-a) = %#v, want claimed item %#v", gotClaimed, claimed)
	}

	gotRunning, err := store.CurrentItemForRunner("runner-b")
	if err != nil {
		t.Fatalf("CurrentItemForRunner(runner-b) error = %v", err)
	}
	if gotRunning == nil || gotRunning.ID != running.ID || gotRunning.State != "running" || gotRunning.ClaimedBy != "runner-b" {
		t.Fatalf("CurrentItemForRunner(runner-b) = %#v, want running item %#v", gotRunning, running)
	}

	gotNone, err := store.CurrentItemForRunner("runner-c")
	if err != nil {
		t.Fatalf("CurrentItemForRunner(runner-c) error = %v", err)
	}
	if gotNone != nil {
		t.Fatalf("CurrentItemForRunner(runner-c) = %#v, want nil", gotNone)
	}
}

func seedRunnerRow(t *testing.T, store *Store, id string, pid int, presets string, capacity int, startedAt time.Time, heartbeatAt time.Time) {
	t.Helper()

	if _, err := store.db.Exec(`
INSERT INTO runners (id, pid, presets, capacity, started_at, heartbeat_at)
VALUES (?, ?, ?, ?, ?, ?)`,
		id,
		pid,
		presets,
		capacity,
		formatQueueTime(startedAt),
		formatQueueTime(heartbeatAt),
	); err != nil {
		t.Fatalf("seed runner %q: %v", id, err)
	}
}

func seedRunnerCurrentItem(t *testing.T, store *Store, id string, runnerID string, state string) workitem.Item {
	t.Helper()

	now := time.Now().UTC()
	item := workitem.Item{
		ID:             id,
		Kind:           workitem.KindImplement,
		Source:         "test-source",
		SourceRef:      id,
		IdempotencyKey: "idem-" + id,
		Preset:         "linux",
		Priority:       1,
		Payload:        []byte(`{"task_id":"` + id + `"}`),
		State:          state,
		Attempt:        1,
		MaxAttempts:    3,
		ClaimedBy:      runnerID,
		LeaseExpiresAt: now.Add(time.Minute),
		HeartbeatAt:    now,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if _, err := store.db.Exec(`
INSERT INTO work_items (
	id, kind, source, source_ref, idempotency_key, preset, priority, payload,
	state, attempt, max_attempts, not_before, claimed_by, lease_expires_at,
	heartbeat_at, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		item.ID,
		string(item.Kind),
		item.Source,
		item.SourceRef,
		item.IdempotencyKey,
		item.Preset,
		item.Priority,
		string(item.Payload),
		item.State,
		item.Attempt,
		item.MaxAttempts,
		"",
		item.ClaimedBy,
		formatQueueTime(item.LeaseExpiresAt),
		formatQueueTime(item.HeartbeatAt),
		formatQueueTime(item.CreatedAt),
		formatQueueTime(item.UpdatedAt),
	); err != nil {
		t.Fatalf("seed current item %q: %v", id, err)
	}
	return item
}
