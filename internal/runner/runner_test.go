package runner

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type callRecorder struct {
	calls []string
}

func (r *callRecorder) record(entry string) {
	r.calls = append(r.calls, entry)
}

type fakeBeads struct {
	recorder   *callRecorder
	readyIssue Issue
	treeIssue  Issue
	showQueue  []Bead
}

func (f *fakeBeads) Ready(rootID string) (Issue, error) {
	if f.recorder != nil {
		f.recorder.record("beads.ready")
	}
	return f.readyIssue, nil
}

func (f *fakeBeads) Tree(rootID string) (Issue, error) {
	if f.recorder != nil {
		f.recorder.record("beads.tree")
	}
	if f.treeIssue.ID != "" || f.treeIssue.IssueType != "" || f.treeIssue.Status != "" || len(f.treeIssue.Children) > 0 {
		return f.treeIssue, nil
	}
	return f.readyIssue, nil
}

func (f *fakeBeads) Show(id string) (Bead, error) {
	if f.recorder != nil {
		f.recorder.record("beads.show")
	}
	if len(f.showQueue) == 0 {
		return Bead{}, nil
	}
	next := f.showQueue[0]
	f.showQueue = f.showQueue[1:]
	return next, nil
}

func (f *fakeBeads) UpdateStatus(id string, status string) error {
	if f.recorder != nil {
		f.recorder.record("beads.update:" + status)
	}
	return nil
}

func (f *fakeBeads) UpdateStatusWithReason(id string, status string, reason string) error {
	if f.recorder != nil {
		f.recorder.record("beads.update:" + status + ":" + reason)
	}
	return nil
}

func (f *fakeBeads) Close(id string) error {
	if f.recorder != nil {
		f.recorder.record("beads.close")
	}
	return nil
}

func (f *fakeBeads) Sync() error {
	if f.recorder != nil {
		f.recorder.record("beads.sync")
	}
	return nil
}

type fakePrompt struct {
	recorder *callRecorder
	prompt   string
}

func (f *fakePrompt) Build(issueID string, title string, description string, acceptance string) string {
	if f.recorder != nil {
		f.recorder.record("prompt.build")
	}
	return f.prompt
}

type fakeOpenCode struct {
	recorder *callRecorder
	err      error
}

func (f *fakeOpenCode) Run(issueID string, repoRoot string, prompt string, model string, configRoot string, configDir string, logPath string) error {
	if f.recorder != nil {
		f.recorder.record("opencode.run")
	}
	return f.err
}

type fakeGit struct {
	recorder *callRecorder
	dirty    bool
	rev      string
}

func (f *fakeGit) AddAll() error {
	if f.recorder != nil {
		f.recorder.record("git.add")
	}
	return nil
}

func (f *fakeGit) IsDirty() (bool, error) {
	if f.recorder != nil {
		f.recorder.record("git.dirty")
	}
	return f.dirty, nil
}

func (f *fakeGit) Commit(message string) error {
	if f.recorder != nil {
		f.recorder.record("git.commit:" + message)
	}
	return nil
}

func (f *fakeGit) RevParseHead() (string, error) {
	if f.recorder != nil {
		f.recorder.record("git.rev-parse")
	}
	return f.rev, nil
}

type fakeLogger struct {
	recorder *callRecorder
	entries  []logEntry
}

type logEntry struct {
	status string
}

type eventRecorder struct {
	events []Event
}

func (e *eventRecorder) Emit(event Event) {
	e.events = append(e.events, event)
}

func assertEvents(t *testing.T, events []Event, expected ...EventType) {
	t.Helper()
	if len(events) != len(expected) {
		t.Fatalf("expected %d events, got %d", len(expected), len(events))
	}
	for i, event := range events {
		if event.Type != expected[i] {
			t.Fatalf("event %d expected %q, got %q", i, expected[i], event.Type)
		}
		if event.Phase == "" {
			t.Fatalf("event %d expected phase", i)
		}
	}
}

