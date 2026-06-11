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
	"github.com/egv/yolo-runner/v2/internal/agent/splitter"
	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/engine"
	"github.com/egv/yolo-runner/v2/internal/startrek"
	arcvcs "github.com/egv/yolo-runner/v2/internal/vcs/arc"
	gitvcs "github.com/egv/yolo-runner/v2/internal/vcs/git"
)

var errTrackerWatchLockHeld = errors.New("tracker-watch lock held")

const (
	defaultTrackerWatchMaxConsecutiveFailures          = 3
	defaultTrackerWatchResilientMaxConsecutiveFailures = 20
	trackerWatchStartrekNeedsInfoLabel                 = "needs-info"
	trackerWatchStartrekNeedsInfoMarker                = "needs-info"
)

type trackerWatchLock struct {
	path string
	file *os.File
}

type trackerWatchPollIteration func(context.Context) error
type trackerWatchIterationErrorHandler func(error)
type trackerWatchPollIntervalProvider func() time.Duration
type trackerWatchPollWait func(context.Context, time.Duration) error

type trackerAgentConfigResolver interface {
	ResolveTrackerAgentConfig(repoRoot string) (trackerAgentConfig, error)
}

var newTrackerWatchConfigService = func() trackerAgentConfigResolver {
	return newTrackerConfigService()
}

func defaultRunTrackerWatch(ctx context.Context, cfg trackerWatchConfig) error {
	cfg.eventsPath = resolveTrackerWatchEventsPath(cfg)
	eventSink, closeEventSink := trackerWatchEventSink(cfg)
	defer closeEventSink()
	cfg.eventSink = eventSink

	configService := newTrackerWatchConfigService()
	startupTrackerAgentConfig, err := configService.ResolveTrackerAgentConfig(cfg.repoRoot)
	if err != nil {
		return err
	}
	lock, err := acquireTrackerWatchLock(startupTrackerAgentConfig.LockPath)
	if err != nil {
		return err
	}
	defer func() {
		_ = lock.Release()
	}()
	sawIterationError := false
	recoveredFromIterationError := false
	pollInterval := trackerWatchDynamicPollIntervalProvider(cfg.repoRoot, configService, startupTrackerAgentConfig.PollInterval)
	err = runTrackerWatchPollLoop(ctx, cfg.once, pollInterval, func(ctx context.Context) error {
		err := runTrackerWatchPollIteration(ctx, cfg)
		if err == nil && sawIterationError {
			recoveredFromIterationError = true
		}
		return err
	}, func(err error) {
		sawIterationError = true
		emitTrackerWatchIterationWarning(ctx, cfg.eventSink, err)
	}, defaultTrackerWatchResilientMaxConsecutiveFailures, waitTrackerWatchPollInterval)
	if errors.Is(err, context.Canceled) && recoveredFromIterationError {
		return nil
	}
	return err
}

func trackerWatchDynamicPollIntervalProvider(repoRoot string, configService trackerAgentConfigResolver, lastGood time.Duration) trackerWatchPollIntervalProvider {
	return func() time.Duration {
		if configService == nil {
			return lastGood
		}
		trackerAgentConfig, err := configService.ResolveTrackerAgentConfig(repoRoot)
		if err != nil {
			return lastGood
		}
		lastGood = trackerAgentConfig.PollInterval
		return lastGood
	}
}

func resolveTrackerWatchEventsPath(cfg trackerWatchConfig) string {
	if strings.TrimSpace(cfg.eventsPath) != "" {
		return cfg.eventsPath
	}
	if cfg.stream {
		return ""
	}
	return filepath.Join(cfg.repoRoot, "runner-logs", "agent.events.jsonl")
}

