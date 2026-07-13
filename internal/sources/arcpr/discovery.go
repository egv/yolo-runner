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
	// ListReviewPRs returns the open PRs to monitor for its configured user.
	ListReviewPRs(ctx context.Context) ([]arcanum.PRSummary, error)
}

type PRStateFetcher interface {
	FetchPRRuntimeState(ctx context.Context, workspace string, prID string) (arcreview.PRRuntimeState, error)
}

// PRCommentFetcher reads the review threads needed for workspace-free comment
// writeback. It keeps author-mode replies and resolutions independent from a
// PR checkout that may be actively owned by an implementation worker.
type PRCommentFetcher func(ctx context.Context, prID string) ([]arcreview.PRComment, error)

// PRPublicationVerifier confirms that the active Arcanum version is visible to
// reviewers before author-mode implementation writeback posts a fix reply.
type PRPublicationVerifier func(ctx context.Context, prID string) error

type PRListerFunc func(context.Context) ([]arcanum.PRSummary, error)

func (f PRListerFunc) ListReviewPRs(ctx context.Context) ([]arcanum.PRSummary, error) {
	return f(ctx)
}

type PRStateFetcherFunc func(context.Context, string, string) (arcreview.PRRuntimeState, error)

func (f PRStateFetcherFunc) FetchPRRuntimeState(ctx context.Context, workspace string, prID string) (arcreview.PRRuntimeState, error) {
	return f(ctx, workspace, prID)
}

type Source struct {
	SourceName string
	Preset     string
	Reviewer   string
	// Author is the Arcadia login whose PRs trigger author-mode triage
	// (argue/resolve/implement on review comments). May equal Reviewer. When
	// empty, no PR is processed in author mode. At least one of Reviewer or
	// Author must be set for discovery to poll anything.
	Author string
	// WritebackWorkspace is only for result handling while discovery is cross-project.
	WritebackWorkspace string
	// WritebackWorkspaces are tried in order for result handling while discovery is cross-project.
	WritebackWorkspaces []string
	ObjectsBaseDir      string
	MountsBaseDir       string
	AllowShip           bool
	Priority            int
	MaxAttempts         int
	State               *arcreviewstate.Store
	Lister              PRLister
	StateFetcher        PRStateFetcher
	CommentFetcher      PRCommentFetcher
	APIClient           *arcanum.APIClient
	PublicationVerifier PRPublicationVerifier
	ReplyApplier        arcreview.PRReviewCycleReplyApplier
	ReviewApplier       arcreview.PRReviewCycleReviewApplier
	ResolveApplier      arcreview.PRReviewCycleResolveApplier
	ShipGate            arcreview.PRReviewCycleShipGate
	// Queue is the work queue the orchestration layer fans author-mode work
	// into. It is optional at the Source/discovery layer (wired by cmd) and
	// discovery must never assume it is present.
	Queue *workqueue.Store
	// Author-mode gates default to true (enforced by NewSource). Each gate opts
	// the author-mode triage into one behavior; clearing one disables only that
	// behavior while leaving the rest on.
	AuthorModeEnabled      bool
	AutoArgueEnabled       bool
	ResolveEnabled         bool
	ImplementFanOutEnabled bool
}

// NewSource returns an arcpr Source with the author-mode behaviors enabled by
// default. The gates are plain bools whose zero value is false, so NewSource is
// the single place that enforces the default-true intent; callers then assign
// the connection-scoped fields (State, Lister, APIClient, Queue, ...) on the
// returned value. Discovery is Queue-nil-safe.
func NewSource() *Source {
	return &Source{
		AuthorModeEnabled:      true,
		AutoArgueEnabled:       true,
		ResolveEnabled:         true,
		ImplementFanOutEnabled: true,
	}
}

type discoveredPR struct {
	ID       string
	Revision string
	Author   string
}

type arcanumPRLister struct {
	Reviewer  string
	Author    string
	APIClient *arcanum.APIClient
}

func (l arcanumPRLister) ListReviewPRs(ctx context.Context) ([]arcanum.PRSummary, error) {
	return arcanum.ListReviewPRsForRoles(ctx, l.APIClient, l.Reviewer, l.Author)
}

type arcanumPRStateFetcher struct{}

