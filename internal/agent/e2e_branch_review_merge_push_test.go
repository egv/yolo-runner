package agent

import (
	"context"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anomalyco/yolo-runner/internal/contracts"
	"github.com/anomalyco/yolo-runner/internal/tk"
)

func TestE2EBranchReviewMergePushWithSeededTKBacklog(t *testing.T) {
	if _, err := exec.LookPath("tk"); err != nil {
		t.Skip("tk CLI is required for e2e test")
	}

	repo := t.TempDir()
	r := tkCommandRunner{dir: repo}
	rootID := mustTKCreate(t, r, "Roadmap", "epic", "0", "")
	taskID := mustTKCreate(t, r, "Implement task", "task", "0", rootID)

	mgr := tk.NewTaskManager(r)
	runner := &fakeRunner{results: []contracts.RunnerResult{
		{Status: contracts.RunnerResultCompleted},
		{Status: contracts.RunnerResultCompleted, ReviewReady: true},
	}}
	vcs := &fakeVCS{}
	loop := NewLoop(mgr, runner, nil, LoopOptions{
		ParentID:       rootID,
		MaxTasks:       1,
		RepoRoot:       repo,
		VCS:            vcs,
		RequireReview:  true,
		MergeOnSuccess: true,
	})

	summary, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if summary.Completed != 1 {
		t.Fatalf("expected one completed task, got %#v", summary)
	}

	if len(runner.modes) != 2 || runner.modes[0] != contracts.RunnerModeImplement || runner.modes[1] != contracts.RunnerModeReview {
		t.Fatalf("expected implement+review run sequence, got %#v", runner.modes)
	}

	if !containsCall(vcs.calls, "create_branch:"+taskID) {
		t.Fatalf("expected per-task branch creation, got %v", vcs.calls)
	}
	if !containsCall(vcs.calls, "merge_to_main:task/"+taskID) {
		t.Fatalf("expected merge-to-main call, got %v", vcs.calls)
	}
	if !containsCall(vcs.calls, "push_main") {
		t.Fatalf("expected push-main call, got %v", vcs.calls)
	}

	task, err := mgr.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("get task failed: %v", err)
	}
	if task.Status != contracts.TaskStatusClosed {
		t.Fatalf("expected task closed after successful flow, got %s", task.Status)
	}
}

