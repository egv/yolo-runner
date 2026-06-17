package beads

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/engine"
)

func TestRustAdapterUsesNoDaemonForUpdateStatus(t *testing.T) {
	runner := &fakeRunner{}
	adapter := NewRustAdapter(runner)

	if err := adapter.UpdateStatus("task-1", "open"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertCall(t, runner.calls, []string{"br", "--no-daemon", "update", "task-1", "--status", "open"})
}

func TestRustAdapterUsesCloseCommandForClosedStatus(t *testing.T) {
	runner := &fakeRunner{}
	adapter := NewRustAdapter(runner)

	if err := adapter.UpdateStatus("task-1", "closed"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertCall(t, runner.calls, []string{"br", "--no-daemon", "close", "task-1"})
}

func TestRustAdapterUsesNoDaemonForSync(t *testing.T) {
	runner := &fakeRunner{}
	adapter := NewRustAdapter(runner)

	if err := adapter.Sync(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertCall(t, runner.calls, []string{"br", "--no-daemon", "sync", "--flush-only"})
}

func TestRustAdapterReadyAllUsesWorkspaceWideReadyCommand(t *testing.T) {
	runner := &fakeRunner{output: `[{"id":"task-1","title":"Task 1","issue_type":"task","status":"open","priority":2}]`}
	adapter := NewRustAdapter(runner)

	issues, err := adapter.ReadyAll()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(issues) != 1 || issues[0].ID != "task-1" {
		t.Fatalf("unexpected ready issues: %#v", issues)
	}

	assertCall(t, runner.calls, []string{"br", "--no-daemon", "ready", "--limit", "0", "--json"})
}

func TestTaskManagerReadyTasksUsesReadyAllPriorityMetadata(t *testing.T) {
	runner := &fakeRunner{output: `[{"id":"task-1","title":"Task 1","description":"do it","issue_type":"task","status":"open","priority":3},{"id":"epic-1","issue_type":"epic","status":"open"}]`}
	manager := NewTaskManager(runner, "/repo")

	tasks, err := manager.ReadyTasks(context.Background())
	if err != nil {
		t.Fatalf("ReadyTasks() error = %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("ReadyTasks() len = %d, want 1: %#v", len(tasks), tasks)
	}
	if tasks[0].ID != "task-1" || tasks[0].Title != "Task 1" || tasks[0].Description != "do it" {
		t.Fatalf("unexpected task: %#v", tasks[0])
	}
	if got := tasks[0].Metadata["priority"]; got != "3" {
		t.Fatalf("priority metadata = %q, want %q", got, "3")
	}
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

func TestRustAdapterTreeWrapsSingleReadyChildUnderRoot(t *testing.T) {
	runner := &fakeRunner{outputs: []string{
		`[{"id":"root.1","issue_type":"task","status":"open"}]`,
	}}
	adapter := NewRustAdapter(runner)

	issue, err := adapter.Tree("root")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if issue.ID != "root" || issue.IssueType != "epic" {
		t.Fatalf("expected synthetic root epic, got %#v", issue)
	}
	if len(issue.Children) != 1 || issue.Children[0].ID != "root.1" {
		t.Fatalf("unexpected children: %#v", issue.Children)
	}

	assertCall(t, runner.calls, []string{"br", "--no-daemon", "ready", "--parent", "root", "--recursive", "--json"})
}

func TestTaskTreeIncludesSiblingDependencyRelations(t *testing.T) {
	runner := &fakeRunner{outputs: []string{
		`[{"id":"root.1","issue_type":"task","status":"open"},{"id":"root.2","issue_type":"task","status":"open"}]`,
		`[{"id":"root","title":"Root Epic","status":"open"}]`,
		`[{"id":"root.1","title":"First Task","status":"open"}]`,
		`[{"id":"root.2","title":"Second Task","status":"open"}]`,
		`[{"issue_id":"root","depends_on_id":"root-parent","type":"parent-child"}]`,
		`[{"issue_id":"root.1","depends_on_id":"root","type":"parent-child"}]`,
		`[{"issue_id":"root.2","depends_on_id":"root","type":"parent-child"},{"issue_id":"root.2","depends_on_id":"root.1","type":"blocks"}]`,
	}}
	manager := NewTaskManager(runner, "/repo")

	tree, err := manager.GetTaskTree(context.Background(), "root")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	graph, err := engine.NewTaskEngine().BuildGraph(tree)
	if err != nil {
		t.Fatalf("build graph: %v", err)
	}
	ready := engine.NewTaskEngine().GetNextAvailable(graph)
	if len(ready) != 1 || ready[0].ID != "root.1" {
		t.Fatalf("expected only first task to be ready, got %#v", ready)
	}

	expected := "br --no-daemon dep list root.2 --json"
	for _, call := range runner.calls {
		if strings.Join(call, " ") == expected {
			return
		}
	}
	t.Fatalf("expected dependency call %q, got %#v", expected, runner.calls)
}

func TestGetTaskTreeFromJSONLRespectsSiblingOrdering(t *testing.T) {
	repoRoot := t.TempDir()
	beadsDir := filepath.Join(repoRoot, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatalf("mkdir .beads: %v", err)
	}
	issues := strings.Join([]string{
		`{"id":"root","title":"OpenCode Epic","status":"open","issue_type":"epic"}`,
		`{"id":"root.1","title":"First Task","status":"open","issue_type":"task","dependencies":[{"issue_id":"root.1","depends_on_id":"root","type":"parent-child"}]}`,
		`{"id":"root.2","title":"Second Task","status":"open","issue_type":"task","dependencies":[{"issue_id":"root.2","depends_on_id":"root","type":"parent-child"},{"issue_id":"root.2","depends_on_id":"root.1","type":"blocks"}]}`,
		`{"id":"root.3","title":"Third Task","status":"open","issue_type":"task","dependencies":[{"issue_id":"root.3","depends_on_id":"root","type":"parent-child"},{"issue_id":"root.3","depends_on_id":"root.2","type":"waits-for"}]}`,
	}, "\n")
	if err := os.WriteFile(filepath.Join(beadsDir, "issues.jsonl"), []byte(issues+"\n"), 0o644); err != nil {
		t.Fatalf("write issues.jsonl: %v", err)
	}

	manager := NewTaskManager(&fakeRunner{}, repoRoot)
	tree, err := manager.GetTaskTree(context.Background(), "root")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	graph, err := engine.NewTaskEngine().BuildGraph(tree)
	if err != nil {
		t.Fatalf("build graph: %v", err)
	}
	ready := engine.NewTaskEngine().GetNextAvailable(graph)
	if len(ready) != 1 || ready[0].ID != "root.1" {
		t.Fatalf("expected only first task to be ready, got %#v", ready)
	}
}

func TestGetTaskTreeFromJSONLBlocksUnclosedExternalDependencies(t *testing.T) {
	repoRoot := t.TempDir()
	beadsDir := filepath.Join(repoRoot, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatalf("mkdir .beads: %v", err)
	}
	issues := strings.Join([]string{
		`{"id":"root","title":"OpenCode Epic","status":"open","issue_type":"epic"}`,
		`{"id":"root.ready","title":"Ready Task","status":"open","issue_type":"task","dependencies":[{"issue_id":"root.ready","depends_on_id":"root","type":"parent-child"},{"issue_id":"root.ready","depends_on_id":"external.closed","type":"blocks"}]}`,
		`{"id":"root.blocked","title":"Blocked Task","status":"open","issue_type":"task","dependencies":[{"issue_id":"root.blocked","depends_on_id":"root","type":"parent-child"},{"issue_id":"root.blocked","depends_on_id":"external.open","type":"blocks"}]}`,
		`{"id":"external.closed","title":"External Closed","status":"closed","issue_type":"task"}`,
		`{"id":"external.open","title":"External Open","status":"open","issue_type":"task"}`,
	}, "\n")
	if err := os.WriteFile(filepath.Join(beadsDir, "issues.jsonl"), []byte(issues+"\n"), 0o644); err != nil {
		t.Fatalf("write issues.jsonl: %v", err)
	}

	manager := NewTaskManager(&fakeRunner{}, repoRoot)
	tree, err := manager.GetTaskTree(context.Background(), "root")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := tree.MissingDependenciesByTask["root.ready"]; len(got) != 0 {
		t.Fatalf("closed external dependency should not block root.ready, got %#v", got)
	}
	if got := tree.MissingDependenciesByTask["root.blocked"]; !reflect.DeepEqual(got, []string{"external.open"}) {
		t.Fatalf("expected root.blocked to be blocked by external.open, got %#v", got)
	}
	graph, err := engine.NewTaskEngine().BuildGraph(tree)
	if err != nil {
		t.Fatalf("build graph: %v", err)
	}
	ready := engine.NewTaskEngine().GetNextAvailable(graph)
	if len(ready) != 1 || ready[0].ID != "root.ready" {
		t.Fatalf("expected only task with closed external dependency to be ready, got %#v", ready)
	}
}

func TestCloseTreatsAlreadyClosedAsSuccess(t *testing.T) {
	// br exits non-zero when the task is already closed; closing an
	// already-closed task must be a no-op success so a double-close (queue
	// dispatcher + loop both applying a completed result) does not fail the run.
	runner := &fakeRunner{
		output: "Warning: Skipped X: already closed\nError: Nothing to do: all 1 issue(s) skipped\n",
		err:    errors.New("exit status 3"),
	}
	adapter := NewRustAdapter(runner)
	if err := adapter.Close("yolo-x"); err != nil {
		t.Fatalf("Close on already-closed task should succeed, got: %v", err)
	}
}

func TestCloseSurfacesRealErrors(t *testing.T) {
	runner := &fakeRunner{
		output: "Error: database is locked\n",
		err:    errors.New("exit status 1"),
	}
	adapter := NewRustAdapter(runner)
	if err := adapter.Close("yolo-x"); err == nil {
		t.Fatal("Close should surface a genuine failure")
	}
}
