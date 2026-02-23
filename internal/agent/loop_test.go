package agent

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egv/yolo-runner/internal/contracts"
	enginepkg "github.com/egv/yolo-runner/internal/engine"
	"github.com/egv/yolo-runner/internal/logging"
	"github.com/egv/yolo-runner/internal/ui/tui"
)

func TestLoopCompletesTask(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{{Status: contracts.RunnerResultCompleted}}}
	loop := NewLoop(mgr, run, nil, LoopOptions{ParentID: "root", MaxRetries: 1})

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if summary.Completed != 1 || summary.TotalProcessed() != 1 {
		t.Fatalf("unexpected summary: %#v", summary)
	}
	if mgr.statusByID["t-1"] != contracts.TaskStatusClosed {
		t.Fatalf("expected task closed, got %s", mgr.statusByID["t-1"])
	}
}

func TestBuildPromptReviewRequiresStructuredVerdict(t *testing.T) {
	prompt := buildPrompt(contracts.Task{ID: "t-1", Title: "Task 1", Description: "Check behavior"}, contracts.RunnerModeReview, false)
	if !strings.Contains(prompt, "Mode: Review") {
		t.Fatalf("expected review mode marker in prompt, got %q", prompt)
	}
	if !strings.Contains(prompt, "REVIEW_VERDICT: pass") || !strings.Contains(prompt, "REVIEW_VERDICT: fail") {
		t.Fatalf("expected structured review verdict instructions, got %q", prompt)
	}
	if !strings.Contains(prompt, "REVIEW_FAIL_FEEDBACK:") {
		t.Fatalf("expected structured review fail feedback instructions, got %q", prompt)
	}
}

func TestBuildPromptImplementExcludesReviewVerdictInstructions(t *testing.T) {
	prompt := buildPrompt(contracts.Task{ID: "t-1", Title: "Task 1", Description: "Implement behavior"}, contracts.RunnerModeImplement, false)
	if !strings.Contains(prompt, "Mode: Implementation") {
		t.Fatalf("expected implementation mode marker in prompt, got %q", prompt)
	}
	if strings.Contains(prompt, "REVIEW_VERDICT") {
		t.Fatalf("did not expect review verdict instructions in implement prompt, got %q", prompt)
	}
}

func TestBuildPromptImplementIncludesCommandContractAndTDDChecklist(t *testing.T) {
	prompt := buildPrompt(contracts.Task{ID: "t-1", Title: "Task 1", Description: "Implement behavior"}, contracts.RunnerModeImplement, false)

	required := []string{
		"Command Contract:",
		"- Work only on this task; do not switch tasks.",
		"- Do not call task-selection/status tools (the runner owns task state).",
		"- Keep edits scoped to files required for this task.",
		"Strict TDD Checklist:",
		"[ ] Add or update a test that fails for the target behavior.",
		"[ ] Run the targeted test and confirm it fails before implementation.",
		"[ ] Implement the minimal code change required for the test to pass.",
		"[ ] Re-run targeted tests, then run broader relevant tests.",
		"[ ] Stop only when all tests pass and acceptance criteria are covered.",
	}
	for _, needle := range required {
		if !strings.Contains(prompt, needle) {
			t.Fatalf("expected prompt to include %q, got %q", needle, prompt)
		}
	}
}

func TestBuildPromptImplementIncludesRedGreenRefactorWorkflowWhenTDDModeEnabled(t *testing.T) {
	prompt := buildPrompt(contracts.Task{ID: "t-1", Title: "Task 1", Description: "Implement behavior"}, contracts.RunnerModeImplement, true)

	required := []string{
		"Strict TDD Workflow (Red-Green-Refactor):",
		"Tests-First Gate:",
		"- Confirm tests for the target behavior exist before implementation.",
		"- Run tests before changes and confirm they fail to define expected behavior.",
		"- Do not implement until tests-first gate is passing.",
		"1. RED: Add or update a test that fails for the target behavior.",
		"2. GREEN: Implement the minimal code required for that test to pass.",
		"3. REFACTOR: Improve the design while preserving passing tests.",
		"- Required sequence: test-first, targeted fail check, minimal green fix, then refactor.",
		"- Re-run targeted tests, then run broader relevant tests.",
		"- Stop only when all tests pass and acceptance criteria are covered.",
	}
	for _, needle := range required {
		if !strings.Contains(prompt, needle) {
			t.Fatalf("expected prompt to include %q, got %q", needle, prompt)
		}
	}
}

func TestLoopBlocksTDDTaskWhenNoTestsArePresent(t *testing.T) {
	repoRoot := t.TempDir()
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{{Status: contracts.RunnerResultCompleted}}}
	loop := NewLoop(mgr, run, nil, LoopOptions{ParentID: "root", TDDMode: true, RepoRoot: repoRoot})

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if summary.Blocked != 1 {
		t.Fatalf("expected blocked summary, got %#v", summary)
	}
	if mgr.statusByID["t-1"] != contracts.TaskStatusBlocked {
		t.Fatalf("expected blocked task status, got %s", mgr.statusByID["t-1"])
	}
	if len(run.requests) != 0 {
		t.Fatalf("expected no runner requests when blocked by tdd tests-first gate, got %d", len(run.requests))
	}
	if got := mgr.dataByID["t-1"]["triage_reason"]; !strings.Contains(got, "tests-first") {
		t.Fatalf("expected triage_reason to mention tests-first gate, got %q", got)
	}
}

func TestLoopAllowsTDDTaskWhenTestsAreFailing(t *testing.T) {
	repoRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoRoot, "go.mod"), []byte("module tdd-test\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("failed to write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "feature_test.go"), []byte("package main\n\nimport \"testing\"\n\nfunc TestFeature(t *testing.T) {\n\tt.Fatalf(\"intentional failing test\")\n}\n"), 0o644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{{Status: contracts.RunnerResultCompleted}}}
	loop := NewLoop(mgr, run, nil, LoopOptions{ParentID: "root", TDDMode: true, RepoRoot: repoRoot})

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if summary.Completed != 1 {
		t.Fatalf("expected completed summary, got %#v", summary)
	}
	if mgr.statusByID["t-1"] != contracts.TaskStatusClosed {
		t.Fatalf("expected closed status after completion, got %s", mgr.statusByID["t-1"])
	}
	if len(run.requests) != 1 {
		t.Fatalf("expected one runner request, got %d", len(run.requests))
	}
	if got := run.requests[0].Mode; got != contracts.RunnerModeImplement {
		t.Fatalf("expected implement mode request, got %s", got)
	}
}

func TestLoopBlocksTDDTaskWhenTestsArePresentButPassing(t *testing.T) {
	repoRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoRoot, "go.mod"), []byte("module tdd-test\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("failed to write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "feature.go"), []byte("package main\n\nfunc Feature() bool {\n\treturn true\n}\n"), 0o644); err != nil {
		t.Fatalf("failed to write feature file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "feature_test.go"), []byte("package main\n\nimport \"testing\"\n\nfunc TestFeature(t *testing.T) {\n\tif !Feature() {\n\t\tt.Fatalf(\"feature should pass\")\n\t}\n}\n"), 0o644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{{Status: contracts.RunnerResultCompleted}}}
	loop := NewLoop(mgr, run, nil, LoopOptions{ParentID: "root", TDDMode: true, RepoRoot: repoRoot})

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if summary.Blocked != 1 {
		t.Fatalf("expected blocked summary, got %#v", summary)
	}
	if got := mgr.statusByID["t-1"]; got != contracts.TaskStatusBlocked {
		t.Fatalf("expected blocked task status, got %s", got)
	}
	if len(run.requests) != 0 {
		t.Fatalf("expected no runner requests when blocked by tdd tests-first gate, got %d", len(run.requests))
	}
	if got := mgr.dataByID["t-1"]["triage_reason"]; !strings.Contains(got, "tests-first") {
		t.Fatalf("expected triage_reason to mention tests-first gate, got %q", got)
	}
	if got := mgr.dataByID["t-1"]["tests_present"]; got != "true" {
		t.Fatalf("expected tests_present=true, got %q", got)
	}
	if got := mgr.dataByID["t-1"]["tests_failing"]; got != "false" {
		t.Fatalf("expected tests_failing=false, got %q", got)
	}
}

func TestBuildImplementPromptIncludesReviewFeedbackWhenRetrying(t *testing.T) {
	prompt := buildImplementPrompt(
		contracts.Task{ID: "t-1", Title: "Task 1", Description: "Implement behavior"},
		"add RED/GREEN note evidence to ticket",
		1,
		false,
	)

	if !strings.Contains(prompt, "Review Remediation Loop: Attempt 1") {
		t.Fatalf("expected remediation loop attempt marker, got %q", prompt)
	}
	if !strings.Contains(prompt, "REVIEW_FAIL_FEEDBACK:") {
		t.Fatalf("expected structured review feedback marker, got %q", prompt)
	}
	if !strings.Contains(prompt, "add RED/GREEN note evidence to ticket") {
		t.Fatalf("expected review feedback body in prompt, got %q", prompt)
	}
}

func TestLoopRetriesReviewFailThenCompletes(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{
		{Status: contracts.RunnerResultCompleted},
		{
			Status:      contracts.RunnerResultCompleted,
			ReviewReady: false,
			Artifacts: map[string]string{
				"review_verdict":       "fail",
				"review_fail_feedback": "missing regression test",
			},
		},
		{Status: contracts.RunnerResultCompleted},
		{Status: contracts.RunnerResultCompleted, ReviewReady: true},
	}}
	loop := NewLoop(mgr, run, nil, LoopOptions{ParentID: "root", MaxRetries: 2, RequireReview: true})

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if summary.Completed != 1 {
		t.Fatalf("expected completion after retry, got %#v", summary)
	}
	if got := mgr.dataByID["t-1"]["review_retry_count"]; got != "1" {
		t.Fatalf("expected review_retry_count=1, got %q", got)
	}
	if len(run.modes) != 4 {
		t.Fatalf("expected implement+review to rerun after review fail, got modes=%#v", run.modes)
	}
	if run.modes[0] != contracts.RunnerModeImplement ||
		run.modes[1] != contracts.RunnerModeReview ||
		run.modes[2] != contracts.RunnerModeImplement ||
		run.modes[3] != contracts.RunnerModeReview {
		t.Fatalf("unexpected runner mode sequence: %#v", run.modes)
	}
}

func TestLoopInvokesReviewRunnerWhenReviewModeEnabled(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{
		{Status: contracts.RunnerResultCompleted},
		{Status: contracts.RunnerResultCompleted, ReviewReady: true},
	}}
	loop := NewLoop(mgr, run, nil, LoopOptions{ParentID: "root", MaxRetries: 1, RequireReview: true, Backend: "codex", Model: "openai/gpt-5.3-codex"})

	_, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if len(run.requests) != 2 {
		t.Fatalf("expected implement+review request count, got %d", len(run.requests))
	}
	if run.requests[0].Mode != contracts.RunnerModeImplement {
		t.Fatalf("expected first request mode=implement, got %s", run.requests[0].Mode)
	}
	if run.requests[1].Mode != contracts.RunnerModeReview {
		t.Fatalf("expected second request mode=review, got %s", run.requests[1].Mode)
	}
	if run.requests[1].Model != "openai/gpt-5.3-codex" {
		t.Fatalf("expected review request to use configured model, got %q", run.requests[1].Model)
	}
}

func TestLoopPassesConfiguredModelToReviewRunnerRequest(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-2", Title: "Task 2", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{
		{Status: contracts.RunnerResultCompleted},
		{Status: contracts.RunnerResultCompleted, ReviewReady: true},
	}}
	loop := NewLoop(mgr, run, nil, LoopOptions{ParentID: "root", MaxRetries: 1, RequireReview: true, Model: "kimi-k2", Backend: "kimi"})

	_, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if len(run.requests) != 2 {
		t.Fatalf("expected two runner requests, got %d", len(run.requests))
	}
	if run.requests[0].Model != "kimi-k2" {
		t.Fatalf("expected implement request model to be propagated, got %q", run.requests[0].Model)
	}
	if run.requests[1].Model != "kimi-k2" {
		t.Fatalf("expected review request model to be propagated, got %q", run.requests[1].Model)
	}
}

func TestLoopAppliesPerTaskRuntimeOverrides(t *testing.T) {
	taskDescription := `---
backend: codex
model: task-model
skillset: docs
tools:
  - shell
  - git
timeout: 45s
mode: review
---
Implement task behavior.`
	mgr := newFakeTaskManager(contracts.Task{
		ID:          "t-1",
		Title:       "Task 1",
		Description: taskDescription,
		Status:      contracts.TaskStatusOpen,
	})
	run := &fakeRunner{results: []contracts.RunnerResult{{Status: contracts.RunnerResultCompleted}}}
	sink := &recordingSink{}
	repoRoot := t.TempDir()
	loop := NewLoop(mgr, run, sink, LoopOptions{
		ParentID:      "root",
		Backend:       "opencode",
		Model:         "global-model",
		RunnerTimeout: 2 * time.Minute,
		RepoRoot:      repoRoot,
	})

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if summary.Completed != 1 {
		t.Fatalf("expected completed summary, got %#v", summary)
	}
	if run.requests[0].Model != "task-model" {
		t.Fatalf("expected task model override, got %q", run.requests[0].Model)
	}
	if run.requests[0].Timeout != 45*time.Second {
		t.Fatalf("expected task timeout override of 45s, got %v", run.requests[0].Timeout)
	}
	if run.requests[0].Metadata["runtime_model"] != "task-model" {
		t.Fatalf("expected runtime_model metadata, got %#v", run.requests[0].Metadata)
	}
	if run.requests[0].Metadata["runtime_backend"] != "codex" {
		t.Fatalf("expected runtime_backend metadata, got %#v", run.requests[0].Metadata)
	}
	if run.requests[0].Metadata["runtime_skillset"] != "docs" {
		t.Fatalf("expected runtime_skillset metadata, got %#v", run.requests[0].Metadata)
	}
	if run.requests[0].Metadata["runtime_tools"] != "shell,git" {
		t.Fatalf("expected runtime_tools metadata, got %#v", run.requests[0].Metadata)
	}
	if run.requests[0].Metadata["runtime_timeout"] != (45 * time.Second).String() {
		t.Fatalf("expected runtime_timeout metadata, got %#v", run.requests[0].Metadata)
	}
	if run.requests[0].Metadata["task_mode"] != "review" {
		t.Fatalf("expected task_mode metadata from overrides, got %#v", run.requests[0].Metadata)
	}
	if got := run.requests[0].Metadata["runtime_config"]; got != "true" {
		t.Fatalf("expected runtime_config metadata flag, got %q", got)
	}

	started := eventsByType(sink.events, contracts.EventTypeRunnerStarted)
	if len(started) != 1 {
		t.Fatalf("expected one runner_started event, got %d", len(started))
	}
	if started[0].Metadata["backend"] != "codex" {
		t.Fatalf("expected runner_started backend override, got %#v", started[0].Metadata)
	}
	if started[0].Metadata["model"] != "task-model" {
		t.Fatalf("expected runner_started model override, got %#v", started[0].Metadata)
	}
	if started[0].Metadata["timeout"] != (45 * time.Second).String() {
		t.Fatalf("expected runner_started timeout metadata, got %#v", started[0].Metadata)
	}
}

