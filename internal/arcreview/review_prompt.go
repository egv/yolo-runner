package arcreview

import (
	"fmt"
	"strings"
	"time"
)

func BuildReviewRevisionPrompt(state PRRuntimeState) string {
	return strings.Join([]string{
		"You are reviewing one Arcanum PR revision. Use only the provided PR metadata, diffs, comments, open blockers, and checks.",
		"Action: review_revision",
		reviewRevisionRulesSection(),
		reviewRevisionMetadataSection(state),
		reviewRevisionDiffsSection(state.ChangedFiles),
		reviewRevisionCommentsSection(state.Comments),
		reviewRevisionBlockersSection(state.OpenIssues),
		reviewRevisionChecksSection(state.Checks),
		reviewRevisionJSONContractSection(),
	}, "\n\n")
}

func reviewRevisionRulesSection() string {
	return strings.Join([]string{
		"Rules:",
		"- Review the current revision for correctness, tests, regressions, and unresolved discussion.",
		"- Do not execute commands, modify files, post comments, or ship the PR.",
		"- Inline comments must be actionable and tied to a concrete file and line when possible.",
		"- Replies must answer or resolve existing review comments only.",
		"- Blockers must describe issues that should prevent shipping.",
	}, "\n")
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

func reviewRevisionDiffsSection(files []PRChangedFile) string {
	lines := []string{"Diffs:"}
	for i, file := range files {
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines,
			"File: "+reviewPromptFallback(file.Path),
			"Old path: "+reviewPromptFallback(file.OldPath),
			"Status: "+reviewPromptFallback(file.Status),
			fmt.Sprintf("Additions: %d", file.Additions),
			fmt.Sprintf("Deletions: %d", file.Deletions),
			"Diff:",
			reviewPromptFallback(file.Diff),
		)
	}
	if len(lines) == 1 {
		lines = append(lines, "None")
	}
	return strings.Join(lines, "\n")
}

func reviewRevisionCommentsSection(comments []PRComment) string {
	lines := []string{"Comments:"}
	for i, comment := range comments {
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines,
			"ID: "+reviewPromptFallback(comment.ID),
			"Thread ID: "+reviewPromptFallback(comment.ThreadID),
			"Author: "+reviewPromptFallback(comment.Author),
			"Revision: "+reviewPromptFallback(comment.Revision),
			"Path: "+reviewPromptFallback(comment.Path),
			fmt.Sprintf("Line: %d", comment.Line),
			"Created at: "+reviewPromptTime(comment.CreatedAt),
			"Updated at: "+reviewPromptTime(comment.UpdatedAt),
			fmt.Sprintf("Resolved: %t", comment.Resolved),
			fmt.Sprintf("Answered: %t", comment.Answered),
			"Body:",
			reviewPromptFallback(comment.Body),
		)
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

func reviewPromptTime(value time.Time) string {
	if value.IsZero() {
		return "None"
	}
	return value.UTC().Format(time.RFC3339)
}
