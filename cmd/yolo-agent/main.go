package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/egv/yolo-runner/v2/internal/agent"
	"github.com/egv/yolo-runner/v2/internal/codingagents"
	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/engine"
	"github.com/egv/yolo-runner/v2/internal/envpreset"
	arcvcs "github.com/egv/yolo-runner/v2/internal/vcs/arc"
	gitvcs "github.com/egv/yolo-runner/v2/internal/vcs/git"
	"github.com/egv/yolo-runner/v2/internal/version"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
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

	embeddedQueueRunnerPollInterval      = 50 * time.Millisecond
	embeddedQueueRunnerHeartbeatInterval = 5 * time.Second
	embeddedQueueRunnerLeaseTTL          = 10 * time.Minute
	embeddedQueueRunnerLiveAfter         = 15 * time.Second
)

type runConfig struct {
	repoRoot               string
	rootID                 string
	queuePath              string
	backend                string
	profile                string
	trackerType            string
	model                  string
	qualityThreshold       int
	qualityGateTools       []string
	qcGateTools            []string
	allowLowQuality        bool
	maxTasks               int
	retryBudget            int
	concurrency            int
	dryRun                 bool
	mode                   string
	stream                 bool
	verboseStream          bool
	streamOutputInterval   time.Duration
	streamOutputBuffer     int
	embeddedRunnerPool     string
	embeddedRunnerReplicas int
	tddMode                bool
	runnerTimeout          time.Duration
	watchdogTimeout        time.Duration
	watchdogInterval       time.Duration
	eventsPath             string
	codingAgents           codingagents.Catalog
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
var launchYoloBoard = func(queuePath string) (io.WriteCloser, func() error, error) {
	cmd := exec.Command("yolo-board", "--queue", queuePath, "--events-stdin")
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

// embeddedQueueMaterializer materializes the embedded queue runner's workspace.
// Defaults to real isolated materialization; tests override it to inject a fake
// isolated workspace without standing up a git remote.
var embeddedQueueMaterializer = envpreset.MaterializeWorkspace
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
	if len(args) > 0 && args[0] == "watch" {
		return watchCommand(args[1:])
	}
	if len(args) > 0 && args[0] == "source" {
		return sourceCommand(args[1:])
	}
	if len(args) > 0 && args[0] == "runner" {
		return runnerDaemonCommand(args[1:])
	}
	if len(args) > 0 && args[0] == "events" {
		return eventsCommand(args[1:])
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
	verboseStream := fs.Bool("verbose-stream", false, "Emit every agent_text event without coalescing")
	tddMode := fs.Bool("tdd", false, "Enable strict test-first Red/Green/Refactor workflow")
	streamOutputInterval := fs.Duration("stream-output-interval", 150*time.Millisecond, "Minimum interval between emitted agent_text events when not verbose")
	streamOutputBuffer := fs.Int("stream-output-buffer", 64, "Maximum coalesced agent_text events retained before drop")
	mode := fs.String("mode", "", "Output mode for runner events (stream, ui)")
	runnerTimeout := fs.Duration("runner-timeout", 0, "Per runner execution timeout")
	watchdogTimeout := fs.Duration("watchdog-timeout", 10*time.Minute, "No-output watchdog timeout for each runner execution")
	watchdogInterval := fs.Duration("watchdog-interval", 5*time.Second, "Polling interval used by the no-output watchdog")
	retryBudget := fs.Int("retry-budget", 5, "Maximum retry attempts per task for remediation loop")
	events := fs.String("events", "", "Path to JSONL events log")
	queue := fs.String("queue", "", "Path to SQLite work queue database; when set, run submits implement work to queue runners")
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
		queuePath:            *queue,
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
	var signalFileSink contracts.EventSink
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
			signalFileSink = fileSink
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
	terminalEvents := newRunTerminalEventEmitter(cfg, eventSink, signalFileSink)
	defer terminalEvents.recoverPanic()
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	stopSignalHandler := terminalEvents.installSignalHandler(cancelRun)
	defer stopSignalHandler()
	runner = terminalEvents.wrapRunner(runner)
	cloneManager, err := runCloneManager(cfg)
	if err != nil {
		return err
	}
	dispatcher, closeDispatcher, err := runWorkDispatcher(cfg)
	if err != nil {
		return err
	}
	if closeDispatcher != nil {
		closers = append(closers, func() {
			_ = closeDispatcher()
		})
	}
	vcsFactory := cloneScopedVCSFactory(cfg, vcs)
	embeddedRunner, err := maybeStartEmbeddedQueueRunner(runCtx, cfg, runner, eventSink)
	if err != nil {
		return err
	}
	if embeddedRunner != nil {
		go embeddedRunner.cancelRunOnError(cancelRun)
	}
	loop := agent.NewLoop(taskManager, runner, eventSink, agent.LoopOptions{
		ParentID:             cfg.rootID,
		MaxRetries:           cfg.retryBudget,
		MaxTasks:             cfg.maxTasks,
		Concurrency:          cfg.concurrency,
		QualityGateThreshold: cfg.qualityThreshold,
		QualityGateTools:     cfg.qualityGateTools,
		QCGateTools:          cfg.qcGateTools,
		AllowLowQuality:      cfg.allowLowQuality,
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
		Dispatcher:           dispatcher,
	})
	if eventSink != nil {
		_ = eventSink.Emit(runCtx, contracts.Event{
			Type:      contracts.EventTypeRunStarted,
			TaskID:    cfg.rootID,
			TaskTitle: "run",
			Metadata:  buildRunStartedMetadata(cfg),
			Timestamp: time.Now().UTC(),
		})
	}

	summary, err := loop.Run(runCtx)
	if embeddedRunner != nil {
		cancelRun()
		if stopErr := embeddedRunner.Stop(); stopErr != nil && (err == nil || errors.Is(err, context.Canceled)) {
			err = stopErr
		}
	}
	terminalEvents.emitFinished(summary, err)
	return err
}

func runWithStorageComponents(ctx context.Context, cfg runConfig, storage contracts.StorageBackend, taskEngine contracts.TaskEngine, runner contracts.AgentRunner, vcs contracts.VCS) error {
	sinks := []contracts.EventSink{}
	closers := []func(){}
	var signalFileSink contracts.EventSink
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
			signalFileSink = fileSink
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
	terminalEvents := newRunTerminalEventEmitter(cfg, eventSink, signalFileSink)
	defer terminalEvents.recoverPanic()
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	stopSignalHandler := terminalEvents.installSignalHandler(cancelRun)
	defer stopSignalHandler()
	runner = terminalEvents.wrapRunner(runner)
	cloneManager, err := runCloneManager(cfg)
	if err != nil {
		return err
	}
	dispatcher, closeDispatcher, err := runWorkDispatcher(cfg)
	if err != nil {
		return err
	}
	if closeDispatcher != nil {
		closers = append(closers, func() {
			_ = closeDispatcher()
		})
	}
	vcsFactory := cloneScopedVCSFactory(cfg, vcs)
	embeddedRunner, err := maybeStartEmbeddedQueueRunner(runCtx, cfg, runner, eventSink)
	if err != nil {
		return err
	}
	if embeddedRunner != nil {
		go embeddedRunner.cancelRunOnError(cancelRun)
	}
	loop := agent.NewLoopWithTaskEngine(storage, taskEngine, runner, eventSink, agent.LoopOptions{
		ParentID:             cfg.rootID,
		MaxRetries:           cfg.retryBudget,
		MaxTasks:             cfg.maxTasks,
		Concurrency:          cfg.concurrency,
		QualityGateThreshold: cfg.qualityThreshold,
		QualityGateTools:     cfg.qualityGateTools,
		QCGateTools:          cfg.qcGateTools,
		AllowLowQuality:      cfg.allowLowQuality,
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
		Dispatcher:           dispatcher,
	})
	if eventSink != nil {
		_ = eventSink.Emit(runCtx, contracts.Event{
			Type:      contracts.EventTypeRunStarted,
			TaskID:    cfg.rootID,
			TaskTitle: "run",
			Metadata:  buildRunStartedMetadata(cfg),
			Timestamp: time.Now().UTC(),
		})
	}

	summary, err := loop.Run(runCtx)
	if embeddedRunner != nil {
		cancelRun()
		if stopErr := embeddedRunner.Stop(); stopErr != nil && (err == nil || errors.Is(err, context.Canceled)) {
			err = stopErr
		}
	}
	terminalEvents.emitFinished(summary, err)
	return err
}

type runTerminalEventEmitter struct {
	cfg            runConfig
	sink           contracts.EventSink
	signalFileSink contracts.EventSink
	once           sync.Once
}

// runTerminalEventEmitter owns the one terminal run_finished event emitted by a
// run. It covers ordinary completion, recoverable panics at the run/runner
// boundary, and SIGINT/SIGTERM. Runtime-fatal errors thrown by the Go runtime
// cannot be recovered in-process; supervisors must synthesize this terminal
// event when a child exits non-zero without writing one.
func newRunTerminalEventEmitter(cfg runConfig, sink contracts.EventSink, signalFileSink contracts.EventSink) *runTerminalEventEmitter {
	return &runTerminalEventEmitter{cfg: cfg, sink: sink, signalFileSink: signalFileSink}
}

func (e *runTerminalEventEmitter) emitFinished(summary contracts.LoopSummary, runErr error) {
	if e == nil || e.sink == nil {
		return
	}
	e.once.Do(func() {
		_ = e.sink.Emit(context.Background(), contracts.Event{
			Type:      contracts.EventTypeRunFinished,
			TaskID:    e.cfg.rootID,
			TaskTitle: "run",
			Metadata:  buildRunFinishedMetadata(e.cfg, summary, runErr),
			Timestamp: time.Now().UTC(),
		})
	})
}

func (e *runTerminalEventEmitter) emitFatal(reason string, alsoWriteSignalFile bool) {
	if e == nil || e.sink == nil {
		return
	}
	e.once.Do(func() {
		event := contracts.Event{
			Type:      contracts.EventTypeRunFinished,
			TaskID:    e.cfg.rootID,
			TaskTitle: "run",
			Metadata:  buildFatalRunFinishedMetadata(e.cfg, reason),
			Timestamp: time.Now().UTC(),
		}
		_ = e.sink.Emit(context.Background(), event)
		if alsoWriteSignalFile && e.signalFileSink != nil {
			_ = e.signalFileSink.Emit(context.Background(), event)
		}
	})
}

func (e *runTerminalEventEmitter) recoverPanic() {
	if recovered := recover(); recovered != nil {
		e.emitFatal(fmt.Sprintf("panic: %v", recovered), false)
		panic(recovered)
	}
}

func (e *runTerminalEventEmitter) wrapRunner(runner contracts.AgentRunner) contracts.AgentRunner {
	if e == nil || runner == nil {
		return runner
	}
	return terminalPanicRecoveringRunner{inner: runner, emitFatal: e.emitFatal}
}

func (e *runTerminalEventEmitter) installSignalHandler(cancel context.CancelFunc) func() {
	if e == nil || e.sink == nil {
		return func() {}
	}
	signals := make(chan os.Signal, 1)
	done := make(chan struct{})
	var stopOnce sync.Once
	stop := func() {
		stopOnce.Do(func() {
			signal.Stop(signals)
			close(done)
		})
	}
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case sig := <-signals:
			if cancel != nil {
				cancel()
			}
			e.emitFatal(fmt.Sprintf("signal: %s", sig), true)
			stop()
			os.Exit(1)
		case <-done:
		}
	}()
	return stop
}

