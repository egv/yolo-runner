package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/egv/yolo-runner/v2/internal/contracts"
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
