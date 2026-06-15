package workitem

import "fmt"

// PRReviewPayload is the typed payload for a pr-review work item.
type PRReviewPayload struct {
	PRID                 string   `json:"pr_id"`
	Revision             string   `json:"revision"`
	UnansweredCommentIDs []string `json:"unanswered_comment_ids,omitempty"`
	Ship                 bool     `json:"ship"`
}

// PRReviewResult is the typed result payload for a pr-review work item.
type PRReviewResult struct {
	Summary          string                  `json:"summary,omitempty"`
	InlineComments   []PRReviewInlineComment `json:"inline_comments,omitempty"`
	Replies          []PRReviewReply         `json:"replies,omitempty"`
	ReviewVerdict    string                  `json:"review_verdict"`
	ShipReady        bool                    `json:"ship_ready"`
	RevisionReviewed string                  `json:"revision_reviewed,omitempty"`
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
