package arcreview

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

func BuildReviewRevisionPrompt(state PRRuntimeState, projectContexts ...ProjectContext) string {
	projectContext := reviewRevisionProjectContext(projectContexts)
	hasProjectContext := hasReviewRevisionProjectContext(projectContext)

	sections := []string{
		reviewRevisionIntroSection(hasProjectContext),
		"Action: review_revision",
		reviewRevisionRulesSection(hasProjectContext),
		reviewRevisionMetadataSection(state),
	}
	if hasProjectContext {
		sections = append(sections, reviewRevisionProjectContextSection(projectContext))
	}
	sections = append(sections,
		reviewRevisionDiffsSection(state.ChangedFiles),
		reviewRevisionCommentsSection(state.Comments),
		reviewRevisionBlockersSection(state.OpenIssues),
		reviewRevisionChecksSection(state.Checks),
		reviewRevisionJSONContractSection(),
	)
	return strings.Join(sections, "\n\n")
}

// maxDiffSectionBytes caps the total size of the Diffs section. Large PRs can
// produce diffs exceeding 1MB, which blows past any model's context limit. The
// truncation keeps the first N files fully, then truncates the rest so the
// review still covers the most important changes.
const maxDiffSectionBytes = 200 * 1024

// maxCommentSectionBytes caps the total size of the Comments section. PRs with
// long review threads can accumulate hundreds of KB of comment text.
const maxCommentSectionBytes = 150 * 1024

func reviewRevisionIntroSection(hasProjectContext bool) string {
	if hasProjectContext {
		return "You are reviewing one Arcanum PR revision in a real checkout. Use the provided PR metadata, project context, diffs, comments, open blockers, and checks."
	}
	return "You are reviewing one Arcanum PR revision. Use only the provided PR metadata, diffs, comments, open blockers, and checks."
}

func reviewRevisionRulesSection(hasProjectContext bool) string {
	lines := []string{
		"Rules:",
		"- Review the current revision for correctness, tests, regressions, and unresolved discussion.",
	}
	if hasProjectContext {
		lines = append(lines, "- You may inspect files and run targeted build/test commands in the real checkout when useful; do not modify files, post comments, or ship the PR.")
	} else {
		lines = append(lines, "- Do not execute commands, modify files, post comments, or ship the PR.")
	}
	lines = append(lines,
		"- Inline comments must be actionable and tied to a concrete file and line when possible.",
		"- Replies must answer or resolve existing review comments only.",
		"- Blockers must describe issues that should prevent shipping.",
	)
	return strings.Join(lines, "\n")
}

func reviewRevisionMetadataSection(state PRRuntimeState) string {
	details := state.Details
	revision := strings.TrimSpace(state.Revision)
	if revision == "" {
		revision = details.Revision
	}
	id := details.ID
	if strings.TrimSpace(id) == "" {
		id = state.PRID
	}

	return strings.Join([]string{
		"PR metadata:",
		"ID: " + reviewPromptFallback(id),
		"Title: " + reviewPromptFallback(details.Title),
		"Author: " + reviewPromptFallback(details.Author),
		"Branch: " + reviewPromptFallback(details.Branch),
		"Source branch: " + reviewPromptFallback(details.SourceBranch),
		"Target branch: " + reviewPromptFallback(details.TargetBranch),
		"Status: " + reviewPromptFallback(details.Status),
		"Revision: " + reviewPromptFallback(revision),
		"URL: " + reviewPromptFallback(details.URL),
		"",
		"Description:",
		reviewPromptFallback(details.Description),
	}, "\n")
}

func reviewRevisionProjectContext(contexts []ProjectContext) ProjectContext {
	if len(contexts) == 0 {
		return ProjectContext{}
	}
	return contexts[0]
}

func hasReviewRevisionProjectContext(context ProjectContext) bool {
	return strings.TrimSpace(context.Root) != "" ||
		len(reviewPromptCommandArgs(context.Command)) > 0 ||
		strings.TrimSpace(context.ConventionsExcerpt) != "" ||
		len(reviewPromptLinkedTicketAcceptanceLines(context.LinkedTickets)) > 0
}

