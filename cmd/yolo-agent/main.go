package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egv/yolo-runner/v2/internal/agent"
	"github.com/egv/yolo-runner/v2/internal/claude"
	"github.com/egv/yolo-runner/v2/internal/codex"
	"github.com/egv/yolo-runner/v2/internal/codingagents"
	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/distributed"
	"github.com/egv/yolo-runner/v2/internal/engine"
	"github.com/egv/yolo-runner/v2/internal/kimi"
	"github.com/egv/yolo-runner/v2/internal/opencode"
	gitvcs "github.com/egv/yolo-runner/v2/internal/vcs/git"
	"github.com/egv/yolo-runner/v2/internal/version"
)

const (
	backendOpenCode     = "opencode"
	backendCodex        = "codex"
	backendClaude       = "claude"
	backendKimi         = "kimi"
	backendGemini       = "gemini"
	agentModeStream     = "stream"
	agentModeUI         = "ui"
	agentRoleLocal      = "local"
	agentRoleMaster     = "mastermind"
	agentRoleWorker     = "executor"
	distributedBusRedis = "redis"
	distributedBusNATS  = "nats"
	inboxAuthTokenEnv   = "YOLO_INBOX_WRITE_TOKEN"
	monitorSourceIDEnv  = "YOLO_MONITOR_SOURCE_ID"
)

var taskGraphSyncInterval = 5 * time.Second

type runConfig struct {
	repoRoot                        string
	rootID                          string
	backend                         string
	profile                         string
	trackerType                     string
	model                           string
	qualityThreshold                int
	qualityGateTools                []string
	qcGateTools                     []string
	allowLowQuality                 bool
	maxTasks                        int
	retryBudget                     int
	concurrency                     int
	dryRun                          bool
	mode                            string
	stream                          bool
	verboseStream                   bool
	streamOutputInterval            time.Duration
	streamOutputBuffer              int
	tddMode                         bool
	runnerTimeout                   time.Duration
	watchdogTimeout                 time.Duration
	watchdogInterval                time.Duration
	eventsPath                      string
	role                            string
	distributedBusBackend           string
	distributedBusAddress           string
	distributedBusPrefix            string
	distributedRoleID               string
	codingAgents                    codingagents.Catalog
	distributedExecutorCapabilities []distributed.Capability
	distributedHeartbeatInterval    time.Duration
	distributedRequestTimeout       time.Duration
	distributedRegistryTTL          time.Duration
	distributedEventBus             distributed.Bus
}

var newDistributedBus = func(backend string, address string) (distributed.Bus, error) {
	switch backend {
	case distributedBusRedis:
		return distributed.NewRedisBus(address)
	case distributedBusNATS:
		return distributed.NewNATSBus(address)
	default:
		return nil, fmt.Errorf("unsupported distributed bus backend %q", backend)
	}
}

var loadCodingAgentsCatalog = codingagents.LoadCatalog

var runConfigValidateCommand = defaultRunConfigValidateCommand
var launchYoloTUI = func() (io.WriteCloser, func() error, error) {
	cmd := exec.Command("yolo-tui", "--events-stdin")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, err
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, nil, err
	}
	return stdin, func() error {
		_ = stdin.Close()
		return cmd.Wait()
	}, nil
}

var runConfigInitCommand = defaultRunConfigInitCommand

