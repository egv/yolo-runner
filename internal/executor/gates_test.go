package executor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

func TestRunQualityGateBlocksBelowMetadataScoreFromExecutor(t *testing.T) {
	mgr := newGateFakeTaskManager(contracts.Task{ID: "t-1", Title: "Low quality task", Status: contracts.TaskStatusOpen})
	sink := &gateRecordingSink{}
	markBlockedCalls := 0
	clearTerminalCalls := 0
	deps := GateDependencies{
		Tasks:  mgr,
		Events: sink,
		MarkTaskBlockedWithData: func(taskID string, taskData map[string]string) error {
			markBlockedCalls++
			return nil
		},
		ClearTaskTerminalState: func(taskID string) error {
			clearTerminalCalls++
			return nil
		},
	}

	blocked, err := RunQualityGate(
		context.Background(),
		contracts.Task{
			ID:       "t-1",
			Title:    "Low quality task",
			Metadata: map[string]string{"quality_score": "45"},
		},
		deps,
		GateOptions{RepoRoot: "/repo", QualityGateThreshold: 50},
		GateEventContext{WorkerID: "worker", ClonePath: "/repo", QueuePos: 1},
	)
	if err != nil {
		t.Fatalf("RunQualityGate failed: %v", err)
	}
	if !blocked {
		t.Fatalf("expected quality gate to block below-threshold task")
	}
	if mgr.statusByID["t-1"] != contracts.TaskStatusBlocked {
		t.Fatalf("expected blocked status, got %s", mgr.statusByID["t-1"])
	}
	if got := mgr.dataByID["t-1"]["triage_reason"]; !strings.Contains(got, "quality score 45 is below threshold 50") {
		t.Fatalf("expected threshold triage reason, got %q", got)
	}
	if markBlockedCalls != 1 || clearTerminalCalls != 1 {
		t.Fatalf("expected scheduler callbacks once, got mark=%d clear=%d", markBlockedCalls, clearTerminalCalls)
	}
	if len(sink.events) == 0 {
		t.Fatalf("expected gate events to be emitted")
	}
}

func TestRunQCGateBlocksFailingReviewFromExecutor(t *testing.T) {
	mgr := newGateFakeTaskManager(contracts.Task{ID: "t-1", Title: "QC review", Status: contracts.TaskStatusOpen})
	deps := GateDependencies{
		Tasks: mgr,
		MarkTaskBlockedWithData: func(taskID string, taskData map[string]string) error {
			return nil
		},
		ClearTaskTerminalState: func(taskID string) error {
			return nil
		},
	}
	result := contracts.RunnerResult{
		Status:      contracts.RunnerResultCompleted,
		ReviewReady: true,
		Artifacts: map[string]string{
			"review_verdict":       "fail",
			"review_fail_feedback": "review rejected by owner",
		},
	}

	blocked, err := RunQCGate(
		context.Background(),
		contracts.Task{ID: "t-1", Title: "QC review"},
		result,
		deps,
		GateOptions{RequireReview: true},
		GateEventContext{WorkerID: "worker", ClonePath: "/repo", QueuePos: 1},
	)
	if err != nil {
		t.Fatalf("RunQCGate failed: %v", err)
	}
	if !blocked {
		t.Fatalf("expected QC gate to block failing review")
	}
	if got := mgr.dataByID["t-1"]["triage_reason"]; !strings.Contains(got, "review rejected by owner") {
		t.Fatalf("expected review feedback in triage reason, got %q", got)
	}
	var report QCGateReport
	if err := json.Unmarshal([]byte(mgr.dataByID["t-1"]["qc_gate_report"]), &report); err != nil {
		t.Fatalf("expected valid QC report: %v", err)
	}
	if report.Status != "failed" || report.Review != "fail" || len(report.Tools) != 1 {
		t.Fatalf("unexpected QC report: %#v", report)
	}
}

func TestRunQCTestSuiteValidationPassesFromExecutor(t *testing.T) {
	repoRoot := createGateTestRepo(t, map[string]string{
		"go.mod": `module qc-gate-test

go 1.22
`,
		"main.go": `package main

func Sum(a, b int) int {
\treturn a + b
}
`,
		"main_test.go": `package main

import "testing"

func TestSum(t *testing.T) {
\tif Sum(2, 3) != 5 {
\t\tt.Fatalf("unexpected sum: %d", Sum(2, 3))
\t}
}
`,
	})

	result := RunQCTestSuiteValidation(context.Background(), repoRoot)
	if result.Tool != QCGateToolTestRunner {
		t.Fatalf("expected tool=%q, got %q", QCGateToolTestRunner, result.Tool)
	}
	if result.Status != "passed" {
		t.Fatalf("expected passed status, got %q (reason=%q)", result.Status, result.Reason)
	}
	if !result.Passed {
		t.Fatalf("expected test suite validation to pass: %v", result)
	}
	if result.Critical {
		t.Fatalf("did not expect critical error for passing test suite: %v", result)
	}
	if result.Value != "passed" {
		t.Fatalf("expected value=passed, got %q", result.Value)
	}
}

func createGateTestRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	repoRoot := t.TempDir()
	for path, content := range files {
		fullPath := filepath.Join(repoRoot, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatalf("failed to create repository directory: %v", err)
		}
		normalizedContent := strings.ReplaceAll(content, "\\t", "\t")
		if err := os.WriteFile(fullPath, []byte(normalizedContent), 0o644); err != nil {
			t.Fatalf("failed to write test repo file %s: %v", path, err)
		}
	}
	return repoRoot
}

type gateFakeTaskManager struct {
	tasks      map[string]contracts.Task
	statusByID map[string]contracts.TaskStatus
	dataByID   map[string]map[string]string
}

func newGateFakeTaskManager(tasks ...contracts.Task) *gateFakeTaskManager {
	mgr := &gateFakeTaskManager{
		tasks:      map[string]contracts.Task{},
		statusByID: map[string]contracts.TaskStatus{},
		dataByID:   map[string]map[string]string{},
	}
	for _, task := range tasks {
		mgr.tasks[task.ID] = task
		mgr.statusByID[task.ID] = task.Status
	}
	return mgr
}

func (m *gateFakeTaskManager) NextTasks(context.Context, string) ([]contracts.TaskSummary, error) {
	return nil, nil
}

func (m *gateFakeTaskManager) GetTask(_ context.Context, taskID string) (contracts.Task, error) {
	return m.tasks[taskID], nil
}

func (m *gateFakeTaskManager) SetTaskStatus(_ context.Context, taskID string, status contracts.TaskStatus) error {
	m.statusByID[taskID] = status
	return nil
}

func (m *gateFakeTaskManager) SetTaskData(_ context.Context, taskID string, data map[string]string) error {
	if m.dataByID[taskID] == nil {
		m.dataByID[taskID] = map[string]string{}
	}
	for key, value := range data {
		m.dataByID[taskID][key] = value
	}
	return nil
}

type gateRecordingSink struct {
	events []contracts.Event
}

func (s *gateRecordingSink) Emit(_ context.Context, event contracts.Event) error {
	s.events = append(s.events, event)
	return nil
}
