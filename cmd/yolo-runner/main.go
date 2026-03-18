package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/egv/yolo-runner/v2/internal/beads"
	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/distributed"
	"github.com/egv/yolo-runner/v2/internal/exec"
	"github.com/egv/yolo-runner/v2/internal/logging"
	"github.com/egv/yolo-runner/v2/internal/opencode"
	"github.com/egv/yolo-runner/v2/internal/prompt"
	"github.com/egv/yolo-runner/v2/internal/runner"
	"github.com/egv/yolo-runner/v2/internal/tk"
	"github.com/egv/yolo-runner/v2/internal/ui/tui"
	gitadapter "github.com/egv/yolo-runner/v2/internal/vcs/git"
	"github.com/egv/yolo-runner/v2/internal/version"
	"gopkg.in/yaml.v3"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"
)

type runOnceFunc func(opts runner.RunOnceOptions, deps runner.RunOnceDeps) (string, error)

type exitFunc func(code int)

type beadsRunner interface {
	Run(args ...string) (string, error)
}

type gitRunner interface {
	Run(name string, args ...string) (string, error)
}

type openCodeRunner interface {
	Start(args []string, env map[string]string, stdoutPath string) (opencode.Process, error)
}

// isCommandAvailable checks if a command is available in PATH
func isCommandAvailable(name string) bool {
	_, err := osexec.LookPath(name)
	return err == nil
}

type tuiProgram interface {
	Start() error
	Send(event runner.Event)
	SendInput(msg tea.Msg)
	Quit()
}

type tuiEmitter struct {
	program tuiProgram
}

type runnerUIDispatcher struct {
	writer *runnerUIEventWriter
}

func (t tuiEmitter) Emit(event runner.Event) {
	if t.program == nil {
		return
	}
	go t.program.Send(event)
}

func (d runnerUIDispatcher) Emit(event runner.Event) {
	if d.writer == nil {
		return
	}
	d.writer.Emit(event)
}

type bubbleTUIProgram struct {
	program *tea.Program
}

type runnerUIEventWriter struct {
	mu     sync.Mutex
	writer io.WriteCloser
	buffer strings.Builder
}

type runnerConfig struct {
	Agent runnerConfigAgent `yaml:"agent"`
}

type runnerConfigAgent struct {
	Mode string `yaml:"mode"`
}

const (
	runnerRoleLocal           = "local"
	runnerRoleWorker          = "executor"
	runnerModeUI              = "ui"
	runnerModeHeadless        = "headless"
	runnerConfigPath          = ".yolo-runner/config.yaml"
	runnerDistributedBusRedis = "redis"
	runnerDistributedBusNATS  = "nats"
)

func (b bubbleTUIProgram) Start() error {
	if b.program == nil {
		return nil
	}
	return b.program.Start()
}

func (b bubbleTUIProgram) Send(event runner.Event) {
	if b.program == nil {
		return
	}
	b.program.Send(event)
}

func (b bubbleTUIProgram) SendInput(msg tea.Msg) {
	if b.program == nil {
		return
	}
	b.program.Send(msg)
}

func (b bubbleTUIProgram) Quit() {
	if b.program == nil {
		return
	}
	b.program.Quit()
}

var launchYoloTUI = func() (io.WriteCloser, func() error, error) {
	cmd := osexec.Command("yolo-tui", "--events-stdin")
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

var newDistributedBus = func(backend string, address string, opts distributed.BusBackendOptions) (distributed.Bus, error) {
	switch backend {
	case runnerDistributedBusRedis:
		return distributed.NewRedisBus(address, opts)
	case runnerDistributedBusNATS:
		return distributed.NewNATSBus(address, opts)
	default:
		return nil, fmt.Errorf("unsupported distributed bus backend %q", backend)
	}
}

var isTerminal = func(writer io.Writer) bool {
	if file, ok := writer.(*os.File); ok {
		return term.IsTerminal(int(file.Fd()))
	}
	return false
}

var newTUIProgram = func(model tea.Model, stdout io.Writer, input io.Reader) tuiProgram {
	if input == nil {
		input = os.Stdin
	}
	program := tea.NewProgram(model, tea.WithInput(input), tea.WithOutput(stdout))
	return bubbleTUIProgram{program: program}
}

type adapterRunner struct {
	runner *exec.CommandRunner
}

func (a adapterRunner) Run(args ...string) (string, error) {
	if a.runner == nil {
		return runCommand(args...)
	}
	return a.runner.Run(args...)
}

func normalizeRunnerMode(raw string, field string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" {
		return "", nil
	}
	switch value {
	case runnerModeUI, runnerModeHeadless:
		return value, nil
	}
	return "", fmt.Errorf("%s in %s must be one of: %s, %s", field, runnerConfigPath, runnerModeUI, runnerModeHeadless)
}