func TestLoopRunsComplexDependencyTreeWithPerTaskOverridesAndParallelism(t *testing.T) {
	taskDescription := func(taskModel string, tools ...string) string {
		lines := []string{
			"---",
			"model: " + taskModel,
		}
		if len(tools) > 0 {
			lines = append(lines, "tools:")
			for _, tool := range tools {
				lines = append(lines, "  - "+tool)
			}
		}
		lines = append(lines, "---", "Task body")
		return strings.Join(lines, "\n")
	}

	mgr := newFakeTaskManager(
		contracts.Task{
			ID:          "task-a",
			Title:       "Task A",
			Description: taskDescription("task-a-model", "shell"),
			Status:      contracts.TaskStatusOpen,
		},
		contracts.Task{
			ID:          "task-b",
			Title:       "Task B",
			Description: taskDescription("task-b-model", "git"),
			Status:      contracts.TaskStatusOpen,
		},
		contracts.Task{
			ID:     "task-c",
			Title:  "Task C",
			Status: contracts.TaskStatusOpen,
		},
		contracts.Task{
			ID:          "task-d",
			Title:       "Task D",
			Description: taskDescription("task-d-model", "docs"),
			Status:      contracts.TaskStatusOpen,
		},
		contracts.Task{
			ID:          "task-e",
			Title:       "Task E",
			Description: taskDescription("task-e-model", "sql", "shell"),
			Status:      contracts.TaskStatusOpen,
		},
		contracts.Task{
			ID:          "task-f",
			Title:       "Task F",
			Description: taskDescription("task-f-model", "docker"),
			Status:      contracts.TaskStatusOpen,
		},
	)
	mgr.dependsOn = map[string][]string{
		"task-c": {"task-a"},
		"task-d": {"task-a", "task-b"},
		"task-e": {"task-c"},
		"task-f": {"task-e", "task-d"},
	}

	started := make(chan string, 6)
	runner := &dependencyTrackingRunner{
		started: started,
		release: make(chan struct{}),
	}
	loop := NewLoop(mgr, runner, nil, LoopOptions{
		ParentID:    "root",
		Concurrency: 2,
		Model:       "global-model",
	})

	type loopResult struct {
		summary contracts.LoopSummary
		err     error
	}
	resultCh := make(chan loopResult, 1)
	go func() {
		summary, err := loop.Run(context.Background())
		resultCh <- loopResult{summary: summary, err: err}
	}()

	startOrder := make([]string, 0, 6)
	startOrder = append(startOrder, <-started, <-started)
	if startOrder[0] == startOrder[1] {
		t.Fatalf("expected two different tasks to start first, got %q", startOrder)
	}
	if (startOrder[0] != "task-a" && startOrder[0] != "task-b") ||
		(startOrder[1] != "task-a" && startOrder[1] != "task-b") {
		t.Fatalf("expected first wave to be task-a and task-b, got %q", startOrder)
	}
	close(runner.release)

	for len(startOrder) < 6 {
		startOrder = append(startOrder, <-started)
	}
	result := <-resultCh
	if result.err != nil {
		t.Fatalf("loop failed: %v", result.err)
	}
	if result.summary.Completed != 6 {
		t.Fatalf("expected six completed tasks, got %#v", result.summary)
	}
	if runner.maxActive < 2 {
		t.Fatalf("expected parallel execution with maxActive>=2, got %d", runner.maxActive)
	}

	if startOrder[0] != "task-a" && startOrder[0] != "task-b" {
		t.Fatalf("expected first wave to begin with task-a/task-b, got %q", startOrder[0])
	}
	if startOrder[1] != "task-a" && startOrder[1] != "task-b" {
		t.Fatalf("expected first wave to begin with task-a/task-b, got %q", startOrder[1])
	}
	if startOrder[0] == startOrder[1] {
		t.Fatalf("expected two different tasks in first wave, got %q", startOrder[:2])
	}
	startPos := map[string]int{}
	for pos, taskID := range startOrder {
		startPos[taskID] = pos
	}
	_, hasTaskA := startPos["task-a"]
	_, hasTaskB := startPos["task-b"]
	if !hasTaskA || !hasTaskB {
		t.Fatalf("expected first wave task-a and task-b to start, got %q", startOrder)
	}
	for _, dependency := range []struct {
		before string
		after  string
	}{
		{before: "task-a", after: "task-c"},
		{before: "task-a", after: "task-d"},
		{before: "task-b", after: "task-d"},
		{before: "task-c", after: "task-e"},
		{before: "task-d", after: "task-f"},
		{before: "task-e", after: "task-f"},
	} {
		beforePos, okBefore := startPos[dependency.before]
		afterPos, okAfter := startPos[dependency.after]
		if !okBefore || !okAfter {
			t.Fatalf("expected both %s and %s in start order, got %q", dependency.before, dependency.after, startOrder)
		}
		if beforePos >= afterPos {
			t.Fatalf("expected task %s to start before %s, got positions %d and %d", dependency.before, dependency.after, beforePos, afterPos)
		}
	}

	requestByTask := map[string]contracts.RunnerRequest{}
	for _, request := range runner.requests {
		requestByTask[request.TaskID] = request
	}

	expectedModels := map[string]string{
		"task-a": "task-a-model",
		"task-b": "task-b-model",
		"task-c": "global-model",
		"task-d": "task-d-model",
		"task-e": "task-e-model",
		"task-f": "task-f-model",
	}
	expectedRuntimeTools := map[string]string{
		"task-a": "shell",
		"task-b": "git",
		"task-c": "",
		"task-d": "docs",
		"task-e": "sql,shell",
		"task-f": "docker",
	}

	for taskID, expectedModel := range expectedModels {
		request, ok := requestByTask[taskID]
		if !ok {
			t.Fatalf("expected request for task %s", taskID)
		}
		if request.Model != expectedModel {
			t.Fatalf("expected task %s to use model %q, got %q", taskID, expectedModel, request.Model)
		}

		if expectedRuntimeTools[taskID] == "" {
			if runtimeTools, ok := request.Metadata["runtime_tools"]; ok && runtimeTools != "" {
				t.Fatalf("expected task %s to use no per-task tools override, got %q", taskID, runtimeTools)
			}
			if runtimeConfig, ok := request.Metadata["runtime_config"]; ok && runtimeConfig == "true" {
				t.Fatalf("expected task %s runtime_config=false", taskID)
			}
			continue
		}
		if request.Metadata["runtime_tools"] != expectedRuntimeTools[taskID] {
			t.Fatalf("expected task %s runtime tools %q, got %q", taskID, expectedRuntimeTools[taskID], request.Metadata["runtime_tools"])
		}
		if request.Metadata["runtime_model"] != expectedModel {
			t.Fatalf("expected task %s runtime model %q, got %q", taskID, expectedModel, request.Metadata["runtime_model"])
		}
		if request.Metadata["runtime_config"] != "true" {
			t.Fatalf("expected task %s runtime_config=true, got %q", taskID, request.Metadata["runtime_config"])
		}
	}
}

func TestLoopRetriesFailedImplementationWithFallbackModel(t *testing.T) {
	cases := []struct {
		name   string
		reason string
	}{
		{
			name:   "type failure",
			reason: "type failure: expected string but got number",
		},
		{
			name:   "tool failure",
			reason: "tool failure: shell command timed out",
		},
		{
			name:   "parse failure",
			reason: "invalid json response while parsing model output",
		},
		{
			name:   "provider error",
			reason: "provider error: 429 too many requests",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
			run := &fakeRunner{results: []contracts.RunnerResult{
				{Status: contracts.RunnerResultFailed, Reason: tc.reason},
				{Status: contracts.RunnerResultCompleted},
			}}
			loop := NewLoop(mgr, run, nil, LoopOptions{
				ParentID:      "root",
				Model:         "primary-model",
				FallbackModel: "fallback-model",
			})

			summary, err := loop.Run(context.Background())
			if err != nil {
				t.Fatalf("loop failed: %v", err)
			}
			if summary.Completed != 1 {
				t.Fatalf("expected task completed after fallback retry, got %#v", summary)
			}
			if len(run.requests) != 2 {
				t.Fatalf("expected fallback retry request sequence, got %d requests", len(run.requests))
			}
			if run.requests[0].Model != "primary-model" {
				t.Fatalf("expected first request to use primary model, got %q", run.requests[0].Model)
			}
			if run.requests[1].Model != "fallback-model" {
				t.Fatalf("expected fallback request to use fallback model, got %q", run.requests[1].Model)
			}
		})
	}
}

func TestLoopNoFallbackOnReviewFailureResult(t *testing.T) {
	cases := []struct {
		name   string
		reason string
	}{
		{
			name:   "review rejected",
			reason: "review rejected: missing regression test for retry/backoff flow",
		},
		{
			name:   "review verdict",
			reason: "review verdict returned fail: security concern",
		},
		{
			name:   "review feedback",
			reason: "review feedback suggests API change",
		},
		{
			name:   "acceptance criteria",
			reason: "failing acceptance criteria: behavior does not match",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
			run := &fakeRunner{results: []contracts.RunnerResult{
				{Status: contracts.RunnerResultFailed, Reason: tc.reason},
			}}
			loop := NewLoop(mgr, run, nil, LoopOptions{
				ParentID:      "root",
				Model:         "primary-model",
				FallbackModel: "fallback-model",
			})

			summary, err := loop.Run(context.Background())
			if err != nil {
				t.Fatalf("loop failed: %v", err)
			}
			if summary.Failed != 1 {
				t.Fatalf("expected failed summary, got %#v", summary)
			}
			if len(run.requests) != 1 {
				t.Fatalf("expected single implement attempt, got %d requests", len(run.requests))
			}
			if run.requests[0].Model != "primary-model" {
				t.Fatalf("expected primary model request, got %q", run.requests[0].Model)
			}
		})
	}
}

func TestLoopLogsModelFallbackDecision(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{
		{Status: contracts.RunnerResultFailed, Reason: "tool failure: external tool not available"},
		{Status: contracts.RunnerResultCompleted},
	}}
	sink := &recordingSink{}
	loop := NewLoop(mgr, run, sink, LoopOptions{
		ParentID:      "root",
		Model:         "primary-model",
		FallbackModel: "fallback-model",
	})

	_, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}

	runnerStarted := eventsByType(sink.events, contracts.EventTypeRunnerStarted)
	if len(runnerStarted) != 2 {
		t.Fatalf("expected two runner_started events for fallback attempt, got %d", len(runnerStarted))
	}
	fallbackStart := runnerStarted[1]
	if fallbackStart.Metadata["decision"] != "model_fallback" {
		t.Fatalf("expected model_fallback decision, got %#v", fallbackStart.Metadata)
	}
	if fallbackStart.Metadata["model_previous"] != "primary-model" {
		t.Fatalf("expected fallback to include previous model, got %#v", fallbackStart.Metadata)
	}
	if fallbackStart.Metadata["model"] != "fallback-model" {
		t.Fatalf("expected fallback runner_started model to be fallback-model, got %#v", fallbackStart.Metadata)
	}
}

func TestLoopEmitsReviewAttemptTelemetryOnPassAfterRetry(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{
		{Status: contracts.RunnerResultCompleted},
		{
			Status:      contracts.RunnerResultCompleted,
			ReviewReady: false,
			Artifacts: map[string]string{
				"review_verdict":       "fail",
				"review_fail_feedback": "missing regression test",
			},
		},
		{Status: contracts.RunnerResultCompleted},
		{Status: contracts.RunnerResultCompleted, ReviewReady: true},
	}}
	sink := &recordingSink{}
	loop := NewLoop(mgr, run, sink, LoopOptions{ParentID: "root", MaxRetries: 1, RequireReview: true})

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if summary.Completed != 1 {
		t.Fatalf("expected completion after retry, got %#v", summary)
	}

	started := eventsByType(sink.events, contracts.EventTypeReviewStarted)
	if len(started) != 2 {
		t.Fatalf("expected two review_started events, got %d", len(started))
	}
	if started[0].Metadata["review_attempt"] != "1" || started[0].Metadata["review_retry_count"] != "0" {
		t.Fatalf("expected first review_started telemetry, got %#v", started[0].Metadata)
	}
	if started[1].Metadata["review_attempt"] != "2" || started[1].Metadata["review_retry_count"] != "1" {
		t.Fatalf("expected second review_started telemetry, got %#v", started[1].Metadata)
	}

	finished := eventsByType(sink.events, contracts.EventTypeReviewFinished)
	if len(finished) != 2 {
		t.Fatalf("expected two review_finished events, got %d", len(finished))
	}
	if finished[0].Metadata["review_attempt"] != "1" || finished[0].Metadata["review_retry_count"] != "0" {
		t.Fatalf("expected first review_finished telemetry, got %#v", finished[0].Metadata)
	}
	if finished[1].Metadata["review_attempt"] != "2" || finished[1].Metadata["review_retry_count"] != "1" {
		t.Fatalf("expected second review_finished telemetry, got %#v", finished[1].Metadata)
	}
}

func TestLoopInjectsPriorReviewBlockersIntoRetryImplementPrompt(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{
		{Status: contracts.RunnerResultCompleted},
		{
			Status:      contracts.RunnerResultCompleted,
			ReviewReady: false,
			Artifacts: map[string]string{
				"review_verdict":       "fail",
				"review_fail_feedback": "missing regression test for retry/backoff flow",
			},
		},
		{Status: contracts.RunnerResultCompleted},
		{Status: contracts.RunnerResultCompleted, ReviewReady: true},
	}}
	loop := NewLoop(mgr, run, nil, LoopOptions{ParentID: "root", MaxRetries: 1, RequireReview: true})

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if summary.Completed != 1 {
		t.Fatalf("expected completed summary after retry, got %#v", summary)
	}
	if len(run.requests) != 4 {
		t.Fatalf("expected implement+review+implement+review requests, got %d", len(run.requests))
	}
	initialPrompt := run.requests[0].Prompt
	if strings.Contains(initialPrompt, "Prior Review Blockers:") {
		t.Fatalf("did not expect initial implementation prompt to include retry blockers, got %q", initialPrompt)
	}
	retryPrompt := run.requests[2].Prompt
	if !strings.Contains(retryPrompt, "Prior Review Blockers:") {
		t.Fatalf("expected retry implementation prompt to include prior blockers section, got %q", retryPrompt)
	}
	if !strings.Contains(retryPrompt, "missing regression test for retry/backoff flow") {
		t.Fatalf("expected retry implementation prompt to include prior blocker feedback, got %q", retryPrompt)
	}
}

func TestLoopMarksFailedAfterRetryExhausted(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{
		{Status: contracts.RunnerResultCompleted},
		{Status: contracts.RunnerResultFailed, Reason: "review rejected: first"},
		{Status: contracts.RunnerResultCompleted},
		{Status: contracts.RunnerResultFailed, Reason: "review rejected: second"},
	}}
	loop := NewLoop(mgr, run, nil, LoopOptions{ParentID: "root", MaxRetries: 1, RequireReview: true})

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if summary.Failed != 1 {
		t.Fatalf("expected failed summary, got %#v", summary)
	}
	if mgr.statusByID["t-1"] != contracts.TaskStatusFailed {
		t.Fatalf("expected failed status, got %s", mgr.statusByID["t-1"])
	}
	if got := mgr.dataByID["t-1"]["triage_status"]; got != "failed" {
		t.Fatalf("expected triage_status=failed, got %q", got)
	}
	if got := mgr.dataByID["t-1"]["triage_reason"]; got != "review rejected: second" {
		t.Fatalf("expected triage_reason from final review failure, got %q", got)
	}
	if got := mgr.dataByID["t-1"]["review_retry_count"]; got != "1" {
		t.Fatalf("expected review_retry_count=1 after retry exhaustion, got %q", got)
	}
}

func TestLoopEmitsReviewAttemptTelemetryOnRetryExhaustionFailure(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{
		{Status: contracts.RunnerResultCompleted},
		{
			Status:      contracts.RunnerResultCompleted,
			ReviewReady: false,
			Artifacts: map[string]string{
				"review_verdict":       "fail",
				"review_fail_feedback": "first review blocker",
			},
		},
		{Status: contracts.RunnerResultCompleted},
		{
			Status:      contracts.RunnerResultCompleted,
			ReviewReady: false,
			Artifacts: map[string]string{
				"review_verdict":       "fail",
				"review_fail_feedback": "second review blocker",
			},
		},
	}}
	sink := &recordingSink{}
	loop := NewLoop(mgr, run, sink, LoopOptions{ParentID: "root", MaxRetries: 1, RequireReview: true})

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if summary.Failed != 1 {
		t.Fatalf("expected failure after retry exhaustion, got %#v", summary)
	}

	started := eventsByType(sink.events, contracts.EventTypeReviewStarted)
	if len(started) != 2 {
		t.Fatalf("expected two review_started events, got %d", len(started))
	}
	if started[0].Metadata["review_attempt"] != "1" || started[0].Metadata["review_retry_count"] != "0" {
		t.Fatalf("expected first review_started telemetry, got %#v", started[0].Metadata)
	}
	if started[1].Metadata["review_attempt"] != "2" || started[1].Metadata["review_retry_count"] != "1" {
		t.Fatalf("expected second review_started telemetry, got %#v", started[1].Metadata)
	}

	finished := eventsByType(sink.events, contracts.EventTypeReviewFinished)
	if len(finished) != 2 {
		t.Fatalf("expected two review_finished events, got %d", len(finished))
	}
	if finished[0].Metadata["review_attempt"] != "1" || finished[0].Metadata["review_retry_count"] != "0" {
		t.Fatalf("expected first review_finished telemetry, got %#v", finished[0].Metadata)
	}
	if finished[1].Metadata["review_attempt"] != "2" || finished[1].Metadata["review_retry_count"] != "1" {
		t.Fatalf("expected second review_finished telemetry, got %#v", finished[1].Metadata)
	}
}

func TestLoopUsesFinalUnresolvedBlockerSummaryAfterReviewRetryExhausted(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{
		{Status: contracts.RunnerResultCompleted},
		{
			Status:      contracts.RunnerResultCompleted,
			ReviewReady: false,
			Artifacts: map[string]string{
				"review_verdict":       "fail",
				"review_fail_feedback": "missing regression test for retry/backoff flow",
			},
		},
		{Status: contracts.RunnerResultCompleted},
		{
			Status:      contracts.RunnerResultCompleted,
			ReviewReady: false,
			Artifacts: map[string]string{
				"review_verdict": "fail",
			},
		},
	}}
	vcs := &fakeVCS{}
	loop := NewLoop(mgr, run, nil, LoopOptions{
		ParentID:       "root",
		MaxRetries:     1,
		RequireReview:  true,
		MergeOnSuccess: true,
		VCS:            vcs,
	})

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if summary.Failed != 1 {
		t.Fatalf("expected failed summary, got %#v", summary)
	}
	if mgr.statusByID["t-1"] != contracts.TaskStatusFailed {
		t.Fatalf("expected failed status, got %s", mgr.statusByID["t-1"])
	}
	if got := mgr.dataByID["t-1"]["triage_reason"]; got != "review rejected: missing regression test for retry/backoff flow" {
		t.Fatalf("expected final unresolved blocker summary in triage_reason, got %q", got)
	}
	if got := mgr.dataByID["t-1"]["review_retry_count"]; got != "1" {
		t.Fatalf("expected review_retry_count=1 after retry exhaustion, got %q", got)
	}
	if containsCall(vcs.calls, "merge_to_main:task/t-1") {
		t.Fatalf("did not expect merge_to_main call on terminal failure, got %v", vcs.calls)
	}
	if containsCall(vcs.calls, "push_main") {
		t.Fatalf("did not expect push_main call on terminal failure, got %v", vcs.calls)
	}
}

func TestLoopDoesNotRetryNonReviewFailureWhenRetryBudgetRemains(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{
		{Status: contracts.RunnerResultFailed, Reason: "lint failed"},
		{Status: contracts.RunnerResultCompleted},
	}}
	loop := NewLoop(mgr, run, nil, LoopOptions{ParentID: "root", MaxRetries: 2})

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if summary.Failed != 1 {
		t.Fatalf("expected non-review failure to fail immediately, got %#v", summary)
	}
	if len(run.modes) != 1 || run.modes[0] != contracts.RunnerModeImplement {
		t.Fatalf("expected exactly one implement run with no retry, got modes=%#v", run.modes)
	}
	if got := mgr.dataByID["t-1"]["review_retry_count"]; got != "" {
		t.Fatalf("expected no review_retry_count for non-review failure, got %q", got)
	}
}

func TestLoopMarksBlockedWithReason(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{{Status: contracts.RunnerResultBlocked, Reason: "needs manual input"}}}
	loop := NewLoop(mgr, run, nil, LoopOptions{ParentID: "root", MaxRetries: 0})

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if summary.Blocked != 1 {
		t.Fatalf("expected blocked summary, got %#v", summary)
	}
	if mgr.statusByID["t-1"] != contracts.TaskStatusBlocked {
		t.Fatalf("expected blocked status, got %s", mgr.statusByID["t-1"])
	}
	if got := mgr.dataByID["t-1"]["triage_status"]; got != "blocked" {
		t.Fatalf("expected triage_status=blocked, got %q", got)
	}
	if got := mgr.dataByID["t-1"]["triage_reason"]; got != "needs manual input" {
		t.Fatalf("expected triage_reason to be saved, got %q", got)
	}
}

