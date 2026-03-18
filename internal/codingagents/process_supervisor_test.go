package codingagents

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeSupervisedProcess struct {
	waitCh     chan error
	stopCh     chan error
	killCh     chan error
	stopCalled chan struct{}
	killCalled chan struct{}
	stopCount  int
	killCount  int
}

func newFakeSupervisedProcess() *fakeSupervisedProcess {
	return &fakeSupervisedProcess{
		waitCh:     make(chan error, 1),
		stopCh:     make(chan error, 1),
		killCh:     make(chan error, 1),
		stopCalled: make(chan struct{}, 1),
		killCalled: make(chan struct{}, 1),
	}
}

func (p *fakeSupervisedProcess) Wait() error {
	return <-p.waitCh
}

func (p *fakeSupervisedProcess) Stop() error {
	p.stopCount++
	select {
	case p.stopCalled <- struct{}{}:
	default:
	}
	return <-p.stopCh
}

func (p *fakeSupervisedProcess) Kill() error {
	p.killCount++
	select {
	case p.killCalled <- struct{}{}:
	default:
	}
	return <-p.killCh
}

func TestProcessSupervisorRunStopsProcessAfterSuccessfulTask(t *testing.T) {
	proc := newFakeSupervisedProcess()
	proc.stopCh <- nil
	go func() {
		<-proc.stopCalled
		proc.waitCh <- nil
	}()

	supervisor := ProcessSupervisor{
		Start: func(context.Context) (SupervisedProcess, error) {
			return proc, nil
		},
		WaitReady: func(context.Context, SupervisedProcess) error {
			return nil
		},
		GracePeriod: 25 * time.Millisecond,
	}

	if err := supervisor.Run(context.Background(), func(context.Context, SupervisedProcess) error {
		return nil
	}); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if proc.stopCount != 1 {
		t.Fatalf("expected one graceful stop, got %d", proc.stopCount)
	}
	if proc.killCount != 0 {
		t.Fatalf("expected no force kill, got %d", proc.killCount)
	}
}

func TestProcessSupervisorRunForceKillsWhenGracefulShutdownTimesOut(t *testing.T) {
	proc := newFakeSupervisedProcess()
	proc.killCh <- nil

	supervisor := ProcessSupervisor{
		Start: func(context.Context) (SupervisedProcess, error) {
			return proc, nil
		},
		WaitReady: func(context.Context, SupervisedProcess) error {
			return nil
		},
		GracePeriod: 20 * time.Millisecond,
	}

	stopReleased := make(chan struct{})
	go func() {
		<-stopReleased
		proc.stopCh <- nil
	}()
	go func() {
		<-proc.killCalled
		proc.waitCh <- nil
	}()

	if err := supervisor.Run(context.Background(), func(context.Context, SupervisedProcess) error {
		close(stopReleased)
		return nil
	}); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if proc.stopCount != 1 {
		t.Fatalf("expected one graceful stop, got %d", proc.stopCount)
	}
	if proc.killCount != 1 {
		t.Fatalf("expected one force kill, got %d", proc.killCount)
	}
}

func TestProcessSupervisorRunCleansUpWhenReadinessFails(t *testing.T) {
	proc := newFakeSupervisedProcess()
	proc.stopCh <- nil
	readyErr := errors.New("not ready")
	go func() {
		<-proc.stopCalled
		proc.waitCh <- nil
	}()

	supervisor := ProcessSupervisor{
		Start: func(context.Context) (SupervisedProcess, error) {
			return proc, nil
		},
		WaitReady: func(context.Context, SupervisedProcess) error {
			return readyErr
		},
		GracePeriod: 25 * time.Millisecond,
	}

	err := supervisor.Run(context.Background(), func(context.Context, SupervisedProcess) error {
		t.Fatal("run callback should not be called when readiness fails")
		return nil
	})
	if !errors.Is(err, readyErr) {
		t.Fatalf("expected readiness error, got %v", err)
	}
	if proc.stopCount != 1 {
		t.Fatalf("expected cleanup stop after readiness failure, got %d", proc.stopCount)
	}
}

func TestProcessSupervisorRunCleansUpOnCancellation(t *testing.T) {
	proc := newFakeSupervisedProcess()
	proc.stopCh <- nil

	supervisor := ProcessSupervisor{
		Start: func(context.Context) (SupervisedProcess, error) {
			return proc, nil
		},
		WaitReady: func(context.Context, SupervisedProcess) error {
			return nil
		},
		GracePeriod: 25 * time.Millisecond,
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		<-proc.stopCalled
		proc.waitCh <- nil
	}()
	go func() {
		done <- supervisor.Run(ctx, func(ctx context.Context, _ SupervisedProcess) error {
			<-ctx.Done()
			return ctx.Err()
		})
	}()
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected cancellation error, got %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("expected supervisor to return after cancellation")
	}
	if proc.stopCount != 1 {
		t.Fatalf("expected cleanup stop after cancellation, got %d", proc.stopCount)
	}
}

func TestProcessSupervisorRunFailsWhenProcessExitsBeforeReadiness(t *testing.T) {
	proc := newFakeSupervisedProcess()
	exitErr := errors.New("process exited early")
	proc.stopCh <- nil
	proc.waitCh <- exitErr

	supervisor := ProcessSupervisor{
		Start: func(context.Context) (SupervisedProcess, error) {
			return proc, nil
		},
		WaitReady: func(ctx context.Context, _ SupervisedProcess) error {
			<-ctx.Done()
			return ctx.Err()
		},
		GracePeriod: 25 * time.Millisecond,
	}

	err := supervisor.Run(context.Background(), func(context.Context, SupervisedProcess) error {
		t.Fatal("run callback should not be called when process exits before readiness")
		return nil
	})
	if !errors.Is(err, exitErr) {
		t.Fatalf("expected early-exit error, got %v", err)
	}
	if proc.stopCount != 1 {
		t.Fatalf("expected cleanup stop after early readiness exit, got %d", proc.stopCount)
	}
}

func TestProcessSupervisorRunKillsProcessWhenReadinessTimesOut(t *testing.T) {
	proc := newFakeSupervisedProcess()
	proc.killCh <- nil

	supervisor := ProcessSupervisor{
		Start: func(context.Context) (SupervisedProcess, error) {
			return proc, nil
		},
		WaitReady: func(ctx context.Context, _ SupervisedProcess) error {
			<-ctx.Done()
			return ctx.Err()
		},
		GracePeriod: 20 * time.Millisecond,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- supervisor.Run(ctx, func(context.Context, SupervisedProcess) error {
			t.Fatal("run callback should not be called when readiness times out")
			return nil
		})
	}()

	select {
	case <-proc.killCalled:
		proc.waitCh <- nil
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected supervisor to force-kill stalled readiness cleanup")
	}

	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expected readiness timeout error, got %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected supervisor to return after readiness timeout")
	}

	if proc.stopCount != 1 {
		t.Fatalf("expected graceful stop attempt before kill, got %d", proc.stopCount)
	}
	if proc.killCount != 1 {
		t.Fatalf("expected force kill after readiness timeout, got %d", proc.killCount)
	}
}
