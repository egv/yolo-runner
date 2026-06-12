package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egv/yolo-runner/v2/internal/agent"
	"github.com/egv/yolo-runner/v2/internal/codingagents"
	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/engine"
	arcvcs "github.com/egv/yolo-runner/v2/internal/vcs/arc"
	gitvcs "github.com/egv/yolo-runner/v2/internal/vcs/git"
	"github.com/egv/yolo-runner/v2/internal/version"
)

const (
	backendOpenCode    = "opencode"
	backendOpenCodeACP = "opencode-acp"
	backendCodex       = "codex"
	backendCodexCLI    = "codex-cli"
	backendClaude      = "claude"
	backendKimi        = "kimi"
	backendGemini      = "gemini"
	agentModeStream    = "stream"
	agentModeUI        = "ui"
)

type runConfig struct {
	repoRoot             string
	rootID               string
	backend              string
	profile              string
	trackerType          string
	model                string
	qualityThreshold     int
	qualityGateTools     []string
	qcGateTools          []string
	allowLowQuality      bool
	maxTasks             int
	retryBudget          int
	concurrency          int
	dryRun               bool
	mode                 string
	stream               bool
	verboseStream        bool
	streamOutputInterval time.Duration
	streamOutputBuffer   int
	tddMode              bool
	runnerTimeout        time.Duration
	watchdogTimeout      time.Duration
	watchdogInterval     time.Duration
	eventsPath           string
	codingAgents         codingagents.Catalog
}

type trackerWatchConfig struct {
	repoRoot   string
	profile    string
	once       bool
	dryRun     bool
	stream     bool
	eventsPath string
	eventSink  contracts.EventSink
}