func buildFatalRunFinishedMetadata(cfg runConfig, reason string) map[string]string {
	metadata := buildRunFinishedMetadata(cfg, contracts.LoopSummary{}, errors.New(reason))
	metadata["reason"] = reason
	return metadata
}

type terminalPanicRecoveringRunner struct {
	inner     contracts.AgentRunner
	emitFatal func(reason string, alsoWriteSignalFile bool)
}

func (r terminalPanicRecoveringRunner) Run(ctx context.Context, request contracts.RunnerRequest) (result contracts.RunnerResult, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			reason := fmt.Sprintf("panic: %v", recovered)
			if r.emitFatal != nil {
				r.emitFatal(reason, false)
			}
			result = contracts.RunnerResult{Status: contracts.RunnerResultFailed, Reason: reason}
			err = errors.New(reason)
		}
	}()
	return r.inner.Run(ctx, request)
}

func runWorkDispatcher(cfg runConfig) (agent.WorkDispatcher, func() error, error) {
	if strings.TrimSpace(cfg.queuePath) == "" {
		return nil, nil, nil
	}
	dispatcher, err := agent.NewQueueDispatcher(cfg.queuePath, agent.QueueDispatcherOptions{
		Preset: queuePresetForRun(cfg),
	})
	if err != nil {
		return nil, nil, err
	}
	return dispatcher, dispatcher.Close, nil
}

