package startrek

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
	trackerstartrek "github.com/egv/yolo-runner/v2/internal/startrek"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

func TestSourcePollResumeNeedsInfoTasksSubmitsFreshPreflight(t *testing.T) {
	ctx := context.Background()
	store, err := workqueue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatalf("Open(queue) error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close(queue) error = %v", err)
		}
	})

	const oldKey = "st/VAY-42/preflight/rev1"
	if _, err := store.Enqueue(workqueue.Submission{
		Kind:           workitem.KindPreflight,
		Source:         "startrek-st-dev",
		SourceRef:      "VAY-42",
		IdempotencyKey: oldKey,
		Preset:         "st-dev",
		Payload:        json.RawMessage(`{"old":true}`),
	}); err != nil {
		t.Fatalf("Enqueue(old preflight) error = %v", err)
	}
	oldItem, err := store.Claim("runner-a", []string{"st-dev"}, time.Minute)
	if err != nil {
		t.Fatalf("Claim(old preflight) error = %v", err)
	}
	if oldItem == nil {
		t.Fatalf("expected old preflight item to claim")
	}
	if err := store.Complete(oldItem.ID, workqueue.Result{Payload: json.RawMessage(`{"verdict":"needs_info"}`)}); err != nil {
		t.Fatalf("Complete(old preflight) error = %v", err)
	}

	backend := &fakeStartrekDiscoveryBackend{
		resumed: []string{"VAY-42"},
		tree: contracts.TaskTree{
			Root: contracts.Task{ID: "VAY", Title: "Queue root", Status: contracts.TaskStatusOpen},
			Tasks: map[string]contracts.Task{
				"VAY": {
					ID:     "VAY",
					Title:  "Queue root",
					Status: contracts.TaskStatusOpen,
				},
				"VAY-42": {
					ID:       "VAY-42",
					Title:    "Clarify ownership",
					Status:   contracts.TaskStatusOpen,
					ParentID: "VAY",
					Metadata: map[string]string{"revision": "rev1"},
				},
			},
			Relations: []contracts.TaskRelation{{
				FromID: "VAY",
				ToID:   "VAY-42",
				Type:   contracts.RelationParent,
			}},
		},
		details: map[string]contracts.Task{
			"VAY-42": {
				ID:          "VAY-42",
				Title:       "Clarify ownership",
				Description: "Recent comments:\nAda: The package owner is adapta/messenger.",
				Status:      contracts.TaskStatusOpen,
				Metadata:    map[string]string{"revision": "rev1"},
			},
		},
	}
	src := &Source{
		SourceName:     "startrek-st-dev",
		Backend:        backend,
		Queue:          store,
		Queues:         []Queue{{Key: "VAY"}},
		Preset:         "st-dev",
		ReadyLabel:     "yolo-agent-ready",
		NeedsInfoLabel: "needs-info",
		Marker:         "needs-info",
	}

	submissions, err := src.Poll(ctx)
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	if len(submissions) != 1 {
		t.Fatalf("Poll() submissions = %d, want exactly one: %#v", len(submissions), submissions)
	}
	got := submissions[0]
	if got.Kind != workitem.KindPreflight || got.Source != "startrek-st-dev" || got.SourceRef != "VAY-42" {
		t.Fatalf("unexpected resumed preflight submission: %#v", got)
	}
	if got.IdempotencyKey == oldKey || !strings.HasPrefix(got.IdempotencyKey, "st/VAY-42/preflight/") {
		t.Fatalf("resumed preflight idempotency key = %q, want fresh st/VAY-42/preflight/<rev> distinct from %q", got.IdempotencyKey, oldKey)
	}
	if got.Preset != "st-dev" {
		t.Fatalf("resumed preflight preset = %q, want st-dev", got.Preset)
	}

	if _, err := store.Enqueue(got); err != nil {
		t.Fatalf("Enqueue(resumed preflight) error = %v", err)
	}
	claimed, err := store.Claim("runner-b", []string{"st-dev"}, time.Minute)
	if err != nil {
		t.Fatalf("Claim(resumed preflight) error = %v", err)
	}
	if claimed == nil {
		t.Fatalf("expected resumed preflight to enqueue as a new claimable item")
	}
	extra, err := store.Claim("runner-c", []string{"st-dev"}, time.Minute)
	if err != nil {
		t.Fatalf("Claim(extra) error = %v", err)
	}
	if extra != nil {
		t.Fatalf("expected exactly one resumed preflight item, got extra %#v", extra)
	}
}

type fakeStartrekDiscoveryBackend struct {
	resumeInputs []trackerstartrek.NeedsInfoResumeInput
	resumed      []string
	tree         contracts.TaskTree
	details      map[string]contracts.Task
}

func (f *fakeStartrekDiscoveryBackend) ResumeNeedsInfoTasks(_ context.Context, input trackerstartrek.NeedsInfoResumeInput) ([]string, error) {
	f.resumeInputs = append(f.resumeInputs, input)
	return append([]string(nil), f.resumed...), nil
}

func (f *fakeStartrekDiscoveryBackend) GetTaskTree(_ context.Context, _ string) (*contracts.TaskTree, error) {
	tree := f.tree
	tree.Tasks = cloneDiscoveryTasks(f.tree.Tasks)
	tree.Relations = append([]contracts.TaskRelation(nil), f.tree.Relations...)
	return &tree, nil
}

func (f *fakeStartrekDiscoveryBackend) GetTask(_ context.Context, taskID string) (*contracts.Task, error) {
	task := f.details[taskID]
	task.Metadata = cloneStartrekStringMap(task.Metadata)
	return &task, nil
}

func (f *fakeStartrekDiscoveryBackend) SetTaskStatus(context.Context, string, contracts.TaskStatus) error {
	return nil
}

func (f *fakeStartrekDiscoveryBackend) SetTaskData(context.Context, string, map[string]string) error {
	return nil
}

func cloneDiscoveryTasks(tasks map[string]contracts.Task) map[string]contracts.Task {
	if tasks == nil {
		return nil
	}
	out := make(map[string]contracts.Task, len(tasks))
	for id, task := range tasks {
		task.Metadata = cloneStartrekStringMap(task.Metadata)
		out[id] = task
	}
	return out
}
