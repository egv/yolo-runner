package main

import (
	"path/filepath"
	"testing"

	arcreviewstate "github.com/egv/yolo-runner/v2/internal/arcreview/state"
)

func TestReconcileArcReviewSessionsCreatesMissingPendingSessionsAndKeepsExistingNonTerminal(t *testing.T) {
	store, err := arcreviewstate.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	existing := arcreviewstate.Session{
		ID:           "session-existing",
		PRID:         "ARCADIA-102",
		Workspace:    "/repo/workspaces/pr-102",
		Branch:       "arc-review/102",
		Status:       "running",
		PID:          4242,
		Revision:     "r-existing",
		FailureCount: 2,
		LogPath:      "/tmp/pr-102.log",
	}
	if _, err := store.CreateSession(existing); err != nil {
		t.Fatalf("CreateSession(existing) error = %v", err)
	}

	created, err := reconcileArcReviewSessions(store, []arcReviewDiscoveredPR{
		{
			ID:        "ARCADIA-101",
			Workspace: "/repo/workspaces/pr-101",
			Branch:    "arc-review/101",
			Revision:  "r-new",
		},
		{
			ID:        "ARCADIA-102",
			Workspace: "/repo/workspaces/pr-102-new",
			Branch:    "arc-review/102-new",
			Revision:  "r-newer",
		},
	})
	if err != nil {
		t.Fatalf("reconcileArcReviewSessions() error = %v", err)
	}
	if len(created) != 1 {
		t.Fatalf("reconcileArcReviewSessions() created %d sessions, want 1: %#v", len(created), created)
	}
	if got := created[0]; got.PRID != "ARCADIA-101" ||
		got.Workspace != "/repo/workspaces/pr-101" ||
		got.Branch != "arc-review/101" ||
		got.Revision != "r-new" ||
		got.Status != "pending" {
		t.Fatalf("created session mismatch: %#v", got)
	}

	createdAgain, err := reconcileArcReviewSessions(store, []arcReviewDiscoveredPR{
		{
			ID:        "ARCADIA-101",
			Workspace: "/repo/workspaces/pr-101",
			Branch:    "arc-review/101",
			Revision:  "r-new",
		},
	})
	if err != nil {
		t.Fatalf("second reconcileArcReviewSessions() error = %v", err)
	}
	if len(createdAgain) != 0 {
		t.Fatalf("second reconcileArcReviewSessions() created %d sessions, want 0: %#v", len(createdAgain), createdAgain)
	}

	unchanged, err := store.GetSession("session-existing")
	if err != nil {
		t.Fatalf("GetSession(existing) error = %v", err)
	}
	if unchanged != existing {
		t.Fatalf("existing session changed\ngot:  %#v\nwant: %#v", unchanged, existing)
	}
}
