package arcpr

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/egv/yolo-runner/v2/internal/arcanum"
	"github.com/egv/yolo-runner/v2/internal/arcreview"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

func (s *Source) HandleResult(ctx context.Context, item workitem.Item, result workqueue.Result) (submissions []workqueue.Submission, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if item.Kind != workitem.KindPRReview && item.Kind != workitem.KindResolvePRComment && item.Kind != workitem.KindImplement {
		return nil, nil
	}
	if s == nil {
		return nil, errors.New("arcpr source is required")
	}
	if s.State == nil {
		return nil, errors.New("arcpr source state store is required")
	}
	if result.Status != "" && result.Status != workqueue.ResultStatusCompleted {
		return nil, nil
	}

	if item.Kind == workitem.KindResolvePRComment {
		return s.handleResolvePRCommentResult(ctx, item)
	}

	// An author-mode implement item (origin arcpr-author) resolves its review
	// comment only after it lands. finalizeCommentResolveIfComplete is a no-op
	// for any other implement item, so routing all KindImplement results through
	// it is safe; blocked/failed results were screened out by the completed-status
	// guard above, so a comment is never resolved against a failed implementation.
	if item.Kind == workitem.KindImplement {
		return s.finalizeCommentResolveIfComplete(ctx, item, result)
	}

	payload, err := workitem.DecodePRReviewPayload(item.Payload)
	if err != nil {
		return nil, err
	}
	resultPayload, err := workitem.DecodePRReviewResult(result.Payload)
	if err != nil {
		return nil, err
	}

	prID := fallbackText(payload.PRID, strings.TrimPrefix(strings.TrimSpace(item.SourceRef), "pr:"))
	if prID == "" {
		return nil, errors.New("arc PR ID is required")
	}

	state, workspace, cleanup, err := s.fetchWritebackState(ctx, prID)
	if err != nil {
		return nil, err
	}
	if cleanup != nil {
		defer func() {
			if cleanupErr := cleanup(); cleanupErr != nil {
				if err != nil {
					err = errors.Join(err, cleanupErr)
					return
				}
				err = cleanupErr
			}
		}()
	}
	gateStateBase := stateWithWritebackIdentity(state, prID, "")
	writebackState := stateWithWritebackIdentity(state, prID, payload.Revision)

	replies := resultPayload.Replies
	var arguedCommentIDs []string
	var resolveReplies []workitem.PRReviewReply
	var implementSubmissions []workqueue.Submission
	if payload.Mode == workitem.PRReviewModeAuthor && s.AuthorModeEnabled {
		argueReplies := authorArgueReplies(resultPayload.CommentDecisions, state.Details.Author, s.AutoArgueEnabled)
		for _, reply := range argueReplies {
			arguedCommentIDs = append(arguedCommentIDs, reply.CommentID)
			replies = append(replies, reply)
		}
		resolveReplies = authorResolveReplies(resultPayload.CommentDecisions, state.Details.Author, s.ResolveEnabled)
		implementSubmissions, err = s.enqueueAuthorImplementSubmissions(ctx, item, prID, payload, state, resultPayload)
		if err != nil {
			return nil, err
		}
	}

	repliedCommentIDs := resultReplyCommentIDs(replies)
	if len(replies) > 0 {
		if err := s.applyPRReviewReplies(ctx, writebackState, replies); err != nil {
			return nil, fmt.Errorf("apply arc PR replies: %w", err)
		}
		if err := s.State.StoreAnsweredCommentIDs(ctx, prID, repliedCommentIDs); err != nil {
			return nil, err
		}
	}

	// Author-mode argue replies close out their threads: mark them answered so a
	// reviewer's genuinely new reply (not a self reply) re-surfaces the thread.
	for _, commentID := range arguedCommentIDs {
		if err := s.State.RecordThreadAnswered(ctx, prID, commentID, "", time.Now()); err != nil {
			return nil, fmt.Errorf("record arc PR thread answered for %q: %w", commentID, err)
		}
	}

	// Author-mode resolve decisions post their disclosure-footer'd reply and
	// resolve the comment via the resolve applier. They are kept out of `replies`
	// above: the reply and resolve appliers share the answered gate, so routing
	// the resolve reply through the reply applier would short-circuit the resolve
	// applier (and vice versa) - see applyPRReviewResolveDecisions.
	if len(resolveReplies) > 0 {
		if err := s.applyPRReviewResolveDecisions(ctx, writebackState, resolveReplies); err != nil {
			return nil, fmt.Errorf("apply arc PR resolve decisions: %w", err)
		}
	}

	reviewedRevision := strings.TrimSpace(resultPayload.RevisionReviewed)
	if reviewedRevision != "" {
		if err := s.applyPRReview(ctx, writebackState, prID, reviewedRevision, resultPayload); err != nil {
			return nil, fmt.Errorf("apply arc PR review: %w", err)
		}
		if err := s.State.StoreReviewedRevision(ctx, prID, reviewedRevision); err != nil {
			return nil, err
		}
	}

	if resultPayload.ShipReady {
		gateState, err := s.shipGateState(ctx, gateStateBase, prID, repliedCommentIDs)
		if err != nil {
			return nil, err
		}
		if reviewedRevision == "" {
			reviewedRevision, err = s.State.GetReviewedRevision(ctx, prID)
			if err != nil {
				return nil, err
			}
		}
		if err := s.gateAndShip(ctx, workspace, gateState, reviewedRevision, payload, resultPayload); err != nil {
			return nil, fmt.Errorf("gate and ship arc PR: %w", err)
		}
	}

	return implementSubmissions, nil
}

