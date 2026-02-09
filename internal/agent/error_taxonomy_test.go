package agent

import (
	"errors"
	"strings"
	"testing"
)

func TestFormatActionableErrorIncludesCategoryCauseAndNextStep(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		category string
	}{
		{name: "git vcs", err: errors.New("git checkout feature/task failed"), category: "git/vcs"},
		{name: "tracker", err: errors.New("tk show task-1: file not found"), category: "tracker"},
		{name: "runner init", err: errors.New("serena initialization failed: missing config"), category: "runner_init"},
		{name: "runner timeout stall", err: errors.New("opencode stall category=no_output"), category: "runner_timeout_stall"},
		{name: "review gating", err: errors.New("review rejected: failing acceptance criteria"), category: "review_gating"},
		{name: "merge queue conflict", err: errors.New("merge conflict while landing branch"), category: "merge_queue_conflict"},
		{name: "auth profile config", err: errors.New("auth token missing for profile default"), category: "auth_profile_config"},
		{name: "filesystem clone", err: errors.New("chdir /missing/repo: no such file or directory"), category: "filesystem_clone"},
		{name: "lock contention", err: errors.New("task lock already held by another worker"), category: "lock_contention"},
		{name: "dirty worktree", err: errors.New("worktree is dirty: commit or stash changes"), category: "git/vcs"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			message := FormatActionableError(tc.err)
			if !strings.Contains(message, "Category: "+tc.category) {
				t.Fatalf("expected category %q in message, got %q", tc.category, message)
			}
			if !strings.Contains(message, "Cause: "+tc.err.Error()) {
				t.Fatalf("expected cause in message, got %q", message)
			}
			if !strings.Contains(message, "Next step:") {
				t.Fatalf("expected next step in message, got %q", message)
			}
		})
	}
}

func TestFormatActionableErrorFallsBackToUnknownCategory(t *testing.T) {
	err := errors.New("unexpected boom")
	message := FormatActionableError(err)

	if !strings.Contains(message, "Category: unknown") {
		t.Fatalf("expected unknown category, got %q", message)
	}
	if !strings.Contains(message, "Cause: "+err.Error()) {
		t.Fatalf("expected cause in message, got %q", message)
	}
	if !strings.Contains(message, "Next step:") {
		t.Fatalf("expected next step in message, got %q", message)
	}
}
