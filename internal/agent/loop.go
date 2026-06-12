package agent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/executor"
	"github.com/egv/yolo-runner/v2/internal/scheduler"
	"github.com/egv/yolo-runner/v2/internal/tk"
)

type taskRuntimeConfig struct {
	backend   string
	model     string
	skillset  string
	tools     []string
	mode      string
	timeout   time.Duration
	useConfig bool
}

type taskLock interface {
	TryLock(taskID string) bool
	Unlock(taskID string)
}

type landingLock interface {
	Lock()
	Unlock()
}

type CloneManager interface {
	CloneForTask(ctx context.Context, taskID string, repoRoot string) (string, error)
	Cleanup(taskID string) error
}

type VCSFactory func(repoRoot string) contracts.VCS

type pullRequestCreator interface {
	CreatePR(ctx context.Context, title string, body string) (string, error)
}

type LoopOptions struct {
	ParentID             string
	MaxRetries           int
	MaxTasks             int
	Concurrency          int
	SchedulerStatePath   string
	DryRun               bool
	Stop                 <-chan struct{}
	RepoRoot             string
	Backend              string
	Model                string
	FallbackModel        string
	RunnerTimeout        time.Duration
	WatchdogTimeout      time.Duration
	WatchdogInterval     time.Duration
	HeartbeatInterval    time.Duration
	NoOutputWarningAfter time.Duration
	TDDMode              bool
	QualityGateThreshold int
	QualityGateTools     []string
	QCGateTools          []string
	AllowLowQuality      bool
	VCS                  contracts.VCS
	RequireReview        bool
	MergeOnSuccess       bool
	CloneManager         CloneManager
	VCSFactory           VCSFactory
}

type Loop struct {
	tasks           contracts.TaskManager
	runner          contracts.AgentRunner
	events          contracts.EventSink
	options         LoopOptions
	taskLock        taskLock
	landingLock     landingLock
	cloneManager    CloneManager
	schedulerState  *schedulerStateStore
	parentFinalizer *parentFinalizer
	eventMetadataMu sync.Mutex
	eventMetadata   map[string]map[string]string
	workerStartHook func(workerID int)
}

type taskConcurrencyCalculator interface {
	CalculateConcurrency(ctx context.Context, maxWorkers int) (int, error)
}

type taskCompletionChecker interface {
	IsComplete(ctx context.Context) (bool, error)
}

func NewLoop(tasks contracts.TaskManager, runner contracts.AgentRunner, events contracts.EventSink, options LoopOptions) *Loop {
	return &Loop{
		tasks:           tasks,
		runner:          runner,
		events:          events,
		options:         options,
		taskLock:        scheduler.NewTaskLock(),
		landingLock:     scheduler.NewLandingLock(),
		cloneManager:    options.CloneManager,
		schedulerState:  newSchedulerStateStore(options.SchedulerStatePath, options.ParentID),
		parentFinalizer: newParentFinalizer(tasks),
	}
}

func NewLoopWithTaskEngine(storage contracts.StorageBackend, taskEngine contracts.TaskEngine, runner contracts.AgentRunner, events contracts.EventSink, options LoopOptions) *Loop {
	taskManager := newStorageEngineTaskManager(storage, taskEngine, options.ParentID)
	return NewLoop(taskManager, runner, events, options)
}

func (l *Loop) Run(ctx context.Context) (contracts.LoopSummary, error) {
	summary := contracts.LoopSummary{}
	requestedConcurrency := l.options.Concurrency
	if requestedConcurrency < 0 {
		requestedConcurrency = 1
	}
	if calculator, ok := l.tasks.(taskConcurrencyCalculator); ok {
		recommended, err := calculator.CalculateConcurrency(ctx, requestedConcurrency)
		if err != nil {
			return summary, err
		}
		if requestedConcurrency == 0 || recommended > 0 {
			l.options.Concurrency = recommended
		} else {
			l.options.Concurrency = requestedConcurrency
		}
	} else if requestedConcurrency == 0 {
		l.options.Concurrency = 1
	} else {
		l.options.Concurrency = requestedConcurrency
	}
	if l.options.Concurrency <= 0 {
		l.options.Concurrency = 1
	}

	if l.options.DryRun {
		next, err := l.tasks.NextTasks(ctx, l.options.ParentID)
		if err != nil {
			return summary, err
		}
		if len(next) == 0 {
			return summary, nil
		}
		task, err := l.tasks.GetTask(ctx, next[0].ID)
		if err != nil {
			return summary, err
		}
		_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeTaskStarted, TaskID: task.ID, TaskTitle: task.Title, Message: task.Title, Timestamp: time.Now().UTC()})
		summary.Skipped++
		return summary, nil
	}

	if err := l.recoverSchedulerState(ctx); err != nil {
		return summary, err
	}

	type taskResult struct {
		taskID   string
		workerID int
		queuePos int
		priority int
		summary  contracts.LoopSummary
		err      error
	}
	type taskJob struct {
		taskID   string
		queuePos int
		priority int
	}

	results := make(chan taskResult, l.options.Concurrency)
	tasksCh := make(chan taskJob)
	inFlight := map[string]struct{}{}
	queueCounter := 0

	for workerID := 0; workerID < l.options.Concurrency; workerID++ {
		id := workerID
		go func() {
			if l.workerStartHook != nil {
				l.workerStartHook(id)
			}
			for job := range tasksCh {
				func(taskID string, queuePos int, priority int) {
					defer func() {
						if l.taskLock != nil {
							l.taskLock.Unlock(taskID)
						}
					}()
					resultSummary, taskErr := l.runTask(ctx, taskID, id, queuePos, priority)
					results <- taskResult{taskID: taskID, workerID: id, queuePos: queuePos, priority: priority, summary: resultSummary, err: taskErr}
				}(job.taskID, job.queuePos, job.priority)
			}
		}()
	}
	defer close(tasksCh)

	for {
		if l.stopRequested() && len(inFlight) == 0 {
			return summary, nil
		}
		if l.options.MaxTasks > 0 && summary.TotalProcessed() >= l.options.MaxTasks && len(inFlight) == 0 {
			return summary, nil
		}

		for len(inFlight) < l.options.Concurrency {
			if l.options.MaxTasks > 0 && summary.TotalProcessed()+len(inFlight) >= l.options.MaxTasks {
				break
			}

			next, err := l.tasks.NextTasks(ctx, l.options.ParentID)
			if err != nil {
				return summary, err
			}
			if len(next) == 0 {
				break
			}

			taskID := ""
			taskPriority := 0
			for _, candidate := range next {
				if _, running := inFlight[candidate.ID]; !running {
					if l.taskLock != nil && !l.taskLock.TryLock(candidate.ID) {
						continue
					}
					taskID = candidate.ID
					if candidate.Priority != nil {
						taskPriority = *candidate.Priority
					}
					break
				}
			}
			if taskID == "" {
				break
			}

			if err := l.markTaskInFlight(taskID); err != nil {
				return summary, err
			}

			queueCounter++
			inFlight[taskID] = struct{}{}
			tasksCh <- taskJob{taskID: taskID, queuePos: queueCounter, priority: taskPriority}
		}

		if len(inFlight) == 0 {
			if completionChecker, ok := l.tasks.(taskCompletionChecker); ok {
				complete, err := completionChecker.IsComplete(ctx)
				if err != nil {
					return summary, err
				}
				if !complete {
					return summary, fmt.Errorf("task graph incomplete/stalled: no tasks in flight and no tasks available for parent %q", strings.TrimSpace(l.options.ParentID))
				}
			}
			if err := l.finalizeParentIfReady(ctx); err != nil {
				return summary, err
			}
			return summary, nil
		}

		result := <-results
		delete(inFlight, result.taskID)
		if result.err != nil {
			return summary, result.err
		}
		summary.Completed += result.summary.Completed
		summary.Blocked += result.summary.Blocked
		summary.Failed += result.summary.Failed
		summary.Skipped += result.summary.Skipped
		if result.summary.Completed > 0 {
			if err := l.finalizeParentIfReady(ctx); err != nil {
				return summary, err
			}
		}
	}
}