func (arcanumPRStateFetcher) FetchPRRuntimeState(ctx context.Context, workspace string, prID string) (arcreview.PRRuntimeState, error) {
	if strings.TrimSpace(workspace) == "" {
		comments, err := arcanum.FetchPRComments(ctx, prID)
		if err != nil {
			return arcreview.PRRuntimeState{}, err
		}
		prID = strings.TrimSpace(prID)
		return arcreview.PRRuntimeState{
			PRID:     prID,
			Details:  arcreview.PRDetails{ID: prID},
			Comments: comments,
		}, nil
	}
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
	if err := s.reconcileSupersededAuthorImplementItems(ctx, discovered); err != nil {
		return nil, err
	}

	submissions := make([]workqueue.Submission, 0, len(discovered))
	for _, pr := range discovered {
		prID := strings.TrimSpace(pr.ID)
		if prID == "" {
			return nil, errors.New("arc PR ID is required")
		}
		revision := strings.TrimSpace(pr.Revision)
		if revision == "" {
			return nil, fmt.Errorf("arc PR %q head commit is required", prID)
		}

		// Author mode exists to respond to review threads on the user's own PR.
		// A new push without an actionable comment must not enqueue a review
		// simply to rebase and republish the same PR version: that created a
		// self-sustaining stream of empty author reviews and draft versions.
		mode := workitem.PRReviewModeReviewer
		if s.Author != "" && strings.EqualFold(strings.TrimSpace(pr.Author), s.Author) {
			mode = workitem.PRReviewModeAuthor
		}
		var unansweredCommentIDs []string
		var triggeringReplies map[string]string
		if mode == workitem.PRReviewModeAuthor {
			state, err := s.stateFetcher().FetchPRRuntimeState(ctx, "", prID)
			if err != nil {
				return nil, fmt.Errorf("fetch arc PR runtime state for author comments for %q: %w", prID, err)
			}
			unansweredCommentIDs, triggeringReplies, err = s.unansweredCommentIDs(ctx, prID, state.Comments)
			if err != nil {
				return nil, err
			}
			if len(unansweredCommentIDs) == 0 {
				continue
			}
		} else {
			reviewedRevision, err := s.State.GetReviewedRevision(ctx, prID)
			if err != nil {
				return nil, err
			}
			if strings.TrimSpace(reviewedRevision) == revision {
				state, err := s.stateFetcher().FetchPRRuntimeState(ctx, "", prID)
				if err != nil {
					return nil, fmt.Errorf("fetch arc PR runtime state for comments for %q: %w", prID, err)
				}
				unansweredCommentIDs, triggeringReplies, err = s.unansweredCommentIDs(ctx, prID, state.Comments)
				if err != nil {
					return nil, err
				}
				if len(unansweredCommentIDs) == 0 {
					continue
				}
			}
		}

		payload, err := json.Marshal(workitem.PRReviewPayload{
			PRID:                 prID,
			Revision:             revision,
			Mode:                 mode,
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
			IdempotencyKey: prReviewIdempotencyKey(s.Name(), prID, revision, mode, unansweredCommentIDs, triggeringReplies),
			Preset:         preset,
			Priority:       s.Priority,
			Payload:        payload,
			MaxAttempts:    s.MaxAttempts,
			// A newer author-mode revision supersedes any pending older
			// author-mode review. The queue applies this atomically with the
			// enqueue, so an older revision cannot later rebase the PR.
			SupersedePending: mode == workitem.PRReviewModeAuthor,
		})
	}
	return submissions, nil
}

func (s *Source) discoverPRs(ctx context.Context) ([]discoveredPR, error) {
	seen := map[string]bool{}
	var discovered []discoveredPR
	prs, err := s.lister().ListReviewPRs(ctx)
	if err != nil {
		return nil, fmt.Errorf("list arc review PRs: %w", err)
	}
	for _, pr := range prs {
		prID := strings.TrimSpace(pr.ID)
		revision := strings.TrimSpace(pr.FromID)
		if prID == "" {
			continue
		}
		key := prID + "\x00" + revision
		if seen[key] {
			continue
		}
		seen[key] = true
		discovered = append(discovered, discoveredPR{
			ID:       prID,
			Revision: revision,
			Author:   strings.TrimSpace(pr.Author),
		})
	}
	return discovered, nil
}

