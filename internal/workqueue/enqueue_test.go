package workqueue

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/egv/yolo-runner/v2/internal/workitem"
)

func TestEnqueueWithDepsDedupesByIdempotencyKeyAndWritesDeps(t *testing.T) {
	store := openEnqueueTestStore(t)

	depA, err := store.Enqueue(Submission{
		Kind:           workitem.KindImplement,
		Source:         "st-adapta",
		SourceRef:      "ADAPTABOT-10",
		IdempotencyKey: "st/ADAPTABOT-10/implement/1",
		Preset:         "adapta",
		Priority:       20,
		Payload:        json.RawMessage(`{"task_id":"ADAPTABOT-10"}`),
	})
	if err != nil {
		t.Fatalf("Enqueue(depA) error = %v", err)
	}
	depB, err := store.Enqueue(Submission{
		Kind:           workitem.KindImplement,
		Source:         "st-adapta",
		SourceRef:      "ADAPTABOT-11",
		IdempotencyKey: "st/ADAPTABOT-11/implement/1",
		Preset:         "adapta",
		Priority:       10,
		Payload:        json.RawMessage(`{"task_id":"ADAPTABOT-11"}`),
	})
	if err != nil {
		t.Fatalf("Enqueue(depB) error = %v", err)
	}

	first, err := store.EnqueueWithDeps(Submission{
		Kind:           workitem.KindImplement,
		Source:         "st-adapta",
		SourceRef:      "ADAPTABOT-12",
		IdempotencyKey: "st/ADAPTABOT-12/implement/1",
		Preset:         "adapta",
		Priority:       50,
		Payload:        json.RawMessage(`{"task_id":"ADAPTABOT-12"}`),
		MaxAttempts:    4,
	}, []string{depA.ID, depB.ID})
	if err != nil {
		t.Fatalf("EnqueueWithDeps(first) error = %v", err)
	}

	duplicate, err := store.EnqueueWithDeps(Submission{
		Kind:           workitem.KindImplement,
		Source:         "st-adapta",
		SourceRef:      "ADAPTABOT-12-changed",
		IdempotencyKey: "st/ADAPTABOT-12/implement/1",
		Preset:         "adapta",
		Priority:       1,
		Payload:        json.RawMessage(`{"task_id":"ADAPTABOT-12","changed":true}`),
	}, []string{depA.ID})
	if err != nil {
		t.Fatalf("EnqueueWithDeps(duplicate) error = %v", err)
	}

	if duplicate.ID != first.ID {
		t.Fatalf("duplicate ID = %q, want existing ID %q", duplicate.ID, first.ID)
	}
	if duplicate.SourceRef != first.SourceRef || duplicate.Priority != first.Priority || string(duplicate.Payload) != string(first.Payload) {
		t.Fatalf("duplicate returned changed item: got %#v, want original %#v", duplicate, first)
	}
	if duplicate.State != "pending" {
		t.Fatalf("duplicate state = %q, want pending", duplicate.State)
	}
	if duplicate.MaxAttempts != 4 {
		t.Fatalf("duplicate max attempts = %d, want original value 4", duplicate.MaxAttempts)
	}

	assertItemCountForKey(t, store, "st/ADAPTABOT-12/implement/1", 1)
	assertDepsForItem(t, store, first.ID, []string{depA.ID, depB.ID})

	if _, err := store.EnqueueWithDeps(Submission{
		Kind:           workitem.KindImplement,
		Source:         "st-adapta",
		SourceRef:      "ADAPTABOT-13",
		IdempotencyKey: "st/ADAPTABOT-13/implement/1",
		Preset:         "adapta",
		Payload:        json.RawMessage(`{"task_id":"ADAPTABOT-13"}`),
	}, []string{"missing-dependency"}); err == nil {
		t.Fatalf("EnqueueWithDeps(missing dependency) error = nil, want dependency failure")
	}
	assertItemCountForKey(t, store, "st/ADAPTABOT-13/implement/1", 0)
}

