package arcpr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/egv/yolo-runner/v2/internal/arcanum"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

// arcPRAuthorOrigin marks an implement item as an arcpr author-mode fan-out so
// its completion can be wired to comment resolution (see
// authorImplementMetadata).
const arcPRAuthorOrigin = "arcpr-author"

// implementSkippedStatus is the implement-result status the runner reports when
// the landing-applicability gate found the comment already handled. Must match
// runnerImplementSkippedStatus in cmd/yolo-agent.
const implementSkippedStatus = "skipped"

// finalizeCommentResolveIfComplete enqueues a resolve-pr-comment follow-up for a
// review comment once the author-mode implement item spawned to address it lands.
// It mirrors startrek's finalizeFollowUpIfSplitComplete: the comment's tracked
// implement items must ALL be complete before the comment is resolved.
//
// A comment is mapped 1:1 to a single implement item (task .9), so the comment's
// implement work is complete only when the item whose result we are handling is
// the comment's tracked implement item - the runner invokes HandleResult solely
// for completed items, so that item has landed. When the tracked item is still in
// flight (e.g. the comment was re-triaged and now tracks a newer item), a stale
// completion must not resolve the comment prematurely, so nothing is enqueued and
// the thread stays open until the tracked item lands.
//
// The resolve submission carries the implement item(s) as dependencies so it
// cannot run until they are done; its idempotency key (arcpr/<prID>/resolve/
// <commentID>/<revHash>) makes the enqueue idempotent across result retries.
func (s *Source) finalizeCommentResolveIfComplete(ctx context.Context, item workitem.Item, result workqueue.Result) ([]workqueue.Submission, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	payload, err := workitem.DecodeImplementPayload(item.Payload)
	if err != nil {
		return nil, fmt.Errorf("decode arc PR implement payload: %w", err)
	}
	metadata := payload.PromptContext.Metadata
	if strings.TrimSpace(metadata["origin"]) != arcPRAuthorOrigin {
		// Not an arcpr author-mode fan-out: nothing for this source to finalize.
		return nil, nil
	}
	commentIDs := workitem.ImplementCommentIDs(metadata)
	prID := fallbackText(metadata["arc_pr_id"], strings.TrimPrefix(strings.TrimSpace(item.SourceRef), "pr:"))
	if len(commentIDs) == 0 || prID == "" {
		return nil, nil
	}
	skipped := false
	if len(strings.TrimSpace(string(result.Payload))) > 0 {
		implementResult, err := workitem.DecodeImplementResult(result.Payload)
		if err != nil {
			return nil, fmt.Errorf("decode arc PR implement result for comments %v: %w", commentIDs, err)
		}
		skipped = strings.EqualFold(strings.TrimSpace(implementResult.Status), implementSkippedStatus)
	}
	if skipped {
		// The runner found the comments no longer applicable at landing time
		// (resolved, deleted, or already answered): nothing landed, so no reply
		// or thread resolution must be posted. Record them as answered so the
		// next triage does not enqueue the same obsolete work again.
		if err := s.State.StoreAnsweredCommentIDs(ctx, prID, commentIDs); err != nil {
			return nil, fmt.Errorf("record skipped arc PR comments %v as answered: %w", commentIDs, err)
		}
		return nil, nil
	}
	if err := s.verifyAuthorImplementPublished(ctx, prID); err != nil {
		return nil, fmt.Errorf("verify published active version before resolving comments %v: %w", commentIDs, err)
	}
	if s.Queue == nil {
		return nil, errors.New("arcpr source: work queue is required to enqueue comment resolve")
	}

	// One landed batch resolves every comment it covered: each tracked comment
	// gets its own dependency-gated resolve submission.
	var submissions []workqueue.Submission
	for _, commentID := range commentIDs {
		record, ok, err := s.GetCommentImplementItem(ctx, prID, commentID)
		if err != nil {
			return nil, err
		}
		if !ok {
			// The comment is not tracked for implement fan-out; leave it open.
			continue
		}
		siblingItemIDs := []string{strings.TrimSpace(record.ImplementItemID)}
		if !commentImplementItemsComplete(siblingItemIDs, item.ID) {
			// The comment was re-triaged and now tracks a newer item; a stale
			// completion must not resolve it prematurely.
			continue
		}
		submission, err := resolveCommentSubmission(s.Name(), prID, commentID, item, result)
		if err != nil {
			return nil, err
		}
		if _, err := s.Queue.EnqueueWithDeps(submission, siblingItemIDs); err != nil {
			return nil, fmt.Errorf("enqueue arc PR resolve for comment %q: %w", commentID, err)
		}
		submissions = append(submissions, submission)
	}
	return submissions, nil
}

