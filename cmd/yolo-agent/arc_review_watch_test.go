package main

import (
	"path/filepath"
	"strings"
	"testing"

	arcreviewstate "github.com/egv/yolo-runner/v2/internal/arcreview/state"
)

func TestRunArcReviewWatchPollIterationReconcilesDiscoveredPRsOnce(t *testing.T) {
	repoRoot := t.TempDir()
	statePath := filepath.Join(repoRoot, "state", "arc-review-watch.db")
	writeTrackerConfigYAML(t, repoRoot, `
profiles:
  default:
    tracker:
      type: tk
arc_review_watch:
  state_path: state/arc-review-watch.db
  branches:
    - trunk
`)

	originalDiscover := discoverArcReviewPRs
	t.Cleanup(func() {
		discoverArcReviewPRs = originalDiscover
	})
	discoverCalls := 0
	discoverArcReviewPRs = func(_ arcReviewWatchCommandConfig, cfg arcReviewWatchConfig) ([]arcReviewDiscoveredPR, error) {
		discoverCalls++
		if cfg.StatePath != statePath {
			t.Fatalf("discoverArcReviewPRs() state path = %q, want %q", cfg.StatePath, statePath)
		}
		if got := strings.Join(cfg.Branches, ","); got != "trunk" {
			t.Fatalf("discoverArcReviewPRs() branches = %q, want trunk", got)
		}
		return []arcReviewDiscoveredPR{
			{
				ID:        "ARCADIA-201",
				Workspace: "/arcadia/users/alice/pr-201",
				Branch:    "trunk",
				Revision:  "rev-201",
			},
			{
				ID:        "ARCADIA-201",
				Workspace: "/arcadia/users/alice/pr-201-duplicate",
				Branch:    "trunk",
				Revision:  "rev-201-duplicate",
			},
			{
				ID:        "ARCADIA-202",
				Workspace: "/arcadia/users/alice/pr-202-new",
				Branch:    "trunk",
				Revision:  "rev-202-new",
			},
		}, nil
	}

	store, err := arcreviewstate.Open(statePath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	existing := arcreviewstate.Session{
		ID:           "session-existing",
		PRID:         "ARCADIA-202",
		Workspace:    "/arcadia/users/alice/pr-202",
		Branch:       "trunk",
		Status:       "running",
		PID:          4242,
		Revision:     "rev-202",
		FailureCount: 1,
		LogPath:      "/tmp/pr-202.log",
	}
	if _, err := store.CreateSession(existing); err != nil {
		t.Fatalf("CreateSession(existing) error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	cfg := arcReviewWatchCommandConfig{repoRoot: repoRoot}
	if err := runArcReviewWatchPollIteration(cfg); err != nil {
		t.Fatalf("first runArcReviewWatchPollIteration() error = %v", err)
	}
	if err := runArcReviewWatchPollIteration(cfg); err != nil {
		t.Fatalf("second runArcReviewWatchPollIteration() error = %v", err)
	}
	if discoverCalls != 2 {
		t.Fatalf("discoverArcReviewPRs() calls = %d, want 2", discoverCalls)
	}

	reopened, err := arcreviewstate.Open(statePath)
	if err != nil {
		t.Fatalf("reopen state error = %v", err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	sessions, err := reopened.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("ListSessions() returned %d sessions, want 2: %#v", len(sessions), sessions)
	}
	created, err := reopened.ListSessionsByPRID("ARCADIA-201")
	if err != nil {
		t.Fatalf("ListSessionsByPRID(new) error = %v", err)
	}
	if len(created) != 1 {
		t.Fatalf("new PR sessions = %d, want 1: %#v", len(created), created)
	}
	if got := created[0]; got.Status != "pending" ||
		got.Workspace != "/arcadia/users/alice/pr-201" ||
		got.Branch != "trunk" ||
		got.Revision != "rev-201" {
		t.Fatalf("created session mismatch: %#v", got)
	}
	unchanged, err := reopened.GetSession("session-existing")
	if err != nil {
		t.Fatalf("GetSession(existing) error = %v", err)
	}
	if unchanged != existing {
		t.Fatalf("existing session changed\ngot:  %#v\nwant: %#v", unchanged, existing)
	}
}

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
