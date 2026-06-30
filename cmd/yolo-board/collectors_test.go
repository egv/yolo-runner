package main

import (
	"strings"
	"testing"
	"time"

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
