package state

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenInitializesRequiredTablesIdempotently(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.db")

	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	assertTablesExist(t, store.db, "pr_sessions", "pr_events", "heartbeats")
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, err := Open(dbPath)
	if err != nil {
		t.Fatalf("reopen Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	assertTablesExist(t, reopened.db, "pr_sessions", "pr_events", "heartbeats")
}

func TestStoreSessionCRUDPersistsAcrossReopen(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.db")
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)

	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	created, err := store.CreateSession(Session{
		ID:           "session-1",
		PRID:         "ARCADIA-42",
		Workspace:    "/repo/workspaces/pr-42",
		Branch:       "arc-review/42",
		Status:       "starting",
		PID:          1234,
		Revision:     "r1",
		Heartbeat:    now,
		FailureCount: 1,
		LogPath:      "/tmp/pr-42.log",
	})
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	assertSessionEqual(t, created, Session{
		ID:           "session-1",
		PRID:         "ARCADIA-42",
		Workspace:    "/repo/workspaces/pr-42",
		Branch:       "arc-review/42",
		Status:       "starting",
		PID:          1234,
		Revision:     "r1",
		Heartbeat:    now,
		FailureCount: 1,
		LogPath:      "/tmp/pr-42.log",
	})

	fetched, err := store.GetSession("session-1")
	if err != nil {
		t.Fatalf("GetSession() error = %v", err)
	}
	assertSessionEqual(t, fetched, created)

	updatedHeartbeat := now.Add(5 * time.Minute)
	updated, err := store.UpdateSession(Session{
		ID:           "session-1",
		PRID:         "ARCADIA-42",
		Workspace:    "/repo/workspaces/pr-42b",
		Branch:       "arc-review/42-retry",
		Status:       "running",
		PID:          5678,
		Revision:     "r2",
		Heartbeat:    updatedHeartbeat,
		FailureCount: 2,
		LogPath:      "/tmp/pr-42-retry.log",
	})
	if err != nil {
		t.Fatalf("UpdateSession() error = %v", err)
	}
	assertSessionEqual(t, updated, Session{
		ID:           "session-1",
		PRID:         "ARCADIA-42",
		Workspace:    "/repo/workspaces/pr-42b",
		Branch:       "arc-review/42-retry",
		Status:       "running",
		PID:          5678,
		Revision:     "r2",
		Heartbeat:    updatedHeartbeat,
		FailureCount: 2,
		LogPath:      "/tmp/pr-42-retry.log",
	})

	if _, err := store.CreateSession(Session{
		ID:        "session-2",
		PRID:      "ARCADIA-43",
		Workspace: "/repo/workspaces/pr-43",
		Branch:    "arc-review/43",
		Status:    "queued",
	}); err != nil {
		t.Fatalf("CreateSession(session-2) error = %v", err)
	}

	listed, err := store.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("ListSessions() returned %d sessions, want 2: %#v", len(listed), listed)
	}
	assertSessionEqual(t, listed[0], updated)

	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, err := Open(dbPath)
	if err != nil {
		t.Fatalf("reopen Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	persisted, err := reopened.GetSession("session-1")
	if err != nil {
		t.Fatalf("reopened GetSession() error = %v", err)
	}
	assertSessionEqual(t, persisted, updated)
}

func assertTablesExist(t *testing.T, db *sql.DB, tableNames ...string) {
	t.Helper()

	for _, tableName := range tableNames {
		var name string
		err := db.QueryRow(
			"SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?",
			tableName,
		).Scan(&name)
		if err != nil {
			t.Fatalf("table %q was not created: %v", tableName, err)
		}
	}
}

func assertSessionEqual(t *testing.T, got Session, want Session) {
	t.Helper()

	if got.ID != want.ID ||
		got.PRID != want.PRID ||
		got.Workspace != want.Workspace ||
		got.Branch != want.Branch ||
		got.Status != want.Status ||
		got.PID != want.PID ||
		got.Revision != want.Revision ||
		!got.Heartbeat.Equal(want.Heartbeat) ||
		got.FailureCount != want.FailureCount ||
		got.LogPath != want.LogPath {
		t.Fatalf("session mismatch\ngot:  %#v\nwant: %#v", got, want)
	}
}