// handleResolvePRCommentResult consumes a resolve-pr-comment result: it decodes
// the payload, fetches writeback state for the PR, posts the implementation
// reply when supplied, and resolves the single comment. A completion reply is
// still useful when another actor resolved the thread first, so it is gated on
// a reply previously recorded by this runner rather than the remote issue
// status.
func (s *Source) handleResolvePRCommentResult(ctx context.Context, item workitem.Item) (submissions []workqueue.Submission, err error) {
	payload, err := workitem.DecodeResolvePRCommentPayload(item.Payload)
	if err != nil {
		return nil, err
	}
	prID := fallbackText(payload.PRID, strings.TrimPrefix(strings.TrimSpace(item.SourceRef), "pr:"))
	if prID == "" {
		return nil, errors.New("arc PR ID is required")
	}
	state, cleanup, err := s.fetchResolvePRCommentState(ctx, prID)
	if err != nil {
		return nil, err
	}
	if cleanup != nil {
		defer func() {
			if cleanupErr := cleanup(); cleanupErr != nil {
				if err != nil {
					err = errors.Join(err, cleanupErr)
					return
				}
				err = cleanupErr
			}
		}()
	}
	writebackState := stateWithWritebackIdentity(state, prID, "")
	commentID := strings.TrimSpace(payload.CommentID)
	if replyBody := strings.TrimSpace(payload.ReplyBody); replyBody != "" {
		answered, err := s.persistedAnsweredCommentSet(ctx, prID)
		if err != nil {
			return nil, err
		}
		if !answered[commentID] {
			replyClient, err := s.replyCommentClient()
			if err != nil {
				return nil, err
			}
			if err := replyClient.PostCommentReply(ctx, prID, commentID, arcreview.WithDisclosureFooter(replyBody, state.Details.Author)); err != nil {
				return nil, fmt.Errorf("post arc PR implementation reply %q: %w", payload.CommentID, err)
			}
			// ResolveApplier persists an answered marker after it resolves an open
			// thread. When the comment was already resolved by another actor it
			// deliberately skips that call, so persist the reply here to make an
			// explicit replay idempotent.
			if commentIsResolved(state.Comments, commentID) {
				if err := s.State.StoreAnsweredCommentIDs(ctx, prID, []string{commentID}); err != nil {
					return nil, fmt.Errorf("store posted implementation reply %q: %w", commentID, err)
				}
			}
		}
	}

	resolvePayload, err := json.Marshal(arcreview.ResolveResult{ResolvedCommentIDs: []string{payload.CommentID}})
	if err != nil {
		return nil, fmt.Errorf("marshal arc PR resolve result: %w", err)
	}
	applier, err := s.resolveApplier()
	if err != nil {
		return nil, err
	}
	if _, err := applier.Apply(ctx, writebackState, resolvePayload); err != nil {
		return nil, fmt.Errorf("apply arc PR resolve: %w", err)
	}
	return nil, nil
}

