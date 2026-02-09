package agent

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anomalyco/yolo-runner/internal/contracts"
)

func TestLoopCompletesTask(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{{Status: contracts.RunnerResultCompleted}}}
	loop := NewLoop(mgr, run, nil, LoopOptions{ParentID: "root", MaxRetries: 1})

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if summary.Completed != 1 || summary.TotalProcessed() != 1 {
		t.Fatalf("unexpected summary: %#v", summary)
	}
	if mgr.statusByID["t-1"] != contracts.TaskStatusClosed {
		t.Fatalf("expected task closed, got %s", mgr.statusByID["t-1"])
	}
}

func TestLoopRetriesFailedTaskThenCompletes(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{
		{Status: contracts.RunnerResultFailed, Reason: "first failure"},
		{Status: contracts.RunnerResultCompleted},
	}}
	loop := NewLoop(mgr, run, nil, LoopOptions{ParentID: "root", MaxRetries: 2})

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if summary.Completed != 1 {
		t.Fatalf("expected completion after retry, got %#v", summary)
	}
	if got := mgr.dataByID["t-1"]["retry_count"]; got != "1" {
		t.Fatalf("expected retry_count=1, got %q", got)
	}
}

func TestLoopMarksFailedAfterRetryExhausted(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{
		{Status: contracts.RunnerResultFailed, Reason: "first"},
		{Status: contracts.RunnerResultFailed, Reason: "second"},
	}}
	loop := NewLoop(mgr, run, nil, LoopOptions{ParentID: "root", MaxRetries: 1})

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if summary.Failed != 1 {
		t.Fatalf("expected failed summary, got %#v", summary)
	}
	if mgr.statusByID["t-1"] != contracts.TaskStatusFailed {
		t.Fatalf("expected failed status, got %s", mgr.statusByID["t-1"])
	}
	if got := mgr.dataByID["t-1"]["triage_status"]; got != "failed" {
		t.Fatalf("expected triage_status=failed, got %q", got)
	}
	if got := mgr.dataByID["t-1"]["triage_reason"]; got != "second" {
		t.Fatalf("expected triage_reason=second, got %q", got)
	}
}

func TestLoopMarksBlockedWithReason(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{{Status: contracts.RunnerResultBlocked, Reason: "needs manual input"}}}
	loop := NewLoop(mgr, run, nil, LoopOptions{ParentID: "root", MaxRetries: 0})

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if summary.Blocked != 1 {
		t.Fatalf("expected blocked summary, got %#v", summary)
	}
	if mgr.statusByID["t-1"] != contracts.TaskStatusBlocked {
		t.Fatalf("expected blocked status, got %s", mgr.statusByID["t-1"])
	}
	if got := mgr.dataByID["t-1"]["triage_status"]; got != "blocked" {
		t.Fatalf("expected triage_status=blocked, got %q", got)
	}
	if got := mgr.dataByID["t-1"]["triage_reason"]; got != "needs manual input" {
		t.Fatalf("expected triage_reason to be saved, got %q", got)
	}
}

func TestLoopCreatesAndChecksOutTaskBranchBeforeRun(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{{Status: contracts.RunnerResultCompleted}}}
	vcs := &fakeVCS{}
	loop := NewLoop(mgr, run, nil, LoopOptions{ParentID: "root", MaxRetries: 0, VCS: vcs})

	_, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}

	if len(vcs.calls) != 3 {
		t.Fatalf("expected 3 vcs calls, got %v", vcs.calls)
	}
	if vcs.calls[0] != "ensure_main" {
		t.Fatalf("expected ensure_main first, got %v", vcs.calls)
	}
	if vcs.calls[1] != "create_branch:t-1" {
		t.Fatalf("expected create branch for task, got %v", vcs.calls)
	}
	if vcs.calls[2] != "checkout:task/t-1" {
		t.Fatalf("expected checkout of task branch, got %v", vcs.calls)
	}
}

type fakeTaskManager struct {
	mu         sync.Mutex
	queue      []contracts.Task
	statusByID map[string]contracts.TaskStatus
	dataByID   map[string]map[string]string
}