func RunMain(args []string, run func(context.Context, runConfig) error) int {
	if version.IsVersionRequest(args) {
		version.Print(os.Stdout, "yolo-agent")
		return 0
	}

	if len(args) > 0 && args[0] == "config" {
		return runConfigCommand(args[1:])
	}

	fs := flag.NewFlagSet("yolo-agent", flag.ContinueOnError)
	repo := fs.String("repo", ".", "Repository root")
	root := fs.String("root", "", "Root task ID")
	backend := fs.String("backend", "", "DEPRECATED: use --agent-backend (opencode, codex, claude, kimi, gemini)")
	agentBackend := fs.String("agent-backend", "", "Runner backend (opencode, codex, claude, kimi, gemini)")
	model := fs.String("model", "", "Model for CLI agent")
	profile := fs.String("profile", "", "Tracker profile name from .yolo-runner/config.yaml")
	qualityThreshold := fs.Int("quality-threshold", 0, "Minimum quality score required to run a task")
	qualityGateTools := fs.String("quality-gate-tools", "", "Comma-separated quality tools to run in quality gate")
	qcGateTools := fs.String("qc-gate-tools", "", "Comma-separated quality tools to run in quality-control gate")
	allowLowQuality := fs.Bool("allow-low-quality", false, "Proceed with warning when quality score is below threshold")
	max := fs.Int("max", 0, "Maximum tasks to execute")
	concurrency := fs.Int("concurrency", 1, "Maximum number of active task workers")
	dryRun := fs.Bool("dry-run", false, "Dry run task loop")
	stream := fs.Bool("stream", false, "Emit NDJSON events to stdout for piping into yolo-tui")
	verboseStream := fs.Bool("verbose-stream", false, "Emit every runner_output event without coalescing")
	tddMode := fs.Bool("tdd", false, "Enable strict test-first Red/Green/Refactor workflow")
	streamOutputInterval := fs.Duration("stream-output-interval", 150*time.Millisecond, "Minimum interval between emitted runner_output events when not verbose")
	streamOutputBuffer := fs.Int("stream-output-buffer", 64, "Maximum coalesced runner_output events retained before drop")
	mode := fs.String("mode", "", "Output mode for runner events (stream, ui)")
	runnerTimeout := fs.Duration("runner-timeout", 0, "Per runner execution timeout")
	watchdogTimeout := fs.Duration("watchdog-timeout", 10*time.Minute, "No-output watchdog timeout for each runner execution")
	watchdogInterval := fs.Duration("watchdog-interval", 5*time.Second, "Polling interval used by the no-output watchdog")
	retryBudget := fs.Int("retry-budget", 5, "Maximum retry attempts per task for remediation loop")
	events := fs.String("events", "", "Path to JSONL events log")
	role := fs.String("role", "", "Distributed execution role: local, mastermind, executor")
	distributedBusBackend := fs.String("distributed-bus-backend", "", "Distributed bus backend (redis, nats)")
	distributedBusAddress := fs.String("distributed-bus-address", "", "Distributed bus address")
	distributedBusPrefix := fs.String("distributed-bus-prefix", "", "Distributed bus subject prefix")
	distributedExecutorID := fs.String("distributed-executor-id", "", "Distributed executor id (executor role)")
	distributedExecutorCapabilities := fs.String("distributed-executor-capabilities", "", "Comma-separated capabilities to advertise in executor role")
	distributedHeartbeatInterval := fs.Duration("distributed-heartbeat-interval", 5*time.Second, "Heartbeat interval in executor role")
	distributedRequestTimeout := fs.Duration("distributed-request-timeout", 30*time.Second, "Request timeout for task dispatch in mastermind/executor roles")
	distributedRegistryTTL := fs.Duration("distributed-registry-ttl", 30*time.Second, "Executor registration TTL in mastermind role")
	var err error
	if err = fs.Parse(args); err != nil {
		return 1
	}
	setFlags := map[string]struct{}{}
	fs.Visit(func(f *flag.Flag) {
		setFlags[f.Name] = struct{}{}
	})
	flagWasSet := func(name string) bool {
		_, ok := setFlags[name]
		return ok
	}
	selectedRole := strings.TrimSpace(*role)
	if selectedRole == "" {
		selectedRole = strings.TrimSpace(os.Getenv("YOLO_DISTRIBUTED_ROLE"))
	}
	selectedRole, err = normalizeDistributedRole(selectedRole)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	if *root == "" && selectedRole != agentRoleWorker {
		fmt.Fprintln(os.Stderr, "--root is required")
		return 1
	}
	codingAgents, err := loadCodingAgentsCatalog(*repo)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	configDefaults, err := loadYoloAgentConfigDefaults(*repo)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defaultBackend := strings.TrimSpace(os.Getenv("YOLO_AGENT_BACKEND"))
	if defaultBackend == "" {
		defaultBackend = configDefaults.Backend
	}
	selectedBackendRaw := resolveBackendSelectionPolicy(backendSelectionPolicyInput{
		AgentBackendFlag:      *agentBackend,
		LegacyBackendFlag:     *backend,
		ProfileDefaultBackend: defaultBackend,
	})
	selectedProfile := resolveProfileSelectionPolicy(profileSelectionInput{
		FlagValue: *profile,
		EnvValue:  os.Getenv("YOLO_PROFILE"),
	})
	selectedModel := strings.TrimSpace(*model)
	if selectedModel == "" {
		selectedModel = configDefaults.Model
	}
	selectedConcurrency := *concurrency
	if !flagWasSet("concurrency") && configDefaults.Concurrency != nil {
		selectedConcurrency = *configDefaults.Concurrency
	}
	selectedRunnerTimeout := *runnerTimeout
	if !flagWasSet("runner-timeout") && configDefaults.RunnerTimeout != nil {
		selectedRunnerTimeout = *configDefaults.RunnerTimeout
	}
	selectedWatchdogTimeout := *watchdogTimeout
	if !flagWasSet("watchdog-timeout") && configDefaults.WatchdogTimeout != nil {
		selectedWatchdogTimeout = *configDefaults.WatchdogTimeout
	}
	selectedWatchdogInterval := *watchdogInterval
	if !flagWasSet("watchdog-interval") && configDefaults.WatchdogInterval != nil {
		selectedWatchdogInterval = *configDefaults.WatchdogInterval
	}
	selectedRetryBudget := *retryBudget
	if !flagWasSet("retry-budget") && configDefaults.RetryBudget != nil {
		selectedRetryBudget = *configDefaults.RetryBudget
	}
	selectedMode := strings.TrimSpace(configDefaults.Mode)
	if *mode != "" {
		selectedMode = strings.TrimSpace(*mode)
	}
	if *stream {
		selectedMode = agentModeStream
	}
	selectedMode, err = normalizeAndValidateAgentMode(selectedMode, "mode")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	selectedStream := selectedMode == agentModeStream || selectedMode == agentModeUI
	selectedBackend, _, err := selectBackend(selectedBackendRaw, backendSelectionOptions{
		RequireReview: true,
		Stream:        selectedStream,
	}, catalogBackendCapabilities(codingAgents))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if selectedModel == "" {
		selectedModel = catalogBackendDefaultModel(codingAgents, selectedBackend)
	}
	if err := codingAgents.ValidateBackendUsage(selectedBackend, selectedModel, os.Getenv); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if selectedConcurrency <= 0 {
		fmt.Fprintln(os.Stderr, "--concurrency must be greater than 0")
		return 1
	}
	if *streamOutputInterval <= 0 {
		fmt.Fprintln(os.Stderr, "--stream-output-interval must be greater than 0")
		return 1
	}
	if *streamOutputBuffer <= 0 {
		fmt.Fprintln(os.Stderr, "--stream-output-buffer must be greater than 0")
		return 1
	}
	if *qualityThreshold < 0 {
		fmt.Fprintln(os.Stderr, "--quality-threshold must be greater than or equal to 0")
		return 1
	}
	selectedQualityGateTools := parseQualityGateTools(*qualityGateTools)
	selectedQCGateTools := parseQualityGateTools(*qcGateTools)
	if selectedWatchdogTimeout <= 0 {
		fmt.Fprintln(os.Stderr, "--watchdog-timeout must be greater than 0")
		return 1
	}
	if selectedWatchdogInterval <= 0 {
		fmt.Fprintln(os.Stderr, "--watchdog-interval must be greater than 0")
		return 1
	}
	if selectedRetryBudget < 0 {
		fmt.Fprintln(os.Stderr, "--retry-budget must be greater than or equal to 0")
		return 1
	}
	selectedDistributedBusBackend := strings.TrimSpace(*distributedBusBackend)
	if selectedDistributedBusBackend == "" {
		selectedDistributedBusBackend = strings.TrimSpace(os.Getenv("YOLO_DISTRIBUTED_BUS_BACKEND"))
	}
	if selectedDistributedBusBackend == "" {
		selectedDistributedBusBackend = distributedBusRedis
	}
	selectedDistributedBusBackend, err = normalizeDistributedBusBackend(selectedDistributedBusBackend)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	selectedDistributedBusAddress := strings.TrimSpace(*distributedBusAddress)
	if selectedDistributedBusAddress == "" {
		selectedDistributedBusAddress = strings.TrimSpace(os.Getenv("YOLO_DISTRIBUTED_BUS_ADDRESS"))
	}
	selectedDistributedBusPrefix := strings.TrimSpace(*distributedBusPrefix)
	if selectedDistributedBusPrefix == "" {
		selectedDistributedBusPrefix = strings.TrimSpace(os.Getenv("YOLO_DISTRIBUTED_BUS_PREFIX"))
	}
	if selectedDistributedBusPrefix == "" {
		selectedDistributedBusPrefix = "yolo"
	}
	selectedDistributedExecutorCapabilities, err := distributedExecutorCapabilitiesForBackend(codingAgents, selectedBackend, *distributedExecutorCapabilities, flagWasSet("distributed-executor-capabilities"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	selectedDistributedHeartbeatInterval := *distributedHeartbeatInterval
	selectedDistributedRequestTimeout := *distributedRequestTimeout
	selectedDistributedRegistryTTL := *distributedRegistryTTL
	if selectedDistributedHeartbeatInterval <= 0 {
		fmt.Fprintln(os.Stderr, "--distributed-heartbeat-interval must be greater than 0")
		return 1
	}
	if selectedDistributedRequestTimeout <= 0 {
		fmt.Fprintln(os.Stderr, "--distributed-request-timeout must be greater than 0")
		return 1
	}
	if selectedDistributedRegistryTTL <= 0 {
		fmt.Fprintln(os.Stderr, "--distributed-registry-ttl must be greater than 0")
		return 1
	}

	if run == nil {
		run = defaultRun
	}

	if err := run(context.Background(), runConfig{
		repoRoot:                        *repo,
		rootID:                          *root,
		backend:                         selectedBackend,
		profile:                         selectedProfile,
		model:                           selectedModel,
		maxTasks:                        *max,
		retryBudget:                     selectedRetryBudget,
		concurrency:                     selectedConcurrency,
		dryRun:                          *dryRun,
		stream:                          selectedStream,
		mode:                            selectedMode,
		verboseStream:                   *verboseStream,
		tddMode:                         *tddMode,
		streamOutputInterval:            *streamOutputInterval,
		streamOutputBuffer:              *streamOutputBuffer,
		qualityThreshold:                *qualityThreshold,
		qualityGateTools:                selectedQualityGateTools,
		qcGateTools:                     selectedQCGateTools,
		allowLowQuality:                 *allowLowQuality,
		runnerTimeout:                   selectedRunnerTimeout,
		watchdogTimeout:                 selectedWatchdogTimeout,
		watchdogInterval:                selectedWatchdogInterval,
		eventsPath:                      *events,
		role:                            selectedRole,
		distributedBusBackend:           selectedDistributedBusBackend,
		distributedBusAddress:           selectedDistributedBusAddress,
		distributedBusPrefix:            selectedDistributedBusPrefix,
		distributedRoleID:               strings.TrimSpace(*distributedExecutorID),
		codingAgents:                    codingAgents,
		distributedExecutorCapabilities: selectedDistributedExecutorCapabilities,
		distributedHeartbeatInterval:    selectedDistributedHeartbeatInterval,
		distributedRequestTimeout:       selectedDistributedRequestTimeout,
		distributedRegistryTTL:          selectedDistributedRegistryTTL,
	}); err != nil {
		fmt.Fprintln(os.Stderr, agent.FormatActionableError(err))
		return 1
	}
	return 0
}

func runConfigCommand(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: yolo-agent config <validate|init> [flags]")
		return 1
	}

	switch args[0] {
	case "validate":
		return runConfigValidateCommand(args[1:])
	case "init":
		return runConfigInitCommand(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown config command: %s\n", args[0])
		fmt.Fprintln(os.Stderr, "usage: yolo-agent config <validate|init> [flags]")
		return 1
	}
}

func parseQualityGateTools(raw string) []string {
	parts := strings.FieldsFunc(strings.TrimSpace(raw), func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\t' || r == ' '
	})
	tools := make([]string, 0, len(parts))
	for _, tool := range parts {
		tool = strings.TrimSpace(tool)
		if tool == "" {
			continue
		}
		tools = append(tools, tool)
	}
	return tools
}

func main() {
	os.Exit(RunMain(os.Args[1:], nil))
}

func defaultRun(ctx context.Context, cfg runConfig) error {
	if err := resolveRunConfigCodingAgents(&cfg); err != nil {
		return err
	}
	if cfg.role == agentRoleWorker {
		return runDistributedExecutor(ctx, cfg)
	}
	originalWD, originalWDErr := os.Getwd()
	if err := os.Chdir(cfg.repoRoot); err != nil {
		return err
	}
	if originalWDErr == nil {
		defer func() {
			_ = os.Chdir(originalWD)
		}()
	}
	cfg.eventsPath = resolveEventsPath(cfg)

	trackerProfile, err := resolveTrackerProfile(cfg.repoRoot, cfg.profile, cfg.rootID, os.Getenv)
	if err != nil {
		return err
	}
	cfg.profile = trackerProfile.Name
	cfg.trackerType = trackerProfile.Tracker.Type
	storageBackend, err := buildStorageBackendForTracker(cfg.repoRoot, trackerProfile)
	if err != nil {
		return err
	}
	taskStatusBackends := map[string]contracts.StorageBackend{}
	if strings.TrimSpace(cfg.trackerType) != "" {
		taskStatusBackends[strings.ToLower(strings.TrimSpace(cfg.trackerType))] = storageBackend
	}
	vcsAdapter := gitvcs.NewVCSAdapter(localGitRunner{dir: cfg.repoRoot})
	runnerAdapter, err := buildRunnerAdapter(cfg)
	if err != nil {
		return err
	}
	runnerAdapter, distributedBus, closeDistributed, err := maybeWrapWithMastermind(ctx, cfg, runnerAdapter, taskStatusBackends)
	if err != nil {
		return err
	}
	cfg.distributedEventBus = distributedBus
	if closeDistributed != nil {
		defer func() {
			_ = closeDistributed()
		}()
	}

	taskEngine := engine.NewTaskEngine()
	return runWithStorageComponents(ctx, cfg, storageBackend, taskEngine, runnerAdapter, vcsAdapter)
}

func buildRunnerAdapter(cfg runConfig) (contracts.AgentRunner, error) {
	selectedBackend := normalizeBackend(cfg.backend)
	if selectedBackend == "" {
		return nil, fmt.Errorf("unsupported runner backend %q", cfg.backend)
	}
	definition, ok := cfg.codingAgents.Backend(selectedBackend)
	if !ok {
		return nil, fmt.Errorf("unsupported runner backend %q", cfg.backend)
	}

	switch definition.Adapter {
	case "opencode":
		command := append([]string{}, definition.Args...)
		return opencode.NewCLIRunnerAdapter(opencode.CommandRunner{}, nil, defaultConfigRoot(), defaultConfigDir(), definition.Binary, command...), nil
	case "codex":
		return codex.NewCLIRunnerAdapter(definition.Binary, nil, definition.Args...), nil
	case "claude":
		return claude.NewCLIRunnerAdapter(definition.Binary, nil, definition.Args...), nil
	case "kimi":
		return kimi.NewCLIRunnerAdapter(definition.Binary, nil, definition.Args...), nil
	case "command":
		return codingagents.NewGenericCLIRunnerAdapter(definition.Name, definition.Binary, definition.Args, nil), nil
	default:
		return nil, fmt.Errorf("unsupported runner backend adapter %q", definition.Adapter)
	}
}

func maybeWrapWithMastermind(ctx context.Context, cfg runConfig, localRunner contracts.AgentRunner, taskStatusBackends map[string]contracts.StorageBackend) (contracts.AgentRunner, distributed.Bus, func() error, error) {
	if cfg.role != agentRoleMaster {
		return localRunner, nil, nil, nil
	}
	taskStatusBackends = discoverTaskStatusBackendsForMastermind(cfg, taskStatusBackends)
	bus, err := newDistributedBus(cfg.distributedBusBackend, cfg.distributedBusAddress)
	if err != nil {
		return nil, nil, nil, err
	}
	subjects := distributed.DefaultEventSubjects(cfg.distributedBusPrefix)
	id := strings.TrimSpace(cfg.distributedRoleID)
	if id == "" {
		id = "mastermind"
	}
	mastermind := distributed.NewMastermind(distributed.MastermindOptions{
		ID:                    id,
		Bus:                   bus,
		Subjects:              subjects,
		RegistryTTL:           cfg.distributedRegistryTTL,
		RequestTimeout:        cfg.distributedRequestTimeout,
		ServiceHandler:        defaultServiceHandler(localRunner),
		StatusUpdateBackends:  toTaskStatusWriterMap(taskStatusBackends),
		StatusUpdateAuthToken: strings.TrimSpace(os.Getenv(inboxAuthTokenEnv)),
		TaskGraphSyncRoots:    []string{strings.TrimSpace(cfg.rootID)},
		TaskGraphSyncInterval: taskGraphSyncInterval,
	})
	if err := mastermind.Start(ctx); err != nil {
		_ = bus.Close()
		return nil, nil, nil, err
	}
	return distributedMastermindRunner{mastermind: mastermind}, bus, bus.Close, nil
}

func discoverTaskStatusBackendsForMastermind(cfg runConfig, taskStatusBackends map[string]contracts.StorageBackend) map[string]contracts.StorageBackend {
	if strings.TrimSpace(cfg.role) != agentRoleMaster || strings.TrimSpace(cfg.repoRoot) == "" {
		return taskStatusBackends
	}
	if taskStatusBackends == nil {
		taskStatusBackends = map[string]contracts.StorageBackend{}
	}
	model, err := newTrackerConfigService().LoadModel(cfg.repoRoot)
	if err != nil {
		return taskStatusBackends
	}

	profileNames := make([]string, 0, len(model.Profiles))
	for name := range model.Profiles {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		profileNames = append(profileNames, name)
	}
	sort.Strings(profileNames)
	for _, profileName := range profileNames {
		profileDef, ok := model.Profiles[profileName]
		if !ok {
			continue
		}
		tracker, err := validateTrackerModel(profileName, profileDef.Tracker, cfg.rootID, os.Getenv)
		if err != nil {
			continue
		}
		backendID := strings.ToLower(strings.TrimSpace(tracker.Type))
		if backendID == "" {
			continue
		}
		if _, exists := taskStatusBackends[backendID]; exists {
			continue
		}

		backend, err := buildStorageBackendForTracker(cfg.repoRoot, resolvedTrackerProfile{
			Name:    profileName,
			Tracker: tracker,
		})
		if err != nil {
			continue
		}
		taskStatusBackends[backendID] = backend
	}

	return taskStatusBackends
}

func toTaskStatusWriterMap(backends map[string]contracts.StorageBackend) map[string]distributed.TaskStatusWriter {
	if len(backends) == 0 {
		return nil
	}
	out := make(map[string]distributed.TaskStatusWriter, len(backends))
	for backendID, backend := range backends {
		out[backendID] = backend
	}
	return out
}

func runDistributedExecutor(ctx context.Context, cfg runConfig) error {
	if err := resolveRunConfigCodingAgents(&cfg); err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	bus, err := newDistributedBus(cfg.distributedBusBackend, cfg.distributedBusAddress)
	if err != nil {
		return err
	}
	defer func() {
		_ = bus.Close()
	}()

	runners, err := buildDistributedAgentRunners(cfg)
	if err != nil {
		return err
	}
	if len(runners) == 0 {
		return fmt.Errorf("no coding agents available for distributed execution")
	}
	subjects := distributed.DefaultEventSubjects(cfg.distributedBusPrefix)
	executorID := strings.TrimSpace(cfg.distributedRoleID)
	if executorID == "" {
		executorID = "executor"
	}
	worker := distributed.NewExecutorWorker(distributed.ExecutorWorkerOptions{
		ID:       executorID,
		Bus:      bus,
		Backends: runners,
		AgentResolver: func(metadata map[string]string) (string, contracts.AgentRunner, error) {
			return resolveDistributedRunnerSelection(cfg, runners, metadata)
		},
		Subjects:          subjects,
		Capabilities:      cfg.distributedExecutorCapabilities,
		HeartbeatInterval: cfg.distributedHeartbeatInterval,
		RequestTimeout:    cfg.distributedRequestTimeout,
	})
	return worker.Start(ctx)
}

func buildDistributedAgentRunners(cfg runConfig) (map[string]contracts.AgentRunner, error) {
	names := cfg.codingAgents.Names()
	if len(names) == 0 {
		return nil, fmt.Errorf("no coding agent backends configured")
	}
	runners := make(map[string]contracts.AgentRunner, len(names))
	for _, name := range names {
		name = normalizeBackend(name)
		if name == "" {
			continue
		}
		agentCfg := cfg
		agentCfg.backend = name
		runner, err := buildRunnerAdapter(agentCfg)
		if err != nil {
			return nil, fmt.Errorf("build runner for backend %q: %w", name, err)
		}
		runners[name] = runner
	}
	return runners, nil
}

func resolveDistributedRunnerSelection(cfg runConfig, runners map[string]contracts.AgentRunner, metadata map[string]string) (string, contracts.AgentRunner, error) {
	selectedBackend := resolveAgentBackendFromMetadata(metadata, cfg.codingAgents, cfg.backend)
	selectedBackend = normalizeBackend(selectedBackend)
	if selectedBackend == "" {
		return "", nil, fmt.Errorf("agent selection resolved to empty backend")
	}
	runner, ok := runners[selectedBackend]
	if !ok || runner == nil {
		return "", nil, fmt.Errorf("no runner configured for backend %q", selectedBackend)
	}
	if err := cfg.codingAgents.ValidateBackendUsage(selectedBackend, "", os.Getenv); err != nil {
		return "", nil, err
	}
	return selectedBackend, runner, nil
}

func resolveAgentBackendFromMetadata(metadata map[string]string, catalog codingagents.Catalog, defaultBackend string) string {
	if metadata == nil {
		metadata = map[string]string{}
	}
	explicitBackend := strings.TrimSpace(strings.ToLower(metadata["backend"]))
	if explicitBackend == "" {
		explicitBackend = strings.TrimSpace(strings.ToLower(metadata["agent"]))
	}
	if explicitBackend == "" {
		explicitBackend = strings.TrimSpace(strings.ToLower(metadata["agent_name"]))
	}
	if explicitBackend != "" {
		return explicitBackend
	}

	languages := parseMetadataCSV(metadata["language"])
	if len(languages) == 0 {
		languages = parseMetadataCSV(metadata["languages"])
	}
	features := parseMetadataCSV(metadata["feature"])
	if len(features) == 0 {
		features = parseMetadataCSV(metadata["features"])
	}
	if len(languages) == 0 && len(features) == 0 {
		return normalizeBackend(defaultBackend)
	}

	matches := make([]string, 0, 2)
	for _, name := range catalog.Names() {
		definition, ok := catalog.Backend(name)
		if !ok {
			continue
		}
		if !backendMatchesCapabilities(definition, languages, features) {
			continue
		}
		matches = append(matches, name)
	}
	sort.Strings(matches)
	if len(matches) == 0 {
		return normalizeBackend(defaultBackend)
	}
	return matches[0]
}

func parseMetadataCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(strings.ToLower(part))
		if value == "" {
			continue
		}
		duplicate := false
		for _, existing := range values {
			if existing == value {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		values = append(values, value)
	}
	return values
}

func backendMatchesCapabilities(definition codingagents.BackendDefinition, languages []string, features []string) bool {
	if len(languages) > 0 {
		for _, language := range languages {
			if !containsStringIgnoreCase(definition.Capabilities.Languages, language) {
				return false
			}
		}
	}
	if len(features) > 0 {
		for _, feature := range features {
			if !containsStringIgnoreCase(definition.Capabilities.Features, feature) {
				return false
			}
		}
	}
	return true
}

func containsStringIgnoreCase(values []string, raw string) bool {
	target := strings.TrimSpace(strings.ToLower(raw))
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), target) {
			return true
		}
	}
	return false
}

