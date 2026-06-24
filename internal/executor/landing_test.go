package executor

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

func TestRunLandingRemediatesMergeConflictBeforeRetry(t *testing.T) {
	task := contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen, ParentID: "root"}
	mgr := newLandingFakeTaskManager(task)
	runner := &landingFakeRunner{results: []contracts.RunnerResult{{Status: contracts.RunnerResultCompleted}}}
	vcs := &landingFakeVCS{mergeErrs: []error{
		errors.New("git merge --no-ff task/t-1 failed: CONFLICT (content): Merge conflict"),
		nil,
	}}
	sink := &landingRecordingSink{}
	lock := &landingRecordingLock{}
	markBlockedCalls := 0
	clearTerminalCalls := 0

	blocked, err := RunLanding(context.Background(), task, LandingDependencies{
		Tasks:       mgr,
		Runner:      runner,
		Events:      sink,
		VCS:         vcs,
		LandingLock: lock,
		MarkTaskBlockedWithData: func(taskID string, taskData map[string]string) error {
			markBlockedCalls++
			return nil
		},
		ClearTaskTerminalState: func(taskID string) error {
			clearTerminalCalls++
			return nil
		},
	}, LandingOptions{
		ParentID: "root",
		Backend:  "codex",
		Model:    "gpt-test",
		Runtime: TaskRuntimeConfig{
			Backend: "codex",
			Model:   "gpt-test",
		},
	}, LandingEventContext{
		TaskBranch: "task/t-1",
		WorkerID:   "worker-0",
		ClonePath:  t.TempDir(),
		QueuePos:   1,
	})
	if err != nil {
		t.Fatalf("RunLanding failed: %v", err)
	}
	if blocked {
		t.Fatalf("did not expect landing to block after successful remediation")
	}
	if vcs.mergeCalls != 2 {
		t.Fatalf("expected merge retry after remediation, got %d merge calls", vcs.mergeCalls)
	}
	if len(runner.requests) != 1 {
		t.Fatalf("expected one remediation runner request, got %d", len(runner.requests))
	}
	if !strings.Contains(runner.requests[0].Prompt, "Landing Merge Remediation:") {
		t.Fatalf("expected remediation prompt, got %q", runner.requests[0].Prompt)
	}
	if !strings.Contains(runner.requests[0].Prompt, "Merge Failure Details:") {
		t.Fatalf("expected merge failure details in remediation prompt, got %q", runner.requests[0].Prompt)
	}
	if !landingHasEventType(sink.events, contracts.EventTypeMergeRetry) {
		t.Fatalf("expected merge_retry event")
	}
	if !landingHasEventType(sink.events, contracts.EventTypeMergeLanded) {
		t.Fatalf("expected merge_landed event")
	}
	if !landingHasEventType(sink.events, contracts.EventTypeMergeCompleted) {
		t.Fatalf("expected merge_completed event")
	}
	if !landingHasEventType(sink.events, contracts.EventTypePushCompleted) {
		t.Fatalf("expected push_completed event")
	}
	if lock.lockCalls != 1 || lock.unlockCalls != 1 {
		t.Fatalf("expected one landing lock/unlock pair, got lock=%d unlock=%d", lock.lockCalls, lock.unlockCalls)
	}
	if markBlockedCalls != 0 || clearTerminalCalls != 0 {
		t.Fatalf("did not expect blocked callbacks, got mark=%d clear=%d", markBlockedCalls, clearTerminalCalls)
	}
}

func TestRunLandingRetriesMergeFailureOnceThenSucceeds(t *testing.T) {
	task := contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen, ParentID: "root"}
	vcs := &landingFakeVCS{mergeErrs: []error{errors.New("temporary merge failure"), nil}}
	runner := &landingFakeRunner{}
	sink := &landingRecordingSink{}

	blocked, err := RunLanding(context.Background(), task, LandingDependencies{
		Tasks:  newLandingFakeTaskManager(task),
		Runner: runner,
		Events: sink,
		VCS:    vcs,
	}, LandingOptions{ParentID: "root"}, LandingEventContext{
		TaskBranch: "task/t-1",
		WorkerID:   "worker-0",
		ClonePath:  t.TempDir(),
		QueuePos:   1,
	})
	if err != nil {
		t.Fatalf("RunLanding failed: %v", err)
	}
	if blocked {
		t.Fatalf("did not expect landing to block after retry success")
	}
	if vcs.mergeCalls != 2 {
		t.Fatalf("expected one retry with two merge attempts, got %d", vcs.mergeCalls)
	}
	if len(runner.requests) != 0 {
		t.Fatalf("did not expect non-conflict merge failure to run remediation, got %d requests", len(runner.requests))
	}
	if !landingHasEventType(sink.events, contracts.EventTypeMergeRetry) {
		t.Fatalf("expected merge_retry event")
	}
	if !landingHasEventType(sink.events, contracts.EventTypeMergeLanded) {
		t.Fatalf("expected merge_landed event")
	}
}

