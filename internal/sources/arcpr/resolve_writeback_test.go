package arcpr

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

// fanOutAuthorImplement enqueues a single author-mode implement item for
// comment-1 on PR 42 (revision r7) through the real writeback path and returns
// the concrete implement work item claimed from the queue plus its idempotency
// key. The implement item is left in the claimed (not yet done) state so callers
// can assert resolve dependency gating.
func fanOutAuthorImplement(t *testing.T, src *Source) (workitem.Item, string) {
	t.Helper()
	ctx := context.Background()

	reviewItem := workitem.Item{
		ID:        "review-item-1",
		Kind:      workitem.KindPRReview,
		SourceRef: "pr:42",
		Preset:    "adapta",
		Payload: mustMarshalArcPRWriteback(t, workitem.PRReviewPayload{
			PRID:     "42",
			Revision: "r7",
			Mode:     workitem.PRReviewModeAuthor,
		}),
	}
	reviewResult := workqueue.Result{
		Status: workqueue.ResultStatusCompleted,
		Payload: mustMarshalArcPRWriteback(t, workitem.PRReviewResult{
			CommentDecisions: []workitem.PRReviewCommentDecision{
				{
					CommentID: "comment-1",
					Decision:  workitem.PRReviewCommentDecisionImplement,
					Scope: &workitem.PRReviewImplementScope{
						Title:        "Add nil guard",
						Instructions: "Return early when the value is nil.",
						TargetFiles:  []string{"internal/foo/bar.go"},
					},
				},
			},
		}),
	}
	if _, err := src.HandleResult(ctx, reviewItem, reviewResult); err != nil {
		t.Fatalf("fan-out HandleResult() error = %v", err)
	}

	claimed, err := src.Queue.Claim("runner-a", []string{"adapta"}, time.Minute)
	if err != nil {
		t.Fatalf("Claim() implement item error = %v", err)
	}
	if claimed == nil {
		t.Fatalf("Claim() returned nil, want the spawned implement item")
	}
	if claimed.Kind != workitem.KindImplement {
		t.Fatalf("claimed kind = %q, want implement", claimed.Kind)
	}
	implementKey := "arcpr/42/implement/comment-1/dbb7b294e78f"
	if claimed.IdempotencyKey != implementKey {
		t.Fatalf("claimed implement idempotency key = %q, want %q", claimed.IdempotencyKey, implementKey)
	}
	return *claimed, implementKey
}

// All implement decisions of one triage land as ONE batched item (one agent
// run, one push, one Arcanum iteration), and its completion resolves EVERY
// covered comment with its own reply.
func TestFanOutBatchesDecisionsAndResolveCoversEveryComment(t *testing.T) {
	ctx := context.Background()
	client := &fakeArcPRWritebackClient{}
	src := arcPRAuthorImplementTestSource(t, client, true, true)

	reviewItem := workitem.Item{
		ID:        "review-item-1",
		Kind:      workitem.KindPRReview,
		SourceRef: "pr:42",
		Preset:    "adapta",
		Payload: mustMarshalArcPRWriteback(t, workitem.PRReviewPayload{
			PRID:     "42",
			Revision: "r7",
			Mode:     workitem.PRReviewModeAuthor,
		}),
	}
	reviewResult := workqueue.Result{
		Status: workqueue.ResultStatusCompleted,
		Payload: mustMarshalArcPRWriteback(t, workitem.PRReviewResult{
			CommentDecisions: []workitem.PRReviewCommentDecision{
				{
					CommentID: "comment-1",
					Decision:  workitem.PRReviewCommentDecisionImplement,
					Scope:     &workitem.PRReviewImplementScope{Title: "Add nil guard", Instructions: "Return early when nil."},
				},
				{
					CommentID: "comment-2",
					Decision:  workitem.PRReviewCommentDecisionImplement,
					Scope:     &workitem.PRReviewImplementScope{Title: "Fix filter", Instructions: "Apply the filter to the export endpoint too."},
				},
			},
		}),
	}
	submissions, err := src.HandleResult(ctx, reviewItem, reviewResult)
	if err != nil {
		t.Fatalf("HandleResult(review) error = %v", err)
	}
	if len(submissions) != 1 {
		t.Fatalf("fan-out submissions = %d, want ONE batched implement: %#v", len(submissions), submissions)
	}
	var implementPayload workitem.ImplementPayload
	if err := json.Unmarshal(submissions[0].Payload, &implementPayload); err != nil {
		t.Fatalf("unmarshal implement payload: %v", err)
	}
	meta := implementPayload.PromptContext.Metadata
	if meta["arc_comment_ids"] != "comment-1,comment-2" {
		t.Fatalf("arc_comment_ids = %q, want both comments", meta["arc_comment_ids"])
	}
	for _, want := range []string{"comment-1", "comment-2", "Add nil guard", "Fix filter", "Return early when nil.", "Apply the filter to the export endpoint too."} {
		if !strings.Contains(implementPayload.Description, want) {
			t.Fatalf("batched description missing %q:\n%s", want, implementPayload.Description)
		}
	}

	implementItem, err := src.Queue.Claim("runner-a", []string{"adapta"}, time.Minute)
	if err != nil || implementItem == nil {
		t.Fatalf("Claim() = %#v, %v; want the batched implement item", implementItem, err)
	}
	implementResult := workqueue.Result{
		Status: workqueue.ResultStatusCompleted,
		Payload: mustMarshalArcPRWriteback(t, workitem.ImplementResult{
			Status:    "completed",
			CommitSHA: "beefcafe",
		}),
	}
	resolves, err := src.HandleResult(ctx, *implementItem, implementResult)
	if err != nil {
		t.Fatalf("HandleResult(implement) error = %v", err)
	}
	if len(resolves) != 2 {
		t.Fatalf("resolve submissions = %d, want one per comment: %#v", len(resolves), resolves)
	}
	seen := map[string]bool{}
	for _, sub := range resolves {
		var rp workitem.ResolvePRCommentPayload
		if err := json.Unmarshal(sub.Payload, &rp); err != nil {
			t.Fatalf("unmarshal resolve payload: %v", err)
		}
		seen[rp.CommentID] = true
		if rp.ReplyBody != "Fixed in `beefcafe`." {
			t.Fatalf("resolve reply = %q, want Fixed in beefcafe", rp.ReplyBody)
		}
	}
	if !seen["comment-1"] || !seen["comment-2"] {
		t.Fatalf("resolves cover %v, want both comments", seen)
	}
}

