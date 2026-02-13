package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/anomalyco/yolo-runner/internal/agent"
	"github.com/anomalyco/yolo-runner/internal/claude"
	"github.com/anomalyco/yolo-runner/internal/codex"
	"github.com/anomalyco/yolo-runner/internal/contracts"
	"github.com/anomalyco/yolo-runner/internal/kimi"
	"github.com/anomalyco/yolo-runner/internal/opencode"
	"github.com/anomalyco/yolo-runner/internal/tk"
	gitvcs "github.com/anomalyco/yolo-runner/internal/vcs/git"
)

const (
	backendOpenCode = "opencode"
	backendCodex    = "codex"
	backendClaude   = "claude"
	backendKimi     = "kimi"
)

type runConfig struct {
	repoRoot             string
	rootID               string
	backend              string
	model                string
	maxTasks             int
	concurrency          int
	dryRun               bool
	stream               bool
	verboseStream        bool
	streamOutputInterval time.Duration
	streamOutputBuffer   int
	runnerTimeout        time.Duration
	watchdogTimeout      time.Duration
	watchdogInterval     time.Duration
	eventsPath           string
}

func RunMain(args []string, run func(context.Context, runConfig) error) int {
	fs := flag.NewFlagSet("yolo-agent", flag.ContinueOnError)
	repo := fs.String("repo", ".", "Repository root")
	root := fs.String("root", "", "Root task ID")
	backend := fs.String("backend", backendOpenCode, "Runner backend (opencode|codex|claude|kimi)")
	model := fs.String("model", "", "Model for CLI agent")
	max := fs.Int("max", 0, "Maximum tasks to execute")
	concurrency := fs.Int("concurrency", 1, "Maximum number of active task workers")
	dryRun := fs.Bool("dry-run", false, "Dry run task loop")
	stream := fs.Bool("stream", false, "Emit NDJSON events to stdout for piping into yolo-tui")
	verboseStream := fs.Bool("verbose-stream", false, "Emit every runner_output event without coalescing")
	streamOutputInterval := fs.Duration("stream-output-interval", 150*time.Millisecond, "Minimum interval between emitted runner_output events when not verbose")
	streamOutputBuffer := fs.Int("stream-output-buffer", 64, "Maximum coalesced runner_output events retained before drop")
	runnerTimeout := fs.Duration("runner-timeout", 0, "Per runner execution timeout")
	watchdogTimeout := fs.Duration("watchdog-timeout", 10*time.Minute, "No-output watchdog timeout for each runner execution")
	watchdogInterval := fs.Duration("watchdog-interval", 5*time.Second, "Polling interval used by the no-output watchdog")
	events := fs.String("events", "", "Path to JSONL events log")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *root == "" {
		fmt.Fprintln(os.Stderr, "--root is required")
		return 1
	}
	selectedBackend, _, err := selectBackend(*backend, backendSelectionOptions{
		RequireReview: true,
		Stream:        *stream,
	}, defaultBackendCapabilityMatrix())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if *concurrency <= 0 {
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
	if *watchdogTimeout <= 0 {
		fmt.Fprintln(os.Stderr, "--watchdog-timeout must be greater than 0")
		return 1
	}
	if *watchdogInterval <= 0 {
		fmt.Fprintln(os.Stderr, "--watchdog-interval must be greater than 0")
		return 1
	}

	if run == nil {
		run = defaultRun
	}

	if err := run(context.Background(), runConfig{
		repoRoot:             *repo,
		rootID:               *root,
		backend:              selectedBackend,
		model:                *model,
		maxTasks:             *max,
		concurrency:          *concurrency,
		dryRun:               *dryRun,
		stream:               *stream,
		verboseStream:        *verboseStream,
		streamOutputInterval: *streamOutputInterval,
		streamOutputBuffer:   *streamOutputBuffer,
		runnerTimeout:        *runnerTimeout,
		watchdogTimeout:      *watchdogTimeout,
		watchdogInterval:     *watchdogInterval,
		eventsPath:           *events,
	}); err != nil {
		fmt.Fprintln(os.Stderr, agent.FormatActionableError(err))
		return 1
	}
	return 0
}

func main() {
	os.Exit(RunMain(os.Args[1:], nil))
}

func defaultRun(ctx context.Context, cfg runConfig) error {
	if err := os.Chdir(cfg.repoRoot); err != nil {
		return err
	}
	cfg.eventsPath = resolveEventsPath(cfg)

	tkRunner := localRunner{dir: cfg.repoRoot}
	taskManager := tk.NewTaskManager(tkRunner)
	vcsAdapter := gitvcs.NewVCSAdapter(localGitRunner{dir: cfg.repoRoot})
	runnerAdapter, err := buildRunnerAdapter(cfg)
	if err != nil {
		return err
	}
	return runWithComponents(ctx, cfg, taskManager, runnerAdapter, vcsAdapter)
}

func buildRunnerAdapter(cfg runConfig) (contracts.AgentRunner, error) {
	selectedBackend, _, err := selectBackend(cfg.backend, backendSelectionOptions{
		RequireReview: true,
		Stream:        cfg.stream,
	}, defaultBackendCapabilityMatrix())
	if err != nil {
		return nil, err
	}

	switch selectedBackend {
	case backendOpenCode:
		return opencode.NewCLIRunnerAdapter(opencode.CommandRunner{}, nil, defaultConfigRoot(), defaultConfigDir()), nil
	case backendCodex:
		return codex.NewCLIRunnerAdapter("", nil), nil
	case backendClaude:
		return claude.NewCLIRunnerAdapter("", nil), nil
	case backendKimi:
		return kimi.NewCLIRunnerAdapter("", nil), nil
	default:
		return nil, fmt.Errorf("unsupported runner backend %q", cfg.backend)
	}
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
		sinks = append(sinks, contracts.NewStreamEventSinkWithOptions(os.Stdout, contracts.StreamEventSinkOptions{
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
		ParentID:           cfg.rootID,
		MaxTasks:           cfg.maxTasks,
		Concurrency:        cfg.concurrency,
		SchedulerStatePath: filepath.Join(cfg.repoRoot, ".yolo-runner", "scheduler-state.json"),
		DryRun:             cfg.dryRun,
		RepoRoot:           cfg.repoRoot,
		Backend:            cfg.backend,
		Model:              cfg.model,
		RunnerTimeout:      cfg.runnerTimeout,
		WatchdogTimeout:    cfg.watchdogTimeout,
		WatchdogInterval:   cfg.watchdogInterval,
		VCS:                vcs,
		RequireReview:      true,
		MergeOnSuccess:     true,
		CloneManager:       agent.NewGitCloneManager(filepath.Join(cfg.repoRoot, ".yolo-runner", "clones")),
		VCSFactory:         vcsFactory,
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

	_, err := loop.Run(ctx)
	return err
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
		"concurrency":            strconv.Itoa(cfg.concurrency),
		"model":                  cfg.model,
		"runner_timeout":         cfg.runnerTimeout.String(),
		"stream":                 strconv.FormatBool(cfg.stream),
		"verbose_stream":         strconv.FormatBool(cfg.verboseStream),
		"stream_output_interval": cfg.streamOutputInterval.String(),
		"stream_output_buffer":   strconv.Itoa(cfg.streamOutputBuffer),
		"watchdog_timeout":       cfg.watchdogTimeout.String(),
		"watchdog_interval":      cfg.watchdogInterval.String(),
	}
}

func normalizeBackend(raw string) string {
	backend := strings.ToLower(strings.TrimSpace(raw))
	if backend == "" {
		return backendOpenCode
	}
	return backend
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
