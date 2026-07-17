package workitem

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// ImplementPayload is the typed payload for an implement work item.
type ImplementPayload struct {
	TaskID        string                 `json:"task_id"`
	Title         string                 `json:"title"`
	Description   string                 `json:"description"`
	PromptContext ImplementPromptContext `json:"prompt_context"`
	BaseBranch    string                 `json:"base_branch,omitempty"`
	RetryContext  ImplementRetryContext  `json:"retry_context,omitempty"`
	TDD           bool                   `json:"tdd"`
	QualityGate   bool                   `json:"quality_gate"`
}

func (p ImplementPayload) MarshalJSON() ([]byte, error) {
	type implementPayloadJSON struct {
		TaskID        string                 `json:"task_id"`
		Title         string                 `json:"title"`
		Description   string                 `json:"description"`
		PromptContext ImplementPromptContext `json:"prompt_context"`
		BaseBranch    string                 `json:"base_branch,omitempty"`
		RetryContext  *ImplementRetryContext `json:"retry_context,omitempty"`
		TDD           bool                   `json:"tdd"`
		QualityGate   bool                   `json:"quality_gate"`
	}

	var retry *ImplementRetryContext
	if !p.RetryContext.isZero() {
		retry = &p.RetryContext
	}
	return json.Marshal(implementPayloadJSON{
		TaskID:        p.TaskID,
		Title:         p.Title,
		Description:   p.Description,
		PromptContext: p.PromptContext,
		BaseBranch:    p.BaseBranch,
		RetryContext:  retry,
		TDD:           p.TDD,
		QualityGate:   p.QualityGate,
	})
}

// ImplementPromptContext carries prompt text and source-side context for the runner.
type ImplementPromptContext struct {
	Prompt   string            `json:"prompt"`
	ParentID string            `json:"parent_id,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// ImplementCommentIDs returns the review comment IDs an arcpr author-mode
// implement item addresses. Batched items carry the full list in
// arc_comment_ids (comma-separated); older single-comment items only set
// arc_comment_id. Order is preserved, blanks dropped.
func ImplementCommentIDs(metadata map[string]string) []string {
	raw := strings.TrimSpace(metadata["arc_comment_ids"])
	if raw == "" {
		raw = strings.TrimSpace(metadata["arc_comment_id"])
	}
	if raw == "" {
		return nil
	}
	var ids []string
	for _, id := range strings.Split(raw, ",") {
		if id = strings.TrimSpace(id); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// ImplementRetryContext carries prior-attempt context for retry prompts.
type ImplementRetryContext struct {
	Attempt           int    `json:"attempt,omitempty"`
	PreviousReason    string `json:"previous_reason,omitempty"`
	ReviewFeedback    string `json:"review_feedback,omitempty"`
	PreviousBranch    string `json:"previous_branch,omitempty"`
	PreviousCommitSHA string `json:"previous_commit_sha,omitempty"`
}

func (r ImplementRetryContext) isZero() bool {
	return r == ImplementRetryContext{}
}

// ImplementResult is the typed result payload for an implement work item.
type ImplementResult struct {
	Status        string            `json:"status"`
	Reason        string            `json:"reason,omitempty"`
	Branch        string            `json:"branch,omitempty"`
	CommitSHA     string            `json:"commit_sha,omitempty"`
	PRURL         string            `json:"pr_url,omitempty"`
	ReviewVerdict string            `json:"review_verdict,omitempty"`
	Artifacts     map[string]string `json:"artifacts,omitempty"`
}

// DecodeImplementPayload decodes implement payload JSON while tolerating
// forward-compatible unknown fields.
func DecodeImplementPayload(raw []byte) (ImplementPayload, error) {
	var payload ImplementPayload
	if err := decodeSingleJSON(raw, &payload); err != nil {
		return ImplementPayload{}, fmt.Errorf("decode implement payload: %w", err)
	}
	return payload, nil
}

// DecodeImplementResult decodes implement result JSON while tolerating
// forward-compatible unknown fields.
func DecodeImplementResult(raw []byte) (ImplementResult, error) {
	var result ImplementResult
	if err := decodeSingleJSON(raw, &result); err != nil {
		return ImplementResult{}, fmt.Errorf("decode implement result: %w", err)
	}
	return result, nil
}

func decodeSingleJSON(raw []byte, dst any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("trailing JSON content")
	}
	return nil
}