func trackerWatchEventSink(cfg trackerWatchConfig) (contracts.EventSink, func()) {
	sinks := []contracts.EventSink{}
	closers := []func(){}
	if cfg.stream {
		sinks = append(sinks, contracts.NewStreamEventSink(os.Stdout))
	}
	if strings.TrimSpace(cfg.eventsPath) != "" {
		fileSink := contracts.NewFileEventSink(cfg.eventsPath)
		if cfg.stream {
			mirror := newMirrorEventSink(fileSink, 64)
			closers = append(closers, mirror.Close)
			sinks = append(sinks, mirror)
		} else {
			sinks = append(sinks, fileSink)
		}
	}
	closeFn := func() {
		for _, closer := range closers {
			closer()
		}
	}
	if len(sinks) == 0 {
		return nil, closeFn
	}
	if len(sinks) == 1 {
		return sinks[0], closeFn
	}
	return contracts.NewFanoutEventSink(sinks...), closeFn
}

func runTrackerWatchPollLoop(ctx context.Context, once bool, pollInterval trackerWatchPollIntervalProvider, iterate trackerWatchPollIteration, onIterationError trackerWatchIterationErrorHandler, maxConsecutiveFailures int, wait trackerWatchPollWait) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if pollInterval == nil {
		return errors.New("tracker-watch poll interval provider is required")
	}
	if iterate == nil {
		return errors.New("tracker-watch poll iteration is required")
	}
	if wait == nil {
		return errors.New("tracker-watch poll wait is required")
	}

	consecutiveFailures := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := iterate(ctx); err != nil {
			if onIterationError != nil {
				onIterationError(err)
			}
			if once {
				return err
			}
			consecutiveFailures++
			if maxConsecutiveFailures > 0 && consecutiveFailures >= maxConsecutiveFailures {
				return err
			}
		} else {
			consecutiveFailures = 0
			if once {
				return nil
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := wait(ctx, pollInterval()); err != nil {
			return err
		}
	}
}

