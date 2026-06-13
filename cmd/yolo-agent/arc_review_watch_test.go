package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestArcReviewWatchCommandShimsToSourceArcPRWithDeprecationNotice(t *testing.T) {
	repoRoot := t.TempDir()
	eventsPath := filepath.Join(repoRoot, "runner-logs", "arc-review-watch.events.jsonl")

	originalRunSource := runSourceArcPR
	t.Cleanup(func() {
		runSourceArcPR = originalRunSource
	})

	var captured []sourceArcPRCommandConfig
	runSourceArcPR = func(ctx context.Context, cfg sourceArcPRCommandConfig) error {
		if ctx == nil {
			t.Fatal("runSourceArcPR() context is nil")
		}
		captured = append(captured, cfg)
		return nil
	}

	var code int
	stderrText := captureStderr(t, func() {
		code = RunMain([]string{
			"arc-review-watch",
			"--repo", repoRoot,
			"--profile", "arc-dev",
			"--once",
			"--stream",
			"--events", eventsPath,
		}, func(context.Context, runConfig) error {
			t.Fatalf("legacy run function should not be called")
			return nil
		})
	})
	if code != 0 {
		t.Fatalf("RunMain() exit code = %d, want 0; stderr=%q", code, stderrText)
	}
	if len(captured) != 1 {
		t.Fatalf("runSourceArcPR() calls = %d, want 1", len(captured))
	}
	got := captured[0]
	if got.repoRoot != repoRoot || got.profile != "arc-dev" || got.queuePath != "" || !got.once || !got.stream || got.eventsPath != eventsPath {
		t.Fatalf("source arcpr config mismatch: %#v", got)
	}
	if !strings.Contains(stderrText, "arc-review-watch is deprecated") ||
		!strings.Contains(stderrText, "yolo-agent source arcpr") {
		t.Fatalf("stderr missing deprecation shim notice: %q", stderrText)
	}
}
