package opencode

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeProcess struct {
	killed bool
	waitCh chan struct{}
}

func newFakeProcess() *fakeProcess {
	return &fakeProcess{waitCh: make(chan struct{})}
}

func (p *fakeProcess) Wait() error {
	<-p.waitCh
	return nil
}

func (p *fakeProcess) Kill() error {
	p.killed = true
	close(p.waitCh)
	return nil
}

type delayedProcess struct {
	killed     bool
	waitErr    error
	waitErrCh  chan error
	waitCalled chan struct{}
}

func newDelayedProcess() *delayedProcess {
	return &delayedProcess{
		waitErrCh:  make(chan error, 1),
		waitCalled: make(chan struct{}),
	}
}

func (p *delayedProcess) Wait() error {
	close(p.waitCalled)
	return <-p.waitErrCh
}

func (p *delayedProcess) Kill() error {
	p.killed = true
	return nil
}

func (p *delayedProcess) finish(err error) {
	p.waitErrCh <- err
}

type killBlockingProcess struct {
	killed      bool
	waitCalled  chan struct{}
	waitErrCh   chan error
	killCalled  chan struct{}
	killRelease chan struct{}
	mu          sync.Mutex
}

func newKillBlockingProcess() *killBlockingProcess {
	return &killBlockingProcess{
		waitCalled:  make(chan struct{}),
		waitErrCh:   make(chan error, 1),
		killCalled:  make(chan struct{}),
		killRelease: make(chan struct{}),
	}
}

func (p *killBlockingProcess) Wait() error {
	close(p.waitCalled)
	return <-p.waitErrCh
}

func (p *killBlockingProcess) Kill() error {
	select {
	case <-p.killCalled:
	default:
		close(p.killCalled)
	}
	<-p.killRelease
	p.mu.Lock()
	p.killed = true
	p.mu.Unlock()
	return nil
}

func (p *killBlockingProcess) finish(err error) {
	p.waitErrCh <- err
}

func (p *killBlockingProcess) isKilled() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.killed
}

type immediateProcess struct {
	killed  bool
	waitErr error
}

func (p *immediateProcess) Wait() error {
	return p.waitErr
}

func (p *immediateProcess) Kill() error {
	p.killed = true
	return nil
}

type staticTicker struct{ ch <-chan time.Time }

func newStaticTicker(ch <-chan time.Time) *staticTicker {
	return &staticTicker{ch: ch}
}

func (t *staticTicker) C() <-chan time.Time { return t.ch }
func (t *staticTicker) Stop()               {}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
}

func TestWatchdogTimeoutKillsProcessAndClassifiesPermission(t *testing.T) {
	tempDir := t.TempDir()
	runnerLog := filepath.Join(tempDir, "runner-logs", "opencode", "issue-1.jsonl")
	writeFile(t, runnerLog, "")
	logDir := filepath.Join(tempDir, "opencode", "log")
	opencodeLog := filepath.Join(logDir, "latest.log")
	writeFile(t, opencodeLog, "INFO service=permission asking\nINFO session id=ses_123\n")

	base := time.Now()
	oldTime := base.Add(-1 * time.Second)
	if err := os.Chtimes(runnerLog, oldTime, oldTime); err != nil {
		t.Fatalf("chtimes runner log: %v", err)
	}

	proc := newFakeProcess()
	tickCh := make(chan time.Time, 1)
	tickCh <- base
	calls := 0
	watchdog := NewWatchdog(WatchdogConfig{
		LogPath:         runnerLog,
		OpenCodeLogDir:  logDir,
		Timeout:         10 * time.Millisecond,
		CompletionGrace: 10 * time.Second,
		TailLines:       50,
		NewTicker: func(duration time.Duration) WatchdogTicker {
			return newStaticTicker(tickCh)
		},
		After: func(time.Duration) <-chan time.Time {
			ch := make(chan time.Time, 1)
			ch <- base
			return ch
		},
		Now: func() time.Time {
			calls++
			if calls == 1 {
				return base
			}
			return base.Add(2 * time.Second)
		},
	})

	errCh := make(chan error, 1)
	go func() {
		errCh <- watchdog.Monitor(proc)
	}()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatalf("expected error")
		}
		stall, ok := err.(*StallError)
		if !ok {
			t.Fatalf("expected StallError, got %T", err)
		}
		if stall.Category != "permission" {
			t.Fatalf("expected permission category, got %q", stall.Category)
		}
		if !proc.killed {
			t.Fatalf("expected process to be killed")
		}
		if !strings.Contains(err.Error(), "permission") {
			t.Fatalf("expected permission in error, got %q", err.Error())
		}
		if !strings.Contains(err.Error(), opencodeLog) {
			t.Fatalf("expected opencode log path in error")
		}
		if !strings.Contains(err.Error(), runnerLog) {
			t.Fatalf("expected runner log path in error")
		}
		if !strings.Contains(err.Error(), "ses_123") {
			t.Fatalf("expected session id in error")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timed out waiting for watchdog")
	}
}

