package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anomalyco/yolo-runner/internal/contracts"
	"github.com/anomalyco/yolo-runner/internal/tk"
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

type fakeAgentRunner struct {
	results  []contracts.RunnerResult
	index    int
	requests []contracts.RunnerRequest
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

type fakeVCS struct{}

func (fakeVCS) EnsureMain(context.Context) error { return nil }
func (fakeVCS) CreateTaskBranch(_ context.Context, taskID string) (string, error) {
	return "task/" + taskID, nil
}
func (fakeVCS) Checkout(context.Context, string) error            { return nil }
func (fakeVCS) CommitAll(context.Context, string) (string, error) { return "", nil }
func (fakeVCS) MergeToMain(context.Context, string) error         { return nil }
func (fakeVCS) PushBranch(context.Context, string) error          { return nil }
func (fakeVCS) PushMain(context.Context) error                    { return nil }

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