func resolveRunnerMode(repoRoot string, modeFlag string, headlessFlag bool) (string, error) {
	mode, err := normalizeRunnerMode(modeFlag, "mode")
	if err != nil {
		return "", err
	}
	if mode != "" {
		return mode, nil
	}
	if headlessFlag {
		return runnerModeHeadless, nil
	}

	configMode, err := resolveRunnerModeFromConfig(repoRoot)
	if err != nil {
		return "", err
	}
	return configMode, nil
}

func resolveRunnerModeFromConfig(repoRoot string) (string, error) {
	configPath := filepath.Join(repoRoot, runnerConfigPath)
	content, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("cannot read config file at %s: %w", runnerConfigPath, err)
	}

	var config runnerConfig
	decoder := yaml.NewDecoder(strings.NewReader(string(content)))
	if err := decoder.Decode(&config); err != nil {
		return "", fmt.Errorf("cannot parse config file at %s: %w", runnerConfigPath, err)
	}
	return normalizeRunnerMode(config.Agent.Mode, "agent.mode")
}

func (w *runnerUIEventWriter) Emit(event runner.Event) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()

	message := strings.TrimSpace(event.RunnerEventMessage())
	if message == "" {
		message = strings.TrimSpace(event.RunnerEventThought())
	}
	if title := strings.TrimSpace(event.RunnerEventTitle()); title != "" {
		if message == "" {
			message = title
		} else {
			message = title + " " + message
		}
	}
	if eventType := event.RunnerEventType(); eventType != "" {
		if message == "" {
			message = eventType
		} else {
			message = eventType + ": " + message
		}
	}
	if phase := strings.TrimSpace(event.Phase); phase != "" {
		if message == "" {
			message = phase
		} else {
			message = message + " (" + phase + ")"
		}
	}
	w.emitLine(message)
}

func newRunnerUIEventWriter(w io.WriteCloser) *runnerUIEventWriter {
	return &runnerUIEventWriter{writer: w}
}

