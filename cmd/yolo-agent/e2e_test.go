package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anomalyco/yolo-runner/internal/claude"
	"github.com/anomalyco/yolo-runner/internal/codex"
	"github.com/anomalyco/yolo-runner/internal/contracts"
	"github.com/anomalyco/yolo-runner/internal/tk"
	"github.com/anomalyco/yolo-runner/internal/ui/monitor"
)

func TestE2E_YoloAgentRunCompletesSeededTKTask(t *testing.T) {
	if _, err := exec.LookPath("tk"); err != nil {
		t.Skip("tk CLI is required for e2e test")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git CLI is required for e2e test")
	}

	repo := t.TempDir()
	runCommand(t, repo, "git", "init")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write seed file: %v", err)
	}
	runCommand(t, repo, "git", "add", "README.md")
	runCommand(t, repo, "git", "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "init")

	runner := localRunner{dir: repo}
	rootID := mustCreateTicket(t, runner, "Roadmap", "epic", "0", "")
	taskID := mustCreateTicket(t, runner, "Self-host task", "task", "0", rootID)

	taskManager := tk.NewTaskManager(runner)
	fakeAgent := &fakeAgentRunner{results: []contracts.RunnerResult{{Status: contracts.RunnerResultCompleted}, {Status: contracts.RunnerResultCompleted, ReviewReady: true}}}
	fakeVCS := &fakeVCS{}

	err := runWithComponents(context.Background(), runConfig{repoRoot: repo, rootID: rootID, maxTasks: 1}, taskManager, fakeAgent, fakeVCS)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}

	task, err := taskManager.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("get task failed: %v", err)
	}
	if task.Status != contracts.TaskStatusClosed {
		t.Fatalf("expected task to be closed, got %s", task.Status)
	}
	if len(fakeAgent.requests) == 0 {
		t.Fatalf("expected runner to be invoked")
	}
	if fakeAgent.requests[0].RepoRoot == repo {
		t.Fatalf("expected runner repo root to use isolated clone path, got %q", fakeAgent.requests[0].RepoRoot)
	}
}

func TestE2E_StreamSmoke_ConcurrencyEmitsMultiWorkerParseableEvents(t *testing.T) {
	if _, err := exec.LookPath("tk"); err != nil {
		t.Skip("tk CLI is required for e2e test")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git CLI is required for e2e test")
	}

	repo := t.TempDir()
	runCommand(t, repo, "git", "init")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write seed file: %v", err)
	}
	runCommand(t, repo, "git", "add", "README.md")
	runCommand(t, repo, "git", "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "init")

	runner := localRunner{dir: repo}
	rootID := mustCreateTicket(t, runner, "Roadmap", "epic", "0", "")
	mustCreateTicket(t, runner, "Task 1", "task", "0", rootID)
	mustCreateTicket(t, runner, "Task 2", "task", "0", rootID)

	taskManager := tk.NewTaskManager(runner)
	fakeAgent := &parallelFakeAgentRunner{results: []contracts.RunnerResult{
		{Status: contracts.RunnerResultCompleted},
		{Status: contracts.RunnerResultCompleted},
		{Status: contracts.RunnerResultCompleted, ReviewReady: true},
		{Status: contracts.RunnerResultCompleted, ReviewReady: true},
	}, delay: 30 * time.Millisecond}
	fakeVCS := &fakeVCS{}

	originalStdout := os.Stdout
	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = writePipe
	defer func() {
		os.Stdout = originalStdout
	}()

	runErr := runWithComponents(context.Background(), runConfig{repoRoot: repo, rootID: rootID, maxTasks: 2, concurrency: 2, stream: true}, taskManager, fakeAgent, fakeVCS)
	if runErr != nil {
		t.Fatalf("run failed: %v", runErr)
	}
	if err := writePipe.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	raw, err := io.ReadAll(readPipe)
	if err != nil {
		t.Fatalf("read stream: %v", err)
	}
	if err := readPipe.Close(); err != nil {
		t.Fatalf("close reader: %v", err)
	}

	decoder := contracts.NewEventDecoder(bytes.NewReader(raw))
	model := monitor.NewModel(nil)
	workers := map[string]struct{}{}
	for {
		event, err := decoder.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("failed to decode NDJSON stream: %v", err)
		}
		model.Apply(event)
		if event.Type == contracts.EventTypeRunnerStarted && strings.TrimSpace(event.WorkerID) != "" {
			workers[event.WorkerID] = struct{}{}
		}
	}
	if len(workers) < 2 {
		t.Fatalf("expected at least 2 workers in stream, got %d from %q", len(workers), string(raw))
	}
	view := model.View()
	if !strings.Contains(view, "Workers:") {
		t.Fatalf("expected model view to render workers section, got %q", view)
	}
}