func TestRunLandingDefersArcPRLandingWithoutMergePushOrPRCreation(t *testing.T) {
	task := contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen, ParentID: "root"}
	vcs := &landingFakeArcPRVCS{}
	sink := &landingRecordingSink{}

	blocked, err := RunLanding(context.Background(), task, LandingDependencies{
		Tasks:  newLandingFakeTaskManager(task),
		Events: sink,
		VCS:    vcs,
	}, LandingOptions{ParentID: "root"}, LandingEventContext{
		TaskBranch: "task/t-1",
		WorkerID:   "worker-0",
		ClonePath:  t.TempDir(),
		QueuePos:   1,
	})
	if err != nil {
		t.Fatalf("RunLanding failed: %v", err)
	}
	if blocked {
		t.Fatalf("did not expect deferred arc landing to block")
	}
	if !landingContainsCallPrefix(vcs.calls, "commit_all:chore(task): auto-commit before landing t-1") {
		t.Fatalf("expected auto-commit before deferred arc landing, got %v", vcs.calls)
	}
	if landingContainsCall(vcs.calls, "merge_to_main:task/t-1") {
		t.Fatalf("did not expect merge_to_main during deferred arc landing, got %v", vcs.calls)
	}
	if landingContainsCall(vcs.calls, "push_main") {
		t.Fatalf("did not expect push_main during deferred arc landing, got %v", vcs.calls)
	}
	if landingContainsCallPrefix(vcs.calls, "create_pr:") {
		t.Fatalf("did not expect CreatePR during deferred arc landing, got %v", vcs.calls)
	}
	if !landingHasEventType(sink.events, contracts.EventTypeMergeLanded) {
		t.Fatalf("expected merge_landed event")
	}
}

type landingFakeTaskManager struct {
	tasks      map[string]contracts.Task
	statusByID map[string]contracts.TaskStatus
	dataByID   map[string]map[string]string
}

func newLandingFakeTaskManager(tasks ...contracts.Task) *landingFakeTaskManager {
	mgr := &landingFakeTaskManager{
		tasks:      map[string]contracts.Task{},
		statusByID: map[string]contracts.TaskStatus{},
		dataByID:   map[string]map[string]string{},
	}
	for _, task := range tasks {
		mgr.tasks[task.ID] = task
		mgr.statusByID[task.ID] = task.Status
	}
	return mgr
}

func (m *landingFakeTaskManager) NextTasks(context.Context, string) ([]contracts.TaskSummary, error) {
	return nil, nil
}

func (m *landingFakeTaskManager) GetTask(_ context.Context, taskID string) (contracts.Task, error) {
	task, ok := m.tasks[taskID]
	if !ok {
		return contracts.Task{}, errors.New("task not found")
	}
	return task, nil
}

func (m *landingFakeTaskManager) SetTaskStatus(_ context.Context, taskID string, status contracts.TaskStatus) error {
	m.statusByID[taskID] = status
	return nil
}

func (m *landingFakeTaskManager) SetTaskData(_ context.Context, taskID string, data map[string]string) error {
	if m.dataByID[taskID] == nil {
		m.dataByID[taskID] = map[string]string{}
	}
	for key, value := range data {
		m.dataByID[taskID][key] = value
	}
	return nil
}

type landingFakeRunner struct {
	results  []contracts.RunnerResult
	requests []contracts.RunnerRequest
}

func (r *landingFakeRunner) Run(_ context.Context, request contracts.RunnerRequest) (contracts.RunnerResult, error) {
	r.requests = append(r.requests, request)
	if len(r.results) == 0 {
		return contracts.RunnerResult{Status: contracts.RunnerResultCompleted}, nil
	}
	result := r.results[0]
	r.results = r.results[1:]
	return result, nil
}

type landingFakeVCS struct {
	calls      []string
	mergeErrs  []error
	mergeCalls int
}

func (v *landingFakeVCS) EnsureMain(context.Context) error { return nil }

func (v *landingFakeVCS) CreateTaskBranch(_ context.Context, taskID string) (string, error) {
	return "task/" + taskID, nil
}

func (v *landingFakeVCS) Checkout(_ context.Context, ref string) error {
	v.calls = append(v.calls, "checkout:"+ref)
	return nil
}

func (v *landingFakeVCS) CommitAll(_ context.Context, message string) (string, error) {
	v.calls = append(v.calls, "commit_all:"+message)
	return "abc123", nil
}

func (v *landingFakeVCS) MergeToMain(_ context.Context, branch string) error {
	v.calls = append(v.calls, "merge_to_main:"+branch)
	v.mergeCalls++
	if len(v.mergeErrs) == 0 {
		return nil
	}
	err := v.mergeErrs[0]
	v.mergeErrs = v.mergeErrs[1:]
	return err
}

func (v *landingFakeVCS) PushBranch(context.Context, string) error { return nil }

func (v *landingFakeVCS) PushMain(context.Context) error {
	v.calls = append(v.calls, "push_main")
	return nil
}

func (v *landingFakeVCS) CheckoutPRBranch(context.Context, string) (string, error) {
	return "", nil
}

func (v *landingFakeVCS) PushPRBranch(context.Context, string) error {
	return nil
}

type landingFakeArcPRVCS struct {
	landingFakeVCS
}

func (v *landingFakeArcPRVCS) CreatePR(_ context.Context, title string, body string) (string, error) {
	v.calls = append(v.calls, "create_pr:"+title+"\n"+body)
	return "https://arc.example.test/review/123", nil
}

type landingRecordingSink struct{ events []contracts.Event }

func (s *landingRecordingSink) Emit(_ context.Context, event contracts.Event) error {
	s.events = append(s.events, event)
	return nil
}

type landingRecordingLock struct {
	lockCalls   int
	unlockCalls int
}

func (l *landingRecordingLock) Lock() {
	l.lockCalls++
}

func (l *landingRecordingLock) Unlock() {
	l.unlockCalls++
}

func landingHasEventType(events []contracts.Event, eventType contracts.EventType) bool {
	for _, event := range events {
		if event.Type == eventType {
			return true
		}
	}
	return false
}

func landingContainsCall(calls []string, want string) bool {
	for _, call := range calls {
		if call == want {
			return true
		}
	}
	return false
}

func landingContainsCallPrefix(calls []string, wantPrefix string) bool {
	for _, call := range calls {
		if strings.HasPrefix(call, wantPrefix) {
			return true
		}
	}
	return false
}