type distributedMastermindRunner struct {
	mastermind *distributed.Mastermind
}

func (r distributedMastermindRunner) Run(ctx context.Context, request contracts.RunnerRequest) (contracts.RunnerResult, error) {
	if r.mastermind == nil {
		return contracts.RunnerResult{}, fmt.Errorf("mastermind runner unavailable")
	}
	return r.mastermind.DispatchTask(ctx, distributed.TaskDispatchRequest{
		RunnerRequest: request,
	})
}

func defaultServiceHandler(localRunner contracts.AgentRunner) distributed.ServiceHandler {
	if localRunner == nil {
		return nil
	}

	return func(ctx context.Context, request distributed.ServiceRequestPayload) (distributed.ServiceResponsePayload, error) {
		if ctx == nil {
			ctx = context.Background()
		}
		service := normalizeDistributedServiceName(request.Service)
		mode, model, ok := serviceModeAndModel(service)
		if !ok {
			return distributed.ServiceResponsePayload{}, fmt.Errorf("unsupported service %q", strings.TrimSpace(request.Service))
		}

		runnerRequest := buildServiceRunnerRequest(request, mode, model)
		result, err := localRunner.Run(ctx, runnerRequest)

		response := distributed.ServiceResponsePayload{
			RequestID:     request.RequestID,
			CorrelationID: request.CorrelationID,
			ExecutorID:    request.ExecutorID,
			Service:       request.Service,
			Artifacts:     copyStringMap(result.Artifacts),
			Error:         "",
		}
		if result.Artifacts == nil {
			response.Artifacts = map[string]string{}
		}
		if err != nil {
			if strings.TrimSpace(err.Error()) != "" {
				response.Error = err.Error()
			}
			return response, err
		}
		if result.Status != contracts.RunnerResultCompleted {
			if strings.TrimSpace(result.Reason) == "" {
				response.Error = fmt.Sprintf("service runner returned status %q", result.Status)
				return response, fmt.Errorf("%s", response.Error)
			}
			response.Error = result.Reason
			return response, fmt.Errorf("%s", response.Error)
		}

		if response.Artifacts == nil {
			response.Artifacts = map[string]string{}
		}
		if _, ok := response.Artifacts["service"]; !ok {
			response.Artifacts["service"] = strings.TrimSpace(request.Service)
		}
		if _, ok := response.Artifacts["mode"]; !ok {
			response.Artifacts["mode"] = string(mode)
		}
		return response, nil
	}
}

