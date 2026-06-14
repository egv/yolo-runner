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

func TestBuildReviewRevisionPromptIncludesProjectContextWhenProvided(t *testing.T) {
	state := PRRuntimeState{
		PRID:     "ARW-15",
		Revision: "r3",
		Details: PRDetails{
			ID:          "ARW-15",
			Title:       "Review with project context",
			Author:      "alice",
			Status:      "open",
			Revision:    "r3",
			Description: "Use local project facts for execution-grade review.",
		},
		ChangedFiles: []PRChangedFile{
			{Path: "taxi/backend-cpp/services/ai_minion/main.cpp", Status: "modified"},
		},
		Checks: []PRCheck{{Name: "ya make", Status: "passed"}},
	}

	prompt := BuildReviewRevisionPrompt(state, ProjectContext{
		Root: "taxi/backend-cpp/services/ai_minion",
		Command: []string{
			"ya",
			"make",
			"-t",
			"taxi/backend-cpp/services/ai_minion",
		},
		ConventionsExcerpt: "Prefer table-driven tests.\nKeep generated code untouched.",
		LinkedTickets: []LinkedTicketSummary{
			{
				ID:                 "TAXI-42",
				Title:              "Keep AI minion retries deterministic",
				AcceptanceCriteria: "- retries preserve ordering\n- tests cover transient failures",
			},
		},
	})

	for _, want := range []string{
		"Project context:",
		"Note: You are inside a real checkout. You may build/run tests",
		"Root: taxi/backend-cpp/services/ai_minion",
		"Build/test command: ya make -t taxi/backend-cpp/services/ai_minion",
		"Conventions excerpt:",
		"Prefer table-driven tests.",
		"Keep generated code untouched.",
		"Linked ticket acceptance criteria:",
		"TAXI-42 - Keep AI minion retries deterministic:",
		"- retries preserve ordering",
		"- tests cover transient failures",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected prompt to contain %q, got:\n%s", want, prompt)
		}
	}

	if strings.Contains(prompt, "- Do not execute commands") {
		t.Fatalf("expected project context prompt to permit build/test execution, got:\n%s", prompt)
	}

	withoutContext := BuildReviewRevisionPrompt(state)
	withEmptyContext := BuildReviewRevisionPrompt(state, ProjectContext{})
	if withEmptyContext != withoutContext {
		t.Fatalf("expected empty project context to leave prompt unchanged\nwithout:\n%s\nwith empty:\n%s", withoutContext, withEmptyContext)
	}
	if strings.Contains(withoutContext, "Project context:") {
		t.Fatalf("expected prompt without context to omit project context section, got:\n%s", withoutContext)
	}
}