func TestLoopBlocksTaskBelowQualityThreshold(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{
		ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen,
		Metadata: map[string]string{"quality_score": "45"},
	})
	run := &fakeRunner{results: []contracts.RunnerResult{{Status: contracts.RunnerResultCompleted}}}
	loop := NewLoop(mgr, run, nil, LoopOptions{ParentID: "root", QualityGateThreshold: 50})

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if summary.Blocked != 1 {
		t.Fatalf("expected blocked summary, got %#v", summary)
	}
	if mgr.statusByID["t-1"] != contracts.TaskStatusBlocked {
		t.Fatalf("expected blocked status, got %s", mgr.statusByID["t-1"])
	}
	if len(run.requests) != 0 {
		t.Fatalf("expected no runner requests when blocked by quality gate, got %d", len(run.requests))
	}
	if got := mgr.dataByID["t-1"]["triage_status"]; got != "blocked" {
		t.Fatalf("expected triage_status=blocked, got %q", got)
	}
	if got := mgr.dataByID["t-1"]["triage_reason"]; !strings.Contains(got, "below threshold") {
		t.Fatalf("expected triage_reason to mention quality threshold, got %q", got)
	}
}

func TestLoopBlocksTaskBelowCoverageThreshold(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{
		ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen,
		Metadata: map[string]string{"coverage": "45"},
	})
	run := &fakeRunner{results: []contracts.RunnerResult{{Status: contracts.RunnerResultCompleted}}}
	loop := NewLoop(mgr, run, nil, LoopOptions{ParentID: "root", QualityGateThreshold: 50})

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if summary.Blocked != 1 {
		t.Fatalf("expected blocked summary, got %#v", summary)
	}
	if mgr.statusByID["t-1"] != contracts.TaskStatusBlocked {
		t.Fatalf("expected blocked status, got %s", mgr.statusByID["t-1"])
	}
	if len(run.requests) != 0 {
		t.Fatalf("expected no runner requests when blocked by coverage gate, got %d", len(run.requests))
	}
	if got := mgr.dataByID["t-1"]["triage_reason"]; !strings.Contains(got, "below threshold") {
		t.Fatalf("expected triage_reason to mention coverage threshold, got %q", got)
	}
	if strings.TrimSpace(mgr.dataByID["t-1"]["quality_gate_comment"]) == "" {
		t.Fatalf("expected quality gate comment to be stored for blocked task")
	}
}

func TestLoopBlocksTaskBelowCoverageThresholdEvenWithHighQualityScore(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{
		ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen,
		Metadata: map[string]string{
			"quality_score": "92",
			"coverage":      "45",
		},
	})
	run := &fakeRunner{results: []contracts.RunnerResult{{Status: contracts.RunnerResultCompleted}}}
	loop := NewLoop(mgr, run, nil, LoopOptions{ParentID: "root", QualityGateThreshold: 50})

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if summary.Blocked != 1 {
		t.Fatalf("expected blocked summary, got %#v", summary)
	}
	if mgr.statusByID["t-1"] != contracts.TaskStatusBlocked {
		t.Fatalf("expected blocked status, got %s", mgr.statusByID["t-1"])
	}
	if len(run.requests) != 0 {
		t.Fatalf("expected no runner requests when blocked by coverage gate, got %d", len(run.requests))
	}
	if got := mgr.dataByID["t-1"]["triage_reason"]; !strings.Contains(got, "below threshold") {
		t.Fatalf("expected triage_reason to mention coverage threshold, got %q", got)
	}
}

func TestLoopBlocksTaskBelowQualityThresholdWithAutoComment(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{
		ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen,
		Metadata: map[string]string{
			"quality_score":  "45",
			"quality_issues": "Missing acceptance criteria in task spec\nMissing testing plan with commands",
		},
	})
	run := &fakeRunner{results: []contracts.RunnerResult{{Status: contracts.RunnerResultCompleted}}}
	loop := NewLoop(mgr, run, nil, LoopOptions{ParentID: "root", QualityGateThreshold: 50})

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if summary.Blocked != 1 {
		t.Fatalf("expected blocked summary, got %#v", summary)
	}
	comment := mgr.dataByID["t-1"]["quality_gate_comment"]
	if comment == "" {
		t.Fatalf("expected quality gate comment for blocked task")
	}
	if !strings.Contains(comment, "quality score 45 is below threshold 50") {
		t.Fatalf("expected quality score context in comment, got %q", comment)
	}
	if !strings.Contains(comment, "Missing acceptance criteria in task spec") {
		t.Fatalf("expected comment to include quality issues, got %q", comment)
	}
	if !strings.Contains(comment, "Please update the task") {
		t.Fatalf("expected comment to include suggested fix, got %q", comment)
	}
}

func TestLoopAllowsTaskAtOrAboveCoverageThreshold(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{
		ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen,
		Metadata: map[string]string{"coverage": "50"},
	})
	run := &fakeRunner{results: []contracts.RunnerResult{{Status: contracts.RunnerResultCompleted}}}
	loop := NewLoop(mgr, run, nil, LoopOptions{ParentID: "root", QualityGateThreshold: 50})

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if summary.Completed != 1 {
		t.Fatalf("expected completed summary, got %#v", summary)
	}
	if mgr.statusByID["t-1"] != contracts.TaskStatusClosed {
		t.Fatalf("expected task to close when coverage is at threshold, got %s", mgr.statusByID["t-1"])
	}
	if len(run.requests) != 1 {
		t.Fatalf("expected one runner request when coverage passes threshold, got %d", len(run.requests))
	}
}

func TestLoopAllowsTaskBelowQualityThresholdWithOverride(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{
		ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen,
		Metadata: map[string]string{"quality_score": "45"},
	})
	sink := &recordingSink{}
	run := &fakeRunner{results: []contracts.RunnerResult{{Status: contracts.RunnerResultCompleted}}}
	loop := NewLoop(mgr, run, sink, LoopOptions{ParentID: "root", QualityGateThreshold: 50, AllowLowQuality: true})

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if summary.Completed != 1 {
		t.Fatalf("expected completed summary, got %#v", summary)
	}
	if mgr.statusByID["t-1"] != contracts.TaskStatusClosed {
		t.Fatalf("expected closed status, got %s", mgr.statusByID["t-1"])
	}
	warnings := eventsByType(sink.events, contracts.EventTypeRunnerWarning)
	if len(warnings) != 1 {
		t.Fatalf("expected one quality-gate warning, got %d", len(warnings))
	}
	if warnings[0].Message == "" || !strings.Contains(warnings[0].Metadata["reason"], "below threshold") {
		t.Fatalf("expected warning with quality reason, got %#v", warnings[0].Metadata)
	}
}

func TestLoopCreatesAndChecksOutTaskBranchBeforeRun(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{{Status: contracts.RunnerResultCompleted}}}
	vcs := &fakeVCS{}
	loop := NewLoop(mgr, run, nil, LoopOptions{ParentID: "root", MaxRetries: 0, VCS: vcs})

	_, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}

	if len(vcs.calls) != 3 {
		t.Fatalf("expected 3 vcs calls, got %v", vcs.calls)
	}
	if vcs.calls[0] != "ensure_main" {
		t.Fatalf("expected ensure_main first, got %v", vcs.calls)
	}
	if vcs.calls[1] != "create_branch:t-1" {
		t.Fatalf("expected create branch for task, got %v", vcs.calls)
	}
	if vcs.calls[2] != "checkout:task/t-1" {
		t.Fatalf("expected checkout of task branch, got %v", vcs.calls)
	}
}

func TestLoopStorageEnginePathUsesStorageBackendAndTaskEngine(t *testing.T) {
	storage := newSpyStorageBackend([]contracts.Task{
		{ID: "root", Title: "Root", Status: contracts.TaskStatusClosed},
		{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen, ParentID: "root"},
	}, []contracts.TaskRelation{
		{FromID: "root", ToID: "t-1", Type: contracts.RelationParent},
	})
	engine := newSpyTaskEngine(enginepkg.NewTaskEngine())
	run := &fakeRunner{results: []contracts.RunnerResult{{Status: contracts.RunnerResultCompleted}}}
	loop := NewLoopWithTaskEngine(storage, engine, run, nil, LoopOptions{ParentID: "root", Concurrency: 4})

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if summary.Completed != 1 {
		t.Fatalf("expected one completed task, got %#v", summary)
	}
	if storage.getTaskTreeCalls == 0 {
		t.Fatalf("expected storage GetTaskTree to be called")
	}
	if storage.statusSetCount("t-1", contracts.TaskStatusInProgress) == 0 {
		t.Fatalf("expected storage SetTaskStatus to set in_progress")
	}
	if storage.statusSetCount("t-1", contracts.TaskStatusClosed) == 0 {
		t.Fatalf("expected storage SetTaskStatus to set closed")
	}
	if engine.buildGraphCalls == 0 {
		t.Fatalf("expected task engine BuildGraph to be called")
	}
	if engine.nextAvailableCalls == 0 {
		t.Fatalf("expected task engine GetNextAvailable to be called")
	}
	if engine.calculateConcurrencyCalls == 0 {
		t.Fatalf("expected task engine CalculateConcurrency to be called")
	}
	if engine.isCompleteCalls == 0 {
		t.Fatalf("expected task engine IsComplete to be called")
	}
}

func TestStorageEngineTaskManagerSetTaskStatusPropagatesTaskEngineErrors(t *testing.T) {
	storage := newSpyStorageBackend([]contracts.Task{
		{ID: "root", Title: "Root", Status: contracts.TaskStatusClosed},
		{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen, ParentID: "root"},
	}, []contracts.TaskRelation{
		{FromID: "root", ToID: "t-1", Type: contracts.RelationParent},
	})
	engine := newSpyTaskEngine(enginepkg.NewTaskEngine())
	engine.updateTaskStatusErr = errors.New("graph update failed")
	manager := newStorageEngineTaskManager(storage, engine, "root")

	if _, err := manager.NextTasks(context.Background(), "root"); err != nil {
		t.Fatalf("NextTasks failed: %v", err)
	}

	err := manager.SetTaskStatus(context.Background(), "t-1", contracts.TaskStatusClosed)
	if err == nil {
		t.Fatalf("expected SetTaskStatus to return task engine update error")
	}
	if !strings.Contains(err.Error(), "graph update failed") {
		t.Fatalf("expected task engine update error, got %q", err.Error())
	}
	if engine.updateTaskStatusCalls == 0 {
		t.Fatalf("expected task engine UpdateTaskStatus to be called")
	}
	if storage.statusSetCount("t-1", contracts.TaskStatusClosed) == 0 {
		t.Fatalf("expected storage SetTaskStatus to be called before surfacing error")
	}
}

func TestStorageEngineTaskManagerSetTaskStatusPersistsBackendStateChanges(t *testing.T) {
	storage := newPersistingSpyStorageBackend([]contracts.Task{
		{ID: "root", Title: "Root", Status: contracts.TaskStatusClosed},
		{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen, ParentID: "root"},
	}, []contracts.TaskRelation{{FromID: "root", ToID: "t-1", Type: contracts.RelationParent}})
	engine := newSpyTaskEngine(enginepkg.NewTaskEngine())
	manager := newStorageEngineTaskManager(storage, engine, "root")

	if _, err := manager.NextTasks(context.Background(), "root"); err != nil {
		t.Fatalf("NextTasks failed: %v", err)
	}

	if err := manager.SetTaskStatus(context.Background(), "t-1", contracts.TaskStatusClosed); err != nil {
		t.Fatalf("SetTaskStatus failed: %v", err)
	}
	if storage.persistStatusCount("t-1", contracts.TaskStatusClosed) == 0 {
		t.Fatalf("expected persistence hook to record closed status")
	}
	if engine.updateTaskStatusCalls == 0 {
		t.Fatalf("expected task engine graph update after persistence")
	}
}

func TestStorageEngineTaskManagerSetTaskStatusReturnsPersistenceErrors(t *testing.T) {
	storage := newPersistingSpyStorageBackend([]contracts.Task{
		{ID: "root", Title: "Root", Status: contracts.TaskStatusClosed},
		{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen, ParentID: "root"},
	}, []contracts.TaskRelation{{FromID: "root", ToID: "t-1", Type: contracts.RelationParent}})
	storage.persistStatusErr = errors.New("persist failed")
	engine := newSpyTaskEngine(enginepkg.NewTaskEngine())
	manager := newStorageEngineTaskManager(storage, engine, "root")

	if _, err := manager.NextTasks(context.Background(), "root"); err != nil {
		t.Fatalf("NextTasks failed: %v", err)
	}

	err := manager.SetTaskStatus(context.Background(), "t-1", contracts.TaskStatusClosed)
	if err == nil {
		t.Fatalf("expected persistence failure")
	}
	if !strings.Contains(err.Error(), "persist failed") {
		t.Fatalf("expected persistence error, got %q", err.Error())
	}
	if storage.statusSetCount("t-1", contracts.TaskStatusClosed) == 0 {
		t.Fatalf("expected storage SetTaskStatus to occur before persistence error")
	}
	if engine.updateTaskStatusCalls != 0 {
		t.Fatalf("expected graph update to be skipped after persistence failure")
	}
}

func TestStorageEngineTaskManagerSetTaskDataPersistsBackendStateChanges(t *testing.T) {
	storage := newPersistingSpyStorageBackend([]contracts.Task{
		{ID: "root", Title: "Root", Status: contracts.TaskStatusClosed},
		{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen, ParentID: "root"},
	}, []contracts.TaskRelation{{FromID: "root", ToID: "t-1", Type: contracts.RelationParent}})
	engine := newSpyTaskEngine(enginepkg.NewTaskEngine())
	manager := newStorageEngineTaskManager(storage, engine, "root")

	if err := manager.SetTaskData(context.Background(), "t-1", map[string]string{"triage_status": "blocked"}); err != nil {
		t.Fatalf("SetTaskData failed: %v", err)
	}
	if storage.persistDataCount("t-1") == 0 {
		t.Fatalf("expected persistence hook to record task data change")
	}
}

func TestLoopWithTaskEngineTreatsOpenRootWithTerminalChildrenAsComplete(t *testing.T) {
	storage := newSpyStorageBackend([]contracts.Task{
		{ID: "root", Title: "Root", Status: contracts.TaskStatusOpen},
		{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusClosed, ParentID: "root"},
		{ID: "t-2", Title: "Task 2", Status: contracts.TaskStatusFailed, ParentID: "root"},
	}, []contracts.TaskRelation{
		{FromID: "root", ToID: "t-1", Type: contracts.RelationParent},
		{FromID: "root", ToID: "t-2", Type: contracts.RelationParent},
	})
	engine := newSpyTaskEngine(enginepkg.NewTaskEngine())
	run := &fakeRunner{}
	loop := NewLoopWithTaskEngine(storage, engine, run, nil, LoopOptions{ParentID: "root", Concurrency: 2})

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if summary.TotalProcessed() != 0 {
		t.Fatalf("expected no processed tasks, got %#v", summary)
	}
	if len(run.requests) != 0 {
		t.Fatalf("expected runner not to be invoked, got %d calls", len(run.requests))
	}
	if engine.isCompleteCalls == 0 {
		t.Fatalf("expected task engine IsComplete to be called")
	}
}

func TestLoopReturnsErrorWhenCompletionCheckerReportsIncomplete(t *testing.T) {
	mgr := &completionAwareTaskManager{
		fakeTaskManager: newFakeTaskManager(),
		complete:        false,
	}
	loop := NewLoop(mgr, &fakeRunner{}, nil, LoopOptions{ParentID: "root"})

	summary, err := loop.Run(context.Background())
	if err == nil {
		t.Fatalf("expected incomplete graph error, got summary %#v", summary)
	}
	if !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("expected incomplete graph error message, got %q", err.Error())
	}
	if mgr.isCompleteCalls == 0 {
		t.Fatalf("expected completion checker to be called")
	}
}

type fakeTaskManager struct {
	mu             sync.Mutex
	queue          []contracts.Task
	statusByID     map[string]contracts.TaskStatus
	dataByID       map[string]map[string]string
	dependsOn      map[string][]string
	failStatusOnce map[string]error
}

func newFakeTaskManager(tasks ...contracts.Task) *fakeTaskManager {
	status := map[string]contracts.TaskStatus{}
	for _, task := range tasks {
		status[task.ID] = task.Status
	}
	return &fakeTaskManager{queue: tasks, statusByID: status, dataByID: map[string]map[string]string{}}
}

type completionAwareTaskManager struct {
	*fakeTaskManager
	complete        bool
	isCompleteErr   error
	isCompleteCalls int
}

func (m *completionAwareTaskManager) IsComplete(context.Context) (bool, error) {
	m.isCompleteCalls++
	if m.isCompleteErr != nil {
		return false, m.isCompleteErr
	}
	return m.complete, nil
}

func (f *fakeTaskManager) NextTasks(context.Context, string) ([]contracts.TaskSummary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var tasks []contracts.TaskSummary
	for _, task := range f.queue {
		if f.statusByID[task.ID] == contracts.TaskStatusOpen {
			ready := true
			for _, depID := range f.dependsOn[task.ID] {
				if f.statusByID[depID] != contracts.TaskStatusClosed {
					ready = false
					break
				}
			}
			if !ready {
				continue
			}
			tasks = append(tasks, contracts.TaskSummary{ID: task.ID, Title: task.Title})
		}
	}
	return tasks, nil
}

func (f *fakeTaskManager) GetTask(_ context.Context, taskID string) (contracts.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, task := range f.queue {
		if task.ID == taskID {
			copy := task
			copy.Status = f.statusByID[taskID]
			return copy, nil
		}
	}
	return contracts.Task{}, errors.New("missing task")
}

func (f *fakeTaskManager) SetTaskStatus(_ context.Context, taskID string, status contracts.TaskStatus) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failStatusOnce != nil {
		key := taskID + "|" + string(status)
		if err, ok := f.failStatusOnce[key]; ok {
			delete(f.failStatusOnce, key)
			return err
		}
	}
	f.statusByID[taskID] = status
	return nil
}

func (f *fakeTaskManager) SetTaskData(_ context.Context, taskID string, data map[string]string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.dataByID[taskID] == nil {
		f.dataByID[taskID] = map[string]string{}
	}
	for key, value := range data {
		f.dataByID[taskID][key] = value
	}
	return nil
}

type fakeRunner struct {
	results          []contracts.RunnerResult
	idx              int
	modes            []contracts.RunnerMode
	requests         []contracts.RunnerRequest
	progressMessages []string
	progressEvents   []contracts.RunnerProgress
	runDelay         time.Duration
}