func newFakeTaskManager(tasks ...contracts.Task) *fakeTaskManager {
	status := map[string]contracts.TaskStatus{}
	for _, task := range tasks {
		status[task.ID] = task.Status
	}
	return &fakeTaskManager{queue: tasks, statusByID: status, dataByID: map[string]map[string]string{}}
}

func (f *fakeTaskManager) NextTasks(context.Context, string) ([]contracts.TaskSummary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var tasks []contracts.TaskSummary
	for _, task := range f.queue {
		if f.statusByID[task.ID] == contracts.TaskStatusOpen {
			tasks = append(tasks, contracts.TaskSummary{ID: task.ID, Title: task.Title})
		}
	}
	return tasks, nil
}

func (f *fakeTaskManager) GetTask(_ context.Context, taskID string) (contracts.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, task := range f.queue {
		if task.ID == taskID {
			copy := task
			copy.Status = f.statusByID[taskID]
			return copy, nil
		}
	}
	return contracts.Task{}, errors.New("missing task")
}

func (f *fakeTaskManager) SetTaskStatus(_ context.Context, taskID string, status contracts.TaskStatus) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statusByID[taskID] = status
	return nil
}

func (f *fakeTaskManager) SetTaskData(_ context.Context, taskID string, data map[string]string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.dataByID[taskID] == nil {
		f.dataByID[taskID] = map[string]string{}
	}
	for key, value := range data {
		f.dataByID[taskID][key] = value
	}
	return nil
}

type fakeRunner struct {
	results  []contracts.RunnerResult
	idx      int
	modes    []contracts.RunnerMode
	requests []contracts.RunnerRequest
}

func (f *fakeRunner) Run(_ context.Context, request contracts.RunnerRequest) (contracts.RunnerResult, error) {
	f.modes = append(f.modes, request.Mode)
	f.requests = append(f.requests, request)
	if f.idx >= len(f.results) {
		return contracts.RunnerResult{Status: contracts.RunnerResultFailed, Reason: "missing result"}, nil
	}
	result := f.results[f.idx]
	f.idx++
	return result, nil
}

type noopSink struct{}

func (noopSink) Emit(context.Context, contracts.Event) error { return nil }

type fakeVCS struct {
	calls []string
}

func (f *fakeVCS) EnsureMain(context.Context) error {
	f.calls = append(f.calls, "ensure_main")
	return nil
}

func (f *fakeVCS) CreateTaskBranch(_ context.Context, taskID string) (string, error) {
	f.calls = append(f.calls, "create_branch:"+taskID)
	return "task/" + taskID, nil
}

func (f *fakeVCS) Checkout(_ context.Context, ref string) error {
	f.calls = append(f.calls, "checkout:"+ref)
	return nil
}

func (f *fakeVCS) CommitAll(context.Context, string) (string, error) { return "", nil }

func (f *fakeVCS) MergeToMain(_ context.Context, branch string) error {
	f.calls = append(f.calls, "merge_to_main:"+branch)
	return nil
}

func (f *fakeVCS) PushBranch(_ context.Context, branch string) error {
	f.calls = append(f.calls, "push_branch:"+branch)
	return nil
}

func (f *fakeVCS) PushMain(context.Context) error {
	f.calls = append(f.calls, "push_main")
	return nil
}

func TestLoopRunsReviewAfterImplementationSuccess(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{
		{Status: contracts.RunnerResultCompleted},
		{Status: contracts.RunnerResultCompleted},
	}}
	loop := NewLoop(mgr, run, nil, LoopOptions{ParentID: "root", MaxRetries: 0, RequireReview: true})

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if summary.Completed != 1 {
		t.Fatalf("expected completed summary, got %#v", summary)
	}
	if len(run.modes) != 2 {
		t.Fatalf("expected two runner calls, got %d", len(run.modes))
	}
	if run.modes[0] != contracts.RunnerModeImplement || run.modes[1] != contracts.RunnerModeReview {
		t.Fatalf("unexpected runner mode sequence: %#v", run.modes)
	}
}