func (l *Loop) runTask(ctx context.Context, taskID string, workerID int, queuePos int, taskPriority int) (summary contracts.LoopSummary, err error) {
	summary = contracts.LoopSummary{}
	worker := fmt.Sprintf("worker-%d", workerID)

	task, err := l.tasks.GetTask(ctx, taskID)
	if err != nil {
		return summary, err
	}
	metadata := taskMonitoringMetadata(task, l.options.RepoRoot)
	l.rememberTaskEventMetadata(task.ID, metadata)
	_ = l.emit(ctx, contracts.Event{
		Type:      contracts.EventTypeTaskStarted,
		TaskID:    task.ID,
		TaskTitle: task.Title,
		WorkerID:  worker,
		QueuePos:  queuePos,
		Priority:  taskPriority,
		Message:   task.Title,
		Metadata:  metadata,
		Timestamp: time.Now().UTC(),
	})

	taskRuntime, err := resolveTaskRuntimeConfig(task, l.options)
	if err != nil {
		return summary, err
	}

	epicID := strings.TrimSpace(task.ParentID)
	if epicID == "" {
		epicID = strings.TrimSpace(l.options.ParentID)
	}

	if blocked, err := l.runQualityGate(ctx, task, worker, queuePos); err != nil {
		return summary, err
	} else if blocked {
		summary.Blocked++
		return summary, nil
	}

	taskRepoRoot := l.options.RepoRoot
	if l.options.TDDMode {
		testsPresent, testsFailing, err := hasTestsForTDDMode(l.options.RepoRoot)
		if err != nil {
			return summary, err
		}
		if !testsFailing {
			reason := "tdd mode tests-first gate requires tests to be present and currently failing before implementation"
			if !testsPresent {
				reason = "tdd mode tests-first gate requires adding tests before implementation"
			}
			blockedData := map[string]string{
				"triage_status": "blocked",
				"triage_reason": reason,
				"tdd_mode":      "true",
				"tests_present": strconv.FormatBool(testsPresent),
				"tests_failing": strconv.FormatBool(testsFailing),
			}
			blockedData = appendDecisionMetadata(blockedData, "blocked", reason)
			if err := l.markTaskBlockedWithData(task.ID, blockedData); err != nil {
				return summary, err
			}
			if err := l.tasks.SetTaskStatus(ctx, task.ID, contracts.TaskStatusBlocked); err != nil {
				return summary, err
			}
			finishedMetadata := map[string]string{
				"triage_status": "blocked",
				"triage_reason": reason,
				"tdd_mode":      "true",
				"tests_present": strconv.FormatBool(testsPresent),
				"tests_failing": strconv.FormatBool(testsFailing),
			}
			finishedMetadata = appendDecisionMetadata(finishedMetadata, "blocked", reason)
			_ = l.emit(ctx, contracts.Event{
				Type:      contracts.EventTypeTaskFinished,
				TaskID:    task.ID,
				TaskTitle: task.Title,
				WorkerID:  worker,
				ClonePath: taskRepoRoot,
				QueuePos:  queuePos,
				Message:   string(contracts.TaskStatusBlocked),
				Metadata:  finishedMetadata,
				Timestamp: time.Now().UTC(),
			})
			if err := l.tasks.SetTaskData(ctx, task.ID, blockedData); err != nil {
				return summary, err
			}
			_ = l.emit(ctx, contracts.Event{
				Type:      contracts.EventTypeTaskDataUpdated,
				TaskID:    task.ID,
				TaskTitle: task.Title,
				WorkerID:  worker,
				ClonePath: taskRepoRoot,
				QueuePos:  queuePos,
				Metadata:  blockedData,
				Timestamp: time.Now().UTC(),
			})
			if err := l.clearTaskTerminalState(task.ID); err != nil {
				return summary, err
			}
			summary.Blocked++
			return summary, nil
		}
	}

	if l.cloneManager != nil {
		clonePath, cloneErr := l.cloneManager.CloneForTask(ctx, task.ID, l.options.RepoRoot)
		if cloneErr != nil {
			return summary, cloneErr
		}
		taskRepoRoot = clonePath
		defer func() {
			if cleanupErr := l.cloneManager.Cleanup(task.ID); cleanupErr != nil && err == nil {
				err = cleanupErr
			}
		}()
	}

	taskBranch := ""
	taskVCS := l.vcsForRepo(taskRepoRoot)
	if taskVCS != nil {
		if err := taskVCS.EnsureMain(ctx); err != nil {
			return summary, err
		}
		branch, err := taskVCS.CreateTaskBranch(ctx, task.ID)
		if err != nil {
			return summary, err
		}
		taskBranch = branch
		if err := taskVCS.Checkout(ctx, branch); err != nil {
			return summary, err
		}
	}

	reviewRetries := 0
	if count, err := metadataRetryCount(task.Metadata, "review_retry_count"); err == nil {
		reviewRetries = count
	}
	reviewRetryFeedback := ""
	if feedback := executor.ReviewRetryBlockersFromMetadata(task.Metadata); feedback != "" {
		reviewRetryFeedback = feedback
	}
	completionRetries := 0
	if count, err := metadataRetryCount(task.Metadata, "completion_retry_count"); err == nil {
		completionRetries = count
	}
	completionAddendum := strings.TrimSpace(task.Metadata["completion_addendum"])
	implementModel := taskRuntime.model
	if implementModel == "" {
		implementModel = strings.TrimSpace(l.options.Model)
	}
	fallbackModel := strings.TrimSpace(l.options.FallbackModel)
	usedModelFallback := false
	modelBeforeFallback := ""
	modelFallbackReason := ""
	taskBackend := taskRuntime.backend
	if taskBackend == "" {
		taskBackend = strings.TrimSpace(l.options.Backend)
	}
	for {
		reviewFailed := false
		if err := l.tasks.SetTaskStatus(ctx, task.ID, contracts.TaskStatusInProgress); err != nil {
			return summary, err
		}
		implementLogPath := defaultRunnerLogPath(taskRepoRoot, task.ID, epicID, taskBackend)
		if err := ensureRunnerLogDirectory(taskRepoRoot, implementLogPath); err != nil {
			return summary, err
		}
		implementStartMeta := buildRunnerStartedMetadata(contracts.RunnerModeImplement, taskBackend, implementModel, taskRepoRoot, implementLogPath, time.Now().UTC())
		appendTaskRuntimeMetadata(implementStartMeta, taskRuntime)
		if usedModelFallback {
			implementStartMeta = appendDecisionMetadata(implementStartMeta, "model_fallback", modelFallbackReason)
			implementStartMeta["model_previous"] = modelBeforeFallback
			if fallbackModel != "" {
				implementStartMeta["model_fallback"] = fallbackModel
			}
		}
		_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeRunnerStarted, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Message: string(contracts.RunnerModeImplement), Metadata: implementStartMeta, Timestamp: time.Now().UTC()})
		requestMetadata := map[string]string{"log_path": implementLogPath, "clone_path": taskRepoRoot}
		appendTaskRuntimeMetadata(requestMetadata, taskRuntime)
		if l.options.WatchdogTimeout > 0 {
			requestMetadata["watchdog_timeout"] = l.options.WatchdogTimeout.String()
		}
		if l.options.WatchdogInterval > 0 {
			requestMetadata["watchdog_interval"] = l.options.WatchdogInterval.String()
		}

		result, err := l.runRunnerWithMonitoring(ctx, contracts.RunnerRequest{
			TaskID:   task.ID,
			ParentID: l.options.ParentID,
			Mode:     contracts.RunnerModeImplement,
			RepoRoot: taskRepoRoot,
			Model:    implementModel,
			Timeout:  taskRuntime.timeout,
			Prompt: executor.BuildImplementPrompt(
				task,
				reviewRetryFeedback,
				reviewRetries,
				completionAddendum,
				completionRetries,
				l.options.TDDMode,
			),
			Metadata: requestMetadata,
		}, task.ID, task.Title, worker, taskRepoRoot, queuePos)
		if err != nil {
			return summary, err
		}
		_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeRunnerFinished, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Message: string(result.Status), Metadata: buildRunnerFinishedMetadata(result), Timestamp: time.Now().UTC()})

		if result.Status == contracts.RunnerResultCompleted && l.options.RequireReview {
			reviewAttempt := reviewRetries + 1
			reviewTelemetry := map[string]string{
				"review_attempt":     fmt.Sprintf("%d", reviewAttempt),
				"review_retry_count": fmt.Sprintf("%d", reviewRetries),
			}
			_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeReviewStarted, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Metadata: reviewTelemetry, Timestamp: time.Now().UTC()})
			reviewLogPath := defaultRunnerLogPath(taskRepoRoot, task.ID, epicID, taskBackend)
			if err := ensureRunnerLogDirectory(taskRepoRoot, reviewLogPath); err != nil {
				return summary, err
			}
			reviewStartMeta := buildRunnerStartedMetadata(contracts.RunnerModeReview, taskBackend, implementModel, taskRepoRoot, reviewLogPath, time.Now().UTC())
			appendTaskRuntimeMetadata(reviewStartMeta, taskRuntime)
			_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeRunnerStarted, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Message: string(contracts.RunnerModeReview), Metadata: reviewStartMeta, Timestamp: time.Now().UTC()})
			reviewMetadata := map[string]string{"log_path": reviewLogPath, "clone_path": taskRepoRoot}
			appendTaskRuntimeMetadata(reviewMetadata, taskRuntime)
			if l.options.WatchdogTimeout > 0 {
				reviewMetadata["watchdog_timeout"] = l.options.WatchdogTimeout.String()
			}
			if l.options.WatchdogInterval > 0 {
				reviewMetadata["watchdog_interval"] = l.options.WatchdogInterval.String()
			}

			reviewResult, reviewErr := l.runRunnerWithMonitoring(ctx, contracts.RunnerRequest{
				TaskID:   task.ID,
				ParentID: l.options.ParentID,
				Mode:     contracts.RunnerModeReview,
				RepoRoot: taskRepoRoot,
				Model:    implementModel,
				Timeout:  taskRuntime.timeout,
				Prompt:   executor.BuildPrompt(task, contracts.RunnerModeReview, false),
				Metadata: reviewMetadata,
			}, task.ID, task.Title, worker, taskRepoRoot, queuePos)
			if reviewErr != nil {
				return summary, reviewErr
			}
			_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeRunnerFinished, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Message: string(reviewResult.Status), Metadata: buildRunnerFinishedMetadata(reviewResult), Timestamp: time.Now().UTC()})

			finalReviewResult := reviewResult
			if reviewResult.Status == contracts.RunnerResultCompleted && !reviewResult.ReviewReady && reviewVerdictFromArtifacts(reviewResult) == "" {
				verdictMetadata := map[string]string{
					"log_path":     reviewLogPath,
					"clone_path":   taskRepoRoot,
					"review_phase": "verdict_retry",
				}
				if l.options.WatchdogTimeout > 0 {
					verdictMetadata["watchdog_timeout"] = l.options.WatchdogTimeout.String()
				}
				if l.options.WatchdogInterval > 0 {
					verdictMetadata["watchdog_interval"] = l.options.WatchdogInterval.String()
				}
				verdictStartMeta := buildRunnerStartedMetadata(contracts.RunnerModeReview, taskBackend, implementModel, taskRepoRoot, reviewLogPath, time.Now().UTC())
				appendTaskRuntimeMetadata(verdictStartMeta, taskRuntime)
				verdictStartMeta["review_phase"] = "verdict_retry"
				_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeRunnerStarted, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Message: string(contracts.RunnerModeReview), Metadata: verdictStartMeta, Timestamp: time.Now().UTC()})

				verdictResult, verdictErr := l.runRunnerWithMonitoring(ctx, contracts.RunnerRequest{
					TaskID:   task.ID,
					ParentID: l.options.ParentID,
					Mode:     contracts.RunnerModeReview,
					RepoRoot: taskRepoRoot,
					Model:    implementModel,
					Timeout:  taskRuntime.timeout,
					Prompt:   executor.BuildReviewVerdictPrompt(task),
					Metadata: verdictMetadata,
				}, task.ID, task.Title, worker, taskRepoRoot, queuePos)
				if verdictErr != nil {
					return summary, verdictErr
				}
				_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeRunnerFinished, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Message: string(verdictResult.Status), Metadata: buildRunnerFinishedMetadata(verdictResult), Timestamp: time.Now().UTC()})
				finalReviewResult = verdictResult
			}

			if finalReviewResult.Status == contracts.RunnerResultCompleted && !finalReviewResult.ReviewReady {
				finalReviewResult.Status = contracts.RunnerResultFailed
				if verdict := reviewVerdictFromArtifacts(finalReviewResult); verdict == "fail" {
					finalReviewResult.Reason = buildReviewFailReason(finalReviewResult)
				} else {
					finalReviewResult.Reason = "review verdict missing explicit pass"
				}
			}
			if finalReviewResult.Status == contracts.RunnerResultFailed {
				finalReviewResult.Reason = resolveReviewFailureReason(finalReviewResult.Reason, task.Metadata)
			}
			reviewFinishedMetadata := map[string]string{
				"review_attempt":     fmt.Sprintf("%d", reviewAttempt),
				"review_retry_count": fmt.Sprintf("%d", reviewRetries),
			}
			if strings.TrimSpace(finalReviewResult.Reason) != "" {
				reviewFinishedMetadata["reason"] = strings.TrimSpace(finalReviewResult.Reason)
			}
			if verdict := reviewVerdictFromArtifacts(finalReviewResult); verdict != "" {
				reviewFinishedMetadata["review_verdict"] = verdict
			}
			if feedback := reviewFailFeedbackFromArtifacts(finalReviewResult); feedback != "" {
				reviewFinishedMetadata["review_fail_feedback"] = feedback
			}
			_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeReviewFinished, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Message: string(finalReviewResult.Status), Metadata: reviewFinishedMetadata, Timestamp: time.Now().UTC()})
			if finalReviewResult.Status != contracts.RunnerResultCompleted {
				result = finalReviewResult
				if finalReviewResult.Status == contracts.RunnerResultFailed {
					reviewFailed = true
				}
			}
		}

		switch result.Status {
		case contracts.RunnerResultCompleted:
			if blocked, err := l.runQCGate(ctx, task, result, worker, queuePos, taskRepoRoot); err != nil {
				return summary, err
			} else if blocked {
				summary.Blocked++
				return summary, nil
			}

			if err := l.markTaskCompleted(task.ID); err != nil {
				return summary, err
			}
			if l.options.MergeOnSuccess && taskVCS != nil && taskBranch != "" {
				landingState := scheduler.NewLandingQueueStateMachine(2)
				autoCommitSHA := ""
				buildLandingMetadata := func(status string, attempt int, reason string) map[string]string {
					metadata := map[string]string{"landing_status": status}
					metadata = appendDecisionMetadata(metadata, status, reason)
					if attempt > 0 {
						metadata["landing_attempt"] = fmt.Sprintf("%d", attempt)
					}
					if strings.TrimSpace(reason) != "" {
						metadata["triage_reason"] = reason
					}
					if autoCommitSHA != "" {
						metadata["auto_commit_sha"] = autoCommitSHA
					}
					return metadata
				}
				emitMergeQueueEvent := func(eventType contracts.EventType, metadata map[string]string) {
					merged := map[string]string{}
					for key, value := range metadata {
						merged[key] = value
					}
					if autoCommitSHA != "" {
						merged["auto_commit_sha"] = autoCommitSHA
					}
					_ = l.emit(ctx, contracts.Event{
						Type:      eventType,
						TaskID:    task.ID,
						TaskTitle: task.Title,
						WorkerID:  worker,
						ClonePath: taskRepoRoot,
						QueuePos:  queuePos,
						Metadata:  compactMetadata(merged),
						Timestamp: time.Now().UTC(),
					})
				}
				emitMergeQueueEvent(contracts.EventTypeMergeQueued, appendDecisionMetadata(map[string]string{"landing_status": string(landingState.State())}, string(landingState.State()), ""))
				_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeTaskDataUpdated, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Metadata: buildLandingMetadata(string(landingState.State()), 0, ""), Timestamp: time.Now().UTC()})
				if l.landingLock != nil {
					l.landingLock.Lock()
					defer l.landingLock.Unlock()
				}
				landingBlocked := false
				landingReason := ""
				autoCommitDone := false
				for attempt := 1; attempt <= 2; attempt++ {
					_ = landingState.Apply(scheduler.LandingEventBegin)
					_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeTaskDataUpdated, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Metadata: buildLandingMetadata(string(landingState.State()), attempt, ""), Timestamp: time.Now().UTC()})

					if !autoCommitDone {
						sha, err := taskVCS.CommitAll(ctx, autoLandingCommitMessage(task, l.options.ParentID))
						if err != nil {
							landingReason = err.Error()
							_ = landingState.Apply(scheduler.LandingEventFailedPermanent)
							_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeTaskDataUpdated, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Metadata: buildLandingMetadata(string(landingState.State()), attempt, landingReason), Timestamp: time.Now().UTC()})
							landingBlocked = true
							break
						}
						autoCommitDone = true
						autoCommitSHA = strings.TrimSpace(sha)
						if autoCommitSHA != "" {
							_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeTaskDataUpdated, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Metadata: buildLandingMetadata(string(landingState.State()), attempt, ""), Timestamp: time.Now().UTC()})
						}
					}

					if isDeferredPRLandingVCS(taskVCS) {
						_ = landingState.Apply(scheduler.LandingEventSucceeded)
						_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeTaskDataUpdated, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Metadata: buildLandingMetadata(string(landingState.State()), 0, ""), Timestamp: time.Now().UTC()})
						emitMergeQueueEvent(contracts.EventTypeMergeLanded, appendDecisionMetadata(map[string]string{
							"landing_status":  string(landingState.State()),
							"landing_attempt": fmt.Sprintf("%d", attempt),
						}, "landed", landingReason))
						break
					}

					if err := taskVCS.MergeToMain(ctx, taskBranch); err != nil {
						landingReason = err.Error()
						_ = landingState.Apply(scheduler.LandingEventFailedRetryable)
						_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeTaskDataUpdated, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Metadata: buildLandingMetadata(string(landingState.State()), attempt, landingReason), Timestamp: time.Now().UTC()})
						if attempt < 2 {
							emitMergeQueueEvent(contracts.EventTypeMergeRetry, appendDecisionMetadata(map[string]string{
								"landing_status":  string(landingState.State()),
								"landing_attempt": fmt.Sprintf("%d", attempt),
								"triage_reason":   landingReason,
							}, "retry", landingReason))
							if isMergeConflictError(landingReason) {
								remediationResult := l.runLandingMergeConflictRemediation(ctx, task, taskVCS, taskBranch, worker, taskRepoRoot, queuePos, landingReason, taskRuntime)
								if remediationResult.Status != contracts.RunnerResultCompleted {
									remediationReason := strings.TrimSpace(remediationResult.Reason)
									if remediationReason == "" {
										remediationReason = "runner did not complete successfully"
									}
									landingReason = "merge conflict remediation failed: " + remediationReason
									landingBlocked = true
									break
								}
								autoCommitDone = false
								autoCommitSHA = ""
							}
							_ = landingState.Apply(scheduler.LandingEventRequeued)
							_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeTaskDataUpdated, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Metadata: buildLandingMetadata(string(landingState.State()), 0, ""), Timestamp: time.Now().UTC()})
							emitMergeQueueEvent(contracts.EventTypeMergeQueued, appendDecisionMetadata(map[string]string{
								"landing_status":  string(landingState.State()),
								"landing_attempt": fmt.Sprintf("%d", attempt+1),
							}, string(landingState.State()), ""))
							continue
						}
						landingBlocked = true
						break
					}

					mergeMetadata := map[string]string{}
					if autoCommitSHA != "" {
						mergeMetadata["auto_commit_sha"] = autoCommitSHA
					}
					if len(mergeMetadata) == 0 {
						mergeMetadata = nil
					}
					_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeMergeCompleted, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Message: taskBranch, Metadata: mergeMetadata, Timestamp: time.Now().UTC()})
					if err := taskVCS.PushMain(ctx); err != nil {
						landingReason = err.Error()
						_ = landingState.Apply(scheduler.LandingEventFailedPermanent)
						_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeTaskDataUpdated, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Metadata: buildLandingMetadata(string(landingState.State()), attempt, landingReason), Timestamp: time.Now().UTC()})
						landingBlocked = true
						break
					}
					pushMetadata := map[string]string{}
					if autoCommitSHA != "" {
						pushMetadata["auto_commit_sha"] = autoCommitSHA
					}
					if len(pushMetadata) == 0 {
						pushMetadata = nil
					}
					_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypePushCompleted, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Metadata: pushMetadata, Timestamp: time.Now().UTC()})
					_ = landingState.Apply(scheduler.LandingEventSucceeded)
					_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeTaskDataUpdated, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Metadata: buildLandingMetadata(string(landingState.State()), 0, ""), Timestamp: time.Now().UTC()})
					emitMergeQueueEvent(contracts.EventTypeMergeLanded, appendDecisionMetadata(map[string]string{
						"landing_status":  string(landingState.State()),
						"landing_attempt": fmt.Sprintf("%d", attempt),
					}, "landed", landingReason))
					break
				}

				if landingBlocked {
					emitMergeQueueEvent(contracts.EventTypeMergeBlocked, appendDecisionMetadata(map[string]string{
						"landing_status": string(landingState.State()),
						"triage_reason":  landingReason,
					}, "blocked", landingReason))
					blockedData := map[string]string{"triage_status": "blocked", "landing_status": string(landingState.State())}
					if landingReason != "" {
						blockedData["triage_reason"] = landingReason
					}
					blockedData = appendDecisionMetadata(blockedData, "blocked", landingReason)
					if autoCommitSHA != "" {
						blockedData["auto_commit_sha"] = autoCommitSHA
					}
					if err := l.markTaskBlockedWithData(task.ID, blockedData); err != nil {
						return summary, err
					}
					if err := l.tasks.SetTaskStatus(ctx, task.ID, contracts.TaskStatusBlocked); err != nil {
						return summary, err
					}
					finishedMetadata := map[string]string{"triage_status": "blocked"}
					if landingReason != "" {
						finishedMetadata["triage_reason"] = landingReason
					}
					finishedMetadata = appendDecisionMetadata(finishedMetadata, "blocked", landingReason)
					_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeTaskFinished, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Message: string(contracts.TaskStatusBlocked), Metadata: finishedMetadata, Timestamp: time.Now().UTC()})
					if err := l.tasks.SetTaskData(ctx, task.ID, blockedData); err != nil {
						return summary, err
					}
					_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeTaskDataUpdated, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Metadata: blockedData, Timestamp: time.Now().UTC()})
					if err := l.clearTaskTerminalState(task.ID); err != nil {
						return summary, err
					}
					summary.Blocked++
					return summary, nil
				}
			}
			if err := l.tasks.SetTaskStatus(ctx, task.ID, contracts.TaskStatusClosed); err != nil {
				return summary, err
			}
			if err := l.clearTaskTerminalState(task.ID); err != nil {
				return summary, err
			}
			_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeTaskFinished, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Message: string(contracts.TaskStatusClosed), Timestamp: time.Now().UTC()})
			summary.Completed++
			return summary, nil
		case contracts.RunnerResultBlocked:
			blockedData := map[string]string{"triage_status": "blocked"}
			if result.Reason != "" {
				blockedData["triage_reason"] = result.Reason
			}
			blockedData = appendDecisionMetadata(blockedData, "blocked", result.Reason)
			blockedData = appendReviewOutcomeMetadata(blockedData, result)
			if err := l.markTaskBlockedWithData(task.ID, blockedData); err != nil {
				return summary, err
			}
			if err := l.tasks.SetTaskStatus(ctx, task.ID, contracts.TaskStatusBlocked); err != nil {
				return summary, err
			}
			finishedMetadata := map[string]string{"triage_status": "blocked"}
			if result.Reason != "" {
				finishedMetadata["triage_reason"] = result.Reason
			}
			finishedMetadata = appendDecisionMetadata(finishedMetadata, "blocked", result.Reason)
			finishedMetadata = appendReviewOutcomeMetadata(finishedMetadata, result)
			_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeTaskFinished, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Message: string(contracts.TaskStatusBlocked), Metadata: finishedMetadata, Timestamp: time.Now().UTC()})
			if err := l.tasks.SetTaskData(ctx, task.ID, blockedData); err != nil {
				return summary, err
			}
			_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeTaskDataUpdated, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Metadata: blockedData, Timestamp: time.Now().UTC()})
			if err := l.clearTaskTerminalState(task.ID); err != nil {
				return summary, err
			}
			summary.Blocked++
			return summary, nil
		case contracts.RunnerResultFailed:
			if !reviewFailed && !usedModelFallback && shouldUseModelFallbackForFailure(result, implementModel, fallbackModel) {
				usedModelFallback = true
				modelFallbackReason = strings.TrimSpace(result.Reason)
				modelBeforeFallback = implementModel
				implementModel = fallbackModel
				continue
			}

			reviewFail := reviewFailed || isReviewFailResult(result)
			if reviewFail {
				feedback := strings.TrimSpace(reviewFailFeedbackFromArtifacts(result))
				if feedback == "" {
					feedback = strings.TrimSpace(result.Reason)
				}
				reviewRetryFeedback = feedback
				if reviewRetries < l.options.MaxRetries {
					reviewRetries++
					retryData := map[string]string{"review_retry_count": fmt.Sprintf("%d", reviewRetries)}
					if reviewRetryFeedback != "" {
						retryData["review_feedback"] = reviewRetryFeedback
					}
					retryData = appendReviewOutcomeMetadata(retryData, result)
					if strings.TrimSpace(result.Reason) != "" {
						retryData["triage_reason"] = strings.TrimSpace(result.Reason)
					}
					retryData = appendDecisionMetadata(retryData, "retry", result.Reason)
					if err := l.tasks.SetTaskData(ctx, task.ID, retryData); err != nil {
						return summary, err
					}
					if task.Metadata == nil {
						task.Metadata = map[string]string{}
					}
					for key, value := range retryData {
						task.Metadata[key] = value
					}
					_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeTaskDataUpdated, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Metadata: retryData, Timestamp: time.Now().UTC()})
					if err := l.tasks.SetTaskStatus(ctx, task.ID, contracts.TaskStatusOpen); err != nil {
						return summary, err
					}
					continue
				}
			}

			if !reviewFail {
				completionReason := strings.TrimSpace(result.Reason)
				if completionReason == "" {
					completionReason = "implementation completion failed"
				}
				if completionRetries < l.options.MaxRetries {
					completionRetries++
					completionAddendum = appendCompletionAddendum(completionAddendum, completionRetries, completionReason)
					retryData := map[string]string{"completion_retry_count": fmt.Sprintf("%d", completionRetries)}
					retryData["completion_addendum"] = completionAddendum
					retryData = appendDecisionMetadata(retryData, "retry", completionReason)
					retryData = appendReviewOutcomeMetadata(retryData, result)
					retryData["triage_reason"] = completionReason
					if err := l.tasks.SetTaskData(ctx, task.ID, retryData); err != nil {
						return summary, err
					}
					if task.Metadata == nil {
						task.Metadata = map[string]string{}
					}
					for key, value := range retryData {
						task.Metadata[key] = value
					}
					_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeTaskDataUpdated, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Metadata: retryData, Timestamp: time.Now().UTC()})
					if err := l.tasks.SetTaskStatus(ctx, task.ID, contracts.TaskStatusOpen); err != nil {
						return summary, err
					}
					continue
				}

				completionAddendum = appendCompletionAddendum(completionAddendum, completionRetries+1, completionReason)
				blockedData := map[string]string{
					"triage_status":          "blocked",
					"completion_retry_count": fmt.Sprintf("%d", completionRetries),
					"completion_addendum":    completionAddendum,
					"triage_reason":          completionReason,
				}
				blockedData = appendDecisionMetadata(blockedData, "blocked", completionReason)
				blockedData = appendReviewOutcomeMetadata(blockedData, result)
				if err := l.markTaskBlockedWithData(task.ID, blockedData); err != nil {
					return summary, err
				}
				if err := l.tasks.SetTaskStatus(ctx, task.ID, contracts.TaskStatusBlocked); err != nil {
					return summary, err
				}
				finishedMetadata := map[string]string{
					"triage_status":          "blocked",
					"triage_reason":          completionReason,
					"completion_retry_count": fmt.Sprintf("%d", completionRetries),
					"completion_addendum":    completionAddendum,
				}
				finishedMetadata = appendDecisionMetadata(finishedMetadata, "blocked", completionReason)
				_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeTaskFinished, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Message: string(contracts.TaskStatusBlocked), Metadata: finishedMetadata, Timestamp: time.Now().UTC()})
				if err := l.tasks.SetTaskData(ctx, task.ID, blockedData); err != nil {
					return summary, err
				}
				_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeTaskDataUpdated, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Metadata: blockedData, Timestamp: time.Now().UTC()})
				if err := l.clearTaskTerminalState(task.ID); err != nil {
					return summary, err
				}
				summary.Blocked++
				return summary, nil
			}

			failedData := map[string]string{"triage_status": "failed"}
			if result.Reason != "" {
				failedData["triage_reason"] = result.Reason
			}
			failedData = appendDecisionMetadata(failedData, "failed", result.Reason)
			if reviewFail || reviewRetries > 0 {
				failedData["review_retry_count"] = fmt.Sprintf("%d", reviewRetries)
			}
			failedData = appendReviewOutcomeMetadata(failedData, result)
			if err := l.tasks.SetTaskData(ctx, task.ID, failedData); err != nil {
				return summary, err
			}
			_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeTaskDataUpdated, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Metadata: failedData, Timestamp: time.Now().UTC()})
			if err := l.tasks.SetTaskStatus(ctx, task.ID, contracts.TaskStatusFailed); err != nil {
				return summary, err
			}
			if err := l.clearTaskInFlight(task.ID); err != nil {
				return summary, err
			}
			finishedMetadata := map[string]string{"triage_status": "failed"}
			if result.Reason != "" {
				finishedMetadata["triage_reason"] = result.Reason
			}
			finishedMetadata = appendDecisionMetadata(finishedMetadata, "failed", result.Reason)
			finishedMetadata = appendReviewOutcomeMetadata(finishedMetadata, result)
			_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeTaskFinished, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Message: string(contracts.TaskStatusFailed), Metadata: finishedMetadata, Timestamp: time.Now().UTC()})
			summary.Failed++
			return summary, nil
		default:
			failedData := map[string]string{"triage_status": "failed"}
			if result.Reason != "" {
				failedData["triage_reason"] = result.Reason
			}
			failedData = appendDecisionMetadata(failedData, "failed", result.Reason)
			failedData = appendReviewOutcomeMetadata(failedData, result)
			if err := l.tasks.SetTaskData(ctx, task.ID, failedData); err != nil {
				return summary, err
			}
			_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeTaskDataUpdated, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Metadata: failedData, Timestamp: time.Now().UTC()})
			if err := l.tasks.SetTaskStatus(ctx, task.ID, contracts.TaskStatusFailed); err != nil {
				return summary, err
			}
			if err := l.clearTaskInFlight(task.ID); err != nil {
				return summary, err
			}
			finishedMetadata := map[string]string{"triage_status": "failed"}
			if result.Reason != "" {
				finishedMetadata["triage_reason"] = result.Reason
			}
			finishedMetadata = appendDecisionMetadata(finishedMetadata, "failed", result.Reason)
			finishedMetadata = appendReviewOutcomeMetadata(finishedMetadata, result)
			_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeTaskFinished, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Message: string(contracts.TaskStatusFailed), Metadata: finishedMetadata, Timestamp: time.Now().UTC()})
			summary.Failed++
			return summary, nil
		}
	}
}