func TestE2E_CodexTKConcurrency2LandsViaMergeQueue(t *testing.T) {
	if _, err := exec.LookPath("tk"); err != nil {
		t.Skip("tk CLI is required for e2e test")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git CLI is required for e2e test")
	}

	repo := t.TempDir()
	runCommand(t, repo, "git", "init")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write seed file: %v", err)
	}
	runCommand(t, repo, "git", "add", "README.md")
	runCommand(t, repo, "git", "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "init")

	runner := localRunner{dir: repo}
	rootID := mustCreateTicket(t, runner, "Roadmap", "epic", "0", "")
	taskOneID := mustCreateTicket(t, runner, "Task 1", "task", "0", rootID)
	taskTwoID := mustCreateTicket(t, runner, "Task 2", "task", "1", rootID)

	taskManager := tk.NewTaskManager(runner)
	fakeCodex := codex.NewCLIRunnerAdapter(writeFakeCodexBinary(t), nil)
	fakeVCS := &fakeVCS{}

	originalStdout := os.Stdout
	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = writePipe
	defer func() {
		os.Stdout = originalStdout
	}()

	runErr := runWithComponents(context.Background(), runConfig{
		repoRoot:    repo,
		rootID:      rootID,
		backend:     backendCodex,
		model:       "openai/gpt-5.3-codex",
		maxTasks:    2,
		concurrency: 2,
		stream:      true,
	}, taskManager, fakeCodex, fakeVCS)
	if runErr != nil {
		t.Fatalf("run failed: %v", runErr)
	}
	if err := writePipe.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	raw, err := io.ReadAll(readPipe)
	if err != nil {
		t.Fatalf("read stream: %v", err)
	}
	if err := readPipe.Close(); err != nil {
		t.Fatalf("close reader: %v", err)
	}

	decoder := contracts.NewEventDecoder(bytes.NewReader(raw))
	mergeLandedCount := 0
	sawRunStarted := false
	sawCodexBackend := false
	sawConcurrencyTwo := false
	for {
		event, err := decoder.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("failed to decode NDJSON stream: %v", err)
		}
		if event.Type == contracts.EventTypeRunStarted {
			sawRunStarted = true
			if event.Metadata["backend"] == backendCodex {
				sawCodexBackend = true
			}
			if event.Metadata["concurrency"] == "2" {
				sawConcurrencyTwo = true
			}
		}
		if event.Type == contracts.EventTypeMergeLanded {
			mergeLandedCount++
		}
	}
	if !sawRunStarted {
		t.Fatalf("expected run_started event in stream, got %q", string(raw))
	}
	if !sawCodexBackend {
		t.Fatalf("expected run_started metadata backend=%q, got %q", backendCodex, string(raw))
	}
	if !sawConcurrencyTwo {
		t.Fatalf("expected run_started metadata concurrency=2, got %q", string(raw))
	}
	if mergeLandedCount < 1 {
		t.Fatalf("expected at least one merge_landed event, got %d from %q", mergeLandedCount, string(raw))
	}

	for _, taskID := range []string{taskOneID, taskTwoID} {
		task, err := taskManager.GetTask(context.Background(), taskID)
		if err != nil {
			t.Fatalf("get task failed for %s: %v", taskID, err)
		}
		if task.Status != contracts.TaskStatusClosed {
			t.Fatalf("expected task %s to be closed, got %s", taskID, task.Status)
		}
	}
}

