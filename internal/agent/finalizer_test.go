package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/startrek"
)

func TestLoopCreatesParentPRAfterLastSplitSubtaskCloses(t *testing.T) {
	mgr := newFakeTaskManager(
		contracts.Task{
			ID:     "root",
			Title:  "Parent issue",
			Status: contracts.TaskStatusClosed,
			Metadata: map[string]string{
				"split_subtask_ids": "t-1,t-2",
			},
		},
		contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusClosed, ParentID: "root"},
		contracts.Task{ID: "t-2", Title: "Task 2", Status: contracts.TaskStatusOpen, ParentID: "root"},
	)
	run := &fakeRunner{results: []contracts.RunnerResult{{Status: contracts.RunnerResultCompleted}}}
	vcs := &fakeArcPRVCS{}
	loop := NewLoop(mgr, run, nil, LoopOptions{ParentID: "root", MaxRetries: 0, MergeOnSuccess: true, VCS: vcs})

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if summary.Completed != 1 {
		t.Fatalf("expected one completed subtask, got %#v", summary)
	}
	if mgr.statusByID["t-2"] != contracts.TaskStatusClosed {
		t.Fatalf("expected last subtask closed, got %s", mgr.statusByID["t-2"])
	}

	createPRCalls := 0
	for _, call := range vcs.calls {
		if strings.HasPrefix(call, "create_pr:") {
			createPRCalls++
		}
	}
	if createPRCalls != 1 {
		t.Fatalf("expected exactly one parent CreatePR call, got %d calls in %v", createPRCalls, vcs.calls)
	}
	if got := mgr.dataByID["root"]["parent_pr_url"]; got != "https://arc.example.test/review/123" {
		t.Fatalf("expected parent PR URL to be persisted, got %q", got)
	}
}

func TestParentFinalizerCreatesPRFromStartrekCommentBackedSplitMarker(t *testing.T) {
	ctx := context.Background()
	storage := &commentBackedSplitMarkerStorage{
		spyStorageBackend: newSpyStorageBackend([]contracts.Task{
			{ID: "root", Title: "Parent issue", Status: contracts.TaskStatusClosed},
			{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusClosed, ParentID: "root"},
			{ID: "t-2", Title: "Task 2", Status: contracts.TaskStatusClosed, ParentID: "root"},
		}, nil),
	}
	store := startrek.SplitMarkerStore{
		Tracker:      storage,
		SplitVersion: "strict-v1",
	}
	if err := store.Write(ctx, "root", startrek.SplitMarker{
		Version:    "strict-v1",
		SubtaskIDs: []string{"t-1", "t-2"},
	}); err != nil {
		t.Fatalf("write split marker: %v", err)
	}
	if marker, ok, err := store.Read(ctx, "root"); err != nil {
		t.Fatalf("read split marker precondition: %v", err)
	} else if !ok || len(marker.SubtaskIDs) != 2 {
		t.Fatalf("expected readable comment-backed split marker, got marker=%#v ok=%v", marker, ok)
	}

	vcs := &fakeArcPRVCS{}
	manager := newStorageEngineTaskManager(storage, nil, "root")
	created, err := newParentFinalizer(manager).FinalizeIfReady(ctx, "root", vcs)
	if err != nil {
		t.Fatalf("finalize parent: %v", err)
	}
	if !created {
		t.Fatal("expected parent finalization from comment-backed split marker")
	}

	createPRCalls := 0
	for _, call := range vcs.calls {
		if strings.HasPrefix(call, "create_pr:") {
			createPRCalls++
			if !strings.Contains(call, "- t-1") || !strings.Contains(call, "- t-2") {
				t.Fatalf("parent PR body did not include marker subtasks: %q", call)
			}
		}
	}
	if createPRCalls != 1 {
		t.Fatalf("expected exactly one parent CreatePR call, got %d calls in %v", createPRCalls, vcs.calls)
	}

	parent, err := storage.GetTask(ctx, "root")
	if err != nil {
		t.Fatalf("get parent task: %v", err)
	}
	if got := parent.Metadata["parent_pr_url"]; got != "https://arc.example.test/review/123" {
		t.Fatalf("expected parent PR URL to be persisted, got %q", got)
	}
}

type commentBackedSplitMarkerStorage struct {
	*spyStorageBackend
	commentsByIssue map[string][]startrek.IssueComment
}

func (s *commentBackedSplitMarkerStorage) GetIssueComments(_ context.Context, issueID string) ([]startrek.IssueComment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]startrek.IssueComment(nil), s.commentsByIssue[issueID]...), nil
}

func (s *commentBackedSplitMarkerStorage) CreateIssueComment(_ context.Context, issueID string, opts startrek.IssueCommentCreateOptions) (startrek.IssueComment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.commentsByIssue == nil {
		s.commentsByIssue = map[string][]startrek.IssueComment{}
	}
	body := strings.TrimSpace(opts.Body)
	if marker := strings.TrimSpace(opts.Marker); marker != "" {
		body = "<!-- yolo-runner:" + marker + " -->\n\n" + body
	}
	comment := startrek.IssueComment{Body: body}
	s.commentsByIssue[issueID] = append(s.commentsByIssue[issueID], comment)
	return comment, nil
}
