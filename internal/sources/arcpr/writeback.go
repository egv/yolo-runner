package arcpr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/egv/yolo-runner/v2/internal/arcanum"
	"github.com/egv/yolo-runner/v2/internal/arcreview"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

func (s *Source) HandleResult(ctx context.Context, item workitem.Item, result workqueue.Result) ([]workqueue.Submission, error) {
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

	state, workspace, err := s.fetchWritebackState(ctx, prID)
	if err != nil {
		return nil, err
	}
	gateStateBase := stateWithWritebackIdentity(state, prID, "")
	writebackState := stateWithWritebackIdentity(state, prID, payload.Revision)

	repliedCommentIDs := resultReplyCommentIDs(resultPayload.Replies)
	if len(resultPayload.Replies) > 0 {
		if err := s.applyPRReviewReplies(ctx, writebackState, resultPayload); err != nil {
			return nil, fmt.Errorf("apply arc PR replies: %w", err)
		}
		if err := s.State.StoreAnsweredCommentIDs(ctx, prID, repliedCommentIDs); err != nil {
			return nil, err
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

func (s *Source) fetchWritebackState(ctx context.Context, prID string) (arcreview.PRRuntimeState, string, error) {
	workspaces := s.writebackWorkspaces()
	if len(workspaces) == 0 && s.StateFetcher == nil {
		return arcreview.PRRuntimeState{}, "", errors.New("arcpr source writeback workspace is required")
	}

	fetcher := s.stateFetcher()
	if len(workspaces) == 0 {
		state, err := fetcher.FetchPRRuntimeState(ctx, "", prID)
		if err != nil {
			return arcreview.PRRuntimeState{}, "", fmt.Errorf("fetch arc PR runtime state for %q: %w", prID, err)
		}
		return state, "", nil
	}

	errs := make([]error, 0, len(workspaces))
	for _, workspace := range workspaces {
		state, err := fetcher.FetchPRRuntimeState(ctx, workspace, prID)
		if err == nil {
			return state, workspace, nil
		}
		errs = append(errs, fmt.Errorf("workspace %q: %w", workspace, err))
	}
	return arcreview.PRRuntimeState{}, "", fmt.Errorf("fetch arc PR runtime state for %q: %w", prID, errors.Join(errs...))
}

func (s *Source) writebackWorkspaces() []string {
	return normalizeStrings(append([]string{s.WritebackWorkspace}, s.WritebackWorkspaces...))
}

func (s *Source) applyPRReviewReplies(ctx context.Context, state arcreview.PRRuntimeState, result workitem.PRReviewResult) error {
	applier, err := s.replyApplier()
	if err != nil {
		return err
	}
	payload, err := json.Marshal(arcreview.ReplyResult{
		Replies: reviewRepliesFromResult(result.Replies),
	})
	if err != nil {
		return fmt.Errorf("marshal reply result: %w", err)
	}
	_, err = applier.Apply(ctx, state, payload)
	return err
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
	return arcanum.NewAPIClient(arcanum.APIClientConfig{})
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