// fetchResolvePRCommentState obtains only the state required to post a reply
// and resolve one comment. The usual writeback flow needs an Arc checkout for
// revision/check information, but comment resolution is served by the
// Arcanum API and must not contend with an active author implementation mount.
func (s *Source) fetchResolvePRCommentState(ctx context.Context, prID string) (arcreview.PRRuntimeState, func() error, error) {
	if s.StateFetcher != nil || len(s.writebackWorkspaces()) > 0 {
		state, _, cleanup, err := s.fetchWritebackState(ctx, prID)
		return state, cleanup, err
	}

	fetchComments := s.CommentFetcher
	if fetchComments == nil {
		fetchComments = arcanum.FetchPRComments
	}
	comments, err := fetchComments(ctx, prID)
	if err != nil {
		return arcreview.PRRuntimeState{}, nil, fmt.Errorf("fetch arc PR comments for %q: %w", prID, err)
	}
	return arcreview.PRRuntimeState{
		PRID: prID,
		Details: arcreview.PRDetails{
			ID:     prID,
			Author: strings.TrimSpace(s.Author),
		},
		Comments: comments,
	}, nil, nil
}

func (s *Source) fetchWritebackState(ctx context.Context, prID string) (arcreview.PRRuntimeState, string, func() error, error) {
	workspaces := s.writebackWorkspaces()
	fetcher := s.stateFetcher()
	if len(workspaces) == 0 {
		if s.StateFetcher == nil {
			checkout, err := arcanum.PreparePRCheckoutWithConfig(ctx, prID, arcanum.PRCheckoutConfig{
				ObjectsBaseDir: s.ObjectsBaseDir,
				MountsBaseDir:  s.MountsBaseDir,
			})
			if err != nil {
				return arcreview.PRRuntimeState{}, "", nil, fmt.Errorf("prepare arc PR checkout for %q: %w", prID, err)
			}
			if checkout == nil || strings.TrimSpace(checkout.MountPath) == "" {
				return arcreview.PRRuntimeState{}, "", nil, fmt.Errorf("prepare arc PR checkout for %q returned empty mount path", prID)
			}
			state, err := fetcher.FetchPRRuntimeState(ctx, checkout.MountPath, prID)
			if err != nil {
				if checkout.Cleanup != nil {
					err = errors.Join(err, checkout.Cleanup())
				}
				return arcreview.PRRuntimeState{}, "", nil, fmt.Errorf("fetch arc PR runtime state for %q: %w", prID, err)
			}
			return state, checkout.MountPath, checkout.Cleanup, nil
		}

		state, err := fetcher.FetchPRRuntimeState(ctx, "", prID)
		if err != nil {
			return arcreview.PRRuntimeState{}, "", nil, fmt.Errorf("fetch arc PR runtime state for %q: %w", prID, err)
		}
		return state, "", nil, nil
	}

	errs := make([]error, 0, len(workspaces))
	for _, workspace := range workspaces {
		state, err := fetcher.FetchPRRuntimeState(ctx, workspace, prID)
		if err == nil {
			return state, workspace, nil, nil
		}
		errs = append(errs, fmt.Errorf("workspace %q: %w", workspace, err))
	}
	return arcreview.PRRuntimeState{}, "", nil, fmt.Errorf("fetch arc PR runtime state for %q: %w", prID, errors.Join(errs...))
}

func (s *Source) writebackWorkspaces() []string {
	return normalizeStrings(append([]string{s.WritebackWorkspace}, s.WritebackWorkspaces...))
}

