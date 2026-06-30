package main

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

type fakeBoardStore struct {
	items       []workitem.Item
	runners     []workqueue.RunnerRow
	sources     []workqueue.SourceRow
	stateCounts map[string]int
}

func (s fakeBoardStore) ListItems(workqueue.ListItemsFilter) ([]workitem.Item, error) {
	return s.items, nil
}

func (s fakeBoardStore) ListRunners() ([]workqueue.RunnerRow, error) {
	return s.runners, nil
}

func (s fakeBoardStore) ListSources() ([]workqueue.SourceRow, error) {
	return s.sources, nil
}

func (s fakeBoardStore) ItemStateCounts() (map[string]int, error) {
	return s.stateCounts, nil
}

func TestBoardModelPollAndEventUpdateState(t *testing.T) {
	model := newBoardModel(boardConfig{}, nil, nil)
	poll := pollMsg{
		snapshot: boardSnapshot{
			items: []workitem.Item{
				{ID: "item-1", Source: "github", State: "open"},
				{ID: "item-2", Source: "startrek", State: "running"},
			},
			runners: []workqueue.RunnerRow{{ID: "runner-1"}},
			sources: []workqueue.SourceRow{
				{Source: "github", State: "open", Count: 1},
				{Source: "startrek", State: "running", Count: 1},
			},
			stateCounts: map[string]int{"open": 1, "running": 1},
		},
	}

	updated, _ := model.Update(poll)
	board := updated.(boardModel)
	if got := board.snapshot.itemCount(); got != 2 {
		t.Fatalf("item count = %d, want 2", got)
	}
	if got := board.snapshot.sourceCount(); got != 2 {
		t.Fatalf("source count = %d, want 2", got)
	}
	if !strings.Contains(board.View(), "polling 2 items across 2 sources") {
		t.Fatalf("View() = %q, want item/source counts", board.View())
	}

	event := contracts.NewEvent(contracts.EventTypeAgentText, contracts.EventIdentity{Source: "github", RunnerID: "runner-1"})
	event.Message = "hello"
	updated, _ = board.Update(eventMsg{event: event})
	board = updated.(boardModel)
	if got := len(board.events); got != 1 {
		t.Fatalf("event count = %d, want 1", got)
	}
	if board.events[0].Message != "hello" {
		t.Fatalf("event message = %q, want hello", board.events[0].Message)
	}
}

func TestReadEventsFromStdinDecodesSeededNDJSON(t *testing.T) {
	var buf bytes.Buffer
	stream := contracts.NewEventStream(&buf)
	event := contracts.NewEvent(contracts.EventTypeAgentProgress, contracts.EventIdentity{Source: "github", RunnerID: "runner-1"})
	event.Message = "progress"
	if err := stream.Write(event); err != nil {
		t.Fatalf("write event: %v", err)
	}

	ch := make(chan streamMsg, 1)
	readEventsFromStdin(bytes.NewReader(buf.Bytes()), ch)
	msg := <-ch
	decoded, ok := msg.(eventMsg)
	if !ok {
		t.Fatalf("message type = %T, want eventMsg", msg)
	}
	if decoded.event.Type != contracts.EventTypeAgentProgress {
		t.Fatalf("event type = %q, want %q", decoded.event.Type, contracts.EventTypeAgentProgress)
	}
	if decoded.event.Message != "progress" {
		t.Fatalf("event message = %q, want progress", decoded.event.Message)
	}
}

func TestPollBoardStorePopulatesSnapshotCounts(t *testing.T) {
	store := fakeBoardStore{
		items: []workitem.Item{
			{ID: "item-1", Source: "github", State: "open"},
			{ID: "item-2", Source: "github", State: "done"},
		},
		runners:     []workqueue.RunnerRow{{ID: "runner-1"}},
		sources:     []workqueue.SourceRow{{Source: "github", State: "open", Count: 1}, {Source: "github", State: "done", Count: 1}},
		stateCounts: map[string]int{"open": 1, "done": 1},
	}

	msg := pollBoardStore(context.Background(), store)
	poll, ok := msg.(pollMsg)
	if !ok {
		t.Fatalf("message type = %T, want pollMsg", msg)
	}
	if got := poll.snapshot.itemCount(); got != 2 {
		t.Fatalf("item count = %d, want 2", got)
	}
	if got := poll.snapshot.sourceCount(); got != 1 {
		t.Fatalf("source count = %d, want 1", got)
	}
	if got := poll.snapshot.stateCounts["open"]; got != 1 {
		t.Fatalf("open count = %d, want 1", got)
	}
}

func TestMissingDBShowsWaitingSplashWithoutCrashing(t *testing.T) {
	openStore := func(string) (boardStore, error) {
		return nil, errQueueDBMissing
	}
	model := newBoardModel(boardConfig{queuePath: "missing.db"}, openStore, nil)

	updated, _ := model.Update(pollTickMsg{})
	board := updated.(boardModel)
	if !board.waitingForDB {
		t.Fatal("waitingForDB = false, want true")
	}
	if !strings.Contains(board.View(), "waiting for queue DB") {
		t.Fatalf("View() = %q, want waiting splash", board.View())
	}
}

func TestRunMainRequiresQueue(t *testing.T) {
	code := RunMain(nil, strings.NewReader(""), io.Discard, io.Discard)
	if code == 0 {
		t.Fatal("RunMain() exit code = 0, want non-zero")
	}
}
