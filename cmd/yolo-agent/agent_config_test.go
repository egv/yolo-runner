package main

import (
	"strings"
	"testing"
	"time"
)

func TestResolveYoloAgentConfigDefaultsParsesConfiguredValues(t *testing.T) {
	concurrency := 3
	retryBudget := 2
	defaults, err := resolveYoloAgentConfigDefaults(yoloAgentConfigModel{
		Backend:          "codex",
		Model:            "openai/gpt-5.3-codex",
		Concurrency:      &concurrency,
		RunnerTimeout:    "20m",
		WatchdogTimeout:  "9m",
		WatchdogInterval: "2s",
		RetryBudget:      &retryBudget,
	})
	if err != nil {
		t.Fatalf("expected config defaults to parse, got %v", err)
	}
	if defaults.Backend != backendCodex {
		t.Fatalf("expected backend=%q, got %q", backendCodex, defaults.Backend)
	}
	if defaults.Model != "openai/gpt-5.3-codex" {
		t.Fatalf("expected model to parse, got %q", defaults.Model)
	}
	if defaults.Concurrency == nil || *defaults.Concurrency != 3 {
		t.Fatalf("expected concurrency=3, got %#v", defaults.Concurrency)
	}
	if defaults.RunnerTimeout == nil || *defaults.RunnerTimeout != 20*time.Minute {
		t.Fatalf("expected runner timeout 20m, got %#v", defaults.RunnerTimeout)
	}
	if defaults.WatchdogTimeout == nil || *defaults.WatchdogTimeout != 9*time.Minute {
		t.Fatalf("expected watchdog timeout 9m, got %#v", defaults.WatchdogTimeout)
	}
	if defaults.WatchdogInterval == nil || *defaults.WatchdogInterval != 2*time.Second {
		t.Fatalf("expected watchdog interval 2s, got %#v", defaults.WatchdogInterval)
	}
	if defaults.RetryBudget == nil || *defaults.RetryBudget != 2 {
		t.Fatalf("expected retry budget=2, got %#v", defaults.RetryBudget)
	}
}

func TestResolveYoloAgentConfigDefaultsRejectsInvalidDuration(t *testing.T) {
	_, err := resolveYoloAgentConfigDefaults(yoloAgentConfigModel{
		RunnerTimeout: "soon",
	})
	if err == nil {
		t.Fatalf("expected invalid duration to fail")
	}
	if !strings.Contains(err.Error(), "agent.runner_timeout") {
		t.Fatalf("expected field-specific error, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), ".yolo-runner/config.yaml") {
		t.Fatalf("expected config path in error, got %q", err.Error())
	}
}

func TestResolveYoloAgentConfigDefaultsRejectsNonPositiveConcurrency(t *testing.T) {
	concurrency := 0
	_, err := resolveYoloAgentConfigDefaults(yoloAgentConfigModel{
		Concurrency: &concurrency,
	})
	if err == nil {
		t.Fatalf("expected non-positive concurrency to fail")
	}
	if !strings.Contains(err.Error(), "agent.concurrency") {
		t.Fatalf("expected field-specific error, got %q", err.Error())
	}
}

func TestResolveYoloAgentConfigDefaultsRejectsNegativeRetryBudget(t *testing.T) {
	retryBudget := -1
	_, err := resolveYoloAgentConfigDefaults(yoloAgentConfigModel{
		RetryBudget: &retryBudget,
	})
	if err == nil {
		t.Fatalf("expected negative retry budget to fail")
	}
	if !strings.Contains(err.Error(), "agent.retry_budget") {
		t.Fatalf("expected field-specific error, got %q", err.Error())
	}
}