type arcReviewWatchCommandConfig struct {
	repoRoot   string
	profile    string
	once       bool
	dryRun     bool
	stream     bool
	eventsPath string
	eventSink  contracts.EventSink
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
var runTrackerWatch = defaultRunTrackerWatch
var runArcReviewWatch = defaultRunArcReviewWatch

func RunMain(args []string, run func(context.Context, runConfig) error) int {
	if version.IsVersionRequest(args) {
		version.Print(os.Stdout, "yolo-agent")
		return 0
	}

	if len(args) > 0 && args[0] == "config" {
		return runConfigCommand(args[1:])
	}
	if len(args) > 0 && args[0] == "tracker-watch" {
		return trackerWatchCommand(args[1:])
	}
	if len(args) > 0 && args[0] == "arc-review-watch" {
		return arcReviewWatchCommand(args[1:])
	}
	if len(args) > 0 && args[0] == "runner" {
		return runnerDaemonCommand(args[1:])
	}
	if len(args) > 0 && args[0] == arcPRReviewRunnerBinary {
		return arcPRReviewRunnerCommand(args[1:])
	}

	fs := flag.NewFlagSet("yolo-agent", flag.ContinueOnError)
	repo := fs.String("repo", ".", "Repository root")
	root := fs.String("root", "", "Root task ID")
	backend := fs.String("backend", "", "DEPRECATED: use --agent-backend (opencode, codex, codex-cli, claude, kimi, gemini)")
	agentBackend := fs.String("agent-backend", "", "Runner backend (opencode, codex, codex-cli, claude, kimi, gemini)")
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

	if *root == "" {
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
	if run == nil {
		run = defaultRun
	}

	if err := run(context.Background(), runConfig{
		repoRoot:             *repo,
		rootID:               *root,
		backend:              selectedBackend,
		profile:              selectedProfile,
		model:                selectedModel,
		maxTasks:             *max,
		retryBudget:          selectedRetryBudget,
		concurrency:          selectedConcurrency,
		dryRun:               *dryRun,
		stream:               selectedStream,
		mode:                 selectedMode,
		verboseStream:        *verboseStream,
		tddMode:              *tddMode,
		streamOutputInterval: *streamOutputInterval,
		streamOutputBuffer:   *streamOutputBuffer,
		qualityThreshold:     *qualityThreshold,
		qualityGateTools:     selectedQualityGateTools,
		qcGateTools:          selectedQCGateTools,
		allowLowQuality:      *allowLowQuality,
		runnerTimeout:        selectedRunnerTimeout,
		watchdogTimeout:      selectedWatchdogTimeout,
		watchdogInterval:     selectedWatchdogInterval,
		eventsPath:           *events,
		codingAgents:         codingAgents,
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

func trackerWatchCommand(args []string) int {
	fs := flag.NewFlagSet("yolo-agent tracker-watch", flag.ContinueOnError)
	repo := fs.String("repo", ".", "Repository root")
	profile := fs.String("profile", "", "Tracker profile name from .yolo-runner/config.yaml")
	once := fs.Bool("once", false, "Run one tracker watch iteration and exit")
	dryRun := fs.Bool("dry-run", false, "Dry run tracker watch without making changes")
	stream := fs.Bool("stream", false, "Emit NDJSON events to stdout for piping into yolo-tui")
	events := fs.String("events", "", "Path to JSONL events log")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "unexpected tracker-watch argument: %s\n", fs.Arg(0))
		return 1
	}
	handler := runTrackerWatch
	if handler == nil {
		handler = defaultRunTrackerWatch
	}
	if err := handler(context.Background(), trackerWatchConfig{
		repoRoot:   *repo,
		profile:    *profile,
		once:       *once,
		dryRun:     *dryRun,
		stream:     *stream,
		eventsPath: *events,
	}); err != nil {
		fmt.Fprintln(os.Stderr, agent.FormatActionableError(err))
		return 1
	}
	return 0
}

func arcReviewWatchCommand(args []string) int {
	fs := flag.NewFlagSet("yolo-agent arc-review-watch", flag.ContinueOnError)
	repo := fs.String("repo", ".", "Repository root")
	profile := fs.String("profile", "", "Tracker profile name from .yolo-runner/config.yaml")
	once := fs.Bool("once", false, "Run one arc review watch iteration and exit")
	dryRun := fs.Bool("dry-run", false, "Dry run arc review watch without making changes")
	stream := fs.Bool("stream", false, "Emit NDJSON events to stdout for piping into yolo-tui")
	events := fs.String("events", "", "Path to JSONL events log")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "unexpected arc-review-watch argument: %s\n", fs.Arg(0))
		return 1
	}
	handler := runArcReviewWatch
	if handler == nil {
		handler = defaultRunArcReviewWatch
	}
	if err := handler(context.Background(), arcReviewWatchCommandConfig{
		repoRoot:   *repo,
		profile:    *profile,
		once:       *once,
		dryRun:     *dryRun,
		stream:     *stream,
		eventsPath: *events,
	}); err != nil {
		fmt.Fprintln(os.Stderr, agent.FormatActionableError(err))
		return 1
	}
	return 0
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
	args := os.Args[1:]
	if filepath.Base(os.Args[0]) == arcPRReviewRunnerBinary {
		args = append([]string{arcPRReviewRunnerBinary}, args...)
	}
	os.Exit(RunMain(args, nil))
}

func defaultRun(ctx context.Context, cfg runConfig) error {
	if err := resolveRunConfigCodingAgents(&cfg); err != nil {
		return err
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
	vcsAdapter := gitvcs.NewVCSAdapter(localGitRunner{dir: cfg.repoRoot})
	runnerAdapter, err := buildAgentRunner(cfg.codingAgents, cfg.backend, cfg.model, cfg.runnerTimeout)
	if err != nil {
		return err
	}

	taskEngine := engine.NewTaskEngine()
	return runWithStorageComponents(ctx, cfg, storageBackend, taskEngine, runnerAdapter, vcsAdapter)
}

func buildRunnerAdapter(cfg runConfig) (contracts.AgentRunner, error) {
	return buildAgentRunner(cfg.codingAgents, cfg.backend, cfg.model, cfg.runnerTimeout)
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
	cloneManager, err := runCloneManager(cfg)
	if err != nil {
		return err
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
		CloneManager:         cloneManager,
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
	cloneManager, err := runCloneManager(cfg)
	if err != nil {
		return err
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
		CloneManager:         cloneManager,
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

func runCloneManager(cfg runConfig) (agent.CloneManager, error) {
	landingMode, err := resolveLandingMode(cfg.repoRoot)
	if err != nil {
		return nil, err
	}
	if landingMode == landingTypeArcPR {
		return nil, nil
	}
	return agent.NewGitCloneManager(filepath.Join(cfg.repoRoot, ".yolo-runner", "clones")), nil
}

func resolveLandingMode(repoRoot string) (string, error) {
	landingCfg, err := newTrackerConfigService().ResolveLandingConfig(repoRoot)
	if err != nil {
		return "", err
	}
	return landingCfg.Type, nil
}

func cloneScopedVCSFactory(cfg runConfig, vcs contracts.VCS) agent.VCSFactory {
	if _, ok := vcs.(*gitvcs.VCSAdapter); !ok {
		return nil
	}
	landingMode, err := resolveLandingMode(cfg.repoRoot)
	if err != nil {
		return nil
	}
	return func(repoRoot string) contracts.VCS {
		targetRoot := repoRoot
		if targetRoot == "" {
			targetRoot = cfg.repoRoot
		}
		if landingMode == landingTypeArcPR {
			return arcvcs.New(localGitRunner{dir: targetRoot})
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
