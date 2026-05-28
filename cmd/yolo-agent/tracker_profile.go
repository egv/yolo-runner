package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/egv/yolo-runner/v2/internal/beads"
	"github.com/egv/yolo-runner/v2/internal/contracts"
	githubtracker "github.com/egv/yolo-runner/v2/internal/github"
	"github.com/egv/yolo-runner/v2/internal/linear"
	"github.com/egv/yolo-runner/v2/internal/tk"
)

const (
	trackerTypeTK       = "tk"
	trackerTypeLinear   = "linear"
	trackerTypeGitHub   = "github"
	trackerTypeBeads    = "beads"
	trackerTypeStartrek = "startrek"

	landingTypeGit   = "git"
	landingTypeArcPR = "arc-pr"

	defaultProfileName       = "default"
	trackerConfigRelPath     = ".yolo-runner/config.yaml"
	linearTokenEnvVarLabel   = "linear.auth.token_env"
	githubTokenEnvVarLabel   = "github.auth.token_env"
	startrekTokenEnvVarLabel = "startrek.token_env"

	defaultTrackerAgentPollInterval = 30 * time.Second
	defaultTrackerAgentLockPath     = ".yolo-runner/tracker-agent.lock"
	defaultTrackerAgentReadyLabel   = "yolo-agent-ready"
	defaultTrackerAgentRunningLabel = "yolo-agent-in-progress"
	defaultTrackerAgentDoneLabel    = "yolo-agent-completed"
	defaultTrackerAgentBlockedLabel = "yolo-agent-blocked"
	defaultTrackerAgentFailedLabel  = "yolo-agent-failed"
)

type profileSelectionInput struct {
	FlagValue string
	EnvValue  string
}

type trackerProfilesModel struct {
	DefaultProfile string                       `yaml:"default_profile"`
	Profiles       map[string]trackerProfileDef `yaml:"profiles"`
	Agent          yoloAgentConfigModel         `yaml:"agent,omitempty"`
	TrackerAgent   trackerAgentConfigModel      `yaml:"tracker_agent,omitempty"`
	Landing        landingConfigModel           `yaml:"landing,omitempty"`
	Tracker        trackerModel                 `yaml:"tracker,omitempty"`
}

type trackerProfileDef struct {
	Tracker trackerModel `yaml:"tracker"`
}

