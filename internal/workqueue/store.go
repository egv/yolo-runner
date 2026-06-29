package workqueue

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

const queueBusyTimeoutMillis = 5000

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	dbPath, err := resolveQueuePath(path)
	if err != nil {
		return nil, err
	}
	if err := ensureQueueParentDir(dbPath); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open workqueue db: %w", err)
	}
	db.SetMaxOpenConns(1)

	store := &Store{db: db}
	if err := store.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func OpenReadOnly(path string) (*Store, error) {
	dbPath, err := resolveQueuePath(path)
	if err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		return nil, fmt.Errorf("open read-only workqueue db: %w", err)
	}
	db.SetMaxOpenConns(1)

	store := &Store{db: db}
	if err := store.configureReadOnlyPragmas(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func resolveQueuePath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path != "" {
		return path, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve workqueue home directory: %w", err)
	}
	if strings.TrimSpace(home) == "" {
		return "", fmt.Errorf("resolve workqueue home directory: empty home")
	}
	return filepath.Join(home, ".yolo-runner", "queue.db"), nil
}

func ensureQueueParentDir(path string) error {
	path = strings.TrimSpace(path)
	if path == "" || path == ":memory:" || strings.HasPrefix(path, "file:") {
		return nil
	}
	dir := filepath.Dir(path)
	if dir == "." || dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create workqueue directory %q: %w", dir, err)
	}
	return nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) init() error {
	if err := s.configurePragmas(); err != nil {
		return err
	}
	if err := s.createTables(); err != nil {
		return err
	}
	if err := s.ensureColumns(); err != nil {
		return err
	}
	if err := s.createIndexes(); err != nil {
		return err
	}
	return nil
}

func (s *Store) configurePragmas() error {
	if _, err := s.db.Exec(fmt.Sprintf("PRAGMA busy_timeout = %d", queueBusyTimeoutMillis)); err != nil {
		return fmt.Errorf("set workqueue busy_timeout: %w", err)
	}

	var journalMode string
	if err := s.db.QueryRow("PRAGMA journal_mode=WAL").Scan(&journalMode); err != nil {
		return fmt.Errorf("set workqueue journal_mode WAL: %w", err)
	}
	if !strings.EqualFold(journalMode, "wal") {
		return fmt.Errorf("set workqueue journal_mode WAL: got %q", journalMode)
	}
	return nil
}

func (s *Store) configureReadOnlyPragmas() error {
	if _, err := s.db.Exec(fmt.Sprintf("PRAGMA busy_timeout = %d", queueBusyTimeoutMillis)); err != nil {
		return fmt.Errorf("set read-only workqueue busy_timeout: %w", err)
	}
	if _, err := s.db.Exec("PRAGMA query_only = 1"); err != nil {
		return fmt.Errorf("set read-only workqueue query_only: %w", err)
	}
	return nil
}

func (s *Store) createTables() error {
	const schema = `
CREATE TABLE IF NOT EXISTS work_items (
	id TEXT PRIMARY KEY,
	kind TEXT NOT NULL,
	source TEXT NOT NULL,
	source_ref TEXT NOT NULL,
	idempotency_key TEXT NOT NULL UNIQUE,
	preset TEXT NOT NULL,
	priority INTEGER NOT NULL DEFAULT 0,
	payload TEXT NOT NULL,
	state TEXT NOT NULL,
	attempt INTEGER NOT NULL DEFAULT 0,
	max_attempts INTEGER NOT NULL DEFAULT 3,
	not_before TEXT NOT NULL DEFAULT '',
	claimed_by TEXT NOT NULL DEFAULT '',
	lease_expires_at TEXT NOT NULL DEFAULT '',
	heartbeat_at TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS item_deps (
	item_id TEXT NOT NULL REFERENCES work_items(id),
	depends_on TEXT NOT NULL REFERENCES work_items(id),
	PRIMARY KEY (item_id, depends_on)
);

CREATE TABLE IF NOT EXISTS work_results (
	item_id TEXT PRIMARY KEY REFERENCES work_items(id),
	status TEXT NOT NULL,
	payload TEXT NOT NULL,
	log_path TEXT NOT NULL DEFAULT '',
	started_at TEXT NOT NULL,
	finished_at TEXT NOT NULL,
	consumed_at TEXT NOT NULL DEFAULT '',
	consumed_by TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS runners (
	id TEXT PRIMARY KEY,
	pid INTEGER NOT NULL,
	presets TEXT NOT NULL,
	capacity INTEGER NOT NULL,
	started_at TEXT NOT NULL,
	heartbeat_at TEXT NOT NULL
);`

	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("initialize workqueue schema: %w", err)
	}
	return nil
}

func (s *Store) ensureColumns() error {
	for _, table := range []struct {
		name    string
		columns []columnDef
	}{
		{
			name: "work_items",
			columns: []columnDef{
				{name: "priority", def: "INTEGER NOT NULL DEFAULT 0"},
				{name: "attempt", def: "INTEGER NOT NULL DEFAULT 0"},
				{name: "max_attempts", def: "INTEGER NOT NULL DEFAULT 3"},
				{name: "not_before", def: "TEXT NOT NULL DEFAULT ''"},
				{name: "claimed_by", def: "TEXT NOT NULL DEFAULT ''"},
				{name: "lease_expires_at", def: "TEXT NOT NULL DEFAULT ''"},
				{name: "heartbeat_at", def: "TEXT NOT NULL DEFAULT ''"},
			},
		},
		{
			name: "work_results",
			columns: []columnDef{
				{name: "log_path", def: "TEXT NOT NULL DEFAULT ''"},
				{name: "consumed_at", def: "TEXT NOT NULL DEFAULT ''"},
				{name: "consumed_by", def: "TEXT NOT NULL DEFAULT ''"},
			},
		},
	} {
		if err := s.ensureTableColumns(table.name, table.columns); err != nil {
			return err
		}
	}
	return nil
}

type columnDef struct {
	name string
	def  string
}

func (s *Store) ensureTableColumns(tableName string, definitions []columnDef) error {
	columns, err := s.tableColumns(tableName)
	if err != nil {
		return err
	}
	for _, definition := range definitions {
		if columns[definition.name] {
			continue
		}
		if _, err := s.db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", tableName, definition.name, definition.def)); err != nil {
			return fmt.Errorf("add %s.%s column: %w", tableName, definition.name, err)
		}
	}
	return nil
}

func (s *Store) tableColumns(tableName string) (map[string]bool, error) {
	rows, err := s.db.Query("PRAGMA table_info(" + tableName + ")")
	if err != nil {
		return nil, fmt.Errorf("inspect %s schema: %w", tableName, err)
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
			return nil, fmt.Errorf("scan %s schema: %w", tableName, err)
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read %s schema: %w", tableName, err)
	}
	return columns, nil
}

func (s *Store) createIndexes() error {
	const indexes = `
CREATE INDEX IF NOT EXISTS idx_items_claim ON work_items(state, preset, priority DESC, created_at);
CREATE INDEX IF NOT EXISTS idx_items_source ON work_items(source, state);
CREATE INDEX IF NOT EXISTS idx_items_lease ON work_items(state, lease_expires_at);
CREATE INDEX IF NOT EXISTS idx_items_claimed_by ON work_items(claimed_by);`

	if _, err := s.db.Exec(indexes); err != nil {
		return fmt.Errorf("initialize workqueue indexes: %w", err)
	}
	return nil
}