func (l *Loop) vcsForRepo(repoRoot string) contracts.VCS {
	if l == nil {
		return nil
	}
	if l.options.VCSFactory != nil {
		if scoped := l.options.VCSFactory(repoRoot); scoped != nil {
			return scoped
		}
	}
	return l.options.VCS
}

func isDeferredPRLandingVCS(vcs contracts.VCS) bool {
	if vcs == nil {
		return false
	}
	_, ok := vcs.(pullRequestCreator)
	return ok
}

func taskMonitoringMetadata(task contracts.Task, arcRoot string) map[string]string {
	metadata := cloneStringMap(task.Metadata)
	if metadata == nil {
		metadata = map[string]string{}
	}
	taskID := strings.TrimSpace(task.ID)
	parentID := strings.TrimSpace(task.ParentID)
	if parentID != "" {
		metadata["parent_id"] = parentID
	}
	if strings.TrimSpace(metadata["subtask_id"]) == "" && parentID != "" && taskID != "" {
		metadata["subtask_id"] = taskID
	}
	if strings.TrimSpace(metadata["split_id"]) == "" {
		switch {
		case strings.TrimSpace(metadata[parentSplitSubtaskIDsMetadataKey]) != "" && taskID != "":
			metadata["split_id"] = taskID
		case parentID != "":
			metadata["split_id"] = parentID
		}
	}
	if strings.TrimSpace(metadata["queue"]) == "" {
		if queue := firstNonEmptyString(
			taskDescriptionField(task.Description, "Queue"),
			queueKeyFromTaskID(taskID),
			queueKeyFromTaskID(parentID),
		); queue != "" {
			metadata["queue"] = queue
		}
	}
	if strings.TrimSpace(metadata["arc_root"]) == "" {
		if arcRoot = strings.TrimSpace(arcRoot); arcRoot != "" {
			metadata["arc_root"] = arcRoot
		}
	}
	if strings.TrimSpace(metadata["pr_url"]) == "" {
		if prURL := strings.TrimSpace(metadata[parentPRURLMetadataKey]); prURL != "" {
			metadata["pr_url"] = prURL
		}
	}
	if dependencies := strings.TrimSpace(metadata["dependencies"]); dependencies != "" {
		metadata["dependencies"] = dependencies
	}
	return compactMetadata(metadata)
}

