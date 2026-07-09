package workitem

import "fmt"

// PRReviewPayload is the typed payload for a pr-review work item. Mode
// selects the pr-review mode (see PRReviewMode*); an empty Mode is the
// default reviewer mode.
type PRReviewPayload struct {
	PRID                 string   `json:"pr_id"`
	Revision             string   `json:"revision"`
	Mode                 string   `json:"mode,omitempty"`
	UnansweredCommentIDs []string `json:"unanswered_comment_ids,omitempty"`
	Ship                 bool     `json:"ship"`
}

// PRReviewResult is the typed result payload for a pr-review work item.
// In author mode, CommentDecisions records the per-comment triage.
type PRReviewResult struct {
	Summary          string                    `json:"summary,omitempty"`
	InlineComments   []PRReviewInlineComment   `json:"inline_comments,omitempty"`
	Replies          []PRReviewReply           `json:"replies,omitempty"`
	CommentDecisions []PRReviewCommentDecision `json:"comment_decisions,omitempty"`
	ReviewVerdict    string                    `json:"review_verdict"`
	ShipReason       string                    `json:"ship_reason,omitempty"`
	ShipReady        bool                      `json:"ship_ready"`
	RevisionReviewed string                    `json:"revision_reviewed,omitempty"`
}

// PRReviewReply mirrors arcreview.ReviewReply in the queue result schema.
type PRReviewReply struct {
	CommentID string `json:"comment_id"`
	Body      string `json:"body"`
}

// PRReviewInlineComment mirrors arcreview.ReviewInlineComment in the queue
// result schema, recording the review's per-line findings for audit and
// dry-run inspection.
type PRReviewInlineComment struct {
	Path     string `json:"path"`
	Line     int    `json:"line"`
	Body     string `json:"body"`
	Severity string `json:"severity,omitempty"`
}

// PRReviewCommentDecision records the agent's author-mode triage of a
// single unresolved review comment on its own PR. Decision is one of the
// PRReviewCommentDecision* values below.
type PRReviewCommentDecision struct {
	CommentID string                  `json:"comment_id"`
	Decision  string                  `json:"decision"`
	Language  string                  `json:"language,omitempty"`
	ReplyBody string                  `json:"reply_body,omitempty"`
	Rationale string                  `json:"rationale,omitempty"`
	Scope     *PRReviewImplementScope `json:"scope,omitempty"`
}

// PRReviewCommentDecision* are the closed set of author-mode dispositions
// for a review comment: spawn implement task(s), resolve by reply, or reply
// arguing why it should stay open.
const (
	PRReviewCommentDecisionImplement = "implement"
	PRReviewCommentDecisionResolve   = "resolve"
	PRReviewCommentDecisionArgue     = "argue"
)

// PRReviewImplementScope describes the work needed to satisfy an
// "implement" decision with enough context to spawn an implement task.
type PRReviewImplementScope struct {
	Title        string   `json:"title"`
	Instructions string   `json:"instructions"`
	TargetFiles  []string `json:"target_files,omitempty"`
}

// ResolvePRCommentPayload is the typed payload for a resolve-pr-comment
// work item, which resolves a single review comment after the reply or
// implement task that addresses it has landed.
type ResolvePRCommentPayload struct {
	PRID      string `json:"pr_id"`
	CommentID string `json:"comment_id"`
}

// FinalizePayload is the typed payload for a finalize work item.
type FinalizePayload struct {
	ParentRef     string   `json:"parent_ref"`
	ChildBranches []string `json:"child_branches,omitempty"`
	Title         string   `json:"title"`
}

// FinalizeResult is the typed result payload for a finalize work item.
type FinalizeResult struct {
	PRURL string `json:"pr_url"`
}

// DecodePRReviewPayload decodes pr-review payload JSON while tolerating
// forward-compatible unknown fields.
func DecodePRReviewPayload(raw []byte) (PRReviewPayload, error) {
	var payload PRReviewPayload
	if err := decodeSingleJSON(raw, &payload); err != nil {
		return PRReviewPayload{}, fmt.Errorf("decode pr-review payload: %w", err)
	}
	return payload, nil
}

// DecodePRReviewResult decodes pr-review result JSON while tolerating
// forward-compatible unknown fields.
func DecodePRReviewResult(raw []byte) (PRReviewResult, error) {
	var result PRReviewResult
	if err := decodeSingleJSON(raw, &result); err != nil {
		return PRReviewResult{}, fmt.Errorf("decode pr-review result: %w", err)
	}
	return result, nil
}

// DecodeResolvePRCommentPayload decodes resolve-pr-comment payload JSON
// while tolerating forward-compatible unknown fields.
func DecodeResolvePRCommentPayload(raw []byte) (ResolvePRCommentPayload, error) {
	var payload ResolvePRCommentPayload
	if err := decodeSingleJSON(raw, &payload); err != nil {
		return ResolvePRCommentPayload{}, fmt.Errorf("decode resolve-pr-comment payload: %w", err)
	}
	return payload, nil
}

// DecodeFinalizePayload decodes finalize payload JSON while tolerating
// forward-compatible unknown fields.
func DecodeFinalizePayload(raw []byte) (FinalizePayload, error) {
	var payload FinalizePayload
	if err := decodeSingleJSON(raw, &payload); err != nil {
		return FinalizePayload{}, fmt.Errorf("decode finalize payload: %w", err)
	}
	return payload, nil
}

// DecodeFinalizeResult decodes finalize result JSON while tolerating
// forward-compatible unknown fields.
func DecodeFinalizeResult(raw []byte) (FinalizeResult, error) {
	var result FinalizeResult
	if err := decodeSingleJSON(raw, &result); err != nil {
		return FinalizeResult{}, fmt.Errorf("decode finalize result: %w", err)
	}
	return result, nil
}