func TestLoopFailsTaskWhenReviewFails(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{
		{Status: contracts.RunnerResultCompleted},
		{Status: contracts.RunnerResultFailed, Reason: "review rejected"},
	}}
	loop := NewLoop(mgr, run, nil, LoopOptions{ParentID: "root", MaxRetries: 0, RequireReview: true})

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if summary.Failed != 1 {
		t.Fatalf("expected failed summary, got %#v", summary)
	}
	if mgr.statusByID["t-1"] != contracts.TaskStatusFailed {
		t.Fatalf("expected failed task status, got %s", mgr.statusByID["t-1"])
	}
}

func TestLoopMergesAndPushesAfterSuccessfulReview(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{
		{Status: contracts.RunnerResultCompleted},
		{Status: contracts.RunnerResultCompleted},
	}}
	vcs := &fakeVCS{}
	loop := NewLoop(mgr, run, nil, LoopOptions{ParentID: "root", MaxRetries: 0, RequireReview: true, MergeOnSuccess: true, VCS: vcs})

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if summary.Completed != 1 {
		t.Fatalf("expected completed summary, got %#v", summary)
	}
	if !containsCall(vcs.calls, "merge_to_main:task/t-1") {
		t.Fatalf("expected merge_to_main call, got %v", vcs.calls)
	}
	if !containsCall(vcs.calls, "push_main") {
		t.Fatalf("expected push_main call, got %v", vcs.calls)
	}
}

func containsCall(calls []string, want string) bool {
	for _, call := range calls {
		if call == want {
			return true
		}
	}
	return false
}

func TestLoopRespectsMaxTasksLimit(t *testing.T) {
	mgr := newFakeTaskManager(
		contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen},
		contracts.Task{ID: "t-2", Title: "Task 2", Status: contracts.TaskStatusOpen},
	)
	run := &fakeRunner{results: []contracts.RunnerResult{
		{Status: contracts.RunnerResultCompleted},
		{Status: contracts.RunnerResultCompleted},
	}}
	loop := NewLoop(mgr, run, nil, LoopOptions{ParentID: "root", MaxTasks: 1})

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if summary.Completed != 1 {
		t.Fatalf("expected exactly one completion, got %#v", summary)
	}
	if mgr.statusByID["t-2"] != contracts.TaskStatusOpen {
		t.Fatalf("expected second task to remain open")
	}
}

func TestLoopDryRunSkipsExecution(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{{Status: contracts.RunnerResultCompleted}}}
	loop := NewLoop(mgr, run, nil, LoopOptions{ParentID: "root", DryRun: true})

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if summary.Skipped != 1 {
		t.Fatalf("expected skipped summary for dry run, got %#v", summary)
	}
	if len(run.modes) != 0 {
		t.Fatalf("runner should not be called in dry run")
	}
}

func TestLoopStopsWhenSignalChannelCloses(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{{Status: contracts.RunnerResultCompleted}}}
	stop := make(chan struct{})
	close(stop)
	loop := NewLoop(mgr, run, nil, LoopOptions{ParentID: "root", Stop: stop})

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if summary.TotalProcessed() != 0 {
		t.Fatalf("expected no processed tasks when stop already closed, got %#v", summary)
	}
}

func TestLoopBuildsRunnerRequestWithRepoAndModel(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Description: "Do work", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{{Status: contracts.RunnerResultCompleted}}}
	loop := NewLoop(mgr, run, nil, LoopOptions{ParentID: "root", RepoRoot: "/repo", Model: "openai/gpt-5.3-codex", RunnerTimeout: 3 * time.Second})

	_, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}

	if len(run.requests) != 1 {
		t.Fatalf("expected one runner request, got %d", len(run.requests))
	}
	req := run.requests[0]
	if req.RepoRoot != "/repo" {
		t.Fatalf("expected repo root /repo, got %q", req.RepoRoot)
	}
	if req.Model != "openai/gpt-5.3-codex" {
		t.Fatalf("expected model to be set, got %q", req.Model)
	}
	if req.Prompt == "" {
		t.Fatalf("expected non-empty prompt")
	}
	if req.Timeout != 3*time.Second {
		t.Fatalf("expected timeout=3, got %s", req.Timeout)
	}
}

