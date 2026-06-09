package main

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestBuildArcReviewProcessSpecBuildsStableCommandEnvAndLogPath(t *testing.T) {
	repoRoot := filepath.Join(string(filepath.Separator), "repo", "yolo")
	statePath := filepath.Join(repoRoot, ".yolo-runner", "arc-review-watch-state.db")
	eventsPath := filepath.Join(repoRoot, "runner-logs", "arc-review-watch.events.jsonl")

	spec := buildArcReviewProcessSpec(arcReviewProcessConfig{
		RepoRoot:   repoRoot,
		Workspace:  filepath.Join(repoRoot, "workspaces", "pr-42"),
		PRID:       "42",
		SessionID:  "pr-42",
		StatePath:  statePath,
		EventsPath: eventsPath,
	})

	wantArgv := []string{
		"arc-pr-review-runner",
		"--repo", repoRoot,
		"--workspace", filepath.Join(repoRoot, "workspaces", "pr-42"),
		"--pr-id", "42",
		"--state-path", statePath,
		"--events", eventsPath,
	}
	if !reflect.DeepEqual(spec.Argv, wantArgv) {
		t.Fatalf("unexpected argv:\n got %#v\nwant %#v", spec.Argv, wantArgv)
	}

	wantEnv := []string{"YOLO_ARC_REVIEW_SESSION_ID=pr-42"}
	if !reflect.DeepEqual(spec.Env, wantEnv) {
		t.Fatalf("unexpected env:\n got %#v\nwant %#v", spec.Env, wantEnv)
	}

	wantLogPath := filepath.Join(repoRoot, "runner-logs", "arc-pr-review-pr-42.log")
	if spec.LogPath != wantLogPath {
		t.Fatalf("unexpected log path: got %q want %q", spec.LogPath, wantLogPath)
	}
}