func serviceModeAndModel(service string) (contracts.RunnerMode, string, bool) {
	switch service {
	case string(distributed.CapabilityLargerModel), "review-with-larger-model":
		return contracts.RunnerModeReview, "", true
	case string(distributed.CapabilityRewriteTask), "rewrite-task":
		return contracts.RunnerModeImplement, "", true
	case string(distributed.CapabilityReview):
		return contracts.RunnerModeReview, "", true
	case string(distributed.CapabilityImplement):
		return contracts.RunnerModeImplement, "", true
	default:
		return "", "", false
	}
}

func buildServiceRunnerRequest(request distributed.ServiceRequestPayload, mode contracts.RunnerMode, model string) contracts.RunnerRequest {
	metadata := copyStringMap(request.Metadata)
	if metadata == nil {
		metadata = map[string]string{}
	}
	runnerRequest := contracts.RunnerRequest{
		TaskID:   strings.TrimSpace(request.TaskID),
		ParentID: strings.TrimSpace(metadata["parent_id"]),
		Prompt:   strings.TrimSpace(metadata["prompt"]),
		Mode:     mode,
		Model:    strings.TrimSpace(metadata["model"]),
		RepoRoot: strings.TrimSpace(metadata["repo_root"]),
		Metadata: metadata,
	}
	if strings.TrimSpace(request.TaskID) == "" {
		runnerRequest.TaskID = strings.TrimSpace(request.RequestID)
	}
	if model != "" && runnerRequest.Model == "" {
		runnerRequest.Model = model
	}
	if timeoutRaw := strings.TrimSpace(metadata["timeout"]); timeoutRaw != "" {
		if timeout, err := time.ParseDuration(timeoutRaw); err == nil {
			runnerRequest.Timeout = timeout
		}
	}
	return runnerRequest
}