func taskDescriptionField(description string, field string) string {
	field = strings.TrimSpace(field)
	if field == "" {
		return ""
	}
	prefix := field + ":"
	for _, line := range strings.Split(description, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}

func queueKeyFromTaskID(taskID string) string {
	prefix, _, ok := strings.Cut(strings.TrimSpace(taskID), "-")
	if !ok || prefix == "" {
		return ""
	}
	for _, r := range prefix {
		if r < 'A' || r > 'Z' {
			return ""
		}
	}
	return prefix
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func cloneStringMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func (l *Loop) rememberTaskEventMetadata(taskID string, metadata map[string]string) {
	taskID = strings.TrimSpace(taskID)
	metadata = compactMetadata(metadata)
	if l == nil || taskID == "" || len(metadata) == 0 {
		return
	}
	l.eventMetadataMu.Lock()
	defer l.eventMetadataMu.Unlock()
	if l.eventMetadata == nil {
		l.eventMetadata = map[string]map[string]string{}
	}
	l.eventMetadata[taskID] = cloneStringMap(metadata)
}

func (l *Loop) taskEventMetadata(taskID string) map[string]string {
	taskID = strings.TrimSpace(taskID)
	if l == nil || taskID == "" {
		return nil
	}
	l.eventMetadataMu.Lock()
	defer l.eventMetadataMu.Unlock()
	return cloneStringMap(l.eventMetadata[taskID])
}

func (l *Loop) enrichEventMetadata(event contracts.Event) contracts.Event {
	base := l.taskEventMetadata(event.TaskID)
	if len(base) == 0 && len(event.Metadata) == 0 {
		return event
	}
	merged := map[string]string{}
	for key, value := range base {
		merged[key] = value
	}
	for key, value := range event.Metadata {
		if strings.TrimSpace(value) == "" {
			continue
		}
		merged[key] = value
	}
	if strings.TrimSpace(merged["pr_url"]) == "" {
		if prURL := strings.TrimSpace(merged[parentPRURLMetadataKey]); prURL != "" {
			merged["pr_url"] = prURL
		}
	}
	event.Metadata = compactMetadata(merged)
	return event
}

func (l *Loop) emit(ctx context.Context, event contracts.Event) error {
	if l.events == nil {
		return nil
	}
	event = l.enrichEventMetadata(event)
	return l.events.Emit(ctx, event)
}

func (l *Loop) parentPRURLSnapshot(ctx context.Context) map[string]string {
	if l == nil || l.parentFinalizer == nil || l.tasks == nil {
		return nil
	}
	parentIDs, err := l.parentFinalizer.finalizationParentIDs(ctx, l.options.ParentID)
	if err != nil {
		return nil
	}
	snapshot := map[string]string{}
	for _, parentID := range parentIDs {
		parent, err := l.tasks.GetTask(ctx, parentID)
		if err != nil {
			continue
		}
		snapshot[parentID] = strings.TrimSpace(parent.Metadata[parentPRURLMetadataKey])
	}
	return snapshot
}

func (l *Loop) emitParentPRCreatedEvents(ctx context.Context, before map[string]string) {
	if l == nil || l.parentFinalizer == nil || l.tasks == nil {
		return
	}
	parentIDs, err := l.parentFinalizer.finalizationParentIDs(ctx, l.options.ParentID)
	if err != nil {
		return
	}
	for _, parentID := range parentIDs {
		parent, err := l.tasks.GetTask(ctx, parentID)
		if err != nil {
			continue
		}
		prURL := strings.TrimSpace(parent.Metadata[parentPRURLMetadataKey])
		if prURL == "" || before[parentID] == prURL {
			continue
		}
		metadata := taskMonitoringMetadata(parent, l.options.RepoRoot)
		metadata[parentPRCreatedMetadataKey] = "true"
		metadata[parentPRURLMetadataKey] = prURL
		metadata["pr_url"] = prURL
		metadata = compactMetadata(metadata)
		l.rememberTaskEventMetadata(parent.ID, metadata)
		_ = l.emit(ctx, contracts.Event{
			Type:      contracts.EventTypeTaskDataUpdated,
			TaskID:    parent.ID,
			TaskTitle: parent.Title,
			Message:   "parent_pr_created",
			Metadata:  metadata,
			Timestamp: time.Now().UTC(),
		})
	}
}

func (l *Loop) runRunnerWithMonitoring(ctx context.Context, request contracts.RunnerRequest, taskID string, taskTitle string, worker string, clonePath string, queuePos int) (contracts.RunnerResult, error) {
	return executor.RunWithMonitoring(ctx, l.runner, loopMonitorEventSink{loop: l}, request, executor.MonitorEventContext{
		TaskID:    taskID,
		TaskTitle: taskTitle,
		WorkerID:  worker,
		ClonePath: clonePath,
		QueuePos:  queuePos,
	}, executor.MonitorOptions{
		HeartbeatInterval:    l.options.HeartbeatInterval,
		NoOutputWarningAfter: l.options.NoOutputWarningAfter,
	})
}

type loopMonitorEventSink struct {
	loop *Loop
}

func (s loopMonitorEventSink) Emit(ctx context.Context, event contracts.Event) error {
	return s.loop.emit(ctx, event)
}

func (l *Loop) runLandingMergeConflictRemediation(ctx context.Context, task contracts.Task, taskVCS contracts.VCS, taskBranch string, worker string, taskRepoRoot string, queuePos int, mergeFailureReason string, runtime taskRuntimeConfig) contracts.RunnerResult {
	if taskVCS != nil && strings.TrimSpace(taskBranch) != "" {
		if err := taskVCS.Checkout(ctx, taskBranch); err != nil {
			return contracts.RunnerResult{Status: contracts.RunnerResultFailed, Reason: fmt.Sprintf("git checkout %s failed: %v", taskBranch, err)}
		}
	}

	epicID := strings.TrimSpace(task.ParentID)
	if epicID == "" {
		epicID = strings.TrimSpace(l.options.ParentID)
	}

	runtimeBackend := strings.TrimSpace(runtime.backend)
	if runtimeBackend == "" {
		runtimeBackend = strings.TrimSpace(l.options.Backend)
	}
	runtimeModel := strings.TrimSpace(runtime.model)
	if runtimeModel == "" {
		runtimeModel = strings.TrimSpace(l.options.Model)
	}

	remediationLogPath := defaultRunnerLogPath(taskRepoRoot, task.ID, epicID, runtimeBackend)
	if err := ensureRunnerLogDirectory(taskRepoRoot, remediationLogPath); err != nil {
		return contracts.RunnerResult{Status: contracts.RunnerResultFailed, Reason: err.Error()}
	}
	remediationStartMeta := buildRunnerStartedMetadata(contracts.RunnerModeImplement, runtimeBackend, runtimeModel, taskRepoRoot, remediationLogPath, time.Now().UTC())
	remediationStartMeta = appendTaskRuntimeMetadata(remediationStartMeta, runtime)
	remediationStartMeta["landing_phase"] = "merge_conflict_remediation"
	_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeRunnerStarted, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Message: string(contracts.RunnerModeImplement), Metadata: remediationStartMeta, Timestamp: time.Now().UTC()})

	remediationMetadata := map[string]string{"log_path": remediationLogPath, "clone_path": taskRepoRoot, "landing_phase": "merge_conflict_remediation"}
	remediationMetadata = appendTaskRuntimeMetadata(remediationMetadata, runtime)
	if l.options.WatchdogTimeout > 0 {
		remediationMetadata["watchdog_timeout"] = l.options.WatchdogTimeout.String()
	}
	if l.options.WatchdogInterval > 0 {
		remediationMetadata["watchdog_interval"] = l.options.WatchdogInterval.String()
	}

	result, err := l.runRunnerWithMonitoring(ctx, contracts.RunnerRequest{
		TaskID:   task.ID,
		ParentID: l.options.ParentID,
		Mode:     contracts.RunnerModeImplement,
		RepoRoot: taskRepoRoot,
		Model:    runtimeModel,
		Timeout:  runtime.timeout,
		Prompt:   buildMergeConflictRemediationPrompt(task, taskBranch, mergeFailureReason),
		Metadata: remediationMetadata,
	}, task.ID, task.Title, worker, taskRepoRoot, queuePos)
	if err != nil {
		result = contracts.RunnerResult{Status: contracts.RunnerResultFailed, Reason: err.Error()}
	}

	_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeRunnerFinished, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Message: string(result.Status), Metadata: buildRunnerFinishedMetadata(result), Timestamp: time.Now().UTC()})
	return result
}