func runTrackerWatchPollIteration(ctx context.Context, cfg trackerWatchConfig) error {
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

	trackerAgentConfig, err := newTrackerWatchConfigService().ResolveTrackerAgentConfig(cfg.repoRoot)
	if err != nil {
		return err
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
		if _, err := backend.ResumeNeedsInfoTasks(ctx, startrek.NeedsInfoResumeInput{
			QueueKey:   queueKey,
			ReadyLabel: trackerAgentConfig.Labels.Ready,
		}); err != nil {
			return err
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
		queueErr := runTrackerWatchStartrekQueue(ctx, cfg, backend, runner, preflightRunner, runnerDefaults, queue, tree.Root, available, tree.Tasks, queueRootPath, trackerAgentConfig)
		if cleanupWorkspace != nil {
			cleanupWorkspace()
		}
		if queueErr != nil {
			return queueErr
		}
	}
	return nil
}

type trackerWatchStartrekBackend interface {
	contracts.StorageBackend
	RemoveLabel(ctx context.Context, issueID string, label string) error
	AddLabel(ctx context.Context, issueID string, label string) error
	CreateIssue(ctx context.Context, opts startrek.IssueCreateOptions) (startrek.Issue, error)
	GetIssueComments(ctx context.Context, issueID string) ([]startrek.IssueComment, error)
	CreateIssueComment(ctx context.Context, issueID string, opts startrek.IssueCommentCreateOptions) (startrek.IssueComment, error)
}

type trackerWatchStartrekTaskCycleAction string

const (
	trackerWatchStartrekTaskCycleWait      trackerWatchStartrekTaskCycleAction = "wait"
	trackerWatchStartrekTaskCycleSplit     trackerWatchStartrekTaskCycleAction = "split"
	trackerWatchStartrekTaskCycleImplement trackerWatchStartrekTaskCycleAction = "implement"
	trackerWatchStartrekSplitVersion                                           = "strict-v1"
)

func runTrackerWatchStartrekQueue(ctx context.Context, cfg trackerWatchConfig, backend trackerWatchStartrekBackend, runner contracts.AgentRunner, preflightRunner *preflight.Runner, runnerDefaults trackerWatchRunnerDefaults, queue startrekQueueModel, queueRoot contracts.Task, available []contracts.TaskSummary, tasks map[string]contracts.Task, queueRootPath string, trackerAgentConfig trackerAgentConfig) error {
	hasReadyTask := false
	for _, summary := range available {
		if strings.TrimSpace(summary.ID) == strings.TrimSpace(queueRoot.ID) {
			continue
		}
		task := trackerWatchStartrekTaskFromTree(summary, tasks)
		preflightQueueRoot, err := trackerWatchStartrekPreflightQueueRoot(ctx, backend, queueRoot, task, tasks)
		if err != nil {
			return err
		}
		ready, err := runTrackerWatchStartrekPreflight(ctx, backend, preflightRunner, trackerWatchStartrekPreflightInput{
			TaskSummary:      summary,
			QueueRoot:        preflightQueueRoot,
			ConfigRepoRoot:   cfg.repoRoot,
			QueueRootPath:    queueRootPath,
			Backend:          runnerDefaults.Config.Backend,
			Model:            runnerDefaults.Config.Model,
			Timeout:          runnerDefaults.RunnerTimeoutValue(),
			TrackerAgentConf: trackerAgentConfig,
			EventSink:        cfg.eventSink,
		})
		if err != nil {
			return err
		}
		switch planTrackerWatchStartrekTaskCycle(queueRoot, task, ready) {
		case trackerWatchStartrekTaskCycleSplit:
			if err := runTrackerWatchStartrekSplit(ctx, backend, runner, trackerWatchStartrekSplitInput{
				TaskSummary:      summary,
				QueueRoot:        queueRoot,
				ConfigRepoRoot:   cfg.repoRoot,
				QueueRootPath:    queueRootPath,
				Backend:          runnerDefaults.Config.Backend,
				Model:            runnerDefaults.Config.Model,
				Timeout:          runnerDefaults.RunnerTimeoutValue(),
				TrackerAgentConf: trackerAgentConfig,
				EventSink:        cfg.eventSink,
				Queue:            queue,
			}); err != nil {
				return err
			}
		case trackerWatchStartrekTaskCycleImplement:
			hasReadyTask = true
		}
	}
	if !hasReadyTask {
		return nil
	}
	return runTrackerWatchStartrekImplementation(ctx, cfg, backend, runner, runnerDefaults, queue, queueRootPath, trackerAgentConfig)
}

func trackerWatchStartrekTaskFromTree(summary contracts.TaskSummary, tasks map[string]contracts.Task) contracts.Task {
	taskID := strings.TrimSpace(summary.ID)
	if taskID != "" {
		if task, ok := tasks[taskID]; ok {
			return task
		}
	}
	return contracts.Task{
		ID:     taskID,
		Title:  strings.TrimSpace(summary.Title),
		Status: contracts.TaskStatusOpen,
	}
}

func trackerWatchStartrekPreflightQueueRoot(ctx context.Context, backend trackerWatchStartrekBackend, queueRoot contracts.Task, task contracts.Task, tasks map[string]contracts.Task) (contracts.Task, error) {
	parentID := trackerWatchStartrekPreflightParentID(queueRoot, task, tasks)
	if parentID == "" {
		return queueRoot, nil
	}
	parent, err := backend.GetTask(ctx, parentID)
	if err != nil {
		return contracts.Task{}, fmt.Errorf("get startrek parent issue %q for preflight: %w", parentID, err)
	}
	if parent == nil || strings.TrimSpace(parent.ID) == "" {
		return queueRoot, nil
	}
	return *parent, nil
}

func trackerWatchStartrekPreflightParentID(queueRoot contracts.Task, task contracts.Task, tasks map[string]contracts.Task) string {
	rootID := strings.TrimSpace(queueRoot.ID)
	parentID := strings.TrimSpace(task.ParentID)
	if parentID != "" && !strings.EqualFold(parentID, rootID) {
		return parentID
	}
	if !trackerWatchStartrekTaskHasSubtaskLabel(task) {
		return ""
	}
	for _, dependencyID := range trackerWatchStartrekTaskDependencyIDs(task) {
		if dependencyID == "" || strings.EqualFold(dependencyID, rootID) || strings.EqualFold(dependencyID, strings.TrimSpace(task.ID)) {
			continue
		}
		if dependencyTask, ok := tasks[dependencyID]; ok {
			dependencyParentID := strings.TrimSpace(dependencyTask.ParentID)
			if dependencyParentID != "" && !strings.EqualFold(dependencyParentID, rootID) {
				continue
			}
		}
		return dependencyID
	}
	return ""
}

func trackerWatchStartrekTaskHasSubtaskLabel(task contracts.Task) bool {
	for _, token := range trackerWatchStartrekTaskDescriptionTokens(task) {
		if strings.EqualFold(strings.Trim(token, ".,;"), "agent:subtask") {
			return true
		}
	}
	return false
}

func trackerWatchStartrekTaskDependencyIDs(task contracts.Task) []string {
	seen := map[string]struct{}{}
	ids := make([]string, 0)
	for _, raw := range strings.Split(task.Metadata["dependencies"], ",") {
		ids = appendUniqueTrackerWatchStartrekDependencyID(ids, seen, raw)
	}
	for _, token := range trackerWatchStartrekTaskDescriptionTokens(task) {
		dependencyID, ok := strings.CutPrefix(strings.TrimSpace(token), "depends-on:")
		if !ok {
			continue
		}
		ids = appendUniqueTrackerWatchStartrekDependencyID(ids, seen, dependencyID)
	}
	return ids
}

func trackerWatchStartrekTaskDescriptionTokens(task contracts.Task) []string {
	return strings.FieldsFunc(task.Description, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	})
}

