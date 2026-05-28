package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egv/yolo-runner/v2/internal/agent/preflight"
	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/engine"
	"github.com/egv/yolo-runner/v2/internal/startrek"
)

var errTrackerWatchLockHeld = errors.New("tracker-watch lock held")

type trackerWatchLock struct {
	path string
	file *os.File
}

type trackerWatchPollIteration func(context.Context) error
type trackerWatchPollWait func(context.Context, time.Duration) error

func defaultRunTrackerWatch(ctx context.Context, cfg trackerWatchConfig) error {
	trackerAgentConfig, err := newTrackerConfigService().ResolveTrackerAgentConfig(cfg.repoRoot)
	if err != nil {
		return err
	}
	lock, err := acquireTrackerWatchLock(trackerAgentConfig.LockPath)
	if err != nil {
		return err
	}
	defer func() {
		_ = lock.Release()
	}()
	return runTrackerWatchPollLoop(ctx, cfg.once, trackerAgentConfig.PollInterval, func(ctx context.Context) error {
		return runTrackerWatchPollIteration(ctx, cfg, trackerAgentConfig)
	}, waitTrackerWatchPollInterval)
}

func runTrackerWatchPollLoop(ctx context.Context, once bool, pollInterval time.Duration, iterate trackerWatchPollIteration, wait trackerWatchPollWait) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if iterate == nil {
		return errors.New("tracker-watch poll iteration is required")
	}
	if wait == nil {
		return errors.New("tracker-watch poll wait is required")
	}

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := iterate(ctx); err != nil {
			return err
		}
		if once {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := wait(ctx, pollInterval); err != nil {
			return err
		}
	}
}

