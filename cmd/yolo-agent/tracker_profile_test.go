package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveTrackerProfileDefaultsToTKWhenConfigMissing(t *testing.T) {
	repoRoot := t.TempDir()

	got, err := resolveTrackerProfile(repoRoot, "", "root-1", nil)
	if err != nil {
		t.Fatalf("expected default profile resolution, got %v", err)
	}
	if got.Name != "default" {
		t.Fatalf("expected default profile name, got %q", got.Name)
	}
	if got.Tracker.Type != trackerTypeTK {
		t.Fatalf("expected tracker type %q, got %q", trackerTypeTK, got.Tracker.Type)
	}
}

func TestResolveTrackerProfileUsesSelectedProfile(t *testing.T) {
	repoRoot := t.TempDir()
	writeTrackerConfigYAML(t, repoRoot, `
default_profile: local
profiles:
  local:
    tracker:
      type: tk
      tk:
        scope:
          root: yr
  linear-dev:
    tracker:
      type: tk
`)

	got, err := resolveTrackerProfile(repoRoot, "linear-dev", "yr-8nec", nil)
	if err != nil {
		t.Fatalf("expected selected profile, got %v", err)
	}
	if got.Name != "linear-dev" {
		t.Fatalf("expected selected profile linear-dev, got %q", got.Name)
	}
	if got.Tracker.Type != trackerTypeTK {
		t.Fatalf("expected tracker type %q, got %q", trackerTypeTK, got.Tracker.Type)
	}
}

func TestResolveTrackerProfileRejectsUnknownProfile(t *testing.T) {
	repoRoot := t.TempDir()
	writeTrackerConfigYAML(t, repoRoot, `
profiles:
  default:
    tracker:
      type: tk
`)

	_, err := resolveTrackerProfile(repoRoot, "missing", "root-1", nil)
	if err == nil {
		t.Fatalf("expected unknown profile to fail")
	}
	if !strings.Contains(err.Error(), `tracker profile "missing"`) {
		t.Fatalf("expected unknown profile error, got %q", err.Error())
	}
}

func TestResolveTrackerProfileRejectsTKScopeMismatch(t *testing.T) {
	repoRoot := t.TempDir()
	writeTrackerConfigYAML(t, repoRoot, `
profiles:
  default:
    tracker:
      type: tk
      tk:
        scope:
          root: roadmap
`)

	_, err := resolveTrackerProfile(repoRoot, "", "other-root", nil)
	if err == nil {
		t.Fatalf("expected tk scope mismatch to fail")
	}
	if !strings.Contains(err.Error(), `outside tk scope`) {
		t.Fatalf("expected scope mismatch error, got %q", err.Error())
	}
}

func TestResolveTrackerProfileRejectsLinearMissingWorkspace(t *testing.T) {
	repoRoot := t.TempDir()
	writeTrackerConfigYAML(t, repoRoot, `
profiles:
  default:
    tracker:
      type: linear
      linear:
        auth:
          token_env: LINEAR_TOKEN
`)

	_, err := resolveTrackerProfile(repoRoot, "", "root-1", func(string) string { return "token" })
	if err == nil {
		t.Fatalf("expected missing linear workspace to fail")
	}
	if !strings.Contains(err.Error(), `linear.scope.workspace`) {
		t.Fatalf("expected linear workspace validation error, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), `.yolo-runner/config.yaml`) {
		t.Fatalf("expected config path guidance, got %q", err.Error())
	}
}

func TestResolveTrackerProfileRejectsLinearMissingTokenEnv(t *testing.T) {
	repoRoot := t.TempDir()
	writeTrackerConfigYAML(t, repoRoot, `
profiles:
  default:
    tracker:
      type: linear
      linear:
        scope:
          workspace: anomaly
`)

	_, err := resolveTrackerProfile(repoRoot, "", "root-1", func(string) string { return "token" })
	if err == nil {
		t.Fatalf("expected missing linear token env to fail")
	}
	if !strings.Contains(err.Error(), `linear.auth.token_env`) {
		t.Fatalf("expected linear token_env validation error, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), `.yolo-runner/config.yaml`) {
		t.Fatalf("expected config path guidance, got %q", err.Error())
	}
}

func TestResolveTrackerProfileRejectsLinearMissingTokenValue(t *testing.T) {
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

	_, err := resolveTrackerProfile(repoRoot, "", "root-1", func(string) string { return "" })
	if err == nil {
		t.Fatalf("expected missing linear token value to fail")
	}
	if !strings.Contains(err.Error(), `missing auth token`) {
		t.Fatalf("expected linear token value validation error, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), `export LINEAR_TOKEN=<linear-api-token>`) {
		t.Fatalf("expected auth token export guidance, got %q", err.Error())
	}
}

func TestResolveTrackerProfileRejectsLinearMultiWorkspaceConfig(t *testing.T) {
	repoRoot := t.TempDir()
	writeTrackerConfigYAML(t, repoRoot, `
profiles:
  default:
    tracker:
      type: linear
      linear:
        scope:
          workspace: anomaly,another
        auth:
          token_env: LINEAR_TOKEN
`)

	_, err := resolveTrackerProfile(repoRoot, "", "root-1", func(string) string { return "token" })
	if err == nil {
		t.Fatalf("expected multi-workspace configuration to fail")
	}
	if !strings.Contains(err.Error(), `single-workspace`) {
		t.Fatalf("expected single-workspace guidance, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), `linear.scope.workspace`) {
		t.Fatalf("expected workspace field guidance, got %q", err.Error())
	}
}

func TestBuildTaskManagerForTrackerSupportsTK(t *testing.T) {
	manager, err := buildTaskManagerForTracker(t.TempDir(), resolvedTrackerProfile{
		Name: "default",
		Tracker: trackerModel{
			Type: trackerTypeTK,
		},
	})
	if err != nil {
		t.Fatalf("expected tk task manager to build, got %v", err)
	}
	if manager == nil {
		t.Fatalf("expected non-nil task manager")
	}
}

func TestBuildTaskManagerForTrackerRejectsUnsupportedType(t *testing.T) {
	_, err := buildTaskManagerForTracker(t.TempDir(), resolvedTrackerProfile{
		Name: "linear",
		Tracker: trackerModel{
			Type: trackerTypeLinear,
		},
	})
	if err == nil {
		t.Fatalf("expected unsupported tracker type to fail")
	}
	if !strings.Contains(err.Error(), `tracker type "linear"`) {
		t.Fatalf("expected unsupported tracker error, got %q", err.Error())
	}
}

func TestResolveProfileSelectionPolicyPrefersFlag(t *testing.T) {
	got := resolveProfileSelectionPolicy(profileSelectionInput{
		FlagValue: "qa",
		EnvValue:  "default",
	})
	if got != "qa" {
		t.Fatalf("expected flag to win, got %q", got)
	}
}

func TestResolveProfileSelectionPolicyFallsBackToEnv(t *testing.T) {
	got := resolveProfileSelectionPolicy(profileSelectionInput{
		EnvValue: "default",
	})
	if got != "default" {
		t.Fatalf("expected env value, got %q", got)
	}
}

func writeTrackerConfigYAML(t *testing.T, repoRoot string, payload string) {
	t.Helper()
	configDir := filepath.Join(repoRoot, ".yolo-runner")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	configPath := filepath.Join(configDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(strings.TrimSpace(payload)+"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}
