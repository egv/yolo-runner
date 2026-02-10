package main

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anomalyco/yolo-runner/internal/contracts"
)

func TestRunMainParsesFlagsAndInvokesRun(t *testing.T) {
	called := false
	var got runConfig
	run := func(_ context.Context, cfg runConfig) error {
		called = true
		got = cfg
		return nil
	}

	code := RunMain([]string{"--repo", "/repo", "--root", "root-1", "--model", "openai/gpt-5.3-codex", "--max", "2", "--concurrency", "3", "--dry-run", "--runner-timeout", "30s", "--events", "/repo/runner-logs/agent.events.jsonl"}, run)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if !called {
		t.Fatalf("expected run function to be called")
	}
	if got.repoRoot != "/repo" || got.rootID != "root-1" || got.model != "openai/gpt-5.3-codex" {
		t.Fatalf("unexpected config: %#v", got)
	}
	if got.maxTasks != 2 || !got.dryRun {
		t.Fatalf("expected max=2 dry-run=true, got %#v", got)
	}
	if got.runnerTimeout != 30*time.Second {
		t.Fatalf("expected runner timeout 30s, got %s", got.runnerTimeout)
	}
	if got.eventsPath != "/repo/runner-logs/agent.events.jsonl" {
		t.Fatalf("expected events path to be parsed, got %q", got.eventsPath)
	}
	if got.concurrency != 3 {
		t.Fatalf("expected concurrency=3, got %d", got.concurrency)
	}
	if got.stream {
		t.Fatalf("expected stream=false by default")
	}
}

func TestRunMainParsesStreamFlag(t *testing.T) {
	called := false
	var got runConfig
	run := func(_ context.Context, cfg runConfig) error {
		called = true
		got = cfg
		return nil
	}

	code := RunMain([]string{"--repo", "/repo", "--root", "root-1", "--stream"}, run)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if !called {
		t.Fatalf("expected run function to be called")
	}
	if !got.stream {
		t.Fatalf("expected stream=true")
	}
}

func TestRunMainParsesVerboseStreamFlag(t *testing.T) {
	called := false
	var got runConfig
	run := func(_ context.Context, cfg runConfig) error {
		called = true
		got = cfg
		return nil
	}

	code := RunMain([]string{"--repo", "/repo", "--root", "root-1", "--stream", "--verbose-stream"}, run)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if !called {
		t.Fatalf("expected run function to be called")
	}
	if !got.stream {
		t.Fatalf("expected stream=true")
	}
	if !got.verboseStream {
		t.Fatalf("expected verboseStream=true")
	}
}

func TestResolveEventsPathDisablesDefaultFileInStreamMode(t *testing.T) {
	got := resolveEventsPath(runConfig{repoRoot: "/repo", stream: true, eventsPath: ""})
	if got != "" {
		t.Fatalf("expected no default file path in stream mode, got %q", got)
	}
}