func appendUniqueTrackerWatchStartrekDependencyID(ids []string, seen map[string]struct{}, raw string) []string {
	id := strings.Trim(strings.TrimSpace(raw), ".,;")
	if id == "" {
		return ids
	}
	key := strings.ToLower(id)
	if _, ok := seen[key]; ok {
		return ids
	}
	seen[key] = struct{}{}
	return append(ids, id)
}

func planTrackerWatchStartrekTaskCycle(queueRoot contracts.Task, task contracts.Task, preflightReady bool) trackerWatchStartrekTaskCycleAction {
	if !preflightReady {
		return trackerWatchStartrekTaskCycleWait
	}
	taskID := strings.TrimSpace(task.ID)
	if taskID == "" || strings.EqualFold(taskID, strings.TrimSpace(queueRoot.ID)) {
		return trackerWatchStartrekTaskCycleWait
	}
	parentID := strings.TrimSpace(task.ParentID)
	if parentID == "" || strings.EqualFold(parentID, strings.TrimSpace(queueRoot.ID)) {
		return trackerWatchStartrekTaskCycleSplit
	}
	return trackerWatchStartrekTaskCycleImplement
}

type trackerWatchStartrekSplitInput struct {
	TaskSummary      contracts.TaskSummary
	QueueRoot        contracts.Task
	ConfigRepoRoot   string
	QueueRootPath    string
	Backend          string
	Model            string
	Timeout          time.Duration
	TrackerAgentConf trackerAgentConfig
	EventSink        contracts.EventSink
	Queue            startrekQueueModel
}

