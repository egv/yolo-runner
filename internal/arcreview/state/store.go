package state

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

// ThreadState tracks the last reply the agent has seen on a review comment thread
// so that a genuinely new non-self reply re-surfaces the thread for re-engagement.
type ThreadState struct {
	PRID            string
	CommentID       string
	LastSeenReplyID string
	LastSeenReplyAt time.Time
	AnsweredAt      time.Time
	UpdatedAt       time.Time
}

func Open(path string) (*Store, error) {
	if err := ensureStateParentDir(path); err != nil {
		return nil, err
	}
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

func ensureStateParentDir(path string) error {
	path = strings.TrimSpace(path)
	if path == "" || path == ":memory:" || strings.HasPrefix(path, "file:") {
		return nil
	}
	dir := filepath.Dir(path)
	if dir == "." || dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create arc review state directory %q: %w", dir, err)
	}
	return nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) init() error {
	const schema = `
CREATE TABLE IF NOT EXISTS answered_comments (
	pr_id TEXT NOT NULL,
	comment_id TEXT NOT NULL,
	answered_at TEXT NOT NULL,
	PRIMARY KEY (pr_id, comment_id)
);

CREATE TABLE IF NOT EXISTS reviewed_revisions (
	pr_id TEXT PRIMARY KEY,
	revision TEXT NOT NULL,
	reviewed_at TEXT NOT NULL
);

	CREATE TABLE IF NOT EXISTS comment_thread_state (
		pr_id TEXT NOT NULL,
		comment_id TEXT NOT NULL,
		last_seen_reply_id TEXT NOT NULL DEFAULT '',
		last_seen_reply_at TEXT NOT NULL DEFAULT '',
		answered_at TEXT NOT NULL DEFAULT '',
		updated_at TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (pr_id, comment_id)
	);`

	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("initialize arc review state schema: %w", err)
	}
	return nil
}

