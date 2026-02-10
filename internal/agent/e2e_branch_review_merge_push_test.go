package agent

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/anomalyco/yolo-runner/internal/contracts"
	"github.com/anomalyco/yolo-runner/internal/tk"
)

func TestE2EBranchReviewMergePushWithSeededTKBacklog(t *testing.T) {
	if _, err := exec.LookPath("tk"); err != nil {
		t.Skip("tk CLI is required for e2e test")
	}

	repo := t.TempDir()
	r := tkCommandRunner{dir: repo}
	rootID := mustTKCreate(t, r, "Roadmap", "epic", "0", "")
	taskID := mustTKCreate(t, r, "Implement task", "task", "0", rootID)

	mgr := tk.NewTaskManager(r)
	runner := &fakeRunner{results: []contracts.RunnerResult{
		{Status: contracts.RunnerResultCompleted},
		{Status: contracts.RunnerResultCompleted, ReviewReady: true},
	}}
	vcs := &fakeVCS{}
	loop := NewLoop(mgr, runner, nil, LoopOptions{
		ParentID:       rootID,
		MaxTasks:       1,
		RepoRoot:       repo,
		VCS:            vcs,
		RequireReview:  true,
		MergeOnSuccess: true,
	})

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if summary.Completed != 1 {
		t.Fatalf("expected one completed task, got %#v", summary)
	}

	if len(runner.modes) != 2 || runner.modes[0] != contracts.RunnerModeImplement || runner.modes[1] != contracts.RunnerModeReview {
		t.Fatalf("expected implement+review run sequence, got %#v", runner.modes)
	}

	if !containsCall(vcs.calls, "create_branch:"+taskID) {
		t.Fatalf("expected per-task branch creation, got %v", vcs.calls)
	}
	if !containsCall(vcs.calls, "merge_to_main:task/"+taskID) {
		t.Fatalf("expected merge-to-main call, got %v", vcs.calls)
	}
	if !containsCall(vcs.calls, "push_main") {
		t.Fatalf("expected push-main call, got %v", vcs.calls)
	}

	task, err := mgr.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("get task failed: %v", err)
	}
	if task.Status != contracts.TaskStatusClosed {
		t.Fatalf("expected task closed after successful flow, got %s", task.Status)
	}
}

type tkCommandRunner struct{ dir string }

func (r tkCommandRunner) Run(args ...string) (string, error) {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = r.dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func mustTKCreate(t *testing.T, runner tkCommandRunner, title string, issueType string, priority string, parent string) string {
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
