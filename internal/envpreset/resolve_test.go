package envpreset

import (
	"testing"
	"time"
)

func TestResolveAgentAndLandingMatchConfigDefaultingSemantics(t *testing.T) {
	defaultAgent, err := ResolveAgent(Preset{})
	if err != nil {
		t.Fatalf("ResolveAgent returned error: %v", err)
	}
	if defaultAgent.Backend != "opencode" {
		t.Fatalf("expected default backend opencode, got %q", defaultAgent.Backend)
	}
	if defaultAgent.Model != "zai-coding-plan/glm-4.7" {
		t.Fatalf("expected default model from opencode backend, got %q", defaultAgent.Model)
	}
	if defaultAgent.RunnerTimeout != 0 {
		t.Fatalf("expected default runner timeout 0, got %s", defaultAgent.RunnerTimeout)
	}
	if defaultAgent.WatchdogTimeout != 10*time.Minute {
		t.Fatalf("expected default watchdog timeout 10m, got %s", defaultAgent.WatchdogTimeout)
	}
	if defaultAgent.WatchdogInterval != 5*time.Second {
		t.Fatalf("expected default watchdog interval 5s, got %s", defaultAgent.WatchdogInterval)
	}

	defaultLanding, err := ResolveLanding(Preset{})
	if err != nil {
		t.Fatalf("ResolveLanding returned error: %v", err)
	}
	if defaultLanding != LandingTypeGitMerge {
		t.Fatalf("expected default landing %q, got %q", LandingTypeGitMerge, defaultLanding)
	}

	configuredAgent, err := ResolveAgent(Preset{Agent: Agent{
		Backend:          " CoDeX ",
		RunnerTimeout:    20 * time.Minute,
		WatchdogTimeout:  9 * time.Minute,
		WatchdogInterval: 2 * time.Second,
	}})
	if err != nil {
		t.Fatalf("ResolveAgent with configured agent returned error: %v", err)
	}
	if configuredAgent.Backend != "codex" {
		t.Fatalf("expected configured backend codex, got %q", configuredAgent.Backend)
	}
	if configuredAgent.Model != "gpt-5.3-codex-spark" {
		t.Fatalf("expected configured backend model fallback, got %q", configuredAgent.Model)
	}
	if configuredAgent.RunnerTimeout != 20*time.Minute {
		t.Fatalf("expected configured runner timeout 20m, got %s", configuredAgent.RunnerTimeout)
	}
	if configuredAgent.WatchdogTimeout != 9*time.Minute {
		t.Fatalf("expected configured watchdog timeout 9m, got %s", configuredAgent.WatchdogTimeout)
	}
	if configuredAgent.WatchdogInterval != 2*time.Second {
		t.Fatalf("expected configured watchdog interval 2s, got %s", configuredAgent.WatchdogInterval)
	}

	for _, landingType := range []LandingType{LandingTypeGitMerge, LandingTypeArcPR, LandingTypeNone} {
		t.Run(string(landingType), func(t *testing.T) {
			got, err := ResolveLanding(Preset{Landing: Landing{Type: landingType}})
			if err != nil {
				t.Fatalf("ResolveLanding returned error: %v", err)
			}
			if got != landingType {
				t.Fatalf("expected landing %q, got %q", landingType, got)
			}
		})
	}
}