func (s *Store) StoreAnsweredCommentIDs(ctx context.Context, prID string, commentIDs []string) error {
	prID = strings.TrimSpace(prID)
	if prID == "" {
		return fmt.Errorf("PR ID is required")
	}
	normalizedIDs, err := normalizeAnsweredCommentIDs(commentIDs)
	if err != nil {
		return err
	}
	if len(normalizedIDs) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin answered comment store for PR %q: %w", prID, err)
	}
	defer tx.Rollback()

	answeredAt := formatTime(time.Now().UTC())
	for _, commentID := range normalizedIDs {
		if _, err := tx.ExecContext(ctx, `
INSERT OR IGNORE INTO answered_comments (pr_id, comment_id, answered_at)
VALUES (?, ?, ?)`,
			prID,
			commentID,
			answeredAt,
		); err != nil {
			return fmt.Errorf("store answered comment %q for PR %q: %w", commentID, prID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit answered comment store for PR %q: %w", prID, err)
	}
	return nil
}

func (s *Store) ListAnsweredCommentIDs(ctx context.Context, prID string) ([]string, error) {
	prID = strings.TrimSpace(prID)
	if prID == "" {
		return nil, fmt.Errorf("PR ID is required")
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT comment_id
FROM answered_comments
WHERE pr_id = ?
ORDER BY comment_id`, prID)
	if err != nil {
		return nil, fmt.Errorf("list answered comments for PR %q: %w", prID, err)
	}
	defer rows.Close()

	var commentIDs []string
	for rows.Next() {
		var commentID string
		if err := rows.Scan(&commentID); err != nil {
			return nil, fmt.Errorf("scan answered comment for PR %q: %w", prID, err)
		}
		commentIDs = append(commentIDs, commentID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read answered comments for PR %q: %w", prID, err)
	}
	return commentIDs, nil
}

func (s *Store) StoreReviewedRevision(ctx context.Context, prID string, revision string) error {
	prID = strings.TrimSpace(prID)
	if prID == "" {
		return fmt.Errorf("PR ID is required")
	}
	revision = strings.TrimSpace(revision)
	if revision == "" {
		return fmt.Errorf("revision is required")
	}

	if _, err := s.db.ExecContext(ctx, `
INSERT INTO reviewed_revisions (pr_id, revision, reviewed_at)
VALUES (?, ?, ?)
ON CONFLICT(pr_id) DO UPDATE SET
	revision = excluded.revision,
	reviewed_at = excluded.reviewed_at`,
		prID,
		revision,
		formatTime(time.Now().UTC()),
	); err != nil {
		return fmt.Errorf("store reviewed revision for PR %q: %w", prID, err)
	}
	return nil
}

func (s *Store) GetReviewedRevision(ctx context.Context, prID string) (string, error) {
	prID = strings.TrimSpace(prID)
	if prID == "" {
		return "", fmt.Errorf("PR ID is required")
	}

	var revision string
	err := s.db.QueryRowContext(ctx, `
SELECT revision
FROM reviewed_revisions
WHERE pr_id = ?`, prID).Scan(&revision)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get reviewed revision for PR %q: %w", prID, err)
	}
	return revision, nil
}

func (s *Store) RecordThreadAnswered(ctx context.Context, prID string, commentID string, lastSeenReplyID string, lastSeenReplyAt time.Time) error {
	prID = strings.TrimSpace(prID)
	if prID == "" {
		return fmt.Errorf("PR ID is required")
	}
	commentID = strings.TrimSpace(commentID)
	if commentID == "" {
		return fmt.Errorf("comment ID is required")
	}
	lastSeenReplyID = strings.TrimSpace(lastSeenReplyID)

	now := time.Now().UTC()
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO comment_thread_state (pr_id, comment_id, last_seen_reply_id, last_seen_reply_at, answered_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(pr_id, comment_id) DO UPDATE SET
	last_seen_reply_id = excluded.last_seen_reply_id,
	last_seen_reply_at = excluded.last_seen_reply_at,
	updated_at = excluded.updated_at`,
		prID,
		commentID,
		lastSeenReplyID,
		formatTime(lastSeenReplyAt),
		formatTime(now),
		formatTime(now),
	); err != nil {
		return fmt.Errorf("record thread answered for PR %q comment %q: %w", prID, commentID, err)
	}
	return nil
}

func (s *Store) GetThreadState(ctx context.Context, prID string, commentID string) (ThreadState, error) {
	prID = strings.TrimSpace(prID)
	if prID == "" {
		return ThreadState{}, fmt.Errorf("PR ID is required")
	}
	commentID = strings.TrimSpace(commentID)
	if commentID == "" {
		return ThreadState{}, fmt.Errorf("comment ID is required")
	}

	state := ThreadState{PRID: prID, CommentID: commentID}
	var lastSeenReplyID, lastSeenReplyAt, answeredAt, updatedAt string
	err := s.db.QueryRowContext(ctx, `
SELECT last_seen_reply_id, last_seen_reply_at, answered_at, updated_at
FROM comment_thread_state
WHERE pr_id = ? AND comment_id = ?`, prID, commentID).Scan(
		&lastSeenReplyID, &lastSeenReplyAt, &answeredAt, &updatedAt,
	)
	if err == sql.ErrNoRows {
		return ThreadState{}, nil
	}
	if err != nil {
		return ThreadState{}, fmt.Errorf("get thread state for PR %q comment %q: %w", prID, commentID, err)
	}
	state.LastSeenReplyID = lastSeenReplyID
	state.LastSeenReplyAt = parseTime(lastSeenReplyAt)
	state.AnsweredAt = parseTime(answeredAt)
	state.UpdatedAt = parseTime(updatedAt)
	return state, nil
}

func (s *Store) ListThreadStates(ctx context.Context, prID string) ([]ThreadState, error) {
	prID = strings.TrimSpace(prID)
	if prID == "" {
		return nil, fmt.Errorf("PR ID is required")
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT pr_id, comment_id, last_seen_reply_id, last_seen_reply_at, answered_at, updated_at
FROM comment_thread_state
WHERE pr_id = ?
ORDER BY comment_id`, prID)
	if err != nil {
		return nil, fmt.Errorf("list thread states for PR %q: %w", prID, err)
	}
	defer rows.Close()

	var states []ThreadState
	for rows.Next() {
		var state ThreadState
		var lastSeenReplyID, lastSeenReplyAt, answeredAt, updatedAt string
		if err := rows.Scan(&state.PRID, &state.CommentID, &lastSeenReplyID, &lastSeenReplyAt, &answeredAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan thread state for PR %q: %w", prID, err)
		}
		state.LastSeenReplyID = lastSeenReplyID
		state.LastSeenReplyAt = parseTime(lastSeenReplyAt)
		state.AnsweredAt = parseTime(answeredAt)
		state.UpdatedAt = parseTime(updatedAt)
		states = append(states, state)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read thread states for PR %q: %w", prID, err)
	}
	return states, nil
}

func normalizeAnsweredCommentIDs(commentIDs []string) ([]string, error) {
	seen := map[string]bool{}
	normalized := make([]string, 0, len(commentIDs))
	for _, commentID := range commentIDs {
		commentID = strings.TrimSpace(commentID)
		if commentID == "" {
			return nil, fmt.Errorf("answered comment ID is required")
		}
		if seen[commentID] {
			continue
		}
		seen[commentID] = true
		normalized = append(normalized, commentID)
	}
	return normalized, nil
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}
