package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/anomalyco/yolo-runner/internal/contracts"
	githubtracker "github.com/anomalyco/yolo-runner/internal/github"
	"github.com/anomalyco/yolo-runner/internal/linear"
	"github.com/anomalyco/yolo-runner/internal/tk"
)

const (
	trackerTypeTK     = "tk"
	trackerTypeLinear = "linear"
	trackerTypeGitHub = "github"

	defaultProfileName     = "default"
	trackerConfigRelPath   = ".yolo-runner/config.yaml"
	linearTokenEnvVarLabel = "linear.auth.token_env"
	githubTokenEnvVarLabel = "github.auth.token_env"
)

type profileSelectionInput struct {
	FlagValue string
	EnvValue  string
}

type trackerProfilesModel struct {
	DefaultProfile string                       `yaml:"default_profile"`
	Profiles       map[string]trackerProfileDef `yaml:"profiles"`
	Agent          yoloAgentConfigModel         `yaml:"agent,omitempty"`
	Tracker        trackerModel                 `yaml:"tracker,omitempty"`
}

type trackerProfileDef struct {
	Tracker trackerModel `yaml:"tracker"`
}

type trackerModel struct {
	Type   string              `yaml:"type"`
	TK     *tkTrackerModel     `yaml:"tk,omitempty"`
	Linear *linearTrackerModel `yaml:"linear,omitempty"`
	GitHub *githubTrackerModel `yaml:"github,omitempty"`
}

type tkTrackerModel struct {
	Scope tkScopeModel `yaml:"scope"`
}

type tkScopeModel struct {
	Root string `yaml:"root"`
}

type linearTrackerModel struct {
	Scope linearScopeModel `yaml:"scope"`
	Auth  linearAuthModel  `yaml:"auth"`
}

type linearScopeModel struct {
	Workspace string `yaml:"workspace"`
}

type linearAuthModel struct {
	TokenEnv string `yaml:"token_env"`
}

type githubTrackerModel struct {
	Scope githubScopeModel `yaml:"scope"`
	Auth  githubAuthModel  `yaml:"auth"`
}

type githubScopeModel struct {
	Owner string `yaml:"owner"`
	Repo  string `yaml:"repo"`
}

type githubAuthModel struct {
	TokenEnv string `yaml:"token_env"`
}

type yoloAgentConfigModel struct {
	Backend          string `yaml:"backend,omitempty"`
	Model            string `yaml:"model,omitempty"`
	Concurrency      *int   `yaml:"concurrency,omitempty"`
	RunnerTimeout    string `yaml:"runner_timeout,omitempty"`
	WatchdogTimeout  string `yaml:"watchdog_timeout,omitempty"`
	WatchdogInterval string `yaml:"watchdog_interval,omitempty"`
	RetryBudget      *int   `yaml:"retry_budget,omitempty"`
}

type resolvedTrackerProfile struct {
	Name    string
	Tracker trackerModel
}

var newLinearTaskManager = func(cfg linear.Config) (contracts.TaskManager, error) {
	return linear.NewTaskManager(cfg)
}

var newGitHubTaskManager = func(cfg githubtracker.Config) (contracts.TaskManager, error) {
	return githubtracker.NewTaskManager(cfg)
}

