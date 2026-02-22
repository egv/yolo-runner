package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anomalyco/yolo-runner/internal/contracts"
	enginepkg "github.com/anomalyco/yolo-runner/internal/engine"
	githubtracker "github.com/anomalyco/yolo-runner/internal/github"
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

func TestResolveTrackerProfileRejectsGitHubMissingOwner(t *testing.T) {
	repoRoot := t.TempDir()
	writeTrackerConfigYAML(t, repoRoot, `
profiles:
  default:
    tracker:
      type: github
      github:
        scope:
          repo: yolo-runner
        auth:
          token_env: GITHUB_TOKEN
`)

	_, err := resolveTrackerProfile(repoRoot, "", "root-1", func(string) string { return "token" })
	if err == nil {
		t.Fatalf("expected missing github owner to fail")
	}
	if !strings.Contains(err.Error(), `github.scope.owner`) {
		t.Fatalf("expected github owner validation error, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), `.yolo-runner/config.yaml`) {
		t.Fatalf("expected config path guidance, got %q", err.Error())
	}
}

func TestResolveTrackerProfileRejectsGitHubMissingRepo(t *testing.T) {
	repoRoot := t.TempDir()
	writeTrackerConfigYAML(t, repoRoot, `
profiles:
  default:
    tracker:
      type: github
      github:
        scope:
          owner: anomalyco
        auth:
          token_env: GITHUB_TOKEN
`)

	_, err := resolveTrackerProfile(repoRoot, "", "root-1", func(string) string { return "token" })
	if err == nil {
		t.Fatalf("expected missing github repo to fail")
	}
	if !strings.Contains(err.Error(), `github.scope.repo`) {
		t.Fatalf("expected github repo validation error, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), `.yolo-runner/config.yaml`) {
		t.Fatalf("expected config path guidance, got %q", err.Error())
	}
}

func TestResolveTrackerProfileRejectsGitHubMissingTokenEnv(t *testing.T) {
	repoRoot := t.TempDir()
	writeTrackerConfigYAML(t, repoRoot, `
profiles:
  default:
    tracker:
      type: github
      github:
        scope:
          owner: anomalyco
          repo: yolo-runner
`)

	_, err := resolveTrackerProfile(repoRoot, "", "root-1", func(string) string { return "token" })
	if err == nil {
		t.Fatalf("expected missing github token env to fail")
	}
	if !strings.Contains(err.Error(), `github.auth.token_env`) {
		t.Fatalf("expected github token_env validation error, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), `.yolo-runner/config.yaml`) {
		t.Fatalf("expected config path guidance, got %q", err.Error())
	}
}

func TestResolveTrackerProfileRejectsGitHubMissingTokenValue(t *testing.T) {
	repoRoot := t.TempDir()
	writeTrackerConfigYAML(t, repoRoot, `
profiles:
  default:
    tracker:
      type: github
      github:
        scope:
          owner: anomalyco
          repo: yolo-runner
        auth:
          token_env: GITHUB_TOKEN
`)

	_, err := resolveTrackerProfile(repoRoot, "", "root-1", func(string) string { return "" })
	if err == nil {
		t.Fatalf("expected missing github token value to fail")
	}
	if !strings.Contains(err.Error(), `missing auth token`) {
		t.Fatalf("expected github token value validation error, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), `export GITHUB_TOKEN=<github-personal-access-token>`) {
		t.Fatalf("expected auth token export guidance, got %q", err.Error())
	}
}

func TestResolveTrackerProfileRejectsGitHubMultiRepoConfig(t *testing.T) {
	repoRoot := t.TempDir()
	writeTrackerConfigYAML(t, repoRoot, `
profiles:
  default:
    tracker:
      type: github
      github:
        scope:
          owner: anomalyco
          repo: yolo-runner,another-repo
        auth:
          token_env: GITHUB_TOKEN
`)

	_, err := resolveTrackerProfile(repoRoot, "", "root-1", func(string) string { return "token" })
	if err == nil {
		t.Fatalf("expected multi-repo configuration to fail")
	}
	if !strings.Contains(err.Error(), `single-repo`) {
		t.Fatalf("expected single-repo guidance, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), `github.scope.repo`) {
		t.Fatalf("expected repo field guidance, got %q", err.Error())
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

func TestBuildTaskManagerForTrackerSupportsGitHub(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "ghp_test")
	originalFactory := newGitHubTaskManager
	t.Cleanup(func() {
		newGitHubTaskManager = originalFactory
	})

	var got githubtracker.Config
	newGitHubTaskManager = func(cfg githubtracker.Config) (contracts.TaskManager, error) {
		got = cfg
		return staticTaskManager{}, nil
	}

	manager, err := buildTaskManagerForTracker(t.TempDir(), resolvedTrackerProfile{
		Name: "github",
		Tracker: trackerModel{
			Type: trackerTypeGitHub,
			GitHub: &githubTrackerModel{
				Scope: githubScopeModel{
					Owner: "anomalyco",
					Repo:  "yolo-runner",
				},
				Auth: githubAuthModel{
					TokenEnv: "GITHUB_TOKEN",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("expected github task manager to build, got %v", err)
	}
	if manager == nil {
		t.Fatalf("expected non-nil github task manager")
	}
	if got.Owner != "anomalyco" {
		t.Fatalf("expected owner to be wired, got %q", got.Owner)
	}
	if got.Repo != "yolo-runner" {
		t.Fatalf("expected repo to be wired, got %q", got.Repo)
	}
	if got.Token != "ghp_test" {
		t.Fatalf("expected token to be loaded from env, got %q", got.Token)
	}
}

func TestBuildStorageBackendForTrackerSupportsGitHub(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "ghp_test")
	originalFactory := newGitHubStorageBackend
	t.Cleanup(func() {
		newGitHubStorageBackend = originalFactory
	})

	var got githubtracker.Config
	newGitHubStorageBackend = func(cfg githubtracker.Config) (contracts.StorageBackend, error) {
		got = cfg
		return staticStorageBackend{}, nil
	}

	backend, err := buildStorageBackendForTracker(t.TempDir(), resolvedTrackerProfile{
		Name: "github",
		Tracker: trackerModel{
			Type: trackerTypeGitHub,
			GitHub: &githubTrackerModel{
				Scope: githubScopeModel{
					Owner: "anomalyco",
					Repo:  "yolo-runner",
				},
				Auth: githubAuthModel{
					TokenEnv: "GITHUB_TOKEN",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("expected github storage backend to build, got %v", err)
	}
	if backend == nil {
		t.Fatalf("expected non-nil github storage backend")
	}
	if got.Owner != "anomalyco" {
		t.Fatalf("expected owner to be wired, got %q", got.Owner)
	}
	if got.Repo != "yolo-runner" {
		t.Fatalf("expected repo to be wired, got %q", got.Repo)
	}
	if got.Token != "ghp_test" {
		t.Fatalf("expected token to be loaded from env, got %q", got.Token)
	}
}

func TestBuildStorageBackendForTrackerSupportsTK(t *testing.T) {
	originalFactory := newTKStorageBackend
	t.Cleanup(func() {
		newTKStorageBackend = originalFactory
	})

	repoRoot := t.TempDir()
	var gotRepoRoot string
	newTKStorageBackend = func(root string) (contracts.StorageBackend, error) {
		gotRepoRoot = root
		return staticStorageBackend{}, nil
	}

	backend, err := buildStorageBackendForTracker(repoRoot, resolvedTrackerProfile{
		Name: "default",
		Tracker: trackerModel{
			Type: trackerTypeTK,
		},
	})
	if err != nil {
		t.Fatalf("expected tk storage backend to build, got %v", err)
	}
	if backend == nil {
		t.Fatalf("expected non-nil tk storage backend")
	}
	if gotRepoRoot != repoRoot {
		t.Fatalf("expected tk storage backend factory to receive repo root %q, got %q", repoRoot, gotRepoRoot)
	}
}

func TestBuildStorageBackendForTrackerSupportsLinear(t *testing.T) {
	t.Setenv("LINEAR_TOKEN", "lin_api_test")
	originalFactory := newLinearStorageBackend
	t.Cleanup(func() {
		newLinearStorageBackend = originalFactory
	})

	var got linear.Config
	newLinearStorageBackend = func(cfg linear.Config) (contracts.StorageBackend, error) {
		got = cfg
		return staticStorageBackend{}, nil
	}

	backend, err := buildStorageBackendForTracker(t.TempDir(), resolvedTrackerProfile{
		Name: "linear",
		Tracker: trackerModel{
			Type: trackerTypeLinear,
			Linear: &linearTrackerModel{
				Scope: linearScopeModel{Workspace: "anomaly"},
				Auth:  linearAuthModel{TokenEnv: "LINEAR_TOKEN"},
			},
		},
	})
	if err != nil {
		t.Fatalf("expected linear storage backend to build, got %v", err)
	}
	if backend == nil {
		t.Fatalf("expected non-nil linear storage backend")
	}
	if got.Workspace != "anomaly" {
		t.Fatalf("expected workspace to be wired, got %q", got.Workspace)
	}
	if got.Token != "lin_api_test" {
		t.Fatalf("expected token to be loaded from env, got %q", got.Token)
	}
}

func TestBuildTaskManagerForTrackerWrapsGitHubAuthErrors(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "ghp_invalid")
	originalFactory := newGitHubTaskManager
	t.Cleanup(func() {
		newGitHubTaskManager = originalFactory
	})
	newGitHubTaskManager = func(githubtracker.Config) (contracts.TaskManager, error) {
		return nil, errors.New("bad credentials")
	}

	_, err := buildTaskManagerForTracker(t.TempDir(), resolvedTrackerProfile{
		Name: "github",
		Tracker: trackerModel{
			Type: trackerTypeGitHub,
			GitHub: &githubTrackerModel{
				Scope: githubScopeModel{
					Owner: "anomalyco",
					Repo:  "yolo-runner",
				},
				Auth: githubAuthModel{
					TokenEnv: "GITHUB_TOKEN",
				},
			},
		},
	})
	if err == nil {
		t.Fatalf("expected github auth failure to be returned")
	}
	if !strings.Contains(err.Error(), `github auth validation failed`) {
		t.Fatalf("expected github auth context in error, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), `GITHUB_TOKEN`) {
		t.Fatalf("expected token env to be included, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), `bad credentials`) {
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

func TestBuildStorageBackendForTrackerTKSkipsDanglingDependencyRelations(t *testing.T) {
	originalFactory := newTKStorageBackend
	t.Cleanup(func() {
		newTKStorageBackend = originalFactory
	})
	newTKStorageBackend = func(string) (contracts.StorageBackend, error) {
		return taskManagerStorageBackend{taskManager: newFallbackScenarioTaskManager()}, nil
	}

	backend, err := buildStorageBackendForTracker(t.TempDir(), resolvedTrackerProfile{
		Name: "default",
		Tracker: trackerModel{
			Type: trackerTypeTK,
		},
	})
	if err != nil {
		t.Fatalf("build tk fallback backend: %v", err)
	}

	assertFallbackBackendGraphBuildsWithReadyTask(t, backend)
}

func TestBuildStorageBackendForTrackerLinearSkipsDanglingDependencyRelations(t *testing.T) {
	t.Setenv("LINEAR_TOKEN", "lin_api_test")
	originalFactory := newLinearStorageBackend
	t.Cleanup(func() {
		newLinearStorageBackend = originalFactory
	})
	newLinearStorageBackend = func(linear.Config) (contracts.StorageBackend, error) {
		return taskManagerStorageBackend{taskManager: newFallbackScenarioTaskManager()}, nil
	}

	backend, err := buildStorageBackendForTracker(t.TempDir(), resolvedTrackerProfile{
		Name: "linear",
		Tracker: trackerModel{
			Type: trackerTypeLinear,
			Linear: &linearTrackerModel{
				Scope: linearScopeModel{Workspace: "anomaly"},
				Auth:  linearAuthModel{TokenEnv: "LINEAR_TOKEN"},
			},
		},
	})
	if err != nil {
		t.Fatalf("build linear fallback backend: %v", err)
	}

	assertFallbackBackendGraphBuildsWithReadyTask(t, backend)
}

func TestBuildStorageBackendForTrackerTKUsesTaskManagerTreeForCompletion(t *testing.T) {
	originalFactory := newTKStorageBackend
	t.Cleanup(func() {
		newTKStorageBackend = originalFactory
	})
	newTKStorageBackend = func(string) (contracts.StorageBackend, error) {
		return taskManagerStorageBackend{taskManager: newFallbackCompletionTaskManager()}, nil
	}

	backend, err := buildStorageBackendForTracker(t.TempDir(), resolvedTrackerProfile{
		Name: "default",
		Tracker: trackerModel{
			Type: trackerTypeTK,
		},
	})
	if err != nil {
		t.Fatalf("build tk fallback backend: %v", err)
	}

	assertFallbackBackendTreatsOpenRootWithTerminalChildrenAsComplete(t, backend)
}

func TestBuildStorageBackendForTrackerLinearUsesTaskManagerTreeForCompletion(t *testing.T) {
	t.Setenv("LINEAR_TOKEN", "lin_api_test")
	originalFactory := newLinearStorageBackend
	t.Cleanup(func() {
		newLinearStorageBackend = originalFactory
	})
	newLinearStorageBackend = func(linear.Config) (contracts.StorageBackend, error) {
		return taskManagerStorageBackend{taskManager: newFallbackCompletionTaskManager()}, nil
	}

	backend, err := buildStorageBackendForTracker(t.TempDir(), resolvedTrackerProfile{
		Name: "linear",
		Tracker: trackerModel{
			Type: trackerTypeLinear,
			Linear: &linearTrackerModel{
				Scope: linearScopeModel{Workspace: "anomaly"},
				Auth:  linearAuthModel{TokenEnv: "LINEAR_TOKEN"},
			},
		},
	})
	if err != nil {
		t.Fatalf("build linear fallback backend: %v", err)
	}

	assertFallbackBackendTreatsOpenRootWithTerminalChildrenAsComplete(t, backend)
}

func TestTaskManagerStorageBackendGetTaskReturnsNilWhenTaskManagerReturnsEmptyTask(t *testing.T) {
	backend := taskManagerStorageBackend{taskManager: staticTaskManager{}}

	task, err := backend.GetTask(context.Background(), "missing")
	if err != nil {
		t.Fatalf("GetTask returned error: %v", err)
	}
	if task != nil {
		t.Fatalf("expected nil task for missing lookup, got %#v", task)
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

func assertFallbackBackendGraphBuildsWithReadyTask(t *testing.T, backend contracts.StorageBackend) {
	t.Helper()

	tree, err := backend.GetTaskTree(context.Background(), "root")
	if err != nil {
		t.Fatalf("GetTaskTree returned error: %v", err)
	}

	for _, relation := range tree.Relations {
		if _, ok := tree.Tasks[relation.FromID]; !ok {
			t.Fatalf("relation %q references missing from task %q", relation.Type, relation.FromID)
		}
		if _, ok := tree.Tasks[relation.ToID]; !ok {
			t.Fatalf("relation %q references missing to task %q", relation.Type, relation.ToID)
		}
	}

	taskEngine := enginepkg.NewTaskEngine()
	graph, err := taskEngine.BuildGraph(tree)
	if err != nil {
		t.Fatalf("BuildGraph returned error: %v", err)
	}
	ready := taskEngine.GetNextAvailable(graph)
	if len(ready) != 1 || ready[0].ID != "task-ready" {
		t.Fatalf("expected task-ready to remain runnable, got %#v", ready)
	}
}

func assertFallbackBackendTreatsOpenRootWithTerminalChildrenAsComplete(t *testing.T, backend contracts.StorageBackend) {
	t.Helper()

	tree, err := backend.GetTaskTree(context.Background(), "root")
	if err != nil {
		t.Fatalf("GetTaskTree returned error: %v", err)
	}

	taskEngine := enginepkg.NewTaskEngine()
	graph, err := taskEngine.BuildGraph(tree)
	if err != nil {
		t.Fatalf("BuildGraph returned error: %v", err)
	}

	ready := taskEngine.GetNextAvailable(graph)
	if len(ready) != 0 {
		t.Fatalf("expected no runnable tasks for terminal child graph, got %#v", ready)
	}
	if !taskEngine.IsComplete(graph) {
		t.Fatalf("expected open root with terminal children to be complete")
	}
}

func newFallbackScenarioTaskManager() *fallbackScenarioTaskManager {
	return &fallbackScenarioTaskManager{
		nextTasks: []contracts.TaskSummary{
			{ID: "task-ready", Title: "Task ready"},
		},
		tasks: map[string]contracts.Task{
			"root": {
				ID:     "root",
				Title:  "Root",
				Status: contracts.TaskStatusOpen,
			},
			"task-ready": {
				ID:       "task-ready",
				Title:    "Task ready",
				ParentID: "root",
				Status:   contracts.TaskStatusOpen,
				Metadata: map[string]string{
					"dependencies": "task-closed",
				},
			},
			"task-closed": {
				ID:       "task-closed",
				Title:    "Task closed dependency",
				ParentID: "root",
				Status:   contracts.TaskStatusClosed,
			},
		},
	}
}

type fallbackScenarioTaskManager struct {
	nextTasks []contracts.TaskSummary
	tasks     map[string]contracts.Task
}

func (m *fallbackScenarioTaskManager) NextTasks(_ context.Context, _ string) ([]contracts.TaskSummary, error) {
	return append([]contracts.TaskSummary(nil), m.nextTasks...), nil
}

func (m *fallbackScenarioTaskManager) GetTask(_ context.Context, taskID string) (contracts.Task, error) {
	task, ok := m.tasks[taskID]
	if !ok {
		return contracts.Task{}, fmt.Errorf("task %q not found", taskID)
	}
	return task, nil
}

func (m *fallbackScenarioTaskManager) SetTaskStatus(_ context.Context, _ string, _ contracts.TaskStatus) error {
	return nil
}

func (m *fallbackScenarioTaskManager) SetTaskData(_ context.Context, _ string, _ map[string]string) error {
	return nil
}

type fallbackCompletionTaskManager struct {
	tree  contracts.TaskTree
	tasks map[string]contracts.Task
}

func newFallbackCompletionTaskManager() *fallbackCompletionTaskManager {
	tasks := map[string]contracts.Task{
		"root": {
			ID:     "root",
			Title:  "Root",
			Status: contracts.TaskStatusOpen,
		},
		"task-closed": {
			ID:       "task-closed",
			Title:    "Closed task",
			ParentID: "root",
			Status:   contracts.TaskStatusClosed,
		},
		"task-failed": {
			ID:       "task-failed",
			Title:    "Failed task",
			ParentID: "root",
			Status:   contracts.TaskStatusFailed,
		},
	}
	return &fallbackCompletionTaskManager{
		tree: contracts.TaskTree{
			Root: tasks["root"],
			Tasks: map[string]contracts.Task{
				"root":        tasks["root"],
				"task-closed": tasks["task-closed"],
				"task-failed": tasks["task-failed"],
			},
			Relations: []contracts.TaskRelation{
				{FromID: "root", ToID: "task-closed", Type: contracts.RelationParent},
				{FromID: "root", ToID: "task-failed", Type: contracts.RelationParent},
			},
		},
		tasks: tasks,
	}
}

func (m *fallbackCompletionTaskManager) GetTaskTree(_ context.Context, _ string) (*contracts.TaskTree, error) {
	tree := m.tree
	tree.Tasks = map[string]contracts.Task{}
	for id, task := range m.tree.Tasks {
		tree.Tasks[id] = task
	}
	tree.Relations = append([]contracts.TaskRelation(nil), m.tree.Relations...)
	return &tree, nil
}

func (m *fallbackCompletionTaskManager) NextTasks(_ context.Context, _ string) ([]contracts.TaskSummary, error) {
	return nil, nil
}

func (m *fallbackCompletionTaskManager) GetTask(_ context.Context, taskID string) (contracts.Task, error) {
	task, ok := m.tasks[taskID]
	if !ok {
		return contracts.Task{}, fmt.Errorf("task %q not found", taskID)
	}
	return task, nil
}

func (m *fallbackCompletionTaskManager) SetTaskStatus(_ context.Context, _ string, _ contracts.TaskStatus) error {
	return nil
}

func (m *fallbackCompletionTaskManager) SetTaskData(_ context.Context, _ string, _ map[string]string) error {
	return nil
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

type staticStorageBackend struct{}

func (staticStorageBackend) GetTaskTree(_ context.Context, _ string) (*contracts.TaskTree, error) {
	return &contracts.TaskTree{}, nil
}

func (staticStorageBackend) GetTask(_ context.Context, _ string) (*contracts.Task, error) {
	return &contracts.Task{}, nil
}

func (staticStorageBackend) SetTaskStatus(_ context.Context, _ string, _ contracts.TaskStatus) error {
	return nil
}

func (staticStorageBackend) SetTaskData(_ context.Context, _ string, _ map[string]string) error {
	return nil
}
