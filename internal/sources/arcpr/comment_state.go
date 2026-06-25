package arcpr

import (
	"context"
	"errors"

	arcreviewstate "github.com/egv/yolo-runner/v2/internal/arcreview/state"
)

// CommentImplementItemRecord maps a review comment to the implement work item
// spawned to address it, so the comment is resolved only after that implement
// item (and any sibling items for the same comment) land. Mirrors the startrek
// split_subtask_items comment->child mapping; the underlying persistence lives
// on the shared arc review state store (s.State).
type CommentImplementItemRecord = arcreviewstate.CommentImplementItemRecord

// RecordCommentImplementItem records (or upserts) the implement item mapped to a
// review comment for a PR. Re-recording the same (PR, comment) updates the
// implement item rather than inserting a duplicate.
func (s *Source) RecordCommentImplementItem(ctx context.Context, record CommentImplementItemRecord) error {
	if s == nil || s.State == nil {
		return errors.New("arcpr source state store is required")
	}
	return s.State.RecordCommentImplementItem(ctx, record)
}

// GetCommentImplementItem returns the implement item mapped to a review comment,
// mirroring the state store lookup. ok is false when no mapping is recorded.
func (s *Source) GetCommentImplementItem(ctx context.Context, prID string, commentID string) (CommentImplementItemRecord, bool, error) {
	if s == nil || s.State == nil {
		return CommentImplementItemRecord{}, false, errors.New("arcpr source state store is required")
	}
	return s.State.GetCommentImplementItem(ctx, prID, commentID)
}

// ListCommentImplementItems returns every comment->implement-item mapping recorded
// for a PR, ordered by comment ID.
func (s *Source) ListCommentImplementItems(ctx context.Context, prID string) ([]CommentImplementItemRecord, error) {
	if s == nil || s.State == nil {
		return nil, errors.New("arcpr source state store is required")
	}
	return s.State.ListCommentImplementItems(ctx, prID)
}