func TestWatchdogNoOutputIncludesLastOutputAge(t *testing.T) {
	tempDir := t.TempDir()
	runnerLog := filepath.Join(tempDir, "runner-logs", "opencode", "issue-2.jsonl")
	writeFile(t, runnerLog, "")
	logDir := filepath.Join(tempDir, "opencode", "log")
	opencodeLog := filepath.Join(logDir, "latest.log")
	writeFile(t, opencodeLog, "INFO service=provider status=started\n")

	base := time.Now()
	oldTime := base.Add(-1 * time.Second)
	if err := os.Chtimes(runnerLog, oldTime, oldTime); err != nil {
		t.Fatalf("chtimes runner log: %v", err)
	}

	proc := newFakeProcess()
	tickCh := make(chan time.Time, 1)
	tickCh <- base
	calls := 0
	watchdog := NewWatchdog(WatchdogConfig{
		LogPath:         runnerLog,
		OpenCodeLogDir:  logDir,
		Timeout:         10 * time.Millisecond,
		CompletionGrace: 10 * time.Second,
		TailLines:       50,
		NewTicker: func(time.Duration) WatchdogTicker {
			return newStaticTicker(tickCh)
		},
		After: func(time.Duration) <-chan time.Time {
			ch := make(chan time.Time, 1)
			ch <- base
			return ch
		},
		Now: func() time.Time {
			calls++
			if calls == 1 {
				return base
			}
			return base.Add(2 * time.Second)
		},
	})

	errCh := make(chan error, 1)
	go func() {
		errCh <- watchdog.Monitor(proc)
	}()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatalf("expected error")
		}
		stall, ok := err.(*StallError)
		if !ok {
			t.Fatalf("expected StallError, got %T", err)
		}
		if stall.Category != "no_output" {
			t.Fatalf("expected no_output category, got %q", stall.Category)
		}
		if !strings.Contains(err.Error(), "last_output_age=") {
			t.Fatalf("expected last_output_age in error, got %q", err.Error())
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timed out waiting for watchdog")
	}
}

func TestWatchdogDoesNotStallAfterProcessExit(t *testing.T) {
	tempDir := t.TempDir()
	runnerLog := filepath.Join(tempDir, "runner-logs", "opencode", "issue-3.jsonl")
	writeFile(t, runnerLog, "{\"ok\":true}\n")

	proc := &immediateProcess{}
	watchdog := NewWatchdog(WatchdogConfig{
		LogPath:         runnerLog,
		OpenCodeLogDir:  filepath.Join(tempDir, "opencode", "log"),
		Timeout:         20 * time.Millisecond,
		CompletionGrace: 10 * time.Second,
		TailLines:       50,
		Now:             func() time.Time { return time.Now() },
		After: func(time.Duration) <-chan time.Time {
			ch := make(chan time.Time)
			return ch
		},
	})

	err := watchdog.Monitor(proc)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if proc.killed {
		t.Fatalf("expected process not to be killed")
	}
}

