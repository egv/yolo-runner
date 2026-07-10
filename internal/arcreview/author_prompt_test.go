package arcreview

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestBuildAuthorModePromptContainsDecisionContractAndOmitsReviewerFields(t *testing.T) {
	createdAt := time.Date(2026, 6, 9, 14, 30, 0, 0, time.UTC)

	prompt := BuildAuthorModePrompt(PRRuntimeState{
		PRID:     "ARW-42",
		Revision: "r3",
		Details: PRDetails{
			ID:           "ARW-42",
			Title:        "Author-mode triage",
			Author:       "alice",
			SourceBranch: "users/alice/arw-42",
			TargetBranch: "trunk",
			Status:       "open",
			Revision:     "r3",
			Description:  "Author replies to reviewer comments.",
		},
		ChangedFiles: []PRChangedFile{
			{
				Path:      "internal/arcreview/author_prompt.go",
				Status:    "added",
				Additions: 40,
				Diff:      "@@ -0,0 +1,2 @@\n+package arcreview",
			},
		},
		Comments: []PRComment{
			{
				ID:        "comment-1",
				Author:    "bob",
				Body:      "Почему здесь нет обработки ошибки?",
				Path:      "internal/arcreview/author_prompt.go",
				Line:      12,
				CreatedAt: createdAt,
			},
		},
		Checks: []PRCheck{
			{Name: "go test ./internal/arcreview", Status: "passed"},
		},
	})

	for _, want := range []string{
		"author",
		"Action: author_review",
		"comment_decisions",
		"language",
		"implement",
		"resolve",
		"argue",
		"commenter's language",
		"PR metadata:",
		"Diffs:",
		"Comments:",
		"comment-1",
		"scope",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected author prompt to contain %q, got:\n%s", want, prompt)
		}
	}

	for _, forbidden := range []string{
		"ship",
		"blockers",
		"Action: review_revision",
		`"summary"`,
	} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("expected author prompt to omit %q, got:\n%s", forbidden, prompt)
		}
	}
}

func TestBuildAuthorModePromptReusesSharedSections(t *testing.T) {
	state := PRRuntimeState{
		PRID: "ARW-43",
		Details: PRDetails{
			ID:       "ARW-43",
			Title:    "Author mode with project context",
			Author:   "alice",
			Revision: "r2",
		},
		ChangedFiles: []PRChangedFile{
			{Path: "taxi/backend-cpp/services/ai_minion/main.cpp", Status: "modified"},
		},
	}

	prompt := BuildAuthorModePrompt(state, ProjectContext{
		Root:               "taxi/backend-cpp/services/ai_minion",
		Command:            []string{"ya", "make", "-t", "taxi/backend-cpp/services/ai_minion"},
		ConventionsExcerpt: "Prefer table-driven tests.",
		LinkedTickets: []LinkedTicketSummary{
			{
				ID:                 "TAXI-7",
				Title:              "Deterministic retries",
				AcceptanceCriteria: "- retries preserve ordering",
			},
		},
	})

	for _, want := range []string{
		"Project context:",
		"Root: taxi/backend-cpp/services/ai_minion",
		"Build/test command: ya make -t taxi/backend-cpp/services/ai_minion",
		"Conventions excerpt:",
		"Linked ticket acceptance criteria:",
		"TAXI-7 - Deterministic retries:",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected author prompt to reuse shared section containing %q, got:\n%s", want, prompt)
		}
	}

	withEmptyContext := BuildAuthorModePrompt(state, ProjectContext{})
	withoutContext := BuildAuthorModePrompt(state)
	if withEmptyContext != withoutContext {
		t.Fatalf("expected empty project context to leave author prompt unchanged\nwithout:\n%s\nwith empty:\n%s", withoutContext, withEmptyContext)
	}
}

func TestParseAuthorDecisionResult(t *testing.T) {
	t.Run("valid payload with all fields", func(t *testing.T) {
		payload := []byte(`{
			"comment_decisions": [
				{
					"comment_id": "comment-1",
					"decision": "implement",
					"language": "ru",
					"reply_body": "Добавлю обработку ошибки.",
					"rationale": "Комментарий обоснован.",
					"scope": {
						"title": "Handle parse error",
						"instructions": "Return the error from ParseAuthorDecisionResult.",
						"target_files": ["internal/arcreview/author_prompt.go"]
					}
				},
				{
					"comment_id": "comment-2",
					"decision": "argue",
					"language": "en",
					"reply_body": "This is intentional.",
					"rationale": "Existing behavior is correct."
				}
			]
		}`)

		result, err := ParseAuthorDecisionResult(payload)
		if err != nil {
			t.Fatalf("ParseAuthorDecisionResult() error = %v", err)
		}
		want := AuthorDecisionResult{
			Decisions: []AuthorCommentDecision{
				{
					CommentID: "comment-1",
					Decision:  "implement",
					Language:  "ru",
					ReplyBody: "Добавлю обработку ошибки.",
					Rationale: "Комментарий обоснован.",
					Scope: &AuthorImplementScope{
						Title:        "Handle parse error",
						Instructions: "Return the error from ParseAuthorDecisionResult.",
						TargetFiles:  []string{"internal/arcreview/author_prompt.go"},
					},
				},
				{
					CommentID: "comment-2",
					Decision:  "argue",
					Language:  "en",
					ReplyBody: "This is intentional.",
					Rationale: "Existing behavior is correct.",
				},
			},
		}
		if !reflect.DeepEqual(result, want) {
			t.Fatalf("ParseAuthorDecisionResult() mismatch:\n got: %#v\nwant: %#v", result, want)
		}
	})

	t.Run("empty payload is rejected", func(t *testing.T) {
		if _, err := ParseAuthorDecisionResult([]byte("   ")); err == nil {
			t.Fatalf("expected error for empty payload, got nil")
		}
	})

	t.Run("extracts decisions from noisy output", func(t *testing.T) {
		payload := []byte("Проверил комментарии.\n```json\n" +
			`{"comment_decisions":[{"comment_id":"c1","decision":"resolve"}]}` +
			"\n```\nГотово.")
		result, err := ParseAuthorDecisionResult(payload)
		if err != nil {
			t.Fatalf("ParseAuthorDecisionResult() error = %v", err)
		}
		if len(result.Decisions) != 1 || result.Decisions[0].CommentID != "c1" {
			t.Fatalf("ParseAuthorDecisionResult() = %#v", result)
		}
	})

	t.Run("invalid JSON is rejected", func(t *testing.T) {
		if _, err := ParseAuthorDecisionResult([]byte(`{"comment_decisions": not valid}`)); err == nil {
			t.Fatalf("expected error for invalid JSON, got nil")
		}
	})

	t.Run("empty decisions list is accepted", func(t *testing.T) {
		result, err := ParseAuthorDecisionResult([]byte(`{"comment_decisions":[]}`))
		if err != nil {
			t.Fatalf("expected no error for empty decisions list, got %v", err)
		}
		if len(result.Decisions) != 0 {
			t.Fatalf("expected zero decisions, got %#v", result.Decisions)
		}
	})
}
