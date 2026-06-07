package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egv/yolo-runner/v2/internal/agent"
	"github.com/egv/yolo-runner/v2/internal/agent/preflight"
	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/engine"
	"github.com/egv/yolo-runner/v2/internal/startrek"
	arcvcs "github.com/egv/yolo-runner/v2/internal/vcs/arc"
	gitvcs "github.com/egv/yolo-runner/v2/internal/vcs/git"
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
	runner, runnerDefaults, err := buildTrackerWatchRunner(cfg.repoRoot)
	if err != nil {
		return err
	}
	preflightRunner := preflight.NewRunner(runner)

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
		available := taskEngine.GetNextAvailable(graph)
		if len(available) == 0 {
			continue
		}
		queueRootPath, cleanupWorkspace, err := prepareTrackerWatchQueueWorkspace(ctx, cfg, queue)
		if err != nil {
			return err
		}
		queueErr := runTrackerWatchStartrekQueue(ctx, cfg, backend, runner, preflightRunner, runnerDefaults, queue, tree.Root, available, queueRootPath, trackerAgentConfig)
		if cleanupWorkspace != nil {
			cleanupWorkspace()
		}
		if queueErr != nil {
			return queueErr
		}
	}
	return nil
}

func runTrackerWatchStartrekQueue(ctx context.Context, cfg trackerWatchConfig, backend *startrek.StorageBackend, runner contracts.AgentRunner, preflightRunner *preflight.Runner, runnerDefaults trackerWatchRunnerDefaults, queue startrekQueueModel, queueRoot contracts.Task, available []contracts.TaskSummary, queueRootPath string, trackerAgentConfig trackerAgentConfig) error {
	hasReadyTask := false
	for _, summary := range available {
		if strings.TrimSpace(summary.ID) == strings.TrimSpace(queueRoot.ID) {
			continue
		}
		ready, err := runTrackerWatchStartrekPreflight(ctx, backend, preflightRunner, trackerWatchStartrekPreflightInput{
			TaskSummary:      summary,
			QueueRoot:        queueRoot,
			QueueRootPath:    queueRootPath,
			Model:            runnerDefaults.Config.Model,
			Timeout:          runnerDefaults.RunnerTimeoutValue(),
			TrackerAgentConf: trackerAgentConfig,
		})
		if err != nil {
			return err
		}
		if ready {
			hasReadyTask = true
		}
	}
	if !hasReadyTask {
		return nil
	}
	return runTrackerWatchStartrekImplementation(ctx, cfg, backend, runner, runnerDefaults, queue, queueRootPath, trackerAgentConfig)
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
		Endpoint:        profile.Tracker.Startrek.Endpoint,
		Token:           token,
		ReadyLabel:      trackerAgentConfig.Labels.Ready,
		InProgressLabel: trackerAgentConfig.Labels.InProgress,
		CompletedLabel:  trackerAgentConfig.Labels.Completed,
		BlockedLabel:    trackerAgentConfig.Labels.Blocked,
		FailedLabel:     trackerAgentConfig.Labels.Failed,
	})
	if err != nil {
		return nil, err
	}
	return backend, nil
}

func buildTrackerWatchRunner(repoRoot string) (contracts.AgentRunner, trackerWatchRunnerDefaults, error) {
	defaults, err := loadYoloAgentConfigDefaults(repoRoot)
	if err != nil {
		return nil, trackerWatchRunnerDefaults{}, err
	}
	catalog, err := loadCodingAgentsCatalog(repoRoot)
	if err != nil {
		return nil, trackerWatchRunnerDefaults{}, err
	}
	resolved := trackerWatchRunnerDefaults{Config: defaults}
	runner, err := buildRunnerAdapter(runConfig{
		repoRoot:      repoRoot,
		backend:       defaults.Backend,
		model:         defaults.Model,
		runnerTimeout: resolved.RunnerTimeoutValue(),
		codingAgents:  catalog,
	})
	if err != nil {
		return nil, trackerWatchRunnerDefaults{}, err
	}
	return runner, resolved, nil
}