func runTrackerWatchStartrekSplit(ctx context.Context, backend trackerWatchStartrekBackend, runner contracts.AgentRunner, input trackerWatchStartrekSplitInput) error {
	taskID := strings.TrimSpace(input.TaskSummary.ID)
	if taskID == "" {
		return nil
	}
	task, err := backend.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	if task == nil {
		return fmt.Errorf("startrek task %q not found before split", taskID)
	}

	logPath := trackerWatchSplitLogPath(input.ConfigRepoRoot, input.Backend, taskID)
	startedAt := time.Now().UTC()
	startMetadata := trackerWatchSplitRunnerMetadata(input.Backend, input.Model, input.QueueRootPath, logPath, startedAt)
	emitTrackerWatchEvent(ctx, input.EventSink, contracts.Event{
		Type:      contracts.EventTypeRunnerStarted,
		TaskID:    task.ID,
		TaskTitle: task.Title,
		ClonePath: input.QueueRootPath,
		Message:   string(contracts.RunnerModeReview),
		Metadata:  startMetadata,
		Timestamp: startedAt,
	})

	markerExisted := false
	if _, ok, err := (startrek.SplitMarkerStore{
		Tracker:      backend,
		SplitVersion: trackerWatchStartrekSplitVersion,
	}).Read(ctx, taskID); err != nil {
		return err
	} else {
		markerExisted = ok
	}

	splitOutput, err := splitter.NewRunner(runner).Run(ctx, splitter.RunInput{
		Task:      *task,
		QueueRoot: input.QueueRoot,
		Model:     input.Model,
		RepoRoot:  strings.TrimSpace(input.QueueRootPath),
		Timeout:   input.Timeout,
		Metadata: map[string]string{
			"phase":    "split",
			"tracker":  trackerTypeStartrek,
			"log_path": logPath,
		},
		OnProgress: trackerWatchSplitProgressEmitter(ctx, input.EventSink, *task, input.QueueRootPath),
	})

	finishedAt := time.Now().UTC()
	finishMetadata := copyStringMap(startMetadata)
	finishMetadata["finished_at"] = finishedAt.Format(time.RFC3339)
	if err != nil {
		finishMetadata["status"] = string(contracts.RunnerResultFailed)
		finishMetadata["error"] = strings.TrimSpace(err.Error())
	} else {
		finishMetadata["status"] = string(contracts.RunnerResultCompleted)
		finishMetadata["subtasks"] = fmt.Sprint(len(splitOutput.Tasks))
	}
	emitTrackerWatchEvent(ctx, input.EventSink, contracts.Event{
		Type:      contracts.EventTypeRunnerFinished,
		TaskID:    task.ID,
		TaskTitle: task.Title,
		ClonePath: input.QueueRootPath,
		Message:   string(contracts.RunnerModeReview),
		Metadata:  finishMetadata,
		Timestamp: finishedAt,
	})
	if err != nil {
		return err
	}

	readyLabel := strings.TrimSpace(input.TrackerAgentConf.Labels.Ready)
	if readyLabel == "" {
		readyLabel = defaultTrackerAgentReadyLabel
	}
	splitResult, err := (startrek.IdempotentSplitSubtaskCreationService{
		Tracker:      backend,
		ReadyLabel:   readyLabel,
		SubtaskLabel: "agent:subtask",
		SplitVersion: trackerWatchStartrekSplitVersion,
	}).Create(ctx, startrek.SplitSubtasksInput{
		QueueKey:          strings.TrimSpace(input.Queue.Key),
		ParentID:          taskID,
		ParentTitle:       task.Title,
		ParentDescription: task.Description,
		Output:            splitOutput,
	})
	if err != nil {
		return err
	}
	if !markerExisted {
		if err := startrek.PostSplitCreatedComment(ctx, backend, taskID, trackerWatchStartrekSplitIssueIDs(splitResult.Issues)); err != nil {
			return err
		}
	}
	return nil
}

func trackerWatchSplitLogPath(configRepoRoot string, backend string, taskID string) string {
	configRepoRoot = strings.TrimSpace(configRepoRoot)
	taskID = strings.TrimSpace(taskID)
	if configRepoRoot == "" || taskID == "" {
		return ""
	}
	backendDir := strings.ToLower(strings.TrimSpace(backend))
	if backendDir == "" {
		backendDir = "opencode"
	}
	return filepath.Join(configRepoRoot, "runner-logs", backendDir, taskID+".split.jsonl")
}

func trackerWatchSplitRunnerMetadata(backend string, model string, clonePath string, logPath string, startedAt time.Time) map[string]string {
	backend = strings.ToLower(strings.TrimSpace(backend))
	if backend == "" {
		backend = "opencode"
	}
	return compactTrackerWatchMetadata(map[string]string{
		"backend":    backend,
		"mode":       string(contracts.RunnerModeReview),
		"phase":      "split",
		"started_at": startedAt.Format(time.RFC3339),
		"clone_path": strings.TrimSpace(clonePath),
		"log_path":   strings.TrimSpace(logPath),
		"model":      strings.TrimSpace(model),
	})
}

