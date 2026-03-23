package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

func TestIntegrationTDDWorkflowHonorsTestsFirstThenImplementation(t *testing.T) {
	repoRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoRoot, "go.mod"), []byte("module tdd-integration\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("failed to write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "feature.go"), []byte("package main\n\nfunc Feature() int {\n\treturn 1\n}\n"), 0o644); err != nil {
		t.Fatalf("failed to write feature.go: %v", err)
	}

	task := contracts.Task{
		ID:     "tdd-task",
		Title:  "Implement feature",
		Status: contracts.TaskStatusOpen,
	}
	mgr := newFakeTaskManager(task)
	loop := NewLoop(mgr, &fakeRunner{}, nil, LoopOptions{
		ParentID: "root",
		RepoRoot: repoRoot,
		TDDMode:  true,
	})

	// First run: no tests exist, so TDD gate must block implementation.
	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed on initial run: %v", err)
	}
	if summary.Blocked != 1 {
		t.Fatalf("expected initial run to block until tests are added, got %#v", summary)
	}
	if mgr.statusByID["tdd-task"] != contracts.TaskStatusBlocked {
		t.Fatalf("expected blocked status after initial run, got %s", mgr.statusByID["tdd-task"])
	}
	if got := mgr.dataByID["tdd-task"]["triage_reason"]; !strings.Contains(got, "tests-first") {
		t.Fatalf("expected tests-first triage reason, got %q", got)
	}
	if got := mgr.dataByID["tdd-task"]["tests_present"]; got != "false" {
		t.Fatalf("expected tests_present=false, got %q", got)
	}

	// Add a failing test to satisfy tests-first requirement.
	if err := os.WriteFile(filepath.Join(repoRoot, "feature_test.go"), []byte("package main\n\nimport \"testing\"\n\nfunc TestFeature(t *testing.T) {\n\tif Feature() == 1 {\n\t\tt.Fatalf(\"feature is still unimplemented\")\n\t}\n}\n"), 0o644); err != nil {
		t.Fatalf("failed to write failing test file: %v", err)
	}

	if err := mgr.SetTaskStatus(context.Background(), "tdd-task", contracts.TaskStatusOpen); err != nil {
		t.Fatalf("failed to reopen blocked task: %v", err)
	}

	runResultTask := &fakeRunner{
		results: []contracts.RunnerResult{
			{Status: contracts.RunnerResultCompleted},
		},
	}
	loop = NewLoop(mgr, runResultTask, nil, LoopOptions{
		ParentID: "root",
		RepoRoot: repoRoot,
		TDDMode:  true,
	})

	// Second run: now tests exist and fail, so implementation may proceed.
	summary, err = loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed after adding test: %v", err)
	}
	if summary.Completed != 1 {
		t.Fatalf("expected implementation run to complete after tests are added, got %#v", summary)
	}
	if mgr.statusByID["tdd-task"] != contracts.TaskStatusClosed {
		t.Fatalf("expected closed status after implementation request completes, got %s", mgr.statusByID["tdd-task"])
	}
	if len(runResultTask.requests) != 1 {
		t.Fatalf("expected one implementation request after tests were added, got %d", len(runResultTask.requests))
	}
	if runResultTask.requests[0].Mode != contracts.RunnerModeImplement {
		t.Fatalf("expected implement mode after test-first gate, got %s", runResultTask.requests[0].Mode)
	}
}

func TestIntegrationCoverageGateBlocksUntilFixedForTDDTask(t *testing.T) {
	repoRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoRoot, "go.mod"), []byte("module tdd-integration\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("failed to write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "coverage.go"), []byte("package main\n\nfunc Covered() bool { return true }\n"), 0o644); err != nil {
		t.Fatalf("failed to write code file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "coverage_test.go"), []byte("package main\n\nimport \"testing\"\n\nfunc TestCovered(t *testing.T) {\n\tif !Covered() {\n\t\tt.Fatalf(\"coverage helper should return true\")\n\t}\n}\n"), 0o644); err != nil {
		t.Fatalf("failed to write passing test file: %v", err)
	}

	mgr := newFakeTaskManager(contracts.Task{
		ID:       "coverage-task",
		Title:    "Implement coverage-safe behavior",
		Status:   contracts.TaskStatusOpen,
		Metadata: map[string]string{"coverage": "45"},
	})
	loop := NewLoop(mgr, &fakeRunner{}, nil, LoopOptions{
		ParentID:             "root",
		RepoRoot:             repoRoot,
		TDDMode:              false,
		QualityGateThreshold: 50,
	})

	// First run: coverage is below threshold, so run must remain blocked.
	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed on low coverage run: %v", err)
	}
	if summary.Blocked != 1 {
		t.Fatalf("expected low coverage run to be blocked, got %#v", summary)
	}
	if got := mgr.dataByID["coverage-task"]["triage_reason"]; !strings.Contains(got, "below threshold") {
		t.Fatalf("expected below-threshold triage reason, got %q", got)
	}
	if len(mgr.statusByID) != 1 || mgr.statusByID["coverage-task"] != contracts.TaskStatusBlocked {
		t.Fatalf("expected blocked task status, got %v", mgr.statusByID["coverage-task"])
	}

	// Fix coverage metric and rerun to verify the block is lifted.
	mgr = newFakeTaskManager(contracts.Task{
		ID:       "coverage-task",
		Title:    "Implement coverage-safe behavior",
		Status:   contracts.TaskStatusOpen,
		Metadata: map[string]string{"coverage": "50"},
	})
	runner := &fakeRunner{
		results: []contracts.RunnerResult{
			{Status: contracts.RunnerResultCompleted},
		},
	}
	loop = NewLoop(mgr, runner, nil, LoopOptions{
		ParentID:             "root",
		RepoRoot:             repoRoot,
		TDDMode:              false,
		QualityGateThreshold: 50,
	})

	summary, err = loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed after coverage fixed: %v", err)
	}
	if summary.Completed != 1 {
		t.Fatalf("expected blocked task to complete after coverage is fixed, got %#v", summary)
	}
	if mgr.statusByID["coverage-task"] != contracts.TaskStatusClosed {
		t.Fatalf("expected closed status after passing coverage gate, got %s", mgr.statusByID["coverage-task"])
	}
	if len(runner.requests) != 1 {
		t.Fatalf("expected one implementation request after coverage fixed, got %d", len(runner.requests))
	}
	if runner.requests[0].Mode != contracts.RunnerModeImplement {
		t.Fatalf("expected implement mode after coverage gate passes, got %s", runner.requests[0].Mode)
	}
}