type embeddedQueueRunnerHandle struct {
	cancel  context.CancelFunc
	done    chan error
	wait    sync.Once
	waitErr error
}

func maybeStartEmbeddedQueueRunner(ctx context.Context, cfg runConfig, runner contracts.AgentRunner, events contracts.EventSink) (*embeddedQueueRunnerHandle, error) {
	if strings.TrimSpace(cfg.queuePath) == "" {
		return nil, nil
	}
	preset := queuePresetForRun(cfg)
	if preset == "" {
		return nil, nil
	}
	pool := strings.TrimSpace(cfg.embeddedRunnerPool)

	live, err := queueHasLiveRunnerForPresetInPool(cfg.queuePath, preset, pool, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	if live {
		return nil, nil
	}
	if pool == "" {
		pool = "global"
	}

	embeddedPreset, err := synthesizeEmbeddedQueuePreset(cfg)
	if err != nil {
		return nil, err
	}

	store, err := workqueue.Open(cfg.queuePath)
	if err != nil {
		return nil, err
	}
	runners, err := openRunnerRegistry(cfg.queuePath)
	if err != nil {
		_ = store.Close()
		return nil, err
	}

	replicas := cfg.embeddedRunnerReplicas
	if replicas <= 0 {
		replicas = cfg.concurrency
	}
	if replicas <= 0 {
		replicas = 1
	}

	baseCfg := runnerDaemonCommandConfig{
		presets:           []string{preset},
		capacity:          1,
		pollInterval:      embeddedQueueRunnerPollInterval,
		heartbeatInterval: embeddedQueueRunnerHeartbeatInterval,
		leaseTTL:          embeddedQueueRunnerLeaseTTL,
	}

	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	workerErrs := make(chan error, replicas)

	for replica := 0; replica < replicas; replica++ {
		daemon, runnerID, err := buildEmbeddedQueueRunnerWorker(cfg, runner, events, store, runners, baseCfg, preset, embeddedPreset, pool, replica)
		if err != nil {
			_ = runners.Close()
			_ = store.Close()
			return nil, err
		}
		go func(daemon runnerDaemon, runnerID string) {
			runErr := daemon.Run(runCtx)
			if err := unregisterQueueRunner(runners, runnerID); err != nil && (runErr == nil || errors.Is(runErr, context.Canceled)) {
				runErr = err
			}
			workerErrs <- runErr
		}(daemon, runnerID)
	}

	go func() {
		var runErr error
		for i := 0; i < replicas; i++ {
			workerErr := <-workerErrs
			if workerErr != nil && !errors.Is(workerErr, context.Canceled) {
				if runErr == nil || errors.Is(runErr, context.Canceled) {
					runErr = workerErr
				}
				cancel()
				continue
			}
			if runErr == nil {
				runErr = workerErr
			}
		}
		if err := runners.Close(); err != nil && (runErr == nil || errors.Is(runErr, context.Canceled)) {
			runErr = err
		}
		if err := store.Close(); err != nil && (runErr == nil || errors.Is(runErr, context.Canceled)) {
			runErr = err
		}
		done <- runErr
	}()

	return &embeddedQueueRunnerHandle{cancel: cancel, done: done}, nil
}

func buildEmbeddedQueueRunnerWorker(cfg runConfig, runner contracts.AgentRunner, events contracts.EventSink, store *workqueue.Store, runners *runnerRegistry, baseCfg runnerDaemonCommandConfig, preset string, embeddedPreset envpreset.Preset, pool string, replica int) (runnerDaemon, string, error) {
	if strings.TrimSpace(preset) == "" {
		return runnerDaemon{}, "", fmt.Errorf("embedded queue runner preset is required")
	}

	daemonCfg := baseCfg
	daemonCfg.runnerID = embeddedQueueRunnerID(pool, preset, replica)
	if err := runners.Register(daemonCfg.runnerID, daemonCfg.presets, daemonCfg.capacity); err != nil {
		return runnerDaemon{}, "", err
	}

	daemon, err := newRunnerDaemon(daemonCfg, store, runners, runnerDaemonBuildOptions{
		handlers: handlersFromWorkerRunConfig(cfg, runner, events),
		environmentPresets: map[string]envpreset.Preset{
			preset: embeddedPreset,
		},
		materializer: embeddedQueueMaterializer,
		eventSink:    embeddedQueueRunnerEventSink(events),
	})
	if err != nil {
		return runnerDaemon{}, "", err
	}

	return daemon, daemonCfg.runnerID, nil
}

func handlersFromWorkerRunConfig(cfg runConfig, runner contracts.AgentRunner, events contracts.EventSink) runnerKindRegistry {
	return runnerKindRegistry{
		workitem.KindImplement: newRunnerImplementKindHandler(embeddedQueueRunnerExecutorResolver(cfg, runner, events)),
		workitem.KindFinalize:  newRunnerFinalizeKindHandler(),
	}
}

func (h *embeddedQueueRunnerHandle) Stop() error {
	if h == nil {
		return nil
	}
	h.cancel()
	err := h.Wait()
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func (h *embeddedQueueRunnerHandle) Wait() error {
	if h == nil {
		return nil
	}
	h.wait.Do(func() {
		h.waitErr = <-h.done
	})
	return h.waitErr
}

func (h *embeddedQueueRunnerHandle) cancelRunOnError(cancel context.CancelFunc) {
	if h == nil || cancel == nil {
		return
	}
	if err := h.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		cancel()
	}
}

func queueHasLiveRunnerForPreset(queuePath string, preset string, now time.Time) (bool, error) {
	return queueHasLiveRunnerForPresetInPool(queuePath, preset, "", now)
}

func queueHasLiveRunnerForPresetInPool(queuePath string, preset string, pool string, now time.Time) (bool, error) {
	runners, err := openRunnerRegistry(queuePath)
	if err != nil {
		return false, err
	}
	defer runners.Close()

	rows, err := runners.db.Query(`
SELECT id, presets, capacity, heartbeat_at
FROM runners`)
	if err != nil {
		return false, fmt.Errorf("list queue runners: %w", err)
	}
	defer rows.Close()

	liveAfter := now.Add(-embeddedQueueRunnerLiveAfter)
	for rows.Next() {
		var rawPresets string
		var capacity int
		var runnerID string
		var heartbeatAt string
		if err := rows.Scan(&runnerID, &rawPresets, &capacity, &heartbeatAt); err != nil {
			return false, fmt.Errorf("scan queue runner: %w", err)
		}
		pool = strings.TrimSpace(pool)
		if pool != "" && !embeddedQueueRunnerPoolMatches(runnerID, pool) {
			continue
		}
		if capacity <= 0 || !runnerPresetMatches(rawPresets, preset) {
			continue
		}
		heartbeat, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(heartbeatAt))
		if err != nil {
			return false, fmt.Errorf("parse queue runner heartbeat %q: %w", heartbeatAt, err)
		}
		if !heartbeat.Before(liveAfter) {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("read queue runners: %w", err)
	}
	return false, nil
}

func embeddedQueueRunnerPoolMatches(runnerID string, pool string) bool {
	pool = strings.TrimSpace(pool)
	if pool == "" {
		return true
	}

	runnerPool, ok := embeddedQueueRunnerPoolFromID(runnerID)
	if !ok {
		return false
	}
	return runnerPool == pool
}

func embeddedQueueRunnerPoolFromID(runnerID string) (string, bool) {
	runnerID = strings.TrimSpace(runnerID)
	if !strings.HasPrefix(runnerID, "embedded-pool-") {
		return "", false
	}
	pool, _, ok := strings.Cut(strings.TrimPrefix(runnerID, "embedded-pool-"), "-pid-")
	if !ok || strings.TrimSpace(pool) == "" {
		return "", false
	}
	return strings.TrimSpace(pool), true
}

func runnerPresetMatches(rawPresets string, preset string) bool {
	preset = strings.TrimSpace(preset)
	for _, candidate := range parseRunnerPresets(rawPresets) {
		if candidate == preset {
			return true
		}
	}
	return false
}

func unregisterQueueRunner(runners *runnerRegistry, runnerID string) error {
	if runners == nil || runners.db == nil {
		return nil
	}
	runnerID = strings.TrimSpace(runnerID)
	if runnerID == "" {
		return nil
	}
	if _, err := runners.db.Exec(`DELETE FROM runners WHERE id = ?`, runnerID); err != nil {
		return fmt.Errorf("unregister embedded queue runner %q: %w", runnerID, err)
	}
	return nil
}

func embeddedQueueRunnerID(pool string, preset string, replica int) string {
	return fmt.Sprintf("embedded-pool-%s-pid-%d-replica-%d-preset-%s",
		safeRunnerIDForPath(pool),
		os.Getpid(),
		replica,
		safeRunnerIDForPath(preset))
}

func embeddedQueueRunnerExecutorResolver(cfg runConfig, runner contracts.AgentRunner, events contracts.EventSink) runnerImplementExecutorResolver {
	return func(context.Context, workitem.Item, envpreset.Workspace) (runnerImplementExecutor, error) {
		return runnerImplementExecutor{
			Runner: runner,
			Agent: envpreset.ResolvedAgent{
				Backend:          cfg.backend,
				Model:            cfg.model,
				RunnerTimeout:    cfg.runnerTimeout,
				WatchdogTimeout:  cfg.watchdogTimeout,
				WatchdogInterval: cfg.watchdogInterval,
			},
			Landing: embeddedQueueRunnerLanding(cfg),
			Events:  events,
		}, nil
	}
}

func embeddedQueueRunnerLanding(cfg runConfig) envpreset.LandingType {
	landingMode, err := resolveLandingMode(cfg.repoRoot)
	if err != nil {
		return envpreset.LandingTypeGitMerge
	}
	if landingMode == landingTypeArcPR {
		return envpreset.LandingTypeArcPR
	}
	return envpreset.LandingTypeGitMerge
}

// synthesizeEmbeddedQueuePreset builds the single-command embedded runner's
// environment preset from the run config. Git repos clone per item from the
// local checkout (CloneForTask rewrites the remote to the source origin, so
// landing pushes to the real remote). Arc repos need an explicit mount/subpath
// that the run config does not carry, so they must use a standalone runner with
// an environments.yaml arc-shared preset.
func synthesizeEmbeddedQueuePreset(cfg runConfig) (envpreset.Preset, error) {
	if embeddedQueueRunnerLanding(cfg) == envpreset.LandingTypeArcPR {
		return envpreset.Preset{}, fmt.Errorf(
			"embedded queue runner does not support arc repos; start a standalone "+
				"runner (yolo-agent runner --queue %s --presets %s) with an arc-shared "+
				"preset in ~/.yolo-runner/environments.yaml",
			cfg.queuePath, queuePresetForRun(cfg))
	}
	return envpreset.Preset{
		Workspace: envpreset.Workspace{
			Strategy:   envpreset.WorkspaceStrategyGitClone,
			Origin:     cfg.repoRoot,
			BaseBranch: "main",
		},
		Landing: envpreset.Landing{Type: envpreset.LandingTypeGitMerge},
	}, nil
}

type embeddedQueueDiscardEventSink struct{}

func embeddedQueueRunnerEventSink(sink contracts.EventSink) contracts.EventSink {
	if sink != nil {
		return sink
	}
	return embeddedQueueDiscardEventSink{}
}

func (embeddedQueueDiscardEventSink) Emit(context.Context, contracts.Event) error {
	return nil
}

func queuePresetForRun(cfg runConfig) string {
	if profile := strings.TrimSpace(cfg.profile); profile != "" {
		return profile
	}
	repoRoot := strings.TrimSpace(cfg.repoRoot)
	if repoRoot == "" {
		repoRoot = "."
	}
	if abs, err := filepath.Abs(repoRoot); err == nil {
		repoRoot = abs
	}
	base := strings.TrimSpace(filepath.Base(repoRoot))
	if base == "." || base == string(filepath.Separator) {
		return ""
	}
	return base
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
	case s.ch <- event.WithClonedMetadata():
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

func defaultRunnerEnvironmentsPath() string {
	return defaultRunnerDaemonEnvironmentsPath
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
