package main

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

func TestCollectorsTabRendersSeededSourcesAndSourcehostProc(t *testing.T) {
	now := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	snapshot := boardSnapshot{
		items: []workitem.Item{
			{ID: "item-1", Source: "github-prs", State: "pending", UpdatedAt: now.Add(-5 * time.Minute)},
			{ID: "item-2", Source: "github-prs", State: "active", UpdatedAt: now.Add(-2 * time.Minute)},
			{ID: "item-3", Source: "github-prs", State: "done", UpdatedAt: now.Add(-4 * time.Minute)},
		},
		sources: []workqueue.SourceRow{
			{Source: "github-prs", State: "pending", Count: 3},
			{Source: "github-prs", State: "active", Count: 1},
			{Source: "github-prs", State: "done", Count: 7},
		},
	}
	events := []contracts.Event{
		{
			Type:      contracts.EventTypeSourcePoll,
			Source:    "github-prs",
			Proc:      "sourcehost-github",
			Metadata:  map[string]string{"component": "sourcehost", "source": "github-prs", "last_error": "rate limited"},
			Timestamp: now.Add(-3 * time.Minute),
		},
		{
			Type:      contracts.EventTypeSourceHeartbeat,
			Source:    "github-prs",
			Proc:      "sourcehost-github",
			Metadata:  map[string]string{"component": "sourcehost", "source": "github-prs"},
			Timestamp: now.Add(-45 * time.Second),
		},
		{
			Type:      contracts.EventTypeRunStarted,
			Proc:      "sourcehost-startrek",
			Metadata:  map[string]string{"component": "sourcehost", "source": "startrek-security"},
			Timestamp: now.Add(-10 * time.Minute),
		},
		{
			Type:      contracts.EventTypeSourceHeartbeat,
			Source:    "startrek-security",
			Proc:      "sourcehost-startrek",
			Metadata:  map[string]string{"component": "sourcehost", "source": "startrek-security"},
			Timestamp: now.Add(-90 * time.Second),
		},
	}

	view := renderCollectorsTab(snapshot, events, now, 0)
	for _, want := range []string{
		"Collectors",
		"NAME\tTYPE\tPENDING\tACTIVE\tDONE\tLAST POLL\tLAST ERROR\tHEARTBEAT",
		"> github-prs\tgithub\t3\t1\t7\t2m0s\trate limited\t45s",
		"  startrek-security\tstartrek\t0\t0\t0\t-\t-\t1m30s",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("renderCollectorsTab() missing %q:\n%s", want, view)
		}
	}
}

func TestCollectorsTabCursorSelectsRow(t *testing.T) {
	snapshot := boardSnapshot{
		sources: []workqueue.SourceRow{
			{Source: "github-prs", State: "pending", Count: 1},
			{Source: "startrek-security", State: "done", Count: 1},
		},
	}

	view := renderCollectorsTab(snapshot, nil, time.Time{}, 1)
	if !strings.Contains(view, "  github-prs\tgithub\t1\t0\t0\t-\t-\t-") {
		t.Fatalf("renderCollectorsTab() should leave first row unselected:\n%s", view)
	}
	if !strings.Contains(view, "> startrek-security\tstartrek\t0\t0\t1\t-\t-\t-") {
		t.Fatalf("renderCollectorsTab() should select second row:\n%s", view)
	}
}

func TestCollectorsTabLastPollIgnoresItemHeartbeat(t *testing.T) {
	now := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	snapshot := boardSnapshot{
		items: []workitem.Item{
			{
				ID:          "item-1",
				Source:      "github-prs",
				State:       "pending",
				UpdatedAt:   now.Add(-5 * time.Minute),
				HeartbeatAt: now.Add(-10 * time.Second),
			},
		},
		sources: []workqueue.SourceRow{
			{Source: "github-prs", State: "pending", Count: 1},
		},
	}

	view := renderCollectorsTab(snapshot, nil, now, 0)
	want := "> github-prs\tgithub\t1\t0\t0\t5m0s\t-\t-"
	if !strings.Contains(view, want) {
		t.Fatalf("renderCollectorsTab() should use updated_at, not item heartbeat, for last poll; missing %q:\n%s", want, view)
	}
}