type trackerWatchRunnerDefaults struct {
	Config yoloAgentConfigDefaults
}

func (d trackerWatchRunnerDefaults) RunnerTimeoutValue() time.Duration {
	if d.Config.RunnerTimeout != nil {
		return *d.Config.RunnerTimeout
	}
	return 0
}

func (d trackerWatchRunnerDefaults) WatchdogTimeoutValue() time.Duration {
	if d.Config.WatchdogTimeout != nil {
		return *d.Config.WatchdogTimeout
	}
	return 10 * time.Minute
}

func (d trackerWatchRunnerDefaults) WatchdogIntervalValue() time.Duration {
	if d.Config.WatchdogInterval != nil {
		return *d.Config.WatchdogInterval
	}
	return 5 * time.Second
}

func (d trackerWatchRunnerDefaults) RetryBudgetValue() int {
	if d.Config.RetryBudget != nil {
		return *d.Config.RetryBudget
	}
	return 5
}

func (d trackerWatchRunnerDefaults) ConcurrencyValue() int {
	if d.Config.Concurrency != nil {
		return *d.Config.Concurrency
	}
	return 1
}

func runTrackerWatchStartrekPreflight(ctx context.Context, backend *startrek.StorageBackend, preflightRunner *preflight.Runner, input trackerWatchStartrekPreflightInput) (bool, error) {
	taskID := strings.TrimSpace(input.TaskSummary.ID)
	if taskID == "" {
		return false, nil
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
		return false, err
	}
	if err := backend.AddLabel(ctx, taskID, inProgressLabel); err != nil {
		return false, err
	}

	task, err := backend.GetTask(ctx, taskID)
	if err != nil {
		return false, err
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
		return false, err
	}

	if result.Decision == preflight.DecisionNeedsInfo {
		questions := result.Questions
		if len(questions) == 0 {
			questions = []string{"Please clarify the missing implementation details needed for yolo-runner to proceed."}
		}
		_, err := (startrek.NeedsInfoTransitionService{
			Tracker:         backend,
			ProcessingLabel: inProgressLabel,
		}).Apply(ctx, startrek.NeedsInfoTransitionInput{
			IssueID:    taskID,
			Summary:    result.Summary,
			Questions:  questions,
			SummoneeID: startrekSummoneeID(*task),
		})
		if err != nil {
			return false, err
		}
		return false, nil
	}

	if err := backend.RemoveLabel(ctx, taskID, inProgressLabel); err != nil {
		return false, err
	}
	if err := backend.AddLabel(ctx, taskID, readyLabel); err != nil {
		return false, err
	}
	return true, nil
}

func runTrackerWatchStartrekImplementation(ctx context.Context, cfg trackerWatchConfig, backend contracts.StorageBackend, runner contracts.AgentRunner, defaults trackerWatchRunnerDefaults, queue startrekQueueModel, repoRoot string, _ trackerAgentConfig) error {
	queueKey := strings.TrimSpace(queue.Key)
	if queueKey == "" {
		return nil
	}
	repoRoot = strings.TrimSpace(repoRoot)
	if repoRoot == "" {
		repoRoot = strings.TrimSpace(cfg.repoRoot)
	}
	vcs, err := trackerWatchVCS(cfg.repoRoot, repoRoot)
	if err != nil {
		return err
	}
	loop := agentLoopForTrackerWatch(backend, runner, vcs, trackerWatchLoopOptions{
		ConfigRepoRoot: cfg.repoRoot,
		TaskRepoRoot:   repoRoot,
		QueueKey:       queueKey,
		Defaults:       defaults,
	})
	_, err = loop.Run(ctx)
	return err
}

