package state

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"

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
	assertTablesExist(t, store.db, "answered_comments", "reviewed_revisions")
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
	assertTablesExist(t, reopened.db, "answered_comments", "reviewed_revisions")
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
