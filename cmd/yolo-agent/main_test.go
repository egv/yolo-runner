package main

import (
	"context"
	"testing"
	"time"
)

func TestRunMainParsesFlagsAndInvokesRun(t *testing.T) {
	called := false
	var got runConfig
	run := func(_ context.Context, cfg runConfig) error {
		called = true
		got = cfg
		return nil
	}

	code := RunMain([]string{"--repo", "/repo", "--root", "root-1", "--model", "openai/gpt-5.3-codex", "--max", "2", "--concurrency", "3", "--dry-run", "--runner-timeout", "30s", "--events", "/repo/runner-logs/agent.events.jsonl"}, run)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if !called {
		t.Fatalf("expected run function to be called")
	}
	if got.repoRoot != "/repo" || got.rootID != "root-1" || got.model != "openai/gpt-5.3-codex" {
		t.Fatalf("unexpected config: %#v", got)
	}
	if got.maxTasks != 2 || !got.dryRun {
		t.Fatalf("expected max=2 dry-run=true, got %#v", got)
	}
	if got.runnerTimeout != 30*time.Second {
		t.Fatalf("expected runner timeout 30s, got %s", got.runnerTimeout)
	}
	if got.eventsPath != "/repo/runner-logs/agent.events.jsonl" {
		t.Fatalf("expected events path to be parsed, got %q", got.eventsPath)
	}
	if got.concurrency != 3 {
		t.Fatalf("expected concurrency=3, got %d", got.concurrency)
	}
}

func TestRunMainRequiresRoot(t *testing.T) {
	code := RunMain([]string{"--repo", "/repo"}, func(context.Context, runConfig) error { return nil })
	if code != 1 {
		t.Fatalf("expected exit code 1 when root missing, got %d", code)
	}
}

func TestRunMainRejectsNonPositiveConcurrency(t *testing.T) {
	called := false
	code := RunMain([]string{"--repo", "/repo", "--root", "root-1", "--concurrency", "0"}, func(context.Context, runConfig) error {
		called = true
		return nil
	})

	if code != 1 {
		t.Fatalf("expected exit code 1 when concurrency is non-positive, got %d", code)
	}
	if called {
		t.Fatalf("expected run function not to be called for invalid concurrency")
	}
}