func trackerWatchSplitProgressEmitter(ctx context.Context, sink contracts.EventSink, task contracts.Task, clonePath string) func(contracts.RunnerProgress) {
	return func(progress contracts.RunnerProgress) {
		eventTime := progress.Timestamp
		if eventTime.IsZero() {
			eventTime = time.Now().UTC()
		}
		metadata := copyStringMap(progress.Metadata)
		if metadata == nil {
			metadata = map[string]string{}
		}
		if strings.TrimSpace(metadata["phase"]) == "" {
			metadata["phase"] = "split"
		}
		emitTrackerWatchEvent(ctx, sink, contracts.Event{
			Type:      trackerWatchEventTypeForRunnerProgress(progress.Type),
			TaskID:    task.ID,
			TaskTitle: task.Title,
			ClonePath: strings.TrimSpace(clonePath),
			Message:   progress.Message,
			Metadata:  compactTrackerWatchMetadata(metadata),
			Timestamp: eventTime,
		})
	}
}

func trackerWatchStartrekSplitIssueIDs(issues []startrek.Issue) []string {
	ids := make([]string, 0, len(issues))
	for _, issue := range issues {
		if id := strings.TrimSpace(issue.ID); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

type trackerWatchStartrekPreflightInput struct {
	TaskSummary      contracts.TaskSummary
	QueueRoot        contracts.Task
	ConfigRepoRoot   string
	QueueRootPath    string
	Backend          string
	Model            string
	Timeout          time.Duration
	TrackerAgentConf trackerAgentConfig
	EventSink        contracts.EventSink
}

func trackerWatchPreflightLogPath(configRepoRoot string, backend string, taskID string) string {
	configRepoRoot = strings.TrimSpace(configRepoRoot)
	taskID = strings.TrimSpace(taskID)
	if configRepoRoot == "" || taskID == "" {
		return ""
	}
	backendDir := strings.ToLower(strings.TrimSpace(backend))
	if backendDir == "" {
		backendDir = "opencode"
	}
	return filepath.Join(configRepoRoot, "runner-logs", backendDir, taskID+".preflight.jsonl")
}

func trackerWatchPreflightRunnerMetadata(backend string, model string, clonePath string, logPath string, startedAt time.Time) map[string]string {
	backend = strings.ToLower(strings.TrimSpace(backend))
	if backend == "" {
		backend = "opencode"
	}
	return compactTrackerWatchMetadata(map[string]string{
		"backend":    backend,
		"mode":       string(contracts.RunnerModeReview),
		"phase":      "preflight",
		"started_at": startedAt.Format(time.RFC3339),
		"clone_path": strings.TrimSpace(clonePath),
		"log_path":   strings.TrimSpace(logPath),
		"model":      strings.TrimSpace(model),
	})
}

func trackerWatchPreflightProgressEmitter(ctx context.Context, sink contracts.EventSink, task contracts.Task, clonePath string) func(contracts.RunnerProgress) {
	return func(progress contracts.RunnerProgress) {
		eventTime := progress.Timestamp
		if eventTime.IsZero() {
			eventTime = time.Now().UTC()
		}
		metadata := copyStringMap(progress.Metadata)
		if metadata == nil {
			metadata = map[string]string{}
		}
		if strings.TrimSpace(metadata["phase"]) == "" {
			metadata["phase"] = "preflight"
		}
		emitTrackerWatchEvent(ctx, sink, contracts.Event{
			Type:      trackerWatchEventTypeForRunnerProgress(progress.Type),
			TaskID:    task.ID,
			TaskTitle: task.Title,
			ClonePath: strings.TrimSpace(clonePath),
			Message:   progress.Message,
			Metadata:  compactTrackerWatchMetadata(metadata),
			Timestamp: eventTime,
		})
	}
}

func trackerWatchEventTypeForRunnerProgress(progressType string) contracts.EventType {
	switch strings.TrimSpace(progressType) {
	case string(contracts.EventTypeRunnerCommandStarted):
		return contracts.EventTypeRunnerCommandStarted
	case string(contracts.EventTypeRunnerCommandFinished):
		return contracts.EventTypeRunnerCommandFinished
	case string(contracts.EventTypeRunnerOutput):
		return contracts.EventTypeRunnerOutput
	case string(contracts.EventTypeRunnerWarning):
		return contracts.EventTypeRunnerWarning
	default:
		return contracts.EventTypeRunnerProgress
	}
}

func emitTrackerWatchEvent(ctx context.Context, sink contracts.EventSink, event contracts.Event) {
	if sink == nil {
		return
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	_ = sink.Emit(ctx, event)
}

func emitTrackerWatchIterationWarning(ctx context.Context, sink contracts.EventSink, err error) {
	if err == nil {
		return
	}
	emitTrackerWatchEvent(ctx, sink, contracts.Event{
		Type:    contracts.EventTypeRunnerWarning,
		Message: agent.FormatActionableError(err),
		Metadata: compactTrackerWatchMetadata(map[string]string{
			"phase": "watch_iteration",
		}),
	})
}

func compactTrackerWatchMetadata(metadata map[string]string) map[string]string {
	out := copyStringMap(metadata)
	for key, value := range out {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
			delete(out, key)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
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
		StatusTransitions: startrek.StatusTransitionNames{
			Ready:               trackerAgentConfig.StatusTransitions.Ready,
			InProgress:          trackerAgentConfig.StatusTransitions.InProgress,
			Completed:           trackerAgentConfig.StatusTransitions.Completed,
			Blocked:             trackerAgentConfig.StatusTransitions.Blocked,
			Failed:              trackerAgentConfig.StatusTransitions.Failed,
			CompletedResolution: trackerAgentConfig.StatusTransitions.CompletedResolution,
		},
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

func runTrackerWatchStartrekPreflight(ctx context.Context, backend trackerWatchStartrekBackend, preflightRunner *preflight.Runner, input trackerWatchStartrekPreflightInput) (bool, error) {
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
	preflightLogPath := trackerWatchPreflightLogPath(input.ConfigRepoRoot, input.Backend, taskID)
	startedAt := time.Now().UTC()
	startMetadata := trackerWatchPreflightRunnerMetadata(input.Backend, input.Model, input.QueueRootPath, preflightLogPath, startedAt)
	emitTrackerWatchEvent(ctx, input.EventSink, contracts.Event{
		Type:      contracts.EventTypeRunnerStarted,
		TaskID:    task.ID,
		TaskTitle: task.Title,
		ClonePath: input.QueueRootPath,
		Message:   string(contracts.RunnerModeReview),
		Metadata:  startMetadata,
		Timestamp: startedAt,
	})
	result, err := preflightRunner.Run(ctx, preflight.RunInput{
		Task:      *task,
		QueueRoot: input.QueueRoot,
		Model:     input.Model,
		RepoRoot:  strings.TrimSpace(input.QueueRootPath),
		Timeout:   input.Timeout,
		Metadata: map[string]string{
			"phase":    "preflight",
			"tracker":  trackerTypeStartrek,
			"log_path": preflightLogPath,
		},
		OnProgress: trackerWatchPreflightProgressEmitter(ctx, input.EventSink, *task, input.QueueRootPath),
	})
	finishedAt := time.Now().UTC()
	finishMetadata := copyStringMap(startMetadata)
	finishMetadata["finished_at"] = finishedAt.Format(time.RFC3339)
	if err != nil {
		finishMetadata["status"] = string(contracts.RunnerResultFailed)
		finishMetadata["error"] = strings.TrimSpace(err.Error())
	} else {
		finishMetadata["status"] = string(contracts.RunnerResultCompleted)
		finishMetadata["decision"] = string(result.Decision)
		finishMetadata["summary"] = strings.TrimSpace(result.Summary)
	}
	emitTrackerWatchEvent(ctx, input.EventSink, contracts.Event{
		Type:      contracts.EventTypeRunnerFinished,
		TaskID:    task.ID,
		TaskTitle: task.Title,
		ClonePath: input.QueueRootPath,
		Message:   string(contracts.RunnerModeReview),
		Metadata:  finishMetadata,
		Timestamp: finishedAt,
	})
	if err != nil {
		return false, err
	}

	if result.Decision == preflight.DecisionReply {
		if err := applyTrackerWatchStartrekPreflightReply(ctx, backend, trackerWatchStartrekPreflightReplyInput{
			IssueID:         taskID,
			ProcessingLabel: inProgressLabel,
			ReplyText:       result.ReplyText,
			SummoneeID:      startrekSummoneeID(*task),
		}); err != nil {
			return false, err
		}
		return false, nil
	}

	if result.Decision == preflight.DecisionNeedsInfo {
		questions := result.Questions
		if len(questions) == 0 {
			questions = fallbackTrackerWatchPreflightQuestions(*task, result)
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

type trackerWatchStartrekPreflightReplyInput struct {
	IssueID         string
	ProcessingLabel string
	ReplyText       string
	SummoneeID      string
}

func applyTrackerWatchStartrekPreflightReply(ctx context.Context, backend trackerWatchStartrekBackend, input trackerWatchStartrekPreflightReplyInput) error {
	if backend == nil {
		return errors.New("startrek preflight reply backend is required")
	}
	issueID := strings.TrimSpace(input.IssueID)
	if issueID == "" {
		return errors.New("startrek preflight reply issue id is required")
	}
	replyText := strings.TrimSpace(input.ReplyText)
	if replyText == "" {
		return errors.New("startrek preflight reply text is required")
	}
	processingLabel := strings.TrimSpace(input.ProcessingLabel)
	if processingLabel == "" {
		processingLabel = defaultTrackerAgentRunningLabel
	}

	if err := backend.RemoveLabel(ctx, issueID, processingLabel); err != nil {
		return fmt.Errorf("remove startrek processing label from issue %q before preflight reply: %w", issueID, err)
	}
	if err := backend.AddLabel(ctx, issueID, trackerWatchStartrekNeedsInfoLabel); err != nil {
		return fmt.Errorf("add startrek needs-info label to issue %q after preflight reply: %w", issueID, err)
	}
	if _, err := backend.CreateIssueComment(ctx, issueID, startrek.IssueCommentCreateOptions{
		Body:     replyText,
		AuthorID: strings.TrimSpace(input.SummoneeID),
		Marker:   trackerWatchStartrekNeedsInfoMarker,
	}); err != nil {
		return fmt.Errorf("post startrek preflight reply on needs-info issue %q: %w", issueID, err)
	}
	return nil
}

func fallbackTrackerWatchPreflightQuestions(task contracts.Task, result preflight.Result) []string {
	summary := strings.TrimSpace(result.Summary)
	russian := trackerWatchTaskLooksRussian(task)
	if summary != "" {
		if russian {
			return []string{fmt.Sprintf("Предварительная проверка не может продолжить: %s Уточните этот блокер.", summary)}
		}
		return []string{fmt.Sprintf("Preflight could not proceed because: %s Please clarify this blocker.", summary)}
	}
	taskTitle := strings.TrimSpace(task.Title)
	if taskTitle == "" {
		taskTitle = strings.TrimSpace(task.ID)
	}
	if taskTitle != "" {
		if russian {
			return []string{fmt.Sprintf("Предварительная проверка пометила задачу %q как требующую уточнений, но не сформулировала конкретный вопрос. Добавьте в задачу недостающие детали для реализации.", taskTitle)}
		}
		return []string{fmt.Sprintf("Preflight marked %q as needing clarification but did not provide a concrete question. Please update the task with the specific missing implementation details.", taskTitle)}
	}
	if russian {
		return []string{"Предварительная проверка пометила задачу как требующую уточнений, но не сформулировала конкретный вопрос. Добавьте в задачу недостающие детали для реализации."}
	}
	return []string{"Preflight marked this task as needing clarification but did not provide a concrete question. Please update the task with the specific missing implementation details."}
}

func trackerWatchTaskLooksRussian(task contracts.Task) bool {
	return trackerWatchContainsCyrillic(task.Title) || trackerWatchContainsCyrillic(task.Description)
}

func trackerWatchContainsCyrillic(value string) bool {
	for _, r := range value {
		if r >= '\u0400' && r <= '\u04FF' {
			return true
		}
	}
	return false
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
		EventSink:      cfg.eventSink,
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
	EventSink      contracts.EventSink
}

func agentLoopForTrackerWatch(backend contracts.StorageBackend, runner contracts.AgentRunner, vcs contracts.VCS, opts trackerWatchLoopOptions) *agent.Loop {
	return agent.NewLoopWithTaskEngine(backend, engine.NewTaskEngine(), runner, opts.EventSink, agent.LoopOptions{
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