func (w *runnerUIEventWriter) Write(p []byte) (int, error) {
	if w == nil {
		return len(p), nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buffer.Write(p)
	buffered := normalizeLineBreaks(w.buffer.String())
	lines := strings.Split(buffered, "\n")
	for i := 0; i < len(lines)-1; i++ {
		w.emitLine(strings.TrimRight(lines[i], "\r"))
	}
	remaining := lines[len(lines)-1]
	w.buffer.Reset()
	if remaining != "" {
		w.buffer.WriteString(remaining)
	}
	return len(p), nil
}

func (w *runnerUIEventWriter) Flush() {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	remaining := normalizeLineBreaks(w.buffer.String())
	w.buffer.Reset()
	if remaining == "" {
		return
	}
	lines := strings.Split(remaining, "\n")
	for i, line := range lines {
		if i == len(lines)-1 && line == "" {
			continue
		}
		w.emitLine(strings.TrimRight(line, "\r"))
	}
}

func (w *runnerUIEventWriter) Close() error {
	if w == nil {
		return nil
	}
	w.Flush()
	if w.writer == nil {
		return nil
	}
	return w.writer.Close()
}

func (w *runnerUIEventWriter) emitLine(line string) {
	if w == nil || strings.TrimSpace(line) == "" {
		return
	}
	if w.writer == nil {
		return
	}
	event := contracts.Event{Type: contracts.EventTypeRunnerOutput, Message: line, Timestamp: time.Now().UTC()}
	payload, err := contracts.MarshalEventJSONL(event)
	if err != nil {
		return
	}
	_, _ = w.writer.Write([]byte(payload))
}

type adapterGitRunner struct {
	runner *gitadapter.GitCommandAdapter
}

func (a adapterGitRunner) Run(name string, args ...string) (string, error) {
	if a.runner == nil {
		return runCommand(append([]string{name}, args...)...)
	}
	return a.runner.Run(name, args...)
}

type openCodeAdapter struct {
	runner    openCodeRunner
	acpClient opencode.ACPClient
}

func (o openCodeAdapter) Run(issueID string, repoRoot string, promptText string, model string, configRoot string, configDir string, logPath string) error {
	return opencode.RunWithACP(context.Background(), issueID, repoRoot, promptText, model, configRoot, configDir, logPath, o.runner, o.acpClient)
}

func (o openCodeAdapter) RunWithContext(ctx context.Context, issueID string, repoRoot string, promptText string, model string, configRoot string, configDir string, logPath string) error {
	return opencode.RunWithACP(ctx, issueID, repoRoot, promptText, model, configRoot, configDir, logPath, o.runner, o.acpClient)
}

func RunOnceMain(args []string, runOnce runOnceFunc, exit exitFunc, stdout io.Writer, stderr io.Writer, beadsRunner beadsRunner, gitRunner gitRunner) int {
	if version.IsVersionRequest(args) {
		version.Print(stdout, "yolo-runner")
		if exit != nil {
			exit(0)
		}
		return 0
	}

	if stderr != nil {
		fmt.Fprintln(stderr, compatibilityNotice())
	}
	if len(args) > 0 && args[0] == "update" {
		return runUpdate(args[1:], stdout, stderr, nil)
	}
	if len(args) > 0 && args[0] == "init" {
		return InitMain(args[1:], exit, stderr)
	}

	fs := flag.NewFlagSet("yolo-runner", flag.ContinueOnError)
	fs.SetOutput(stderr)

	repoRoot := fs.String("repo", ".", "Repository root path")
	rootID := fs.String("root", "", "Root bead/epic ID")
	model := fs.String("model", "", "OpenCode model")
	dryRun := fs.Bool("dry-run", false, "Print task and prompt without executing")
	headless := fs.Bool("headless", false, "Force plain output without TUI")
	mode := fs.String("mode", "", "Output mode for runner events (ui, headless)")
	configRoot := fs.String("config-root", "", "OpenCode config root")
	configDir := fs.String("config-dir", "", "OpenCode config dir")
	role := fs.String("role", "", "Distributed execution role: local, executor")
	distributedBusBackend := fs.String("distributed-bus-backend", "", "Distributed bus backend (redis, nats)")
	distributedBusAddress := fs.String("distributed-bus-address", "", "Distributed bus address")
	distributedBusPrefix := fs.String("distributed-bus-prefix", "", "Distributed bus subject prefix")
	distributedExecutorID := fs.String("distributed-executor-id", "", "Distributed executor id (executor role)")
	distributedExecutorCapabilities := fs.String("distributed-executor-capabilities", "implement,review", "Comma-separated capabilities to advertise in executor role")
	distributedHeartbeatInterval := fs.Duration("distributed-heartbeat-interval", 5*time.Second, "Heartbeat interval in executor role")
	distributedRequestTimeout := fs.Duration("distributed-request-timeout", 30*time.Second, "Request timeout for task dispatch in distributed roles")

	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(stderr, err)
		if exit != nil {
			exit(1)
		}
		return 1
	}

	if runOnce == nil {
		runOnce = runner.RunOnce
	}

	selectedRole, err := normalizeDistributedRoleForRunner(*role, os.Getenv("YOLO_DISTRIBUTED_ROLE"))
	if err != nil {
		fmt.Fprintln(stderr, err)
		if exit != nil {
			exit(1)
		}
		return 1
	}
	selectedDistributedBusConfig, err := resolveRunnerDistributedBusConfig(
		*repoRoot,
		*distributedBusBackend,
		*distributedBusAddress,
		*distributedBusPrefix,
		os.Getenv,
	)
	if err != nil {
		fmt.Fprintln(stderr, err)
		if exit != nil {
			exit(1)
		}
		return 1
	}
	selectedDistributedExecutorCapabilities, err := parseDistributedExecutorCapabilities(*distributedExecutorCapabilities)
	if err != nil {
		fmt.Fprintln(stderr, err)
		if exit != nil {
			exit(1)
		}
		return 1
	}
	if *distributedHeartbeatInterval <= 0 {
		fmt.Fprintln(stderr, "--distributed-heartbeat-interval must be greater than 0")
		if exit != nil {
			exit(1)
		}
		return 1
	}
	if *distributedRequestTimeout <= 0 {
		fmt.Fprintln(stderr, "--distributed-request-timeout must be greater than 0")
		if exit != nil {
			exit(1)
		}
		return 1
	}

	if selectedRole == runnerRoleWorker {
		if err := runDistributedExecutor(context.Background(), distributedRunnerConfig{
			repoRoot:          *repoRoot,
			model:             *model,
			busBackend:        selectedDistributedBusConfig.Backend,
			busAddress:        selectedDistributedBusConfig.Address,
			busPrefix:         selectedDistributedBusConfig.Prefix,
			busOptions:        selectedDistributedBusConfig.BackendOptions(),
			executorID:        strings.TrimSpace(*distributedExecutorID),
			capabilities:      selectedDistributedExecutorCapabilities,
			heartbeatInterval: *distributedHeartbeatInterval,
			requestTimeout:    *distributedRequestTimeout,
			configRoot:        strings.TrimSpace(*configRoot),
			configDir:         strings.TrimSpace(*configDir),
		}); err != nil {
			fmt.Fprintln(stderr, err)
			if exit != nil {
				exit(1)
			}
			return 1
		}
		if exit != nil {
			exit(0)
		}
		return 0
	}

	resolvedMode, err := resolveRunnerMode(*repoRoot, *mode, *headless)
	if err != nil {
		fmt.Fprintln(stderr, err)
		if exit != nil {
			exit(1)
		}
		return 1
	}

	if err := opencode.ValidateAgent(*repoRoot); err != nil {
		fmt.Fprintln(stderr, err)
		if exit != nil {
			exit(1)
		}
		return 1
	}

	if beadsRunner == nil {
		logDir := filepath.Join(*repoRoot, "runner-logs")
		beadsRunner = adapterRunner{runner: exec.NewCommandRunner(logDir, stdout)}
	}
	if gitRunner == nil {
		logDir := filepath.Join(*repoRoot, "runner-logs")
		commandRunner := exec.NewCommandRunner(logDir, stdout)
		gitCommandAdapter := gitadapter.NewGitCommandAdapter(commandRunner)
		gitRunner = adapterGitRunner{runner: gitCommandAdapter}
	}

	// Detect which task tracker to use: tk first, then beads_rust (br), then classic beads (bd)
	var taskTrackerAdapter runner.BeadsClient
	var trackerType string

	// Allow override via environment variable for testing
	if os.Getenv("YOLO_RUNNER_TASK_TRACKER") == "beads" {
		// Check if br (beads_rust) is available, prefer it over bd (classic beads)
		if isCommandAvailable("br") {
			taskTrackerAdapter = beads.NewRustAdapter(beadsRunner)
			trackerType = "beads_rust"
		} else {
			taskTrackerAdapter = beads.New(beadsRunner)
			trackerType = "beads"
		}
	} else if tk.IsAvailable() {
		taskTrackerAdapter = tk.New(beadsRunner)
		trackerType = "tk"
	} else if beads.IsAvailable(*repoRoot) {
		// Check if br (beads_rust) is available, prefer it over bd (classic beads)
		if isCommandAvailable("br") {
			taskTrackerAdapter = beads.NewRustAdapter(beadsRunner)
			trackerType = "beads_rust"
		} else {
			taskTrackerAdapter = beads.New(beadsRunner)
			trackerType = "beads"
		}
	} else {
		fmt.Fprintln(stderr, "Error: no task tracker found. Install tk (preferred) or initialize beads.")
		if exit != nil {
			exit(1)
		}
		return 1
	}

	gitAdapter := gitadapter.New(gitRunner)
	openCodeAdapter := openCodeAdapter{runner: defaultOpenCodeRunner{}}

	resolvedRootID := *rootID
	if resolvedRootID == "" {
		inferredRootID, err := inferDefaultRootID(*repoRoot, trackerType)
		if err != nil {
			fmt.Fprintln(stderr, err)
			if exit != nil {
				exit(1)
			}
			return 1
		}
		resolvedRootID = inferredRootID
	}

	deps := runner.RunOnceDeps{
		Beads:    taskTrackerAdapter,
		Prompt:   promptBuilder{},
		OpenCode: openCodeAdapter,
		Git:      gitAdapter,
		Logger:   runnerLogger{},
	}

	resolvedConfigRoot := *configRoot
	resolvedConfigDir := *configDir
	if resolvedConfigRoot == "" {
		homeDir := os.Getenv("HOME")
		if homeDir != "" {
			resolvedConfigRoot = filepath.Join(homeDir, ".config", "opencode-runner")
		}
	}
	if resolvedConfigDir == "" && resolvedConfigRoot != "" {
		resolvedConfigDir = filepath.Join(resolvedConfigRoot, "opencode")
	}

	options := runner.RunOnceOptions{
		RepoRoot:   *repoRoot,
		RootID:     resolvedRootID,
		Model:      *model,
		ConfigRoot: resolvedConfigRoot,
		ConfigDir:  resolvedConfigDir,
		DryRun:     *dryRun,
		Out:        stdout,
	}
	options.StatusPorcelain = func(context.Context) (string, error) {
		return gitAdapter.StatusPorcelain()
	}
	options.GitRestoreAll = func(context.Context) error {
		return gitAdapter.RestoreAll()
	}
	options.GitCleanAll = func(context.Context) error {
		return gitAdapter.CleanAll()
	}
	options.CleanupConfirm = func(summary string) (bool, error) {
		return cleanupConfirmPrompt(summary, os.Stdin, stdout)
	}

	if stdout == nil {
		options.Out = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}

	var program tuiProgram
	var tuiWriter *tuiLogWriter
	var uiWriter *runnerUIEventWriter
	var closeUIDispatcher func()
	previousCommandOutput := commandOutput
	if resolvedMode == runnerModeUI {
		stdin, closeFn, err := launchYoloTUI()
		if err != nil {
			fmt.Fprintln(stderr, err)
			if exit != nil {
				exit(1)
			}
			return 1
		}
		uiWriter = newRunnerUIEventWriter(stdin)
		deps.Events = runnerUIDispatcher{writer: uiWriter}
		options.Out = uiWriter
		commandOutput = uiWriter
		closeUIDispatcher = func() {
			_ = uiWriter.Close()
			_ = closeFn()
		}
	} else if resolvedMode == "" && isTerminal(stdout) {
		stopCh := make(chan struct{})
		options.Stop = stopCh
		program = newTUIProgram(tui.NewModelWithStop(nil, stopCh), stdout, os.Stdin)
		deps.Events = tuiEmitter{program: program}
		tuiWriter = newTUILogWriter(program)
		options.Out = tuiWriter
		commandOutput = tuiWriter
		go func() {
			if err := program.Start(); err != nil {
				fmt.Fprintln(stderr, err)
				if exit != nil {
					exit(1)
				}
			}
		}()
	}
	defer func() {
		commandOutput = previousCommandOutput
		if tuiWriter != nil {
			tuiWriter.Flush()
		}
		if closeUIDispatcher != nil {
			closeUIDispatcher()
		}
	}()
	if isTerminal(stdout) {
		defer fmt.Fprint(stdout, "\x1b[?25h")
	}

	// Loop until there are no tasks left. Blocked tasks are skipped.
	_, err = runner.RunLoop(options, deps, 0, runOnce)
	if program != nil {
		program.Quit()
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		if exit != nil {
			exit(1)
		}
		return 1
	}

	if exit != nil {
		exit(0)
	}
	return 0
}

