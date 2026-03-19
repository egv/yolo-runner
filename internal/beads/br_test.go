package beads

import (
	"context"
	"os"
	"path/filepath"
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
