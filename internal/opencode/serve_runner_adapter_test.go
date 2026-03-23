package opencode

import (
	"context"
	"errors"
	"testing"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

// stubServeRuntime implements contracts.TaskSessionRuntime for testing.
type stubServeRuntime struct {
	session contracts.TaskSession
	err     error
}

func (r *stubServeRuntime) Start(_ context.Context, _ contracts.TaskSessionStartRequest) (contracts.TaskSession, error) {
	return r.session, r.err
}

// stubServeSession implements contracts.TaskSession and serveSessionWaiter for testing.
type stubServeSession struct {
	executeErr    error
	waitResultFn  func(ctx context.Context) error
}

func (s *stubServeSession) ID() string { return "stub-session-1" }
func (s *stubServeSession) WaitReady(_ context.Context) error { return nil }
func (s *stubServeSession) Execute(_ context.Context, _ contracts.TaskSessionExecuteRequest) error {
	return s.executeErr
}
func (s *stubServeSession) Cancel(_ context.Context, _ contracts.TaskSessionCancellation) error {
	return nil
}
func (s *stubServeSession) Teardown(_ context.Context, _ contracts.TaskSessionTeardown) error {
	return nil
}
func (s *stubServeSession) waitWithContext(ctx context.Context) error {
	if s.waitResultFn != nil {
		return s.waitResultFn(ctx)
	}
	return nil
}

func newStubServeAdapter(session *stubServeSession) *ServeRunnerAdapter {
	return &ServeRunnerAdapter{
		runtime: &stubServeRuntime{session: session},
	}
}

func TestServeRunnerAdapterRunReturnsCompletedWhenSessionProcessExitsCleanly(t *testing.T) {
	session := &stubServeSession{waitResultFn: func(_ context.Context) error { return nil }}
	adapter := newStubServeAdapter(session)

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		TaskID:   "task-complete",
		RepoRoot: t.TempDir(),
		Prompt:   "do something",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != contracts.RunnerResultCompleted {
		t.Fatalf("expected completed, got %s (reason: %q)", result.Status, result.Reason)
	}
}

func TestServeRunnerAdapterRunReturnsFailedWhenExecuteFails(t *testing.T) {
	session := &stubServeSession{executeErr: errors.New("execute failed")}
	adapter := newStubServeAdapter(session)

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		TaskID:   "task-exec-fail",
		RepoRoot: t.TempDir(),
		Prompt:   "do something",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != contracts.RunnerResultFailed {
		t.Fatalf("expected failed, got %s (reason: %q)", result.Status, result.Reason)
	}
}

func TestServeRunnerAdapterRunReturnsBlockedOnTimeout(t *testing.T) {
	session := &stubServeSession{
		waitResultFn: func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		},
	}
	adapter := newStubServeAdapter(session)

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		TaskID:   "task-timeout",
		RepoRoot: t.TempDir(),
		Prompt:   "do something",
		Timeout:  1, // 1 nanosecond — immediate timeout
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != contracts.RunnerResultBlocked {
		t.Fatalf("expected blocked, got %s (reason: %q)", result.Status, result.Reason)
	}
}

func TestServeRunnerAdapterImplementsContract(t *testing.T) {
	var _ contracts.AgentRunner = (*ServeRunnerAdapter)(nil)
}