func runTrackerWatchPollIteration(ctx context.Context, cfg trackerWatchConfig, trackerAgentConfig trackerAgentConfig) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if cfg.dryRun {
		return nil
	}

	profile, err := resolveTrackerProfile(cfg.repoRoot, cfg.profile, "", os.Getenv)
	if err != nil {
		return err
	}
	if profile.Tracker.Type != trackerTypeStartrek {
		return nil
	}
	if profile.Tracker.Startrek == nil {
		return errors.New("tracker.startrek settings are required")
	}

	backend, err := buildTrackerWatchStartrekBackend(profile, trackerAgentConfig)
	if err != nil {
		return err
	}
	preflightRunner, preflightModel, preflightTimeout, err := buildTrackerWatchPreflightRunner(cfg.repoRoot)
	if err != nil {
		return err
	}

	taskEngine := engine.NewTaskEngine()
	for _, queue := range profile.Tracker.Startrek.Queues {
		queueKey := strings.TrimSpace(queue.Key)
		if queueKey == "" {
			continue
		}
		tree, err := backend.GetTaskTree(ctx, queueKey)
		if err != nil {
			return err
		}
		graph, err := taskEngine.BuildGraph(tree)
		if err != nil {
			return err
		}
		for _, summary := range taskEngine.GetNextAvailable(graph) {
			if strings.TrimSpace(summary.ID) == strings.TrimSpace(tree.Root.ID) {
				continue
			}
			if err := runTrackerWatchStartrekPreflight(ctx, backend, preflightRunner, trackerWatchStartrekPreflightInput{
				TaskSummary:      summary,
				QueueRoot:        tree.Root,
				QueueRootPath:    queue.Root,
				Model:            preflightModel,
				Timeout:          preflightTimeout,
				TrackerAgentConf: trackerAgentConfig,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

type trackerWatchStartrekPreflightInput struct {
	TaskSummary      contracts.TaskSummary
	QueueRoot        contracts.Task
	QueueRootPath    string
	Model            string
	Timeout          time.Duration
	TrackerAgentConf trackerAgentConfig
}

func buildTrackerWatchStartrekBackend(profile resolvedTrackerProfile, trackerAgentConfig trackerAgentConfig) (*startrek.StorageBackend, error) {
	if profile.Tracker.Startrek == nil {
		return nil, errors.New("tracker.startrek settings are required")
	}
	tokenEnv := strings.TrimSpace(profile.Tracker.Startrek.TokenEnv)
	token := ""
	if tokenEnv != "" {
		token = os.Getenv(tokenEnv)
	}
	backend, err := startrek.NewStorageBackend(startrek.Config{
		Endpoint:   profile.Tracker.Startrek.Endpoint,
		Token:      token,
		ReadyLabel: trackerAgentConfig.Labels.Ready,
	})
	if err != nil {
		return nil, err
	}
	return backend, nil
}

func buildTrackerWatchPreflightRunner(repoRoot string) (*preflight.Runner, string, time.Duration, error) {
	defaults, err := loadYoloAgentConfigDefaults(repoRoot)
	if err != nil {
		return nil, "", 0, err
	}
	catalog, err := loadCodingAgentsCatalog(repoRoot)
	if err != nil {
		return nil, "", 0, err
	}
	timeout := time.Duration(0)
	if defaults.RunnerTimeout != nil {
		timeout = *defaults.RunnerTimeout
	}
	runner, err := buildRunnerAdapter(runConfig{
		repoRoot:      repoRoot,
		backend:       defaults.Backend,
		model:         defaults.Model,
		runnerTimeout: timeout,
		codingAgents:  catalog,
	})
	if err != nil {
		return nil, "", 0, err
	}
	return preflight.NewRunner(runner), defaults.Model, timeout, nil
}

func runTrackerWatchStartrekPreflight(ctx context.Context, backend *startrek.StorageBackend, preflightRunner *preflight.Runner, input trackerWatchStartrekPreflightInput) error {
	taskID := strings.TrimSpace(input.TaskSummary.ID)
	if taskID == "" {
		return nil
	}
	readyLabel := strings.TrimSpace(input.TrackerAgentConf.Labels.Ready)
	inProgressLabel := strings.TrimSpace(input.TrackerAgentConf.Labels.InProgress)
	if readyLabel == "" {
		readyLabel = defaultTrackerAgentReadyLabel
	}
	if inProgressLabel == "" {
		inProgressLabel = defaultTrackerAgentRunningLabel
	}

	if err := backend.RemoveLabel(ctx, taskID, readyLabel); err != nil {
		return err
	}
	if err := backend.AddLabel(ctx, taskID, inProgressLabel); err != nil {
		return err
	}

	task, err := backend.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	result, err := preflightRunner.Run(ctx, preflight.RunInput{
		Task:      *task,
		QueueRoot: input.QueueRoot,
		Model:     input.Model,
		RepoRoot:  strings.TrimSpace(input.QueueRootPath),
		Timeout:   input.Timeout,
		Metadata: map[string]string{
			"phase":   "preflight",
			"tracker": trackerTypeStartrek,
		},
	})
	if err != nil {
		return err
	}

	if result.Decision == preflight.DecisionNeedsInfo {
		_, err := (startrek.NeedsInfoTransitionService{
			Tracker:         backend,
			ProcessingLabel: inProgressLabel,
		}).Apply(ctx, startrek.NeedsInfoTransitionInput{
			IssueID:    taskID,
			Summary:    result.Summary,
			Questions:  result.Questions,
			SummoneeID: startrekSummoneeID(*task),
		})
		return err
	}

	if err := backend.RemoveLabel(ctx, taskID, inProgressLabel); err != nil {
		return err
	}
	return backend.AddLabel(ctx, taskID, readyLabel)
}

func startrekSummoneeID(task contracts.Task) string {
	for _, line := range strings.Split(task.Description, "\n") {
		line = strings.TrimSpace(line)
		value, ok := strings.CutPrefix(line, "Author:")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if start := strings.LastIndex(value, "("); start >= 0 && strings.HasSuffix(value, ")") {
			return strings.TrimSpace(strings.TrimSuffix(value[start+1:], ")"))
		}
		return value
	}
	return ""
}

func waitTrackerWatchPollInterval(ctx context.Context, interval time.Duration) error {
	if ctx == nil {
		ctx = context.Background()
	}

	timer := time.NewTimer(interval)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func acquireTrackerWatchLock(lockPath string) (*trackerWatchLock, error) {
	lockPath = strings.TrimSpace(lockPath)
	if lockPath == "" {
		return nil, errors.New("tracker-watch lock path is required")
	}
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return nil, fmt.Errorf("cannot create tracker-watch lock directory for %s: %w", lockPath, err)
	}
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("cannot open tracker-watch lock at %s: %w", lockPath, err)
	}
	if err := lockTrackerWatchFile(file); err != nil {
		_ = file.Close()
		if errors.Is(err, errTrackerWatchLockHeld) {
			return nil, fmt.Errorf("tracker-watch lock is already held at %s", lockPath)
		}
		return nil, fmt.Errorf("cannot acquire tracker-watch lock at %s: %w", lockPath, err)
	}
	if err := file.Truncate(0); err != nil {
		_ = unlockTrackerWatchFile(file)
		_ = file.Close()
		return nil, fmt.Errorf("cannot update tracker-watch lock at %s: %w", lockPath, err)
	}
	if _, err := fmt.Fprintf(file, "pid=%d\n", os.Getpid()); err != nil {
		_ = unlockTrackerWatchFile(file)
		_ = file.Close()
		return nil, fmt.Errorf("cannot update tracker-watch lock at %s: %w", lockPath, err)
	}
	return &trackerWatchLock{
		path: lockPath,
		file: file,
	}, nil
}

func (l *trackerWatchLock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	file := l.file
	l.file = nil
	unlockErr := unlockTrackerWatchFile(file)
	closeErr := file.Close()
	if unlockErr != nil {
		return fmt.Errorf("cannot release tracker-watch lock at %s: %w", l.path, unlockErr)
	}
	if closeErr != nil {
		return fmt.Errorf("cannot close tracker-watch lock at %s: %w", l.path, closeErr)
	}
	return nil
}