func resolveTaskRuntimeConfig(task contracts.Task, options LoopOptions) (taskRuntimeConfig, error) {
	backend := strings.TrimSpace(options.Backend)
	model := strings.TrimSpace(options.Model)
	timeout := options.RunnerTimeout

	taskRuntime := taskRuntimeConfig{
		backend: backend,
		model:   model,
		timeout: timeout,
	}

	overrides, hasOverrides, err := tk.ParseTicketFrontmatterFromDescription(task.Description)
	if err != nil {
		return taskRuntime, err
	}
	if !hasOverrides {
		return taskRuntime, nil
	}

	taskRuntime.useConfig = true
	if strings.TrimSpace(overrides.Backend) != "" {
		taskRuntime.backend = strings.TrimSpace(overrides.Backend)
	}
	if strings.TrimSpace(overrides.Model) != "" {
		taskRuntime.model = strings.TrimSpace(overrides.Model)
	}
	if strings.TrimSpace(overrides.Skillset) != "" {
		taskRuntime.skillset = strings.TrimSpace(overrides.Skillset)
	}
	if len(overrides.Tools) > 0 {
		taskRuntime.tools = append([]string{}, overrides.Tools...)
	}
	if strings.TrimSpace(overrides.Mode) != "" {
		taskRuntime.mode = strings.TrimSpace(overrides.Mode)
	}
	if overrides.HasTimeout {
		taskRuntime.timeout = overrides.Timeout
	}
	return taskRuntime, nil
}

