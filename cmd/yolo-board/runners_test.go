package main

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
	_ "modernc.org/sqlite"
)

func TestRunnersTabRendersSeededRunnersAndCurrentItems(t *testing.T) {
	now := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
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

	seedBoardRunnerRow(t, dbPath, "runner-a", 101, "linux,arc", 2, now.Add(-10*time.Minute), now.Add(-45*time.Second))
	seedBoardRunnerRow(t, dbPath, "runner-b", 202, "mac", 1, now.Add(-20*time.Minute), now.Add(-3*time.Minute))
	seedBoardQueueItem(t, store, "runner-a", "github", "GH-123", workitem.KindImplement)
	seedBoardQueueItem(t, store, "runner-b", "startrek", "YT-456", workitem.KindReview)

	msg := pollBoardStore(context.Background(), store)
	poll, ok := msg.(pollMsg)
	if !ok {
		t.Fatalf("message type = %T, want pollMsg", msg)
	}

	view := renderRunnersTab(poll.snapshot, now)
	for _, want := range []string{
		"runner-a",
		"101",
		"linux,arc",
		"2",
		"45s",
		"implement github/GH-123",
		"runner-b",
		"202",
		"mac",
		"1",
		"3m0s",
		"review startrek/YT-456",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("renderRunnersTab() missing %q:\n%s", want, view)
		}
	}
}

func seedBoardRunnerRow(t *testing.T, dbPath string, id string, pid int, presets string, capacity int, startedAt time.Time, heartbeatAt time.Time) {
	t.Helper()

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open seed db: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`
INSERT INTO runners (id, pid, presets, capacity, started_at, heartbeat_at)
VALUES (?, ?, ?, ?, ?, ?)`,
		id,
		pid,
		presets,
		capacity,
		startedAt.UTC().Format(time.RFC3339Nano),
		heartbeatAt.UTC().Format(time.RFC3339Nano),
	); err != nil {
		t.Fatalf("seed runner %q: %v", id, err)
	}
}

func seedBoardQueueItem(t *testing.T, store *workqueue.Store, runnerID string, source string, sourceRef string, kind workitem.Kind) {
	t.Helper()

	item, err := store.Enqueue(workitem.Submission{
		Kind:           kind,
		Source:         source,
		SourceRef:      sourceRef,
		IdempotencyKey: source + "-" + sourceRef,
		Preset:         "linux",
		Priority:       1,
		Payload:        []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	claimed, err := store.Claim(runnerID, []string{"linux"}, time.Minute)
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if claimed == nil || claimed.ID != item.ID {
		t.Fatalf("Claim() = %#v, want item %s", claimed, item.ID)
	}
}