type distributedRunnerConfig struct {
	repoRoot          string
	model             string
	busBackend        string
	busAddress        string
	busPrefix         string
	busOptions        distributed.BusBackendOptions
	executorID        string
	capabilities      []distributed.Capability
	heartbeatInterval time.Duration
	requestTimeout    time.Duration
	configRoot        string
	configDir         string
}

func runDistributedExecutor(ctx context.Context, cfg distributedRunnerConfig) error {
	bus, err := newDistributedBus(cfg.busBackend, cfg.busAddress, cfg.busOptions)
	if err != nil {
		return err
	}
	defer func() {
		_ = bus.Close()
	}()

	if cfg.configRoot == "" {
		homeDir := os.Getenv("HOME")
		if homeDir != "" {
			cfg.configRoot = filepath.Join(homeDir, ".config", "opencode-runner")
		}
	}
	if cfg.configDir == "" && cfg.configRoot != "" {
		cfg.configDir = filepath.Join(cfg.configRoot, "opencode")
	}

	adapter := opencode.NewCLIRunnerAdapter(defaultOpenCodeRunner{}, nil, cfg.configRoot, cfg.configDir, "")
	executorID := cfg.executorID
	if executorID == "" {
		executorID = "executor"
	}
	worker := distributed.NewExecutorWorker(distributed.ExecutorWorkerOptions{
		ID:                executorID,
		Bus:               bus,
		Runner:            adapter,
		Subjects:          distributed.DefaultEventSubjects(cfg.busPrefix),
		Capabilities:      cfg.capabilities,
		HeartbeatInterval: cfg.heartbeatInterval,
		RequestTimeout:    cfg.requestTimeout,
	})
	return worker.Start(ctx)
}