func appendTaskRuntimeMetadata(metadata map[string]string, runtime taskRuntimeConfig) map[string]string {
	if metadata == nil {
		metadata = map[string]string{}
	}
	if runtime.useConfig {
		metadata["runtime_config"] = "true"
	}
	if strings.TrimSpace(runtime.backend) != "" {
		if strings.TrimSpace(metadata["backend"]) == "" {
			metadata["backend"] = strings.TrimSpace(runtime.backend)
		}
		metadata["runtime_backend"] = strings.TrimSpace(runtime.backend)
	}
	if strings.TrimSpace(runtime.model) != "" {
		if strings.TrimSpace(metadata["model"]) == "" {
			metadata["model"] = strings.TrimSpace(runtime.model)
		}
		metadata["runtime_model"] = strings.TrimSpace(runtime.model)
	}
	if strings.TrimSpace(runtime.skillset) != "" {
		metadata["skillset"] = strings.TrimSpace(runtime.skillset)
		metadata["runtime_skillset"] = strings.TrimSpace(runtime.skillset)
	}
	if runtime.timeout >= 0 {
		metadata["timeout"] = runtime.timeout.String()
		metadata["runtime_timeout"] = runtime.timeout.String()
	}
	if len(runtime.tools) > 0 {
		tools := strings.Join(runtime.tools, ",")
		metadata["tools"] = tools
		metadata["runtime_tools"] = tools
	}
	if strings.TrimSpace(runtime.mode) != "" {
		metadata["task_mode"] = strings.TrimSpace(strings.ToLower(runtime.mode))
		metadata["runtime_mode"] = strings.TrimSpace(strings.ToLower(runtime.mode))
	}
	return compactMetadata(metadata)
}