func TestLoopEmitsLifecycleEvents(t *testing.T) {
	mgr := newFakeTaskManager(contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen})
	run := &fakeRunner{results: []contracts.RunnerResult{{Status: contracts.RunnerResultCompleted}}}
	sink := &recordingSink{}
	loop := NewLoop(mgr, run, sink, LoopOptions{ParentID: "root"})

	_, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if len(sink.events) == 0 {
		t.Fatalf("expected emitted events")
	}
	if sink.events[0].Type != contracts.EventTypeTaskStarted {
		t.Fatalf("expected first event task_started, got %s", sink.events[0].Type)
	}
	if sink.events[0].TaskTitle != "Task 1" {
		t.Fatalf("expected task title in event, got %q", sink.events[0].TaskTitle)
	}
	if !hasEventType(sink.events, contracts.EventTypeRunnerStarted) {
		t.Fatalf("expected runner_started event")
	}
	if !hasEventType(sink.events, contracts.EventTypeRunnerFinished) {
		t.Fatalf("expected runner_finished event")
	}
	if !hasEventType(sink.events, contracts.EventTypeTaskFinished) {
		t.Fatalf("expected task_finished event")
	}
}

func TestLoopHonorsConcurrencyLimit(t *testing.T) {
	mgr := newFakeTaskManager(
		contracts.Task{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen},
		contracts.Task{ID: "t-2", Title: "Task 2", Status: contracts.TaskStatusOpen},
		contracts.Task{ID: "t-3", Title: "Task 3", Status: contracts.TaskStatusOpen},
		contracts.Task{ID: "t-4", Title: "Task 4", Status: contracts.TaskStatusOpen},
	)
	run := &blockingRunner{
		release: make(chan struct{}),
	}
	loop := NewLoop(mgr, run, nil, LoopOptions{ParentID: "root", RequireReview: false, Concurrency: 2})

	resultCh := make(chan struct {
		summary contracts.LoopSummary
		err     error
	}, 1)
	go func() {
		summary, err := loop.Run(context.Background())
		resultCh <- struct {
			summary contracts.LoopSummary
			err     error
		}{summary: summary, err: err}
	}()

	deadline := time.After(2 * time.Second)
	for {
		if atomic.LoadInt32(&run.maxActive) >= 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("expected at least 2 concurrent executions, got %d", atomic.LoadInt32(&run.maxActive))
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}

	close(run.release)
	result := <-resultCh
	if result.err != nil {
		t.Fatalf("loop failed: %v", result.err)
	}
	if result.summary.Completed != 4 {
		t.Fatalf("expected all tasks completed, got %#v", result.summary)
	}
	if got := atomic.LoadInt32(&run.maxActive); got > 2 {
		t.Fatalf("expected max active executions <= 2, got %d", got)
	}
}

func hasEventType(events []contracts.Event, eventType contracts.EventType) bool {
	for _, event := range events {
		if event.Type == eventType {
			return true
		}
	}
	return false
}

type recordingSink struct{ events []contracts.Event }

func (r *recordingSink) Emit(_ context.Context, event contracts.Event) error {
	r.events = append(r.events, event)
	return nil
}

type blockingRunner struct {
	release   chan struct{}
	active    int32
	maxActive int32
}

func (b *blockingRunner) Run(_ context.Context, _ contracts.RunnerRequest) (contracts.RunnerResult, error) {
	active := atomic.AddInt32(&b.active, 1)
	for {
		maxActive := atomic.LoadInt32(&b.maxActive)
		if active <= maxActive {
			break
		}
		if atomic.CompareAndSwapInt32(&b.maxActive, maxActive, active) {
			break
		}
	}
	<-b.release
	atomic.AddInt32(&b.active, -1)
	return contracts.RunnerResult{Status: contracts.RunnerResultCompleted}, nil
}