// reconcileSupersededAuthorImplementItems removes pending implementation work
// that a newer author-mode triage has replaced. The state store keeps the one
// current implement item for each comment; a queue item that carries the same
// comment but no longer matches that mapping can never be allowed to alter the
// PR branch. Claimed and running items are intentionally left untouched.
func (s *Source) reconcileSupersededAuthorImplementItems(ctx context.Context, discovered []discoveredPR) error {
	if s.Queue == nil || strings.TrimSpace(s.Author) == "" {
		return nil
	}
	for _, pr := range discovered {
		prID := strings.TrimSpace(pr.ID)
		if prID == "" || !strings.EqualFold(strings.TrimSpace(pr.Author), strings.TrimSpace(s.Author)) {
			continue
		}
		mappings, err := s.ListCommentImplementItems(ctx, prID)
		if err != nil {
			return fmt.Errorf("list arc PR implement mappings for %q: %w", prID, err)
		}
		if len(mappings) == 0 {
			continue
		}
		currentByComment := make(map[string]string, len(mappings))
		for _, mapping := range mappings {
			commentID := strings.TrimSpace(mapping.CommentID)
			itemID := strings.TrimSpace(mapping.ImplementItemID)
			if commentID != "" && itemID != "" {
				currentByComment[commentID] = itemID
			}
		}
		pending, err := s.Queue.ListItems(workqueue.ListItemsFilter{
			Source:    s.Name(),
			SourceRef: "pr:" + prID,
			State:     "pending",
			Kind:      string(workitem.KindImplement),
		})
		if err != nil {
			return fmt.Errorf("list pending arc PR implement items for %q: %w", prID, err)
		}
		for _, candidate := range pending {
			implement, err := workitem.DecodeImplementPayload(candidate.Payload)
			if err != nil {
				// A malformed task is left to normal runner error reporting; it is
				// not safe to infer a comment identity from corrupt metadata.
				continue
			}
			metadata := implement.PromptContext.Metadata
			if strings.TrimSpace(metadata["origin"]) != "arcpr-author" || strings.TrimSpace(metadata["arc_pr_id"]) != prID {
				continue
			}
			commentID := strings.TrimSpace(metadata["arc_comment_id"])
			currentItemID, tracked := currentByComment[commentID]
			if !tracked || currentItemID == candidate.ID {
				continue
			}
			if _, err := s.Queue.CancelPendingItem(candidate.ID); err != nil {
				return fmt.Errorf("cancel superseded arc PR implement item %q: %w", candidate.ID, err)
			}
		}
	}
	return nil
}

func (s *Source) unansweredCommentIDs(ctx context.Context, prID string, comments []arcreview.PRComment) ([]string, map[string]string, error) {
	answeredIDs, err := s.State.ListAnsweredCommentIDs(ctx, prID)
	if err != nil {
		return nil, nil, err
	}
	answered := make(map[string]bool, len(answeredIDs))
	for _, id := range answeredIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			answered[id] = true
		}
	}

	threadStates, err := s.State.ListThreadStates(ctx, prID)
	if err != nil {
		return nil, nil, err
	}
	stateByID := make(map[string]arcreviewstate.ThreadState, len(threadStates))
	for _, ts := range threadStates {
		if id := strings.TrimSpace(ts.CommentID); id != "" {
			stateByID[id] = ts
		}
	}

	repliesByRoot := repliesByThreadRoot(comments)

	seen := map[string]bool{}
	var unanswered []string
	triggering := map[string]string{}
	for _, comment := range comments {
		id := strings.TrimSpace(comment.ID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		if comment.Resolved {
			continue
		}

		// Genuinely unanswered comment: surface it for triage.
		if !comment.Answered && !answered[id] {
			unanswered = append(unanswered, id)
			continue
		}

		// Already answered comment: re-engage only when a tracked thread has a
		// genuinely new reply from someone other than the agent (s.Reviewer).
		state, tracked := stateByID[id]
		if !tracked {
			continue
		}
		if trigger := newestUnseenNonSelfReply(repliesByRoot[id], state, s.Reviewer); trigger != nil {
			unanswered = append(unanswered, id)
			triggering[id] = trigger.ID
		}
	}
	sort.Strings(unanswered)
	return unanswered, triggering, nil
}

