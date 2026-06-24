package state

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/arcreview"
)

var _ arcreview.ReviewedRevisionStore = (*Store)(nil)
var _ arcreview.AnsweredCommentStore = (*Store)(nil)

func TestOpenInitializesSourceStateTablesIdempotently(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.db")

	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	assertTablesExist(t, store.db, "answered_comments", "reviewed_revisions", "comment_thread_state")
	assertTablesMissing(t, store.db, "pr_sessions", "pr_events", "heartbeats")
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
	assertTablesExist(t, reopened.db, "answered_comments", "reviewed_revisions", "comment_thread_state")
	assertTablesMissing(t, reopened.db, "pr_sessions", "pr_events", "heartbeats")
}

func TestStoreAnsweredCommentIDsRoundTripsDedupedAndSorted(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "state.db")

	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if err := store.StoreAnsweredCommentIDs(ctx, "ARCADIA-42", []string{"c2", "c1", "c2"}); err != nil {
		t.Fatalf("StoreAnsweredCommentIDs() error = %v", err)
	}
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

	answered, err := reopened.ListAnsweredCommentIDs(ctx, "ARCADIA-42")
	if err != nil {
		t.Fatalf("ListAnsweredCommentIDs() error = %v", err)
	}
	if want := []string{"c1", "c2"}; !reflect.DeepEqual(answered, want) {
		t.Fatalf("ListAnsweredCommentIDs() = %#v, want %#v", answered, want)
	}
}

func TestStoreReviewedRevisionRoundTrips(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "state.db")

	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	unknownRevision, err := store.GetReviewedRevision(ctx, "ARCADIA-404")
	if err != nil {
		t.Fatalf("GetReviewedRevision(unknown) error = %v", err)
	}
	if unknownRevision != "" {
		t.Fatalf("GetReviewedRevision(unknown) = %q, want empty", unknownRevision)
	}

	if err := store.StoreReviewedRevision(ctx, "ARCADIA-42", "r7"); err != nil {
		t.Fatalf("StoreReviewedRevision() error = %v", err)
	}
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

	revision, err := reopened.GetReviewedRevision(ctx, "ARCADIA-42")
	if err != nil {
		t.Fatalf("GetReviewedRevision() error = %v", err)
	}
	if revision != "r7" {
		t.Fatalf("GetReviewedRevision() = %q, want %q", revision, "r7")
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

func assertTablesMissing(t *testing.T, db *sql.DB, tableNames ...string) {
	t.Helper()

	for _, tableName := range tableNames {
		var name string
		err := db.QueryRow(
			"SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?",
			tableName,
		).Scan(&name)
		if err != sql.ErrNoRows {
			t.Fatalf("table %q exists after state trim; scan name=%q err=%v", tableName, name, err)
		}
	}
}

func openStateTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return store
}

func TestRecordThreadAnsweredRoundTripsAndUpsertsLastSeen(t *testing.T) {
	ctx := context.Background()
	store := openStateTestStore(t)

	got, err := store.GetThreadState(ctx, "PR-1", "c1")
	if err != nil {
		t.Fatalf("GetThreadState(missing) error = %v", err)
	}
	if got.CommentID != "" {
		t.Fatalf("GetThreadState(missing) = %#v, want empty thread state", got)
	}

	firstSeen := time.Date(2026, 6, 24, 10, 0, 0, 0, time.UTC)
	if err := store.RecordThreadAnswered(ctx, "PR-1", "c1", "r1", firstSeen); err != nil {
		t.Fatalf("RecordThreadAnswered(r1) error = %v", err)
	}

	got, err = store.GetThreadState(ctx, "PR-1", "c1")
	if err != nil {
		t.Fatalf("GetThreadState(after r1) error = %v", err)
	}
	if got.PRID != "PR-1" || got.CommentID != "c1" {
		t.Fatalf("GetThreadState identity = %#v, want PR-1/c1", got)
	}
	if got.LastSeenReplyID != "r1" {
		t.Fatalf("LastSeenReplyID = %q, want r1", got.LastSeenReplyID)
	}
	if !got.LastSeenReplyAt.Equal(firstSeen) {
		t.Fatalf("LastSeenReplyAt = %v, want %v", got.LastSeenReplyAt, firstSeen)
	}
	if got.AnsweredAt.IsZero() {
		t.Fatalf("AnsweredAt was not recorded")
	}
	if got.UpdatedAt.IsZero() {
		t.Fatalf("UpdatedAt was not recorded")
	}
	firstAnsweredAt := got.AnsweredAt

	secondSeen := firstSeen.Add(time.Hour)
	if err := store.RecordThreadAnswered(ctx, "PR-1", "c1", "r2", secondSeen); err != nil {
		t.Fatalf("RecordThreadAnswered(r2) error = %v", err)
	}
	got, err = store.GetThreadState(ctx, "PR-1", "c1")
	if err != nil {
		t.Fatalf("GetThreadState(after r2) error = %v", err)
	}
	if got.LastSeenReplyID != "r2" {
		t.Fatalf("upsert LastSeenReplyID = %q, want r2", got.LastSeenReplyID)
	}
	if !got.LastSeenReplyAt.Equal(secondSeen) {
		t.Fatalf("upsert LastSeenReplyAt = %v, want %v", got.LastSeenReplyAt, secondSeen)
	}
	if !got.AnsweredAt.Equal(firstAnsweredAt) {
		t.Fatalf("upsert AnsweredAt = %v, want unchanged %v", got.AnsweredAt, firstAnsweredAt)
	}

	states, err := store.ListThreadStates(ctx, "PR-1")
	if err != nil {
		t.Fatalf("ListThreadStates(PR-1) error = %v", err)
	}
	if len(states) != 1 {
		t.Fatalf("ListThreadStates(PR-1) = %#v, want 1 state", states)
	}
	if states[0].LastSeenReplyID != "r2" {
		t.Fatalf("ListThreadStates()[0].LastSeenReplyID = %q, want r2", states[0].LastSeenReplyID)
	}

	other, err := store.ListThreadStates(ctx, "PR-2")
	if err != nil {
		t.Fatalf("ListThreadStates(PR-2) error = %v", err)
	}
	if len(other) != 0 {
		t.Fatalf("ListThreadStates(PR-2) = %#v, want none", other)
	}
}