// A skipped implement result means the runner's landing gate found the comment
// already handled (resolved, deleted, or answered): nothing landed, so no
// reply is posted and no resolve is enqueued. The comment is recorded as
// answered so the next triage does not enqueue the same obsolete work again.
func TestFinalizeCommentResolveSkippedImplementResolvesNothing(t *testing.T) {
	ctx := context.Background()
	client := &fakeArcPRWritebackClient{}
	src := arcPRAuthorImplementTestSource(t, client, true, true)

	implementItem, _ := fanOutAuthorImplement(t, src)
	skippedResult := workqueue.Result{
		Status: workqueue.ResultStatusCompleted,
		Payload: mustMarshalArcPRWriteback(t, workitem.ImplementResult{
			Status: "skipped",
			Reason: "comment comment-1 on PR 42 is already resolved",
		}),
	}

	submissions, err := src.HandleResult(ctx, implementItem, skippedResult)
	if err != nil {
		t.Fatalf("HandleResult(skipped implement) error = %v", err)
	}
	if len(submissions) != 0 {
		t.Fatalf("submissions = %#v, want none for a skipped implement", submissions)
	}
	if err := src.Queue.Complete(implementItem.ID, skippedResult); err != nil {
		t.Fatalf("Complete() implement item error = %v", err)
	}
	if claimed, err := src.Queue.Claim("runner-a", []string{"adapta"}, time.Minute); err != nil {
		t.Fatalf("Claim() after skipped implement error = %v", err)
	} else if claimed != nil {
		t.Fatalf("a resolve item was enqueued for a skipped implement: %#v", claimed)
	}

	answered, err := src.State.ListAnsweredCommentIDs(ctx, "42")
	if err != nil {
		t.Fatalf("ListAnsweredCommentIDs() error = %v", err)
	}
	found := false
	for _, id := range answered {
		if id == "comment-1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("answered comments = %#v, want comment-1 recorded to stop re-triage", answered)
	}
}