func (s *Source) lister() PRLister {
	if s.Lister != nil {
		return s.Lister
	}
	return arcanumPRLister{
		Reviewer:  s.Reviewer,
		Author:    s.Author,
		APIClient: s.APIClient,
	}
}

func (s *Source) stateFetcher() PRStateFetcher {
	if s.StateFetcher != nil {
		return s.StateFetcher
	}
	return arcanumPRStateFetcher{}
}

func prReviewIdempotencyKey(sourceName string, prID string, revision string, mode string, commentIDs []string, triggeringReplies map[string]string) string {
	return strings.Join([]string{
		strings.TrimSpace(sourceName),
		"pr-review",
		strings.TrimSpace(prID),
		strings.TrimSpace(revision),
		strings.TrimSpace(mode),
		commentSetHash(commentIDs, triggeringReplies),
	}, "/")
}

func commentSetHash(commentIDs []string, triggeringReplies map[string]string) string {
	normalized := normalizeStrings(commentIDs)
	sort.Strings(normalized)

	hash := sha256.New()
	for _, id := range normalized {
		hash.Write([]byte(id))
		hash.Write([]byte{0})
		if reply := strings.TrimSpace(triggeringReplies[id]); reply != "" {
			hash.Write([]byte("reply="))
			hash.Write([]byte(reply))
			hash.Write([]byte{0})
		}
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// repliesByThreadRoot groups reply comments under the root review comment of
// their thread. A reply's ThreadID points at its direct parent, so replies are
// resolved to their root by walking the parent chain. Direct and nested replies
// both land under the same root.
func repliesByThreadRoot(comments []arcreview.PRComment) map[string][]arcreview.PRComment {
	parentOf := make(map[string]string)
	exists := make(map[string]bool)
	for _, comment := range comments {
		id := strings.TrimSpace(comment.ID)
		if id == "" {
			continue
		}
		exists[id] = true
		if parent := strings.TrimSpace(comment.ThreadID); parent != "" {
			parentOf[id] = parent
		}
	}

	replies := make(map[string][]arcreview.PRComment)
	for _, comment := range comments {
		id := strings.TrimSpace(comment.ID)
		if id == "" || strings.TrimSpace(comment.ThreadID) == "" {
			continue
		}
		root := threadRootOf(id, parentOf, exists)
		replies[root] = append(replies[root], comment)
	}
	return replies
}

func threadRootOf(id string, parentOf map[string]string, exists map[string]bool) string {
	seen := map[string]bool{}
	cur := id
	for {
		if seen[cur] {
			return cur
		}
		seen[cur] = true
		parent := parentOf[cur]
		if parent == "" || parent == cur || !exists[parent] {
			return cur
		}
		cur = parent
	}
}

// newestUnseenNonSelfReply returns the newest reply in a thread that the agent
// has not yet seen and that was not posted by the agent itself, or nil if there
// is no such reply to re-engage on.
func newestUnseenNonSelfReply(replies []arcreview.PRComment, state arcreviewstate.ThreadState, reviewer string) *arcreview.PRComment {
	var newest *arcreview.PRComment
	for i := range replies {
		reply := &replies[i]
		if isSelfReply(reply.Author, reviewer) {
			continue
		}
		if !isUnseenReply(reply, state) {
			continue
		}
		if newerReply(newest, reply) {
			newest = reply
		}
	}
	return newest
}

func isUnseenReply(reply *arcreview.PRComment, state arcreviewstate.ThreadState) bool {
	if id := strings.TrimSpace(reply.ID); id != "" && id == strings.TrimSpace(state.LastSeenReplyID) {
		return false
	}
	if state.LastSeenReplyAt.IsZero() {
		return true
	}
	return reply.CreatedAt.After(state.LastSeenReplyAt)
}

func newerReply(current *arcreview.PRComment, candidate *arcreview.PRComment) bool {
	if current == nil {
		return true
	}
	if !candidate.CreatedAt.Equal(current.CreatedAt) {
		return candidate.CreatedAt.After(current.CreatedAt)
	}
	return strings.TrimSpace(candidate.ID) > strings.TrimSpace(current.ID)
}

func isSelfReply(author string, reviewer string) bool {
	author = strings.TrimSpace(author)
	reviewer = strings.TrimSpace(reviewer)
	if author == "" || reviewer == "" {
		return false
	}
	return strings.EqualFold(author, reviewer)
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