func resolveProfileSelectionPolicy(input profileSelectionInput) string {
	for _, value := range []string{
		input.FlagValue,
		input.EnvValue,
	} {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func resolveTrackerProfile(repoRoot string, selectedProfile string, rootID string, getenv func(string) string) (resolvedTrackerProfile, error) {
	return newTrackerConfigService(repoRoot, getenv).resolveTrackerProfile(selectedProfile, rootID)
}

func buildTaskManagerForTracker(repoRoot string, profile resolvedTrackerProfile) (contracts.TaskManager, error) {
	switch profile.Tracker.Type {
	case trackerTypeTK:
		return tk.NewTaskManager(localRunner{dir: repoRoot}), nil
	case trackerTypeLinear:
		if profile.Tracker.Linear == nil {
			return nil, fmt.Errorf("tracker.linear settings are required for profile %q", profile.Name)
		}
		workspace := strings.TrimSpace(profile.Tracker.Linear.Scope.Workspace)
		if workspace == "" {
			return nil, fmt.Errorf("%s is required for profile %q", "linear.scope.workspace", profile.Name)
		}
		tokenEnv := strings.TrimSpace(profile.Tracker.Linear.Auth.TokenEnv)
		if tokenEnv == "" {
			return nil, fmt.Errorf("%s is required for profile %q", linearTokenEnvVarLabel, profile.Name)
		}
		tokenValue := strings.TrimSpace(os.Getenv(tokenEnv))
		if tokenValue == "" {
			return nil, fmt.Errorf("missing auth token from %s for profile %q", tokenEnv, profile.Name)
		}
		manager, err := newLinearTaskManager(linear.Config{
			Workspace: workspace,
			Token:     tokenValue,
		})
		if err != nil {
			return nil, fmt.Errorf("linear auth validation failed for profile %q using %s: %w", profile.Name, tokenEnv, err)
		}
		return manager, nil
	case trackerTypeGitHub:
		if profile.Tracker.GitHub == nil {
			return nil, fmt.Errorf("tracker.github settings are required for profile %q", profile.Name)
		}
		owner := strings.TrimSpace(profile.Tracker.GitHub.Scope.Owner)
		if owner == "" {
			return nil, fmt.Errorf("%s is required for profile %q", "github.scope.owner", profile.Name)
		}
		repo := strings.TrimSpace(profile.Tracker.GitHub.Scope.Repo)
		if repo == "" {
			return nil, fmt.Errorf("%s is required for profile %q", "github.scope.repo", profile.Name)
		}
		tokenEnv := strings.TrimSpace(profile.Tracker.GitHub.Auth.TokenEnv)
		if tokenEnv == "" {
			return nil, fmt.Errorf("%s is required for profile %q", githubTokenEnvVarLabel, profile.Name)
		}
		tokenValue := strings.TrimSpace(os.Getenv(tokenEnv))
		if tokenValue == "" {
			return nil, fmt.Errorf("missing auth token from %s for profile %q", tokenEnv, profile.Name)
		}
		manager, err := newGitHubTaskManager(githubtracker.Config{
			Owner: owner,
			Repo:  repo,
			Token: tokenValue,
		})
		if err != nil {
			return nil, fmt.Errorf("github auth validation failed for profile %q using %s: %w", profile.Name, tokenEnv, err)
		}
		return manager, nil
	default:
		return nil, fmt.Errorf("tracker type %q is not supported yet", profile.Tracker.Type)
	}
}

func validateTrackerModel(profileName string, model trackerModel, rootID string, getenv func(string) string) (trackerModel, error) {
	model.Type = strings.ToLower(strings.TrimSpace(model.Type))
	if model.Type == "" {
		return trackerModel{}, fmt.Errorf("tracker.type is required for profile %q", profileName)
	}

	switch model.Type {
	case trackerTypeTK:
		if model.TK != nil {
			scopeRoot := strings.TrimSpace(model.TK.Scope.Root)
			if scopeRoot != "" && strings.TrimSpace(rootID) != scopeRoot {
				return trackerModel{}, fmt.Errorf("root %q is outside tk scope %q in profile %q", rootID, scopeRoot, profileName)
			}
		}
		return model, nil
	case trackerTypeLinear:
		if model.Linear == nil {
			return trackerModel{}, fmt.Errorf("tracker.linear settings are required for profile %q", profileName)
		}
		workspace := strings.TrimSpace(model.Linear.Scope.Workspace)
		if workspace == "" {
			return trackerModel{}, fmt.Errorf("%s is required for profile %q in %s; set it to your single Linear workspace slug", "linear.scope.workspace", profileName, trackerConfigRelPath)
		}
		if hasMultipleScopeValues(workspace) {
			return trackerModel{}, fmt.Errorf("%s must contain exactly one workspace for profile %q in %s (single-workspace mode); got %q", "linear.scope.workspace", profileName, trackerConfigRelPath, workspace)
		}
		tokenEnv := strings.TrimSpace(model.Linear.Auth.TokenEnv)
		if tokenEnv == "" {
			return trackerModel{}, fmt.Errorf("%s is required for profile %q in %s; set it to the env var that stores your Linear API token", linearTokenEnvVarLabel, profileName, trackerConfigRelPath)
		}
		if getenv != nil && strings.TrimSpace(getenv(tokenEnv)) == "" {
			return trackerModel{}, fmt.Errorf("missing auth token from %s for profile %q configured in %s; set it in your shell (for example: export %s=<linear-api-token>)", tokenEnv, profileName, trackerConfigRelPath, tokenEnv)
		}
		model.Linear.Scope.Workspace = workspace
		model.Linear.Auth.TokenEnv = tokenEnv
		return model, nil
	case trackerTypeGitHub:
		if model.GitHub == nil {
			return trackerModel{}, fmt.Errorf("tracker.github settings are required for profile %q", profileName)
		}
		owner := strings.TrimSpace(model.GitHub.Scope.Owner)
		if owner == "" {
			return trackerModel{}, fmt.Errorf("%s is required for profile %q in %s; set it to your GitHub organization or username", "github.scope.owner", profileName, trackerConfigRelPath)
		}
		if hasMultipleScopeValues(owner) {
			return trackerModel{}, fmt.Errorf("%s must contain exactly one owner for profile %q in %s (single-owner mode); got %q", "github.scope.owner", profileName, trackerConfigRelPath, owner)
		}
		repo := strings.TrimSpace(model.GitHub.Scope.Repo)
		if repo == "" {
			return trackerModel{}, fmt.Errorf("%s is required for profile %q in %s; set it to your single GitHub repository name", "github.scope.repo", profileName, trackerConfigRelPath)
		}
		if hasMultipleScopeValues(repo) {
			return trackerModel{}, fmt.Errorf("%s must contain exactly one repository for profile %q in %s (single-repo mode); got %q", "github.scope.repo", profileName, trackerConfigRelPath, repo)
		}
		if strings.Contains(repo, "/") {
			return trackerModel{}, fmt.Errorf("%s must be a repository name only for profile %q in %s; set owner separately via github.scope.owner (got %q)", "github.scope.repo", profileName, trackerConfigRelPath, repo)
		}
		tokenEnv := strings.TrimSpace(model.GitHub.Auth.TokenEnv)
		if tokenEnv == "" {
			return trackerModel{}, fmt.Errorf("%s is required for profile %q in %s; set it to the env var that stores your GitHub personal access token", githubTokenEnvVarLabel, profileName, trackerConfigRelPath)
		}
		if getenv != nil && strings.TrimSpace(getenv(tokenEnv)) == "" {
			return trackerModel{}, fmt.Errorf("missing auth token from %s for profile %q configured in %s; set it in your shell (for example: export %s=<github-personal-access-token>)", tokenEnv, profileName, trackerConfigRelPath, tokenEnv)
		}
		model.GitHub.Scope.Owner = owner
		model.GitHub.Scope.Repo = repo
		model.GitHub.Auth.TokenEnv = tokenEnv
		return model, nil
	default:
		return trackerModel{}, fmt.Errorf("unsupported tracker type %q for profile %q", model.Type, profileName)
	}
}

func hasMultipleScopeValues(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return false
	}
	if strings.Contains(trimmed, ",") || strings.Contains(trimmed, ";") {
		return true
	}
	return len(strings.Fields(trimmed)) > 1
}

func sortedProfileNames(profiles map[string]trackerProfileDef) []string {
	if len(profiles) == 0 {
		return nil
	}
	names := make([]string, 0, len(profiles))
	for name := range profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
