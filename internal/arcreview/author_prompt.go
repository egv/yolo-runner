package arcreview

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// AuthorDecisionImplement, AuthorDecisionResolve, and AuthorDecisionArgue are
// the closed set of author-mode dispositions for an unresolved review comment
// on the author's own PR: spawn implement task(s), resolve by reply, or reply
// arguing why it should stay open.
const (
	AuthorDecisionImplement = "implement"
	AuthorDecisionResolve   = "resolve"
	AuthorDecisionArgue     = "argue"
)

// BuildAuthorModePrompt builds the author-mode prompt for an Arc PR review.
// Unlike the reviewer prompt, it asks the model to triage each unresolved
// review comment on the author's own PR into implement / resolve / argue
// decisions. It reuses the shared metadata, project context, diff, comment,
// and check sections from review_prompt.go (calling the existing
// reviewRevision*Section helpers directly) and swaps only the role/rules and
// the JSON contract.
func BuildAuthorModePrompt(state PRRuntimeState, projectContexts ...ProjectContext) string {
	projectContext := reviewRevisionProjectContext(projectContexts)
	hasProjectContext := hasReviewRevisionProjectContext(projectContext)

	sections := []string{
		authorModeRoleSection(hasProjectContext),
		"Action: author_review",
		authorModeRulesSection(),
		reviewRevisionMetadataSection(state),
	}
	if hasProjectContext {
		sections = append(sections, reviewRevisionProjectContextSection(projectContext))
	}
	sections = append(sections,
		reviewRevisionDiffsSection(state.ChangedFiles, hasProjectContext),
		reviewRevisionCommentsSection(state.Comments),
		reviewRevisionChecksSection(state.Checks),
		authorModeJSONContractSection(),
	)
	return strings.Join(sections, "\n\n")
}

func authorModeRoleSection(hasProjectContext bool) string {
	if hasProjectContext {
		return "You are the author of this PR responding to review comments on your own PR in a real checkout. Use the provided PR metadata, project context, diffs, comments, and checks."
	}
	return "You are the author of this PR responding to review comments on your own PR. Use only the provided PR metadata, diffs, comments, and checks."
}

func authorModeRulesSection() string {
	lines := []string{
		"Rules:",
		"- Triage each unresolved review comment into exactly one decision: implement, resolve, or argue.",
		"- implement: the comment is valid and needs a code change. Populate scope with the work to do; do not resolve the comment until the implement task lands.",
		"- resolve: the comment is valid and a reply is enough to address it, for example answering a question. Provide reply_body, then resolve the comment.",
		"- argue: the comment is invalid. Provide reply_body explaining why it should stay open, and leave the comment unresolved.",
		"- Reply in the commenter's language: detect it from the comment body so a Russian comment gets a Russian reply and an English comment gets an English reply.",
		"- Keep replies in clean Markdown.",
		"- Do not write the disclosure footer; the system appends it to every reply automatically.",
		"- Reference each comment by its comment_id from Comments.",
	}
	return strings.Join(lines, "\n")
}

func authorModeJSONContractSection() string {
	return `Required JSON response:
Return only valid JSON matching this schema. Do not wrap it in Markdown or add prose outside the JSON object:
{
  "comment_decisions": [
    {
      "comment_id": "existing comment id",
      "decision": "implement | resolve | argue",
      "language": "language of the comment body",
      "reply_body": "reply to the commenter in their language",
      "rationale": "why this decision was chosen",
      "scope": {
        "title": "implement task title",
        "instructions": "what to change",
        "target_files": ["file path"]
      }
    }
  ]
}

Schema rules:
- Emit one comment_decisions entry per unresolved comment you triage; an empty list is allowed when there are no unresolved comments.
- decision must be implement, resolve, or argue.
- language must match the language of the comment body.
- reply_body is required for resolve and argue, and recommended for implement.
- scope is required only for implement and must describe the code change with enough detail to spawn an implement task.
- comment_id must reference an existing comment from Comments.`
}

// AuthorDecisionResult is the author-mode model output: a per-comment triage
// of the unresolved review comments on the author's own PR.
type AuthorDecisionResult struct {
	Decisions []AuthorCommentDecision `json:"comment_decisions"`
}

// AuthorCommentDecision records the author-mode disposition for a single
// unresolved review comment. Decision is one of the AuthorDecision* values.
type AuthorCommentDecision struct {
	CommentID string                `json:"comment_id"`
	Decision  string                `json:"decision"`
	Language  string                `json:"language,omitempty"`
	ReplyBody string                `json:"reply_body,omitempty"`
	Rationale string                `json:"rationale,omitempty"`
	Scope     *AuthorImplementScope `json:"scope,omitempty"`
}

// AuthorImplementScope describes the work needed to satisfy an "implement"
// decision with enough context to spawn an implement task.
type AuthorImplementScope struct {
	Title        string   `json:"title"`
	Instructions string   `json:"instructions"`
	TargetFiles  []string `json:"target_files,omitempty"`
}

// ParseAuthorDecisionResult parses the author-mode model output. Codex output
// can include progress prose before the required JSON object, so this mirrors
// ParseReviewResult's balanced-object extraction.
func ParseAuthorDecisionResult(payload []byte) (AuthorDecisionResult, error) {
	if strings.TrimSpace(string(payload)) == "" {
		return AuthorDecisionResult{}, fmt.Errorf("author decision result payload is required")
	}

	result, err := extractAuthorDecisionResultJSON(payload)
	if err != nil {
		return AuthorDecisionResult{}, fmt.Errorf("parse author decision result: %w", err)
	}
	return result, nil
}

func extractAuthorDecisionResultJSON(payload []byte) (AuthorDecisionResult, error) {
	candidate := stripMarkdownFences(bytes.TrimSpace(payload))

	var lastErr error
	for i := 0; i < len(candidate); i++ {
		if candidate[i] != '{' {
			continue
		}
		object, ok := balancedJSONObject(candidate[i:])
		if !ok || !bytes.Contains(object, []byte(`"comment_decisions"`)) {
			continue
		}
		var result AuthorDecisionResult
		if err := json.Unmarshal(object, &result); err != nil {
			lastErr = err
			continue
		}
		return result, nil
	}
	if lastErr != nil {
		return AuthorDecisionResult{}, lastErr
	}
	return AuthorDecisionResult{}, fmt.Errorf("no JSON object found in author decision output")
}
