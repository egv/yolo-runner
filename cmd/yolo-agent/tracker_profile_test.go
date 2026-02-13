package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anomalyco/yolo-runner/internal/contracts"
	"github.com/anomalyco/yolo-runner/internal/linear"
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

func TestBuildTaskManagerForTrackerSupportsLinear(t *testing.T) {
	t.Setenv("LINEAR_TOKEN", "lin_api_test")
	originalFactory := newLinearTaskManager
	t.Cleanup(func() {
		newLinearTaskManager = originalFactory
	})

	var got linear.Config
	newLinearTaskManager = func(cfg linear.Config) (contracts.TaskManager, error) {
		got = cfg
		return staticTaskManager{}, nil
	}

	manager, err := buildTaskManagerForTracker(t.TempDir(), resolvedTrackerProfile{
		Name: "linear",
		Tracker: trackerModel{
			Type: trackerTypeLinear,
			Linear: &linearTrackerModel{
				Scope: linearScopeModel{
					Workspace: "anomaly",
				},
				Auth: linearAuthModel{
					TokenEnv: "LINEAR_TOKEN",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("expected linear task manager to build, got %v", err)
	}
	if manager == nil {
		t.Fatalf("expected non-nil linear task manager")
	}
	if got.Workspace != "anomaly" {
		t.Fatalf("expected workspace to be wired, got %q", got.Workspace)
	}
	if got.Token != "lin_api_test" {
		t.Fatalf("expected token to be loaded from env, got %q", got.Token)
	}
}

func TestBuildTaskManagerForTrackerWrapsLinearAuthErrors(t *testing.T) {
	t.Setenv("LINEAR_TOKEN", "lin_api_invalid")
	originalFactory := newLinearTaskManager
	t.Cleanup(func() {
		newLinearTaskManager = originalFactory
	})
	newLinearTaskManager = func(linear.Config) (contracts.TaskManager, error) {
		return nil, errors.New("invalid authentication")
	}

	_, err := buildTaskManagerForTracker(t.TempDir(), resolvedTrackerProfile{
		Name: "linear",
		Tracker: trackerModel{
			Type: trackerTypeLinear,
			Linear: &linearTrackerModel{
				Scope: linearScopeModel{
					Workspace: "anomaly",
				},
				Auth: linearAuthModel{
					TokenEnv: "LINEAR_TOKEN",
				},
			},
		},
	})
	if err == nil {
		t.Fatalf("expected linear auth failure to be returned")
	}
	if !strings.Contains(err.Error(), `linear auth validation failed`) {
		t.Fatalf("expected linear auth context in error, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), `LINEAR_TOKEN`) {
		t.Fatalf("expected token env to be included, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), `invalid authentication`) {
		t.Fatalf("expected original auth error to be preserved, got %q", err.Error())
	}
}

func TestBuildTaskManagerForTrackerRejectsUnsupportedType(t *testing.T) {
	_, err := buildTaskManagerForTracker(t.TempDir(), resolvedTrackerProfile{
		Name: "unknown",
		Tracker: trackerModel{
			Type: "unknown",
		},
	})
	if err == nil {
		t.Fatalf("expected unsupported tracker type to fail")
	}
	if !strings.Contains(err.Error(), `tracker type "unknown"`) {
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

type staticTaskManager struct{}

func (staticTaskManager) NextTasks(_ context.Context, _ string) ([]contracts.TaskSummary, error) {
	return nil, nil
}

func (staticTaskManager) GetTask(_ context.Context, _ string) (contracts.Task, error) {
	return contracts.Task{}, nil
}

func (staticTaskManager) SetTaskStatus(_ context.Context, _ string, _ contracts.TaskStatus) error {
	return nil
}

func (staticTaskManager) SetTaskData(_ context.Context, _ string, _ map[string]string) error {
	return nil
}
