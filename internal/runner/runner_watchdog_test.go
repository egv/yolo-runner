package runner

import (
	"bytes"
	"strings"
	"testing"

	"yolo-runner/internal/opencode"
)

func TestRunOnceMarksBlockedOnStall(t *testing.T) {
	recorder := &callRecorder{}
	beads := &fakeBeads{
		recorder:   recorder,
		readyIssue: Issue{ID: "task-1", IssueType: "task", Status: "open"},
		showQueue:  []Bead{{ID: "task-1", Title: "Stall Task"}},
	}
	stallErr := &opencode.StallError{Category: "permission", LogPath: "/tmp/runner.log", OpenCodeLog: "/tmp/opencode.log"}
	deps := RunOnceDeps{
		Beads:    beads,
		Prompt:   &fakePrompt{recorder: recorder, prompt: "PROMPT"},
		OpenCode: &fakeOpenCode{recorder: recorder, err: stallErr},
		Git:      &fakeGit{recorder: recorder, dirty: true, rev: "deadbeef"},
		Logger:   &fakeLogger{recorder: recorder},
		Events:   &eventRecorder{},
	}
	opts := RunOnceOptions{RepoRoot: "/repo", RootID: "root", Out: &bytes.Buffer{}}

	result, err := RunOnce(opts, deps)
	if err == nil {
		t.Fatalf("expected error")
	}
	if result != "blocked" {
		t.Fatalf("expected blocked, got %q", result)
	}

	joined := strings.Join(recorder.calls, ",")
	if !strings.Contains(joined, "beads.update:blocked:opencode stall category=permission") {
		t.Fatalf("expected blocked status with reason, got %v", recorder.calls)
	}
	if !strings.Contains(err.Error(), "permission") {
		t.Fatalf("expected error to include classification, got %q", err.Error())
	}
}
