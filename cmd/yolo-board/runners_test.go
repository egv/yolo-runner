package main

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/egv/yolo-runner/v2/internal/contracts"
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

func TestRunnersEnterShowsRunnerDetailWithCurrentItemAndActivity(t *testing.T) {
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
	current := seedBoardQueueItem(t, store, "runner-a", "github", "GH-123", workitem.KindImplement)

	msg := pollBoardStore(context.Background(), store)
	poll, ok := msg.(pollMsg)
	if !ok {
		t.Fatalf("message type = %T, want pollMsg", msg)
	}
	model := newBoardModel(boardConfig{}, nil, nil)
	updated, _ := model.Update(poll)
	board := updated.(boardModel)

	output := contracts.NewEvent(contracts.EventTypeAgentText, contracts.EventIdentity{Source: "github", SourceRef: "GH-123", RunnerID: "runner-a"})
	output.ItemID = current.ID
	output.Message = "running go test"
	output.Timestamp = now.Add(time.Second)
	progress := contracts.NewEvent(contracts.EventTypeAgentProgress, contracts.EventIdentity{Source: "github", SourceRef: "GH-123", RunnerID: "runner-a"})
	progress.ItemID = current.ID
	progress.Message = "2/3 checklist"
	progress.Timestamp = now.Add(2 * time.Second)
	finished := contracts.NewEvent(contracts.EventTypeAgentFinished, contracts.EventIdentity{Source: "github", SourceRef: "GH-120", RunnerID: "runner-a"})
	finished.ItemID = "previous-item"
	finished.Message = "completed"
	finished.Timestamp = now.Add(3 * time.Second)
	for _, event := range []contracts.Event{output, progress, finished} {
		updated, _ = board.Update(eventMsg{event: event})
		board = updated.(boardModel)
	}

	updated, _ = board.Update(tea.KeyMsg{Type: tea.KeyEnter})
	board = updated.(boardModel)

	view := board.View()
	for _, want := range []string{
		"Runner runner-a",
		"Registration",
		"runner-a\t101\tlinux,arc\t2\t",
		"Current item",
		current.ID,
		"implement github/GH-123",
		"Live activity",
		"runner_output\trunning go test",
		"runner_progress\t2/3 checklist",
		"Recent finishes",
		"previous-item",
		"completed",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("View() missing %q:\n%s", want, view)
		}
	}
}

func TestRunnerDetailMatchesQueueRunnerMonitoringEvents(t *testing.T) {
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
	current := seedBoardQueueItem(t, store, "runner-a", "github", "GH-123", workitem.KindImplement)

	msg := pollBoardStore(context.Background(), store)
	poll, ok := msg.(pollMsg)
	if !ok {
		t.Fatalf("message type = %T, want pollMsg", msg)
	}
	model := newBoardModel(boardConfig{}, nil, nil)
	updated, _ := model.Update(poll)
	board := updated.(boardModel)

	output := contracts.Event{
		Type:      contracts.EventTypeAgentText,
		TaskID:    current.ID,
		WorkerID:  "runner-a",
		Message:   "line output",
		Timestamp: now.Add(time.Second),
	}
	progress := contracts.Event{
		Type:      contracts.EventTypeAgentProgress,
		TaskID:    current.ID,
		WorkerID:  "runner-a",
		Message:   "working",
		Timestamp: now.Add(2 * time.Second),
	}
	other := contracts.Event{
		Type:      contracts.EventTypeAgentText,
		TaskID:    "other-item",
		WorkerID:  "runner-a",
		Message:   "other output",
		Timestamp: now.Add(3 * time.Second),
	}
	for _, event := range []contracts.Event{output, progress, other} {
		updated, _ = board.Update(eventMsg{event: event})
		board = updated.(boardModel)
	}

	view := renderRunnerDetail(board.snapshot, board.events, "runner-a", now)
	for _, want := range []string{
		"runner_output\tline output",
		"runner_progress\tworking",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("renderRunnerDetail() missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "other output") {
		t.Fatalf("renderRunnerDetail() included activity for another item:\n%s", view)
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

func seedBoardQueueItem(t *testing.T, store *workqueue.Store, runnerID string, source string, sourceRef string, kind workitem.Kind) workitem.Item {
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
	return *claimed
}
