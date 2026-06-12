package envpreset

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
	"gopkg.in/yaml.v3"
)

type WorkspaceStrategy string

const (
	WorkspaceStrategyGitClone  WorkspaceStrategy = "git-clone"
	WorkspaceStrategyArcShared WorkspaceStrategy = "arc-shared"
	WorkspaceStrategyPath      WorkspaceStrategy = "path"
)

type LandingType string

const (
	LandingTypeGitMerge LandingType = "git-merge"
	LandingTypeArcPR    LandingType = "arc-pr"
	LandingTypeNone     LandingType = "none"
)

type Preset struct {
	Workspace Workspace `yaml:"workspace"`
	Landing   Landing   `yaml:"landing"`
	Agent     Agent     `yaml:"agent"`
	Limits    Limits    `yaml:"limits"`
	Env       Env       `yaml:"env"`
}

type Workspace struct {
	Strategy   WorkspaceStrategy `yaml:"strategy"`
	Origin     string            `yaml:"origin"`
	BaseBranch string            `yaml:"base_branch"`
	Mount      string            `yaml:"mount"`
	Subpath    string            `yaml:"subpath"`
	Path       string            `yaml:"path"`
	VCS        contracts.VCS     `yaml:"-"`
	Cleanup    func() error      `yaml:"-"`
}

type Landing struct {
	Type          LandingType `yaml:"type"`
	TitleTemplate string      `yaml:"title_template"`
}

type Agent struct {
	Backend          string        `yaml:"backend"`
	Model            string        `yaml:"model"`
	RunnerTimeout    time.Duration `yaml:"runner_timeout"`
	WatchdogTimeout  time.Duration `yaml:"watchdog_timeout"`
	WatchdogInterval time.Duration `yaml:"watchdog_interval"`
}

type Limits struct {
	MaxConcurrent int `yaml:"max_concurrent"`
}

type Env struct {
	Passthrough []string          `yaml:"passthrough"`
	Set         map[string]string `yaml:"set"`
}

type fileModel struct {
	Presets map[string]presetModel `yaml:"presets"`
}

type presetModel struct {
	Workspace Workspace   `yaml:"workspace"`
	Landing   Landing     `yaml:"landing"`
	Agent     agentModel  `yaml:"agent"`
	Limits    limitsModel `yaml:"limits"`
	Env       Env         `yaml:"env"`
}

type agentModel struct {
	Backend          string `yaml:"backend"`
	Model            string `yaml:"model"`
	RunnerTimeout    string `yaml:"runner_timeout"`
	WatchdogTimeout  string `yaml:"watchdog_timeout"`
	WatchdogInterval string `yaml:"watchdog_interval"`
}

type limitsModel struct {
	MaxConcurrent *int `yaml:"max_concurrent"`
}

func Load(path string) (map[string]Preset, error) {
	configPath, err := expandHome(path)
	if err != nil {
		return nil, err
	}

	content, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("cannot read environments file %s: %w", path, err)
	}

	var model fileModel
	decoder := yaml.NewDecoder(strings.NewReader(string(content)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&model); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("cannot parse environments file %s: %w", path, err)
	}

	presets, err := validateModel(model)
	if err != nil {
		return nil, fmt.Errorf("invalid environments file %s: %w", path, err)
	}
	return presets, nil
}

func validateModel(model fileModel) (map[string]Preset, error) {
	if len(model.Presets) == 0 {
		return nil, fmt.Errorf("presets must define at least one preset")
	}

	names := make([]string, 0, len(model.Presets))
	for name := range model.Presets {
		names = append(names, name)
	}
	sort.Strings(names)

	presets := make(map[string]Preset, len(model.Presets))
	for _, name := range names {
		trimmedName := strings.TrimSpace(name)
		if trimmedName == "" {
			return nil, fmt.Errorf("preset name must not be empty")
		}
		if trimmedName != name {
			return nil, fmt.Errorf("preset name %q must not have leading or trailing whitespace", name)
		}

		modelPreset := model.Presets[name]
		workspace, err := validateWorkspace(name, modelPreset.Workspace)
		if err != nil {
			return nil, err
		}
		landing, err := validateLanding(name, modelPreset.Landing)
		if err != nil {
			return nil, err
		}
		agent, err := validateAgent(name, modelPreset.Agent)
		if err != nil {
			return nil, err
		}
		limits, err := validateLimits(name, modelPreset.Limits)
		if err != nil {
			return nil, err
		}
		env, err := validateEnv(name, modelPreset.Env)
		if err != nil {
			return nil, err
		}

		presets[name] = Preset{
			Workspace: workspace,
			Landing:   landing,
			Agent:     agent,
			Limits:    limits,
			Env:       env,
		}
	}
	return presets, nil
}