func reviewRevisionProjectContextSection(context ProjectContext) string {
	lines := []string{
		"Project context:",
		"Note: You are inside a real checkout. You may build/run tests and inspect files for this review; keep commands targeted and do not modify files.",
		"Root: " + reviewPromptFallback(context.Root),
		"Build/test command: " + reviewPromptCommand(context.Command),
	}

	if conventions := strings.TrimSpace(context.ConventionsExcerpt); conventions != "" {
		lines = append(lines,
			"",
			"Conventions excerpt:",
			conventions,
		)
	}

	if linkedTicketLines := reviewPromptLinkedTicketAcceptanceLines(context.LinkedTickets); len(linkedTicketLines) > 0 {
		lines = append(lines,
			"",
			"Linked ticket acceptance criteria:",
		)
		lines = append(lines, linkedTicketLines...)
	}

	return strings.Join(lines, "\n")
}

func reviewPromptCommand(args []string) string {
	args = reviewPromptCommandArgs(args)
	if len(args) == 0 {
		return "None"
	}
	parts := make([]string, 0, len(args))
	for _, arg := range args {
		if strings.ContainsAny(arg, " \t\r\n\"'\\") {
			parts = append(parts, strconv.Quote(arg))
			continue
		}
		parts = append(parts, arg)
	}
	return strings.Join(parts, " ")
}

func reviewPromptCommandArgs(args []string) []string {
	trimmed := make([]string, 0, len(args))
	for _, arg := range args {
		arg = strings.TrimSpace(arg)
		if arg != "" {
			trimmed = append(trimmed, arg)
		}
	}
	return trimmed
}

func reviewPromptLinkedTicketAcceptanceLines(tickets []LinkedTicketSummary) []string {
	lines := make([]string, 0, len(tickets)*2)
	for _, ticket := range tickets {
		acceptanceCriteria := strings.TrimSpace(ticket.AcceptanceCriteria)
		if acceptanceCriteria == "" {
			continue
		}
		lines = append(lines, reviewPromptLinkedTicketHeader(ticket)+":", acceptanceCriteria)
	}
	return lines
}

func reviewPromptLinkedTicketHeader(ticket LinkedTicketSummary) string {
	id := strings.TrimSpace(ticket.ID)
	title := strings.TrimSpace(ticket.Title)
	switch {
	case id != "" && title != "":
		return id + " - " + title
	case id != "":
		return id
	case title != "":
		return title
	default:
		return "Linked ticket"
	}
}

func reviewRevisionDiffsSection(files []PRChangedFile) string {
	lines := []string{"Diffs:"}
	totalSize := 0
	truncated := 0
	for i, file := range files {
		if i > 0 {
			lines = append(lines, "")
		}
		diff := reviewPromptFallback(file.Diff)
		// Stop adding full diffs once the section exceeds the budget; list
		// remaining files by name only so the reviewer knows they exist.
		if totalSize > maxDiffSectionBytes {
			lines = append(lines, "File: "+reviewPromptFallback(file.Path)+" (diff truncated — see checkout)")
			truncated++
			continue
		}
		entry := strings.Join([]string{
			"File: "+reviewPromptFallback(file.Path),
			"Old path: "+reviewPromptFallback(file.OldPath),
			"Status: "+reviewPromptFallback(file.Status),
			fmt.Sprintf("Additions: %d", file.Additions),
			fmt.Sprintf("Deletions: %d", file.Deletions),
			"Diff:",
			diff,
		}, "\n")
		totalSize += len(entry)
		lines = append(lines, entry)
	}
	if truncated > 0 {
		lines = append(lines, fmt.Sprintf("\n(%d additional files truncated — review them in the checkout)", truncated))
	}
	if len(lines) == 1 {
		lines = append(lines, "None")
	}
	return strings.Join(lines, "\n")
}