func (s *Source) verifyAuthorImplementPublished(ctx context.Context, prID string) error {
	if s.PublicationVerifier != nil {
		return s.PublicationVerifier(ctx, prID)
	}
	// Unit-test sources can be intentionally API-free. The real arcpr command
	// always wires APIClient, so production writeback must verify publication.
	if s.APIClient == nil {
		return nil
	}
	return arcanum.VerifyActiveDiffSetPublishedWithClient(ctx, s.APIClient, prID)
}

// commentImplementItemsComplete reports whether every tracked implement item for
// a comment has landed. The runner only invokes HandleResult for completed items,
// so an item is done when it is the one whose result is being handled. With the
// 1:1 comment->item mapping the only sibling is the tracked item; if it is not
// the completing item it is still in flight, so the comment must stay open.
func commentImplementItemsComplete(siblingItemIDs []string, completingItemID string) bool {
	completingItemID = strings.TrimSpace(completingItemID)
	if completingItemID == "" || len(siblingItemIDs) == 0 {
		return false
	}
	for _, siblingID := range siblingItemIDs {
		if strings.TrimSpace(siblingID) != completingItemID {
			return false
		}
	}
	return true
}

// resolveCommentSubmission builds the resolve-pr-comment follow-up submission for
// a comment. The resolve idempotency key reuses the implement item's revision
// hash (the tail of its idempotency key) so the resolve is stamped to the same
// PR revision that spawned the implement work.
func resolveCommentSubmission(sourceName string, prID string, commentID string, item workitem.Item, result workqueue.Result) (workqueue.Submission, error) {
	revHash, err := resolveCommentRevisionHash(item.IdempotencyKey)
	if err != nil {
		return workqueue.Submission{}, fmt.Errorf("derive arc PR resolve revision hash for comment %q: %w", commentID, err)
	}
	implementPayload, err := workitem.DecodeImplementPayload(item.Payload)
	if err != nil {
		return workqueue.Submission{}, fmt.Errorf("decode arc PR implement payload for comment %q: %w", commentID, err)
	}
	implementResult, err := workitem.DecodeImplementResult(result.Payload)
	if err != nil {
		return workqueue.Submission{}, fmt.Errorf("decode arc PR implement result for comment %q: %w", commentID, err)
	}
	payload, err := json.Marshal(workitem.ResolvePRCommentPayload{
		PRID:      prID,
		CommentID: commentID,
		ReplyBody: authorImplementCompletionReply(implementPayload, implementResult),
	})
	if err != nil {
		return workqueue.Submission{}, fmt.Errorf("encode arc PR resolve submission for comment %q: %w", commentID, err)
	}
	return workqueue.Submission{
		Kind:           workitem.KindResolvePRComment,
		Source:         sourceName,
		SourceRef:      "pr:" + prID,
		IdempotencyKey: "arcpr/" + prID + "/resolve/" + commentID + "/" + revHash,
		Preset:         strings.TrimSpace(item.Preset),
		Priority:       item.Priority,
		Payload:        payload,
		MaxAttempts:    item.MaxAttempts,
	}, nil
}

func authorImplementCompletionReply(payload workitem.ImplementPayload, result workitem.ImplementResult) string {
	if commitSHA := strings.TrimSpace(result.CommitSHA); commitSHA != "" {
		return "Fixed in `" + commitSHA + "`."
	}
	if title := strings.TrimSpace(payload.Title); title != "" {
		return "Implemented: " + title + "."
	}
	return "Implemented the requested change."
}

// resolveCommentRevisionHash extracts the revision hash from an implement item's
// idempotency key (arcpr/<prID>/implement/<commentID>/<revHash>) so the resolve
// follow-up shares the implement work's revision stamp.
func resolveCommentRevisionHash(implementKey string) (string, error) {
	implementKey = strings.TrimSpace(implementKey)
	parts := strings.Split(implementKey, "/")
	if len(parts) != 5 || parts[0] != "arcpr" || parts[2] != "implement" {
		return "", fmt.Errorf("arc PR implement idempotency key %q must match arcpr/<prID>/implement/<commentID>/<rev>", implementKey)
	}
	return strings.TrimSpace(parts[4]), nil
}