func (s *Source) applyPRReviewReplies(ctx context.Context, state arcreview.PRRuntimeState, replies []workitem.PRReviewReply) error {
	applier, err := s.replyApplier()
	if err != nil {
		return err
	}
	payload, err := json.Marshal(arcreview.ReplyResult{
		Replies: reviewRepliesFromResult(replies),
	})
	if err != nil {
		return fmt.Errorf("marshal reply result: %w", err)
	}
	_, err = applier.Apply(ctx, state, payload)
	return err
}

// applyPRReviewResolveDecisions posts the disclosure-footer'd reply for each
// author-mode "resolve" decision and then resolves the comment via the
// resolve applier. The reply and resolve appliers both gate on the shared
// answered set and both mark handled comments answered, so the resolve reply
// is posted directly here (rather than through the reply applier): routing it
// through the reply applier would mark the comment answered and make the
// resolve applier skip it, while resolving first would make the reply applier
// skip the reply. Comments already answered/resolved are skipped so a retry
// never double-posts or double-resolves.
func (s *Source) applyPRReviewResolveDecisions(ctx context.Context, state arcreview.PRRuntimeState, replies []workitem.PRReviewReply) error {
	if len(replies) == 0 {
		return nil
	}
	prID := currentStatePRID(state, "")
	if prID == "" {
		return errors.New("arc PR ID is required")
	}
	handled, err := s.handledCommentSet(ctx, prID, state.Comments)
	if err != nil {
		return err
	}
	replyClient, err := s.replyCommentClient()
	if err != nil {
		return err
	}
	var resolveIDs []string
	for _, reply := range replies {
		commentID := strings.TrimSpace(reply.CommentID)
		if commentID == "" {
			return errors.New("arc PR resolve reply comment ID is required")
		}
		if handled[commentID] {
			continue
		}
		body := strings.TrimSpace(reply.Body)
		if body == "" {
			return fmt.Errorf("arc PR resolve reply body is required for comment %q", commentID)
		}
		if err := replyClient.PostCommentReply(ctx, prID, commentID, body); err != nil {
			return fmt.Errorf("post arc PR resolve reply %q: %w", commentID, err)
		}
		resolveIDs = append(resolveIDs, commentID)
	}
	if len(resolveIDs) == 0 {
		return nil
	}
	applier, err := s.resolveApplier()
	if err != nil {
		return err
	}
	payload, err := json.Marshal(arcreview.ResolveResult{ResolvedCommentIDs: resolveIDs})
	if err != nil {
		return fmt.Errorf("marshal arc PR resolve result: %w", err)
	}
	if _, err := applier.Apply(ctx, state, payload); err != nil {
		return fmt.Errorf("apply arc PR resolve: %w", err)
	}
	return nil
}

// replyCommentClient returns the Arcanum reply client backing the reply
// applier, used to post resolve replies directly (see
// applyPRReviewResolveDecisions).
func (s *Source) replyCommentClient() (arcreview.ReplyArcanumClient, error) {
	applier, err := s.replyApplier()
	if err != nil {
		return nil, err
	}
	replyApplier, ok := applier.(arcreview.ReplyApplier)
	if !ok || replyApplier.Client == nil {
		return nil, errors.New("arcpr source: reply Arcanum client is required to post resolve replies")
	}
	return replyApplier.Client, nil
}

// handledCommentSet mirrors the resolve applier's gating set: persisted
// answered IDs plus comments flagged Answered or Resolved in the fetched
// state. Resolve decisions skip these so a retry never double-posts or
// double-resolves.
func (s *Source) handledCommentSet(ctx context.Context, prID string, comments []arcreview.PRComment) (map[string]bool, error) {
	handled, err := s.persistedAnsweredCommentSet(ctx, prID)
	if err != nil {
		return nil, err
	}
	for _, comment := range comments {
		id := strings.TrimSpace(comment.ID)
		if id != "" && (comment.Answered || comment.Resolved) {
			handled[id] = true
		}
	}
	return handled, nil
}

func (s *Source) persistedAnsweredCommentSet(ctx context.Context, prID string) (map[string]bool, error) {
	answeredIDs, err := s.State.ListAnsweredCommentIDs(ctx, prID)
	if err != nil {
		return nil, fmt.Errorf("list answered comment IDs: %w", err)
	}
	answered := make(map[string]bool, len(answeredIDs))
	for _, id := range answeredIDs {
		if id = strings.TrimSpace(id); id != "" {
			answered[id] = true
		}
	}
	return answered, nil
}

