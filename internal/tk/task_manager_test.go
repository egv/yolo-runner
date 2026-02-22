package tk

import (
	"context"
	"strings"
	"testing"

	"github.com/anomalyco/yolo-runner/internal/contracts"
	enginepkg "github.com/anomalyco/yolo-runner/internal/engine"
)

func TestTaskManagerSetTaskDataUsesAddNote(t *testing.T) {
	r := &fakeRunner{responses: map[string]string{}}
	m := NewTaskManager(r)

	err := m.SetTaskData(context.Background(), "t-1", map[string]string{"triage_status": "blocked", "triage_reason": "timeout"})
	if err != nil {
		t.Fatalf("set task data failed: %v", err)
	}

	if !r.called("tk add-note t-1 triage_reason=timeout") {
		t.Fatalf("expected triage_reason note call, got %v", r.calls)
	}
	if !r.called("tk add-note t-1 triage_status=blocked") {
		t.Fatalf("expected triage_status note call, got %v", r.calls)
	}
}

func TestTaskManagerNextTasksMapsReadyResults(t *testing.T) {
	r := &fakeRunner{responses: map[string]string{
		"tk query": `{"id":"root","status":"open","type":"epic","priority":0}` + "\n" +
			`{"id":"root.1","status":"open","type":"task","priority":1,"parent":"root","title":"A"}`,
		"tk ready":   "root.1 [open] - A\n",
		"tk blocked": "",
	}}
	m := NewTaskManager(r)

	tasks, err := m.NextTasks(context.Background(), "root")
	if err != nil {
		t.Fatalf("next tasks failed: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != "root.1" || tasks[0].Title != "A" {
		t.Fatalf("unexpected next tasks: %#v", tasks)
	}
}

func TestTaskManagerSetTaskStatusUsesMappedStatusCalls(t *testing.T) {
	r := &fakeRunner{responses: map[string]string{}}
	m := NewTaskManager(r)

	if err := m.SetTaskStatus(context.Background(), "t-1", contracts.TaskStatusInProgress); err != nil {
		t.Fatalf("set status failed: %v", err)
	}

	if !containsPrefix(r.calls, "tk start t-1") {
		t.Fatalf("expected tk start call, got %v", r.calls)
	}
}

func TestTaskManagerNextTasksSkipsTerminalFailedTasks(t *testing.T) {
	r := &fakeRunner{responses: map[string]string{
		"tk query": `{"id":"root","status":"open","type":"epic","priority":0}` + "\n" +
			`{"id":"root.1","status":"open","type":"task","priority":1,"parent":"root","title":"A"}` + "\n" +
			`{"id":"root.2","status":"open","type":"task","priority":2,"parent":"root","title":"B"}`,
		"tk ready":   "root.1 [open] - A\nroot.2 [open] - B\n",
		"tk blocked": "",
	}}
	m := NewTaskManager(r)
	if err := m.SetTaskStatus(context.Background(), "root.1", contracts.TaskStatusFailed); err != nil {
		t.Fatalf("mark failed status failed: %v", err)
	}

	tasks, err := m.NextTasks(context.Background(), "root")
	if err != nil {
		t.Fatalf("next tasks failed: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != "root.2" {
		t.Fatalf("expected only non-failed task, got %#v", tasks)
	}
}

func TestTaskManagerNextTasksClearsTerminalStateWhenReopened(t *testing.T) {
	r := &fakeRunner{responses: map[string]string{
		"tk query": `{"id":"root","status":"open","type":"epic","priority":0}` + "\n" +
			`{"id":"root.1","status":"open","type":"task","priority":1,"parent":"root","title":"A"}`,
		"tk ready":   "root.1 [open] - A\n",
		"tk blocked": "",
	}}
	m := NewTaskManager(r)
	if err := m.SetTaskStatus(context.Background(), "root.1", contracts.TaskStatusFailed); err != nil {
		t.Fatalf("mark failed status failed: %v", err)
	}
	if err := m.SetTaskStatus(context.Background(), "root.1", contracts.TaskStatusOpen); err != nil {
		t.Fatalf("set open status failed: %v", err)
	}

	tasks, err := m.NextTasks(context.Background(), "root")
	if err != nil {
		t.Fatalf("next tasks failed: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != "root.1" {
		t.Fatalf("expected reopened task to be runnable again, got %#v", tasks)
	}
}

func TestTaskManagerNextTasksFiltersUnsatisfiedDependencies(t *testing.T) {
	r := &fakeRunner{responses: map[string]string{
		"tk query": `{"id":"root","status":"open","type":"epic","priority":0}` + "\n" +
			`{"id":"root.1","status":"open","type":"task","priority":1,"parent":"root","title":"Blocked by dep","deps":["dep.1"]}` + "\n" +
			`{"id":"root.2","status":"open","type":"task","priority":2,"parent":"root","title":"Ready now"}` + "\n" +
			`{"id":"dep.1","status":"open","type":"task","priority":0,"title":"Dependency"}`,
		"tk ready":       "root.1 [open] - Blocked by dep\nroot.2 [open] - Ready now\n",
		"tk blocked":     "",
		"tk show root.1": "notes:\n",
		"tk show root.2": "notes:\n",
	}}
	m := NewTaskManager(r)

	tasks, err := m.NextTasks(context.Background(), "root")
	if err != nil {
		t.Fatalf("next tasks failed: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != "root.2" {
		t.Fatalf("expected only dependency-satisfied task, got %#v", tasks)
	}
}

func TestTaskManagerGetTaskIncludesDependencyMetadata(t *testing.T) {
	r := &fakeRunner{responses: map[string]string{
		"tk show t-1": "# Task 1\n",
		"tk query": `{"id":"t-1","status":"open","type":"task","title":"Task 1","description":"do work","deps":["d-1","d-2"]}` + "\n" +
			`{"id":"d-1","status":"closed","type":"task","title":"Dep 1"}` + "\n" +
			`{"id":"d-2","status":"open","type":"task","title":"Dep 2"}`,
	}}
	m := NewTaskManager(r)

	task, err := m.GetTask(context.Background(), "t-1")
	if err != nil {
		t.Fatalf("get task failed: %v", err)
	}
	if task.Metadata["dependencies"] != "d-1,d-2" {
		t.Fatalf("expected dependency metadata, got %#v", task.Metadata)
	}
}

func TestTaskManagerGetTaskTreeTreatsOpenRootWithTerminalChildrenAsComplete(t *testing.T) {
	r := &fakeRunner{responses: map[string]string{
		"tk query": `{"id":"root","status":"open","type":"epic","title":"Root"}` + "\n" +
			`{"id":"root.1","status":"closed","type":"task","priority":1,"parent":"root","title":"Done child"}` + "\n" +
			`{"id":"root.2","status":"failed","type":"task","priority":2,"parent":"root","title":"Failed child"}`,
	}}
	m := NewTaskManager(r)

	tree, err := m.GetTaskTree(context.Background(), "root")
	if err != nil {
		t.Fatalf("get task tree failed: %v", err)
	}
	if len(tree.Tasks) != 3 {
		t.Fatalf("expected 3 tasks in tree, got %d", len(tree.Tasks))
	}

	taskEngine := enginepkg.NewTaskEngine()
	graph, err := taskEngine.BuildGraph(tree)
	if err != nil {
		t.Fatalf("BuildGraph returned error: %v", err)
	}
	if ready := taskEngine.GetNextAvailable(graph); len(ready) != 0 {
		t.Fatalf("expected no runnable tasks, got %#v", ready)
	}
	if !taskEngine.IsComplete(graph) {
		t.Fatalf("expected open root with terminal children to be complete")
	}
}

func containsPrefix(calls []string, prefix string) bool {
	for _, call := range calls {
		if strings.HasPrefix(call, prefix) {
			return true
		}
	}
	return false
}
