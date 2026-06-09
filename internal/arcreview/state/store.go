package state

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

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
	workspace TEXT NOT NULL DEFAULT '',
	branch TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL,
	pid INTEGER NOT NULL DEFAULT 0,
	revision TEXT NOT NULL DEFAULT '',
	heartbeat_at TEXT NOT NULL DEFAULT '',
	failure_count INTEGER NOT NULL DEFAULT 0,
	log_path TEXT NOT NULL DEFAULT '',
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
	if err := s.ensureSessionColumns(); err != nil {
		return err
	}
	return nil
}

func (s *Store) ensureSessionColumns() error {
	columns, err := s.sessionColumns()
	if err != nil {
		return err
	}
	for _, column := range []struct {
		name string
		def  string
	}{
		{name: "workspace", def: "TEXT NOT NULL DEFAULT ''"},
		{name: "branch", def: "TEXT NOT NULL DEFAULT ''"},
		{name: "pid", def: "INTEGER NOT NULL DEFAULT 0"},
		{name: "revision", def: "TEXT NOT NULL DEFAULT ''"},
		{name: "heartbeat_at", def: "TEXT NOT NULL DEFAULT ''"},
		{name: "failure_count", def: "INTEGER NOT NULL DEFAULT 0"},
		{name: "log_path", def: "TEXT NOT NULL DEFAULT ''"},
	} {
		if columns[column.name] {
			continue
		}
		if _, err := s.db.Exec(fmt.Sprintf("ALTER TABLE pr_sessions ADD COLUMN %s %s", column.name, column.def)); err != nil {
			return fmt.Errorf("add pr_sessions.%s column: %w", column.name, err)
		}
	}
	return nil
}

func (s *Store) sessionColumns() (map[string]bool, error) {
	rows, err := s.db.Query("PRAGMA table_info(pr_sessions)")
	if err != nil {
		return nil, fmt.Errorf("inspect pr_sessions schema: %w", err)
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
			return nil, fmt.Errorf("scan pr_sessions schema: %w", err)
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read pr_sessions schema: %w", err)
	}
	return columns, nil
}

func (s *Store) CreateSession(session Session) (Session, error) {
	if err := validateSession(session); err != nil {
		return Session{}, err
	}
	now := formatTime(time.Now().UTC())
	_, err := s.db.Exec(`
INSERT INTO pr_sessions (
	id, pr_id, workspace, branch, status, pid, revision, heartbeat_at,
	failure_count, log_path, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		session.ID,
		session.PRID,
		session.Workspace,
		session.Branch,
		session.Status,
		session.PID,
		session.Revision,
		formatTime(session.Heartbeat),
		session.FailureCount,
		session.LogPath,
		now,
		now,
	)
	if err != nil {
		return Session{}, fmt.Errorf("create PR session %q: %w", session.ID, err)
	}
	return session, nil
}

func (s *Store) GetSession(id string) (Session, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Session{}, fmt.Errorf("session ID is required")
	}
	session, err := scanSession(s.db.QueryRow(sessionSelectSQL()+" WHERE id = ?", id))
	if err != nil {
		if err == sql.ErrNoRows {
			return Session{}, err
		}
		return Session{}, fmt.Errorf("get PR session %q: %w", id, err)
	}
	return session, nil
}

func (s *Store) UpdateSession(session Session) (Session, error) {
	if err := validateSession(session); err != nil {
		return Session{}, err
	}
	result, err := s.db.Exec(`
UPDATE pr_sessions
SET pr_id = ?,
	workspace = ?,
	branch = ?,
	status = ?,
	pid = ?,
	revision = ?,
	heartbeat_at = ?,
	failure_count = ?,
	log_path = ?,
	updated_at = ?
WHERE id = ?`,
		session.PRID,
		session.Workspace,
		session.Branch,
		session.Status,
		session.PID,
		session.Revision,
		formatTime(session.Heartbeat),
		session.FailureCount,
		session.LogPath,
		formatTime(time.Now().UTC()),
		session.ID,
	)
	if err != nil {
		return Session{}, fmt.Errorf("update PR session %q: %w", session.ID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Session{}, fmt.Errorf("update PR session %q rows affected: %w", session.ID, err)
	}
	if affected == 0 {
		return Session{}, sql.ErrNoRows
	}
	return session, nil
}

func (s *Store) ListSessions() ([]Session, error) {
	rows, err := s.db.Query(sessionSelectSQL() + " ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("list PR sessions: %w", err)
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		session, err := scanSession(rows)
		if err != nil {
			return nil, fmt.Errorf("scan PR session: %w", err)
		}
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read PR sessions: %w", err)
	}
	return sessions, nil
}

type sessionScanner interface {
	Scan(dest ...any) error
}

func scanSession(scanner sessionScanner) (Session, error) {
	var session Session
	var heartbeat string
	if err := scanner.Scan(
		&session.ID,
		&session.PRID,
		&session.Workspace,
		&session.Branch,
		&session.Status,
		&session.PID,
		&session.Revision,
		&heartbeat,
		&session.FailureCount,
		&session.LogPath,
	); err != nil {
		return Session{}, err
	}
	parsed, err := parseTime(heartbeat)
	if err != nil {
		return Session{}, err
	}
	session.Heartbeat = parsed
	return session, nil
}

func sessionSelectSQL() string {
	return `SELECT id, pr_id, workspace, branch, status, pid, revision, heartbeat_at, failure_count, log_path FROM pr_sessions`
}

func validateSession(session Session) error {
	if strings.TrimSpace(session.ID) == "" {
		return fmt.Errorf("session ID is required")
	}
	if strings.TrimSpace(session.PRID) == "" {
		return fmt.Errorf("PR ID is required")
	}
	if strings.TrimSpace(session.Status) == "" {
		return fmt.Errorf("session status is required")
	}
	return nil
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse session heartbeat %q: %w", value, err)
	}
	return parsed, nil
}