func commentIsResolved(comments []arcreview.PRComment, commentID string) bool {
	commentID = strings.TrimSpace(commentID)
	for _, comment := range comments {
		if strings.TrimSpace(comment.ID) == commentID {
			return comment.Resolved
		}
	}
	return false
}

// authorResolveReplies builds disclosure-footer'd replies for each "resolve"
// decision when resolveEnabled is set. The caller gates entry on author mode
// (payload.Mode == PRReviewModeAuthor && AuthorModeEnabled); resolveEnabled
// further opts the resolve disposition into posting and resolving. Argue and
// implement decisions are handled by other writeback paths and are ignored
// here.
func authorResolveReplies(decisions []workitem.PRReviewCommentDecision, author string, resolveEnabled bool) []workitem.PRReviewReply {
	if !resolveEnabled {
		return nil
	}
	var replies []workitem.PRReviewReply
	for _, decision := range decisions {
		if strings.TrimSpace(decision.Decision) != workitem.PRReviewCommentDecisionResolve {
			continue
		}
		commentID := strings.TrimSpace(decision.CommentID)
		if commentID == "" || strings.TrimSpace(decision.ReplyBody) == "" {
			continue
		}
		replies = append(replies, workitem.PRReviewReply{
			CommentID: commentID,
			Body:      arcreview.WithDisclosureFooter(decision.ReplyBody, author),
		})
	}
	return replies
}

// authorArgueReplies builds disclosure-footer'd replies for each "argue"
// decision when autoArgueEnabled is set. The caller gates entry on author mode
// (payload.Mode == PRReviewModeAuthor && AuthorModeEnabled); autoArgueEnabled
// further opts the argue disposition into posting. Resolve and implement
// decisions are handled by other writeback paths and are ignored here.
func authorArgueReplies(decisions []workitem.PRReviewCommentDecision, author string, autoArgueEnabled bool) []workitem.PRReviewReply {
	if !autoArgueEnabled {
		return nil
	}
	var replies []workitem.PRReviewReply
	for _, decision := range decisions {
		if strings.TrimSpace(decision.Decision) != workitem.PRReviewCommentDecisionArgue {
			continue
		}
		commentID := strings.TrimSpace(decision.CommentID)
		if commentID == "" || strings.TrimSpace(decision.ReplyBody) == "" {
			continue
		}
		replies = append(replies, workitem.PRReviewReply{
			CommentID: commentID,
			Body:      arcreview.WithDisclosureFooter(decision.ReplyBody, author),
		})
	}
	return replies
}

