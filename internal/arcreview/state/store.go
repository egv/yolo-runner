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
