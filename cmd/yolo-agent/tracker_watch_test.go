package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestDefaultRunTrackerWatchRejectsHeldLock(t *testing.T) {
	repoRoot := t.TempDir()
	lockPath := filepath.Join(repoRoot, "locks", "tracker-agent.lock")
	writeTrackerConfigYAML(t, repoRoot, `
profiles:
  default:
    tracker:
      type: tk
tracker_agent:
  lock_path: locks/tracker-agent.lock
`)
	release := holdTrackerWatchLockForTest(t, lockPath)
	defer release()

	err := defaultRunTrackerWatch(context.Background(), trackerWatchConfig{
		repoRoot: repoRoot,
		once:     true,
	})
	if err == nil {
		t.Fatalf("expected held tracker-watch lock to fail")
	}
	if !strings.Contains(err.Error(), "tracker-watch lock is already held") {
		t.Fatalf("expected clear lock-held error, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), lockPath) {
		t.Fatalf("expected lock path %q in error, got %q", lockPath, err.Error())
	}
}

func holdTrackerWatchLockForTest(t *testing.T, lockPath string) func() {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		t.Fatalf("mkdir lock dir: %v", err)
	}
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("open lock file: %v", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		_ = file.Close()
		t.Fatalf("hold lock file: %v", err)
	}

	return func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
	}
}