func (f *fakeLogger) AppendRunnerSummary(repoRoot string, issueID string, title string, status string, commitSHA string) error {
	if f.recorder != nil {
		f.recorder.record("log.append:" + status)
	}
	f.entries = append(f.entries, logEntry{status: status})
	return nil
}

func TestRunOnceNoTasks(t *testing.T) {
	recorder := &callRecorder{}
	beads := &fakeBeads{
		recorder:   recorder,
		readyIssue: Issue{ID: "root", IssueType: "epic", Status: "open"},
	}
	deps := RunOnceDeps{
		Beads:    beads,
		Prompt:   &fakePrompt{recorder: recorder, prompt: "PROMPT"},
		OpenCode: &fakeOpenCode{recorder: recorder},
		Git:      &fakeGit{recorder: recorder},
		Logger:   &fakeLogger{recorder: recorder},
	}
	opts := RunOnceOptions{RepoRoot: "/repo", RootID: "root", Out: &bytes.Buffer{}}

	result, err := RunOnce(opts, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "no_tasks" {
		t.Fatalf("expected no_tasks, got %q", result)
	}
	if !strings.Contains(opts.Out.(*bytes.Buffer).String(), "No tasks available") {
		t.Fatalf("expected no tasks message, got %q", opts.Out.(*bytes.Buffer).String())
	}
	if strings.Join(recorder.calls, ",") != "beads.ready,beads.tree" {
		t.Fatalf("unexpected calls: %v", recorder.calls)
	}
}

func TestRunOnceDryRun(t *testing.T) {
	recorder := &callRecorder{}
	beads := &fakeBeads{
		recorder:   recorder,
		readyIssue: Issue{ID: "task-1", IssueType: "task", Status: "open"},
		showQueue: []Bead{{
			ID:                 "task-1",
			Title:              "Test Task",
			Description:        "Desc",
			AcceptanceCriteria: "Acceptance",
		}},
	}
	output := &bytes.Buffer{}
	deps := RunOnceDeps{
		Beads:    beads,
		Prompt:   &fakePrompt{recorder: recorder, prompt: "PROMPT"},
		OpenCode: &fakeOpenCode{recorder: recorder},
		Git:      &fakeGit{recorder: recorder},
		Logger:   &fakeLogger{recorder: recorder},
	}
	opts := RunOnceOptions{RepoRoot: "/repo", RootID: "root", DryRun: true, Out: output}

	result, err := RunOnce(opts, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "dry_run" {
		t.Fatalf("expected dry_run, got %q", result)
	}
	printed := output.String()
	if !strings.Contains(printed, "Task: task-1 - Test Task") {
		t.Fatalf("expected task line in output, got %q", printed)
	}
	if !strings.Contains(printed, "PROMPT") {
		t.Fatalf("expected prompt in output, got %q", printed)
	}
	if !strings.Contains(printed, "Command: opencode run PROMPT --agent yolo --format json /repo") {
		t.Fatalf("expected command in output, got %q", printed)
	}
	expectedCalls := "beads.ready,beads.tree,beads.show,prompt.build"
	if strings.Join(recorder.calls, ",") != expectedCalls {
		t.Fatalf("unexpected calls: %v", recorder.calls)
	}
}

func TestRunOnceNoChangesBlocksTask(t *testing.T) {
	recorder := &callRecorder{}
	beads := &fakeBeads{
		recorder:   recorder,
		readyIssue: Issue{ID: "task-1", IssueType: "task", Status: "open"},
		showQueue: []Bead{{
			ID:    "task-1",
			Title: "No Change",
		}},
	}
	logger := &fakeLogger{recorder: recorder}
	deps := RunOnceDeps{
		Beads:    beads,
		Prompt:   &fakePrompt{recorder: recorder, prompt: "PROMPT"},
		OpenCode: &fakeOpenCode{recorder: recorder},
		Git:      &fakeGit{recorder: recorder, dirty: false, rev: "abc123"},
		Logger:   logger,
	}

	opts := RunOnceOptions{RepoRoot: "/repo", RootID: "root", Out: &bytes.Buffer{}}

	result, err := RunOnce(opts, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "blocked" {
		t.Fatalf("expected blocked, got %q", result)
	}
	expectedCalls := []string{
		"beads.ready",
		"beads.tree",
		"beads.show",
		"prompt.build",
		"beads.update:in_progress",
		"opencode.run",
		"git.add",
		"git.dirty",
		"git.rev-parse",
		"log.append:blocked",
		"beads.update:blocked",
	}
	if strings.Join(recorder.calls, ",") != strings.Join(expectedCalls, ",") {
		t.Fatalf("unexpected calls: %v", recorder.calls)
	}
	if len(logger.entries) != 1 || logger.entries[0].status != "blocked" {
		t.Fatalf("expected blocked log entry, got %#v", logger.entries)
	}
}

func TestRunOnceChangesCommitCloseSync(t *testing.T) {
	recorder := &callRecorder{}
	beads := &fakeBeads{
		recorder:   recorder,
		readyIssue: Issue{ID: "task-1", IssueType: "task", Status: "open"},
		showQueue: []Bead{
			{ID: "task-1", Title: "My Task", Status: "open"},
			{ID: "task-1", Status: "closed"},
		},
	}
	logger := &fakeLogger{recorder: recorder}
	events := &eventRecorder{}
	deps := RunOnceDeps{
		Beads:    beads,
		Prompt:   &fakePrompt{recorder: recorder, prompt: "PROMPT"},
		OpenCode: &fakeOpenCode{recorder: recorder},
		Git:      &fakeGit{recorder: recorder, dirty: true, rev: "deadbeef"},
		Logger:   logger,
		Events:   events,
	}

	opts := RunOnceOptions{RepoRoot: "/repo", RootID: "root", Out: &bytes.Buffer{}}

	result, err := RunOnce(opts, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "completed" {
		t.Fatalf("expected completed, got %q", result)
	}
	expectedCalls := []string{
		"beads.ready",
		"beads.tree",
		"beads.show",
		"prompt.build",
		"beads.update:in_progress",
		"opencode.run",
		"git.add",
		"git.dirty",
		"git.commit:feat: my task",
		"git.rev-parse",
		"log.append:completed",
		"beads.close",
		"beads.show",
		"beads.sync",
	}
	if strings.Join(recorder.calls, ",") != strings.Join(expectedCalls, ",") {
		t.Fatalf("unexpected calls: %v", recorder.calls)
	}
	if len(logger.entries) != 1 || logger.entries[0].status != "completed" {
		t.Fatalf("expected completed log entry, got %#v", logger.entries)
	}
	assertEvents(t, events.events,
		EventSelectTask,
		EventBeadsUpdate,
		EventOpenCodeStart,
		EventOpenCodeEnd,
		EventGitAdd,
		EventGitStatus,
		EventGitCommit,
		EventBeadsClose,
		EventBeadsVerify,
		EventBeadsSync,
	)
}

func TestRunOnceUsesFallbackCommitMessage(t *testing.T) {
	recorder := &callRecorder{}
	beads := &fakeBeads{
		recorder:   recorder,
		readyIssue: Issue{ID: "task-1", IssueType: "task", Status: "open"},
		showQueue: []Bead{
			{ID: "task-1", Title: "", Status: "open"},
			{ID: "task-1", Status: "closed"},
		},
	}
	logger := &fakeLogger{recorder: recorder}
	deps := RunOnceDeps{
		Beads:    beads,
		Prompt:   &fakePrompt{recorder: recorder, prompt: "PROMPT"},
		OpenCode: &fakeOpenCode{recorder: recorder},
		Git:      &fakeGit{recorder: recorder, dirty: true, rev: "abc123"},
		Logger:   logger,
		Events:   &eventRecorder{},
	}

	opts := RunOnceOptions{RepoRoot: "/repo", RootID: "root", Out: &bytes.Buffer{}}

	_, err := RunOnce(opts, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, call := range recorder.calls {
		if call == "git.commit:feat: complete bead task" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected fallback commit message, got %v", recorder.calls)
	}
}

func TestRunOncePrintsProgressCounter(t *testing.T) {
	recorder := &callRecorder{}
	beads := &fakeBeads{
		recorder:   recorder,
		readyIssue: Issue{ID: "task-1", IssueType: "task", Status: "open"},
		showQueue: []Bead{
			{ID: "task-1", Title: "Progress Task", Status: "open"},
			{ID: "task-1", Status: "closed"},
		},
	}
	output := &bytes.Buffer{}
	deps := RunOnceDeps{
		Beads:    beads,
		Prompt:   &fakePrompt{recorder: recorder, prompt: "PROMPT"},
		OpenCode: &fakeOpenCode{recorder: recorder},
		Git:      &fakeGit{recorder: recorder, dirty: true, rev: "deadbeef"},
		Logger:   &fakeLogger{recorder: recorder},
		Events:   &eventRecorder{},
	}

	opts := RunOnceOptions{RepoRoot: "/repo", RootID: "root", Out: output, Progress: ProgressState{Completed: 2, Total: 5}}

	result, err := RunOnce(opts, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "completed" {
		t.Fatalf("expected completed, got %q", result)
	}
	if !strings.Contains(output.String(), "Starting [2/5] task-1: Progress Task") {
		t.Fatalf("expected progress counter in output, got %q", output.String())
	}
}

func TestRunOncePrintsLifecycle(t *testing.T) {
	start := time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC)
	prevNow := now
	now = func() time.Time { return start.Add(time.Second) }
	t.Cleanup(func() { now = prevNow })

	ticker := newFakeProgressTicker()
	openCode := &blockingOpenCode{started: make(chan struct{}), release: make(chan struct{})}
	output := &bytes.Buffer{}

	recorder := &callRecorder{}
	beads := &fakeBeads{
		recorder:   recorder,
		readyIssue: Issue{ID: "task-1", IssueType: "task", Status: "open"},
		showQueue: []Bead{
			{ID: "task-1", Title: "My Task", Status: "open"},
			{ID: "task-1", Status: "closed"},
		},
	}
	deps := RunOnceDeps{
		Beads:    beads,
		Prompt:   &fakePrompt{recorder: recorder, prompt: "PROMPT"},
		OpenCode: openCode,
		Git:      &fakeGit{recorder: recorder, dirty: true, rev: "deadbeef"},
		Logger:   &fakeLogger{recorder: recorder},
		Events:   &eventRecorder{},
	}

	opts := RunOnceOptions{RepoRoot: "/repo", RootID: "root", Out: output, ProgressTicker: ticker, LogPath: filepath.Join(t.TempDir(), "log.jsonl")}

	resultCh := make(chan struct{})
	var result string
	var err error
	go func() {
		result, err = RunOnce(opts, deps)
		close(resultCh)
	}()

	<-openCode.started
	close(openCode.release)
	select {
	case <-resultCh:
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("expected RunOnce to finish")
	}

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "completed" {
		t.Fatalf("expected completed, got %q", result)
	}
	printed := output.String()
	if !strings.Contains(printed, "Starting [0/1] task-1: My Task") {
		t.Fatalf("expected start line, got %q", printed)
	}
	if !strings.Contains(printed, "State: selecting task") {
		t.Fatalf("expected selecting task state, got %q", printed)
	}
	if !strings.Contains(printed, "State: opencode running") {
		t.Fatalf("expected opencode state, got %q", printed)
	}
	if !strings.Contains(printed, "Finished task-1: completed (0s)") {
		t.Fatalf("expected finish line with elapsed, got %q", printed)
	}
}
