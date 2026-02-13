package conformance

import (
	"context"
	"strings"
	"testing"

	"github.com/anomalyco/yolo-runner/internal/contracts"
)

type TaskManagerScenario string

const (
	TaskManagerScenarioTaskSelection            TaskManagerScenario = "task_selection"
	TaskManagerScenarioGetTaskDetails           TaskManagerScenario = "get_task_details"
	TaskManagerScenarioTerminalStateTransitions TaskManagerScenario = "terminal_state_transitions"
	TaskManagerScenarioStatusLifecycle          TaskManagerScenario = "status_lifecycle"
	TaskManagerScenarioSetTaskData              TaskManagerScenario = "set_task_data"
)

type TaskManagerFixture struct {
	Manager contracts.TaskManager
	Assert  func(t *testing.T)
}

type TaskManagerFactory func(t *testing.T, scenario TaskManagerScenario) TaskManagerFixture

type TaskManagerConfig struct {
	Backend        string
	NewTaskManager TaskManagerFactory
}

func RunTaskManagerSuite(t *testing.T, cfg TaskManagerConfig) {
	t.Helper()

	backend := strings.TrimSpace(cfg.Backend)
	if backend == "" {
		t.Fatal("conformance backend is required")
	}
	if cfg.NewTaskManager == nil {
		t.Fatal("conformance task manager factory is required")
	}

	t.Run("next tasks filters unsatisfied dependencies and sorts by priority", func(t *testing.T) {
		fixture := taskManagerFixtureForScenario(t, cfg, TaskManagerScenarioTaskSelection)
		tasks, err := fixture.Manager.NextTasks(context.Background(), "root")
		if err != nil {
			t.Fatalf("NextTasks returned error: %v", err)
		}
		if len(tasks) != 2 {
			t.Fatalf("expected 2 tasks, got %#v", tasks)
		}
		assertTaskSummary(t, tasks[0], "root.2", "Ready now", 0)
		assertTaskSummary(t, tasks[1], "root.3", "Lower priority", 2)
		runTaskManagerFixtureAssert(t, fixture)
	})

	t.Run("get task includes dependency metadata", func(t *testing.T) {
		fixture := taskManagerFixtureForScenario(t, cfg, TaskManagerScenarioGetTaskDetails)
		task, err := fixture.Manager.GetTask(context.Background(), "t-1")
		if err != nil {
			t.Fatalf("GetTask returned error: %v", err)
		}
		if task.ID != "t-1" {
			t.Fatalf("expected task ID t-1, got %q", task.ID)
		}
		if task.Title != "Task 1" {
			t.Fatalf("expected task title %q, got %q", "Task 1", task.Title)
		}
		if task.Description != "do work" {
			t.Fatalf("expected task description %q, got %q", "do work", task.Description)
		}
		if task.Status != contracts.TaskStatusOpen {
			t.Fatalf("expected task status %q, got %q", contracts.TaskStatusOpen, task.Status)
		}
		if deps := strings.TrimSpace(task.Metadata["dependencies"]); deps != "d-1,d-2" {
			t.Fatalf("expected dependencies metadata %q, got %q", "d-1,d-2", deps)
		}
		runTaskManagerFixtureAssert(t, fixture)
	})

	t.Run("failed and blocked tasks are excluded from scheduling until reopened", func(t *testing.T) {
		fixture := taskManagerFixtureForScenario(t, cfg, TaskManagerScenarioTerminalStateTransitions)
		if err := fixture.Manager.SetTaskStatus(context.Background(), "root.1", contracts.TaskStatusFailed); err != nil {
			t.Fatalf("SetTaskStatus failed->failed: %v", err)
		}
		tasks, err := fixture.Manager.NextTasks(context.Background(), "root")
		if err != nil {
			t.Fatalf("NextTasks after failed status returned error: %v", err)
		}
		assertTaskIDs(t, tasks, "root.2")

		if err := fixture.Manager.SetTaskStatus(context.Background(), "root.1", contracts.TaskStatusOpen); err != nil {
			t.Fatalf("SetTaskStatus failed->open: %v", err)
		}
		tasks, err = fixture.Manager.NextTasks(context.Background(), "root")
		if err != nil {
			t.Fatalf("NextTasks after reopening failed task returned error: %v", err)
		}
		assertTaskIDs(t, tasks, "root.1", "root.2")

		if err := fixture.Manager.SetTaskStatus(context.Background(), "root.1", contracts.TaskStatusBlocked); err != nil {
			t.Fatalf("SetTaskStatus open->blocked: %v", err)
		}
		tasks, err = fixture.Manager.NextTasks(context.Background(), "root")
		if err != nil {
			t.Fatalf("NextTasks after blocked status returned error: %v", err)
		}
		assertTaskIDs(t, tasks, "root.2")

		if err := fixture.Manager.SetTaskStatus(context.Background(), "root.1", contracts.TaskStatusOpen); err != nil {
			t.Fatalf("SetTaskStatus blocked->open: %v", err)
		}
		tasks, err = fixture.Manager.NextTasks(context.Background(), "root")
		if err != nil {
			t.Fatalf("NextTasks after reopening blocked task returned error: %v", err)
		}
		assertTaskIDs(t, tasks, "root.1", "root.2")
		runTaskManagerFixtureAssert(t, fixture)
	})

	t.Run("status lifecycle accepts open, in progress, blocked, failed, and closed", func(t *testing.T) {
		fixture := taskManagerFixtureForScenario(t, cfg, TaskManagerScenarioStatusLifecycle)
		lifecycle := []contracts.TaskStatus{
			contracts.TaskStatusInProgress,
			contracts.TaskStatusClosed,
			contracts.TaskStatusBlocked,
			contracts.TaskStatusOpen,
			contracts.TaskStatusFailed,
			contracts.TaskStatusOpen,
		}
		for _, status := range lifecycle {
			if err := fixture.Manager.SetTaskStatus(context.Background(), "t-1", status); err != nil {
				t.Fatalf("SetTaskStatus(%q) returned error: %v", status, err)
			}
		}
		runTaskManagerFixtureAssert(t, fixture)
	})

	t.Run("set task data accepts metadata map", func(t *testing.T) {
		fixture := taskManagerFixtureForScenario(t, cfg, TaskManagerScenarioSetTaskData)
		err := fixture.Manager.SetTaskData(context.Background(), "t-1", map[string]string{
			"triage_status": "blocked",
			"triage_reason": "timeout",
		})
		if err != nil {
			t.Fatalf("SetTaskData returned error: %v", err)
		}
		runTaskManagerFixtureAssert(t, fixture)
	})
}

