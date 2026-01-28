package runner

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/anomalyco/yolo-runner/internal/opencode"
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
	if !strings.Contains(joined, "beads.tree") {
		t.Fatalf("expected tree call, got %v", recorder.calls)
	}
	if !strings.Contains(err.Error(), "permission") {
		t.Fatalf("expected error to include classification, got %q", err.Error())
	}
}

func TestRunOnceStallReasonFallbackOnUpdateFailure(t *testing.T) {
	recorder := &callRecorder{}
	beads := &fakeBeads{
		recorder:   recorder,
		readyIssue: Issue{ID: "task-1", IssueType: "task", Status: "open"},
		showQueue:  []Bead{{ID: "task-1", Title: "Stall Task"}},
	}
	reasonErr := errors.New("reason too long")
	beadsFailing := &fakeBeadsFailingReason{
		fakeBeads:   beads,
		reasonErr:   reasonErr,
		shortReason: "opencode stall category=no_output",
	}
	stallErr := &opencode.StallError{
		Category:      "no_output",
		LogPath:       "/tmp/runner.log",
		OpenCodeLog:   "/tmp/opencode.log",
		LastOutputAge: 2 * time.Minute,
		Tail:          []string{strings.Repeat("x", 50)},
	}
	deps := RunOnceDeps{
		Beads:    beadsFailing,
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
	if !strings.Contains(joined, "beads.update:blocked:opencode stall category=no_output") {
		t.Fatalf("expected fallback blocked update, got %v", recorder.calls)
	}
	if !strings.Contains(err.Error(), "no_output") {
		t.Fatalf("expected error to include classification, got %q", err.Error())
	}
}

func TestRunOnceStallReasonTruncatesLongTail(t *testing.T) {
	recorder := &callRecorder{}
	beads := &fakeBeads{
		recorder:   recorder,
		readyIssue: Issue{ID: "task-1", IssueType: "task", Status: "open"},
		showQueue:  []Bead{{ID: "task-1", Title: "Stall Task"}},
	}
	stallErr := &opencode.StallError{
		Category:      "permission",
		LogPath:       "/tmp/runner.log",
		OpenCodeLog:   "/tmp/opencode.log",
		LastOutputAge: 30 * time.Second,
		Tail:          []string{strings.Repeat("x", 5000)},
	}
	deps := RunOnceDeps{
		Beads:    beads,
		Prompt:   &fakePrompt{recorder: recorder, prompt: "PROMPT"},
		OpenCode: &fakeOpenCode{recorder: recorder, err: stallErr},
		Git:      &fakeGit{recorder: recorder, dirty: true, rev: "deadbeef"},
		Logger:   &fakeLogger{recorder: recorder},
		Events:   &eventRecorder{},
	}
	opts := RunOnceOptions{RepoRoot: "/repo", RootID: "root", Out: &bytes.Buffer{}}

	_, err := RunOnce(opts, deps)
	if err == nil {
		t.Fatalf("expected error")
	}

	joined := strings.Join(recorder.calls, ",")
	prefix := "beads.update:blocked:opencode stall category=permission"
	index := strings.Index(joined, prefix)
	if index == -1 {
		t.Fatalf("expected blocked status with reason, got %v", recorder.calls)
	}
	reason := joined[index+len(prefix):]
	if strings.Contains(reason, strings.Repeat("x", 5000)) {
		t.Fatalf("expected reason to be truncated")
	}
}

func TestRunOnceMarksBlockedOnVerificationFailure(t *testing.T) {
	recorder := &callRecorder{}
	beads := &fakeBeads{
		recorder:   recorder,
		readyIssue: Issue{ID: "task-1", IssueType: "task", Status: "open"},
		showQueue:  []Bead{{ID: "task-1", Title: "Verify Task"}},
	}
	verifyErr := &opencode.VerificationError{Reason: "verification did not confirm completion"}
	deps := RunOnceDeps{
		Beads:    beads,
		Prompt:   &fakePrompt{recorder: recorder, prompt: "PROMPT"},
		OpenCode: &fakeOpenCode{recorder: recorder, err: verifyErr},
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
	if !strings.Contains(joined, "beads.update:blocked:verification did not confirm completion") {
		t.Fatalf("expected blocked status with reason, got %v", recorder.calls)
	}
	if !strings.Contains(err.Error(), "verification") {
		t.Fatalf("expected error to include verification, got %q", err.Error())
	}
}

func TestRunOnceStallReasonSanitizesUnsafeChars(t *testing.T) {
	recorder := &callRecorder{}
	beads := &fakeBeads{
		recorder:   recorder,
		readyIssue: Issue{ID: "task-1", IssueType: "task", Status: "open"},
		showQueue:  []Bead{{ID: "task-1", Title: "Stall Task"}},
	}
	stallErr := &opencode.StallError{
		Category:      "permission",
		LogPath:       "/tmp/runner.log",
		OpenCodeLog:   "/tmp/opencode.log",
		LastOutputAge: 30 * time.Second,
		Tail:          []string{"line|with|pipes", "line\twith\ttabs"},
	}
	deps := RunOnceDeps{
		Beads:    beads,
		Prompt:   &fakePrompt{recorder: recorder, prompt: "PROMPT"},
		OpenCode: &fakeOpenCode{recorder: recorder, err: stallErr},
		Git:      &fakeGit{recorder: recorder, dirty: true, rev: "deadbeef"},
		Logger:   &fakeLogger{recorder: recorder},
		Events:   &eventRecorder{},
	}
	opts := RunOnceOptions{RepoRoot: "/repo", RootID: "root", Out: &bytes.Buffer{}}

	_, err := RunOnce(opts, deps)
	if err == nil {
		t.Fatalf("expected error")
	}

	joined := strings.Join(recorder.calls, ",")
	prefix := "beads.update:blocked:"
	index := strings.Index(joined, prefix)
	if index == -1 {
		t.Fatalf("expected blocked status with reason, got %v", recorder.calls)
	}
	reason := joined[index+len(prefix):]
	if strings.Contains(reason, "|") {
		t.Fatalf("expected reason to sanitize pipes, got %q", reason)
	}
	if strings.Contains(reason, "\t") || strings.Contains(reason, "\n") {
		t.Fatalf("expected reason to sanitize control chars, got %q", reason)
	}
}

func TestRunOnceStallReasonFallsBackToStatusOnly(t *testing.T) {
	recorder := &callRecorder{}
	beads := &fakeBeads{
		recorder:   recorder,
		readyIssue: Issue{ID: "task-1", IssueType: "task", Status: "open"},
		showQueue:  []Bead{{ID: "task-1", Title: "Stall Task"}},
	}
	beadsFailing := &fakeBeadsRejectingReason{
		fakeBeads: beads,
		reasonErr: errors.New("invalid reason"),
	}
	stallErr := &opencode.StallError{Category: "no_output", LogPath: "/tmp/runner.log", OpenCodeLog: "/tmp/opencode.log"}
	deps := RunOnceDeps{
		Beads:    beadsFailing,
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
	if !strings.Contains(joined, "beads.update:blocked") {
		t.Fatalf("expected fallback status update, got %v", recorder.calls)
	}
}

type fakeBeadsFailingReason struct {
	*fakeBeads
	reasonErr   error
	shortReason string
}

type fakeBeadsRejectingReason struct {
	*fakeBeads
	reasonErr error
}

func (f *fakeBeadsRejectingReason) UpdateStatusWithReason(id string, status string, reason string) error {
	if f.recorder != nil {
		f.recorder.record("beads.update:" + status + ":" + reason)
	}
	return f.reasonErr
}

func (f *fakeBeadsFailingReason) UpdateStatusWithReason(id string, status string, reason string) error {
	if f.recorder != nil {
		f.recorder.record("beads.update:" + status + ":" + reason)
	}
	if reason == f.shortReason {
		return nil
	}
	return f.reasonErr
}
