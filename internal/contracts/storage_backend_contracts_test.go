package contracts

import (
	"context"
	"testing"
)

func TestStorageBackendContractsCanBeImplementedByFakes(t *testing.T) {
	ctx := context.Background()

	backend := &fakeStorageBackend{
		tree: TaskTree{
			Root: Task{ID: "root", Title: "Root"},
			Tasks: map[string]Task{
				"root": {ID: "root", Title: "Root"},
				"t-1":  {ID: "t-1", Title: "Task 1"},
			},
			Relations: []TaskRelation{
				{FromID: "root", ToID: "t-1", Type: RelationParent},
				{FromID: "t-1", ToID: "root", Type: RelationDependsOn},
				{FromID: "root", ToID: "t-1", Type: RelationBlocks},
			},
		},
		tasks: map[string]Task{
			"t-1": {ID: "t-1", Title: "Task 1", Status: TaskStatusOpen},
		},
		statuses: make(map[string]TaskStatus),
		data:     make(map[string]map[string]string),
	}

	tree, err := backend.GetTaskTree(ctx, "root")
	if err != nil {
		t.Fatalf("GetTaskTree returned error: %v", err)
	}
	if tree.Root.ID != "root" {
		t.Fatalf("expected root task ID %q, got %q", "root", tree.Root.ID)
	}
	if len(tree.Tasks) != 2 {
		t.Fatalf("expected 2 tasks in tree, got %d", len(tree.Tasks))
	}
	if len(tree.Relations) != 3 {
		t.Fatalf("expected 3 relations, got %d", len(tree.Relations))
	}

	task, err := backend.GetTask(ctx, "t-1")
	if err != nil {
		t.Fatalf("GetTask returned error: %v", err)
	}
	if task.ID != "t-1" {
		t.Fatalf("expected task ID %q, got %q", "t-1", task.ID)
	}

	if err := backend.SetTaskStatus(ctx, "t-1", TaskStatusInProgress); err != nil {
		t.Fatalf("SetTaskStatus returned error: %v", err)
	}
	if backend.statuses["t-1"] != TaskStatusInProgress {
		t.Fatalf("expected status %q, got %q", TaskStatusInProgress, backend.statuses["t-1"])
	}

	if err := backend.SetTaskData(ctx, "t-1", map[string]string{"owner": "agent"}); err != nil {
		t.Fatalf("SetTaskData returned error: %v", err)
	}
	if backend.data["t-1"]["owner"] != "agent" {
		t.Fatalf("expected stored task data owner=agent, got %#v", backend.data["t-1"])
	}
}

func TestTaskRelationTypeConstants(t *testing.T) {
	if got := string(RelationParent); got != "parent" {
		t.Fatalf("expected RelationParent %q, got %q", "parent", got)
	}
	if got := string(RelationDependsOn); got != "depends_on" {
		t.Fatalf("expected RelationDependsOn %q, got %q", "depends_on", got)
	}
	if got := string(RelationBlocks); got != "blocks" {
		t.Fatalf("expected RelationBlocks %q, got %q", "blocks", got)
	}
}

var _ StorageBackend = (*fakeStorageBackend)(nil)

type fakeStorageBackend struct {
	tree     TaskTree
	tasks    map[string]Task
	statuses map[string]TaskStatus
	data     map[string]map[string]string
}

func (f *fakeStorageBackend) GetTaskTree(context.Context, string) (*TaskTree, error) {
	tree := f.tree
	return &tree, nil
}

func (f *fakeStorageBackend) GetTask(context.Context, string) (*Task, error) {
	task := f.tasks["t-1"]
	return &task, nil
}

func (f *fakeStorageBackend) SetTaskStatus(_ context.Context, taskID string, status TaskStatus) error {
	f.statuses[taskID] = status
	return nil
}

func (f *fakeStorageBackend) SetTaskData(_ context.Context, taskID string, data map[string]string) error {
	f.data[taskID] = data
	return nil
}
