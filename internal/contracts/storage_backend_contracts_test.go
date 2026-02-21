package contracts

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestStorageBackendContractGetTaskTreeReturnsCompleteHierarchy(t *testing.T) {
	ctx := context.Background()
	backend := newFakeStorageBackendFixture()

	tree, err := backend.GetTaskTree(ctx, "root")
	if err != nil {
		t.Fatalf("GetTaskTree returned error: %v", err)
	}
	if tree.Root.ID != "root" {
		t.Fatalf("expected root task ID %q, got %q", "root", tree.Root.ID)
	}
	if len(tree.Tasks) != 3 {
		t.Fatalf("expected 3 tasks in tree, got %d", len(tree.Tasks))
	}
	if len(tree.Relations) != 3 {
		t.Fatalf("expected 3 relations, got %d", len(tree.Relations))
	}
	if _, ok := tree.Tasks["t-1"]; !ok {
		t.Fatalf("expected task tree to include %q", "t-1")
	}
	if _, ok := tree.Tasks["t-2"]; !ok {
		t.Fatalf("expected task tree to include %q", "t-2")
	}
}

func TestStorageBackendContractGetTaskReturnsTaskByID(t *testing.T) {
	ctx := context.Background()
	backend := newFakeStorageBackendFixture()

	task, err := backend.GetTask(ctx, "t-1")
	if err != nil {
		t.Fatalf("GetTask returned error: %v", err)
	}
	if task.ID != "t-1" {
		t.Fatalf("expected task ID %q, got %q", "t-1", task.ID)
	}
	task, err = backend.GetTask(ctx, "t-2")
	if err != nil {
		t.Fatalf("GetTask returned error: %v", err)
	}
	if task.ID != "t-2" {
		t.Fatalf("expected task ID %q, got %q", "t-2", task.ID)
	}
}

func TestStorageBackendContractSetTaskStatusPersistsStatusChanges(t *testing.T) {
	ctx := context.Background()
	backend := newFakeStorageBackendFixture()

	if err := backend.SetTaskStatus(ctx, "t-1", TaskStatusInProgress); err != nil {
		t.Fatalf("SetTaskStatus returned error: %v", err)
	}
	if backend.statuses["t-1"] != TaskStatusInProgress {
		t.Fatalf("expected status %q, got %q", TaskStatusInProgress, backend.statuses["t-1"])
	}

	task, err := backend.GetTask(ctx, "t-1")
	if err != nil {
		t.Fatalf("GetTask returned error: %v", err)
	}
	if task.Status != TaskStatusInProgress {
		t.Fatalf("expected persisted task status %q, got %q", TaskStatusInProgress, task.Status)
	}
}

func TestStorageBackendContractSetTaskDataStoresKeyValuePairs(t *testing.T) {
	ctx := context.Background()
	backend := newFakeStorageBackendFixture()

	update := map[string]string{"owner": "agent", "priority": "high"}
	if err := backend.SetTaskData(ctx, "t-1", update); err != nil {
		t.Fatalf("SetTaskData returned error: %v", err)
	}
	if backend.data["t-1"]["owner"] != "agent" {
		t.Fatalf("expected stored task data owner=agent, got %#v", backend.data["t-1"])
	}
	if backend.data["t-1"]["priority"] != "high" {
		t.Fatalf("expected stored task data priority=high, got %#v", backend.data["t-1"])
	}
}

func TestStorageBackendContractGetTaskTreeReturnsErrorForInvalidRootID(t *testing.T) {
	ctx := context.Background()
	backend := newFakeStorageBackendFixture()

	_, err := backend.GetTaskTree(ctx, "missing-root")
	if err == nil {
		t.Fatalf("expected GetTaskTree to fail for invalid root ID")
	}
	if !strings.Contains(err.Error(), `root task "missing-root" not found`) {
		t.Fatalf("expected clear missing-root error, got %q", err)
	}
}

func TestStorageBackendContractGetTaskReturnsErrorForMissingTaskID(t *testing.T) {
	ctx := context.Background()
	backend := newFakeStorageBackendFixture()

	_, err := backend.GetTask(ctx, "missing-task")
	if err == nil {
		t.Fatalf("expected GetTask to fail for missing task ID")
	}
	if !strings.Contains(err.Error(), `task "missing-task" not found`) {
		t.Fatalf("expected clear missing-task error, got %q", err)
	}
}

func TestStorageBackendContractsCanBeImplementedByFakes(t *testing.T) {
	ctx := context.Background()
	backend := newFakeStorageBackendFixture()

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

func newFakeStorageBackendFixture() *fakeStorageBackend {
	return &fakeStorageBackend{
		tree: TaskTree{
			Root: Task{ID: "root", Title: "Root"},
			Tasks: map[string]Task{
				"root": {ID: "root", Title: "Root"},
				"t-1":  {ID: "t-1", Title: "Task 1", Status: TaskStatusOpen},
				"t-2":  {ID: "t-2", Title: "Task 2", Status: TaskStatusBlocked},
			},
			Relations: []TaskRelation{
				{FromID: "root", ToID: "t-1", Type: RelationParent},
				{FromID: "root", ToID: "t-2", Type: RelationParent},
				{FromID: "t-2", ToID: "t-1", Type: RelationDependsOn},
			},
		},
		tasks: map[string]Task{
			"t-1": {ID: "t-1", Title: "Task 1", Status: TaskStatusOpen},
			"t-2": {ID: "t-2", Title: "Task 2", Status: TaskStatusBlocked},
		},
		statuses: make(map[string]TaskStatus),
		data:     make(map[string]map[string]string),
	}
}

func (f *fakeStorageBackend) GetTaskTree(_ context.Context, rootID string) (*TaskTree, error) {
	if _, ok := f.tree.Tasks[rootID]; !ok || f.tree.Root.ID != rootID {
		return nil, fmt.Errorf("root task %q not found", rootID)
	}
	tree := f.tree
	return &tree, nil
}

func (f *fakeStorageBackend) GetTask(_ context.Context, taskID string) (*Task, error) {
	task, ok := f.tasks[taskID]
	if !ok {
		return nil, fmt.Errorf("task %q not found", taskID)
	}
	return &task, nil
}

func (f *fakeStorageBackend) SetTaskStatus(_ context.Context, taskID string, status TaskStatus) error {
	task, ok := f.tasks[taskID]
	if !ok {
		return fmt.Errorf("task %q not found", taskID)
	}
	task.Status = status
	f.tasks[taskID] = task
	f.statuses[taskID] = status
	return nil
}

func (f *fakeStorageBackend) SetTaskData(_ context.Context, taskID string, data map[string]string) error {
	if _, ok := f.tasks[taskID]; !ok {
		return fmt.Errorf("task %q not found", taskID)
	}
	dataCopy := make(map[string]string, len(data))
	for k, v := range data {
		dataCopy[k] = v
	}
	f.data[taskID] = dataCopy
	return nil
}
