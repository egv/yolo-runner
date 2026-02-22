package tk

import (
	"context"
	"testing"

	"github.com/anomalyco/yolo-runner/internal/contracts"
)

func TestStorageBackendGetTaskTreeReturnsHierarchyWithDependencies(t *testing.T) {
	r := &fakeRunner{responses: map[string]string{
		"tk query": `{"id":"root","status":"open","type":"epic","title":"Root"}` + "\n" +
			`{"id":"root.1","status":"open","type":"task","title":"Child","parent":"root","deps":["root.2"]}` + "\n" +
			`{"id":"root.2","status":"closed","type":"task","title":"Dependency","parent":"root"}`,
	}}
	backend := NewStorageBackend(r)

	tree, err := backend.GetTaskTree(context.Background(), "root")
	if err != nil {
		t.Fatalf("GetTaskTree failed: %v", err)
	}
	if tree == nil {
		t.Fatalf("expected non-nil task tree")
	}
	if tree.Root.ID != "root" {
		t.Fatalf("expected root ID root, got %q", tree.Root.ID)
	}
	if len(tree.Tasks) != 3 {
		t.Fatalf("expected 3 tasks in tree, got %d", len(tree.Tasks))
	}
	if deps := tree.Tasks["root.1"].Metadata["dependencies"]; deps != "root.2" {
		t.Fatalf("expected dependency metadata root.2, got %q", deps)
	}

	assertTaskRelation(t, tree.Relations, contracts.TaskRelation{FromID: "root", ToID: "root.1", Type: contracts.RelationParent})
	assertTaskRelation(t, tree.Relations, contracts.TaskRelation{FromID: "root", ToID: "root.2", Type: contracts.RelationParent})
	assertTaskRelation(t, tree.Relations, contracts.TaskRelation{FromID: "root.1", ToID: "root.2", Type: contracts.RelationDependsOn})
	assertTaskRelation(t, tree.Relations, contracts.TaskRelation{FromID: "root.2", ToID: "root.1", Type: contracts.RelationBlocks})
}

func TestStorageBackendGetTaskReturnsNilWhenMissing(t *testing.T) {
	r := &fakeRunner{responses: map[string]string{
		"tk show missing": "# Missing task\n",
		"tk query":        `{"id":"root.1","status":"open","type":"task","title":"Task"}`,
	}}
	backend := NewStorageBackend(r)

	task, err := backend.GetTask(context.Background(), "missing")
	if err != nil {
		t.Fatalf("GetTask failed: %v", err)
	}
	if task != nil {
		t.Fatalf("expected missing task lookup to return nil task, got %#v", task)
	}
}

func assertTaskRelation(t *testing.T, relations []contracts.TaskRelation, want contracts.TaskRelation) {
	t.Helper()
	for _, relation := range relations {
		if relation.FromID == want.FromID && relation.ToID == want.ToID && relation.Type == want.Type {
			return
		}
	}
	t.Fatalf("expected relation %#v, got %#v", want, relations)
}
