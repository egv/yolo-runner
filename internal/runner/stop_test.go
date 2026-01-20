package runner

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

type fakeCleanupGit struct {
	recorder *callRecorder
	status   string
}

func (f *fakeCleanupGit) StatusPorcelain() (string, error) {
	if f.recorder != nil {
		f.recorder.record("git.status")
	}
	return f.status, nil
}

func (f *fakeCleanupGit) RestoreAll() error {
	if f.recorder != nil {
		f.recorder.record("git.restore")
	}
	return nil
}

func (f *fakeCleanupGit) CleanAll() error {
	if f.recorder != nil {
		f.recorder.record("git.clean")
	}
	return nil
}

type fakeOpenCodeWithContext struct {
	recorder *callRecorder
	started  chan struct{}
	ctx      context.Context
}

func (f *fakeOpenCodeWithContext) Run(issueID string, repoRoot string, prompt string, model string, configRoot string, configDir string, logPath string) error {
	panic("Run should not be called")
}

func (f *fakeOpenCodeWithContext) RunWithContext(ctx context.Context, issueID string, repoRoot string, prompt string, model string, configRoot string, configDir string, logPath string) error {
	if f.recorder != nil {
		f.recorder.record("opencode.run.ctx")
	}
	f.ctx = ctx
	close(f.started)
	<-ctx.Done()
	return ctx.Err()
}

func TestStopStateRequestCancelsContext(t *testing.T) {
	stop := NewStopState()
	if stop.Requested() {
		t.Fatalf("expected stop to be false initially")
	}

	stop.Request()
	if !stop.Requested() {
		t.Fatalf("expected stop to be requested")
	}

	select {
	case <-stop.Context().Done():
		// ok
	default:
		t.Fatalf("expected stop context to be canceled")
	}
}

func TestStopCleanupRestoresOnConfirm(t *testing.T) {
	recorder := &callRecorder{}
	beads := &fakeBeads{recorder: recorder}
	git := &fakeCleanupGit{recorder: recorder, status: " M file.go"}
	stop := NewStopState()
	stop.MarkInProgress("task-1")
	stop.Request()

	confirmCalled := false
	confirm := func(summary string) (bool, error) {
		confirmCalled = true
		if !strings.Contains(summary, "file.go") {
			t.Fatalf("expected summary to include status, got %q", summary)
		}
		return true, nil
	}

	output := &bytes.Buffer{}
	err := CleanupAfterStop(stop, StopCleanupConfig{
		Beads:   beads,
		Git:     git,
		Out:     output,
		Confirm: confirm,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !confirmCalled {
		t.Fatalf("expected confirmation prompt")
	}
	expected := []string{
		"beads.update:open",
		"git.status",
		"git.restore",
		"git.clean",
	}
	if strings.Join(recorder.calls, ",") != strings.Join(expected, ",") {
		t.Fatalf("unexpected calls: %v", recorder.calls)
	}
	if !strings.Contains(output.String(), "file.go") {
		t.Fatalf("expected status output, got %q", output.String())
	}
}

func TestStopCleanupSkipsRestoreOnDecline(t *testing.T) {
	recorder := &callRecorder{}
	beads := &fakeBeads{recorder: recorder}
	git := &fakeCleanupGit{recorder: recorder, status: " M file.go"}
	stop := NewStopState()
	stop.MarkInProgress("task-1")
	stop.Request()

	confirm := func(summary string) (bool, error) {
		return false, nil
	}

	err := CleanupAfterStop(stop, StopCleanupConfig{
		Beads:   beads,
		Git:     git,
		Out:     &bytes.Buffer{},
		Confirm: confirm,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []string{
		"beads.update:open",
		"git.status",
	}
	if strings.Join(recorder.calls, ",") != strings.Join(expected, ",") {
		t.Fatalf("unexpected calls: %v", recorder.calls)
	}
}

func TestStopCleanupSkipsPromptWhenClean(t *testing.T) {
	recorder := &callRecorder{}
	beads := &fakeBeads{recorder: recorder}
	git := &fakeCleanupGit{recorder: recorder, status: ""}
	stop := NewStopState()

	stop.MarkInProgress("task-1")
	stop.Request()

	confirmCalled := false
	confirm := func(summary string) (bool, error) {
		confirmCalled = true
		return false, nil
	}

	err := CleanupAfterStop(stop, StopCleanupConfig{
		Beads:   beads,
		Git:     git,
		Out:     &bytes.Buffer{},
		Confirm: confirm,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if confirmCalled {
		t.Fatalf("expected confirmation to be skipped")
	}
	expected := []string{
		"beads.update:open",
		"git.status",
	}
	if strings.Join(recorder.calls, ",") != strings.Join(expected, ",") {
		t.Fatalf("unexpected calls: %v", recorder.calls)
	}
}

func TestRunOnceStopsOnRequest(t *testing.T) {
	recorder := &callRecorder{}
	beads := &fakeBeads{
		recorder:   recorder,
		readyIssue: Issue{ID: "task-1", IssueType: "task", Status: "open"},
		showQueue: []Bead{{
			ID:                 "task-1",
			Title:              "Stop Task",
			Description:        "desc",
			AcceptanceCriteria: "accept",
		}},
	}
	openCode := &fakeOpenCodeWithContext{recorder: recorder, started: make(chan struct{})}
	stop := NewStopState()

	opts := RunOnceOptions{RepoRoot: "/repo", RootID: "root", Out: &bytes.Buffer{}, Stop: stop}
	deps := RunOnceDeps{
		Beads:    beads,
		Prompt:   &fakePrompt{recorder: recorder, prompt: "PROMPT"},
		OpenCode: openCode,
		Git:      &fakeGit{recorder: recorder, dirty: false, rev: "abc123"},
		Logger:   &fakeLogger{recorder: recorder},
	}

	resultCh := make(chan struct{})
	var result string
	var err error
	go func() {
		result, err = RunOnce(opts, deps)
		close(resultCh)
	}()

	<-openCode.started
	stop.Request()

	confirm := func(summary string) (bool, error) {
		return false, nil
	}
	err = CleanupAfterStop(stop, StopCleanupConfig{
		Beads:   deps.Beads,
		Git:     &fakeCleanupGit{recorder: recorder, status: " M file.go"},
		Out:     &bytes.Buffer{},
		Confirm: confirm,
	})
	if err != nil {
		t.Fatalf("cleanup error: %v", err)
	}

	select {
	case <-resultCh:
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("expected RunOnce to stop")
	}

	if err == nil || err != context.Canceled {
		t.Fatalf("expected context canceled, got %v", err)
	}
	if result != "stopped" {
		t.Fatalf("expected stopped result, got %q", result)
	}

	joined := strings.Join(recorder.calls, ",")
	if !strings.Contains(joined, "beads.update:open") {
		t.Fatalf("expected beads reset, got %v", recorder.calls)
	}
	if !strings.Contains(joined, "git.status") {
		t.Fatalf("expected git status, got %v", recorder.calls)
	}
}