// enqueueAuthorImplementSubmissions fans out one implement work item per
// author-mode "implement" decision. Each item carries the PR, comment, branch,
// and author metadata the runner needs to land the fix on the PR branch (task
// 13); the comment -> implement-item mapping is recorded (task 9) so the comment
// is resolved only after the item lands (task 11). The caller gates entry on
// author mode (payload.Mode == PRReviewModeAuthor && AuthorModeEnabled);
// implementFanOut further opts the implement disposition into fanning out. The
// comment is NOT resolved here - resolution happens after the implement item
// lands.
func (s *Source) enqueueAuthorImplementSubmissions(ctx context.Context, item workitem.Item, prID string, payload workitem.PRReviewPayload, state arcreview.PRRuntimeState, result workitem.PRReviewResult) ([]workqueue.Submission, error) {
	if !s.ImplementFanOutEnabled {
		return nil, nil
	}
	decisions := authorImplementDecisions(result.CommentDecisions)
	if len(decisions) == 0 {
		return nil, nil
	}
	if s.Queue == nil {
		return nil, errors.New("arcpr source: work queue is required to fan out author-mode implement tasks")
	}
	branch := strings.TrimSpace(state.Details.Branch)
	author := strings.TrimSpace(state.Details.Author)
	revHash := revisionHash(payload.Revision)
	submissions := make([]workqueue.Submission, 0, len(decisions))
	for _, decision := range decisions {
		commentID := strings.TrimSpace(decision.CommentID)
		if commentID == "" {
			return nil, errors.New("arc PR implement decision comment ID is required")
		}
		previous, hadPrevious, err := s.GetCommentImplementItem(ctx, prID, commentID)
		if err != nil {
			return nil, err
		}
		submission, err := authorImplementSubmission(s.Name(), prID, commentID, revHash, item, decision, branch, author)
		if err != nil {
			return nil, err
		}
		queued, err := s.Queue.EnqueueWithDeps(submission, nil)
		if err != nil {
			return nil, fmt.Errorf("enqueue arc PR implement item for comment %q: %w", commentID, err)
		}
		if err := s.RecordCommentImplementItem(ctx, CommentImplementItemRecord{
			PRID:            prID,
			CommentID:       commentID,
			ImplementItemID: queued.ID,
			IdempotencyKey:  submission.IdempotencyKey,
			ReviewItemID:    item.ID,
		}); err != nil {
			return nil, err
		}
		if hadPrevious && strings.TrimSpace(previous.ImplementItemID) != "" && previous.ImplementItemID != queued.ID {
			if _, err := s.Queue.CancelPendingItem(previous.ImplementItemID); err != nil {
				return nil, fmt.Errorf("cancel superseded arc PR implement item %q for comment %q: %w", previous.ImplementItemID, commentID, err)
			}
		}
		submissions = append(submissions, submission)
	}
	return submissions, nil
}

// authorImplementSubmission builds the implement work item submission for a
// single "implement" decision. Title and Description come from the decision
// scope; the prompt metadata carries the Arc PR context the runner needs to land
// the fix on the PR branch.
func authorImplementSubmission(sourceName string, prID string, commentID string, revHash string, item workitem.Item, decision workitem.PRReviewCommentDecision, branch string, author string) (workqueue.Submission, error) {
	title, description := authorImplementScopeText(decision.Scope, commentID)
	payload, err := json.Marshal(workitem.ImplementPayload{
		Title:       title,
		Description: description,
		PromptContext: workitem.ImplementPromptContext{
			Metadata: authorImplementMetadata(prID, commentID, branch, author),
		},
	})
	if err != nil {
		return workqueue.Submission{}, fmt.Errorf("encode arc PR implement submission for comment %q: %w", commentID, err)
	}
	return workqueue.Submission{
		Kind:           workitem.KindImplement,
		Source:         sourceName,
		SourceRef:      "pr:" + prID,
		IdempotencyKey: "arcpr/" + prID + "/implement/" + commentID + "/" + revHash,
		Preset:         strings.TrimSpace(item.Preset),
		Priority:       item.Priority,
		Payload:        payload,
		MaxAttempts:    item.MaxAttempts,
	}, nil
}

// authorImplementDecisions selects the "implement" dispositions with a usable
// comment ID. Resolve and argue decisions are handled by other writeback paths
// and are ignored here.
func authorImplementDecisions(decisions []workitem.PRReviewCommentDecision) []workitem.PRReviewCommentDecision {
	var implement []workitem.PRReviewCommentDecision
	for _, decision := range decisions {
		if strings.TrimSpace(decision.Decision) != workitem.PRReviewCommentDecisionImplement {
			continue
		}
		if strings.TrimSpace(decision.CommentID) == "" {
			continue
		}
		implement = append(implement, decision)
	}
	return implement
}

// authorImplementScopeText renders the implement item's Title/Description from a
// decision scope, with a comment-derived fallback title when the scope omits one.
func authorImplementScopeText(scope *workitem.PRReviewImplementScope, commentID string) (string, string) {
	var title, instructions string
	if scope != nil {
		title = strings.TrimSpace(scope.Title)
		instructions = strings.TrimSpace(scope.Instructions)
	}
	if title == "" {
		title = "Address review comment " + commentID
	}
	return title, instructions
}