type trackerModel struct {
	Type     string                `yaml:"type"`
	TK       *tkTrackerModel       `yaml:"tk,omitempty"`
	Linear   *linearTrackerModel   `yaml:"linear,omitempty"`
	GitHub   *githubTrackerModel   `yaml:"github,omitempty"`
	Beads    *beadsTrackerModel    `yaml:"beads,omitempty"`
	Startrek *startrekTrackerModel `yaml:"startrek,omitempty"`
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

type beadsTrackerModel struct {
	// beads_rust doesn't require additional configuration
	// It auto-discovers the .beads directory
}

type startrekTrackerModel struct {
	Endpoint string               `yaml:"endpoint"`
	TokenEnv string               `yaml:"token_env"`
	Queues   []startrekQueueModel `yaml:"queues"`
}

type startrekQueueModel struct {
	Key  string `yaml:"key"`
	Root string `yaml:"root"`
}

type yoloAgentConfigModel struct {
	Backend          string `yaml:"backend,omitempty"`
	Model            string `yaml:"model,omitempty"`
	Mode             string `yaml:"mode,omitempty"`
	Concurrency      *int   `yaml:"concurrency,omitempty"`
	RunnerTimeout    string `yaml:"runner_timeout,omitempty"`
	WatchdogTimeout  string `yaml:"watchdog_timeout,omitempty"`
	WatchdogInterval string `yaml:"watchdog_interval,omitempty"`
	RetryBudget      *int   `yaml:"retry_budget,omitempty"`
}

type trackerAgentConfigModel struct {
	PollInterval string                       `yaml:"poll_interval,omitempty"`
	LockPath     string                       `yaml:"lock_path,omitempty"`
	Labels       trackerAgentLabelNamesConfig `yaml:"labels,omitempty"`
}

type trackerAgentLabelNamesConfig struct {
	Ready      string `yaml:"ready,omitempty"`
	InProgress string `yaml:"in_progress,omitempty"`
	Completed  string `yaml:"completed,omitempty"`
	Blocked    string `yaml:"blocked,omitempty"`
	Failed     string `yaml:"failed,omitempty"`
}

type trackerAgentConfig struct {
	PollInterval time.Duration
	LockPath     string
	Labels       trackerAgentLabelNamesConfig
}

type landingConfigModel struct {
	Type          string `yaml:"type,omitempty"`
	TitleTemplate string `yaml:"title_template,omitempty"`
}

type landingConfig struct {
	Type          string
	TitleTemplate string
}

type resolvedTrackerProfile struct {
	Name    string
	Tracker trackerModel
}

var newLinearTaskManager = func(cfg linear.Config) (contracts.TaskManager, error) {
	return linear.NewTaskManager(cfg)
}

var newLinearStorageBackend = func(cfg linear.Config) (contracts.StorageBackend, error) {
	return linear.NewStorageBackend(cfg)
}

var newTKTaskManager = func(repoRoot string) (contracts.TaskManager, error) {
	return tk.NewTaskManager(localRunner{dir: repoRoot}), nil
}

var newTKStorageBackend = func(repoRoot string) (contracts.StorageBackend, error) {
	return tk.NewStorageBackendWithGitPersistence(localRunner{dir: repoRoot}), nil
}

var newGitHubTaskManager = func(cfg githubtracker.Config) (contracts.TaskManager, error) {
	return githubtracker.NewTaskManager(cfg)
}

var newGitHubStorageBackend = func(cfg githubtracker.Config) (contracts.StorageBackend, error) {
	return githubtracker.NewStorageBackend(cfg)
}

var newBeadsTaskManager = func(repoRoot string) (contracts.TaskManager, error) {
	return beads.NewTaskManager(localRunner{dir: repoRoot}, repoRoot), nil
}

var newBeadsStorageBackend = func(repoRoot string) (contracts.StorageBackend, error) {
	return beads.NewStorageBackend(localRunner{dir: repoRoot}, repoRoot), nil
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
	return newTrackerConfigService().ResolveTrackerProfile(repoRoot, selectedProfile, rootID, getenv)
}

func buildTaskManagerForTracker(repoRoot string, profile resolvedTrackerProfile) (contracts.TaskManager, error) {
	switch profile.Tracker.Type {
	case trackerTypeTK:
		return newTKTaskManager(repoRoot)
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
	case trackerTypeBeads:
		return newBeadsTaskManager(repoRoot)
	default:
		return nil, fmt.Errorf("tracker type %q is not supported yet", profile.Tracker.Type)
	}
}

func buildStorageBackendForTracker(repoRoot string, profile resolvedTrackerProfile) (contracts.StorageBackend, error) {
	switch profile.Tracker.Type {
	case trackerTypeTK:
		backend, err := newTKStorageBackend(repoRoot)
		if err != nil {
			return nil, err
		}
		return backend, nil
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
		backend, err := newGitHubStorageBackend(githubtracker.Config{
			Owner:     owner,
			Repo:      repo,
			Token:     tokenValue,
			StatePath: filepath.Join(repoRoot, ".yolo-runner", fmt.Sprintf("github-state-%s-%s.json", owner, repo)),
		})
		if err != nil {
			return nil, fmt.Errorf("github auth validation failed for profile %q using %s: %w", profile.Name, tokenEnv, err)
		}
		return backend, nil
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
		backend, err := newLinearStorageBackend(linear.Config{
			Workspace: workspace,
			Token:     tokenValue,
		})
		if err != nil {
			return nil, fmt.Errorf("linear auth validation failed for profile %q using %s: %w", profile.Name, tokenEnv, err)
		}
		return backend, nil
	case trackerTypeBeads:
		return newBeadsStorageBackend(repoRoot)
	default:
		return nil, fmt.Errorf("tracker type %q is not supported yet", profile.Tracker.Type)
	}
}

type taskManagerStorageBackend struct {
	taskManager contracts.TaskManager
}

var _ contracts.StorageBackend = taskManagerStorageBackend{}

type taskTreeProvider interface {
	GetTaskTree(ctx context.Context, rootID string) (*contracts.TaskTree, error)
}

func (b taskManagerStorageBackend) GetTaskTree(ctx context.Context, rootID string) (*contracts.TaskTree, error) {
	if provider, ok := b.taskManager.(taskTreeProvider); ok {
		tree, err := provider.GetTaskTree(ctx, rootID)
		if err != nil {
			return nil, err
		}
		if tree != nil {
			return tree, nil
		}
	}

	rootTask, err := b.taskManager.GetTask(ctx, rootID)
	if err != nil {
		rootTask = contracts.Task{ID: rootID, Title: rootID, Status: contracts.TaskStatusOpen}
	}
	if strings.TrimSpace(rootTask.ID) == "" {
		rootTask.ID = strings.TrimSpace(rootID)
	}

	tasks := map[string]contracts.Task{
		rootTask.ID: rootTask,
	}
	relations := make([]contracts.TaskRelation, 0)
	seenRelations := map[string]struct{}{}

	readyTasks, err := b.taskManager.NextTasks(ctx, rootID)
	if err != nil {
		return nil, err
	}
	readyTaskRecords := make([]contracts.Task, 0, len(readyTasks))
	for _, summary := range readyTasks {
		task, err := b.taskManager.GetTask(ctx, summary.ID)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(task.ID) == "" {
			task.ID = strings.TrimSpace(summary.ID)
		}
		if strings.TrimSpace(task.Title) == "" {
			task.Title = strings.TrimSpace(summary.Title)
		}
		tasks[task.ID] = task
		readyTaskRecords = append(readyTaskRecords, task)
	}

	for _, task := range readyTaskRecords {
		parentID := strings.TrimSpace(task.ParentID)
		if parentID == "" {
			parentID = rootTask.ID
		}
		if _, ok := tasks[parentID]; !ok {
			parentID = rootTask.ID
		}
		appendUniqueRelation(&relations, seenRelations, contracts.TaskRelation{
			FromID: parentID,
			ToID:   task.ID,
			Type:   contracts.RelationParent,
		})

		for _, depID := range dependencyIDsFromTask(task) {
			if depID == "" || depID == task.ID {
				continue
			}
			if _, ok := tasks[depID]; !ok {
				continue
			}
			appendUniqueRelation(&relations, seenRelations, contracts.TaskRelation{
				FromID: task.ID,
				ToID:   depID,
				Type:   contracts.RelationDependsOn,
			})
			appendUniqueRelation(&relations, seenRelations, contracts.TaskRelation{
				FromID: depID,
				ToID:   task.ID,
				Type:   contracts.RelationBlocks,
			})
		}
	}

	return &contracts.TaskTree{
		Root:      rootTask,
		Tasks:     tasks,
		Relations: relations,
	}, nil
}

func (b taskManagerStorageBackend) GetTask(ctx context.Context, taskID string) (*contracts.Task, error) {
	task, err := b.taskManager.GetTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(task.ID) == "" {
		return nil, nil
	}
	return &task, nil
}

func (b taskManagerStorageBackend) SetTaskStatus(ctx context.Context, taskID string, status contracts.TaskStatus) error {
	return b.taskManager.SetTaskStatus(ctx, taskID, status)
}

func (b taskManagerStorageBackend) SetTaskData(ctx context.Context, taskID string, data map[string]string) error {
	return b.taskManager.SetTaskData(ctx, taskID, data)
}

func appendUniqueRelation(relations *[]contracts.TaskRelation, seen map[string]struct{}, relation contracts.TaskRelation) {
	if relation.FromID == "" || relation.ToID == "" || relation.FromID == relation.ToID {
		return
	}
	key := string(relation.Type) + "|" + relation.FromID + "|" + relation.ToID
	if _, ok := seen[key]; ok {
		return
	}
	seen[key] = struct{}{}
	*relations = append(*relations, relation)
}

func dependencyIDsFromTask(task contracts.Task) []string {
	if len(task.Metadata) == 0 {
		return nil
	}
	rawDeps := strings.TrimSpace(task.Metadata["dependencies"])
	if rawDeps == "" {
		return nil
	}

	seen := map[string]struct{}{}
	deps := make([]string, 0)
	for _, part := range strings.Split(rawDeps, ",") {
		depID := strings.TrimSpace(part)
		if depID == "" {
			continue
		}
		if _, ok := seen[depID]; ok {
			continue
		}
		seen[depID] = struct{}{}
		deps = append(deps, depID)
	}
	return deps
}

func defaultTrackerProfilesModel() trackerProfilesModel {
	return trackerProfilesModel{
		DefaultProfile: defaultProfileName,
		Profiles: map[string]trackerProfileDef{
			defaultProfileName: {
				Tracker: trackerModel{
					Type: trackerTypeTK,
				},
			},
		},
	}
}

func resolveTrackerAgentConfig(model trackerAgentConfigModel, repoRoot string) (trackerAgentConfig, error) {
	cfg := defaultTrackerAgentConfig()

	if rawPollInterval := strings.TrimSpace(model.PollInterval); rawPollInterval != "" {
		pollInterval, err := time.ParseDuration(rawPollInterval)
		if err != nil {
			return trackerAgentConfig{}, fmt.Errorf("tracker_agent.poll_interval in %s must be a valid duration: %w", trackerConfigRelPath, err)
		}
		if pollInterval <= 0 {
			return trackerAgentConfig{}, fmt.Errorf("tracker_agent.poll_interval in %s must be greater than 0", trackerConfigRelPath)
		}
		cfg.PollInterval = pollInterval
	}

	if lockPath := strings.TrimSpace(model.LockPath); lockPath != "" {
		cfg.LockPath = lockPath
	}
	cfg.LockPath = resolveTrackerAgentLockPath(repoRoot, cfg.LockPath)

	cfg.Labels.Ready = resolveTrackerAgentLabel(model.Labels.Ready, cfg.Labels.Ready)
	cfg.Labels.InProgress = resolveTrackerAgentLabel(model.Labels.InProgress, cfg.Labels.InProgress)
	cfg.Labels.Completed = resolveTrackerAgentLabel(model.Labels.Completed, cfg.Labels.Completed)
	cfg.Labels.Blocked = resolveTrackerAgentLabel(model.Labels.Blocked, cfg.Labels.Blocked)
	cfg.Labels.Failed = resolveTrackerAgentLabel(model.Labels.Failed, cfg.Labels.Failed)

	return cfg, nil
}

func defaultTrackerAgentConfig() trackerAgentConfig {
	return trackerAgentConfig{
		PollInterval: defaultTrackerAgentPollInterval,
		LockPath:     defaultTrackerAgentLockPath,
		Labels: trackerAgentLabelNamesConfig{
			Ready:      defaultTrackerAgentReadyLabel,
			InProgress: defaultTrackerAgentRunningLabel,
			Completed:  defaultTrackerAgentDoneLabel,
			Blocked:    defaultTrackerAgentBlockedLabel,
			Failed:     defaultTrackerAgentFailedLabel,
		},
	}
}

func resolveTrackerAgentLockPath(repoRoot string, lockPath string) string {
	cleaned := filepath.Clean(strings.TrimSpace(lockPath))
	if filepath.IsAbs(cleaned) || strings.TrimSpace(repoRoot) == "" {
		return cleaned
	}
	return filepath.Join(repoRoot, cleaned)
}

func resolveTrackerAgentLabel(raw string, fallback string) string {
	if label := strings.TrimSpace(raw); label != "" {
		return label
	}
	return fallback
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
	case trackerTypeBeads:
		// beads_rust auto-discovers the .beads directory, no additional validation needed
		return model, nil
	case trackerTypeStartrek:
		if model.Startrek == nil {
			return trackerModel{}, fmt.Errorf("tracker.startrek settings are required for profile %q", profileName)
		}
		endpoint := strings.TrimSpace(model.Startrek.Endpoint)
		if endpoint == "" {
			return trackerModel{}, fmt.Errorf("%s is required for profile %q in %s; set it to your Startrek API endpoint", "startrek.endpoint", profileName, trackerConfigRelPath)
		}
		tokenEnv := strings.TrimSpace(model.Startrek.TokenEnv)
		if tokenEnv == "" {
			return trackerModel{}, fmt.Errorf("%s is required for profile %q in %s; set it to the env var that stores your Startrek API token", startrekTokenEnvVarLabel, profileName, trackerConfigRelPath)
		}
		if getenv != nil && strings.TrimSpace(getenv(tokenEnv)) == "" {
			return trackerModel{}, fmt.Errorf("missing auth token from %s for profile %q configured in %s; set it in your shell (for example: export %s=<startrek-api-token>)", tokenEnv, profileName, trackerConfigRelPath, tokenEnv)
		}
		if len(model.Startrek.Queues) == 0 {
			return trackerModel{}, fmt.Errorf("%s is required for profile %q in %s; configure at least one Startrek queue to Arcadia root mapping", "startrek.queues", profileName, trackerConfigRelPath)
		}

		for i := range model.Startrek.Queues {
			key := strings.TrimSpace(model.Startrek.Queues[i].Key)
			if key == "" {
				return trackerModel{}, fmt.Errorf("%s is required for profile %q in %s", fmt.Sprintf("startrek.queues[%d].key", i), profileName, trackerConfigRelPath)
			}
			root := strings.TrimSpace(model.Startrek.Queues[i].Root)
			if root == "" {
				return trackerModel{}, fmt.Errorf("%s is required for profile %q in %s; set it to an existing Arcadia root path", fmt.Sprintf("startrek.queues[%d].root", i), profileName, trackerConfigRelPath)
			}
			cleanRoot := filepath.Clean(root)
			info, err := os.Stat(cleanRoot)
			if err != nil {
				return trackerModel{}, fmt.Errorf("%s must point to an existing Arcadia root path for profile %q in %s: %w", fmt.Sprintf("startrek.queues[%d].root", i), profileName, trackerConfigRelPath, err)
			}
			if !info.IsDir() {
				return trackerModel{}, fmt.Errorf("%s must point to an existing Arcadia root directory for profile %q in %s; got %q", fmt.Sprintf("startrek.queues[%d].root", i), profileName, trackerConfigRelPath, cleanRoot)
			}
			model.Startrek.Queues[i].Key = key
			model.Startrek.Queues[i].Root = cleanRoot
		}
		model.Startrek.Endpoint = endpoint
		model.Startrek.TokenEnv = tokenEnv
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