func TestE2E_ClaudeConflictRetryPathFinalizesWithLandingOrBlockedTriage(t *testing.T) {
	if _, err := exec.LookPath("tk"); err != nil {
		t.Skip("tk CLI is required for e2e test")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git CLI is required for e2e test")
	}

	t.Run("lands after one retry", func(t *testing.T) {
		taskID, taskManager, vcs, events, raw := runClaudeConflictRetryScenario(t, []error{
			errors.New("merge conflict first"),
		})

		if vcs.mergeCalls != 2 {
			t.Fatalf("expected one retry with two merge attempts, got %d", vcs.mergeCalls)
		}

		sawRunStarted := false
		sawClaudeBackend := false
		mergeRetryCount := 0
		sawMergeLanded := false
		sawMergeBlocked := false
		for _, event := range events {
			if event.Type == contracts.EventTypeRunStarted {
				sawRunStarted = true
				if event.Metadata["backend"] == backendClaude {
					sawClaudeBackend = true
				}
			}
			if event.Type == contracts.EventTypeMergeRetry {
				mergeRetryCount++
			}
			if event.Type == contracts.EventTypeMergeLanded {
				sawMergeLanded = true
			}
			if event.Type == contracts.EventTypeMergeBlocked {
				sawMergeBlocked = true
			}
		}

		if !sawRunStarted {
			t.Fatalf("expected run_started event in stream, got %q", raw)
		}
		if !sawClaudeBackend {
			t.Fatalf("expected run_started metadata backend=%q, got %q", backendClaude, raw)
		}
		if mergeRetryCount != 1 {
			t.Fatalf("expected exactly one merge_retry event, got %d from %q", mergeRetryCount, raw)
		}
		if !sawMergeLanded {
			t.Fatalf("expected merge_landed event after retry, got %q", raw)
		}
		if sawMergeBlocked {
			t.Fatalf("did not expect merge_blocked event on landed scenario, got %q", raw)
		}

		task, err := taskManager.GetTask(context.Background(), taskID)
		if err != nil {
			t.Fatalf("get task failed: %v", err)
		}
		if task.Status != contracts.TaskStatusClosed {
			t.Fatalf("expected task to be closed, got %s", task.Status)
		}
	})

	t.Run("blocks with triage metadata after retry exhaustion", func(t *testing.T) {
		_, _, vcs, events, raw := runClaudeConflictRetryScenario(t, []error{
			errors.New("merge conflict first"),
			errors.New("merge conflict second"),
		})

		if vcs.mergeCalls != 2 {
			t.Fatalf("expected one retry with two merge attempts, got %d", vcs.mergeCalls)
		}

		sawRunStarted := false
		sawClaudeBackend := false
		mergeRetryCount := 0
		sawMergeLanded := false
		mergeBlockedReason := ""
		taskFinishedBlocked := false
		taskFinishedReason := ""
		taskDataBlocked := false
		taskDataReason := ""
		for _, event := range events {
			if event.Type == contracts.EventTypeRunStarted {
				sawRunStarted = true
				if event.Metadata["backend"] == backendClaude {
					sawClaudeBackend = true
				}
			}
			switch event.Type {
			case contracts.EventTypeMergeRetry:
				mergeRetryCount++
			case contracts.EventTypeMergeLanded:
				sawMergeLanded = true
			case contracts.EventTypeMergeBlocked:
				mergeBlockedReason = strings.TrimSpace(event.Metadata["triage_reason"])
			case contracts.EventTypeTaskFinished:
				if event.Message == string(contracts.TaskStatusBlocked) {
					taskFinishedBlocked = true
					taskFinishedReason = strings.TrimSpace(event.Metadata["triage_reason"])
				}
			case contracts.EventTypeTaskDataUpdated:
				if event.Metadata["triage_status"] == "blocked" {
					taskDataBlocked = true
					taskDataReason = strings.TrimSpace(event.Metadata["triage_reason"])
				}
			}
		}

		if !sawRunStarted {
			t.Fatalf("expected run_started event in stream, got %q", raw)
		}
		if !sawClaudeBackend {
			t.Fatalf("expected run_started metadata backend=%q, got %q", backendClaude, raw)
		}
		if mergeRetryCount != 1 {
			t.Fatalf("expected exactly one merge_retry event, got %d from %q", mergeRetryCount, raw)
		}
		if sawMergeLanded {
			t.Fatalf("did not expect merge_landed event when both attempts conflict, got %q", raw)
		}
		if mergeBlockedReason == "" {
			t.Fatalf("expected merge_blocked triage_reason metadata, got %q", raw)
		}
		if !strings.Contains(mergeBlockedReason, "merge conflict second") {
			t.Fatalf("expected merge_blocked triage_reason to include final conflict, got %q", mergeBlockedReason)
		}
		if !taskFinishedBlocked {
			t.Fatalf("expected task_finished blocked event, got %q", raw)
		}
		if taskFinishedReason == "" || !strings.Contains(taskFinishedReason, "merge conflict second") {
			t.Fatalf("expected task_finished triage_reason with final conflict, got %q", taskFinishedReason)
		}
		if !taskDataBlocked {
			t.Fatalf("expected task_data_updated with triage_status=blocked, got %q", raw)
		}
		if taskDataReason == "" || !strings.Contains(taskDataReason, "merge conflict second") {
			t.Fatalf("expected task_data_updated triage_reason with final conflict, got %q", taskDataReason)
		}
	})
}

