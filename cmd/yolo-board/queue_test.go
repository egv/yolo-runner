package main

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
	_ "modernc.org/sqlite"
)

func TestQueueTabRendersSeededItemsGroupedByStateWithCounts(t *testing.T) {
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

	pendingLow := seedQueueTabItem(t, store, "github", "GH-100", workitem.KindImplement, "linux", 1)
	pendingHigh := seedQueueTabItem(t, store, "startrek", "YT-200", workitem.KindReview, "mac", 3)
	claimed := seedQueueTabItem(t, store, "github", "GH-300", workitem.KindPreflight, "linux", 2)
	done := seedQueueTabItem(t, store, "github", "GH-400", workitem.KindFinalize, "linux", 4)

	if got, err := store.Claim("runner-a", []string{"linux"}, time.Minute); err != nil {
		t.Fatalf("Claim() error = %v", err)
	} else if got == nil || got.ID != done.ID {
		t.Fatalf("Claim() = %#v, want highest-priority linux item %s", got, done.ID)
	}
	if err := store.Complete(done.ID, workqueue.Result{}); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if got, err := store.Claim("runner-b", []string{"linux"}, time.Minute); err != nil {
		t.Fatalf("Claim() error = %v", err)
	} else if got == nil || got.ID != claimed.ID {
		t.Fatalf("Claim() = %#v, want next linux item %s", got, claimed.ID)
	}

	setQueueTabItemTimes(t, dbPath, pendingLow.ID, now.Add(-4*time.Minute), now.Add(-3*time.Minute))
	setQueueTabItemTimes(t, dbPath, pendingHigh.ID, now.Add(-2*time.Minute), now.Add(-90*time.Second))
	setQueueTabItemTimes(t, dbPath, claimed.ID, now.Add(-10*time.Minute), now.Add(-8*time.Minute))
	setQueueTabItemTimes(t, dbPath, done.ID, now.Add(-30*time.Minute), now.Add(-20*time.Minute))

	msg := pollBoardStore(context.Background(), store)
	poll, ok := msg.(pollMsg)
	if !ok {
		t.Fatalf("message type = %T, want pollMsg", msg)
	}

	view := renderQueueTab(poll.snapshot, now)
	for _, want := range []string{
		"Queue",
		"Counts: claimed=1 done=1 pending=2",
		"KIND\tSOURCE_REF\tPRESET\tPRIORITY\tSTATE\tATTEMPT\tCLAIMED_BY\tAGE",
		"preflight\tGH-300\tlinux\t2\tclaimed\t1\trunner-b\t10m0s",
		"finalize\tGH-400\tlinux\t4\tdone\t1\t-\t30m0s",
		"review\tYT-200\tmac\t3\tpending\t0\t-\t2m0s",
		"implement\tGH-100\tlinux\t1\tpending\t0\t-\t4m0s",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("renderQueueTab() missing %q:\n%s", want, view)
		}
	}

	assertInOrder(t, view,
		"preflight\tGH-300",
		"finalize\tGH-400",
		"review\tYT-200",
		"implement\tGH-100",
	)
}

func TestBoardModelViewRendersQueueTabWithSeededItemsAndCounts(t *testing.T) {
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

	pending := seedQueueTabItem(t, store, "github", "GH-100", workitem.KindImplement, "linux", 1)
	claimed := seedQueueTabItem(t, store, "startrek", "YT-200", workitem.KindReview, "linux", 2)
	if got, err := store.Claim("runner-a", []string{"linux"}, time.Minute); err != nil {
		t.Fatalf("Claim() error = %v", err)
	} else if got == nil || got.ID != claimed.ID {
		t.Fatalf("Claim() = %#v, want highest-priority item %s", got, claimed.ID)
	}

	setQueueTabItemTimes(t, dbPath, pending.ID, now.Add(-4*time.Minute), now.Add(-3*time.Minute))
	setQueueTabItemTimes(t, dbPath, claimed.ID, now.Add(-10*time.Minute), now.Add(-8*time.Minute))

	msg := pollBoardStore(context.Background(), store)
	poll, ok := msg.(pollMsg)
	if !ok {
		t.Fatalf("message type = %T, want pollMsg", msg)
	}
	model := newBoardModel(boardConfig{}, nil, nil)
	updated, _ := model.Update(poll)
	board := updated.(boardModel)
	updated, _ = board.Update(tea.KeyMsg{Type: tea.KeyTab})
	board = updated.(boardModel)

	view := board.View()
	for _, want := range []string{
		"polling 2 items across 2 sources",
		"Queue",
		"Counts: claimed=1 pending=1",
		"review\tYT-200\tlinux\t2\tclaimed\t1\trunner-a",
		"implement\tGH-100\tlinux\t1\tpending\t0\t-",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("View() missing %q:\n%s", want, view)
		}
	}
}

func seedQueueTabItem(t *testing.T, store *workqueue.Store, source string, sourceRef string, kind workitem.Kind, preset string, priority int) workitem.Item {
	t.Helper()

	item, err := store.Enqueue(workitem.Submission{
		Kind:           kind,
		Source:         source,
		SourceRef:      sourceRef,
		IdempotencyKey: source + "-" + sourceRef,
		Preset:         preset,
		Priority:       priority,
		Payload:        []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	return item
}

func setQueueTabItemTimes(t *testing.T, dbPath string, itemID string, createdAt time.Time, updatedAt time.Time) {
	t.Helper()

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open seed db: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(
		`UPDATE work_items SET created_at = ?, updated_at = ? WHERE id = ?`,
		createdAt.UTC().Format(time.RFC3339Nano),
		updatedAt.UTC().Format(time.RFC3339Nano),
		itemID,
	); err != nil {
		t.Fatalf("update item times %q: %v", itemID, err)
	}
}

func assertInOrder(t *testing.T, haystack string, needles ...string) {
	t.Helper()

	last := -1
	for _, needle := range needles {
		next := strings.Index(haystack, needle)
		if next == -1 {
			t.Fatalf("missing %q in:\n%s", needle, haystack)
		}
		if next <= last {
			t.Fatalf("%q was not after previous needle in:\n%s", needle, haystack)
		}
		last = next
	}
}