func TestCollectorsTabBucketsRepeatedSourceErrors(t *testing.T) {
	now := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	snapshot := boardSnapshot{}
	for i := 0; i < 3; i++ {
		snapshot.applyEvent(contracts.Event{
			Type:      contracts.EventTypeSourcePoll,
			Source:    "startrek",
			Proc:      "sourcehost-startrek",
			Metadata:  map[string]string{"component": "sourcehost", "source": "startrek", "last_error": "startrek token missing"},
			Timestamp: now.Add(time.Duration(i) * time.Second),
		})
	}

	view := renderCollectorsTab(snapshot, nil, now.Add(10*time.Second), 0)
	want := "> startrek\tstartrek\t0\t0\t0\t8s\tstartrek token missing (x3)\t-"
	if !strings.Contains(view, want) {
		t.Fatalf("renderCollectorsTab() missing bucketed error row %q:\n%s", want, view)
	}
	if strings.Count(view, "startrek token missing") != 1 {
		t.Fatalf("renderCollectorsTab() should collapse repeated errors into one row:\n%s", view)
	}
}

func TestCollectorsEnterShowsSelectedCollectorItemsResultsAndTimeline(t *testing.T) {
	now := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	item := workitem.Item{
		ID:        "item-1",
		Kind:      workitem.KindImplement,
		Source:    "github-prs",
		SourceRef: "GH-123",
		State:     "done",
		Preset:    "linux",
		UpdatedAt: now.Add(-5 * time.Minute),
	}
	snapshot := boardSnapshot{
		items: []workitem.Item{
			item,
			{ID: "item-2", Kind: workitem.KindReview, Source: "startrek-security", SourceRef: "SEC-1", State: "pending", Preset: "mac"},
		},
		sources: []workqueue.SourceRow{
			{Source: "github-prs", State: "done", Count: 1},
			{Source: "startrek-security", State: "pending", Count: 1},
		},
		unconsumedResults: []workqueue.UnconsumedResult{
			{
				Item: item,
				Result: workqueue.Result{
					ItemID:     item.ID,
					Status:     workqueue.ResultStatusCompleted,
					LogPath:    "runner-logs/item-1.log",
					FinishedAt: now.Add(-30 * time.Second),
				},
			},
		},
	}
	events := []contracts.Event{
		{
			Type:      contracts.EventTypeSourcePoll,
			Source:    "github-prs",
			Proc:      "sourcehost-github",
			Message:   "polled 1 item",
			Metadata:  map[string]string{"component": "sourcehost", "source": "github-prs"},
			Timestamp: now.Add(-2 * time.Minute),
		},
		{
			Type:      contracts.EventTypeSourceHeartbeat,
			Source:    "startrek-security",
			Proc:      "sourcehost-startrek",
			Message:   "other source heartbeat",
			Metadata:  map[string]string{"component": "sourcehost", "source": "startrek-security"},
			Timestamp: now.Add(-1 * time.Minute),
		},
	}

	model := newBoardModel(boardConfig{}, nil, nil)
	updated, _ := model.Update(pollMsg{snapshot: snapshot})
	board := updated.(boardModel)
	for _, event := range events {
		updated, _ = board.Update(eventMsg{event: event})
		board = updated.(boardModel)
	}
	updated, _ = board.Update(tea.KeyMsg{Type: tea.KeyEnter})
	board = updated.(boardModel)

	view := board.View()
	for _, want := range []string{
		"Collector github-prs",
		"Items",
		"item-1\timplement\tGH-123\tdone\tlinux",
		"Results",
		"item-1\tcompleted\trunner-logs/item-1.log\t",
		"Live timeline",
		"sourcehost-github\tsource_poll\tpolled 1 item",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("View() missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "item-2") || strings.Contains(view, "other source heartbeat") {
		t.Fatalf("View() included another source:\n%s", view)
	}
}