// authorImplementMetadata carries the Arc PR context the runner needs to land the
// fix on the PR branch and attribute it to the PR author. origin marks the item
// as an author-mode fan-out so downstream handling knows its lifecycle.
func authorImplementMetadata(prID string, commentID string, branch string, author string) map[string]string {
	metadata := map[string]string{
		"arc_pr_id":      prID,
		"arc_comment_id": commentID,
		"origin":         "arcpr-author",
	}
	if branch != "" {
		metadata["arc_pr_branch"] = branch
	}
	if author != "" {
		metadata["arc_pr_author"] = author
	}
	return metadata
}

// revisionHash returns a stable, slash-free digest of a PR revision for use in
// implement idempotency keys.
func revisionHash(revision string) string {
	revision = strings.TrimSpace(revision)
	if revision == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(revision))
	return hex.EncodeToString(sum[:])[:12]
}

func (s *Source) applyPRReview(ctx context.Context, state arcreview.PRRuntimeState, prID string, revision string, result workitem.PRReviewResult) error {
	storedRevision, err := s.State.GetReviewedRevision(ctx, prID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(storedRevision) == strings.TrimSpace(revision) {
		return nil
	}

	applier, err := s.reviewApplier()
	if err != nil {
		return err
	}
	payload, err := json.Marshal(arcreview.ReviewResult{
		Summary: prReviewSummary(result),
		Ship: arcreview.ReviewShipDecision{
			Verdict: strings.TrimSpace(result.ReviewVerdict),
			Reason:  strings.TrimSpace(result.ShipReason),
		},
	})
	if err != nil {
		return fmt.Errorf("marshal review result: %w", err)
	}
	_, err = applier.Apply(ctx, stateWithWritebackIdentity(state, prID, revision), payload)
	return err
}

func (s *Source) gateAndShip(ctx context.Context, workspace string, state arcreview.PRRuntimeState, reviewedRevision string, payload workitem.PRReviewPayload, result workitem.PRReviewResult) error {
	gate, err := s.shipGate(workspace)
	if err != nil {
		return err
	}
	_, err = gate.GateAndShip(ctx, arcreview.ShipGateRequest{
		State:             state,
		ReviewedRevision:  reviewedRevision,
		AllowShip:         payload.Ship,
		ModelShipDecision: arcreview.ReviewShipDecision{Verdict: strings.TrimSpace(result.ReviewVerdict)},
	})
	return err
}

func (s *Source) shipGateState(ctx context.Context, state arcreview.PRRuntimeState, prID string, repliedCommentIDs []string) (arcreview.PRRuntimeState, error) {
	answeredIDs, err := s.State.ListAnsweredCommentIDs(ctx, prID)
	if err != nil {
		return arcreview.PRRuntimeState{}, err
	}
	answeredIDs = append(answeredIDs, repliedCommentIDs...)
	return stateWithAnsweredComments(state, answeredIDs), nil
}

func (s *Source) replyApplier() (arcreview.PRReviewCycleReplyApplier, error) {
	if s.ReplyApplier != nil {
		return s.ReplyApplier, nil
	}
	apiClient, err := s.arcanumAPIClient()
	if err != nil {
		return nil, err
	}
	return arcreview.ReplyApplier{
		Client: arcanum.NewReplyArcanumClient(apiClient),
		Store:  s.State,
	}, nil
}

func (s *Source) reviewApplier() (arcreview.PRReviewCycleReviewApplier, error) {
	if s.ReviewApplier != nil {
		return s.ReviewApplier, nil
	}
	apiClient, err := s.arcanumAPIClient()
	if err != nil {
		return nil, err
	}
	return arcreview.ReviewApplier{
		Client: arcanum.NewReviewArcanumClient(apiClient),
		Store:  s.State,
	}, nil
}

func (s *Source) resolveApplier() (arcreview.PRReviewCycleResolveApplier, error) {
	if s.ResolveApplier != nil {
		return s.ResolveApplier, nil
	}
	apiClient, err := s.arcanumAPIClient()
	if err != nil {
		return nil, err
	}
	return arcreview.ResolveApplier{
		Client: arcanum.NewResolveArcanumClient(apiClient),
		Store:  s.State,
	}, nil
}

func (s *Source) shipGate(workspace string) (arcreview.PRReviewCycleShipGate, error) {
	if s.ShipGate != nil {
		return s.ShipGate, nil
	}
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return nil, errors.New("arcpr source workspace is required for shipping")
	}
	return arcreview.ShipGate{
		Client: arcanum.NewShipArcanumClient(workspace),
	}, nil
}