func (f *fakeRunner) Run(_ context.Context, request contracts.RunnerRequest) (contracts.RunnerResult, error) {
	f.modes = append(f.modes, request.Mode)
	f.requests = append(f.requests, request)
	if f.runDelay > 0 {
		time.Sleep(f.runDelay)
	}
	if request.OnProgress != nil {
		if len(f.progressEvents) > 0 {
			for _, progress := range f.progressEvents {
				if progress.Timestamp.IsZero() {
					progress.Timestamp = time.Now().UTC()
				}
				request.OnProgress(progress)
			}
		} else {
			for _, message := range f.progressMessages {
				request.OnProgress(contracts.RunnerProgress{Type: "acp_update", Message: message, Timestamp: time.Now().UTC()})
			}
		}
	}
	if f.idx >= len(f.results) {
		return contracts.RunnerResult{Status: contracts.RunnerResultFailed, Reason: "missing result"}, nil
	}
	result := f.results[f.idx]
	f.idx++
	return result, nil
}

type dependencyTrackingRunner struct {
	release   chan struct{}
	started   chan string
	requests  []contracts.RunnerRequest
	active    int
	maxActive int
	mu        sync.Mutex
}

func (r *dependencyTrackingRunner) Run(_ context.Context, request contracts.RunnerRequest) (contracts.RunnerResult, error) {
	r.mu.Lock()
	r.requests = append(r.requests, request)
	r.active++
	if r.active > r.maxActive {
		r.maxActive = r.active
	}
	r.mu.Unlock()

	if r.started != nil {
		r.started <- request.TaskID
	}
	if r.release != nil {
		<-r.release
	}

	r.mu.Lock()
	r.active--
	r.mu.Unlock()
	return contracts.RunnerResult{Status: contracts.RunnerResultCompleted}, nil
}

type logWritingRunner struct {
	logPath string
}

func (r *logWritingRunner) Run(_ context.Context, request contracts.RunnerRequest) (contracts.RunnerResult, error) {
	path := strings.TrimSpace(request.Metadata["log_path"])
	if path == "" {
		return contracts.RunnerResult{Status: contracts.RunnerResultFailed, Reason: "missing log_path metadata"}, nil
	}
	r.logPath = path
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return contracts.RunnerResult{Status: contracts.RunnerResultFailed, Reason: err.Error()}, nil
	}
	if err := os.WriteFile(path, []byte("ok\n"), 0o644); err != nil {
		return contracts.RunnerResult{Status: contracts.RunnerResultFailed, Reason: err.Error()}, nil
	}
	return contracts.RunnerResult{Status: contracts.RunnerResultCompleted}, nil
}

func (r *logWritingRunner) LogPath() string {
	return r.logPath
}

type structuredDecisionRunner struct {
	result  contracts.RunnerResult
	logPath string
}

func (r *structuredDecisionRunner) Run(_ context.Context, request contracts.RunnerRequest) (contracts.RunnerResult, error) {
	path := strings.TrimSpace(request.Metadata["log_path"])
	if path == "" {
		return contracts.RunnerResult{Status: contracts.RunnerResultFailed, Reason: "missing log_path metadata"}, nil
	}
	r.logPath = path
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return contracts.RunnerResult{Status: contracts.RunnerResultFailed, Reason: err.Error()}, nil
	}
	defer file.Close()

	logger := logging.NewStructuredLogger(file, "info", logging.LoggingSchemaFields{TaskID: request.TaskID})
	if err := logger.Log("info", map[string]interface{}{
		"message": "task execution started",
		"outcome": "intentional failure",
	}); err != nil {
		return contracts.RunnerResult{Status: contracts.RunnerResultFailed, Reason: err.Error()}, nil
	}
	if err := logger.Log("warn", map[string]interface{}{
		"message":  "decision event",
		"decision": "failed",
	}); err != nil {
		return contracts.RunnerResult{Status: contracts.RunnerResultFailed, Reason: err.Error()}, nil
	}

	if r.result.Status == "" {
		return contracts.RunnerResult{Status: contracts.RunnerResultFailed, Reason: "missing configured result"}, nil
	}
	return r.result, nil
}

func (r *structuredDecisionRunner) LogPath() string {
	return r.logPath
}

type noopSink struct{}

func (noopSink) Emit(context.Context, contracts.Event) error { return nil }

type fakeVCS struct {
	calls      []string
	commitErr  error
	commitSHA  string
	mergeErr   error
	mergeErrs  []error
	mergeCalls int
	pushErr    error
}

func (f *fakeVCS) EnsureMain(context.Context) error {
	f.calls = append(f.calls, "ensure_main")
	return nil
}

func (f *fakeVCS) CreateTaskBranch(_ context.Context, taskID string) (string, error) {
	f.calls = append(f.calls, "create_branch:"+taskID)
	return "task/" + taskID, nil
}

func (f *fakeVCS) Checkout(_ context.Context, ref string) error {
	f.calls = append(f.calls, "checkout:"+ref)
	return nil
}

func (f *fakeVCS) CommitAll(_ context.Context, message string) (string, error) {
	f.calls = append(f.calls, "commit_all:"+message)
	if f.commitErr != nil {
		return "", f.commitErr
	}
	if f.commitSHA != "" {
		return f.commitSHA, nil
	}
	return "abc123", nil
}

func (f *fakeVCS) MergeToMain(_ context.Context, branch string) error {
	f.calls = append(f.calls, "merge_to_main:"+branch)
	f.mergeCalls++
	if len(f.mergeErrs) > 0 {
		err := f.mergeErrs[0]
		f.mergeErrs = f.mergeErrs[1:]
		return err
	}
	return f.mergeErr
}

func (f *fakeVCS) PushBranch(_ context.Context, branch string) error {
	f.calls = append(f.calls, "push_branch:"+branch)
	return nil
}

func (f *fakeVCS) PushMain(context.Context) error {
	f.calls = append(f.calls, "push_main")
	return f.pushErr
}

func TestLoopRunsReviewAfterImplementationSuccess(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{
		{Status: contracts.RunnerResultCompleted},
		{Status: contracts.RunnerResultCompleted, ReviewReady: true},
	}}
	loop := NewLoop(mgr, run, nil, LoopOptions{ParentID: "root", MaxRetries: 0, RequireReview: true})

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if summary.Completed != 1 {
		t.Fatalf("expected completed summary, got %#v", summary)
	}
	if len(run.modes) != 2 {
		t.Fatalf("expected two runner calls, got %d", len(run.modes))
	}
	if run.modes[0] != contracts.RunnerModeImplement || run.modes[1] != contracts.RunnerModeReview {
		t.Fatalf("unexpected runner mode sequence: %#v", run.modes)
	}
}

func TestLoopFailsTaskWhenReviewFails(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{
		{Status: contracts.RunnerResultCompleted},
		{Status: contracts.RunnerResultFailed, Reason: "review rejected"},
	}}
	loop := NewLoop(mgr, run, nil, LoopOptions{ParentID: "root", MaxRetries: 0, RequireReview: true})

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if summary.Failed != 1 {
		t.Fatalf("expected failed summary, got %#v", summary)
	}
	if mgr.statusByID["t-1"] != contracts.TaskStatusFailed {
		t.Fatalf("expected failed task status, got %s", mgr.statusByID["t-1"])
	}
}

func TestLoopFailsTaskWhenReviewVerdictIsMissing(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{
		{Status: contracts.RunnerResultCompleted},
		{Status: contracts.RunnerResultCompleted, ReviewReady: false},
		{Status: contracts.RunnerResultCompleted, ReviewReady: false},
	}}
	vcs := &fakeVCS{}
	loop := NewLoop(mgr, run, nil, LoopOptions{ParentID: "root", MaxRetries: 0, RequireReview: true, MergeOnSuccess: true, VCS: vcs})

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if summary.Failed != 1 {
		t.Fatalf("expected failed summary, got %#v", summary)
	}
	if mgr.statusByID["t-1"] != contracts.TaskStatusFailed {
		t.Fatalf("expected failed task status, got %s", mgr.statusByID["t-1"])
	}
	if containsCall(vcs.calls, "merge_to_main:task/t-1") {
		t.Fatalf("did not expect merge_to_main call, got %v", vcs.calls)
	}
	if containsCall(vcs.calls, "push_main") {
		t.Fatalf("did not expect push_main call, got %v", vcs.calls)
	}
	if len(run.modes) != 3 {
		t.Fatalf("expected implement + review + verdict retry runs, got %d", len(run.modes))
	}
}

func TestLoopRetriesReviewWithVerdictOnlyPromptAndCompletes(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{
		{Status: contracts.RunnerResultCompleted},
		{Status: contracts.RunnerResultCompleted, ReviewReady: false},
		{Status: contracts.RunnerResultCompleted, ReviewReady: true},
	}}
	loop := NewLoop(mgr, run, nil, LoopOptions{ParentID: "root", MaxRetries: 0, RequireReview: true})

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if summary.Completed != 1 {
		t.Fatalf("expected completed summary after verdict retry, got %#v", summary)
	}
	if mgr.statusByID["t-1"] != contracts.TaskStatusClosed {
		t.Fatalf("expected closed task status, got %s", mgr.statusByID["t-1"])
	}
	if len(run.modes) != 3 {
		t.Fatalf("expected implement + review + verdict retry runs, got %d", len(run.modes))
	}
	if run.modes[0] != contracts.RunnerModeImplement || run.modes[1] != contracts.RunnerModeReview || run.modes[2] != contracts.RunnerModeReview {
		t.Fatalf("unexpected mode sequence: %#v", run.modes)
	}
	if !strings.Contains(run.requests[2].Prompt, "Verdict-only follow-up") {
		t.Fatalf("expected verdict-only retry prompt, got %q", run.requests[2].Prompt)
	}
}

func TestLoopSkipsVerdictRetryWhenReviewVerdictIsExplicitFail(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{
		{Status: contracts.RunnerResultCompleted},
		{Status: contracts.RunnerResultCompleted, ReviewReady: false, Artifacts: map[string]string{"review_verdict": "fail"}},
	}}
	loop := NewLoop(mgr, run, nil, LoopOptions{ParentID: "root", MaxRetries: 0, RequireReview: true})

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if summary.Failed != 1 {
		t.Fatalf("expected failed summary after explicit fail verdict, got %#v", summary)
	}
	if len(run.modes) != 2 {
		t.Fatalf("expected implement + review (no verdict retry), got %d", len(run.modes))
	}
	if got := mgr.dataByID["t-1"]["triage_reason"]; got != "review verdict returned fail" {
		t.Fatalf("expected explicit fail triage reason, got %q", got)
	}
}

func TestLoopUsesStructuredReviewFailFeedbackAsTriageReason(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{
		{Status: contracts.RunnerResultCompleted},
		{
			Status:      contracts.RunnerResultCompleted,
			ReviewReady: false,
			Artifacts: map[string]string{
				"review_verdict":       "fail",
				"review_fail_feedback": "missing regression test for retry/backoff flow",
			},
		},
	}}
	loop := NewLoop(mgr, run, nil, LoopOptions{ParentID: "root", MaxRetries: 0, RequireReview: true})

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if summary.Failed != 1 {
		t.Fatalf("expected failed summary after explicit fail verdict, got %#v", summary)
	}
	if got := mgr.dataByID["t-1"]["triage_reason"]; got != "review rejected: missing regression test for retry/backoff flow" {
		t.Fatalf("expected structured review fail triage reason, got %q", got)
	}
	if got := mgr.dataByID["t-1"]["review_verdict"]; got != "fail" {
		t.Fatalf("expected review_verdict to be persisted, got %q", got)
	}
	if got := mgr.dataByID["t-1"]["review_fail_feedback"]; got != "missing regression test for retry/backoff flow" {
		t.Fatalf("expected review_fail_feedback to be persisted, got %q", got)
	}
}

func TestLoopRetriesReviewFailAndInjectsFeedbackIntoImplementPrompt(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{
		{Status: contracts.RunnerResultCompleted},
		{
			Status:      contracts.RunnerResultCompleted,
			ReviewReady: false,
			Artifacts: map[string]string{
				"review_verdict":       "fail",
				"review_fail_feedback": "add missing RED->GREEN ticket notes",
			},
		},
		{Status: contracts.RunnerResultCompleted},
		{Status: contracts.RunnerResultCompleted, ReviewReady: true},
	}}
	loop := NewLoop(mgr, run, nil, LoopOptions{ParentID: "root", MaxRetries: 1, RequireReview: true})

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if summary.Completed != 1 {
		t.Fatalf("expected completed summary after review remediation retry, got %#v", summary)
	}
	if len(run.requests) != 4 {
		t.Fatalf("expected implement+review then implement+review, got %d requests", len(run.requests))
	}
	if run.requests[2].Mode != contracts.RunnerModeImplement {
		t.Fatalf("expected third request to be implement retry, got %s", run.requests[2].Mode)
	}
	if !strings.Contains(run.requests[2].Prompt, "Review Remediation Loop: Attempt 1") {
		t.Fatalf("expected remediation attempt marker in retry prompt, got %q", run.requests[2].Prompt)
	}
	if !strings.Contains(run.requests[2].Prompt, "add missing RED->GREEN ticket notes") {
		t.Fatalf("expected review fail feedback in retry prompt, got %q", run.requests[2].Prompt)
	}
	if got := mgr.dataByID["t-1"]["review_retry_count"]; got != "1" {
		t.Fatalf("expected review_retry_count=1, got %q", got)
	}
	if got := mgr.dataByID["t-1"]["review_feedback"]; got != "add missing RED->GREEN ticket notes" {
		t.Fatalf("expected review_feedback persisted, got %q", got)
	}
}

func TestLoopMarksFailedWhenReviewRetryBudgetExhausted(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{
		{Status: contracts.RunnerResultCompleted},
		{
			Status:      contracts.RunnerResultCompleted,
			ReviewReady: false,
			Artifacts: map[string]string{
				"review_verdict":       "fail",
				"review_fail_feedback": "first remediation request",
			},
		},
		{Status: contracts.RunnerResultCompleted},
		{
			Status:      contracts.RunnerResultCompleted,
			ReviewReady: false,
			Artifacts: map[string]string{
				"review_verdict":       "fail",
				"review_fail_feedback": "second remediation request still failing",
			},
		},
	}}
	loop := NewLoop(mgr, run, nil, LoopOptions{ParentID: "root", MaxRetries: 1, RequireReview: true})

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if summary.Failed != 1 {
		t.Fatalf("expected failed summary after retry exhaustion, got %#v", summary)
	}
	if mgr.statusByID["t-1"] != contracts.TaskStatusFailed {
		t.Fatalf("expected failed status after retry exhaustion, got %s", mgr.statusByID["t-1"])
	}
	if got := mgr.dataByID["t-1"]["triage_reason"]; got != "review rejected: second remediation request still failing" {
		t.Fatalf("unexpected triage_reason after retry exhaustion: %q", got)
	}
	if got := mgr.dataByID["t-1"]["review_retry_count"]; got != "1" {
		t.Fatalf("expected review_retry_count to remain 1 after one retry, got %q", got)
	}
}

func TestLoopMergesAndPushesAfterSuccessfulReview(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{
		{Status: contracts.RunnerResultCompleted},
		{Status: contracts.RunnerResultCompleted, ReviewReady: true},
	}}
	vcs := &fakeVCS{}
	loop := NewLoop(mgr, run, nil, LoopOptions{ParentID: "root", MaxRetries: 0, RequireReview: true, MergeOnSuccess: true, VCS: vcs})

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if summary.Completed != 1 {
		t.Fatalf("expected completed summary, got %#v", summary)
	}
	if !containsCall(vcs.calls, "merge_to_main:task/t-1") {
		t.Fatalf("expected merge_to_main call, got %v", vcs.calls)
	}
	if !containsCall(vcs.calls, "push_main") {
		t.Fatalf("expected push_main call, got %v", vcs.calls)
	}
	if !containsCallPrefix(vcs.calls, "commit_all:chore(task): auto-commit before landing t-1") {
		t.Fatalf("expected auto-commit call before landing, got %v", vcs.calls)
	}
	if callIndex(vcs.calls, "commit_all:chore(task): auto-commit before landing t-1") > callIndex(vcs.calls, "merge_to_main:task/t-1") {
		t.Fatalf("expected auto-commit before merge, got %v", vcs.calls)
	}
}

func TestLoopBlocksTaskWhenAutoCommitBeforeLandingFails(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{
		{Status: contracts.RunnerResultCompleted},
		{Status: contracts.RunnerResultCompleted, ReviewReady: true},
	}}
	vcs := &fakeVCS{commitErr: errors.New("git commit failed: index lock")}
	loop := NewLoop(mgr, run, nil, LoopOptions{ParentID: "root", MaxRetries: 0, RequireReview: true, MergeOnSuccess: true, VCS: vcs})

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if summary.Blocked != 1 {
		t.Fatalf("expected blocked summary, got %#v", summary)
	}
	if mgr.statusByID["t-1"] != contracts.TaskStatusBlocked {
		t.Fatalf("expected blocked task status, got %s", mgr.statusByID["t-1"])
	}
	if !containsCallPrefix(vcs.calls, "commit_all:chore(task): auto-commit before landing t-1") {
		t.Fatalf("expected auto-commit attempt, got %v", vcs.calls)
	}
	if containsCall(vcs.calls, "merge_to_main:task/t-1") {
		t.Fatalf("did not expect merge call after auto-commit failure, got %v", vcs.calls)
	}
	if containsCall(vcs.calls, "push_main") {
		t.Fatalf("did not expect push_main after auto-commit failure, got %v", vcs.calls)
	}
	if got := mgr.dataByID["t-1"]["triage_reason"]; !strings.Contains(got, "git commit failed") {
		t.Fatalf("expected triage reason with commit failure, got %q", got)
	}
}

func containsCall(calls []string, want string) bool {
	for _, call := range calls {
		if call == want {
			return true
		}
	}
	return false
}

func containsCallPrefix(calls []string, wantPrefix string) bool {
	for _, call := range calls {
		if strings.HasPrefix(call, wantPrefix) {
			return true
		}
	}
	return false
}

func callIndex(calls []string, want string) int {
	for i, call := range calls {
		if call == want {
			return i
		}
	}
	return -1
}

func TestLoopRespectsMaxTasksLimit(t *testing.T) {
	mgr := newFakeTaskManager(
		contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen},
		contracts.Task{ID: "t-2", Title: "Task 2", Status: contracts.TaskStatusOpen},
	)
	run := &fakeRunner{results: []contracts.RunnerResult{
		{Status: contracts.RunnerResultCompleted},
		{Status: contracts.RunnerResultCompleted},
	}}
	loop := NewLoop(mgr, run, nil, LoopOptions{ParentID: "root", MaxTasks: 1})

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if summary.Completed != 1 {
		t.Fatalf("expected exactly one completion, got %#v", summary)
	}
	if mgr.statusByID["t-2"] != contracts.TaskStatusOpen {
		t.Fatalf("expected second task to remain open")
	}
}

