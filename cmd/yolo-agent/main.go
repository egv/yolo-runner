package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/anomalyco/yolo-runner/internal/agent"
	"github.com/anomalyco/yolo-runner/internal/contracts"
	"github.com/anomalyco/yolo-runner/internal/opencode"
	"github.com/anomalyco/yolo-runner/internal/tk"
	gitvcs "github.com/anomalyco/yolo-runner/internal/vcs/git"
)

type runConfig struct {
	repoRoot      string
	rootID        string
	model         string
	maxTasks      int
	concurrency   int
	dryRun        bool
	stream        bool
	runnerTimeout time.Duration
	eventsPath    string
}

func RunMain(args []string, run func(context.Context, runConfig) error) int {
	fs := flag.NewFlagSet("yolo-agent", flag.ContinueOnError)
	repo := fs.String("repo", ".", "Repository root")
	root := fs.String("root", "", "Root task ID")
	model := fs.String("model", "", "Model for CLI agent")
	max := fs.Int("max", 0, "Maximum tasks to execute")
	concurrency := fs.Int("concurrency", 1, "Maximum number of active task workers")
	dryRun := fs.Bool("dry-run", false, "Dry run task loop")
	stream := fs.Bool("stream", false, "Emit NDJSON events to stdout for piping into yolo-tui")
	runnerTimeout := fs.Duration("runner-timeout", 0, "Per runner execution timeout")
	events := fs.String("events", "", "Path to JSONL events log")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *root == "" {
		fmt.Fprintln(os.Stderr, "--root is required")
		return 1
	}
	if *concurrency <= 0 {
		fmt.Fprintln(os.Stderr, "--concurrency must be greater than 0")
		return 1
	}

	if run == nil {
		run = defaultRun
	}

	if err := run(context.Background(), runConfig{
		repoRoot:      *repo,
		rootID:        *root,
		model:         *model,
		maxTasks:      *max,
		concurrency:   *concurrency,
		dryRun:        *dryRun,
		stream:        *stream,
		runnerTimeout: *runnerTimeout,
		eventsPath:    *events,
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
	runnerAdapter := opencode.NewCLIRunnerAdapter(opencode.CommandRunner{}, nil, defaultConfigRoot(), defaultConfigDir())
	return runWithComponents(ctx, cfg, taskManager, runnerAdapter, vcsAdapter)
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
	if cfg.stream {
		sinks = append(sinks, contracts.NewStreamEventSink(os.Stdout))
	}
	if cfg.eventsPath != "" {
		sinks = append(sinks, contracts.NewFileEventSink(cfg.eventsPath))
	}
	eventSink := contracts.EventSink(nil)
	if len(sinks) == 1 {
		eventSink = sinks[0]
	} else if len(sinks) > 1 {
		eventSink = contracts.NewFanoutEventSink(sinks...)
	}
	loop := agent.NewLoop(taskManager, runner, eventSink, agent.LoopOptions{
		ParentID:           cfg.rootID,
		MaxTasks:           cfg.maxTasks,
		Concurrency:        cfg.concurrency,
		SchedulerStatePath: filepath.Join(cfg.repoRoot, ".yolo-runner", "scheduler-state.json"),
		DryRun:             cfg.dryRun,
		RepoRoot:           cfg.repoRoot,
		Model:              cfg.model,
		RunnerTimeout:      cfg.runnerTimeout,
		VCS:                vcs,
		RequireReview:      true,
		MergeOnSuccess:     true,
		CloneManager:       agent.NewGitCloneManager(filepath.Join(cfg.repoRoot, ".yolo-runner", "clones")),
	})

	_, err := loop.Run(ctx)
	return err
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
