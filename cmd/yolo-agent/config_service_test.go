package main

import (
	"testing"
	"time"
)

func TestTrackerConfigServiceLoadsAgentDefaultsAndTrackerProfile(t *testing.T) {
	repoRoot := t.TempDir()
	writeTrackerConfigYAML(t, repoRoot, `
profiles:
  default:
    tracker:
      type: tk
agent:
  backend: codex
  model: openai/gpt-5.3-codex
  concurrency: 3
  runner_timeout: 25m
  watchdog_timeout: 2m
  watchdog_interval: 3s
  retry_budget: 4
`)

	svc := newTrackerConfigService(repoRoot, nil)

	defaults, err := svc.loadAgentDefaults()
	if err != nil {
		t.Fatalf("loadAgentDefaults: %v", err)
	}
	if defaults.Backend != backendCodex {
		t.Fatalf("expected backend=%q, got %q", backendCodex, defaults.Backend)
	}
	if defaults.Model != "openai/gpt-5.3-codex" {
		t.Fatalf("expected model from config, got %q", defaults.Model)
	}
	if defaults.Concurrency == nil || *defaults.Concurrency != 3 {
		t.Fatalf("expected concurrency=3, got %#v", defaults.Concurrency)
	}
	if defaults.RunnerTimeout == nil || *defaults.RunnerTimeout != 25*time.Minute {
		t.Fatalf("expected runner timeout 25m, got %#v", defaults.RunnerTimeout)
	}
	if defaults.WatchdogTimeout == nil || *defaults.WatchdogTimeout != 2*time.Minute {
		t.Fatalf("expected watchdog timeout 2m, got %#v", defaults.WatchdogTimeout)
	}
	if defaults.WatchdogInterval == nil || *defaults.WatchdogInterval != 3*time.Second {
		t.Fatalf("expected watchdog interval 3s, got %#v", defaults.WatchdogInterval)
	}
	if defaults.RetryBudget == nil || *defaults.RetryBudget != 4 {
		t.Fatalf("expected retry budget=4, got %#v", defaults.RetryBudget)
	}

	profile, err := svc.resolveTrackerProfile("", "root-1")
	if err != nil {
		t.Fatalf("resolveTrackerProfile: %v", err)
	}
	if profile.Name != "default" {
		t.Fatalf("expected default profile, got %q", profile.Name)
	}
	if profile.Tracker.Type != trackerTypeTK {
		t.Fatalf("expected tracker type %q, got %q", trackerTypeTK, profile.Tracker.Type)
	}
}

func TestTrackerConfigServiceLoadsConfigModelOnlyOnce(t *testing.T) {
	repoRoot := t.TempDir()
	writeTrackerConfigYAML(t, repoRoot, `
profiles:
  default:
    tracker:
      type: tk
agent:
  backend: codex
`)

	svc := newTrackerConfigService(repoRoot, nil)
	calls := 0
	originalLoad := svc.loadModel
	svc.loadModel = func(path string) (trackerProfilesModel, error) {
		calls++
		return originalLoad(path)
	}

	if _, err := svc.loadAgentDefaults(); err != nil {
		t.Fatalf("loadAgentDefaults: %v", err)
	}
	if _, err := svc.resolveTrackerProfile("", "root-1"); err != nil {
		t.Fatalf("resolveTrackerProfile: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected shared config load once, got %d", calls)
	}
}