func TestLoopDryRunSkipsExecution(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{{Status: contracts.RunnerResultCompleted}}}
	loop := NewLoop(mgr, run, nil, LoopOptions{ParentID: "root", DryRun: true})

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if summary.Skipped != 1 {
		t.Fatalf("expected skipped summary for dry run, got %#v", summary)
	}
	if len(run.modes) != 0 {
		t.Fatalf("runner should not be called in dry run")
	}
}

func TestLoopStopsWhenSignalChannelCloses(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{{Status: contracts.RunnerResultCompleted}}}
	stop := make(chan struct{})
	close(stop)
	loop := NewLoop(mgr, run, nil, LoopOptions{ParentID: "root", Stop: stop})

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if summary.TotalProcessed() != 0 {
		t.Fatalf("expected no processed tasks when stop already closed, got %#v", summary)
	}
}

func TestLoopBuildsRunnerRequestWithRepoAndModel(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Description: "Do work", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{{Status: contracts.RunnerResultCompleted}}}
	loop := NewLoop(mgr, run, nil, LoopOptions{ParentID: "root", RepoRoot: "/repo", Model: "openai/gpt-5.3-codex", RunnerTimeout: 3 * time.Second})

	_, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}

	if len(run.requests) != 1 {
		t.Fatalf("expected one runner request, got %d", len(run.requests))
	}
	req := run.requests[0]
	if req.RepoRoot != "/repo" {
		t.Fatalf("expected repo root /repo, got %q", req.RepoRoot)
	}
	if req.Model != "openai/gpt-5.3-codex" {
		t.Fatalf("expected model to be set, got %q", req.Model)
	}
	if req.Prompt == "" {
		t.Fatalf("expected non-empty prompt")
	}
	if req.Timeout != 3*time.Second {
		t.Fatalf("expected timeout=3, got %s", req.Timeout)
	}
}

func TestLoopEmitsLifecycleEvents(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{{Status: contracts.RunnerResultCompleted}}}
	sink := &recordingSink{}
	loop := NewLoop(mgr, run, sink, LoopOptions{ParentID: "root"})

	_, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if len(sink.events) == 0 {
		t.Fatalf("expected emitted events")
	}
	if sink.events[0].Type != contracts.EventTypeTaskStarted {
		t.Fatalf("expected first event task_started, got %s", sink.events[0].Type)
	}
	if sink.events[0].TaskTitle != "Task 1" {
		t.Fatalf("expected task title in event, got %q", sink.events[0].TaskTitle)
	}
	if !hasEventType(sink.events, contracts.EventTypeRunnerStarted) {
		t.Fatalf("expected runner_started event")
	}
	if !hasEventType(sink.events, contracts.EventTypeRunnerFinished) {
		t.Fatalf("expected runner_finished event")
	}
	if !hasEventType(sink.events, contracts.EventTypeTaskFinished) {
		t.Fatalf("expected task_finished event")
	}
}

func TestLoopEmitsParallelContextInRunnerStartedEvent(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{{Status: contracts.RunnerResultCompleted}}}
	sink := &recordingSink{}
	loop := NewLoop(mgr, run, sink, LoopOptions{ParentID: "root", Concurrency: 1})

	_, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	event, ok := findEventByType(sink.events, contracts.EventTypeRunnerStarted)
	if !ok {
		t.Fatalf("expected runner_started event")
	}
	if event.WorkerID == "" {
		t.Fatalf("expected non-empty worker id")
	}
	if event.QueuePos != 1 {
		t.Fatalf("expected queue position 1, got %d", event.QueuePos)
	}
}

func TestLoopEmitsRunnerStartedMetadata(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{{Status: contracts.RunnerResultCompleted}}}
	sink := &recordingSink{}
	loop := NewLoop(mgr, run, sink, LoopOptions{ParentID: "root", RepoRoot: "/repo", Model: "openai/gpt-5.3-codex"})

	_, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}

	event, ok := findEventByType(sink.events, contracts.EventTypeRunnerStarted)
	if !ok {
		t.Fatalf("expected runner_started event")
	}
	if event.Metadata["backend"] != "opencode" {
		t.Fatalf("expected backend metadata, got %#v", event.Metadata)
	}
	if event.Metadata["mode"] != string(contracts.RunnerModeImplement) {
		t.Fatalf("expected mode metadata, got %#v", event.Metadata)
	}
	if event.Metadata["model"] != "openai/gpt-5.3-codex" {
		t.Fatalf("expected model metadata, got %#v", event.Metadata)
	}
	if event.Metadata["log_path"] != "/repo/runner-logs/root/t-1/opencode/t-1.jsonl" {
		t.Fatalf("expected log_path metadata, got %#v", event.Metadata)
	}
	if event.Metadata["clone_path"] != "/repo" {
		t.Fatalf("expected clone_path metadata, got %#v", event.Metadata)
	}
	if event.Metadata["started_at"] == "" {
		t.Fatalf("expected started_at metadata, got %#v", event.Metadata)
	}
}

func TestLoopEmitsRunnerStartedMetadataWithConfiguredBackend(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{{Status: contracts.RunnerResultCompleted}}}
	sink := &recordingSink{}
	loop := NewLoop(mgr, run, sink, LoopOptions{ParentID: "root", RepoRoot: "/repo", Model: "openai/gpt-5.3-codex", Backend: "codex"})

	_, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}

	event, ok := findEventByType(sink.events, contracts.EventTypeRunnerStarted)
	if !ok {
		t.Fatalf("expected runner_started event")
	}
	if event.Metadata["backend"] != "codex" {
		t.Fatalf("expected backend metadata=codex, got %#v", event.Metadata)
	}
	if event.Metadata["log_path"] != "/repo/runner-logs/root/t-1/codex/t-1.jsonl" {
		t.Fatalf("expected codex log path metadata, got %#v", event.Metadata)
	}
}

func TestLoopEmitsLogsUnderEpicTaskDirectory(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", ParentID: "epic-1", Status: contracts.TaskStatusOpen})
	runner := &logWritingRunner{}
	sink := &recordingSink{}
	repoRoot := t.TempDir()
	loop := NewLoop(mgr, runner, sink, LoopOptions{ParentID: "root", RepoRoot: repoRoot, Model: "openai/gpt-5.3-codex"})

	_, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	expected := filepath.Join(repoRoot, "runner-logs", "epic-1", "t-1", "opencode", "t-1.jsonl")
	if got := runner.LogPath(); got != expected {
		t.Fatalf("expected log to be written to %q, got %q", expected, got)
	}
	if _, err := os.Stat(expected); err != nil {
		t.Fatalf("expected emitted log file %q, got err %v", expected, err)
	}
}

func TestLoopWritesObservabilityPipelineArtifactsForFailedTask(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", ParentID: "epic-1", Status: contracts.TaskStatusOpen})
	runner := &structuredDecisionRunner{
		result: contracts.RunnerResult{
			Status: contracts.RunnerResultFailed,
			Reason: "observability validation failed",
		},
	}
	recording := &recordingSink{}
	repoRoot := t.TempDir()
	eventsPath := filepath.Join(repoRoot, "runner-logs", "agent.events.jsonl")
	sink := contracts.NewFanoutEventSink(contracts.NewFileEventSink(eventsPath), recording)

	loop := NewLoop(mgr, runner, sink, LoopOptions{ParentID: "root", RepoRoot: repoRoot, Model: "openai/gpt-5.3-codex", Backend: "opencode"})
	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if summary.Failed != 1 {
		t.Fatalf("expected failed summary, got %#v", summary)
	}

	expected := filepath.Join(repoRoot, "runner-logs", "epic-1", "t-1", "opencode", "t-1.jsonl")
	if got := runner.LogPath(); got != expected {
		t.Fatalf("expected runner log path %q, got %q", expected, got)
	}
	content, err := os.ReadFile(expected)
	if err != nil {
		t.Fatalf("expected emitted log file %q, got err %v", expected, err)
	}
	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	hasStructuredLine := false
	for scanner.Scan() {
		hasStructuredLine = true
		if err := logging.ValidateStructuredLogLine(scanner.Bytes()); err != nil {
			t.Fatalf("invalid structured log line %q: %v", scanner.Text(), err)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read structured log file: %v", err)
	}
	if !hasStructuredLine {
		t.Fatalf("expected at least one structured log line in %q", expected)
	}

	taskData := eventsByType(recording.events, contracts.EventTypeTaskDataUpdated)
	if len(taskData) == 0 {
		t.Fatalf("expected task_data_updated event for failed task")
	}
	if taskData[len(taskData)-1].Metadata["decision"] != "failed" {
		t.Fatalf("expected failed decision in task_data_updated metadata, got %#v", taskData[len(taskData)-1].Metadata)
	}

	taskFinished := eventsByType(recording.events, contracts.EventTypeTaskFinished)
	if len(taskFinished) == 0 {
		t.Fatalf("expected task_finished event for failed task")
	}
	if taskFinished[len(taskFinished)-1].Metadata["decision"] != "failed" {
		t.Fatalf("expected failed decision in task_finished metadata, got %#v", taskFinished[len(taskFinished)-1].Metadata)
	}

	eventsFile, err := os.ReadFile(eventsPath)
	if err != nil {
		t.Fatalf("expected event file %q, got err %v", eventsPath, err)
	}
	if !strings.Contains(string(eventsFile), "\"decision\":\"failed\"") {
		t.Fatalf("expected decision metadata in events file, got %q", string(eventsFile))
	}

	browser, err := tui.NewLogBrowser(filepath.Join(repoRoot, "runner-logs"))
	if err != nil {
		t.Fatalf("new log browser: %v", err)
	}
	if got := browser.CurrentTask(); got != "t-1" {
		browser.SelectTask(1)
		if got := browser.CurrentTask(); got != "t-1" {
			t.Fatalf("expected log browser to select task-1, got %q", got)
		}
	}
	if got := browser.CurrentLogFile(); got != expected {
		t.Fatalf("expected log browser selected log path %q, got %q", expected, got)
	}
	view := browser.View()
	if !strings.Contains(view, "t-1") || !strings.Contains(view, "level") {
		t.Fatalf("expected log browser view to include rendered task log content, got %q", view)
	}
}

func TestLoopEmitsSeparatedLogPathsForMultipleTasks(t *testing.T) {
	mgr := newFakeTaskManager(
		contracts.Task{ID: "t-1", Title: "Task 1", ParentID: "epic-1", Status: contracts.TaskStatusOpen},
		contracts.Task{ID: "t-2", Title: "Task 2", ParentID: "epic-2", Status: contracts.TaskStatusOpen},
	)
	run := &fakeRunner{results: []contracts.RunnerResult{
		{Status: contracts.RunnerResultCompleted},
		{Status: contracts.RunnerResultCompleted},
	}}
	sink := &recordingSink{}
	loop := NewLoop(mgr, run, sink, LoopOptions{ParentID: "roadmap", RepoRoot: "/repo", Model: "openai/gpt-5.3-codex"})

	_, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}

	runnerStarted := eventsByType(sink.events, contracts.EventTypeRunnerStarted)
	if len(runnerStarted) != 2 {
		t.Fatalf("expected two runner_started events, got %d", len(runnerStarted))
	}

	if runnerStarted[0].Metadata["log_path"] != "/repo/runner-logs/epic-1/t-1/opencode/t-1.jsonl" {
		t.Fatalf("unexpected log path for first task, got %#v", runnerStarted[0].Metadata)
	}
	if runnerStarted[1].Metadata["log_path"] != "/repo/runner-logs/epic-2/t-2/opencode/t-2.jsonl" {
		t.Fatalf("unexpected log path for second task, got %#v", runnerStarted[1].Metadata)
	}
}

func TestLoopEmitsRunnerFinishedMetadataWithStallDiagnostics(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{{
		Status: contracts.RunnerResultBlocked,
		Reason: "opencode stall category=question",
		Artifacts: map[string]string{
			"log_path":        "/repo/runner-logs/opencode/t-1.jsonl",
			"stall_category":  "question",
			"session_id":      "ses_abc123",
			"last_output_age": "31s",
		},
	}}}
	sink := &recordingSink{}
	loop := NewLoop(mgr, run, sink, LoopOptions{ParentID: "root", RepoRoot: "/repo", Model: "openai/gpt-5.3-codex"})

	_, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}

	event, ok := findEventByType(sink.events, contracts.EventTypeRunnerFinished)
	if !ok {
		t.Fatalf("expected runner_finished event")
	}
	if event.Metadata["status"] != string(contracts.RunnerResultBlocked) {
		t.Fatalf("expected status metadata, got %#v", event.Metadata)
	}
	if event.Metadata["reason"] != "opencode stall category=question" {
		t.Fatalf("expected reason metadata, got %#v", event.Metadata)
	}
	if event.Metadata["stall_category"] != "question" {
		t.Fatalf("expected stall_category metadata, got %#v", event.Metadata)
	}
	if event.Metadata["session_id"] != "ses_abc123" {
		t.Fatalf("expected session_id metadata, got %#v", event.Metadata)
	}
	if event.Metadata["last_output_age"] != "31s" {
		t.Fatalf("expected last_output_age metadata, got %#v", event.Metadata)
	}
}

func TestLoopEmitsRunnerProgressEventsFromRunnerCallback(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{{Status: contracts.RunnerResultCompleted}}, progressEvents: []contracts.RunnerProgress{{Type: "runner_cmd_started", Message: "cmd start"}, {Type: "runner_output", Message: "line output"}, {Type: "runner_cmd_finished", Message: "cmd finish"}, {Type: "runner_warning", Message: "stall warning"}}}
	sink := &recordingSink{}
	loop := NewLoop(mgr, run, sink, LoopOptions{ParentID: "root"})

	_, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	startedEvents := eventsByType(sink.events, contracts.EventTypeRunnerCommandStarted)
	outputEvents := eventsByType(sink.events, contracts.EventTypeRunnerOutput)
	finishedEvents := eventsByType(sink.events, contracts.EventTypeRunnerCommandFinished)
	warningEvents := eventsByType(sink.events, contracts.EventTypeRunnerWarning)
	if len(startedEvents) != 1 || len(outputEvents) != 1 || len(finishedEvents) != 1 || len(warningEvents) != 1 {
		t.Fatalf("expected one event for each progress category, got started=%d output=%d finished=%d warning=%d", len(startedEvents), len(outputEvents), len(finishedEvents), len(warningEvents))
	}
	if startedEvents[0].Message != "cmd start" || outputEvents[0].Message != "line output" || finishedEvents[0].Message != "cmd finish" || warningEvents[0].Message != "stall warning" {
		t.Fatalf("unexpected progress message mapping")
	}
}

func TestLoopEmitsRunnerHeartbeatDuringLongRun(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{{Status: contracts.RunnerResultCompleted}}, runDelay: 25 * time.Millisecond}
	sink := &recordingSink{}
	loop := NewLoop(mgr, run, sink, LoopOptions{ParentID: "root", HeartbeatInterval: 5 * time.Millisecond, NoOutputWarningAfter: 100 * time.Millisecond})

	_, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	heartbeats := eventsByType(sink.events, contracts.EventTypeRunnerHeartbeat)
	if len(heartbeats) == 0 {
		t.Fatalf("expected heartbeat events during long run")
	}
}

func TestLoopEmitsRunnerWarningWhenNoOutputThresholdExceeded(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{{Status: contracts.RunnerResultCompleted}}, runDelay: 30 * time.Millisecond}
	sink := &recordingSink{}
	loop := NewLoop(mgr, run, sink, LoopOptions{ParentID: "root", HeartbeatInterval: 5 * time.Millisecond, NoOutputWarningAfter: 10 * time.Millisecond})

	_, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	warnings := eventsByType(sink.events, contracts.EventTypeRunnerWarning)
	if len(warnings) == 0 {
		t.Fatalf("expected warning events when no output threshold exceeded")
	}
}

func TestLoopEmitsTaskDataUpdatedEventForBlockedTriage(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{{Status: contracts.RunnerResultBlocked, Reason: "needs token"}}}
	sink := &recordingSink{}
	loop := NewLoop(mgr, run, sink, LoopOptions{ParentID: "root"})

	_, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}

	events := eventsByType(sink.events, contracts.EventTypeTaskDataUpdated)
	if len(events) != 1 {
		t.Fatalf("expected one task_data_updated event, got %d", len(events))
	}
	if events[0].Metadata["triage_status"] != "blocked" {
		t.Fatalf("expected triage_status=blocked, got %#v", events[0].Metadata)
	}
	if events[0].Metadata["triage_reason"] != "needs token" {
		t.Fatalf("expected triage_reason in metadata, got %#v", events[0].Metadata)
	}
}

func TestLoopEmitsTaskDataUpdatedEventForFailedTriage(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{{Status: contracts.RunnerResultFailed, Reason: "lint failed"}}}
	sink := &recordingSink{}
	loop := NewLoop(mgr, run, sink, LoopOptions{ParentID: "root", MaxRetries: 0})

	_, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}

	events := eventsByType(sink.events, contracts.EventTypeTaskDataUpdated)
	if len(events) != 1 {
		t.Fatalf("expected one task_data_updated event, got %d", len(events))
	}
	if events[0].Metadata["triage_status"] != "failed" {
		t.Fatalf("expected triage_status=failed, got %#v", events[0].Metadata)
	}
	if events[0].Metadata["triage_reason"] != "lint failed" {
		t.Fatalf("expected triage_reason in metadata, got %#v", events[0].Metadata)
	}
}

func TestLoopEmitsTaskFinishedMetadataForFailedTriage(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{{Status: contracts.RunnerResultFailed, Reason: "lint failed"}}}
	sink := &recordingSink{}
	loop := NewLoop(mgr, run, sink, LoopOptions{ParentID: "root", MaxRetries: 0})

	_, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}

	finished := eventsByType(sink.events, contracts.EventTypeTaskFinished)
	if len(finished) != 1 {
		t.Fatalf("expected one task_finished event, got %d", len(finished))
	}
	if finished[0].Message != string(contracts.TaskStatusFailed) {
		t.Fatalf("expected task_finished message=failed, got %q", finished[0].Message)
	}
	if finished[0].Metadata["triage_status"] != "failed" {
		t.Fatalf("expected triage_status=failed on task_finished metadata, got %#v", finished[0].Metadata)
	}
	if finished[0].Metadata["triage_reason"] != "lint failed" {
		t.Fatalf("expected triage_reason=lint failed on task_finished metadata, got %#v", finished[0].Metadata)
	}
}

