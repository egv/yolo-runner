package main

import (
	"context"
	"path/filepath"
	"strings"
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
