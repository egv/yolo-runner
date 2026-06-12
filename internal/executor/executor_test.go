package executor

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/workitem"
)

func TestExecutorExecuteRunsImplementPipeline(t *testing.T) {
	t.Run("completed", func(t *testing.T) {
		runner := &executorFakeRunner{results: []contracts.RunnerResult{
			{Status: contracts.RunnerResultCompleted, Artifacts: map[string]string{"commit_sha": "impl-sha"}},
		}}
		exec := &Executor{
			Runner:   runner,
			RepoRoot: t.TempDir(),
			ParentID: "root",
			Backend:  "codex",
			Model:    "gpt-test",
		}

		result, err := exec.Execute(context.Background(), executorPayload("t-1"))
		if err != nil {
			t.Fatalf("Execute failed: %v", err)
		}
		if result.Status != string(contracts.RunnerResultCompleted) {
			t.Fatalf("expected completed result, got %#v", result)
		}
		if len(runner.requests) != 1 {
			t.Fatalf("expected one runner request, got %d", len(runner.requests))
		}
		if runner.requests[0].Mode != contracts.RunnerModeImplement {
			t.Fatalf("expected implement request, got %s", runner.requests[0].Mode)
		}
		if !strings.Contains(runner.requests[0].Prompt, "Task ID: t-1") {
			t.Fatalf("expected task prompt, got %q", runner.requests[0].Prompt)
		}
		if got := result.Artifacts["commit_sha"]; got != "impl-sha" {
			t.Fatalf("expected artifacts to include commit sha, got %q", got)
		}
	})

	t.Run("review retry then pass", func(t *testing.T) {
		runner := &executorFakeRunner{results: []contracts.RunnerResult{
			{Status: contracts.RunnerResultCompleted},
			{Status: contracts.RunnerResultCompleted, Artifacts: map[string]string{"review_verdict": "fail", "review_fail_feedback": "missing acceptance test"}},
			{Status: contracts.RunnerResultCompleted},
			{Status: contracts.RunnerResultCompleted, ReviewReady: true, Artifacts: map[string]string{"review_verdict": "pass"}},
		}}
		mgr := newLandingFakeTaskManager(contracts.Task{ID: "t-2", Title: "Task t-2", ParentID: "root"})
		exec := &Executor{
			Tasks:         mgr,
			Runner:        runner,
			RepoRoot:      t.TempDir(),
			ParentID:      "root",
			Backend:       "codex",
			Model:         "gpt-test",
			RequireReview: true,
			MaxRetries:    1,
		}

		result, err := exec.Execute(context.Background(), executorPayload("t-2"))
		if err != nil {
			t.Fatalf("Execute failed: %v", err)
		}
		if result.Status != string(contracts.RunnerResultCompleted) {
			t.Fatalf("expected completed result, got %#v", result)
		}
		if result.ReviewVerdict != "pass" {
			t.Fatalf("expected passing review verdict, got %q", result.ReviewVerdict)
		}
		if len(runner.requests) != 4 {
			t.Fatalf("expected implement/review retry sequence, got %d requests", len(runner.requests))
		}
		wantModes := []contracts.RunnerMode{
			contracts.RunnerModeImplement,
			contracts.RunnerModeReview,
			contracts.RunnerModeImplement,
			contracts.RunnerModeReview,
		}
		for i, want := range wantModes {
			if runner.requests[i].Mode != want {
				t.Fatalf("request %d mode = %s, want %s", i, runner.requests[i].Mode, want)
			}
		}
		if !strings.Contains(runner.requests[2].Prompt, "missing acceptance test") {
			t.Fatalf("expected retry implement prompt to include review feedback, got %q", runner.requests[2].Prompt)
		}
		if got := mgr.dataByID["t-2"]["review_retry_count"]; got != "1" {
			t.Fatalf("expected review retry metadata to be persisted, got %q", got)
		}
	})

	t.Run("landing conflict", func(t *testing.T) {
		runner := &executorFakeRunner{results: []contracts.RunnerResult{
			{Status: contracts.RunnerResultCompleted},
			{Status: contracts.RunnerResultCompleted, ReviewReady: true, Artifacts: map[string]string{"review_verdict": "pass"}},
			{Status: contracts.RunnerResultCompleted},
		}}
		vcs := &landingFakeVCS{mergeErrs: []error{
			errors.New("git merge --no-ff task/t-3 failed: CONFLICT (content): Merge conflict"),
			nil,
		}}
		lock := &landingRecordingLock{}
		exec := &Executor{
			Tasks:          newLandingFakeTaskManager(contracts.Task{ID: "t-3", Title: "Task t-3", ParentID: "root"}),
			Runner:         runner,
			RepoRoot:       t.TempDir(),
			ParentID:       "root",
			Backend:        "codex",
			Model:          "gpt-test",
			RequireReview:  true,
			MergeOnSuccess: true,
			VCSFactory: func(string) contracts.VCS {
				return vcs
			},
			LandingLock: lock,
		}

		result, err := exec.Execute(context.Background(), executorPayload("t-3"))
		if err != nil {
			t.Fatalf("Execute failed: %v", err)
		}
		if result.Status != string(contracts.RunnerResultCompleted) {
			t.Fatalf("expected completed result after landing retry, got %#v", result)
		}
		if result.Branch != "task/t-3" {
			t.Fatalf("expected task branch in result, got %q", result.Branch)
		}
		if vcs.mergeCalls != 2 {
			t.Fatalf("expected landing merge retry, got %d merge calls", vcs.mergeCalls)
		}
		if len(runner.requests) != 3 {
			t.Fatalf("expected implement/review/remediation requests, got %d", len(runner.requests))
		}
		if !strings.Contains(runner.requests[2].Prompt, "Landing Merge Remediation:") {
			t.Fatalf("expected landing remediation prompt, got %q", runner.requests[2].Prompt)
		}
		if lock.lockCalls != 1 || lock.unlockCalls != 1 {
			t.Fatalf("expected landing lock/unlock once, got lock=%d unlock=%d", lock.lockCalls, lock.unlockCalls)
		}
	})
}

func executorPayload(taskID string) workitem.ImplementPayload {
	return workitem.ImplementPayload{
		TaskID:      taskID,
		Title:       "Task " + taskID,
		Description: "Implement " + taskID + ".",
		PromptContext: workitem.ImplementPromptContext{
			ParentID: "root",
			Metadata: map[string]string{"queue": "test"},
		},
		BaseBranch: "main",
	}
}

type executorFakeRunner struct {
	results  []contracts.RunnerResult
	requests []contracts.RunnerRequest
}

func (r *executorFakeRunner) Run(_ context.Context, request contracts.RunnerRequest) (contracts.RunnerResult, error) {
	r.requests = append(r.requests, request)
	if len(r.results) == 0 {
		return contracts.RunnerResult{Status: contracts.RunnerResultCompleted}, nil
	}
	result := r.results[0]
	r.results = r.results[1:]
	return result, nil
}