func TestLoopEmitsReviewFeedbackMetadataOnFailedReview(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{
		{Status: contracts.RunnerResultCompleted},
		{
			Status:      contracts.RunnerResultCompleted,
			ReviewReady: false,
			Artifacts: map[string]string{
				"review_verdict":       "fail",
				"review_fail_feedback": "missing e2e assertion for retry path",
			},
		},
	}}
	sink := &recordingSink{}
	loop := NewLoop(mgr, run, sink, LoopOptions{ParentID: "root", MaxRetries: 0, RequireReview: true})

	_, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}

	updates := eventsByType(sink.events, contracts.EventTypeTaskDataUpdated)
	if len(updates) != 1 {
		t.Fatalf("expected one task_data_updated event, got %d", len(updates))
	}
	if updates[0].Metadata["review_verdict"] != "fail" {
		t.Fatalf("expected review_verdict=fail in task_data_updated metadata, got %#v", updates[0].Metadata)
	}
	if updates[0].Metadata["review_fail_feedback"] != "missing e2e assertion for retry path" {
		t.Fatalf("expected review_fail_feedback in task_data_updated metadata, got %#v", updates[0].Metadata)
	}

	finished := eventsByType(sink.events, contracts.EventTypeTaskFinished)
	if len(finished) != 1 {
		t.Fatalf("expected one task_finished event, got %d", len(finished))
	}
	if finished[0].Metadata["review_verdict"] != "fail" {
		t.Fatalf("expected review_verdict=fail in task_finished metadata, got %#v", finished[0].Metadata)
	}
	if finished[0].Metadata["review_fail_feedback"] != "missing e2e assertion for retry path" {
		t.Fatalf("expected review_fail_feedback in task_finished metadata, got %#v", finished[0].Metadata)
	}
}

func TestLoopEmitsDecisionMetadataForReviewRetry(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{
		{Status: contracts.RunnerResultCompleted},
		{
			Status:      contracts.RunnerResultCompleted,
			ReviewReady: false,
			Artifacts: map[string]string{
				"review_verdict":       "fail",
				"review_fail_feedback": "add retry evidence to pass",
			},
		},
		{Status: contracts.RunnerResultCompleted},
		{Status: contracts.RunnerResultCompleted, ReviewReady: true},
	}}
	sink := &recordingSink{}
	loop := NewLoop(mgr, run, sink, LoopOptions{ParentID: "root", MaxRetries: 1, RequireReview: true})

	_, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}

	updates := eventsByType(sink.events, contracts.EventTypeTaskDataUpdated)
	if len(updates) != 1 {
		t.Fatalf("expected one task_data_updated event for retry decision, got %d", len(updates))
	}
	if updates[0].Metadata["decision"] != "retry" {
		t.Fatalf("expected decision=retry on retry update, got %#v", updates[0].Metadata)
	}
	if updates[0].Metadata["reason"] != "review rejected: add retry evidence to pass" {
		t.Fatalf("expected resolved retry reason, got %#v", updates[0].Metadata)
	}
}

func TestLoopEmitsDecisionMetadataForReviewFailure(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{
		{Status: contracts.RunnerResultCompleted},
		{
			Status:      contracts.RunnerResultCompleted,
			ReviewReady: false,
			Artifacts: map[string]string{
				"review_verdict":       "fail",
				"review_fail_feedback": "first reviewer blocker",
			},
		},
		{Status: contracts.RunnerResultCompleted},
		{
			Status:      contracts.RunnerResultCompleted,
			ReviewReady: false,
			Artifacts: map[string]string{
				"review_verdict":       "fail",
				"review_fail_feedback": "second reviewer blocker",
			},
		},
	}}
	sink := &recordingSink{}
	loop := NewLoop(mgr, run, sink, LoopOptions{ParentID: "root", MaxRetries: 1, RequireReview: true})

	_, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}

	updates := eventsByType(sink.events, contracts.EventTypeTaskDataUpdated)
	if len(updates) < 1 {
		t.Fatalf("expected at least one task_data_updated event on final failure, got %d", len(updates))
	}
	finalFailure := updates[len(updates)-1]
	if finalFailure.Metadata["decision"] != "failed" {
		t.Fatalf("expected decision=failed on final failure, got %#v", finalFailure.Metadata)
	}
	if finalFailure.Metadata["triage_status"] != "failed" {
		t.Fatalf("expected triage_status=failed on final failure, got %#v", finalFailure.Metadata)
	}

	finished := eventsByType(sink.events, contracts.EventTypeTaskFinished)
	if len(finished) != 1 {
		t.Fatalf("expected one task_finished event, got %d", len(finished))
	}
	if finished[0].Metadata["decision"] != "failed" {
		t.Fatalf("expected task_finished decision=failed, got %#v", finished[0].Metadata)
	}
	if finished[0].Metadata["reason"] != "review rejected: second reviewer blocker" {
		t.Fatalf("expected resolved failure reason, got %#v", finished[0].Metadata)
	}
}

func TestLoopHonorsConcurrencyLimit(t *testing.T) {
	mgr := newFakeTaskManager(
		contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen},
		contracts.Task{ID: "t-2", Title: "Task 2", Status: contracts.TaskStatusOpen},
		contracts.Task{ID: "t-3", Title: "Task 3", Status: contracts.TaskStatusOpen},
		contracts.Task{ID: "t-4", Title: "Task 4", Status: contracts.TaskStatusOpen},
	)
	run := &blockingRunner{
		release: make(chan struct{}),
	}
	loop := NewLoop(mgr, run, nil, LoopOptions{ParentID: "root", RequireReview: false, Concurrency: 2})

	resultCh := make(chan struct {
		summary contracts.LoopSummary
		err     error
	}, 1)
	go func() {
		summary, err := loop.Run(context.Background())
		resultCh <- struct {
			summary contracts.LoopSummary
			err     error
		}{summary: summary, err: err}
	}()

	deadline := time.After(2 * time.Second)
	for {
		if atomic.LoadInt32(&run.maxActive) >= 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("expected at least 2 concurrent executions, got %d", atomic.LoadInt32(&run.maxActive))
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}

	close(run.release)
	result := <-resultCh
	if result.err != nil {
		t.Fatalf("loop failed: %v", result.err)
	}
	if result.summary.Completed != 4 {
		t.Fatalf("expected all tasks completed, got %#v", result.summary)
	}
	if got := atomic.LoadInt32(&run.maxActive); got > 2 {
		t.Fatalf("expected max active executions <= 2, got %d", got)
	}
}

func TestLoopAutoConcurrencyFromDependencyGraph(t *testing.T) {
	storage := newSpyStorageBackend(
		[]contracts.Task{
			{ID: "root", Title: "Root", Status: contracts.TaskStatusClosed},
			{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen, ParentID: "root"},
			{ID: "t-2", Title: "Task 2", Status: contracts.TaskStatusOpen, ParentID: "root"},
			{ID: "t-3", Title: "Task 3", Status: contracts.TaskStatusOpen, ParentID: "root"},
			{ID: "t-4", Title: "Task 4", Status: contracts.TaskStatusOpen, ParentID: "root"},
		},
		[]contracts.TaskRelation{
			{FromID: "t-1", ToID: "root", Type: contracts.RelationDependsOn},
			{FromID: "t-2", ToID: "root", Type: contracts.RelationDependsOn},
			{FromID: "t-3", ToID: "root", Type: contracts.RelationDependsOn},
			{FromID: "t-4", ToID: "root", Type: contracts.RelationDependsOn},
		},
	)
	run := &fakeRunner{}

	engine := enginepkg.NewTaskEngine()
	manager := newStorageEngineTaskManager(storage, engine, "root")
	loop := NewLoop(manager, run, nil, LoopOptions{ParentID: "root", Concurrency: 0, DryRun: true})

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if got := summary.Skipped; got != 1 {
		t.Fatalf("expected exactly one task skipped in dry run, got %d", got)
	}
	if loop.options.Concurrency != 4 {
		t.Fatalf("expected auto concurrency to be 4, got %d", loop.options.Concurrency)
	}
}

func TestLoopRespectsConfiguredConcurrencyCapForAutoCalculation(t *testing.T) {
	storage := newSpyStorageBackend(
		[]contracts.Task{
			{ID: "root", Title: "Root", Status: contracts.TaskStatusClosed},
			{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen, ParentID: "root"},
			{ID: "t-2", Title: "Task 2", Status: contracts.TaskStatusOpen, ParentID: "root"},
			{ID: "t-3", Title: "Task 3", Status: contracts.TaskStatusOpen, ParentID: "root"},
			{ID: "t-4", Title: "Task 4", Status: contracts.TaskStatusOpen, ParentID: "root"},
		},
		[]contracts.TaskRelation{
			{FromID: "t-1", ToID: "root", Type: contracts.RelationDependsOn},
			{FromID: "t-2", ToID: "root", Type: contracts.RelationDependsOn},
			{FromID: "t-3", ToID: "root", Type: contracts.RelationDependsOn},
			{FromID: "t-4", ToID: "root", Type: contracts.RelationDependsOn},
		},
	)
	run := &fakeRunner{}

	engine := enginepkg.NewTaskEngine()
	manager := newStorageEngineTaskManager(storage, engine, "root")
	loop := NewLoop(manager, run, nil, LoopOptions{ParentID: "root", Concurrency: 2, DryRun: true})

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if got := summary.Skipped; got != 1 {
		t.Fatalf("expected exactly one task skipped in dry run, got %d", got)
	}
	if loop.options.Concurrency != 2 {
		t.Fatalf("expected configured cap to remain 2, got %d", loop.options.Concurrency)
	}
}

func TestLoopDefaultsToAutoConcurrencyWhenNoLimitProvided(t *testing.T) {
	storage := newSpyStorageBackend(
		[]contracts.Task{
			{ID: "root", Title: "Root", Status: contracts.TaskStatusClosed},
			{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen, ParentID: "root"},
			{ID: "t-2", Title: "Task 2", Status: contracts.TaskStatusOpen, ParentID: "root"},
			{ID: "t-3", Title: "Task 3", Status: contracts.TaskStatusOpen, ParentID: "root"},
			{ID: "t-4", Title: "Task 4", Status: contracts.TaskStatusOpen, ParentID: "root"},
		},
		[]contracts.TaskRelation{
			{FromID: "t-1", ToID: "root", Type: contracts.RelationDependsOn},
			{FromID: "t-2", ToID: "root", Type: contracts.RelationDependsOn},
			{FromID: "t-3", ToID: "root", Type: contracts.RelationDependsOn},
			{FromID: "t-4", ToID: "root", Type: contracts.RelationDependsOn},
		},
	)
	run := &fakeRunner{}

	engine := enginepkg.NewTaskEngine()
	manager := newStorageEngineTaskManager(storage, engine, "root")
	loop := NewLoop(manager, run, nil, LoopOptions{ParentID: "root", DryRun: true})

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if got := summary.Skipped; got != 1 {
		t.Fatalf("expected exactly one task skipped in dry run, got %d", got)
	}
	if loop.options.Concurrency != 4 {
		t.Fatalf("expected auto concurrency to default to 4, got %d", loop.options.Concurrency)
	}
}

func TestLoopSkipsExecutionWhenTaskLockDenied(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{{Status: contracts.RunnerResultCompleted}}}
	loop := NewLoop(mgr, run, nil, LoopOptions{ParentID: "root"})
	loop.taskLock = &denyTaskLock{}

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if summary.TotalProcessed() != 0 {
		t.Fatalf("expected no processed tasks, got %#v", summary)
	}
	if len(run.requests) != 0 {
		t.Fatalf("runner should not be called when task lock is denied")
	}
}

func TestLoopUsesLandingLockAroundMergeAndPush(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{{Status: contracts.RunnerResultCompleted}}}
	vcs := &fakeVCS{}
	landing := &recordingLandingLock{}
	loop := NewLoop(mgr, run, nil, LoopOptions{ParentID: "root", VCS: vcs, MergeOnSuccess: true})
	loop.landingLock = landing

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if summary.Completed != 1 {
		t.Fatalf("expected one completed task, got %#v", summary)
	}
	if landing.lockCalls != 1 || landing.unlockCalls != 1 {
		t.Fatalf("expected one lock/unlock pair, got lock=%d unlock=%d", landing.lockCalls, landing.unlockCalls)
	}
}

func TestLoopEmitsLandingQueueLifecycleEventsOnAutoLandSuccess(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{{Status: contracts.RunnerResultCompleted}, {Status: contracts.RunnerResultCompleted, ReviewReady: true}}}
	vcs := &fakeVCS{commitSHA: "deadbeef"}
	sink := &recordingSink{}
	loop := NewLoop(mgr, run, sink, LoopOptions{ParentID: "root", VCS: vcs, MergeOnSuccess: true, RequireReview: true})

	_, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}

	updates := eventsByType(sink.events, contracts.EventTypeTaskDataUpdated)
	if !hasLandingStatus(updates, "queued") {
		t.Fatalf("expected landing queued update, got %#v", updates)
	}
	if !hasLandingStatus(updates, "landing") {
		t.Fatalf("expected landing in-progress update, got %#v", updates)
	}
	if !hasLandingStatus(updates, "landed") {
		t.Fatalf("expected landing landed update, got %#v", updates)
	}
	if !hasMetadataValue(updates, "auto_commit_sha", "deadbeef") {
		t.Fatalf("expected landing updates to include auto_commit_sha, got %#v", updates)
	}
	if !hasEventType(sink.events, contracts.EventTypeMergeCompleted) {
		t.Fatalf("expected merge_completed event")
	}
	if mergeEvent, ok := findEventByType(sink.events, contracts.EventTypeMergeCompleted); !ok || mergeEvent.Metadata["auto_commit_sha"] != "deadbeef" {
		t.Fatalf("expected merge event auto_commit_sha=deadbeef, got %#v", mergeEvent)
	}
	if !hasEventType(sink.events, contracts.EventTypePushCompleted) {
		t.Fatalf("expected push_completed event")
	}
	if pushEvent, ok := findEventByType(sink.events, contracts.EventTypePushCompleted); !ok || pushEvent.Metadata["auto_commit_sha"] != "deadbeef" {
		t.Fatalf("expected push event auto_commit_sha=deadbeef, got %#v", pushEvent)
	}
	if !hasEventType(sink.events, contracts.EventTypeMergeQueued) {
		t.Fatalf("expected merge_queued event")
	}
	if !hasEventType(sink.events, contracts.EventTypeMergeLanded) {
		t.Fatalf("expected merge_landed event")
	}
	queuedIndex := indexOfEventType(sink.events, contracts.EventTypeMergeQueued)
	landedIndex := indexOfEventType(sink.events, contracts.EventTypeMergeLanded)
	if queuedIndex == -1 || landedIndex == -1 || queuedIndex >= landedIndex {
		t.Fatalf("expected merge_queued before merge_landed, got events=%#v", sink.events)
	}
}

func TestLoopMarksLandingQueueBlockedOnMergeFailure(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{{Status: contracts.RunnerResultCompleted}, {Status: contracts.RunnerResultCompleted, ReviewReady: true}}}
	vcs := &fakeVCS{mergeErrs: []error{errors.New("landing failure first"), errors.New("landing failure second")}}
	sink := &recordingSink{}
	loop := NewLoop(mgr, run, sink, LoopOptions{ParentID: "root", VCS: vcs, MergeOnSuccess: true, RequireReview: true})

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("expected merge conflict to block task without failing loop, got %v", err)
	}
	if summary.Blocked != 1 {
		t.Fatalf("expected blocked summary count, got %#v", summary)
	}
	if vcs.mergeCalls != 2 {
		t.Fatalf("expected one retry with two merge attempts, got %d", vcs.mergeCalls)
	}
	if mgr.statusByID["t-1"] != contracts.TaskStatusBlocked {
		t.Fatalf("expected blocked task status, got %s", mgr.statusByID["t-1"])
	}
	if got := mgr.dataByID["t-1"]["triage_status"]; got != "blocked" {
		t.Fatalf("expected triage_status=blocked, got %q", got)
	}
	if got := mgr.dataByID["t-1"]["triage_reason"]; !strings.Contains(got, "landing failure second") {
		t.Fatalf("expected triage reason with final conflict, got %q", got)
	}
	if got := mgr.dataByID["t-1"]["auto_commit_sha"]; got != "abc123" {
		t.Fatalf("expected auto_commit_sha=abc123 in blocked data, got %q", got)
	}

	updates := eventsByType(sink.events, contracts.EventTypeTaskDataUpdated)
	if !hasLandingStatus(updates, "retrying") {
		t.Fatalf("expected retrying landing update, got %#v", updates)
	}
	if !hasLandingStatus(updates, "blocked") {
		t.Fatalf("expected blocked landing update, got %#v", updates)
	}
	if !hasEventType(sink.events, contracts.EventTypeMergeRetry) {
		t.Fatalf("expected merge_retry event")
	}
	if !hasEventType(sink.events, contracts.EventTypeMergeBlocked) {
		t.Fatalf("expected merge_blocked event")
	}
	if hasEventType(sink.events, contracts.EventTypeMergeLanded) {
		t.Fatalf("did not expect merge_landed event on blocked landing")
	}
}

