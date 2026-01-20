package opencode

import (
	"os"
	"path/filepath"
	"strings"
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

	proc := newFakeProcess()
	watchdog := NewWatchdog(WatchdogConfig{
		LogPath:        runnerLog,
		OpenCodeLogDir: logDir,
		Timeout:        20 * time.Millisecond,
		Interval:       5 * time.Millisecond,
		TailLines:      50,
		Now:            func() time.Time { return time.Now() },
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

	oldTime := time.Now().Add(-2 * time.Second)
	if err := os.Chtimes(runnerLog, oldTime, oldTime); err != nil {
		t.Fatalf("chtimes runner log: %v", err)
	}

	proc := newFakeProcess()
	watchdog := NewWatchdog(WatchdogConfig{
		LogPath:        runnerLog,
		OpenCodeLogDir: logDir,
		Timeout:        20 * time.Millisecond,
		Interval:       5 * time.Millisecond,
		TailLines:      50,
		Now:            func() time.Time { return time.Now() },
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