func hasTestsForTDDMode(repoRoot string) (bool, bool, error) {
	root := strings.TrimSpace(repoRoot)
	if root == "" {
		return false, false, nil
	}

	found := false
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == ".git" || info.Name() == "node_modules" || info.Name() == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.Contains(filepath.Base(path), "_test.") {
			found = true
		}
		return nil
	})
	if err != nil {
		return false, false, err
	}
	if !found {
		return false, false, nil
	}
	failing, err := hasFailingTestsForTDDMode(root)
	return found, failing, err
}

func hasFailingTestsForTDDMode(repoRoot string) (bool, error) {
	cmd := exec.Command("go", "test", "./...")
	cmd.Dir = repoRoot
	_, err := cmd.CombinedOutput()
	if err == nil {
		return false, nil
	}
	if _, ok := err.(*exec.ExitError); ok {
		return true, nil
	}
	return false, fmt.Errorf("run tests for tdd mode: %w", err)
}

func metadataRetryCount(metadata map[string]string, key string) (int, error) {
	if len(metadata) == 0 {
		return 0, fmt.Errorf("metadata missing")
	}
	raw := strings.TrimSpace(metadata[key])
	if raw == "" {
		return 0, fmt.Errorf("metadata missing")
	}
	retryCount, err := strconv.Atoi(raw)
	if err != nil {
		return 0, err
	}
	return retryCount, nil
}

func appendCompletionAddendum(previous string, attempt int, reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "implementation completion failed"
	}
	entry := fmt.Sprintf("Attempt %d failure: %s", attempt, reason)
	previous = strings.TrimSpace(previous)
	if previous == "" {
		return entry
	}
	return previous + "\n" + entry
}

func (l *Loop) runQualityGate(ctx context.Context, task contracts.Task, worker string, queuePos int) (bool, error) {
	return executor.RunQualityGate(ctx, task, l.gateDependencies(), l.gateOptions(), executor.GateEventContext{
		WorkerID:  worker,
		ClonePath: l.options.RepoRoot,
		QueuePos:  queuePos,
	})
}

func (l *Loop) runQCGate(ctx context.Context, task contracts.Task, result contracts.RunnerResult, worker string, queuePos int, taskRepoRoot string) (bool, error) {
	return executor.RunQCGate(ctx, task, result, l.gateDependencies(), l.gateOptions(), executor.GateEventContext{
		WorkerID:  worker,
		ClonePath: taskRepoRoot,
		QueuePos:  queuePos,
	})
}

func (l *Loop) gateOptions() executor.GateOptions {
	return executor.GateOptions{
		RepoRoot:             l.options.RepoRoot,
		QualityGateThreshold: l.options.QualityGateThreshold,
		QualityGateTools:     l.options.QualityGateTools,
		QCGateTools:          l.options.QCGateTools,
		AllowLowQuality:      l.options.AllowLowQuality,
		RequireReview:        l.options.RequireReview,
	}
}

func (l *Loop) gateDependencies() executor.GateDependencies {
	return executor.GateDependencies{
		Tasks:                   l.tasks,
		Events:                  loopMonitorEventSink{loop: l},
		MarkTaskBlockedWithData: l.markTaskBlockedWithData,
		ClearTaskTerminalState:  l.clearTaskTerminalState,
	}
}

func buildMergeConflictRemediationPrompt(task contracts.Task, taskBranch string, mergeFailureReason string) string {
	base := executor.BuildImplementPrompt(task, "", 0, "", 0, false)
	sections := []string{
		base,
		strings.Join([]string{
			"Landing Merge Remediation:",
			"- Auto-landing failed while merging the task branch into main.",
			"- Resolve merge conflicts on the task branch so merge-to-main can succeed.",
			"- Keep accepted behavior intact; do not discard required changes.",
			"- Run relevant tests after conflict resolution.",
			"- Commit conflict-resolution changes on the task branch.",
		}, "\n"),
	}
	if strings.TrimSpace(taskBranch) != "" {
		sections = append(sections, "Target Branch: "+strings.TrimSpace(taskBranch))
	}
	if strings.TrimSpace(mergeFailureReason) != "" {
		sections = append(sections, "Merge Failure Details:\n"+strings.TrimSpace(mergeFailureReason))
	}
	return strings.Join(sections, "\n\n")
}