func TestLoopEmitsDecisionMetadataForLandingRetryAndBlocked(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{{Status: contracts.RunnerResultCompleted}, {Status: contracts.RunnerResultCompleted, ReviewReady: true}}}
	vcs := &fakeVCS{mergeErrs: []error{errors.New("landing failure first"), errors.New("landing failure second")}}
	sink := &recordingSink{}
	loop := NewLoop(mgr, run, sink, LoopOptions{ParentID: "root", VCS: vcs, MergeOnSuccess: true, RequireReview: true})

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("expected merge conflict to block task without failing loop, got %v", err)
	}
	if summary.Blocked != 1 {
		t.Fatalf("expected blocked summary count, got %#v", summary)
	}

	mergeRetry, ok := findEventByType(sink.events, contracts.EventTypeMergeRetry)
	if !ok {
		t.Fatalf("expected merge_retry event")
	}
	if mergeRetry.Metadata["decision"] != "retry" {
		t.Fatalf("expected decision=retry on merge_retry event, got %#v", mergeRetry.Metadata)
	}
	if !strings.Contains(mergeRetry.Metadata["reason"], "landing failure first") {
		t.Fatalf("expected retry reason on merge_retry event, got %#v", mergeRetry.Metadata)
	}

	mergeBlocked, ok := findEventByType(sink.events, contracts.EventTypeMergeBlocked)
	if !ok {
		t.Fatalf("expected merge_blocked event")
	}
	if mergeBlocked.Metadata["decision"] != "blocked" {
		t.Fatalf("expected decision=blocked on merge_blocked event, got %#v", mergeBlocked.Metadata)
	}
	if !strings.Contains(mergeBlocked.Metadata["reason"], "landing failure second") {
		t.Fatalf("expected blocked reason on merge_blocked event, got %#v", mergeBlocked.Metadata)
	}

	updates := eventsByType(sink.events, contracts.EventTypeTaskDataUpdated)
	if len(updates) == 0 {
		t.Fatalf("expected task_data_updated events for landing")
	}
	foundBlocked := false
	for _, update := range updates {
		if update.Metadata["triage_status"] != "blocked" {
			continue
		}
		foundBlocked = true
		if update.Metadata["decision"] != "blocked" {
			t.Fatalf("expected decision=blocked on blocked task_data_updated event, got %#v", update.Metadata)
		}
		if !strings.Contains(update.Metadata["reason"], "landing failure second") {
			t.Fatalf("expected blocked task reason in task_data_updated, got %#v", update.Metadata)
		}
	}
	if !foundBlocked {
		t.Fatalf("expected blocked task_data_updated event with triage_status=blocked, got %#v", updates)
	}
}

func TestLoopEmitsDecisionMetadataForLandingLanded(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{{Status: contracts.RunnerResultCompleted}, {Status: contracts.RunnerResultCompleted, ReviewReady: true}}}
	vcs := &fakeVCS{commitSHA: "deadbeef"}
	sink := &recordingSink{}
	loop := NewLoop(mgr, run, sink, LoopOptions{ParentID: "root", VCS: vcs, MergeOnSuccess: true, RequireReview: true})

	_, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}

	mergeLanded, ok := findEventByType(sink.events, contracts.EventTypeMergeLanded)
	if !ok {
		t.Fatalf("expected merge_landed event")
	}
	if mergeLanded.Metadata["decision"] != "landed" {
		t.Fatalf("expected decision=landed on merge_landed event, got %#v", mergeLanded.Metadata)
	}

	updates := eventsByType(sink.events, contracts.EventTypeTaskDataUpdated)
	foundLanded := false
	for _, update := range updates {
		if update.Metadata["landing_status"] != "landed" {
			continue
		}
		foundLanded = true
		if update.Metadata["decision"] != "landed" {
			t.Fatalf("expected landing_status landed update to include decision=landed, got %#v", update.Metadata)
		}
	}
	if !foundLanded {
		t.Fatalf("expected landing_status=landed task_data_updated event, got %#v", updates)
	}
}

func TestLoopAutoLandRetriesOnceThenSucceeds(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{{Status: contracts.RunnerResultCompleted}, {Status: contracts.RunnerResultCompleted, ReviewReady: true}}}
	vcs := &fakeVCS{mergeErrs: []error{errors.New("temporary merge failure"), nil}}
	sink := &recordingSink{}
	loop := NewLoop(mgr, run, sink, LoopOptions{ParentID: "root", VCS: vcs, MergeOnSuccess: true, RequireReview: true})

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if summary.Completed != 1 {
		t.Fatalf("expected completed summary after retry, got %#v", summary)
	}
	if vcs.mergeCalls != 2 {
		t.Fatalf("expected one retry with two merge attempts, got %d", vcs.mergeCalls)
	}
	if mgr.statusByID["t-1"] != contracts.TaskStatusClosed {
		t.Fatalf("expected closed task status, got %s", mgr.statusByID["t-1"])
	}
	updates := eventsByType(sink.events, contracts.EventTypeTaskDataUpdated)
	if !hasLandingStatus(updates, "retrying") {
		t.Fatalf("expected retrying landing update, got %#v", updates)
	}
	if !hasLandingStatus(updates, "landed") {
		t.Fatalf("expected landed landing update, got %#v", updates)
	}
	if !hasEventType(sink.events, contracts.EventTypeMergeRetry) {
		t.Fatalf("expected merge_retry event")
	}
	if !hasEventType(sink.events, contracts.EventTypeMergeLanded) {
		t.Fatalf("expected merge_landed event")
	}
	if indexOfEventType(sink.events, contracts.EventTypeMergeRetry) >= indexOfEventType(sink.events, contracts.EventTypeMergeLanded) {
		t.Fatalf("expected merge_retry before merge_landed, got events=%#v", sink.events)
	}
}

func TestLoopRunsMergeConflictRemediationBeforeLandingRetry(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{
		{Status: contracts.RunnerResultCompleted},
		{Status: contracts.RunnerResultCompleted, ReviewReady: true},
		{Status: contracts.RunnerResultCompleted},
	}}
	vcs := &fakeVCS{mergeErrs: []error{errors.New("git merge --no-ff task/t-1 failed: CONFLICT (content): Merge conflict"), nil}}
	sink := &recordingSink{}
	loop := NewLoop(mgr, run, sink, LoopOptions{ParentID: "root", VCS: vcs, MergeOnSuccess: true, RequireReview: true})

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if summary.Completed != 1 {
		t.Fatalf("expected task to complete after remediation retry, got %#v", summary)
	}
	if vcs.mergeCalls != 2 {
		t.Fatalf("expected two merge attempts, got %d", vcs.mergeCalls)
	}
	if len(run.modes) != 3 {
		t.Fatalf("expected implement+review+remediation implement runs, got %d", len(run.modes))
	}
	if run.modes[2] != contracts.RunnerModeImplement {
		t.Fatalf("expected remediation mode implement, got %s", run.modes[2])
	}
	if !strings.Contains(run.requests[2].Prompt, "Landing Merge Remediation:") {
		t.Fatalf("expected merge remediation prompt, got %q", run.requests[2].Prompt)
	}
	if !strings.Contains(run.requests[2].Prompt, "Merge Failure Details:") {
		t.Fatalf("expected merge failure details in remediation prompt, got %q", run.requests[2].Prompt)
	}
	if !hasEventType(sink.events, contracts.EventTypeMergeRetry) {
		t.Fatalf("expected merge_retry event")
	}
	if !hasEventType(sink.events, contracts.EventTypeMergeLanded) {
		t.Fatalf("expected merge_landed event")
	}
}

func TestLoopBlocksTaskWhenMergeConflictRemediationFails(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{
		{Status: contracts.RunnerResultCompleted},
		{Status: contracts.RunnerResultCompleted, ReviewReady: true},
		{Status: contracts.RunnerResultFailed, Reason: "unable to resolve conflicts automatically"},
	}}
	vcs := &fakeVCS{mergeErrs: []error{errors.New("git merge --no-ff task/t-1 failed: CONFLICT (content): Merge conflict")}}
	sink := &recordingSink{}
	loop := NewLoop(mgr, run, sink, LoopOptions{ParentID: "root", VCS: vcs, MergeOnSuccess: true, RequireReview: true})

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if summary.Blocked != 1 {
		t.Fatalf("expected blocked summary after remediation failure, got %#v", summary)
	}
	if vcs.mergeCalls != 1 {
		t.Fatalf("expected no second merge attempt after remediation failure, got %d", vcs.mergeCalls)
	}
	if mgr.statusByID["t-1"] != contracts.TaskStatusBlocked {
		t.Fatalf("expected blocked status, got %s", mgr.statusByID["t-1"])
	}
	if got := mgr.dataByID["t-1"]["triage_reason"]; !strings.Contains(got, "merge conflict remediation failed") {
		t.Fatalf("expected remediation failure triage reason, got %q", got)
	}
	if hasEventType(sink.events, contracts.EventTypeMergeLanded) {
		t.Fatalf("did not expect merge_landed on remediation failure")
	}
}

func TestLoopStartsFixedWorkerPool(t *testing.T) {
	mgr := newFakeTaskManager(
		contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen},
		contracts.Task{ID: "t-2", Title: "Task 2", Status: contracts.TaskStatusOpen},
		contracts.Task{ID: "t-3", Title: "Task 3", Status: contracts.TaskStatusOpen},
		contracts.Task{ID: "t-4", Title: "Task 4", Status: contracts.TaskStatusOpen},
	)
	run := &blockingRunner{release: make(chan struct{})}
	loop := NewLoop(mgr, run, nil, LoopOptions{ParentID: "root", Concurrency: 3})

	startedWorkers := make(chan int, 4)
	loop.workerStartHook = func(workerID int) {
		startedWorkers <- workerID
	}

	resultCh := make(chan error, 1)
	go func() {
		_, err := loop.Run(context.Background())
		resultCh <- err
	}()

	gotWorkers := map[int]struct{}{}
	deadline := time.After(2 * time.Second)
	for len(gotWorkers) < 3 {
		select {
		case workerID := <-startedWorkers:
			gotWorkers[workerID] = struct{}{}
		case <-deadline:
			t.Fatalf("expected 3 workers to start, got %d", len(gotWorkers))
		}
	}

	select {
	case workerID := <-startedWorkers:
		t.Fatalf("expected fixed worker pool size 3, saw extra worker %d", workerID)
	case <-time.After(50 * time.Millisecond):
	}

	close(run.release)
	if err := <-resultCh; err != nil {
		t.Fatalf("loop failed: %v", err)
	}
}

func TestLoopUsesIsolatedClonePerTaskAndCleansUp(t *testing.T) {
	mgr := newFakeTaskManager(
		contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen},
		contracts.Task{ID: "t-2", Title: "Task 2", Status: contracts.TaskStatusOpen},
	)
	run := &repoRecordingRunner{}
	cloneMgr := newFakeCloneManager()
	loop := NewLoop(mgr, run, nil, LoopOptions{ParentID: "root", RepoRoot: "/repo", Concurrency: 2})
	loop.cloneManager = cloneMgr

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if summary.Completed != 2 {
		t.Fatalf("expected two completed tasks, got %#v", summary)
	}

	repoRoots := run.RepoRootsByTask()
	if len(repoRoots) != 2 {
		t.Fatalf("expected runner requests for both tasks, got %#v", repoRoots)
	}
	if repoRoots["t-1"] == repoRoots["t-2"] {
		t.Fatalf("expected isolated clone path per task, got shared path %q", repoRoots["t-1"])
	}

	if cloneMgr.CleanupCount() != 2 {
		t.Fatalf("expected clone cleanup for each task, got %d", cloneMgr.CleanupCount())
	}
}

func TestLoopUsesRealCloneManagerForParallelTasksAndCleansUpWorktrees(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}

	repoRoot := t.TempDir()
	runGit(t, repoRoot, "init")
	readmePath := filepath.Join(repoRoot, "README.md")
	if err := os.WriteFile(readmePath, []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runGit(t, repoRoot, "add", "README.md")
	runGit(t, repoRoot, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "init")

	cloneBase := t.TempDir()
	cloneManager := NewGitCloneManager(cloneBase)
	runner := &parallelBlockingRepoRunner{
		expected:   2,
		startedAll: make(chan struct{}),
		release:    make(chan struct{}),
	}
	loop := NewLoop(newFakeTaskManager(
		contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen},
		contracts.Task{ID: "t-2", Title: "Task 2", Status: contracts.TaskStatusOpen},
	), runner, nil, LoopOptions{
		ParentID:     "root",
		RepoRoot:     repoRoot,
		Concurrency:  2,
		CloneManager: cloneManager,
	})

	type loopResult struct {
		summary contracts.LoopSummary
		err     error
	}
	resultCh := make(chan loopResult, 1)
	go func() {
		summary, err := loop.Run(context.Background())
		resultCh <- loopResult{summary: summary, err: err}
	}()

	select {
	case <-runner.startedAll:
	case <-time.After(5 * time.Second):
		close(runner.release)
		result := <-resultCh
		t.Fatalf("timed out waiting for parallel task starts: summary=%#v err=%v", result.summary, result.err)
	}

	repoRoots := runner.RepoRootsByTask()
	if len(repoRoots) != 2 {
		close(runner.release)
		_ = <-resultCh
		t.Fatalf("expected two repo roots, got %#v", repoRoots)
	}
	if repoRoots["t-1"] == repoRoots["t-2"] {
		close(runner.release)
		_ = <-resultCh
		t.Fatalf("expected isolated clone path per task, got shared path %q", repoRoots["t-1"])
	}
	for taskID, root := range repoRoots {
		if _, err := os.Stat(root); err != nil {
			close(runner.release)
			_ = <-resultCh
			t.Fatalf("expected live clone for %s at %q: %v", taskID, root, err)
		}
		if root == repoRoot {
			close(runner.release)
			_ = <-resultCh
			t.Fatalf("expected task clone path, got source repo root for %s", taskID)
		}
		if _, err := os.Stat(filepath.Join(root, "README.md")); err != nil {
			close(runner.release)
			_ = <-resultCh
			t.Fatalf("expected cloned file for %s at %q: %v", taskID, root, err)
		}
		if filepath.Dir(root) != filepath.Clean(cloneBase) {
			close(runner.release)
			_ = <-resultCh
			t.Fatalf("expected clone under %q, got %q", cloneBase, root)
		}
	}

	close(runner.release)
	result := <-resultCh
	if result.err != nil {
		t.Fatalf("loop failed: %v", result.err)
	}
	if result.summary.Completed != 2 {
		t.Fatalf("expected two completed tasks, got %#v", result.summary)
	}
	for _, root := range repoRoots {
		if _, err := os.Stat(root); !os.IsNotExist(err) {
			t.Fatalf("expected clone path cleaned up, got %v for %q", err, root)
		}
	}
	entries, err := os.ReadDir(cloneBase)
	if err != nil {
		t.Fatalf("read clone base: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected temporary clone base directory to be empty, got %d entries", len(entries))
	}
}

func TestLoopUsesCloneScopedVCSFactoryForTaskBranchingAndLanding(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{
		{Status: contracts.RunnerResultCompleted},
		{Status: contracts.RunnerResultCompleted, ReviewReady: true},
	}}
	rootVCS := &fakeVCS{}
	cloneVCS := &fakeVCS{}
	cloneMgr := newFakeCloneManager()

	var observedRoots []string
	var rootsMu sync.Mutex
	loop := NewLoop(mgr, run, nil, LoopOptions{
		ParentID:       "root",
		RepoRoot:       "/repo",
		RequireReview:  true,
		MergeOnSuccess: true,
		VCS:            rootVCS,
		VCSFactory: func(repoRoot string) contracts.VCS {
			rootsMu.Lock()
			observedRoots = append(observedRoots, repoRoot)
			rootsMu.Unlock()
			return cloneVCS
		},
	})
	loop.cloneManager = cloneMgr

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if summary.Completed != 1 {
		t.Fatalf("expected one completed task, got %#v", summary)
	}
	if len(rootVCS.calls) != 0 {
		t.Fatalf("expected root VCS to be bypassed, got calls %v", rootVCS.calls)
	}
	if !containsCall(cloneVCS.calls, "create_branch:t-1") {
		t.Fatalf("expected clone-scoped branch creation, got %v", cloneVCS.calls)
	}
	if !containsCall(cloneVCS.calls, "merge_to_main:task/t-1") {
		t.Fatalf("expected clone-scoped landing merge, got %v", cloneVCS.calls)
	}
	if !containsCall(cloneVCS.calls, "push_main") {
		t.Fatalf("expected clone-scoped landing push, got %v", cloneVCS.calls)
	}

	rootsMu.Lock()
	defer rootsMu.Unlock()
	if len(observedRoots) == 0 {
		t.Fatalf("expected VCS factory to be invoked")
	}
	if observedRoots[0] != "/tmp/clone/t-1" {
		t.Fatalf("expected clone-scoped repo root, got %q", observedRoots[0])
	}
}

func TestLoopResumesPersistedSchedulerStateAndDoesNotRerunCompletedTask(t *testing.T) {
	tempDir := t.TempDir()
	statePath := filepath.Join(tempDir, "scheduler-state.json")

	mgr := newFakeTaskManager(
		contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen},
		contracts.Task{ID: "t-2", Title: "Task 2", Status: contracts.TaskStatusOpen},
		contracts.Task{ID: "t-3", Title: "Task 3", Status: contracts.TaskStatusOpen},
	)
	mgr.dependsOn = map[string][]string{
		"t-3": {"t-1", "t-2"},
	}
	mgr.failStatusOnce = map[string]error{
		"t-1|closed": errors.New("simulated interruption while closing task"),
	}

	firstRunRunner := &fakeRunner{results: []contracts.RunnerResult{{Status: contracts.RunnerResultCompleted}}}
	firstRunLoop := NewLoop(mgr, firstRunRunner, nil, LoopOptions{ParentID: "root", SchedulerStatePath: statePath})

	if _, err := firstRunLoop.Run(context.Background()); err == nil {
		t.Fatalf("expected first run to fail due to simulated interruption")
	}
	if len(firstRunRunner.requests) != 1 || firstRunRunner.requests[0].TaskID != "t-1" {
		t.Fatalf("expected first run to execute only t-1 before interruption, got %#v", firstRunRunner.requests)
	}

	secondRunRunner := &fakeRunner{results: []contracts.RunnerResult{
		{Status: contracts.RunnerResultCompleted},
		{Status: contracts.RunnerResultCompleted},
	}}
	secondRunLoop := NewLoop(mgr, secondRunRunner, nil, LoopOptions{ParentID: "root", SchedulerStatePath: statePath})

	summary, err := secondRunLoop.Run(context.Background())
	if err != nil {
		t.Fatalf("resume run failed: %v", err)
	}
	if summary.Completed != 2 {
		t.Fatalf("expected resume run to complete t-2 and t-3, got %#v", summary)
	}
	if len(secondRunRunner.requests) != 2 {
		t.Fatalf("expected exactly two resumed executions, got %d", len(secondRunRunner.requests))
	}
	if secondRunRunner.requests[0].TaskID != "t-2" || secondRunRunner.requests[1].TaskID != "t-3" {
		t.Fatalf("expected resumed order [t-2 t-3], got [%s %s]", secondRunRunner.requests[0].TaskID, secondRunRunner.requests[1].TaskID)
	}
}