func normalizeDistributedServiceName(raw string) string {
	service := strings.TrimSpace(strings.ToLower(raw))
	return strings.ReplaceAll(service, "_", "-")
}

func copyStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return out
}

func resolveEventsPath(cfg runConfig) string {
	if cfg.eventsPath != "" {
		return cfg.eventsPath
	}
	if cfg.stream {
		return ""
	}
	return filepath.Join(cfg.repoRoot, "runner-logs", "agent.events.jsonl")
}

func runWithComponents(ctx context.Context, cfg runConfig, taskManager contracts.TaskManager, runner contracts.AgentRunner, vcs contracts.VCS) error {
	sinks := []contracts.EventSink{}
	closers := []func(){}
	if sink := monitorEventSink(cfg); sink != nil {
		sinks = append(sinks, sink)
	}
	if cfg.stream {
		streamWriter := io.Writer(os.Stdout)
		if cfg.mode == agentModeUI {
			stdin, closeFn, err := launchYoloTUI()
			if err != nil {
				return fmt.Errorf("start yolo-tui: %w", err)
			}
			streamWriter = stdin
			closers = append(closers, func() {
				_ = closeFn()
			})
		}
		sinks = append(sinks, contracts.NewStreamEventSinkWithOptions(streamWriter, contracts.StreamEventSinkOptions{
			VerboseOutput:  cfg.verboseStream,
			OutputInterval: cfg.streamOutputInterval,
			MaxPending:     cfg.streamOutputBuffer,
		}))
	}
	if cfg.eventsPath != "" {
		fileSink := contracts.NewFileEventSink(cfg.eventsPath)
		if cfg.stream {
			mirror := newMirrorEventSink(fileSink, cfg.streamOutputBuffer)
			closers = append(closers, mirror.Close)
			sinks = append(sinks, mirror)
		} else {
			sinks = append(sinks, fileSink)
		}
	}
	defer func() {
		for _, closeFn := range closers {
			closeFn()
		}
	}()
	eventSink := contracts.EventSink(nil)
	if len(sinks) == 1 {
		eventSink = sinks[0]
	} else if len(sinks) > 1 {
		eventSink = contracts.NewFanoutEventSink(sinks...)
	}
	vcsFactory := cloneScopedVCSFactory(cfg, vcs)
	loop := agent.NewLoop(taskManager, runner, eventSink, agent.LoopOptions{
		ParentID:             cfg.rootID,
		MaxRetries:           cfg.retryBudget,
		MaxTasks:             cfg.maxTasks,
		Concurrency:          cfg.concurrency,
		QualityGateThreshold: cfg.qualityThreshold,
		QualityGateTools:     cfg.qualityGateTools,
		QCGateTools:          cfg.qcGateTools,
		AllowLowQuality:      cfg.allowLowQuality,
		SchedulerStatePath:   filepath.Join(cfg.repoRoot, ".yolo-runner", "scheduler-state.json"),
		DryRun:               cfg.dryRun,
		RepoRoot:             cfg.repoRoot,
		Backend:              cfg.backend,
		Model:                cfg.model,
		RunnerTimeout:        cfg.runnerTimeout,
		WatchdogTimeout:      cfg.watchdogTimeout,
		WatchdogInterval:     cfg.watchdogInterval,
		TDDMode:              cfg.tddMode,
		VCS:                  vcs,
		RequireReview:        true,
		MergeOnSuccess:       true,
		CloneManager:         agent.NewGitCloneManager(filepath.Join(cfg.repoRoot, ".yolo-runner", "clones")),
		VCSFactory:           vcsFactory,
	})
	if eventSink != nil {
		_ = eventSink.Emit(ctx, contracts.Event{
			Type:      contracts.EventTypeRunStarted,
			TaskID:    cfg.rootID,
			TaskTitle: "run",
			Metadata:  buildRunStartedMetadata(cfg),
			Timestamp: time.Now().UTC(),
		})
	}

	summary, err := loop.Run(ctx)
	if eventSink != nil {
		_ = eventSink.Emit(ctx, contracts.Event{
			Type:      contracts.EventTypeRunFinished,
			TaskID:    cfg.rootID,
			TaskTitle: "run",
			Metadata:  buildRunFinishedMetadata(cfg, summary, err),
			Timestamp: time.Now().UTC(),
		})
	}
	return err
}

