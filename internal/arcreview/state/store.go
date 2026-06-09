package state

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open arc review state db: %w", err)
	}

	store := &Store{db: db}
	if err := store.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) init() error {
	const schema = `
CREATE TABLE IF NOT EXISTS pr_sessions (
	id TEXT PRIMARY KEY,
	pr_id TEXT NOT NULL,
	status TEXT NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS pr_events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	session_id TEXT NOT NULL,
	event_type TEXT NOT NULL,
	payload TEXT NOT NULL,
	created_at TEXT NOT NULL,
	FOREIGN KEY (session_id) REFERENCES pr_sessions(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS heartbeats (
	session_id TEXT PRIMARY KEY,
	beat_at TEXT NOT NULL,
	metadata TEXT NOT NULL DEFAULT '{}',
	FOREIGN KEY (session_id) REFERENCES pr_sessions(id) ON DELETE CASCADE
);`

	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("initialize arc review state schema: %w", err)
	}
	return nil
}