func validateWorkspace(presetName string, workspace Workspace) (Workspace, error) {
	workspace.Strategy = WorkspaceStrategy(strings.ToLower(strings.TrimSpace(string(workspace.Strategy))))
	workspace.Origin = strings.TrimSpace(workspace.Origin)
	workspace.BaseBranch = strings.TrimSpace(workspace.BaseBranch)
	workspace.Mount = strings.TrimSpace(workspace.Mount)
	workspace.Subpath = strings.TrimSpace(workspace.Subpath)
	workspace.Path = strings.TrimSpace(workspace.Path)

	switch workspace.Strategy {
	case WorkspaceStrategyGitClone:
		if workspace.Origin == "" {
			return Workspace{}, fmt.Errorf("presets.%s.workspace.origin is required when workspace.strategy is %s", presetName, WorkspaceStrategyGitClone)
		}
		if workspace.BaseBranch == "" {
			return Workspace{}, fmt.Errorf("presets.%s.workspace.base_branch is required when workspace.strategy is %s", presetName, WorkspaceStrategyGitClone)
		}
	case WorkspaceStrategyArcShared:
		if workspace.Mount == "" {
			return Workspace{}, fmt.Errorf("presets.%s.workspace.mount is required when workspace.strategy is %s", presetName, WorkspaceStrategyArcShared)
		}
		if workspace.Subpath == "" {
			return Workspace{}, fmt.Errorf("presets.%s.workspace.subpath is required when workspace.strategy is %s", presetName, WorkspaceStrategyArcShared)
		}
	case WorkspaceStrategyPath:
		if workspace.Path == "" {
			return Workspace{}, fmt.Errorf("presets.%s.workspace.path is required when workspace.strategy is %s", presetName, WorkspaceStrategyPath)
		}
	default:
		return Workspace{}, fmt.Errorf("presets.%s.workspace.strategy must be one of: %s, %s, %s (got %q)", presetName, WorkspaceStrategyGitClone, WorkspaceStrategyArcShared, WorkspaceStrategyPath, workspace.Strategy)
	}
	return workspace, nil
}

func validateLanding(presetName string, landing Landing) (Landing, error) {
	landing.Type = LandingType(strings.ToLower(strings.TrimSpace(string(landing.Type))))
	landing.TitleTemplate = strings.TrimSpace(landing.TitleTemplate)

	switch landing.Type {
	case LandingTypeGitMerge, LandingTypeArcPR, LandingTypeNone:
		return landing, nil
	default:
		return Landing{}, fmt.Errorf("presets.%s.landing.type must be one of: %s, %s, %s (got %q)", presetName, LandingTypeGitMerge, LandingTypeArcPR, LandingTypeNone, landing.Type)
	}
}

func validateAgent(presetName string, model agentModel) (Agent, error) {
	runnerTimeout, err := parseDuration(presetName, "runner_timeout", model.RunnerTimeout, false)
	if err != nil {
		return Agent{}, err
	}
	watchdogTimeout, err := parseDuration(presetName, "watchdog_timeout", model.WatchdogTimeout, true)
	if err != nil {
		return Agent{}, err
	}
	watchdogInterval, err := parseDuration(presetName, "watchdog_interval", model.WatchdogInterval, true)
	if err != nil {
		return Agent{}, err
	}

	return Agent{
		Backend:          strings.TrimSpace(model.Backend),
		Model:            strings.TrimSpace(model.Model),
		RunnerTimeout:    runnerTimeout,
		WatchdogTimeout:  watchdogTimeout,
		WatchdogInterval: watchdogInterval,
	}, nil
}

func validateLimits(presetName string, model limitsModel) (Limits, error) {
	if model.MaxConcurrent == nil {
		return Limits{}, nil
	}
	if *model.MaxConcurrent <= 0 {
		return Limits{}, fmt.Errorf("presets.%s.limits.max_concurrent must be greater than 0", presetName)
	}
	return Limits{MaxConcurrent: *model.MaxConcurrent}, nil
}

func validateEnv(presetName string, env Env) (Env, error) {
	for i, name := range env.Passthrough {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			return Env{}, fmt.Errorf("presets.%s.env.passthrough[%d] must not be empty", presetName, i)
		}
		env.Passthrough[i] = trimmed
	}

	if len(env.Set) == 0 {
		return env, nil
	}

	set := make(map[string]string, len(env.Set))
	for name, value := range env.Set {
		trimmedName := strings.TrimSpace(name)
		if trimmedName == "" {
			return Env{}, fmt.Errorf("presets.%s.env.set must not contain an empty variable name", presetName)
		}
		set[trimmedName] = value
	}
	env.Set = set
	return env, nil
}

func parseDuration(presetName string, field string, raw string, mustBePositive bool) (time.Duration, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, nil
	}

	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("presets.%s.agent.%s must be a valid duration: %w", presetName, field, err)
	}
	if mustBePositive && parsed <= 0 {
		return 0, fmt.Errorf("presets.%s.agent.%s must be greater than 0", presetName, field)
	}
	if !mustBePositive && parsed < 0 {
		return 0, fmt.Errorf("presets.%s.agent.%s must be greater than or equal to 0", presetName, field)
	}
	return parsed, nil
}

func expandHome(path string) (string, error) {
	if path == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot expand ~ in environments path: %w", err)
		}
		return home, nil
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot expand ~ in environments path: %w", err)
		}
		return filepath.Join(home, strings.TrimPrefix(path, "~/")), nil
	}
	return path, nil
}
