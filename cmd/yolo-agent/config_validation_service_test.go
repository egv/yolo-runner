package main

import (
	"strings"
	"testing"
)

func TestLoadYoloAgentConfigValidationServiceDefaultsWhenConfigMissing(t *testing.T) {
	repoRoot := t.TempDir()

	service := newTrackerConfigService(repoRoot, nil)

	profile, err := service.resolveTrackerProfile("", "root-1")
	if err != nil {
		t.Fatalf("expected default tracker profile, got %v", err)
	}
	if profile.Name != defaultProfileName {
		t.Fatalf("expected default profile %q, got %q", defaultProfileName, profile.Name)
	}
	if profile.Tracker.Type != trackerTypeTK {
		t.Fatalf("expected default tracker type %q, got %q", trackerTypeTK, profile.Tracker.Type)
	}

	defaults, err := service.loadAgentDefaults()
	if err != nil {
		t.Fatalf("expected default agent settings to validate, got %v", err)
	}
	if defaults.Backend != "" {
		t.Fatalf("expected empty backend default, got %q", defaults.Backend)
	}
}

func TestLoadYoloAgentConfigValidationServiceRejectsInvalidYAML(t *testing.T) {
	repoRoot := t.TempDir()
	writeTrackerConfigYAML(t, repoRoot, `
profiles:
  default:
    tracker:
      type: tk
      tk: [
`)

	service := newTrackerConfigService(repoRoot, nil)
	_, err := service.loadAgentDefaults()
	if err == nil {
		t.Fatalf("expected invalid YAML to fail")
	}
	if !strings.Contains(err.Error(), "cannot parse config file at .yolo-runner/config.yaml") {
		t.Fatalf("expected parse error with config path, got %q", err.Error())
	}
}

func TestLoadYoloAgentConfigValidationServiceRejectsUnknownFields(t *testing.T) {
	repoRoot := t.TempDir()
	writeTrackerConfigYAML(t, repoRoot, `
profiles:
  default:
    tracker:
      type: tk
      unsupported_setting: true
`)

	service := newTrackerConfigService(repoRoot, nil)
	_, err := service.loadAgentDefaults()
	if err == nil {
		t.Fatalf("expected unknown fields to fail")
	}
	if !strings.Contains(err.Error(), "unsupported_setting") {
		t.Fatalf("expected unknown field in error, got %q", err.Error())
	}
}

func TestYoloAgentConfigValidationServiceRejectsUnsupportedBackend(t *testing.T) {
	repoRoot := t.TempDir()
	writeTrackerConfigYAML(t, repoRoot, `
profiles:
  default:
    tracker:
      type: tk
agent:
  backend: unsupported
`)

	service := newTrackerConfigService(repoRoot, nil)

	_, err := service.loadAgentDefaults()
	if err == nil {
		t.Fatalf("expected unsupported backend to fail")
	}
	if !strings.Contains(err.Error(), "agent.backend") {
		t.Fatalf("expected field-specific backend error, got %q", err.Error())
	}
}

func TestYoloAgentConfigValidationServiceRejectsBadDurationAndNumber(t *testing.T) {
	repoRoot := t.TempDir()
	writeTrackerConfigYAML(t, repoRoot, `
profiles:
  default:
    tracker:
      type: tk
agent:
  runner_timeout: soon
  concurrency: 0
`)

	service := newTrackerConfigService(repoRoot, nil)

	_, err := service.loadAgentDefaults()
	if err == nil {
		t.Fatalf("expected invalid numeric/duration defaults to fail")
	}
	if !strings.Contains(err.Error(), "agent.concurrency") && !strings.Contains(err.Error(), "agent.runner_timeout") {
		t.Fatalf("expected duration or numeric validation error, got %q", err.Error())
	}
}

func TestYoloAgentConfigValidationServiceRejectsProfileAuthValidationFailures(t *testing.T) {
	repoRoot := t.TempDir()
	writeTrackerConfigYAML(t, repoRoot, `
profiles:
  default:
    tracker:
      type: linear
      linear:
        scope:
          workspace: anomaly
        auth:
          token_env: LINEAR_TOKEN
`)

	service := newTrackerConfigService(repoRoot, func(string) string { return "" })

	_, err := service.resolveTrackerProfile("default", "root-1")
	if err == nil {
		t.Fatalf("expected auth validation to fail")
	}
	if !strings.Contains(err.Error(), "missing auth token") {
		t.Fatalf("expected auth token guidance, got %q", err.Error())
	}
}
