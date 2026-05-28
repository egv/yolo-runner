//go:build unix

package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

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
