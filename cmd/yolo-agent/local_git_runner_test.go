package main

import (
	"strings"
	"testing"
	"time"
)

// A VCS command that hangs (e.g. arc on a damaged FUSE mount) must fail after
// the timeout instead of parking the landing forever behind live heartbeats.
func TestLocalGitRunnerKillsHungCommandAfterTimeout(t *testing.T) {
	previous := localGitRunnerCommandTimeout
	t.Cleanup(func() { localGitRunnerCommandTimeout = previous })
	localGitRunnerCommandTimeout = 200 * time.Millisecond

	start := time.Now()
	_, err := localGitRunner{dir: t.TempDir()}.Run("sleep", "30")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Run(sleep 30) error = nil, want timeout failure")
	}
	if !strings.Contains(err.Error(), "timed out after") {
		t.Fatalf("Run(sleep 30) error = %v, want a timed-out error", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("Run(sleep 30) took %s, want prompt kill after the 200ms timeout", elapsed)
	}
}

func TestLocalGitRunnerRunsFastCommandsNormally(t *testing.T) {
	out, err := localGitRunner{dir: t.TempDir()}.Run("echo", "ok")
	if err != nil {
		t.Fatalf("Run(echo) error = %v", err)
	}
	if strings.TrimSpace(out) != "ok" {
		t.Fatalf("Run(echo) output = %q, want ok", out)
	}
}
