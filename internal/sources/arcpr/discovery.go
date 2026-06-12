package arcpr

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/egv/yolo-runner/v2/internal/arcanum"
	"github.com/egv/yolo-runner/v2/internal/arcreview"
	arcreviewstate "github.com/egv/yolo-runner/v2/internal/arcreview/state"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

const defaultSourceName = "arcpr"

type PRLister interface {
	ListWorkspacePRs(ctx context.Context, workspace string) ([]arcanum.PRSummary, error)
}

type PRStateFetcher interface {
	FetchPRRuntimeState(ctx context.Context, workspace string, prID string) (arcreview.PRRuntimeState, error)
}

type PRListerFunc func(context.Context, string) ([]arcanum.PRSummary, error)

func (f PRListerFunc) ListWorkspacePRs(ctx context.Context, workspace string) ([]arcanum.PRSummary, error) {
	return f(ctx, workspace)
}

type PRStateFetcherFunc func(context.Context, string, string) (arcreview.PRRuntimeState, error)

func (f PRStateFetcherFunc) FetchPRRuntimeState(ctx context.Context, workspace string, prID string) (arcreview.PRRuntimeState, error) {
	return f(ctx, workspace, prID)
}

type Source struct {
	SourceName   string
	Preset       string
	Reviewer     string
	Workspaces   []string
	Branches     []string
	AllowShip    bool
	Priority     int
	MaxAttempts  int
	State        *arcreviewstate.Store
	Lister       PRLister
	StateFetcher PRStateFetcher
}

type discoveredPR struct {
	ID        string
	Workspace string
	Branch    string
}

type arcanumPRLister struct{}

func (arcanumPRLister) ListWorkspacePRs(ctx context.Context, workspace string) ([]arcanum.PRSummary, error) {
	return arcanum.ListWorkspacePRs(ctx, workspace)
}

type arcanumPRStateFetcher struct{}

func (arcanumPRStateFetcher) FetchPRRuntimeState(ctx context.Context, workspace string, prID string) (arcreview.PRRuntimeState, error) {
	return arcanum.FetchPRRuntimeState(ctx, workspace, prID)
}

func (s *Source) Name() string {
	return fallbackText(s.SourceName, defaultSourceName)
}

func (s *Source) Poll(ctx context.Context) ([]workqueue.Submission, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil {
		return nil, errors.New("arcpr source is required")
	}
	if s.State == nil {
		return nil, errors.New("arcpr source state store is required")
	}
	preset := strings.TrimSpace(s.Preset)
	if preset == "" {
		return nil, errors.New("arcpr source preset is required")
	}

	discovered, err := s.discoverPRs(ctx)
	if err != nil {
		return nil, err
	}

	submissions := make([]workqueue.Submission, 0, len(discovered))
	for _, pr := range discovered {
		state, err := s.stateFetcher().FetchPRRuntimeState(ctx, pr.Workspace, pr.ID)
		if err != nil {
			return nil, fmt.Errorf("fetch arc PR runtime state for %q: %w", pr.ID, err)
		}

		prID := currentStatePRID(state, pr.ID)
		if prID == "" {
			return nil, errors.New("arc PR ID is required")
		}
		revision := currentStateRevision(state)
		if revision == "" {
			return nil, fmt.Errorf("arc PR %q revision is required", prID)
		}

		reviewedRevision, err := s.State.GetReviewedRevision(ctx, prID)
		if err != nil {
			return nil, err
		}
		unansweredCommentIDs, err := s.unansweredCommentIDs(ctx, prID, state.Comments)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(reviewedRevision) == revision && len(unansweredCommentIDs) == 0 {
			continue
		}

		payload, err := json.Marshal(workitem.PRReviewPayload{
			PRID:                 prID,
			Revision:             revision,
			UnansweredCommentIDs: unansweredCommentIDs,
			Ship:                 s.AllowShip,
		})
		if err != nil {
			return nil, fmt.Errorf("marshal arc PR review payload for %q: %w", prID, err)
		}

		submissions = append(submissions, workqueue.Submission{
			Kind:           workitem.KindPRReview,
			Source:         s.Name(),
			SourceRef:      "pr:" + prID,
			IdempotencyKey: prReviewIdempotencyKey(s.Name(), prID, revision, unansweredCommentIDs),
			Preset:         preset,
			Priority:       s.Priority,
			Payload:        payload,
			MaxAttempts:    s.MaxAttempts,
		})
	}
	return submissions, nil
}

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
	if revision := strings.TrimSpace(resultPayload.RevisionReviewed); revision != "" {
		if err := s.State.StoreReviewedRevision(ctx, prID, revision); err != nil {
			return nil, err
		}
	}

	repliedCommentIDs := resultReplyCommentIDs(resultPayload.Replies)
	if len(repliedCommentIDs) > 0 {
		if err := s.State.StoreAnsweredCommentIDs(ctx, prID, repliedCommentIDs); err != nil {
			return nil, err
		}
	}
	return nil, nil
}