func TestE2EMergeQueueSerializesParallelBranchLandingOrder(t *testing.T) {
	if _, err := exec.LookPath("tk"); err != nil {
		t.Skip("tk CLI is required for e2e test")
	}

	repo := t.TempDir()
	r := tkCommandRunner{dir: repo}
	rootID := mustTKCreate(t, r, "Roadmap", "epic", "0", "")
	firstTaskID := mustTKCreate(t, r, "Task 1", "task", "0", rootID)
	secondTaskID := mustTKCreate(t, r, "Task 2", "task", "1", rootID)

	mgr := tk.NewTaskManager(r)
	vcs := newContentionLandingVCS()
	runner := newOrderedLandingRunner(firstTaskID, vcs.FirstMergeStarted())
	sink := &syncRecordingSink{}
	loop := NewLoop(mgr, runner, sink, LoopOptions{
		ParentID:       rootID,
		MaxTasks:       2,
		Concurrency:    2,
		RepoRoot:       repo,
		VCS:            vcs,
		RequireReview:  true,
		MergeOnSuccess: true,
	})

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

	if !vcs.WaitForFirstMergeStarted(2 * time.Second) {
		t.Fatal("expected first merge attempt to start")
	}
	if !runner.WaitForSecondReviewReleased(2 * time.Second) {
		t.Fatal("expected second task review to be released after first merge started")
	}
	if !sink.WaitForEventCount(contracts.EventTypeMergeQueued, 2, 2*time.Second) {
		t.Fatal("expected both branches to be queued for landing contention")
	}
	if got := vcs.MergeCalls(); got != 1 {
		t.Fatalf("expected second merge to wait behind landing lock, got merge calls=%d", got)
	}
	vcs.ReleaseFirstMerge()

	select {
	case result := <-resultCh:
		if result.err != nil {
			t.Fatalf("loop failed: %v", result.err)
		}
		if result.summary.Completed != 2 {
			t.Fatalf("expected two completed tasks, got %#v", result.summary)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("loop timed out while resolving merge queue contention")
	}

	for _, taskID := range []string{firstTaskID, secondTaskID} {
		task, err := mgr.GetTask(context.Background(), taskID)
		if err != nil {
			t.Fatalf("get task failed for %s: %v", taskID, err)
		}
		if task.Status != contracts.TaskStatusClosed {
			t.Fatalf("expected task %s closed after landing, got %s", taskID, task.Status)
		}
	}

	if got := vcs.MaxMergeInFlight(); got != 1 {
		t.Fatalf("expected serialized merge execution, max in-flight merges=%d", got)
	}

	gotOps := vcs.Operations()
	wantOps := []string{
		"merge:task/" + firstTaskID,
		"push:task/" + firstTaskID,
		"merge:task/" + secondTaskID,
		"push:task/" + secondTaskID,
	}
	if len(gotOps) != len(wantOps) {
		t.Fatalf("expected landing operations %v, got %v", wantOps, gotOps)
	}
	for i := range wantOps {
		if gotOps[i] != wantOps[i] {
			t.Fatalf("expected landing operations %v, got %v", wantOps, gotOps)
		}
	}
}

type orderedLandingRunner struct {
	firstTaskID          string
	firstMergeStarted    <-chan struct{}
	secondReviewReleased chan struct{}
	secondReviewOnce     sync.Once
}

func newOrderedLandingRunner(firstTaskID string, firstMergeStarted <-chan struct{}) *orderedLandingRunner {
	return &orderedLandingRunner{
		firstTaskID:          firstTaskID,
		firstMergeStarted:    firstMergeStarted,
		secondReviewReleased: make(chan struct{}),
	}
}

func (r *orderedLandingRunner) Run(ctx context.Context, request contracts.RunnerRequest) (contracts.RunnerResult, error) {
	switch request.Mode {
	case contracts.RunnerModeImplement:
		return contracts.RunnerResult{Status: contracts.RunnerResultCompleted}, nil
	case contracts.RunnerModeReview:
		if request.TaskID != r.firstTaskID {
			select {
			case <-r.firstMergeStarted:
			case <-ctx.Done():
				return contracts.RunnerResult{}, ctx.Err()
			}
			r.secondReviewOnce.Do(func() {
				close(r.secondReviewReleased)
			})
		}
		return contracts.RunnerResult{Status: contracts.RunnerResultCompleted, ReviewReady: true}, nil
	default:
		return contracts.RunnerResult{Status: contracts.RunnerResultFailed, Reason: "unsupported mode"}, nil
	}
}

func (r *orderedLandingRunner) WaitForSecondReviewReleased(timeout time.Duration) bool {
	select {
	case <-r.secondReviewReleased:
		return true
	case <-time.After(timeout):
		return false
	}
}

type contentionLandingVCS struct {
	mu                 sync.Mutex
	operations         []string
	mergeCalls         int
	mergeInFlight      int
	maxMergeInFlight   int
	currentMergeBranch string

	firstMergeStarted chan struct{}
	firstMergeOnce    sync.Once
	releaseFirstMerge chan struct{}
	releaseFirstOnce  sync.Once
}

func newContentionLandingVCS() *contentionLandingVCS {
	return &contentionLandingVCS{
		firstMergeStarted: make(chan struct{}),
		releaseFirstMerge: make(chan struct{}),
	}
}

func (v *contentionLandingVCS) EnsureMain(context.Context) error { return nil }

func (v *contentionLandingVCS) CreateTaskBranch(_ context.Context, taskID string) (string, error) {
	return "task/" + taskID, nil
}

func (v *contentionLandingVCS) Checkout(context.Context, string) error { return nil }

func (v *contentionLandingVCS) CommitAll(context.Context, string) (string, error) { return "", nil }

func (v *contentionLandingVCS) MergeToMain(_ context.Context, branch string) error {
	v.mu.Lock()
	v.mergeCalls++
	call := v.mergeCalls
	v.mergeInFlight++
	if v.mergeInFlight > v.maxMergeInFlight {
		v.maxMergeInFlight = v.mergeInFlight
	}
	v.currentMergeBranch = branch
	v.operations = append(v.operations, "merge:"+branch)
	v.mu.Unlock()

	if call == 1 {
		v.firstMergeOnce.Do(func() {
			close(v.firstMergeStarted)
		})
		<-v.releaseFirstMerge
	}

	v.mu.Lock()
	v.mergeInFlight--
	v.mu.Unlock()
	return nil
}

func (v *contentionLandingVCS) PushBranch(context.Context, string) error { return nil }

func (v *contentionLandingVCS) PushMain(context.Context) error {
	v.mu.Lock()
	v.operations = append(v.operations, "push:"+v.currentMergeBranch)
	v.mu.Unlock()
	return nil
}

func (v *contentionLandingVCS) FirstMergeStarted() <-chan struct{} {
	return v.firstMergeStarted
}

func (v *contentionLandingVCS) WaitForFirstMergeStarted(timeout time.Duration) bool {
	select {
	case <-v.firstMergeStarted:
		return true
	case <-time.After(timeout):
		return false
	}
}

func (v *contentionLandingVCS) ReleaseFirstMerge() {
	v.releaseFirstOnce.Do(func() {
		close(v.releaseFirstMerge)
	})
}

func (v *contentionLandingVCS) MergeCalls() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.mergeCalls
}

func (v *contentionLandingVCS) MaxMergeInFlight() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.maxMergeInFlight
}

func (v *contentionLandingVCS) Operations() []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	out := make([]string, len(v.operations))
	copy(out, v.operations)
	return out
}

type syncRecordingSink struct {
	mu     sync.Mutex
	events []contracts.Event
}

func (s *syncRecordingSink) Emit(_ context.Context, event contracts.Event) error {
	s.mu.Lock()
	s.events = append(s.events, event)
	s.mu.Unlock()
	return nil
}

func (s *syncRecordingSink) WaitForEventCount(eventType contracts.EventType, count int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if s.eventCount(eventType) >= count {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return s.eventCount(eventType) >= count
}

func (s *syncRecordingSink) eventCount(eventType contracts.EventType) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for _, event := range s.events {
		if event.Type == eventType {
			count++
		}
	}
	return count
}

type tkCommandRunner struct{ dir string }

func (r tkCommandRunner) Run(args ...string) (string, error) {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = r.dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func mustTKCreate(t *testing.T, runner tkCommandRunner, title string, issueType string, priority string, parent string) string {
	t.Helper()
	args := []string{"tk", "create", title, "-t", issueType, "-p", priority}
	if parent != "" {
		args = append(args, "--parent", parent)
	}
	out, err := runner.Run(args...)
	if err != nil {
		t.Fatalf("create ticket failed: %v (%s)", err, out)
	}
	return strings.TrimSpace(out)
}