// TestFinalizeCommentResolveEnqueuesResolveWhenTrackedImplementItemLands asserts
// that completing the comment's tracked implement item enqueues exactly one
// KindResolvePRComment follow-up, dependency-gated on that implement item, and
// shaped with the comment's resolve idempotency key and payload.
func TestFinalizeCommentResolveEnqueuesResolveWhenTrackedImplementItemLands(t *testing.T) {
	ctx := context.Background()
	client := &fakeArcPRWritebackClient{}
	src := arcPRAuthorImplementTestSource(t, client, true, true)
	queue := src.Queue

	implementItem, _ := fanOutAuthorImplement(t, src)
	implementResult := workqueue.Result{
		Status: workqueue.ResultStatusCompleted,
		Payload: mustMarshalArcPRWriteback(t, workitem.ImplementResult{
			Status:    "completed",
			Branch:    "users/alice/pr-42",
			CommitSHA: "deadbeef",
		}),
	}

	// Handling the tracked implement item's completion enqueues the resolve.
	submissions, err := src.HandleResult(ctx, implementItem, implementResult)
	if err != nil {
		t.Fatalf("HandleResult(implement) error = %v", err)
	}
	if len(submissions) != 1 {
		t.Fatalf("submissions = %d, want 1: %#v", len(submissions), submissions)
	}
	got := submissions[0]
	wantResolveKey := "arcpr/42/resolve/comment-1/dbb7b294e78f"
	if got.Kind != workitem.KindResolvePRComment {
		t.Fatalf("submission kind = %q, want resolve-pr-comment", got.Kind)
	}
	if got.IdempotencyKey != wantResolveKey {
		t.Fatalf("submission idempotency key = %q, want %q", got.IdempotencyKey, wantResolveKey)
	}
	if got.Source != "arcpr-adapta" {
		t.Fatalf("submission source = %q, want arcpr-adapta", got.Source)
	}
	if got.SourceRef != "pr:42" {
		t.Fatalf("submission source ref = %q, want pr:42", got.SourceRef)
	}
	if got.Preset != "adapta" {
		t.Fatalf("submission preset = %q, want adapta", got.Preset)
	}
	var resolvePayload workitem.ResolvePRCommentPayload
	if err := json.Unmarshal(got.Payload, &resolvePayload); err != nil {
		t.Fatalf("unmarshal resolve payload: %v", err)
	}
	if resolvePayload.PRID != "42" || resolvePayload.CommentID != "comment-1" || resolvePayload.ReplyBody != "Fixed in `deadbeef`." {
		t.Fatalf("resolve payload = %#v, want reply for comment-1 in PR 42", resolvePayload)
	}

	// The resolve is dependency-gated on the implement item, which is still
	// claimed (not done), so it must not be claimable yet.
	if claimed, err := queue.Claim("runner-a", []string{"adapta"}, time.Minute); err != nil {
		t.Fatalf("Claim() before implement done error = %v", err)
	} else if claimed != nil {
		t.Fatalf("resolve was claimable before the implement item landed: %#v", claimed)
	}

	// Once the implement item lands (done), the resolve becomes claimable.
	if err := queue.Complete(implementItem.ID, implementResult); err != nil {
		t.Fatalf("Complete() implement item error = %v", err)
	}
	resolved, err := queue.Claim("runner-a", []string{"adapta"}, time.Minute)
	if err != nil {
		t.Fatalf("Claim() resolve after implement done error = %v", err)
	}
	if resolved == nil {
		t.Fatalf("Claim() returned nil, want the dependency-unblocked resolve item")
	}
	if resolved.Kind != workitem.KindResolvePRComment || resolved.IdempotencyKey != wantResolveKey {
		t.Fatalf("claimed resolve = kind %q key %q, want resolve-pr-comment %q", resolved.Kind, resolved.IdempotencyKey, wantResolveKey)
	}

	// The comment is not resolved until the resolve-pr-comment item itself runs.
	if len(client.resolved) != 0 {
		t.Fatalf("comment resolved before the resolve item ran, want none: %#v", client.resolved)
	}
}

func TestFinalizeCommentResolveWaitsForPublishedVersion(t *testing.T) {
	ctx := context.Background()
	client := &fakeArcPRWritebackClient{}
	src := arcPRAuthorImplementTestSource(t, client, true, true)
	src.PublicationVerifier = func(context.Context, string) error {
		return errors.New("active diff set is draft")
	}
	implementItem, _ := fanOutAuthorImplement(t, src)
	result := workqueue.Result{
		Status: workqueue.ResultStatusCompleted,
		Payload: mustMarshalArcPRWriteback(t, workitem.ImplementResult{
			Status:    "completed",
			CommitSHA: "deadbeef",
		}),
	}

	if _, err := src.HandleResult(ctx, implementItem, result); err == nil {
		t.Fatal("HandleResult() error = nil, want publication gate failure")
	}
	if claimed, err := src.Queue.Claim("runner-b", []string{"adapta"}, time.Minute); err != nil {
		t.Fatalf("Claim() error = %v", err)
	} else if claimed != nil {
		t.Fatalf("resolve was enqueued before publication: %#v", claimed)
	}
}