func taskManagerFixtureForScenario(t *testing.T, cfg TaskManagerConfig, scenario TaskManagerScenario) TaskManagerFixture {
	t.Helper()

	fixture := cfg.NewTaskManager(t, scenario)
	if fixture.Manager == nil {
		t.Fatalf("conformance task manager is required for scenario %q", scenario)
	}
	return fixture
}

func runTaskManagerFixtureAssert(t *testing.T, fixture TaskManagerFixture) {
	t.Helper()
	if fixture.Assert != nil {
		fixture.Assert(t)
	}
}

func assertTaskSummary(t *testing.T, got contracts.TaskSummary, wantID string, wantTitle string, wantPriority int) {
	t.Helper()
	if got.ID != wantID {
		t.Fatalf("expected task ID %q, got %q", wantID, got.ID)
	}
	if got.Title != wantTitle {
		t.Fatalf("expected task title %q, got %q", wantTitle, got.Title)
	}
	if got.Priority == nil {
		t.Fatalf("expected non-nil priority for task %q", got.ID)
	}
	if *got.Priority != wantPriority {
		t.Fatalf("expected priority %d for task %q, got %d", wantPriority, got.ID, *got.Priority)
	}
}

func assertTaskIDs(t *testing.T, tasks []contracts.TaskSummary, want ...string) {
	t.Helper()
	if len(tasks) != len(want) {
		t.Fatalf("expected %d tasks, got %#v", len(want), tasks)
	}
	for idx, task := range tasks {
		if task.ID != want[idx] {
			t.Fatalf("expected task[%d]=%q, got %q", idx, want[idx], task.ID)
		}
	}
}