func prepareTrackerWatchQueueWorkspace(ctx context.Context, cfg trackerWatchConfig, queue startrekQueueModel) (string, func(), error) {
	root := strings.TrimSpace(queue.Root)
	if queue.ArcMount == nil || !queue.ArcMount.Enabled {
		if root == "" {
			root = strings.TrimSpace(cfg.repoRoot)
		}
		return root, nil, nil
	}

	runner := localRunner{dir: cfg.repoRoot}
	mountPath := trackerWatchArcMountPath(cfg.repoRoot, queue)
	storePath := trackerWatchArcStorePath(cfg.repoRoot, queue)
	objectStorePath := trackerWatchArcObjectStorePath(cfg.repoRoot, queue)

	if mounted, err := trackerWatchArcMountIsMounted(ctx, runner, mountPath); err != nil {
		return "", nil, err
	} else if mounted {
		return mountPath, nil, nil
	}

	if err := os.MkdirAll(mountPath, 0o755); err != nil {
		return "", nil, fmt.Errorf("create arc mount path %s: %w", mountPath, err)
	}
	if err := os.MkdirAll(filepath.Join(storePath, ".overlay_v2"), 0o755); err != nil {
		return "", nil, fmt.Errorf("create arc store path %s: %w", storePath, err)
	}
	if err := os.MkdirAll(objectStorePath, 0o755); err != nil {
		return "", nil, fmt.Errorf("create arc object store path %s: %w", objectStorePath, err)
	}

	args := trackerWatchArcMountArgs(mountPath, storePath, objectStorePath, *queue.ArcMount)
	if out, err := runner.Run(args...); err != nil {
		details := strings.TrimSpace(out)
		if details == "" {
			return "", nil, fmt.Errorf("arc mount %s failed: %w", mountPath, err)
		}
		return "", nil, fmt.Errorf("arc mount %s failed: %s: %w", mountPath, details, err)
	}

	cleanup := func() {
		out, err := runner.Run("arc", "unmount", "--forget", mountPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: arc unmount --forget %s failed: %s: %v\n", mountPath, strings.TrimSpace(out), err)
		}
	}
	return mountPath, cleanup, nil
}

type trackerWatchArcMountEntry struct {
	Status string `json:"status"`
	Mount  string `json:"mount"`
}

func trackerWatchArcMountIsMounted(_ context.Context, runner localRunner, mountPath string) (bool, error) {
	out, err := runner.Run("arc", "mount", "-l", "--json")
	if err != nil {
		return false, fmt.Errorf("list arc mounts: %s: %w", strings.TrimSpace(out), err)
	}
	var entries []trackerWatchArcMountEntry
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		return false, fmt.Errorf("parse arc mount list: %w", err)
	}
	for _, entry := range entries {
		if strings.TrimSpace(entry.Status) == "mounted" && filepath.Clean(strings.TrimSpace(entry.Mount)) == filepath.Clean(mountPath) {
			return true, nil
		}
	}
	return false, nil
}

func trackerWatchArcMountPath(repoRoot string, queue startrekQueueModel) string {
	if value := strings.TrimSpace(queue.ArcMount.Mount); value != "" {
		return absTrackerWatchPath(repoRoot, value)
	}
	if value := strings.TrimSpace(queue.Root); value != "" {
		return absTrackerWatchPath(repoRoot, value)
	}
	return filepath.Join(repoRoot, ".yolo-runner", "arc-mounts", trackerWatchQueueSlug(queue.Key))
}

func trackerWatchArcStorePath(repoRoot string, queue startrekQueueModel) string {
	if value := strings.TrimSpace(queue.ArcMount.Store); value != "" {
		return absTrackerWatchPath(repoRoot, value)
	}
	return filepath.Join(repoRoot, ".yolo-runner", "arc-stores", trackerWatchQueueSlug(queue.Key), "store")
}

