package beads

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

func TestReconcileRestartResumesBlockedTaskWithoutDuplicatingCompletedWork(t *testing.T) {
	ctx := context.Background()
	queuePath := filepath.Join(t.TempDir(), "queue.db")
	store := openBeadsSourceQueue(t, queuePath)

	storage := &fakeBeadsStorage{
		tree: &contracts.TaskTree{
			Root: contracts.Task{ID: "epic-1", Title: "Epic", Status: contracts.TaskStatusOpen},
			Tasks: map[string]contracts.Task{
				"epic-1": {ID: "epic-1", Title: "Epic", Status: contracts.TaskStatusOpen},
				"task-a": {ID: "task-a", Title: "Task A", Description: "first", Status: contracts.TaskStatusOpen, ParentID: "epic-1"},
				"task-b": {ID: "task-b", Title: "Task B", Description: "second", Status: contracts.TaskStatusOpen, ParentID: "epic-1"},
			},
			Relations: []contracts.TaskRelation{
				{FromID: "epic-1", ToID: "task-a", Type: contracts.RelationParent},
				{FromID: "epic-1", ToID: "task-b", Type: contracts.RelationParent},
				{FromID: "task-b", ToID: "task-a", Type: contracts.RelationDependsOn},
			},
		},
	}

	src := &Source{
		SourceName: "beads-test",
		RootID:     "epic-1",
		Preset:     "yolo-runner",
		Storage:    storage,
		Queue:      store,
	}
	submissions, err := src.Reconcile(ctx)
	if err != nil {
		t.Fatalf("Reconcile(first) error = %v", err)
	}
	if got, want := submissionRefs(submissions), []string{"task-a", "task-b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Reconcile(first) source refs = %#v, want %#v", got, want)
	}

	first, err := store.Claim("runner-a", []string{"yolo-runner"}, time.Minute)
	if err != nil {
		t.Fatalf("Claim(first) error = %v", err)
	}
	if first == nil || first.SourceRef != "task-a" {
		t.Fatalf("Claim(first) = %#v, want task-a", first)
	}

	blockedByDep, err := store.Claim("runner-b", []string{"yolo-runner"}, time.Minute)
	if err != nil {
		t.Fatalf("Claim(blocked by dependency) error = %v", err)
	}
	if blockedByDep != nil {
		t.Fatalf("Claim(blocked by dependency) = %q, want nil until task-a is done", blockedByDep.SourceRef)
	}

	resultPayload, err := json.Marshal(workitem.ImplementResult{Status: string(contracts.RunnerResultCompleted)})
	if err != nil {
		t.Fatalf("marshal implement result: %v", err)
	}
	if err := store.Complete(first.ID, workqueue.Result{Payload: resultPayload}); err != nil {
		t.Fatalf("Complete(task-a) error = %v", err)
	}

	restartedStore := openBeadsSourceQueue(t, queuePath)
	restarted := &Source{
		SourceName: "beads-test",
		RootID:     "epic-1",
		Preset:     "yolo-runner",
		Storage:    storage,
		Queue:      restartedStore,
	}
	restartedSubmissions, err := restarted.Reconcile(ctx)
	if err != nil {
		t.Fatalf("Reconcile(restart) error = %v", err)
	}
	if got, want := submissionRefs(restartedSubmissions), []string{"task-b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Reconcile(restart) source refs = %#v, want %#v", got, want)
	}

	resumed, err := restartedStore.Claim("runner-c", []string{"yolo-runner"}, time.Minute)
	if err != nil {
		t.Fatalf("Claim(after restart) error = %v", err)
	}
	if resumed == nil || resumed.SourceRef != "task-b" {
		t.Fatalf("Claim(after restart) = %#v, want task-b without reclaiming completed task-a", resumed)
	}

	duplicate, err := restartedStore.Claim("runner-d", []string{"yolo-runner"}, time.Minute)
	if err != nil {
		t.Fatalf("Claim(duplicate check) error = %v", err)
	}
	if duplicate != nil {
		t.Fatalf("Claim(duplicate check) = %q, want no duplicated completed work", duplicate.SourceRef)
	}
}

func TestHandleResultWritesBeadsTerminalStatuses(t *testing.T) {
	ctx := context.Background()
	storage := &fakeBeadsStorage{}
	src := &Source{Storage: storage}

	completedPayload, err := json.Marshal(workitem.ImplementResult{Status: string(contracts.RunnerResultCompleted)})
	if err != nil {
		t.Fatalf("marshal completed result: %v", err)
	}
	if _, err := src.HandleResult(ctx, workitem.Item{
		Kind:      workitem.KindImplement,
		SourceRef: "task-done",
	}, workqueue.Result{Status: workqueue.ResultStatusCompleted, Payload: completedPayload}); err != nil {
		t.Fatalf("HandleResult(completed) error = %v", err)
	}
	if got := storage.statuses["task-done"]; got != contracts.TaskStatusClosed {
		t.Fatalf("completed status = %q, want %q", got, contracts.TaskStatusClosed)
	}

	blockedPayload, err := json.Marshal(workitem.ImplementResult{
		Status: string(contracts.RunnerResultBlocked),
		Reason: "needs a product decision",
	})
	if err != nil {
		t.Fatalf("marshal blocked result: %v", err)
	}
	if _, err := src.HandleResult(ctx, workitem.Item{
		Kind:      workitem.KindImplement,
		SourceRef: "task-blocked",
	}, workqueue.Result{Status: workqueue.ResultStatusBlocked, Payload: blockedPayload}); err != nil {
		t.Fatalf("HandleResult(blocked) error = %v", err)
	}
	if got := storage.statuses["task-blocked"]; got != contracts.TaskStatusBlocked {
		t.Fatalf("blocked status = %q, want %q", got, contracts.TaskStatusBlocked)
	}
	if got := storage.data["task-blocked"]["reason"]; got != "needs a product decision" {
		t.Fatalf("blocked reason data = %q, want reason", got)
	}
}

type fakeBeadsStorage struct {
	tree     *contracts.TaskTree
	statuses map[string]contracts.TaskStatus
	data     map[string]map[string]string
}

func (s *fakeBeadsStorage) GetTaskTree(context.Context, string) (*contracts.TaskTree, error) {
	return s.tree, nil
}

func (s *fakeBeadsStorage) GetTask(_ context.Context, taskID string) (*contracts.Task, error) {
	task := s.tree.Tasks[taskID]
	return &task, nil
}

func (s *fakeBeadsStorage) SetTaskStatus(_ context.Context, taskID string, status contracts.TaskStatus) error {
	if s.statuses == nil {
		s.statuses = map[string]contracts.TaskStatus{}
	}
	s.statuses[taskID] = status
	return nil
}

func (s *fakeBeadsStorage) SetTaskData(_ context.Context, taskID string, data map[string]string) error {
	if s.data == nil {
		s.data = map[string]map[string]string{}
	}
	s.data[taskID] = data
	return nil
}

func openBeadsSourceQueue(t *testing.T, path string) *workqueue.Store {
	t.Helper()

	store, err := workqueue.Open(path)
	if err != nil {
		t.Fatalf("Open(%q) error = %v", path, err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return store
}

func submissionRefs(submissions []workqueue.Submission) []string {
	refs := make([]string, len(submissions))
	for i, submission := range submissions {
		refs[i] = submission.SourceRef
	}
	return refs
}