func runWithStorageComponents(ctx context.Context, cfg runConfig, storage contracts.StorageBackend, taskEngine contracts.TaskEngine, runner contracts.AgentRunner, vcs contracts.VCS) error {
	sinks := []contracts.EventSink{}
	closers := []func(){}
	if sink := monitorEventSink(cfg); sink != nil {
		sinks = append(sinks, sink)
	}
	if cfg.stream {
		streamWriter := io.Writer(os.Stdout)
		if cfg.mode == agentModeUI {
			stdin, closeFn, err := launchYoloTUI()
			if err != nil {
				return fmt.Errorf("start yolo-tui: %w", err)
			}
			streamWriter = stdin
			closers = append(closers, func() {
				_ = closeFn()
			})
		}
		sinks = append(sinks, contracts.NewStreamEventSinkWithOptions(streamWriter, contracts.StreamEventSinkOptions{
			VerboseOutput:  cfg.verboseStream,
			OutputInterval: cfg.streamOutputInterval,
			MaxPending:     cfg.streamOutputBuffer,
		}))
	}
	if cfg.eventsPath != "" {
		fileSink := contracts.NewFileEventSink(cfg.eventsPath)
		if cfg.stream {
			mirror := newMirrorEventSink(fileSink, cfg.streamOutputBuffer)
			closers = append(closers, mirror.Close)
			sinks = append(sinks, mirror)
		} else {
			sinks = append(sinks, fileSink)
		}
	}
	defer func() {
		for _, closeFn := range closers {
			closeFn()
		}
	}()
	eventSink := contracts.EventSink(nil)
	if len(sinks) == 1 {
		eventSink = sinks[0]
	} else if len(sinks) > 1 {
		eventSink = contracts.NewFanoutEventSink(sinks...)
	}
	vcsFactory := cloneScopedVCSFactory(cfg, vcs)
	loop := agent.NewLoopWithTaskEngine(storage, taskEngine, runner, eventSink, agent.LoopOptions{
		ParentID:             cfg.rootID,
		MaxRetries:           cfg.retryBudget,
		MaxTasks:             cfg.maxTasks,
		Concurrency:          cfg.concurrency,
		QualityGateThreshold: cfg.qualityThreshold,
		QualityGateTools:     cfg.qualityGateTools,
		QCGateTools:          cfg.qcGateTools,
		AllowLowQuality:      cfg.allowLowQuality,
		SchedulerStatePath:   filepath.Join(cfg.repoRoot, ".yolo-runner", "scheduler-state.json"),
		DryRun:               cfg.dryRun,
		RepoRoot:             cfg.repoRoot,
		Backend:              cfg.backend,
		Model:                cfg.model,
		RunnerTimeout:        cfg.runnerTimeout,
		WatchdogTimeout:      cfg.watchdogTimeout,
		WatchdogInterval:     cfg.watchdogInterval,
		TDDMode:              cfg.tddMode,
		VCS:                  vcs,
		RequireReview:        true,
		MergeOnSuccess:       true,
		CloneManager:         agent.NewGitCloneManager(filepath.Join(cfg.repoRoot, ".yolo-runner", "clones")),
		VCSFactory:           vcsFactory,
	})
	if eventSink != nil {
		_ = eventSink.Emit(ctx, contracts.Event{
			Type:      contracts.EventTypeRunStarted,
			TaskID:    cfg.rootID,
			TaskTitle: "run",
			Metadata:  buildRunStartedMetadata(cfg),
			Timestamp: time.Now().UTC(),
		})
	}

	summary, err := loop.Run(ctx)
	if eventSink != nil {
		_ = eventSink.Emit(ctx, contracts.Event{
			Type:      contracts.EventTypeRunFinished,
			TaskID:    cfg.rootID,
			TaskTitle: "run",
			Metadata:  buildRunFinishedMetadata(cfg, summary, err),
			Timestamp: time.Now().UTC(),
		})
	}
	return err
}

