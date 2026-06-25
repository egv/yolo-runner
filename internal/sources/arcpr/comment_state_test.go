package arcpr

import (
	"context"
	"reflect"
	"testing"
)

// newCommentStateSource builds an arcpr Source backed by a fresh on-disk state
// store so RecordCommentImplementItem / GetCommentImplementItem /
// ListCommentImplementItems exercise real SQL.
func newCommentStateSource(t *testing.T) *Source {
	t.Helper()
	return &Source{State: openDiscoveryTestState(t)}
}

func TestRecordAndGetCommentImplementItemRoundTrip(t *testing.T) {
	ctx := context.Background()
	src := newCommentStateSource(t)

	record := CommentImplementItemRecord{
		PRID:            "42",
		CommentID:       "comment-1",
		ImplementItemID: "implement-item-1",
		IdempotencyKey:  "idem-1",
		ReviewItemID:    "review-item-1",
	}
	if err := src.RecordCommentImplementItem(ctx, record); err != nil {
		t.Fatalf("RecordCommentImplementItem() error = %v", err)
	}

	got, ok, err := src.GetCommentImplementItem(ctx, "42", "comment-1")
	if err != nil {
		t.Fatalf("GetCommentImplementItem() error = %v", err)
	}
	if !ok {
		t.Fatalf("GetCommentImplementItem() = (_, false, _), want found")
	}
	if got.PRID != record.PRID || got.CommentID != record.CommentID {
		t.Fatalf("unexpected identity: got %#v", got)
	}
	if got.ImplementItemID != record.ImplementItemID {
		t.Fatalf("ImplementItemID = %q, want %q", got.ImplementItemID, record.ImplementItemID)
	}
	if got.IdempotencyKey != record.IdempotencyKey {
		t.Fatalf("IdempotencyKey = %q, want %q", got.IdempotencyKey, record.IdempotencyKey)
	}
	if got.ReviewItemID != record.ReviewItemID {
		t.Fatalf("ReviewItemID = %q, want %q", got.ReviewItemID, record.ReviewItemID)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Fatalf("expected populated timestamps, got created=%v updated=%v", got.CreatedAt, got.UpdatedAt)
	}
}

func TestRecordCommentImplementItemUpsertsImplementItemID(t *testing.T) {
	ctx := context.Background()
	src := newCommentStateSource(t)

	if err := src.RecordCommentImplementItem(ctx, CommentImplementItemRecord{
		PRID:            "42",
		CommentID:       "comment-1",
		ImplementItemID: "implement-item-1",
		IdempotencyKey:  "idem-1",
	}); err != nil {
		t.Fatalf("RecordCommentImplementItem(first) error = %v", err)
	}
	first, _, err := src.GetCommentImplementItem(ctx, "42", "comment-1")
	if err != nil {
		t.Fatalf("GetCommentImplementItem(first) error = %v", err)
	}

	// Re-recording the same (pr_id, comment_id) with a new implement item must
	// update the mapping rather than insert a duplicate.
	if err := src.RecordCommentImplementItem(ctx, CommentImplementItemRecord{
		PRID:            "42",
		CommentID:       "comment-1",
		ImplementItemID: "implement-item-2",
		IdempotencyKey:  "idem-2",
		ReviewItemID:    "review-item-1",
	}); err != nil {
		t.Fatalf("RecordCommentImplementItem(second) error = %v", err)
	}

	got, ok, err := src.GetCommentImplementItem(ctx, "42", "comment-1")
	if err != nil {
		t.Fatalf("GetCommentImplementItem(second) error = %v", err)
	}
	if !ok {
		t.Fatalf("GetCommentImplementItem(second) = (_, false, _), want found")
	}
	if got.ImplementItemID != "implement-item-2" {
		t.Fatalf("ImplementItemID = %q, want upserted %q", got.ImplementItemID, "implement-item-2")
	}
	if got.IdempotencyKey != "idem-2" {
		t.Fatalf("IdempotencyKey = %q, want upserted %q", got.IdempotencyKey, "idem-2")
	}
	if got.ReviewItemID != "review-item-1" {
		t.Fatalf("ReviewItemID = %q, want upserted %q", got.ReviewItemID, "review-item-1")
	}
	if !got.CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf("CreatedAt changed on upsert: first=%v second=%v", first.CreatedAt, got.CreatedAt)
	}
	if got.UpdatedAt.Before(first.UpdatedAt) {
		t.Fatalf("UpdatedAt not advanced on upsert: first=%v second=%v", first.UpdatedAt, got.UpdatedAt)
	}

	// A single row must remain for the comment.
	items, err := src.ListCommentImplementItems(ctx, "42")
	if err != nil {
		t.Fatalf("ListCommentImplementItems() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item after upsert, got %d", len(items))
	}
}

func TestListCommentImplementItemsReturnsAllForPR(t *testing.T) {
	ctx := context.Background()
	src := newCommentStateSource(t)

	records := []CommentImplementItemRecord{
		{PRID: "42", CommentID: "comment-2", ImplementItemID: "item-2", IdempotencyKey: "idem-2"},
		{PRID: "42", CommentID: "comment-1", ImplementItemID: "item-1", IdempotencyKey: "idem-1"},
	}
	for _, record := range records {
		if err := src.RecordCommentImplementItem(ctx, record); err != nil {
			t.Fatalf("RecordCommentImplementItem(%q) error = %v", record.CommentID, err)
		}
	}
	// A different PR must not leak into the listing.
	if err := src.RecordCommentImplementItem(ctx, CommentImplementItemRecord{
		PRID:            "7",
		CommentID:       "comment-1",
		ImplementItemID: "item-other",
		IdempotencyKey:  "idem-other",
	}); err != nil {
		t.Fatalf("RecordCommentImplementItem(other PR) error = %v", err)
	}

	got, err := src.ListCommentImplementItems(ctx, "42")
	if err != nil {
		t.Fatalf("ListCommentImplementItems() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 items for PR 42, got %d: %#v", len(got), got)
	}
	wantCommentIDs := []string{"comment-1", "comment-2"}
	gotCommentIDs := []string{got[0].CommentID, got[1].CommentID}
	if !reflect.DeepEqual(gotCommentIDs, wantCommentIDs) {
		t.Fatalf("comment IDs = %v, want %v (ordered, PR-scoped)", gotCommentIDs, wantCommentIDs)
	}
	for _, item := range got {
		if item.PRID != "42" {
			t.Fatalf("listed item leaked across PRs: %#v", item)
		}
	}
}

func TestGetCommentImplementItemMissingReturnsNotFound(t *testing.T) {
	ctx := context.Background()
	src := newCommentStateSource(t)

	got, ok, err := src.GetCommentImplementItem(ctx, "42", "absent")
	if err != nil {
		t.Fatalf("GetCommentImplementItem(missing) error = %v", err)
	}
	if ok {
		t.Fatalf("GetCommentImplementItem(missing) = (_, true, _), want not found")
	}
	if got != (CommentImplementItemRecord{}) {
		t.Fatalf("GetCommentImplementItem(missing) = %#v, want zero value", got)
	}
}