func TestSchedulerStatePersistResume_HandlesInterruptionAndCorrectQueueContinuation(t *testing.T) {
	// Given restart after interruption
	tempDir := t.TempDir()
	statePath := filepath.Join(tempDir, "scheduler-state.json")

	// Setup tasks with dependencies to test queue continuation
	mgr := newFakeTaskManager(
		contracts.Task{ID: "task-1", Title: "Task 1", Status: contracts.TaskStatusOpen},
		contracts.Task{ID: "task-2", Title: "Task 2", Status: contracts.TaskStatusOpen},
		contracts.Task{ID: "task-3", Title: "Task 3", Status: contracts.TaskStatusOpen},
		contracts.Task{ID: "task-4", Title: "Task 4", Status: contracts.TaskStatusOpen},
	)
	mgr.dependsOn = map[string][]string{
		"task-3": {"task-1", "task-2"},
		"task-4": {"task-3"},
	}

	// Simulate interruption during task closing
	mgr.failStatusOnce = map[string]error{
		"task-1|closed": errors.New("simulated interruption while closing task"),
	}

	// First run: complete task-1, then get interrupted
	firstRunRunner := &fakeRunner{results: []contracts.RunnerResult{{Status: contracts.RunnerResultCompleted}}}
	firstRunLoop := NewLoop(mgr, firstRunRunner, nil, LoopOptions{
		ParentID:           "root",
		SchedulerStatePath: statePath,
		MaxTasks:           1, // Only process one task to ensure controlled scenario
	})

	_, err := firstRunLoop.Run(context.Background())
	if err == nil {
		t.Fatalf("expected first run to fail due to interruption")
	}

	// Verify first run state: task-1 should be persisted as completed
	stateStore := newSchedulerStateStore(statePath, "root")
	snapshot, err := stateStore.Load()
	if err != nil {
		t.Fatalf("failed to load scheduler state: %v", err)
	}

	// task-1 should be marked as completed in persisted state
	if _, exists := snapshot.Completed["task-1"]; !exists {
		t.Fatalf("expected task-1 to be persisted as completed, got completed=%v, in-flight=%v",
			snapshot.Completed, snapshot.InFlight)
	}

	// When resuming after restart
	secondRunRunner := &fakeRunner{results: []contracts.RunnerResult{
		{Status: contracts.RunnerResultCompleted}, // task-2 should complete
		{Status: contracts.RunnerResultCompleted}, // task-3 should complete
		{Status: contracts.RunnerResultCompleted}, // task-4 should complete
	}}
	secondRunLoop := NewLoop(mgr, secondRunRunner, nil, LoopOptions{
		ParentID:           "root",
		SchedulerStatePath: statePath,
		MaxRetries:         0,
	})

	// Then completed tasks are not re-run and queue continues correctly
	summary, err := secondRunLoop.Run(context.Background())
	if err != nil {
		t.Fatalf("resume run failed: %v", err)
	}

	// Should complete exactly 3 tasks (task-2, task-3, task-4) - task-1 should not be re-run
	if summary.Completed != 3 {
		t.Fatalf("expected 3 completed tasks (task-2, task-3, task-4), got %d", summary.Completed)
	}

	// Verify the correct tasks were executed in correct order
	if len(secondRunRunner.requests) != 3 {
		t.Fatalf("expected exactly 3 runner requests, got %d", len(secondRunRunner.requests))
	}

	expectedOrder := []string{"task-2", "task-3", "task-4"}
	for i, expected := range expectedOrder {
		if secondRunRunner.requests[i].TaskID != expected {
			t.Fatalf("expected task %d to be %s, got %s", i+1, expected, secondRunRunner.requests[i].TaskID)
		}
	}

	// Verify final state: all tasks should be closed
	if mgr.statusByID["task-1"] != contracts.TaskStatusClosed {
		t.Fatalf("expected task-1 to be closed, got %s", mgr.statusByID["task-1"])
	}
	if mgr.statusByID["task-2"] != contracts.TaskStatusClosed {
		t.Fatalf("expected task-2 to be closed, got %s", mgr.statusByID["task-2"])
	}
	if mgr.statusByID["task-3"] != contracts.TaskStatusClosed {
		t.Fatalf("expected task-3 to be closed, got %s", mgr.statusByID["task-3"])
	}
	if mgr.statusByID["task-4"] != contracts.TaskStatusClosed {
		t.Fatalf("expected task-4 to be closed, got %s", mgr.statusByID["task-4"])
	}
}

func TestSchedulerStatePersistResume_HandlesBlockedTasksCorrectly(t *testing.T) {
	// Given restart after interruption with blocked tasks
	tempDir := t.TempDir()
	statePath := filepath.Join(tempDir, "scheduler-state.json")

	mgr := newFakeTaskManager(
		contracts.Task{ID: "blocked-task", Title: "Blocked Task", Status: contracts.TaskStatusOpen},
		contracts.Task{ID: "normal-task", Title: "Normal Task", Status: contracts.TaskStatusOpen},
	)

	// Simulate interruption after blocking a task
	mgr.failStatusOnce = map[string]error{
		"blocked-task|blocked": errors.New("simulated interruption while blocking task"),
	}

	// First run: blocked-task gets blocked, then gets interrupted
	firstRunRunner := &fakeRunner{results: []contracts.RunnerResult{
		{Status: contracts.RunnerResultBlocked, Reason: "needs manual intervention"}, // blocked-task gets blocked
	}}
	firstRunLoop := NewLoop(mgr, firstRunRunner, nil, LoopOptions{
		ParentID:           "root",
		SchedulerStatePath: statePath,
		MaxTasks:           1, // Only process one task
	})

	_, err := firstRunLoop.Run(context.Background())
	if err == nil {
		t.Fatalf("expected first run to fail due to interruption")
	}

	// When resuming after restart
	secondRunRunner := &fakeRunner{results: []contracts.RunnerResult{
		{Status: contracts.RunnerResultCompleted}, // normal-task should complete
	}}
	secondRunLoop := NewLoop(mgr, secondRunRunner, nil, LoopOptions{
		ParentID:           "root",
		SchedulerStatePath: statePath,
		MaxRetries:         0,
	})

	// Then blocked tasks remain blocked and other tasks continue
	summary, err := secondRunLoop.Run(context.Background())
	if err != nil {
		t.Fatalf("resume run failed: %v", err)
	}

	// Should complete exactly 1 task (normal-task) - blocked-task should not be re-run
	if summary.Completed != 1 {
		t.Fatalf("expected 1 completed task (normal-task), got %d", summary.Completed)
	}

	// Verify blocked task remains blocked with correct triage data
	if mgr.statusByID["blocked-task"] != contracts.TaskStatusBlocked {
		t.Fatalf("expected blocked-task to remain blocked, got %s", mgr.statusByID["blocked-task"])
	}
	if mgr.dataByID["blocked-task"]["triage_status"] != "blocked" {
		t.Fatalf("expected blocked-task to have triage_status=blocked, got %v", mgr.dataByID["blocked-task"])
	}
	if mgr.dataByID["blocked-task"]["triage_reason"] != "needs manual intervention" {
		t.Fatalf("expected blocked-task to preserve triage_reason, got %v", mgr.dataByID["blocked-task"]["triage_reason"])
	}

	// Verify normal task was completed
	if mgr.statusByID["normal-task"] != contracts.TaskStatusClosed {
		t.Fatalf("expected normal-task to be closed, got %s", mgr.statusByID["normal-task"])
	}
}

func TestSchedulerStateStoreMergesInterleavedUpdates(t *testing.T) {
	tempDir := t.TempDir()
	statePath := filepath.Join(tempDir, "scheduler-state.json")
	store := newSchedulerStateStore(statePath, "root")

	firstSnapshot, err := store.Load()
	if err != nil {
		t.Fatalf("load first snapshot: %v", err)
	}
	secondSnapshot, err := store.Load()
	if err != nil {
		t.Fatalf("load second snapshot: %v", err)
	}

	firstSnapshot.InFlight["t-1"] = struct{}{}
	if err := store.Save(firstSnapshot); err != nil {
		t.Fatalf("save first snapshot: %v", err)
	}

	secondSnapshot.InFlight["t-2"] = struct{}{}
	if err := store.Save(secondSnapshot); err != nil {
		t.Fatalf("save second snapshot: %v", err)
	}

	merged, err := store.Load()
	if err != nil {
		t.Fatalf("load merged snapshot: %v", err)
	}

	if _, ok := merged.InFlight["t-1"]; !ok {
		t.Fatalf("expected t-1 to remain in in-flight set, got %#v", merged.InFlight)
	}
	if _, ok := merged.InFlight["t-2"]; !ok {
		t.Fatalf("expected t-2 in in-flight set, got %#v", merged.InFlight)
	}
}

func hasEventType(events []contracts.Event, eventType contracts.EventType) bool {
	for _, event := range events {
		if event.Type == eventType {
			return true
		}
	}
	return false
}

func findEventByType(events []contracts.Event, eventType contracts.EventType) (contracts.Event, bool) {
	for _, event := range events {
		if event.Type == eventType {
			return event, true
		}
	}
	return contracts.Event{}, false
}

func eventsByType(events []contracts.Event, eventType contracts.EventType) []contracts.Event {
	result := []contracts.Event{}
	for _, event := range events {
		if event.Type == eventType {
			result = append(result, event)
		}
	}
	return result
}

func indexOfEventType(events []contracts.Event, eventType contracts.EventType) int {
	for i, event := range events {
		if event.Type == eventType {
			return i
		}
	}
	return -1
}

func hasLandingStatus(events []contracts.Event, status string) bool {
	for _, event := range events {
		if event.Metadata["landing_status"] == status {
			return true
		}
	}
	return false
}

func hasMetadataValue(events []contracts.Event, key string, value string) bool {
	for _, event := range events {
		if event.Metadata[key] == value {
			return true
		}
	}
	return false
}

type recordingSink struct{ events []contracts.Event }

func (r *recordingSink) Emit(_ context.Context, event contracts.Event) error {
	r.events = append(r.events, event)
	return nil
}

type blockingRunner struct {
	release   chan struct{}
	active    int32
	maxActive int32
}

type repoRecordingRunner struct {
	mu       sync.Mutex
	byTaskID map[string]string
}

func (r *repoRecordingRunner) Run(_ context.Context, request contracts.RunnerRequest) (contracts.RunnerResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.byTaskID == nil {
		r.byTaskID = map[string]string{}
	}
	r.byTaskID[request.TaskID] = request.RepoRoot
	return contracts.RunnerResult{Status: contracts.RunnerResultCompleted}, nil
}

func (r *repoRecordingRunner) RepoRootsByTask() map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]string, len(r.byTaskID))
	for taskID, repoRoot := range r.byTaskID {
		out[taskID] = repoRoot
	}
	return out
}

type parallelBlockingRepoRunner struct {
	expected   int
	mu         sync.Mutex
	byTaskID   map[string]string
	started    int
	startedAll chan struct{}
	release    chan struct{}
}

func (r *parallelBlockingRepoRunner) Run(_ context.Context, request contracts.RunnerRequest) (contracts.RunnerResult, error) {
	r.mu.Lock()
	if r.byTaskID == nil {
		r.byTaskID = map[string]string{}
	}
	r.byTaskID[request.TaskID] = request.RepoRoot
	r.started++
	if r.started == r.expected {
		close(r.startedAll)
	}
	r.mu.Unlock()

	if r.release != nil {
		<-r.release
	}
	return contracts.RunnerResult{Status: contracts.RunnerResultCompleted}, nil
}

func (r *parallelBlockingRepoRunner) RepoRootsByTask() map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]string, len(r.byTaskID))
	for taskID, repoRoot := range r.byTaskID {
		out[taskID] = repoRoot
	}
	return out
}

type fakeCloneManager struct {
	mu          sync.Mutex
	cleanupByID map[string]int
}

func newFakeCloneManager() *fakeCloneManager {
	return &fakeCloneManager{cleanupByID: map[string]int{}}
}

func (f *fakeCloneManager) CloneForTask(_ context.Context, taskID string, _ string) (string, error) {
	return fmt.Sprintf("/tmp/clone/%s", taskID), nil
}

func (f *fakeCloneManager) Cleanup(taskID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cleanupByID[taskID]++
	return nil
}

func (f *fakeCloneManager) CleanupCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	total := 0
	for _, count := range f.cleanupByID {
		total += count
	}
	return total
}

type denyTaskLock struct{}

func (denyTaskLock) TryLock(string) bool { return false }

func (denyTaskLock) Unlock(string) {}

type recordingLandingLock struct {
	lockCalls   int
	unlockCalls int
}

func (l *recordingLandingLock) Lock() {
	l.lockCalls++
}

func (l *recordingLandingLock) Unlock() {
	l.unlockCalls++
}

func (b *blockingRunner) Run(_ context.Context, _ contracts.RunnerRequest) (contracts.RunnerResult, error) {
	active := atomic.AddInt32(&b.active, 1)
	for {
		maxActive := atomic.LoadInt32(&b.maxActive)
		if active <= maxActive {
			break
		}
		if atomic.CompareAndSwapInt32(&b.maxActive, maxActive, active) {
			break
		}
	}
	<-b.release
	atomic.AddInt32(&b.active, -1)
	return contracts.RunnerResult{Status: contracts.RunnerResultCompleted}, nil
}

func TestRunnerLogBackendDirSupportsClaude(t *testing.T) {
	if got := runnerLogBackendDir("claude"); got != "claude" {
		t.Fatalf("expected claude backend dir, got %q", got)
	}
}

type statusTransition struct {
	taskID string
	status contracts.TaskStatus
}

type spyStorageBackend struct {
	mu               sync.Mutex
	tasks            map[string]contracts.Task
	relations        []contracts.TaskRelation
	getTaskTreeCalls int
	setStatusCalls   []statusTransition
}

func newSpyStorageBackend(tasks []contracts.Task, relations []contracts.TaskRelation) *spyStorageBackend {
	byID := make(map[string]contracts.Task, len(tasks))
	for _, task := range tasks {
		byID[task.ID] = task
	}
	return &spyStorageBackend{tasks: byID, relations: append([]contracts.TaskRelation(nil), relations...)}
}

func (s *spyStorageBackend) GetTaskTree(_ context.Context, rootID string) (*contracts.TaskTree, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getTaskTreeCalls++
	root, ok := s.tasks[rootID]
	if !ok {
		return nil, fmt.Errorf("missing root task %q", rootID)
	}
	tasks := make(map[string]contracts.Task, len(s.tasks))
	for taskID, task := range s.tasks {
		tasks[taskID] = task
	}
	return &contracts.TaskTree{
		Root:      root,
		Tasks:     tasks,
		Relations: append([]contracts.TaskRelation(nil), s.relations...),
	}, nil
}

func (s *spyStorageBackend) GetTask(_ context.Context, taskID string) (*contracts.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.tasks[taskID]
	if !ok {
		return nil, errors.New("missing task")
	}
	copy := task
	return &copy, nil
}

func (s *spyStorageBackend) SetTaskStatus(_ context.Context, taskID string, status contracts.TaskStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.tasks[taskID]
	if !ok {
		return errors.New("missing task")
	}
	task.Status = status
	s.tasks[taskID] = task
	s.setStatusCalls = append(s.setStatusCalls, statusTransition{taskID: taskID, status: status})
	return nil
}

func (s *spyStorageBackend) SetTaskData(_ context.Context, taskID string, data map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.tasks[taskID]
	if !ok {
		return errors.New("missing task")
	}
	if len(data) > 0 {
		if task.Metadata == nil {
			task.Metadata = map[string]string{}
		}
		for key, value := range data {
			task.Metadata[key] = value
		}
	}
	s.tasks[taskID] = task
	return nil
}

func (s *spyStorageBackend) statusSetCount(taskID string, status contracts.TaskStatus) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for _, call := range s.setStatusCalls {
		if call.taskID == taskID && call.status == status {
			count++
		}
	}
	return count
}

type persistingSpyStorageBackend struct {
	*spyStorageBackend
	persistStatusErr   error
	persistDataErr     error
	persistStatusCalls []statusTransition
	persistDataCalls   []string
}

func newPersistingSpyStorageBackend(tasks []contracts.Task, relations []contracts.TaskRelation) *persistingSpyStorageBackend {
	return &persistingSpyStorageBackend{spyStorageBackend: newSpyStorageBackend(tasks, relations)}
}

func (s *persistingSpyStorageBackend) PersistTaskStatusChange(_ context.Context, taskID string, status contracts.TaskStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.persistStatusCalls = append(s.persistStatusCalls, statusTransition{taskID: taskID, status: status})
	if s.persistStatusErr != nil {
		return s.persistStatusErr
	}
	return nil
}

func (s *persistingSpyStorageBackend) PersistTaskDataChange(_ context.Context, taskID string, _ map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.persistDataCalls = append(s.persistDataCalls, taskID)
	if s.persistDataErr != nil {
		return s.persistDataErr
	}
	return nil
}

func (s *persistingSpyStorageBackend) persistStatusCount(taskID string, status contracts.TaskStatus) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for _, call := range s.persistStatusCalls {
		if call.taskID == taskID && call.status == status {
			count++
		}
	}
	return count
}

func (s *persistingSpyStorageBackend) persistDataCount(taskID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for _, persisted := range s.persistDataCalls {
		if persisted == taskID {
			count++
		}
	}
	return count
}

type spyTaskEngine struct {
	delegate                  contracts.TaskEngine
	buildGraphCalls           int
	nextAvailableCalls        int
	calculateConcurrencyCalls int
	updateTaskStatusCalls     int
	updateTaskStatusErr       error
	isCompleteCalls           int
}

func newSpyTaskEngine(delegate contracts.TaskEngine) *spyTaskEngine {
	return &spyTaskEngine{delegate: delegate}
}

func (s *spyTaskEngine) BuildGraph(tree *contracts.TaskTree) (*contracts.TaskGraph, error) {
	s.buildGraphCalls++
	return s.delegate.BuildGraph(tree)
}

func (s *spyTaskEngine) GetNextAvailable(graph *contracts.TaskGraph) []contracts.TaskSummary {
	s.nextAvailableCalls++
	return s.delegate.GetNextAvailable(graph)
}

func (s *spyTaskEngine) CalculateConcurrency(graph *contracts.TaskGraph, opts contracts.ConcurrencyOptions) int {
	s.calculateConcurrencyCalls++
	return s.delegate.CalculateConcurrency(graph, opts)
}

func (s *spyTaskEngine) UpdateTaskStatus(graph *contracts.TaskGraph, taskID string, status contracts.TaskStatus) error {
	s.updateTaskStatusCalls++
	if s.updateTaskStatusErr != nil {
		return s.updateTaskStatusErr
	}
	return s.delegate.UpdateTaskStatus(graph, taskID, status)
}

func (s *spyTaskEngine) IsComplete(graph *contracts.TaskGraph) bool {
	s.isCompleteCalls++
	return s.delegate.IsComplete(graph)
}