func reviewRevisionCommentsSection(comments []PRComment) string {
	lines := []string{"Comments:"}
	totalSize := 0
	truncated := 0
	for i, comment := range comments {
		if i > 0 {
			lines = append(lines, "")
		}
		body := reviewPromptFallback(comment.Body)
		entry := strings.Join([]string{
			"ID: " + reviewPromptFallback(comment.ID),
			"Thread ID: " + reviewPromptFallback(comment.ThreadID),
			"Author: " + reviewPromptFallback(comment.Author),
			"Revision: " + reviewPromptFallback(comment.Revision),
			"Path: " + reviewPromptFallback(comment.Path),
			fmt.Sprintf("Line: %d", comment.Line),
			"Created at: " + reviewPromptTime(comment.CreatedAt),
			"Updated at: " + reviewPromptTime(comment.UpdatedAt),
			fmt.Sprintf("Resolved: %t", comment.Resolved),
			fmt.Sprintf("Answered: %t", comment.Answered),
			"Body:",
			body,
		}, "\n")
		// Truncate the body of long comments once the section exceeds the budget.
		if totalSize > maxCommentSectionBytes {
			body = truncatePromptText(body, 200)
			entry = strings.Join([]string{
				"ID: " + reviewPromptFallback(comment.ID),
				"Author: " + reviewPromptFallback(comment.Author),
				"Path: " + reviewPromptFallback(comment.Path),
				fmt.Sprintf("Resolved: %t", comment.Resolved),
				"Body (truncated):",
				body,
			}, "\n")
			truncated++
		}
		totalSize += len(entry)
		lines = append(lines, entry)
	}
	if truncated > 0 {
		lines = append(lines, fmt.Sprintf("\n(%d additional comments truncated)", truncated))
	}
	if len(lines) == 1 {
		lines = append(lines, "None")
	}
	return strings.Join(lines, "\n")
}

func reviewRevisionBlockersSection(issues []PRIssue) string {
	lines := []string{"Open blockers:"}
	for i, issue := range issues {
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines,
			"ID: "+reviewPromptFallback(issue.ID),
			"Status: "+reviewPromptFallback(issue.Status),
			"Severity: "+reviewPromptFallback(issue.Severity),
			"Path: "+reviewPromptFallback(issue.Path),
			fmt.Sprintf("Line: %d", issue.Line),
			"Author: "+reviewPromptFallback(issue.Author),
			fmt.Sprintf("Resolved: %t", issue.Resolved),
			"Message:",
			reviewPromptFallback(issue.Message),
		)
	}
	if len(lines) == 1 {
		lines = append(lines, "None")
	}
	return strings.Join(lines, "\n")
}

func reviewRevisionChecksSection(checks []PRCheck) string {
	lines := []string{"Checks:"}
	for i, check := range checks {
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines,
			"Name: "+reviewPromptFallback(check.Name),
			"Status: "+reviewPromptFallback(check.Status),
			"Summary: "+reviewPromptFallback(check.Summary),
			"URL: "+reviewPromptFallback(check.URL),
			"Started at: "+reviewPromptTime(check.StartedAt),
			"Completed at: "+reviewPromptTime(check.CompletedAt),
		)
	}
	if len(lines) == 1 {
		lines = append(lines, "None")
	}
	return strings.Join(lines, "\n")
}

func reviewRevisionJSONContractSection() string {
	return `Required JSON response:
Return only valid JSON matching this schema. Do not wrap it in Markdown or add prose outside the JSON object:
{
  "summary": "concise review summary",
  "inline_comments": [
    {
      "path": "file path",
      "line": 1,
      "body": "actionable inline review comment",
      "severity": "nit | issue | blocker"
    }
  ],
  "replies": [
    {
      "comment_id": "existing comment id",
      "body": "reply to the existing comment"
    }
  ],
  "blockers": [
    {
      "kind": "code | test | check | discussion",
      "path": "file path or empty string",
      "line": 1,
      "message": "blocking issue that must be fixed before shipping"
    }
  ],
  "ship": {
    "verdict": "ship | do_not_ship",
    "reason": "why this revision should or should not ship"
  }
}

Schema rules:
- summary must be a short sentence about the revision.
- inline_comments must be empty when there are no new inline findings.
- replies must reference comment_id values from Comments only.
- blockers must include every issue that should prevent shipping, including failed checks when relevant.
- ship.verdict must be "ship" only when there are no blockers, no required replies, and checks do not show a failure.`
}

func reviewPromptFallback(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "None"
	}
	return value
}

// truncatePromptText caps a string to maxBytes, keeping the beginning and
// appending a truncation marker. Used to keep large diffs/comments within the
// model's context budget.
func truncatePromptText(value string, maxBytes int) string {
	value = strings.TrimSpace(value)
	if maxBytes <= 0 || len(value) <= maxBytes {
		return value
	}
	return value[:maxBytes] + "\n…(truncated)"
}

func reviewPromptTime(value time.Time) string {
	if value.IsZero() {
		return "None"
	}
	return value.UTC().Format(time.RFC3339)
}
