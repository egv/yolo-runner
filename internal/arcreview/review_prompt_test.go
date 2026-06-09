package arcreview

import (
	"strings"
	"testing"
	"time"
)

func TestBuildReviewRevisionPromptIncludesContextAndJSONContract(t *testing.T) {
	createdAt := time.Date(2026, 6, 9, 14, 30, 0, 0, time.UTC)

	prompt := BuildReviewRevisionPrompt(PRRuntimeState{
		PRID:     "ARW-14",
		Revision: "r7",
		Details: PRDetails{
			ID:           "ARW-14",
			Title:        "Build structured review prompt",
			Author:       "alice",
			Branch:       "feature/arw-14",
			SourceBranch: "users/alice/arw-14",
			TargetBranch: "trunk",
			Status:       "open",
			Revision:     "r7",
			URL:          "https://a.yandex-team.ru/review/ARW-14",
			Description:  "Constrain review behavior before execution is wired.",
		},
		ChangedFiles: []PRChangedFile{
			{
				Path:      "internal/arcreview/review_prompt.go",
				Status:    "added",
				Additions: 80,
				Deletions: 0,
				Diff:      "@@ -0,0 +1,2 @@\n+package arcreview\n+func BuildReviewRevisionPrompt() string { return \"\" }",
			},
		},
		Comments: []PRComment{
			{
				ID:        "comment-1",
				ThreadID:  "thread-1",
				Author:    "bob",
				Body:      "Please require a ship verdict.",
				Path:      "internal/arcreview/review_prompt.go",
				Line:      27,
				Revision:  "r6",
				CreatedAt: createdAt,
				Answered:  false,
			},
		},
		OpenIssues: []PRIssue{
			{
				ID:       "issue-1",
				Status:   "open",
				Severity: "blocker",
				Path:     "internal/arcreview/review_prompt.go",
				Line:     42,
				Message:  "schema is missing blockers",
				Author:   "carol",
			},
		},
		Checks: []PRCheck{
			{
				Name:        "go test ./internal/arcreview",
				Status:      "failed",
				Summary:     "review prompt test is missing",
				URL:         "https://ci.example.test/check/1",
				CompletedAt: createdAt.Add(5 * time.Minute),
			},
		},
	})

	for _, want := range []string{
		"Action: review_revision",
		"PR metadata:",
		"ID: ARW-14",
		"Title: Build structured review prompt",
		"Author: alice",
		"Source branch: users/alice/arw-14",
		"Target branch: trunk",
		"Revision: r7",
		"URL: https://a.yandex-team.ru/review/ARW-14",
		"Constrain review behavior before execution is wired.",
		"Diffs:",
		"File: internal/arcreview/review_prompt.go",
		"Status: added",
		"@@ -0,0 +1,2 @@",
		"Comments:",
		"comment-1",
		"thread-1",
		"Please require a ship verdict.",
		"Open blockers:",
		"issue-1",
		"schema is missing blockers",
		"Checks:",
		"go test ./internal/arcreview",
		"failed",
		"review prompt test is missing",
		"Required JSON response:",
		"Return only valid JSON",
		`"summary"`,
		`"inline_comments"`,
		`"replies"`,
		`"blockers"`,
		`"ship"`,
		`"verdict"`,
		`"comment_id"`,
		`"path"`,
		`"line"`,
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected prompt to contain %q, got:\n%s", want, prompt)
		}
	}

	if strings.Contains(prompt, "REVIEW_VERDICT:") {
		t.Fatalf("expected strict JSON contract, got legacy verdict marker:\n%s", prompt)
	}
}
