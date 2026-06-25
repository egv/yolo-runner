package arcpr

import (
	"context"
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
	if item.Kind != workitem.KindPRReview {
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
	if payload.Mode == workitem.PRReviewModeAuthor && s.AuthorModeEnabled {
		argueReplies := authorArgueReplies(resultPayload.CommentDecisions, state.Details.Author, s.AutoArgueEnabled)
		for _, reply := range argueReplies {
			arguedCommentIDs = append(arguedCommentIDs, reply.CommentID)
			replies = append(replies, reply)
		}
		resolveReplies = authorResolveReplies(resultPayload.CommentDecisions, state.Details.Author, s.ResolveEnabled)
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

	return nil, nil
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
	answeredIDs, err := s.State.ListAnsweredCommentIDs(ctx, prID)
	if err != nil {
		return nil, fmt.Errorf("list answered comment IDs: %w", err)
	}
	handled := make(map[string]bool, len(answeredIDs)+len(comments))
	for _, id := range answeredIDs {
		if id = strings.TrimSpace(id); id != "" {
			handled[id] = true
		}
	}
	for _, comment := range comments {
		id := strings.TrimSpace(comment.ID)
		if id != "" && (comment.Answered || comment.Resolved) {
			handled[id] = true
		}
	}
	return handled, nil
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
		Summary: prReviewSummary(result, revision),
		Ship: arcreview.ReviewShipDecision{
			Verdict: strings.TrimSpace(result.ReviewVerdict),
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

func prReviewSummary(result workitem.PRReviewResult, revision string) string {
	verdict := strings.TrimSpace(result.ReviewVerdict)
	if verdict == "" {
		verdict = "recorded"
	}
	revision = strings.TrimSpace(revision)
	if revision == "" {
		return "Automated PR review: " + verdict + "."
	}
	return fmt.Sprintf("Automated PR review for revision %s: %s.", revision, verdict)
}