func (s *Source) discoverPRs(ctx context.Context) ([]discoveredPR, error) {
	seen := map[string]bool{}
	var discovered []discoveredPR
	for _, workspace := range normalizeStrings(s.Workspaces) {
		prs, err := s.lister().ListWorkspacePRs(ctx, workspace)
		if err != nil {
			return nil, fmt.Errorf("list arc review PRs in workspace %q: %w", workspace, err)
		}
		for _, pr := range arcanum.FilterEligiblePRs(prs, s.Reviewer, s.Branches) {
			prID := strings.TrimSpace(pr.ID)
			if prID == "" || seen[prID] {
				continue
			}
			seen[prID] = true
			discovered = append(discovered, discoveredPR{
				ID:        prID,
				Workspace: workspace,
				Branch:    strings.TrimSpace(pr.Branch),
			})
		}
	}
	return discovered, nil
}

func (s *Source) unansweredCommentIDs(ctx context.Context, prID string, comments []arcreview.PRComment) ([]string, error) {
	answeredIDs, err := s.State.ListAnsweredCommentIDs(ctx, prID)
	if err != nil {
		return nil, err
	}
	answered := make(map[string]bool, len(answeredIDs))
	for _, id := range answeredIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			answered[id] = true
		}
	}

	seen := map[string]bool{}
	var unanswered []string
	for _, comment := range comments {
		id := strings.TrimSpace(comment.ID)
		if id == "" || comment.Resolved || comment.Answered || answered[id] || seen[id] {
			continue
		}
		seen[id] = true
		unanswered = append(unanswered, id)
	}
	sort.Strings(unanswered)
	return unanswered, nil
}

func (s *Source) lister() PRLister {
	if s.Lister != nil {
		return s.Lister
	}
	return arcanumPRLister{}
}

func (s *Source) stateFetcher() PRStateFetcher {
	if s.StateFetcher != nil {
		return s.StateFetcher
	}
	return arcanumPRStateFetcher{}
}

func prReviewIdempotencyKey(sourceName string, prID string, revision string, commentIDs []string) string {
	return strings.Join([]string{
		strings.TrimSpace(sourceName),
		"pr-review",
		strings.TrimSpace(prID),
		strings.TrimSpace(revision),
		commentSetHash(commentIDs),
	}, "/")
}

func commentSetHash(commentIDs []string) string {
	normalized := normalizeStrings(commentIDs)
	sort.Strings(normalized)

	hash := sha256.New()
	for _, id := range normalized {
		hash.Write([]byte(id))
		hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
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

func normalizeStrings(values []string) []string {
	seen := map[string]bool{}
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		normalized = append(normalized, value)
	}
	return normalized
}

func currentStatePRID(state arcreview.PRRuntimeState, fallback string) string {
	if id := strings.TrimSpace(state.PRID); id != "" {
		return id
	}
	if id := strings.TrimSpace(state.Details.ID); id != "" {
		return id
	}
	return strings.TrimSpace(fallback)
}

func currentStateRevision(state arcreview.PRRuntimeState) string {
	if revision := strings.TrimSpace(state.Revision); revision != "" {
		return revision
	}
	return strings.TrimSpace(state.Details.Revision)
}

func fallbackText(primary string, fallback string) string {
	primary = strings.TrimSpace(primary)
	if primary != "" {
		return primary
	}
	return strings.TrimSpace(fallback)
}
