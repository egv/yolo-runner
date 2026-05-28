//go:build !unix

package main

import "testing"

func holdTrackerWatchLockForTest(t *testing.T, lockPath string) func() {
	t.Helper()

	lock, err := acquireTrackerWatchLock(lockPath)
	if err != nil {
		t.Fatalf("hold lock file: %v", err)
	}
	return func() {
		if err := lock.Release(); err != nil {
			t.Fatalf("release lock file: %v", err)
		}
	}
}