func runClaudeConflictRetryScenario(t *testing.T, mergeErrs []error) (string, *tk.TaskManager, *fakeVCS, []contracts.Event, string) {
	t.Helper()

	repo := t.TempDir()
	runCommand(t, repo, "git", "init")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write seed file: %v", err)
	}
	runCommand(t, repo, "git", "add", "README.md")
	runCommand(t, repo, "git", "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "init")

	runner := localRunner{dir: repo}
	rootID := mustCreateTicket(t, runner, "Roadmap", "epic", "0", "")
	taskID := mustCreateTicket(t, runner, "Conflict retry task", "task", "0", rootID)

	taskManager := tk.NewTaskManager(runner)
	fakeClaude := claude.NewCLIRunnerAdapter(writeFakeClaudeBinary(t), nil)
	fakeVCS := &fakeVCS{mergeErrs: append([]error(nil), mergeErrs...)}

	originalStdout := os.Stdout
	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = writePipe
	defer func() {
		os.Stdout = originalStdout
	}()

	runErr := runWithComponents(context.Background(), runConfig{
		repoRoot:    repo,
		rootID:      rootID,
		backend:     backendClaude,
		model:       "claude-3-5-sonnet",
		maxTasks:    1,
		concurrency: 1,
		stream:      true,
	}, taskManager, fakeClaude, fakeVCS)
	if runErr != nil {
		t.Fatalf("run failed: %v", runErr)
	}
	if err := writePipe.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	raw, err := io.ReadAll(readPipe)
	if err != nil {
		t.Fatalf("read stream: %v", err)
	}
	if err := readPipe.Close(); err != nil {
		t.Fatalf("close reader: %v", err)
	}

	events := decodeNDJSONEvents(t, raw)
	return taskID, taskManager, fakeVCS, events, string(raw)
}

func decodeNDJSONEvents(t *testing.T, raw []byte) []contracts.Event {
	t.Helper()

	decoder := contracts.NewEventDecoder(bytes.NewReader(raw))
	events := []contracts.Event{}
	for {
		event, err := decoder.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("failed to decode NDJSON stream: %v", err)
		}
		events = append(events, event)
	}
	return events
}

type fakeAgentRunner struct {
	results  []contracts.RunnerResult
	index    int
	requests []contracts.RunnerRequest
}

type parallelFakeAgentRunner struct {
	mu      sync.Mutex
	results []contracts.RunnerResult
	index   int
	delay   time.Duration
}

func (f *parallelFakeAgentRunner) Run(_ context.Context, _ contracts.RunnerRequest) (contracts.RunnerResult, error) {
	time.Sleep(f.delay)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.index >= len(f.results) {
		return contracts.RunnerResult{Status: contracts.RunnerResultFailed, Reason: "missing result"}, nil
	}
	result := f.results[f.index]
	f.index++
	return result, nil
}

func (f *fakeAgentRunner) Run(_ context.Context, request contracts.RunnerRequest) (contracts.RunnerResult, error) {
	f.requests = append(f.requests, request)
	if f.index >= len(f.results) {
		return contracts.RunnerResult{Status: contracts.RunnerResultFailed, Reason: "missing result"}, nil
	}
	result := f.results[f.index]
	f.index++
	return result, nil
}

type fakeVCS struct {
	mergeErrs  []error
	mergeCalls int
}

func (f *fakeVCS) EnsureMain(context.Context) error { return nil }
func (f *fakeVCS) CreateTaskBranch(_ context.Context, taskID string) (string, error) {
	return "task/" + taskID, nil
}
func (f *fakeVCS) Checkout(context.Context, string) error            { return nil }
func (f *fakeVCS) CommitAll(context.Context, string) (string, error) { return "", nil }
func (f *fakeVCS) MergeToMain(context.Context, string) error {
	f.mergeCalls++
	if len(f.mergeErrs) > 0 {
		err := f.mergeErrs[0]
		f.mergeErrs = f.mergeErrs[1:]
		return err
	}
	return nil
}
func (f *fakeVCS) PushBranch(context.Context, string) error { return nil }
func (f *fakeVCS) PushMain(context.Context) error           { return nil }

func mustCreateTicket(t *testing.T, runner localRunner, title string, issueType string, priority string, parent string) string {
	t.Helper()
	args := []string{"tk", "create", title, "-t", issueType, "-p", priority}
	if parent != "" {
		args = append(args, "--parent", parent)
	}
	out, err := runner.Run(args...)
	if err != nil {
		t.Fatalf("create ticket failed: %v (%s)", err, out)
	}
	return strings.TrimSpace(out)
}

func runCommand(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v failed: %v (%s)", name, args, err, string(output))
	}
}

func writeFakeCodexBinary(t *testing.T) string {
	t.Helper()
	binaryPath := filepath.Join(t.TempDir(), "fake-codex")
	script := "#!/bin/sh\nprintf 'REVIEW_VERDICT: pass\\n'\n"
	if err := os.WriteFile(binaryPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake codex binary: %v", err)
	}
	return binaryPath
}

func writeFakeClaudeBinary(t *testing.T) string {
	t.Helper()
	binaryPath := filepath.Join(t.TempDir(), "fake-claude")
	script := "#!/bin/sh\nprintf 'REVIEW_VERDICT: pass\\n'\n"
	if err := os.WriteFile(binaryPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake claude binary: %v", err)
	}
	return binaryPath
}
