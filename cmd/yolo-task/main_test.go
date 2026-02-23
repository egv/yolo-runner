package main

import (
	"bytes"
	"context"
	"testing"

	"github.com/egv/yolo-runner/internal/contracts"
)

func TestRunMainNextPrintsTaskIDs(t *testing.T) {
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	mgr := &fakeTaskManager{
		next: []contracts.TaskSummary{{ID: "t-1"}, {ID: "t-2"}},
	}

	code := RunMain([]string{"next", "--root", "root-1"}, out, errOut, mgr)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d stderr=%q", code, errOut.String())
	}
	if out.String() != "t-1\nt-2\n" {
		t.Fatalf("unexpected output: %q", out.String())
	}
}

func TestRunMainStatusUpdatesTask(t *testing.T) {
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	mgr := &fakeTaskManager{}

	code := RunMain([]string{"status", "--id", "t-1", "--status", "blocked"}, out, errOut, mgr)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d stderr=%q", code, errOut.String())
	}
	if mgr.lastStatusID != "t-1" || mgr.lastStatus != contracts.TaskStatusBlocked {
		t.Fatalf("status update not forwarded: id=%q status=%q", mgr.lastStatusID, mgr.lastStatus)
	}
}

func TestRunMainDataWritesTaskData(t *testing.T) {
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	mgr := &fakeTaskManager{}

	code := RunMain([]string{"data", "--id", "t-1", "--set", "retry_count=2", "--set", "blocked_reason=timeout"}, out, errOut, mgr)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d stderr=%q", code, errOut.String())
	}
	if mgr.lastDataID != "t-1" {
		t.Fatalf("expected data id t-1, got %q", mgr.lastDataID)
	}
	if mgr.lastData["retry_count"] != "2" || mgr.lastData["blocked_reason"] != "timeout" {
		t.Fatalf("unexpected data map: %#v", mgr.lastData)
	}
}

type fakeTaskManager struct {
	next         []contracts.TaskSummary
	lastStatusID string
	lastStatus   contracts.TaskStatus
	lastDataID   string
	lastData     map[string]string
}

func (f *fakeTaskManager) NextTasks(context.Context, string) ([]contracts.TaskSummary, error) {
	return f.next, nil
}

func (f *fakeTaskManager) GetTask(context.Context, string) (contracts.Task, error) {
	return contracts.Task{}, nil
}

func (f *fakeTaskManager) SetTaskStatus(_ context.Context, taskID string, status contracts.TaskStatus) error {
	f.lastStatusID = taskID
	f.lastStatus = status
	return nil
}

func (f *fakeTaskManager) SetTaskData(_ context.Context, taskID string, data map[string]string) error {
	f.lastDataID = taskID
	f.lastData = data
	return nil
}
