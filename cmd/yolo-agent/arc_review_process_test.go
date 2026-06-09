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
		"--once",
	}
	if !reflect.DeepEqual(spec.Argv, wantArgv) {
		t.Fatalf("unexpected argv:\n got %#v\nwant %#v", spec.Argv, wantArgv)
	}

	if len(spec.Env) != 0 {
		t.Fatalf("unexpected env: %#v", spec.Env)
	}

	wantLogPath := filepath.Join(repoRoot, "runner-logs", "arc-pr-review-pr-42.log")
	if spec.LogPath != wantLogPath {
		t.Fatalf("unexpected log path: got %q want %q", spec.LogPath, wantLogPath)
	}
}
