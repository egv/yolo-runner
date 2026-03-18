package beads

import (
	"context"
	"testing"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

func TestRustAdapterUsesNoDaemonForUpdateStatus(t *testing.T) {
	runner := &fakeRunner{}
	adapter := NewRustAdapter(runner)

	if err := adapter.UpdateStatus("task-1", "open"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertCall(t, runner.calls, []string{"br", "--no-daemon", "update", "task-1", "--status", "open"})
}

func TestRustAdapterUsesNoDaemonForSync(t *testing.T) {
	runner := &fakeRunner{}
	adapter := NewRustAdapter(runner)

	if err := adapter.Sync(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertCall(t, runner.calls, []string{"br", "--no-daemon", "sync", "--flush-only"})
}

func TestTaskManagerSetTaskDataUsesNoDaemon(t *testing.T) {
	runner := &fakeRunner{}
	manager := NewTaskManager(runner, "/repo")

	err := manager.SetTaskData(context.Background(), "task-1", map[string]string{"foo": "bar"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertCall(t, runner.calls, []string{"br", "--no-daemon", "update", "task-1", "--notes", "foo=bar"})

	var _ contracts.TaskManager = manager
}