func normalizeDistributedRoleForRunner(raw string, env string) (string, error) {
	role := strings.ToLower(strings.TrimSpace(raw))
	if role == "" {
		role = strings.TrimSpace(env)
	}
	if role == "" {
		return runnerRoleLocal, nil
	}
	switch role {
	case runnerRoleLocal, runnerRoleWorker:
		return role, nil
	default:
		return "", fmt.Errorf("invalid distributed role %q (supported: %s, %s)", role, runnerRoleLocal, runnerRoleWorker)
	}
}

func normalizeDistributedBusBackendForRunner(raw string) (string, error) {
	backend := strings.ToLower(strings.TrimSpace(raw))
	switch backend {
	case "", runnerDistributedBusRedis:
		return runnerDistributedBusRedis, nil
	case runnerDistributedBusNATS:
		return runnerDistributedBusNATS, nil
	default:
		return "", fmt.Errorf("invalid distributed bus backend %q (supported: %s, %s)", backend, runnerDistributedBusRedis, runnerDistributedBusNATS)
	}
}

func resolveRunnerDistributedBusConfig(
	repoRoot string,
	flagBackend string,
	flagAddress string,
	flagPrefix string,
	getenv func(string) string,
) (distributed.DistributedBusConfig, error) {
	configBus, err := distributed.LoadDistributedBusConfig(repoRoot)
	if err != nil {
		return distributed.DistributedBusConfig{}, err
	}
	configBus = configBus.ApplyDefaults(runnerDistributedBusRedis, "yolo")
	if getenv == nil {
		getenv = os.Getenv
	}

	selectedBackend := strings.TrimSpace(flagBackend)
	if selectedBackend == "" {
		selectedBackend = strings.TrimSpace(getenv("YOLO_DISTRIBUTED_BUS_BACKEND"))
	}
	if selectedBackend == "" {
		selectedBackend = configBus.Backend
	}
	selectedBackend, err = normalizeDistributedBusBackendForRunner(selectedBackend)
	if err != nil {
		return distributed.DistributedBusConfig{}, err
	}

	selectedAddress := strings.TrimSpace(flagAddress)
	if selectedAddress == "" {
		selectedAddress = strings.TrimSpace(getenv("YOLO_DISTRIBUTED_BUS_ADDRESS"))
	}
	if selectedAddress == "" {
		selectedAddress = configBus.Address
	}

	selectedPrefix := strings.TrimSpace(flagPrefix)
	if selectedPrefix == "" {
		selectedPrefix = strings.TrimSpace(getenv("YOLO_DISTRIBUTED_BUS_PREFIX"))
	}
	if selectedPrefix == "" {
		selectedPrefix = configBus.Prefix
	}
	if selectedPrefix == "" {
		selectedPrefix = "yolo"
	}

	configBus.Backend = selectedBackend
	configBus.Address = selectedAddress
	configBus.Prefix = selectedPrefix
	return configBus, nil
}