func monitorEventSink(cfg runConfig) contracts.EventSink {
	if cfg.distributedEventBus == nil {
		return nil
	}
	busSource := strings.TrimSpace(cfg.distributedRoleID)
	if busSource == "" {
		busSource = defaultMonitorEventSource(cfg.role)
	}
	subjects := distributed.DefaultEventSubjects(cfg.distributedBusPrefix)
	return newDistributedMonitorEventSink(cfg.distributedEventBus, subjects.MonitorEvent, busSource)
}

func defaultMonitorEventSource(role string) string {
	switch strings.TrimSpace(role) {
	case agentRoleMaster:
		return "mastermind"
	case agentRoleWorker:
		return "worker"
	default:
		return "agent"
	}
}

type distributedMonitorEventSink struct {
	bus     distributed.Bus
	subject string
	source  string
}

func newDistributedMonitorEventSink(bus distributed.Bus, subject string, source string) contracts.EventSink {
	if bus == nil {
		return nil
	}
	subject = strings.TrimSpace(subject)
	if subject == "" {
		subject = distributed.DefaultEventSubjects("yolo").MonitorEvent
	}
	if strings.TrimSpace(source) == "" {
		source = "agent"
	}
	return &distributedMonitorEventSink{bus: bus, subject: subject, source: source}
}

func (sink *distributedMonitorEventSink) Emit(ctx context.Context, event contracts.Event) error {
	if sink == nil || sink.bus == nil {
		return nil
	}
	envelope, err := distributed.NewEventEnvelope(distributed.EventTypeMonitorEvent, sink.source, "", distributed.MonitorEventPayload{Event: event})
	if err != nil {
		return err
	}
	return sink.bus.Publish(ctx, sink.subject, envelope)
}

func cloneScopedVCSFactory(cfg runConfig, vcs contracts.VCS) agent.VCSFactory {
	if _, ok := vcs.(*gitvcs.VCSAdapter); !ok {
		return nil
	}
	return func(repoRoot string) contracts.VCS {
		targetRoot := repoRoot
		if targetRoot == "" {
			targetRoot = cfg.repoRoot
		}
		return gitvcs.NewVCSAdapter(localGitRunner{dir: targetRoot})
	}
}