func trackerWatchArcObjectStorePath(repoRoot string, queue startrekQueueModel) string {
	if value := strings.TrimSpace(queue.ArcMount.ObjectStore); value != "" {
		return absTrackerWatchPath(repoRoot, value)
	}
	return filepath.Join(repoRoot, ".yolo-runner", "arc-stores", "shared-store")
}

func absTrackerWatchPath(repoRoot string, value string) string {
	if filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	return filepath.Join(repoRoot, value)
}

func trackerWatchQueueSlug(queueKey string) string {
	queueKey = strings.TrimSpace(strings.ToLower(queueKey))
	if queueKey == "" {
		return "default"
	}
	var b strings.Builder
	for _, r := range queueKey {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}

func trackerWatchArcMountArgs(mountPath string, storePath string, objectStorePath string, cfg startrekArcMount) []string {
	args := []string{
		"arc", "mount",
		"-m", mountPath,
		"-S", storePath,
		"--object-store", objectStorePath,
	}
	if boolDefault(cfg.SSHTokens, true) {
		args = append(args, "--ssh-tokens")
	}
	if boolDefault(cfg.AllowOther, true) {
		args = append(args, "--allow-other")
	}
	if boolDefault(cfg.NoHardlinks, false) {
		args = append(args, "--no-hardlinks")
	}
	if cfg.InodeCacheSize != nil {
		args = append(args, "--inode-cache-size", fmt.Sprint(*cfg.InodeCacheSize))
	} else {
		args = append(args, "--inode-cache-size", "100000")
	}
	if cfg.CacheSize != nil {
		args = append(args, "--cache-size", fmt.Sprint(*cfg.CacheSize))
	} else {
		args = append(args, "--cache-size", "134217728")
	}
	if cfg.OverrideLazyCheckout != nil {
		args = append(args, "--override-lazy-checkout="+fmt.Sprint(*cfg.OverrideLazyCheckout))
	}
	if boolDefault(cfg.NoAutoRehash, false) {
		args = append(args, "--no-auto-rehash")
	}
	return args
}

func boolDefault(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

type trackerWatchLoopOptions struct {
	ConfigRepoRoot string
	TaskRepoRoot   string
	QueueKey       string
	Defaults       trackerWatchRunnerDefaults
}

func agentLoopForTrackerWatch(backend contracts.StorageBackend, runner contracts.AgentRunner, vcs contracts.VCS, opts trackerWatchLoopOptions) *agent.Loop {
	return agent.NewLoopWithTaskEngine(backend, engine.NewTaskEngine(), runner, nil, agent.LoopOptions{
		ParentID:           strings.TrimSpace(opts.QueueKey),
		MaxRetries:         opts.Defaults.RetryBudgetValue(),
		Concurrency:        opts.Defaults.ConcurrencyValue(),
		SchedulerStatePath: filepath.Join(strings.TrimSpace(opts.ConfigRepoRoot), ".yolo-runner", "scheduler-state.json"),
		RepoRoot:           strings.TrimSpace(opts.TaskRepoRoot),
		Backend:            opts.Defaults.Config.Backend,
		Model:              opts.Defaults.Config.Model,
		RunnerTimeout:      opts.Defaults.RunnerTimeoutValue(),
		WatchdogTimeout:    opts.Defaults.WatchdogTimeoutValue(),
		WatchdogInterval:   opts.Defaults.WatchdogIntervalValue(),
		VCS:                vcs,
		RequireReview:      true,
		MergeOnSuccess:     true,
	})
}

func trackerWatchVCS(configRepoRoot string, taskRepoRoot string) (contracts.VCS, error) {
	landingMode, err := resolveLandingMode(configRepoRoot)
	if err != nil {
		return nil, err
	}
	if landingMode == landingTypeArcPR {
		return arcvcs.New(localGitRunner{dir: taskRepoRoot}), nil
	}
	return gitvcs.NewVCSAdapter(localGitRunner{dir: taskRepoRoot}), nil
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