func parseDistributedExecutorCapabilities(raw string) ([]distributed.Capability, error) {
	capabilityRaw := strings.TrimSpace(raw)
	if capabilityRaw == "" {
		capabilityRaw = "implement,review"
	}
	values := strings.Split(capabilityRaw, ",")
	seen := map[distributed.Capability]struct{}{}
	capabilities := make([]distributed.Capability, 0, len(values))
	for _, value := range values {
		capability := strings.ToLower(strings.TrimSpace(value))
		switch distributed.Capability(capability) {
		case distributed.CapabilityImplement, distributed.CapabilityReview, distributed.CapabilityRewriteTask, distributed.CapabilityLargerModel, distributed.CapabilityServiceProxy:
			c := distributed.Capability(capability)
			if _, ok := seen[c]; ok {
				continue
			}
			seen[c] = struct{}{}
			capabilities = append(capabilities, c)
		default:
			return nil, fmt.Errorf("invalid distributed capability %q", value)
		}
	}
	if len(capabilities) == 0 {
		return nil, fmt.Errorf("at least one distributed capability is required")
	}
	return capabilities, nil
}

func compatibilityNotice() string {
	return "yolo-runner compatibility mode: prefer yolo-agent for orchestration, yolo-task for tracker actions, and yolo-tui for read-only monitoring"
}