func TestEnqueueSupersedingPendingCancelsOnlyOlderAuthorPRReviews(t *testing.T) {
	store := openEnqueueTestStore(t)
	authorPayload := func(revision string) json.RawMessage {
		raw, err := json.Marshal(workitem.PRReviewPayload{PRID: "42", Revision: revision, Mode: workitem.PRReviewModeAuthor})
		if err != nil {
			t.Fatalf("marshal author payload: %v", err)
		}
		return raw
	}
	reviewerPayload := json.RawMessage(`{"pr_id":"42","revision":"reviewer-r1","ship":false}`)

	olderAuthor, err := store.Enqueue(Submission{
		Kind:           workitem.KindPRReview,
		Source:         "arcpr-adapta",
		SourceRef:      "pr:42",
		IdempotencyKey: "arcpr-adapta/pr-review/42/old/author",
		Preset:         "adapta",
		Payload:        authorPayload("old"),
	})
	if err != nil {
		t.Fatalf("enqueue older author review: %v", err)
	}
	reviewer, err := store.Enqueue(Submission{
		Kind:           workitem.KindPRReview,
		Source:         "arcpr-adapta",
		SourceRef:      "pr:42",
		IdempotencyKey: "arcpr-adapta/pr-review/42/reviewer",
		Preset:         "adapta",
		Payload:        reviewerPayload,
	})
	if err != nil {
		t.Fatalf("enqueue reviewer review: %v", err)
	}
	currentAuthor, err := store.Enqueue(Submission{
		Kind:             workitem.KindPRReview,
		Source:           "arcpr-adapta",
		SourceRef:        "pr:42",
		IdempotencyKey:   "arcpr-adapta/pr-review/42/new/author",
		Preset:           "adapta",
		Payload:          authorPayload("new"),
		SupersedePending: true,
	})
	if err != nil {
		t.Fatalf("enqueue current author review: %v", err)
	}

	for _, tc := range []struct {
		id   string
		want string
	}{
		{id: olderAuthor.ID, want: itemStateCancelled},
		{id: reviewer.ID, want: itemStatePending},
		{id: currentAuthor.ID, want: itemStatePending},
	} {
		detail, err := store.GetItem(tc.id)
		if err != nil {
			t.Fatalf("GetItem(%q): %v", tc.id, err)
		}
		if detail.Item.State != tc.want {
			t.Fatalf("item %q state = %q, want %q", tc.id, detail.Item.State, tc.want)
		}
	}
}

func openEnqueueTestStore(t *testing.T) *Store {
	t.Helper()

	store, err := Open(filepath.Join(t.TempDir(), "queue.db"))
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

func assertItemCountForKey(t *testing.T, store *Store, idempotencyKey string, want int) {
	t.Helper()

	var got int
	if err := store.db.QueryRow(
		"SELECT COUNT(*) FROM work_items WHERE idempotency_key = ?",
		idempotencyKey,
	).Scan(&got); err != nil {
		t.Fatalf("count work_items for idempotency key %q: %v", idempotencyKey, err)
	}
	if got != want {
		t.Fatalf("work_items count for idempotency key %q = %d, want %d", idempotencyKey, got, want)
	}
}

func assertDepsForItem(t *testing.T, store *Store, itemID string, want []string) {
	t.Helper()

	rows, err := store.db.Query(
		"SELECT depends_on FROM item_deps WHERE item_id = ? ORDER BY depends_on",
		itemID,
	)
	if err != nil {
		t.Fatalf("query item_deps for item %q: %v", itemID, err)
	}
	defer rows.Close()

	var got []string
	for rows.Next() {
		var dependsOn string
		if err := rows.Scan(&dependsOn); err != nil {
			t.Fatalf("scan item_deps for item %q: %v", itemID, err)
		}
		got = append(got, dependsOn)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read item_deps for item %q: %v", itemID, err)
	}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("deps for item %q = %#v, want %#v", itemID, got, want)
	}
}
