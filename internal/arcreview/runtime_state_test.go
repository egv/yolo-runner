package arcreview

import (
	"reflect"
	"testing"
	"time"
)

func TestNormalizePRRuntimeStateIncludesReviewInputs(t *testing.T) {
	createdAt := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)

	state := NormalizePRRuntimeState(
		PRDetails{
			ID:       "ARW-42",
			Title:    "Add runtime state",
			Author:   "alice",
			Branch:   "trunk",
			Status:   "open",
			Revision: "r42",
			Issues: []PRIssue{
				{ID: "issue-1", Status: "open", Severity: "major", Path: "runner.go", Line: 17, Message: "handle nil checks", Author: "bob"},
				{ID: "issue-2", Status: "resolved", Severity: "minor", Path: "runner_test.go", Line: 22, Message: "done", Author: "carol"},
			},
		},
		[]PRComment{
			{ID: "comment-1", Author: "bob", Body: "Please cover nil checks.", CreatedAt: createdAt},
			{ID: "comment-2", Author: "alice", Body: "Added coverage.", CreatedAt: createdAt.Add(time.Minute), Resolved: true},
		},
		[]PRChangedFile{
			{Path: "internal/arcreview/runtime_state.go", Status: "added", Additions: 120, Deletions: 0},
			{Path: "internal/arcreview/runtime_state_test.go", Status: "added", Additions: 90, Deletions: 0},
		},
		[]PRCheck{
			{Name: "go test ./internal/arcreview", Status: "passed", URL: "https://ci.example.test/check/1"},
			{Name: "lint", Status: "pending"},
		},
	)

	if state.PRID != "ARW-42" {
		t.Fatalf("PRID = %q, want ARW-42", state.PRID)
	}
	if state.Revision != "r42" {
		t.Fatalf("Revision = %q, want r42", state.Revision)
	}
	if state.Details.Title != "Add runtime state" || state.Details.Author != "alice" || state.Details.Branch != "trunk" {
		t.Fatalf("Details not preserved: %#v", state.Details)
	}
	if !reflect.DeepEqual(state.Comments, []PRComment{
		{ID: "comment-1", Author: "bob", Body: "Please cover nil checks.", CreatedAt: createdAt},
		{ID: "comment-2", Author: "alice", Body: "Added coverage.", CreatedAt: createdAt.Add(time.Minute), Resolved: true},
	}) {
		t.Fatalf("Comments mismatch:\ngot:  %#v", state.Comments)
	}
	if !reflect.DeepEqual(state.OpenIssues, []PRIssue{
		{ID: "issue-1", Status: "open", Severity: "major", Path: "runner.go", Line: 17, Message: "handle nil checks", Author: "bob"},
	}) {
		t.Fatalf("OpenIssues mismatch:\ngot:  %#v", state.OpenIssues)
	}
	if !reflect.DeepEqual(state.ChangedFiles, []PRChangedFile{
		{Path: "internal/arcreview/runtime_state.go", Status: "added", Additions: 120, Deletions: 0},
		{Path: "internal/arcreview/runtime_state_test.go", Status: "added", Additions: 90, Deletions: 0},
	}) {
		t.Fatalf("ChangedFiles mismatch:\ngot:  %#v", state.ChangedFiles)
	}
	if !reflect.DeepEqual(state.Checks, []PRCheck{
		{Name: "go test ./internal/arcreview", Status: "passed", URL: "https://ci.example.test/check/1"},
		{Name: "lint", Status: "pending"},
	}) {
		t.Fatalf("Checks mismatch:\ngot:  %#v", state.Checks)
	}
}