func (s *Source) arcanumAPIClient() (*arcanum.APIClient, error) {
	if s.APIClient != nil {
		return s.APIClient, nil
	}
	// No implicit production client: a Source that needs to post must be given
	// an explicit APIClient (or fake appliers). This prevents tests and local
	// runs that forget to inject one from silently writing to real Arcanum.
	return nil, errors.New("arcpr source: Arcanum API client must be configured explicitly (no implicit production client)")
}

func resultReplyCommentIDs(replies []workitem.PRReviewReply) []string {
	ids := make([]string, 0, len(replies))
	for _, reply := range replies {
		if id := strings.TrimSpace(reply.CommentID); id != "" {
			ids = append(ids, id)
		}
	}
	return normalizeStrings(ids)
}

func reviewRepliesFromResult(replies []workitem.PRReviewReply) []arcreview.ReviewReply {
	out := make([]arcreview.ReviewReply, 0, len(replies))
	for _, reply := range replies {
		out = append(out, arcreview.ReviewReply{
			CommentID: strings.TrimSpace(reply.CommentID),
			Body:      strings.TrimSpace(reply.Body),
		})
	}
	return out
}

func stateWithWritebackIdentity(state arcreview.PRRuntimeState, prID string, revision string) arcreview.PRRuntimeState {
	prID = fallbackText(prID, fallbackText(state.PRID, state.Details.ID))
	revision = fallbackText(revision, currentStateRevision(state))
	state.PRID = prID
	state.Revision = revision
	state.Details.ID = prID
	state.Details.Revision = fallbackText(state.Details.Revision, revision)
	return state
}

func stateWithAnsweredComments(state arcreview.PRRuntimeState, commentIDs []string) arcreview.PRRuntimeState {
	answered := map[string]bool{}
	for _, id := range commentIDs {
		if id := strings.TrimSpace(id); id != "" {
			answered[id] = true
		}
	}
	if len(answered) == 0 {
		return state
	}
	for i := range state.Comments {
		if answered[strings.TrimSpace(state.Comments[i].ID)] {
			state.Comments[i].Answered = true
		}
	}
	return state
}

// prReviewSummary renders the review summary comment posted to the PR. It uses
// the model's own summary and ship reason in natural language (not raw enum
// values or revision SHAs in the visible text), with the reviewed revision
// tracked in an HTML comment so re-reviews are detectable without polluting the
// readable body.
func prReviewSummary(result workitem.PRReviewResult) string {
	var b strings.Builder

	if summary := strings.TrimSpace(result.Summary); summary != "" {
		b.WriteString(summary)
	}

	if reason := strings.TrimSpace(result.ShipReason); reason != "" {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(reason)
	}

	verdictLine := prReviewVerdictLine(strings.TrimSpace(result.ReviewVerdict))
	if verdictLine != "" {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(verdictLine)
	}

	// Track the reviewed revision in an invisible HTML comment (same convention
	// as other automated reviewers) so re-reviews are detectable without
	// exposing the raw SHA in the readable body.
	if revision := strings.TrimSpace(result.RevisionReviewed); revision != "" {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("<!-- yolo-reviewer: reviewed_from_id=" + revision + " -->")
	}

	if b.Len() == 0 {
		return "Reviewed."
	}
	return b.String()
}

// prReviewVerdictLine translates the raw ship verdict into a short natural
// sentence. Returns empty for unknown/missing verdicts so no mechanical text is
// posted.
func prReviewVerdictLine(verdict string) string {
	switch strings.ToLower(verdict) {
	case "ship":
		return "По результатам ревью — шипуй."
	case "do_not_ship", "donotship", "do not ship":
		return "По результатам ревью — не к мержу, есть открытые замечания."
	default:
		return ""
	}
}
