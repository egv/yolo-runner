package tk

import (
	"context"
	"strings"
	"testing"

	"github.com/anomalyco/yolo-runner/internal/contracts"
)

func TestTaskManagerSetTaskDataUsesAddNote(t *testing.T) {
	r := &fakeRunner{responses: map[string]string{}}
	m := NewTaskManager(r)

	err := m.SetTaskData(context.Background(), "t-1", map[string]string{"blocked_reason": "timeout", "retry_count": "2"})
	if err != nil {
		t.Fatalf("set task data failed: %v", err)
	}

	if !r.called("tk add-note t-1 blocked_reason=timeout") {
		t.Fatalf("expected blocked_reason note call, got %v", r.calls)
	}
	if !r.called("tk add-note t-1 retry_count=2") {
		t.Fatalf("expected retry_count note call, got %v", r.calls)
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
		"tk ready":       "root.1 [open] - A\nroot.2 [open] - B\n",
		"tk blocked":     "",
		"tk show root.1": "notes:\n- terminal_state=failed\n",
		"tk show root.2": "notes:\n- something=else\n",
	}}
	m := NewTaskManager(r)

	tasks, err := m.NextTasks(context.Background(), "root")
	if err != nil {
		t.Fatalf("next tasks failed: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != "root.2" {
		t.Fatalf("expected only non-failed task, got %#v", tasks)
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
