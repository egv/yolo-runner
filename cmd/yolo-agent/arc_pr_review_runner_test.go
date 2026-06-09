package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	arcreviewstate "github.com/egv/yolo-runner/v2/internal/arcreview/state"
)

func TestArcPRReviewRunnerCommandOnceWritesHeartbeat(t *testing.T) {
	repoRoot := t.TempDir()
	statePath := filepath.Join(repoRoot, ".yolo-runner", "arc-review-watch-state.db")
	sessionID := "pr-42"

	store, err := arcreviewstate.Open(statePath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	_, err = store.CreateSession(arcreviewstate.Session{
		ID:        sessionID,
		PRID:      "42",
		Workspace: filepath.Join(repoRoot, "workspaces", "pr-42"),
		Status:    "running",
	})
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	code := RunMain([]string{
		"arc-pr-review-runner",
		"--repo", repoRoot,
		"--workspace", filepath.Join(repoRoot, "workspaces", "pr-42"),
		"--pr", "42",
		"--state", statePath,
		"--events", filepath.Join(repoRoot, "runner-logs", "arc-pr-review.events.jsonl"),
		"--once",
	}, func(context.Context, runConfig) error {
		t.Fatalf("legacy run function should not be called")
		return nil
	})
	if code != 0 {
		t.Fatalf("RunMain() exit code = %d, want 0", code)
	}

	reopened, err := arcreviewstate.Open(statePath)
	if err != nil {
		t.Fatalf("reopen state error = %v", err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	heartbeat, err := reopened.GetHeartbeat(sessionID)
	if err != nil {
		t.Fatalf("GetHeartbeat() error = %v", err)
	}
	if heartbeat.IsZero() {
		t.Fatalf("expected heartbeat to be written")
	}
	if time.Since(heartbeat) > time.Minute {
		t.Fatalf("heartbeat is too old: %s", heartbeat)
	}
}