func isMergeConflictError(reason string) bool {
	lower := strings.ToLower(strings.TrimSpace(reason))
	if lower == "" {
		return false
	}
	for _, needle := range []string{"automatic merge failed", "merge conflict", "conflict (", "needs merge"} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

func isReviewFailResult(result contracts.RunnerResult) bool {
	if verdict := reviewVerdictFromArtifacts(result); verdict == "fail" {
		return true
	}
	reason := strings.TrimSpace(result.Reason)
	if reason == "" {
		return false
	}
	lower := strings.ToLower(reason)
	return strings.HasPrefix(lower, "review rejected") ||
		strings.Contains(lower, "review verdict returned fail") ||
		strings.Contains(lower, "review feedback") ||
		strings.Contains(lower, "failing acceptance criteria")
}

func shouldUseModelFallbackForFailure(result contracts.RunnerResult, currentModel string, fallbackModel string) bool {
	return isRecoverableModelFailureResult(result, currentModel, fallbackModel)
}

func isRecoverableModelFailureReason(reason string) bool {
	text := strings.ToLower(strings.TrimSpace(reason))
	if text == "" {
		return false
	}

	// Explicitly avoid fallback on review-style failures; those are handled by
	// the dedicated review retry path.
	for _, needle := range []string{
		"review rejected",
		"review verdict",
		"review feedback",
		"failing acceptance criteria",
	} {
		if strings.Contains(text, needle) {
			return false
		}
	}

	for _, needle := range []string{
		"type failure",
		"type error",
		"type checker",
		"type mismatch",
		"type check",
		"type validation",
		"type annotation",
		"tool failure",
		"tool call",
		"tool call failed",
		"tool unavailable",
		"tool error",
		"tool execution",
		"tool timed out",
		"tool timeout",
		"tool response",
		"parse failure",
		"invalid json",
		"json parse",
		"invalid json response",
		"malformed output",
		"provider error",
		"rate limit",
		"too many requests",
		"quota exceeded",
		"429",
	} {
		if strings.Contains(text, needle) {
			return true
		}
	}

	return false
}

func isRecoverableModelFailureResult(result contracts.RunnerResult, currentModel string, fallbackModel string) bool {
	return isRecoverableModelFailureReason(result.Reason) && strings.TrimSpace(currentModel) != "" && strings.TrimSpace(fallbackModel) != "" && !strings.EqualFold(strings.TrimSpace(currentModel), strings.TrimSpace(fallbackModel))
}

func autoLandingCommitMessage(task contracts.Task, fallbackParentID string) string {
	taskID := strings.TrimSpace(task.ID)
	subject := "chore(task): auto-commit before landing"
	if taskID == "" {
		return subject
	}
	subject = fmt.Sprintf("%s %s", subject, taskID)

	parentID := strings.TrimSpace(task.ParentID)
	if parentID == "" {
		parentID = strings.TrimSpace(fallbackParentID)
	}
	lineage := commitMessageLineage(parentID, taskID)
	if len(lineage) == 0 {
		return subject
	}
	return subject + "\n\n" + strings.Join(lineage, "\n")
}

func commitMessageLineage(parentID string, subtaskID string) []string {
	var lines []string
	if parentID != "" {
		lines = append(lines, "Parent: "+parentID)
	}
	if subtaskID != "" {
		lines = append(lines, "Subtask: "+subtaskID)
	}
	relates := uniqueNonEmpty(parentID, subtaskID)
	if len(relates) > 0 {
		lines = append(lines, "Relates: "+strings.Join(relates, ", "))
	}
	return lines
}

func uniqueNonEmpty(values ...string) []string {
	seen := map[string]struct{}{}
	var result []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func defaultRunnerLogPath(repoRoot string, taskID string, epicID string, backend string) string {
	if strings.TrimSpace(repoRoot) == "" || strings.TrimSpace(taskID) == "" {
		return ""
	}
	parts := []string{repoRoot, "runner-logs"}
	if epicID = strings.TrimSpace(epicID); epicID != "" {
		parts = append(parts, epicID)
	}
	parts = append(parts, strings.TrimSpace(taskID))
	parts = append(parts, runnerLogBackendDir(backend))
	parts = append(parts, taskID+".jsonl")
	return filepath.Join(parts...)
}

func ensureRunnerLogDirectory(repoRoot string, logPath string) error {
	if strings.TrimSpace(repoRoot) == "" {
		return nil
	}
	if _, err := os.Stat(repoRoot); err != nil {
		return nil
	}
	logPath = strings.TrimSpace(logPath)
	if logPath == "" {
		return nil
	}
	return os.MkdirAll(filepath.Dir(logPath), 0o755)
}

func runnerLogBackendDir(backend string) string {
	switch strings.TrimSpace(strings.ToLower(backend)) {
	case "codex":
		return "codex"
	case "claude":
		return "claude"
	case "kimi":
		return "kimi"
	default:
		return "opencode"
	}
}

func buildRunnerStartedMetadata(mode contracts.RunnerMode, backend string, model string, clonePath string, logPath string, startedAt time.Time) map[string]string {
	backendValue := strings.TrimSpace(strings.ToLower(backend))
	if backendValue == "" {
		backendValue = "opencode"
	}
	metadata := map[string]string{
		"backend":    backendValue,
		"mode":       string(mode),
		"started_at": startedAt.UTC().Format(time.RFC3339),
		"clone_path": clonePath,
		"log_path":   logPath,
		"model":      model,
	}
	return compactMetadata(metadata)
}

func reviewVerdictFromArtifacts(result contracts.RunnerResult) string {
	if len(result.Artifacts) == 0 {
		return ""
	}
	verdict := strings.ToLower(strings.TrimSpace(result.Artifacts["review_verdict"]))
	if verdict == "pass" || verdict == "fail" {
		return verdict
	}
	return ""
}

func reviewFailFeedbackFromArtifacts(result contracts.RunnerResult) string {
	if len(result.Artifacts) == 0 {
		return ""
	}
	for _, key := range []string{"review_fail_feedback", "review_feedback"} {
		value := strings.TrimSpace(result.Artifacts[key])
		if value != "" {
			return value
		}
	}
	return ""
}

func buildReviewFailReason(result contracts.RunnerResult) string {
	feedback := reviewFailFeedbackFromArtifacts(result)
	if feedback == "" {
		return "review verdict returned fail"
	}
	lower := strings.ToLower(feedback)
	if strings.HasPrefix(lower, "review rejected") {
		return feedback
	}
	return "review rejected: " + feedback
}

func resolveReviewFailureReason(reason string, retryMetadata map[string]string) string {
	trimmed := strings.TrimSpace(reason)
	if trimmed != "" && !strings.EqualFold(trimmed, "review verdict returned fail") {
		return trimmed
	}
	blockers := strings.TrimSpace(executor.ReviewRetryBlockersFromMetadata(retryMetadata))
	if blockers == "" {
		return trimmed
	}
	lower := strings.ToLower(blockers)
	if strings.HasPrefix(lower, "review rejected") {
		return blockers
	}
	return "review rejected: " + blockers
}

func appendReviewOutcomeMetadata(metadata map[string]string, result contracts.RunnerResult) map[string]string {
	if metadata == nil {
		metadata = map[string]string{}
	}
	if verdict := reviewVerdictFromArtifacts(result); verdict != "" {
		metadata["review_verdict"] = verdict
	}
	if feedback := reviewFailFeedbackFromArtifacts(result); feedback != "" {
		metadata["review_fail_feedback"] = feedback
	}
	return metadata
}

func appendDecisionMetadata(metadata map[string]string, decision string, reason string) map[string]string {
	if metadata == nil {
		metadata = map[string]string{}
	}
	if decision = strings.TrimSpace(decision); decision != "" {
		metadata["decision"] = decision
	}
	if reason = strings.TrimSpace(reason); reason != "" {
		metadata["reason"] = reason
	}
	return metadata
}

func buildRunnerFinishedMetadata(result contracts.RunnerResult) map[string]string {
	metadata := map[string]string{
		"status": string(result.Status),
	}
	if strings.TrimSpace(result.Reason) != "" {
		metadata["reason"] = result.Reason
	}
	if strings.TrimSpace(result.LogPath) != "" {
		metadata["log_path"] = result.LogPath
	}
	for key, value := range result.Artifacts {
		if strings.TrimSpace(value) == "" {
			continue
		}
		metadata[key] = value
	}
	return compactMetadata(metadata)
}

func compactMetadata(metadata map[string]string) map[string]string {
	if len(metadata) == 0 {
		return nil
	}
	filtered := make(map[string]string, len(metadata))
	for key, value := range metadata {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		filtered[key] = trimmed
	}
	if len(filtered) == 0 {
		return nil
	}
	return filtered
}

func (l *Loop) stopRequested() bool {
	if l.options.Stop == nil {
		return false
	}
	select {
	case <-l.options.Stop:
		return true
	default:
		return false
	}
}
