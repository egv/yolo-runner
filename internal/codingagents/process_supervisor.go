package codingagents

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const defaultProcessSupervisorGracePeriod = 2 * time.Second

type SupervisedProcess interface {
	Wait() error
	Stop() error
	Kill() error
}

type ProcessSupervisor struct {
	Start       func(context.Context) (SupervisedProcess, error)
	WaitReady   func(context.Context, SupervisedProcess) error
	GracePeriod time.Duration
}

func (s ProcessSupervisor) Run(ctx context.Context, run func(context.Context, SupervisedProcess) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if s.Start == nil {
		return fmt.Errorf("process supervisor start function is required")
	}

	proc, err := s.Start(ctx)
	if err != nil {
		return err
	}
	if proc == nil {
		return fmt.Errorf("process supervisor start returned nil process")
	}

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- proc.Wait()
	}()

	runErr, waitDone, waitErr := s.awaitReadiness(ctx, proc, waitCh)
	if runErr == nil && run != nil {
		runErr = run(ctx, proc)
	}

	shutdownErr := s.shutdown(proc, waitCh, waitDone, waitErr)
	if runErr != nil {
		return errors.Join(runErr, shutdownErr)
	}
	return shutdownErr
}

func (s ProcessSupervisor) awaitReadiness(ctx context.Context, proc SupervisedProcess, waitCh <-chan error) (error, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s.WaitReady == nil {
		return nil, false, nil
	}

	readyCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	readyCh := make(chan error, 1)
	go func() {
		readyCh <- s.WaitReady(readyCtx, proc)
	}()

	select {
	case err := <-readyCh:
		return err, false, nil
	case err := <-waitCh:
		if err == nil {
			return fmt.Errorf("process exited before readiness"), true, nil
		}
		return fmt.Errorf("process exited before readiness: %w", err), true, err
	case <-ctx.Done():
		return ctx.Err(), false, nil
	}
}

func (s ProcessSupervisor) shutdown(proc SupervisedProcess, waitCh <-chan error, waitDone bool, waitErr error) error {
	stopCh := make(chan error, 1)
	go func() {
		stopCh <- proc.Stop()
	}()

	timer := time.NewTimer(s.gracePeriod())
	defer timer.Stop()

	var stopErr error
	stopDone := false
	activeWaitCh := waitCh
	if waitDone {
		activeWaitCh = nil
	}
	activeStopCh := (<-chan error)(stopCh)
	for {
		select {
		case waitErr = <-activeWaitCh:
			activeWaitCh = nil
			waitDone = true
			if stopDone {
				return errors.Join(stopErr, waitErr)
			}
		case stopErr = <-activeStopCh:
			activeStopCh = nil
			stopDone = true
			if waitDone {
				return errors.Join(stopErr, waitErr)
			}
		case <-timer.C:
			killErr := proc.Kill()
			if !waitDone {
				waitErr = <-waitCh
				waitDone = true
			}
			return errors.Join(stopErr, killErr, waitErr)
		}
	}
}

func (s ProcessSupervisor) gracePeriod() time.Duration {
	if s.GracePeriod > 0 {
		return s.GracePeriod
	}
	return defaultProcessSupervisorGracePeriod
}
