package workqueue

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestOpenInitializesSchemaIdempotentlyAndMigratesMissingColumns(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "queue.db")

	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	assertQueuePragmas(t, store.db)
	assertTablesExist(t, store.db, "work_items", "work_results", "item_deps", "runners")
	assertIndexesExist(t, store.db, "idx_items_claim", "idx_items_source", "idx_items_lease")
	assertColumnsExist(t, store.db, "work_items", workItemColumns...)
	assertColumnsExist(t, store.db, "work_results", workResultColumns...)
	assertColumnsExist(t, store.db, "item_deps", itemDepColumns...)
	assertColumnsExist(t, store.db, "runners", runnerColumns...)
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, err := Open(dbPath)
	if err != nil {
		t.Fatalf("reopen Open() error = %v", err)
	}
	assertQueuePragmas(t, reopened.db)
	assertTablesExist(t, reopened.db, "work_items", "work_results", "item_deps", "runners")
	if err := reopened.Close(); err != nil {
		t.Fatalf("reopened Close() error = %v", err)
	}

	oldDBPath := filepath.Join(t.TempDir(), "old-queue.db")
	createOldWorkItemsTable(t, oldDBPath)
	migrated, err := Open(oldDBPath)
	if err != nil {
		t.Fatalf("Open(old schema) error = %v", err)
	}
	t.Cleanup(func() {
		if err := migrated.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	assertColumnsExist(t, migrated.db, "work_items", workItemColumns...)
	assertTablesExist(t, migrated.db, "work_results", "item_deps", "runners")
}

var workItemColumns = []string{
	"id",
	"kind",
	"source",
	"source_ref",
	"idempotency_key",
	"preset",
	"priority",
	"payload",
	"state",
	"attempt",
	"max_attempts",
	"not_before",
	"claimed_by",
	"lease_expires_at",
	"heartbeat_at",
	"created_at",
	"updated_at",
}

var workResultColumns = []string{
	"item_id",
	"status",
	"payload",
	"log_path",
	"started_at",
	"finished_at",
	"consumed_at",
	"consumed_by",
}

var itemDepColumns = []string{
	"item_id",
	"depends_on",
}

var runnerColumns = []string{
	"id",
	"pid",
	"presets",
	"capacity",
	"started_at",
	"heartbeat_at",
}

func createOldWorkItemsTable(t *testing.T, dbPath string) {
	t.Helper()

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open old schema db: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`
CREATE TABLE work_items (
	id TEXT PRIMARY KEY,
	kind TEXT NOT NULL,
	source TEXT NOT NULL,
	source_ref TEXT NOT NULL,
	idempotency_key TEXT NOT NULL UNIQUE,
	preset TEXT NOT NULL,
	payload TEXT NOT NULL,
	state TEXT NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);`)
	if err != nil {
		t.Fatalf("create old work_items table: %v", err)
	}
}

func assertQueuePragmas(t *testing.T, db *sql.DB) {
	t.Helper()

	var journalMode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("read journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Fatalf("journal_mode = %q, want %q", journalMode, "wal")
	}

	var busyTimeout int
	if err := db.QueryRow("PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
		t.Fatalf("read busy_timeout: %v", err)
	}
	if busyTimeout <= 0 {
		t.Fatalf("busy_timeout = %d, want positive value", busyTimeout)
	}
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

func assertIndexesExist(t *testing.T, db *sql.DB, indexNames ...string) {
	t.Helper()

	for _, indexName := range indexNames {
		var name string
		err := db.QueryRow(
			"SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?",
			indexName,
		).Scan(&name)
		if err != nil {
			t.Fatalf("index %q was not created: %v", indexName, err)
		}
	}
}

func assertColumnsExist(t *testing.T, db *sql.DB, tableName string, columnNames ...string) {
	t.Helper()

	rows, err := db.Query("PRAGMA table_info(" + tableName + ")")
	if err != nil {
		t.Fatalf("inspect %s schema: %v", tableName, err)
	}
	defer rows.Close()

	columns := map[string]bool{}
	for rows.Next() {
		var cid int
		var name string
		var typ string
		var notNull int
		var defaultValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			t.Fatalf("scan %s schema: %v", tableName, err)
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read %s schema: %v", tableName, err)
	}

	for _, columnName := range columnNames {
		if !columns[columnName] {
			t.Fatalf("%s.%s column was not created", tableName, columnName)
		}
	}
}