func TestWatchdogDoesNotStallWhenProcessCompletesDuringStallTickRace(t *testing.T) {
	tempDir := t.TempDir()
	runnerLog := filepath.Join(tempDir, "runner-logs", "opencode", "issue-4.jsonl")
	writeFile(t, runnerLog, "")

	base := time.Now()
	oldTime := base.Add(-1 * time.Second)
	if err := os.Chtimes(runnerLog, oldTime, oldTime); err != nil {
		t.Fatalf("chtimes runner log: %v", err)
	}

	proc := newDelayedProcess()
	waitRelease := make(chan struct{})
	waitErr := errors.New("exit")
	calls := 0
	tickCh := make(chan time.Time, 1)
	watchdog := NewWatchdog(WatchdogConfig{
		LogPath:         runnerLog,
		OpenCodeLogDir:  filepath.Join(tempDir, "opencode", "log"),
		Timeout:         10 * time.Millisecond,
		CompletionGrace: 10 * time.Second,
		TailLines:       5,
		NewTicker: func(time.Duration) WatchdogTicker {
			return newStaticTicker(tickCh)
		},
		After: func(time.Duration) <-chan time.Time {
			ch := make(chan time.Time)
			return ch
		},
		Now: func() time.Time {
			calls++
			if calls == 1 {
				return base
			}
			<-waitRelease
			return base.Add(50 * time.Millisecond)
		},
	})

	errCh := make(chan error, 1)
	go func() {
		errCh <- watchdog.Monitor(proc)
	}()

	select {
	case <-proc.waitCalled:
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("timed out waiting for Wait to be called")
	}

	tickCh <- base
	proc.finish(waitErr)
	close(waitRelease)

	select {
	case err := <-errCh:
		if err != waitErr {
			t.Fatalf("expected wait error %v, got %v", waitErr, err)
		}
		if proc.killed {
			t.Fatalf("expected process not to be killed")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timed out waiting for watchdog")
	}
}

func TestWatchdogPrefersWaitResultOverStallKillWhenWaitCompletesDuringGrace(t *testing.T) {
	tempDir := t.TempDir()
	runnerLog := filepath.Join(tempDir, "runner-logs", "opencode", "issue-grace.jsonl")
	writeFile(t, runnerLog, "")

	base := time.Now()
	oldTime := base.Add(-1 * time.Second)
	if err := os.Chtimes(runnerLog, oldTime, oldTime); err != nil {
		t.Fatalf("chtimes runner log: %v", err)
	}

	proc := newKillBlockingProcess()
	defer func() {
		// Ensure Kill never blocks the test, even if invoked unexpectedly.
		close(proc.killRelease)
	}()
	allowNow := make(chan struct{})
	tickCh := make(chan time.Time, 1)
	calls := 0

	watchdog := NewWatchdog(WatchdogConfig{
		LogPath:         runnerLog,
		OpenCodeLogDir:  filepath.Join(tempDir, "opencode", "log"),
		Timeout:         10 * time.Millisecond,
		Interval:        1 * time.Millisecond,
		CompletionGrace: 200 * time.Millisecond,
		TailLines:       5,
		NewTicker: func(duration time.Duration) WatchdogTicker {
			return newStaticTicker(tickCh)
		},
		After: func(time.Duration) <-chan time.Time {
			ch := make(chan time.Time)
			return ch
		},
		Now: func() time.Time {
			calls++
			if calls == 1 {
				return base
			}
			<-allowNow
			return base.Add(50 * time.Millisecond)
		},
	})

	errCh := make(chan error, 1)
	go func() {
		errCh <- watchdog.Monitor(proc)
	}()

	select {
	case <-proc.waitCalled:
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("timed out waiting for Wait to be called")
	}

	tickCh <- base.Add(1 * time.Millisecond)

	select {
	case <-proc.killCalled:
		t.Fatalf("expected watchdog not to call Kill")
	case <-time.After(50 * time.Millisecond):
	}

	proc.finish(nil)
	close(allowNow)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if proc.isKilled() {
			t.Fatalf("expected process not to be killed")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timed out waiting for watchdog")
	}
}