func TestResolveEventsPathKeepsDefaultFileWhenNotStreaming(t *testing.T) {
	got := resolveEventsPath(runConfig{repoRoot: "/repo", stream: false, eventsPath: ""})
	expected := "/repo/runner-logs/agent.events.jsonl"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestRunWithComponentsStreamWritesNDJSONToStdout(t *testing.T) {
	originalStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() {
		os.Stdout = originalStdout
	}()

	mgr := &testTaskManager{
		tasks: []contracts.Task{{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen}},
	}
	runner := &testRunner{}
	cfg := runConfig{repoRoot: t.TempDir(), rootID: "root", dryRun: true, stream: true}

	runErr := runWithComponents(context.Background(), cfg, mgr, runner, nil)
	if runErr != nil {
		t.Fatalf("runWithComponents failed: %v", runErr)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("close reader: %v", err)
	}
	out := string(data)
	if !strings.Contains(out, `"type":"task_started"`) {
		t.Fatalf("expected task_started event in stdout, got %q", out)
	}
	if strings.Contains(out, "Category:") {
		t.Fatalf("expected stdout to contain JSON events only, got %q", out)
	}
	_ = filepath.Join
}

func TestRunWithComponentsStreamCoalescesRunnerOutputByDefault(t *testing.T) {
	originalStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() {
		os.Stdout = originalStdout
	}()

	repoRoot := initGitRepo(t)
	mgr := &testTaskManager{tasks: []contracts.Task{{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen}}}
	runner := &progressRunner{updates: []contracts.RunnerProgress{{Type: "runner_output", Message: "1"}, {Type: "runner_output", Message: "2"}, {Type: "runner_output", Message: "3"}, {Type: "runner_output", Message: "4"}}}
	cfg := runConfig{repoRoot: repoRoot, rootID: "root", stream: true, streamOutputInterval: time.Hour, streamOutputBuffer: 2}

	runErr := runWithComponents(context.Background(), cfg, mgr, runner, nil)
	if runErr != nil {
		t.Fatalf("runWithComponents failed: %v", runErr)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("close reader: %v", err)
	}

	out := string(data)
	if got := strings.Count(out, `"type":"runner_output"`); got != 2 {
		t.Fatalf("expected coalesced runner_output count=2, got %d output=%q", got, out)
	}
	if !strings.Contains(out, `"coalesced_outputs":"1"`) {
		t.Fatalf("expected coalescing metadata in output, got %q", out)
	}
}

func TestRunWithComponentsVerboseStreamEmitsAllRunnerOutput(t *testing.T) {
	originalStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() {
		os.Stdout = originalStdout
	}()

	repoRoot := initGitRepo(t)
	mgr := &testTaskManager{tasks: []contracts.Task{{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen}}}
	runner := &progressRunner{updates: []contracts.RunnerProgress{{Type: "runner_output", Message: "1"}, {Type: "runner_output", Message: "2"}, {Type: "runner_output", Message: "3"}, {Type: "runner_output", Message: "4"}}}
	cfg := runConfig{repoRoot: repoRoot, rootID: "root", stream: true, verboseStream: true, streamOutputInterval: time.Hour, streamOutputBuffer: 2}

	runErr := runWithComponents(context.Background(), cfg, mgr, runner, nil)
	if runErr != nil {
		t.Fatalf("runWithComponents failed: %v", runErr)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("close reader: %v", err)
	}

	out := string(data)
	if got := strings.Count(out, `"type":"runner_output"`); got != 4 {
		t.Fatalf("expected full runner_output count=4, got %d output=%q", got, out)
	}
}

type testTaskManager struct {
	tasks []contracts.Task
	idx   int
}

func (m *testTaskManager) NextTasks(context.Context, string) ([]contracts.TaskSummary, error) {
	if m.idx >= len(m.tasks) {
		return nil, nil
	}
	task := m.tasks[m.idx]
	m.idx++
	return []contracts.TaskSummary{{ID: task.ID, Title: task.Title}}, nil
}

func (m *testTaskManager) GetTask(_ context.Context, taskID string) (contracts.Task, error) {
	for _, task := range m.tasks {
		if task.ID == taskID {
			return task, nil
		}
	}
	return contracts.Task{}, errors.New("task not found")
}

func (m *testTaskManager) SetTaskStatus(context.Context, string, contracts.TaskStatus) error {
	return nil
}
func (m *testTaskManager) SetTaskData(context.Context, string, map[string]string) error { return nil }

type testRunner struct{}

func (testRunner) Run(context.Context, contracts.RunnerRequest) (contracts.RunnerResult, error) {
	return contracts.RunnerResult{Status: contracts.RunnerResultCompleted}, nil
}

type progressRunner struct {
	updates []contracts.RunnerProgress
	calls   int
}

func (r *progressRunner) Run(_ context.Context, request contracts.RunnerRequest) (contracts.RunnerResult, error) {
	r.calls++
	if request.OnProgress != nil && r.calls == 1 {
		for _, update := range r.updates {
			request.OnProgress(update)
		}
	}
	if r.calls == 1 {
		return contracts.RunnerResult{Status: contracts.RunnerResultCompleted}, nil
	}
	return contracts.RunnerResult{Status: contracts.RunnerResultCompleted, ReviewReady: true}, nil
}

func TestRunMainRequiresRoot(t *testing.T) {
	code := RunMain([]string{"--repo", "/repo"}, func(context.Context, runConfig) error { return nil })
	if code != 1 {
		t.Fatalf("expected exit code 1 when root missing, got %d", code)
	}
}

func TestRunMainRejectsNonPositiveConcurrency(t *testing.T) {
	called := false
	code := RunMain([]string{"--repo", "/repo", "--root", "root-1", "--concurrency", "0"}, func(context.Context, runConfig) error {
		called = true
		return nil
	})

	if code != 1 {
		t.Fatalf("expected exit code 1 when concurrency is non-positive, got %d", code)
	}
	if called {
		t.Fatalf("expected run function not to be called for invalid concurrency")
	}
}

func TestRunMainPrintsActionableTaxonomyMessageOnRunError(t *testing.T) {
	run := func(context.Context, runConfig) error {
		return errors.New("git checkout task/t-1 failed")
	}

	errText := captureStderr(t, func() {
		code := RunMain([]string{"--repo", "/repo", "--root", "root-1"}, run)
		if code != 1 {
			t.Fatalf("expected exit code 1, got %d", code)
		}
	})

	if !strings.Contains(errText, "Category: git/vcs") {
		t.Fatalf("expected category in stderr, got %q", errText)
	}
	if !strings.Contains(errText, "Cause: git checkout task/t-1 failed") {
		t.Fatalf("expected cause in stderr, got %q", errText)
	}
	if !strings.Contains(errText, "Next step:") {
		t.Fatalf("expected next step in stderr, got %q", errText)
	}
}

func TestRunMainHidesGenericExitStatusInActionableMessage(t *testing.T) {
	run := func(context.Context, runConfig) error {
		return errors.Join(
			errors.New("git checkout main failed: error: Your local changes would be overwritten by checkout"),
			errors.New("exit status 1"),
		)
	}

	errText := captureStderr(t, func() {
		code := RunMain([]string{"--repo", "/repo", "--root", "root-1"}, run)
		if code != 1 {
			t.Fatalf("expected exit code 1, got %d", code)
		}
	})

	if strings.Contains(errText, "exit status 1") {
		t.Fatalf("expected generic exit status to be removed, got %q", errText)
	}
	if !strings.Contains(errText, "Category: git/vcs") {
		t.Fatalf("expected categorized error, got %q", errText)
	}
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	original := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w
	defer func() {
		os.Stderr = original
	}()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read stderr: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("close reader: %v", err)
	}
	return string(data)
}

func initGitRepo(t *testing.T) string {
	t.Helper()
	repoRoot := t.TempDir()
	cmd := exec.Command("git", "init", repoRoot)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git init failed: %v output=%s", err, string(out))
	}
	return repoRoot
}
