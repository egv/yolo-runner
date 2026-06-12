package agent

import (
	"context"
	"testing"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

func TestNewLoopDefaultsToInProcessDispatcher(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{{Status: contracts.RunnerResultCompleted}}}
	loop := NewLoop(mgr, run, nil, LoopOptions{ParentID: "root", MaxRetries: 1})

	if loop.options.Dispatcher == nil {
		t.Fatalf("expected default work dispatcher")
	}
	if _, ok := loop.options.Dispatcher.(*inProcessDispatcher); !ok {
		t.Fatalf("expected default dispatcher to be *inProcessDispatcher, got %T", loop.options.Dispatcher)
	}

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if summary.Completed != 1 {
		t.Fatalf("expected task to complete through default dispatcher, got %#v", summary)
	}
}
