package state

import (
	"database/sql"
	"path/filepath"
	"testing"
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
