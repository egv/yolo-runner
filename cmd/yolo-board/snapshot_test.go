package main

import (
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/workitem"
)

func TestSnapshotPollDoesNotClobberNewerEventDerivedField(t *testing.T) {
	t1 := time.Date(2026, 6, 30, 10, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Minute)
	snapshot := boardSnapshot{}
	snapshot.applyEvent(contracts.Event{
		Type:      contracts.EventTypeAgentText,
		ItemID:    "item-1",
		Message:   "event output",
		Timestamp: t2,
	})

	snapshot.applyPoll(boardSnapshot{
		items: []workitem.Item{{
			ID:        "item-1",
			State:     "claimed",
			UpdatedAt: t1,
		}},
	})

	runtime := snapshot.runtimeByItem["item-1"]
	if runtime.output != "event output" {
		t.Fatalf("output = %q, want event output", runtime.output)
	}
	if runtime.phase != string(contracts.EventTypeAgentText) {
		t.Fatalf("phase = %q, want event phase", runtime.phase)
	}
	if !runtime.lastEventAt.Equal(t2) {
		t.Fatalf("lastEventAt = %s, want %s", runtime.lastEventAt, t2)
	}
}

func TestSnapshotPollNewerThanEventWinsEventDerivedField(t *testing.T) {
	t1 := time.Date(2026, 6, 30, 10, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Minute)
	snapshot := boardSnapshot{}
	snapshot.applyEvent(contracts.Event{
		Type:      contracts.EventTypeAgentText,
		ItemID:    "item-1",
		Message:   "event output",
		Timestamp: t1,
	})

	snapshot.applyPoll(boardSnapshot{
		items: []workitem.Item{{
			ID:          "item-1",
			State:       "running",
			HeartbeatAt: t2,
			UpdatedAt:   t1,
		}},
	})

	runtime := snapshot.runtimeByItem["item-1"]
	if runtime.output != "" {
		t.Fatalf("output = %q, want poll to clear stale event output", runtime.output)
	}
	if runtime.phase != "running" {
		t.Fatalf("phase = %q, want running", runtime.phase)
	}
	if !runtime.lastEventAt.IsZero() {
		t.Fatalf("lastEventAt = %s, want zero after newer poll wins", runtime.lastEventAt)
	}
}