func main() {
	RunOnceMain(os.Args[1:], runner.RunOnce, os.Exit, os.Stdout, os.Stderr, nil, nil)
}

func InitMain(args []string, exit exitFunc, stderr io.Writer) int {
	fs := flag.NewFlagSet("yolo-runner-init", flag.ContinueOnError)
	fs.SetOutput(stderr)

	repoRoot := fs.String("repo", ".", "Repository root path")

	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(stderr, err)
		if exit != nil {
			exit(1)
		}
		return 1
	}

	if err := opencode.InitAgent(*repoRoot); err != nil {
		fmt.Fprintln(stderr, err)
		if exit != nil {
			exit(1)
		}
		return 1
	}

	if exit != nil {
		exit(0)
	}
	return 0
}

type promptBuilder struct{}

func (promptBuilder) Build(issueID string, title string, description string, acceptance string) string {
	return prompt.Build(issueID, title, description, acceptance)
}

type runnerLogger struct{}

func (runnerLogger) AppendRunnerSummary(repoRoot string, issueID string, title string, status string, commitSHA string) error {
	return logging.AppendRunnerSummary(repoRoot, issueID, title, status, commitSHA)
}

type defaultOpenCodeRunner struct{}

func (defaultOpenCodeRunner) Start(args []string, env map[string]string, stdoutPath string) (opencode.Process, error) {
	return startCommandWithEnv(args, env, stdoutPath)
}

type roadmapCandidate struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Type   string `json:"issue_type"`
	Status string `json:"status"`
}

func inferDefaultRootID(repoRoot string, trackerType string) (string, error) {
	if trackerType == "tk" {
		return inferRootIDFromTK(repoRoot)
	}
	return inferRootIDFromBeads(repoRoot)
}

func inferRootIDFromBeads(repoRoot string) (string, error) {
	issuesPath := filepath.Join(repoRoot, ".beads", "issues.jsonl")
	file, err := os.Open(issuesPath)
	if err != nil {
		return "", errors.New("missing --root and no readable .beads/issues.jsonl; pass --root explicitly")
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	count := 0
	var match roadmapCandidate
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var item roadmapCandidate
		if err := json.Unmarshal(line, &item); err != nil {
			fmt.Fprintln(os.Stderr, "Error parsing line in issues.jsonl:", err)
			fmt.Fprintf(os.Stderr, "Line content (first 100 bytes): %q\n", string(line[:min(100, len(line))]))
			continue
		}
		if item.Title == "Roadmap" && item.Type == "epic" && (item.Status == "open" || item.Status == "in_progress") {
			count++
			match = item
		}
	}
	if err := scanner.Err(); err != nil {
		return "", errors.New("missing --root and unable to read .beads/issues.jsonl; pass --root explicitly")
	}
	if count == 1 && match.ID != "" {
		return match.ID, nil
	}
	return "", errors.New("missing --root and no unique Roadmap epic found; pass --root explicitly")
}

func inferRootIDFromTK(repoRoot string) (string, error) {
	// For TK, we look for a ticket with "Roadmap" in the title
	// This is a simplified approach - in practice, you might want to use tags
	cmd := osexec.Command("tk", "list", "--status=open")
	cmd.Dir = repoRoot
	output, err := cmd.Output()
	if err != nil {
		return "", errors.New("missing --root and unable to list tk tickets; pass --root explicitly")
	}

	lines := strings.Split(string(output), "\n")
	var candidates []string
	for _, line := range lines {
		if strings.Contains(line, "Roadmap") || strings.Contains(line, "roadmap") {
			fields := strings.Fields(line)
			if len(fields) > 0 {
				candidates = append(candidates, fields[0])
			}
		}
	}

	if len(candidates) == 1 {
		return candidates[0], nil
	}
	return "", errors.New("missing --root and no unique Roadmap ticket found in tk; pass --root explicitly")
}