func buildRunStartedMetadata(cfg runConfig) map[string]string {
	return map[string]string{
		"root_id":                cfg.rootID,
		"backend":                normalizeBackend(cfg.backend),
		"profile":                strings.TrimSpace(cfg.profile),
		"tracker":                strings.TrimSpace(cfg.trackerType),
		"quality_threshold":      strconv.Itoa(cfg.qualityThreshold),
		"retry_budget":           strconv.Itoa(cfg.retryBudget),
		"concurrency":            strconv.Itoa(cfg.concurrency),
		"model":                  cfg.model,
		"allow_low_quality":      strconv.FormatBool(cfg.allowLowQuality),
		"runner_timeout":         cfg.runnerTimeout.String(),
		"stream":                 strconv.FormatBool(cfg.stream),
		"verbose_stream":         strconv.FormatBool(cfg.verboseStream),
		"stream_output_interval": cfg.streamOutputInterval.String(),
		"stream_output_buffer":   strconv.Itoa(cfg.streamOutputBuffer),
		"watchdog_timeout":       cfg.watchdogTimeout.String(),
		"watchdog_interval":      cfg.watchdogInterval.String(),
	}
}

func buildRunFinishedMetadata(cfg runConfig, summary contracts.LoopSummary, runErr error) map[string]string {
	status := "completed"
	metadata := map[string]string{
		"root_id":         cfg.rootID,
		"status":          status,
		"completed":       strconv.Itoa(summary.Completed),
		"blocked":         strconv.Itoa(summary.Blocked),
		"failed":          strconv.Itoa(summary.Failed),
		"skipped":         strconv.Itoa(summary.Skipped),
		"total_processed": strconv.Itoa(summary.TotalProcessed()),
	}
	if runErr != nil {
		metadata["status"] = "failed"
		metadata["error"] = runErr.Error()
	}
	return metadata
}

func normalizeBackend(raw string) string {
	backend := strings.ToLower(strings.TrimSpace(raw))
	if backend == "" {
		return backendOpenCode
	}
	return backend
}

func catalogBackendCapabilities(catalog codingagents.Catalog) map[string]backendCapabilities {
	capabilities := map[string]backendCapabilities{}
	for _, name := range catalog.Names() {
		profile, ok := catalog.CapabilityProfile(name)
		if !ok {
			continue
		}
		capabilities[name] = backendCapabilities{
			SupportsReview: profile.SupportsReview,
			SupportsStream: profile.SupportsStream,
		}
	}
	if len(capabilities) == 0 {
		return defaultBackendCapabilityMatrix()
	}
	return capabilities
}

func distributedExecutorCapabilitiesForBackend(catalog codingagents.Catalog, backend string, explicitRaw string, explicit bool) ([]distributed.Capability, error) {
	if explicit {
		return parseDistributedExecutorCapabilities(explicitRaw)
	}
	caps, ok := catalog.DistributedCapabilities(backend)
	if ok && len(caps) > 0 {
		return caps, nil
	}
	return []distributed.Capability{
		distributed.CapabilityImplement,
		distributed.CapabilityReview,
	}, nil
}

func normalizeDistributedRole(raw string) (string, error) {
	role := strings.ToLower(strings.TrimSpace(raw))
	if role == "" {
		return agentRoleLocal, nil
	}
	switch role {
	case agentRoleLocal, agentRoleMaster, agentRoleWorker:
		return role, nil
	}
	return "", fmt.Errorf("invalid distributed role %q (supported: %s, %s, %s)", role, agentRoleLocal, agentRoleMaster, agentRoleWorker)
}

func normalizeDistributedBusBackend(raw string) (string, error) {
	backend := strings.ToLower(strings.TrimSpace(raw))
	switch backend {
	case "", distributedBusRedis:
		return distributedBusRedis, nil
	case distributedBusNATS:
		return distributedBusNATS, nil
	default:
		return "", fmt.Errorf("invalid distributed bus backend %q (supported: %s, %s)", backend, distributedBusRedis, distributedBusNATS)
	}
}

func parseDistributedExecutorCapabilities(raw string) ([]distributed.Capability, error) {
	capabilityRaw := strings.TrimSpace(raw)
	if capabilityRaw == "" {
		capabilityRaw = "implement,review"
	}
	values := strings.Split(capabilityRaw, ",")
	seen := map[distributed.Capability]struct{}{}
	out := make([]distributed.Capability, 0, len(values))
	for _, value := range values {
		capability := strings.ToLower(strings.TrimSpace(string(distributed.Capability(value))))
		switch distributed.Capability(capability) {
		case distributed.CapabilityImplement, distributed.CapabilityReview, distributed.CapabilityRewriteTask, distributed.CapabilityLargerModel, distributed.CapabilityServiceProxy:
			c := distributed.Capability(capability)
			if _, ok := seen[c]; !ok {
				seen[c] = struct{}{}
				out = append(out, c)
			}
		default:
			return nil, fmt.Errorf("invalid distributed capability %q", value)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("at least one distributed capability is required")
	}
	return out, nil
}

type mirrorEventSink struct {
	base contracts.EventSink
	ch   chan contracts.Event
	wg   sync.WaitGroup
	one  sync.Once
}

func newMirrorEventSink(base contracts.EventSink, buffer int) *mirrorEventSink {
	if buffer <= 0 {
		buffer = 64
	}
	s := &mirrorEventSink{base: base, ch: make(chan contracts.Event, buffer)}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for event := range s.ch {
			_ = s.base.Emit(context.Background(), event)
		}
	}()
	return s
}

func (s *mirrorEventSink) Emit(_ context.Context, event contracts.Event) error {
	if s == nil || s.base == nil {
		return nil
	}
	select {
	case s.ch <- event:
	default:
	}
	return nil
}

func (s *mirrorEventSink) Close() {
	if s == nil {
		return
	}
	s.one.Do(func() {
		close(s.ch)
		s.wg.Wait()
	})
}

type localRunner struct{ dir string }

func (r localRunner) Run(args ...string) (string, error) {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = r.dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

type localGitRunner struct{ dir string }

func (r localGitRunner) Run(name string, args ...string) (string, error) {
	all := append([]string{name}, args...)
	cmd := exec.Command(all[0], all[1:]...)
	cmd.Dir = r.dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func defaultConfigRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "opencode-runner")
}

func defaultConfigDir() string {
	root := defaultConfigRoot()
	if root == "" {
		return ""
	}
	return filepath.Join(root, "opencode")
}

func resolveRunConfigCodingAgents(cfg *runConfig) error {
	if cfg == nil {
		return nil
	}
	if len(cfg.codingAgents.Names()) > 0 {
		return nil
	}
	catalog, err := loadCodingAgentsCatalog(cfg.repoRoot)
	if err != nil {
		return err
	}
	cfg.codingAgents = catalog
	return nil
}