// TestFinalizeCommentResolveSkipsWhenTrackedImplementItemNotLanded asserts that a
// completion for an implement item that is NOT the comment's tracked item enqueues
// nothing: the comment's tracked implement item is still in flight, so resolving
// now would close the thread prematurely.
func TestFinalizeCommentResolveSkipsWhenTrackedImplementItemNotLanded(t *testing.T) {
	ctx := context.Background()
	client := &fakeArcPRWritebackClient{}
	src := arcPRAuthorImplementTestSource(t, client, true, true)
	queue := src.Queue

	// Fan out the tracked implement item for comment-1; it stays in flight.
	fanOutAuthorImplement(t, src)

	// A different implement item referencing comment-1 completes while the
	// tracked item is still pending.
	otherItem := workitem.Item{
		ID:        "implement-item-other",
		Kind:      workitem.KindImplement,
		SourceRef: "pr:42",
		Preset:    "adapta",
		Payload: mustMarshalArcPRWriteback(t, workitem.ImplementPayload{
			PromptContext: workitem.ImplementPromptContext{
				Metadata: map[string]string{
					"arc_pr_id":      "42",
					"arc_comment_id": "comment-1",
					"origin":         "arcpr-author",
				},
			},
		}),
	}
	submissions, err := src.HandleResult(ctx, otherItem, workqueue.Result{Status: workqueue.ResultStatusCompleted})
	if err != nil {
		t.Fatalf("HandleResult(other implement) error = %v", err)
	}
	if len(submissions) != 0 {
		t.Fatalf("submissions = %#v, want none while the tracked item is in flight", submissions)
	}
	if claimed, err := queue.Claim("runner-a", []string{"adapta"}, time.Minute); err != nil {
		t.Fatalf("Claim() error = %v", err)
	} else if claimed != nil && claimed.Kind == workitem.KindResolvePRComment {
		t.Fatalf("resolve enqueued while the tracked implement item is in flight: %#v", claimed)
	}
	if len(client.resolved) != 0 {
		t.Fatalf("comment resolved prematurely: %#v", client.resolved)
	}
}

// TestFinalizeCommentResolveIgnoresNonAuthorImplement asserts that a completed
// implement item not spawned by arcpr author-mode (no arcpr-author origin) is a
// no-op: nothing is enqueued and nothing is resolved.
func TestFinalizeCommentResolveIgnoresNonAuthorImplement(t *testing.T) {
	ctx := context.Background()
	client := &fakeArcPRWritebackClient{}
	src := arcPRAuthorImplementTestSource(t, client, true, true)
	queue := src.Queue

	item := workitem.Item{
		ID:        "implement-item-plain",
		Kind:      workitem.KindImplement,
		SourceRef: "pr:42",
		Preset:    "adapta",
		Payload: mustMarshalArcPRWriteback(t, workitem.ImplementPayload{
			PromptContext: workitem.ImplementPromptContext{
				Metadata: map[string]string{
					"arc_pr_id":      "42",
					"arc_comment_id": "comment-1",
					// origin intentionally absent: this is not an arcpr-author fan-out.
				},
			},
		}),
	}
	submissions, err := src.HandleResult(ctx, item, workqueue.Result{Status: workqueue.ResultStatusCompleted})
	if err != nil {
		t.Fatalf("HandleResult() error = %v", err)
	}
	if len(submissions) != 0 {
		t.Fatalf("submissions = %#v, want none for non-author implement", submissions)
	}
	if claimed, err := queue.Claim("runner-a", []string{"adapta"}, time.Minute); err != nil {
		t.Fatalf("Claim() error = %v", err)
	} else if claimed != nil && claimed.Kind == workitem.KindResolvePRComment {
		t.Fatalf("resolve enqueued for non-author implement: %#v", claimed)
	}
}

// TestFinalizeCommentResolveSkipsUntrackedComment asserts that an arcpr-author
// implement completion for a comment with no recorded implement-item mapping
// enqueues nothing rather than resolving an untracked thread.
func TestFinalizeCommentResolveSkipsUntrackedComment(t *testing.T) {
	ctx := context.Background()
	client := &fakeArcPRWritebackClient{}
	src := arcPRAuthorImplementTestSource(t, client, true, true)
	queue := src.Queue

	item := workitem.Item{
		ID:        "implement-item-untracked",
		Kind:      workitem.KindImplement,
		SourceRef: "pr:42",
		Preset:    "adapta",
		Payload: mustMarshalArcPRWriteback(t, workitem.ImplementPayload{
			PromptContext: workitem.ImplementPromptContext{
				Metadata: map[string]string{
					"arc_pr_id":      "42",
					"arc_comment_id": "comment-untracked",
					"origin":         "arcpr-author",
				},
			},
		}),
	}
	submissions, err := src.HandleResult(ctx, item, workqueue.Result{Status: workqueue.ResultStatusCompleted})
	if err != nil {
		t.Fatalf("HandleResult() error = %v", err)
	}
	if len(submissions) != 0 {
		t.Fatalf("submissions = %#v, want none for untracked comment", submissions)
	}
	if claimed, err := queue.Claim("runner-a", []string{"adapta"}, time.Minute); err != nil {
		t.Fatalf("Claim() error = %v", err)
	} else if claimed != nil && claimed.Kind == workitem.KindResolvePRComment {
		t.Fatalf("resolve enqueued for untracked comment: %#v", claimed)
	}
}
