package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/scheduler"
	taskquality "github.com/egv/yolo-runner/v2/internal/task_quality"
	"github.com/egv/yolo-runner/v2/internal/tk"
)

const defaultQualityGateThreshold = 70

const (
	qualityGateToolTaskValidator     = "task_validator"
	qualityGateToolDependencyChecker = "dependency_checker"
	qcGateToolTestRunner             = "test_runner"
	qcGateToolLinter                 = "linter"
	qcGateToolCoverageChecker        = "coverage_checker"
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
		tasks:          tasks,
		runner:         runner,
		events:         events,
		options:        options,
		taskLock:       scheduler.NewTaskLock(),
		landingLock:    scheduler.NewLandingLock(),
		cloneManager:   options.CloneManager,
		schedulerState: newSchedulerStateStore(options.SchedulerStatePath, options.ParentID),
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
	}
}

func (l *Loop) runTask(ctx context.Context, taskID string, workerID int, queuePos int, taskPriority int) (summary contracts.LoopSummary, err error) {
	summary = contracts.LoopSummary{}
	worker := fmt.Sprintf("worker-%d", workerID)

	task, err := l.tasks.GetTask(ctx, taskID)
	if err != nil {
		return summary, err
	}
	metadata := taskMonitoringMetadata(task)
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
	if feedback := reviewRetryBlockersFromMetadata(task.Metadata); feedback != "" {
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
			Prompt: buildImplementPrompt(
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
				Prompt:   buildPrompt(task, contracts.RunnerModeReview, false),
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
					Prompt:   buildReviewVerdictPrompt(task),
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
						sha, err := taskVCS.CommitAll(ctx, autoLandingCommitMessage(task))
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
					"completion_retry_count":  fmt.Sprintf("%d", completionRetries),
					"completion_addendum":     completionAddendum,
					"triage_reason":           completionReason,
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
					"completion_retry_count":  fmt.Sprintf("%d", completionRetries),
					"completion_addendum":     completionAddendum,
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

func eventTypeForRunnerProgress(progressType string) contracts.EventType {
	switch strings.TrimSpace(progressType) {
	case "runner_cmd_started":
		return contracts.EventTypeRunnerCommandStarted
	case "runner_cmd_finished":
		return contracts.EventTypeRunnerCommandFinished
	case "runner_output":
		return contracts.EventTypeRunnerOutput
	case "runner_warning":
		return contracts.EventTypeRunnerWarning
	default:
		return contracts.EventTypeRunnerProgress
	}
}

func taskMonitoringMetadata(task contracts.Task) map[string]string {
	metadata := cloneStringMap(task.Metadata)
	if metadata == nil {
		metadata = map[string]string{}
	}
	parentID := strings.TrimSpace(task.ParentID)
	if parentID != "" {
		metadata["parent_id"] = parentID
	}
	if dependencies := strings.TrimSpace(metadata["dependencies"]); dependencies != "" {
		metadata["dependencies"] = dependencies
	}
	return metadata
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

func (l *Loop) emit(ctx context.Context, event contracts.Event) error {
	if l.events == nil {
		return nil
	}
	return l.events.Emit(ctx, event)
}

func (l *Loop) runRunnerWithMonitoring(ctx context.Context, request contracts.RunnerRequest, taskID string, taskTitle string, worker string, clonePath string, queuePos int) (contracts.RunnerResult, error) {
	heartbeatInterval := l.options.HeartbeatInterval
	if heartbeatInterval <= 0 {
		heartbeatInterval = 5 * time.Second
	}
	warningAfter := l.options.NoOutputWarningAfter
	if warningAfter <= 0 {
		warningAfter = 30 * time.Second
	}

	lastOutputAt := time.Now().UTC()
	warned := false
	var progressMu sync.Mutex

	request.OnProgress = func(progress contracts.RunnerProgress) {
		eventTime := progress.Timestamp
		if eventTime.IsZero() {
			eventTime = time.Now().UTC()
		}
		progressMu.Lock()
		lastOutputAt = eventTime
		warned = false
		progressMu.Unlock()
		_ = l.emit(ctx, contracts.Event{
			Type:      eventTypeForRunnerProgress(progress.Type),
			TaskID:    taskID,
			TaskTitle: taskTitle,
			WorkerID:  worker,
			ClonePath: clonePath,
			QueuePos:  queuePos,
			Message:   progress.Message,
			Metadata:  progress.Metadata,
			Timestamp: eventTime,
		})
	}

	monitorCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		ticker := time.NewTicker(heartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-monitorCtx.Done():
				return
			case now := <-ticker.C:
				progressMu.Lock()
				elapsed := now.Sub(lastOutputAt)
				alreadyWarned := warned
				if elapsed >= warningAfter {
					warned = true
				}
				progressMu.Unlock()

				_ = l.emit(ctx, contracts.Event{
					Type:      contracts.EventTypeRunnerHeartbeat,
					TaskID:    taskID,
					TaskTitle: taskTitle,
					WorkerID:  worker,
					ClonePath: clonePath,
					QueuePos:  queuePos,
					Message:   "alive",
					Metadata:  map[string]string{"last_output_age": elapsed.Round(time.Second).String()},
					Timestamp: now.UTC(),
				})

				if elapsed >= warningAfter && !alreadyWarned {
					_ = l.emit(ctx, contracts.Event{
						Type:      contracts.EventTypeRunnerWarning,
						TaskID:    taskID,
						TaskTitle: taskTitle,
						WorkerID:  worker,
						ClonePath: clonePath,
						QueuePos:  queuePos,
						Message:   "no output threshold exceeded",
						Metadata:  map[string]string{"last_output_age": elapsed.Round(time.Second).String()},
						Timestamp: now.UTC(),
					})
				}
			}
		}
	}()

	result, err := l.runner.Run(ctx, request)
	cancel()
	return result, err
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

func buildPrompt(task contracts.Task, mode contracts.RunnerMode, tddMode bool) string {
	modeLine := "Implementation"
	if mode == contracts.RunnerModeReview {
		modeLine = "Review"
	}
	sections := []string{
		"Mode: " + modeLine,
		"Task ID: " + task.ID,
		"Title: " + task.Title,
	}
	if mode == contracts.RunnerModeReview {
		sections = append(sections, strings.Join([]string{
			"Review Instructions:",
			"- Include exactly one verdict line in this format: REVIEW_VERDICT: pass OR REVIEW_VERDICT: fail",
			"- Use pass only when implementation satisfies acceptance criteria and tests.",
			"- If fail, include exactly one structured line: REVIEW_FAIL_FEEDBACK: <blocking gaps and required fixes>.",
		}, "\n"))
	} else {
		sections = append(sections, strings.Join([]string{
			"Command Contract:",
			"- Work only on this task; do not switch tasks.",
			"- Do not call task-selection/status tools (the runner owns task state).",
			"- Keep edits scoped to files required for this task.",
		}, "\n"))
		if tddMode {
			sections = append(sections, strings.Join([]string{
				"Strict TDD Workflow (Red-Green-Refactor):",
				"Tests-First Gate:",
				"- Confirm tests for the target behavior exist before implementation.",
				"- Run tests before changes and confirm they fail to define expected behavior.",
				"- Do not implement until tests-first gate is passing.",
				"1. RED: Add or update a test that fails for the target behavior.",
				"2. GREEN: Implement the minimal code required for that test to pass.",
				"3. REFACTOR: Improve the design while preserving passing tests.",
				"- Required sequence: test-first, targeted fail check, minimal green fix, then refactor.",
				"- Re-run targeted tests, then run broader relevant tests.",
				"- Stop only when all tests pass and acceptance criteria are covered.",
			}, "\n"))
		} else {
			sections = append(sections, strings.Join([]string{
				"Strict TDD Checklist:",
				"[ ] Add or update a test that fails for the target behavior.",
				"[ ] Run the targeted test and confirm it fails before implementation.",
				"[ ] Implement the minimal code change required for the test to pass.",
				"[ ] Re-run targeted tests, then run broader relevant tests.",
				"[ ] Stop only when all tests pass and acceptance criteria are covered.",
			}, "\n"))
		}
		if retryAttempt, blockers := reviewRetryPromptContext(task.Metadata); retryAttempt > 0 {
			retrySection := []string{
				"Retry Context:",
				fmt.Sprintf("- Review retry attempt: %d", retryAttempt),
				"Prior Review Blockers:",
			}
			if blockers != "" {
				retrySection = append(retrySection, "- "+blockers)
			} else {
				retrySection = append(retrySection, "- Previous review failed; address blockers before requesting review again.")
			}
			sections = append(sections, strings.Join(retrySection, "\n"))
		}
	}
	if strings.TrimSpace(task.Description) != "" {
		sections = append(sections, "Description:\n"+task.Description)
	}
	return strings.Join(sections, "\n\n")
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

func reviewRetryPromptContext(metadata map[string]string) (int, string) {
	if len(metadata) == 0 {
		return 0, ""
	}
	retryAttempt, err := strconv.Atoi(strings.TrimSpace(metadata["review_retry_count"]))
	if err != nil || retryAttempt <= 0 {
		return 0, ""
	}
	return retryAttempt, reviewRetryBlockersFromMetadata(metadata)
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

func taskQualityScore(metadata map[string]string) (int, bool) {
	raw := strings.TrimSpace(metadata["quality_score"])
	if raw == "" {
		return 0, false
	}
	score, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false
	}
	return score, true
}

func taskExecutionThresholdScore(metadata map[string]string) (int, bool) {
	if score, ok := taskCoverageScore(metadata); ok {
		return score, true
	}
	if score, ok := taskQualityScore(metadata); ok {
		return score, true
	}
	return 0, false
}

func (l *Loop) runQualityGate(ctx context.Context, task contracts.Task, worker string, queuePos int) (bool, error) {
	tools, err := resolveQualityGateTools(l.options.QualityGateTools)
	if err != nil {
		return false, err
	}

	if len(tools) > 0 {
		qualityThreshold := l.options.QualityGateThreshold
		if qualityThreshold <= 0 {
			qualityThreshold = defaultQualityGateThreshold
		}

		qualityScore, qualityIssues, ok, err := l.evaluateTaskQuality(ctx, task, tools)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}

		qualityMetadata := map[string]string{
			"quality_score":     strconv.Itoa(qualityScore),
			"quality_threshold": strconv.Itoa(qualityThreshold),
			"quality_gate":      "true",
			"quality_issues":    strings.Join(qualityIssues, "\n"),
		}
		qualityMetadata["quality_gate_comment"] = qualityGateComment(qualityMetadata, qualityScore, qualityThreshold)

		if qualityScore >= qualityThreshold {
			return false, nil
		}

		qualityGateReason := fmt.Sprintf("quality score %d is below threshold %d", qualityScore, qualityThreshold)
		if l.options.AllowLowQuality {
			warningMetadata := map[string]string{
				"quality_threshold": strconv.Itoa(qualityThreshold),
				"quality_score":     strconv.Itoa(qualityScore),
				"reason":            qualityGateReason,
			}
			for key, value := range qualityMetadata {
				warningMetadata[key] = value
			}
			_ = l.emit(ctx, contracts.Event{
				Type:      contracts.EventTypeRunnerWarning,
				TaskID:    task.ID,
				TaskTitle: task.Title,
				WorkerID:  worker,
				ClonePath: l.options.RepoRoot,
				QueuePos:  queuePos,
				Message:   "quality gate threshold overridden by --allow-low-quality",
				Metadata:  warningMetadata,
				Timestamp: time.Now().UTC(),
			})
			return false, nil
		}

		blockedData := map[string]string{
			"triage_status":        "blocked",
			"triage_reason":        qualityGateReason,
			"quality_score":        strconv.Itoa(qualityScore),
			"quality_threshold":    strconv.Itoa(qualityThreshold),
			"quality_gate":         "true",
			"quality_gate_comment": qualityMetadata["quality_gate_comment"],
		}
		for key, value := range qualityMetadata {
			blockedData[key] = value
		}
		blockedData = appendDecisionMetadata(blockedData, "blocked", qualityGateReason)
		if err := l.markTaskBlockedWithData(task.ID, blockedData); err != nil {
			return false, err
		}
		if err := l.tasks.SetTaskStatus(ctx, task.ID, contracts.TaskStatusBlocked); err != nil {
			return false, err
		}

		finishedMetadata := map[string]string{
			"triage_status":     "blocked",
			"triage_reason":     qualityGateReason,
			"quality_score":     strconv.Itoa(qualityScore),
			"quality_threshold": strconv.Itoa(qualityThreshold),
			"quality_gate":      "true",
		}
		finishedMetadata = appendDecisionMetadata(finishedMetadata, "blocked", qualityGateReason)
		_ = l.emit(ctx, contracts.Event{
			Type:      contracts.EventTypeTaskFinished,
			TaskID:    task.ID,
			TaskTitle: task.Title,
			WorkerID:  worker,
			ClonePath: l.options.RepoRoot,
			QueuePos:  queuePos,
			Message:   string(contracts.TaskStatusBlocked),
			Metadata:  finishedMetadata,
			Timestamp: time.Now().UTC(),
		})
		if err := l.tasks.SetTaskData(ctx, task.ID, blockedData); err != nil {
			return false, err
		}
		_ = l.emit(ctx, contracts.Event{
			Type:      contracts.EventTypeTaskDataUpdated,
			TaskID:    task.ID,
			TaskTitle: task.Title,
			WorkerID:  worker,
			ClonePath: l.options.RepoRoot,
			QueuePos:  queuePos,
			Metadata:  blockedData,
			Timestamp: time.Now().UTC(),
		})
		if err := l.clearTaskTerminalState(task.ID); err != nil {
			return false, err
		}
		return true, nil
	}

	qualityThreshold := l.options.QualityGateThreshold
	if qualityThreshold <= 0 {
		qualityThreshold = defaultQualityGateThreshold
	}

	qualityScore, ok := taskExecutionThresholdScore(task.Metadata)
	if !ok || qualityScore >= qualityThreshold {
		return false, nil
	}

	qualityGateReason := fmt.Sprintf("quality score %d is below threshold %d", qualityScore, qualityThreshold)
	qualityComment := qualityGateComment(task.Metadata, qualityScore, qualityThreshold)
	qualityMetadata := map[string]string{
		"quality_score":        strconv.Itoa(qualityScore),
		"quality_threshold":    strconv.Itoa(qualityThreshold),
		"quality_gate":         "true",
		"quality_gate_comment": qualityComment,
	}
	if l.options.AllowLowQuality {
		warningMetadata := map[string]string{
			"quality_threshold": strconv.Itoa(qualityThreshold),
			"quality_score":     strconv.Itoa(qualityScore),
			"reason":            qualityGateReason,
		}
		for key, value := range qualityMetadata {
			warningMetadata[key] = value
		}
		_ = l.emit(ctx, contracts.Event{
			Type:      contracts.EventTypeRunnerWarning,
			TaskID:    task.ID,
			TaskTitle: task.Title,
			WorkerID:  worker,
			ClonePath: l.options.RepoRoot,
			QueuePos:  queuePos,
			Message:   "quality gate threshold overridden by --allow-low-quality",
			Metadata:  warningMetadata,
			Timestamp: time.Now().UTC(),
		})
		return false, nil
	}

	blockedData := map[string]string{
		"triage_status":        "blocked",
		"triage_reason":        qualityGateReason,
		"quality_score":        strconv.Itoa(qualityScore),
		"quality_threshold":    strconv.Itoa(qualityThreshold),
		"quality_gate":         "true",
		"quality_gate_comment": qualityComment,
	}
	blockedData = appendDecisionMetadata(blockedData, "blocked", qualityGateReason)
	if err := l.markTaskBlockedWithData(task.ID, blockedData); err != nil {
		return false, err
	}
	if err := l.tasks.SetTaskStatus(ctx, task.ID, contracts.TaskStatusBlocked); err != nil {
		return false, err
	}
	finishedMetadata := map[string]string{
		"triage_status":     "blocked",
		"triage_reason":     qualityGateReason,
		"quality_score":     strconv.Itoa(qualityScore),
		"quality_threshold": strconv.Itoa(qualityThreshold),
		"quality_gate":      "true",
	}
	finishedMetadata = appendDecisionMetadata(finishedMetadata, "blocked", qualityGateReason)
	_ = l.emit(ctx, contracts.Event{
		Type:      contracts.EventTypeTaskFinished,
		TaskID:    task.ID,
		TaskTitle: task.Title,
		WorkerID:  worker,
		ClonePath: l.options.RepoRoot,
		QueuePos:  queuePos,
		Message:   string(contracts.TaskStatusBlocked),
		Metadata:  finishedMetadata,
		Timestamp: time.Now().UTC(),
	})
	if err := l.tasks.SetTaskData(ctx, task.ID, blockedData); err != nil {
		return false, err
	}
	_ = l.emit(ctx, contracts.Event{
		Type:      contracts.EventTypeTaskDataUpdated,
		TaskID:    task.ID,
		TaskTitle: task.Title,
		WorkerID:  worker,
		ClonePath: l.options.RepoRoot,
		QueuePos:  queuePos,
		Metadata:  blockedData,
		Timestamp: time.Now().UTC(),
	})
	if err := l.clearTaskTerminalState(task.ID); err != nil {
		return false, err
	}
	return true, nil
}

type qcGateToolResult struct {
	Tool      string `json:"tool"`
	Status    string `json:"status"`
	Passed    bool   `json:"passed"`
	Reason    string `json:"reason,omitempty"`
	Value     string `json:"value,omitempty"`
	Threshold int    `json:"threshold,omitempty"`
	Command   string `json:"command,omitempty"`
	Critical  bool   `json:"critical,omitempty"`
}

type qcGateReport struct {
	Status    string             `json:"status"`
	Tools     []qcGateToolResult `json:"tools"`
	Review    string             `json:"review_verdict,omitempty"`
	Threshold int                `json:"threshold"`
}

func (l *Loop) runQCGate(ctx context.Context, task contracts.Task, result contracts.RunnerResult, worker string, queuePos int, taskRepoRoot string) (bool, error) {
	tools, err := resolveQCGateTools(l.options.QCGateTools)
	if err != nil {
		return false, err
	}
	if len(tools) == 0 && !(l.options.RequireReview && result.ReviewReady) {
		return false, nil
	}

	repoRoot := strings.TrimSpace(taskRepoRoot)
	if repoRoot == "" {
		repoRoot = strings.TrimSpace(l.options.RepoRoot)
	}

	qcThreshold := l.options.QualityGateThreshold
	if qcThreshold <= 0 {
		qcThreshold = defaultQualityGateThreshold
	}

	outcomes := make([]qcGateToolResult, 0, len(tools)+1)
	failed := []string{}
	for _, tool := range tools {
		var outcome qcGateToolResult
		switch tool {
		case qcGateToolTestRunner:
			outcome = l.runQCTestSuiteValidation(ctx, repoRoot)
		case qcGateToolLinter:
			outcome = l.runQCLinterValidation(ctx, repoRoot)
		case qcGateToolCoverageChecker:
			outcome = l.runQCCoverageValidation(ctx, repoRoot, qcThreshold)
		default:
			return false, fmt.Errorf("unsupported quality control gate tool %q", tool)
		}
		outcomes = append(outcomes, outcome)
		if outcome.Critical {
			return false, fmt.Errorf("quality control gate tool %q failed critically: %s", tool, outcome.Reason)
		}
		if !outcome.Passed {
			failed = append(failed, outcome.Reason)
		}
	}

	reviewApproval := qcGateToolResult{}
	if l.options.RequireReview && result.ReviewReady {
		reviewApproval = l.runQCReviewApproval(result)
		outcomes = append(outcomes, reviewApproval)
		if !reviewApproval.Passed {
			failed = append(failed, reviewApproval.Reason)
		}
	}

	report := qcGateReport{
		Status:    "passed",
		Threshold: qcThreshold,
		Tools:     outcomes,
	}
	if reviewApproval.Tool != "" {
		report.Review = reviewApproval.Value
	}
	if len(failed) > 0 {
		report.Status = "failed"
	}

	reportJSON, err := json.Marshal(report)
	if err != nil {
		return false, fmt.Errorf("marshal quality control gate report: %w", err)
	}

	qcMetadata := map[string]string{
		"qc_gate":           "true",
		"qc_gate_status":    report.Status,
		"qc_gate_threshold": strconv.Itoa(qcThreshold),
		"qc_gate_tools":     strings.Join(tools, ","),
		"qc_gate_report":    string(reportJSON),
	}
	for _, outcome := range outcomes {
		keyPrefix := "qc_" + strings.ReplaceAll(strings.ReplaceAll(outcome.Tool, "-", "_"), " ", "_")
		qcMetadata[keyPrefix+"_status"] = outcome.Status
		if strings.TrimSpace(outcome.Value) != "" {
			qcMetadata[keyPrefix+"_value"] = outcome.Value
		}
		if strings.TrimSpace(outcome.Reason) != "" {
			qcMetadata[keyPrefix+"_reason"] = outcome.Reason
		}
	}

	if len(failed) == 0 {
		if err := l.tasks.SetTaskData(ctx, task.ID, qcMetadata); err != nil {
			return false, err
		}
		_ = l.emit(ctx, contracts.Event{
			Type:      contracts.EventTypeTaskDataUpdated,
			TaskID:    task.ID,
			TaskTitle: task.Title,
			WorkerID:  worker,
			ClonePath: taskRepoRoot,
			QueuePos:  queuePos,
			Metadata:  qcMetadata,
			Timestamp: time.Now().UTC(),
		})
		return false, nil
	}

	blockedReason := "quality control gate failed: " + strings.Join(failed, "; ")
	blockedData := map[string]string{
		"triage_status":     "blocked",
		"triage_reason":     blockedReason,
		"qc_gate":           "true",
		"qc_gate_status":    report.Status,
		"qc_gate_threshold": strconv.Itoa(qcThreshold),
		"qc_gate_tools":     strings.Join(tools, ","),
		"qc_gate_report":    string(reportJSON),
	}
	blockedData = appendDecisionMetadata(blockedData, "blocked", blockedReason)
	for key, value := range qcMetadata {
		blockedData[key] = value
	}
	if err := l.markTaskBlockedWithData(task.ID, blockedData); err != nil {
		return false, err
	}
	if err := l.tasks.SetTaskStatus(ctx, task.ID, contracts.TaskStatusBlocked); err != nil {
		return false, err
	}
	finishedMetadata := map[string]string{
		"triage_status":     "blocked",
		"triage_reason":     blockedReason,
		"qc_gate":           "true",
		"qc_gate_status":    report.Status,
		"qc_gate_threshold": strconv.Itoa(qcThreshold),
		"qc_gate_tools":     strings.Join(tools, ","),
	}
	finishedMetadata = appendDecisionMetadata(finishedMetadata, "blocked", blockedReason)
	for key, value := range qcMetadata {
		finishedMetadata[key] = value
	}
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
		return false, err
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
		return false, err
	}
	return true, nil
}

func (l *Loop) runQCTestSuiteValidation(ctx context.Context, repoRoot string) qcGateToolResult {
	output, err := runQCGateCommand(ctx, repoRoot, "go", "test", "./...")
	result := qcGateToolResult{
		Tool:    qcGateToolTestRunner,
		Command: "go test ./...",
	}
	if err == nil {
		result.Passed = true
		result.Status = "passed"
		result.Value = "passed"
		return result
	}
	if _, ok := err.(*exec.ExitError); ok {
		result.Passed = false
		result.Status = "failed"
		result.Reason = strings.TrimSpace(firstNonEmptyLine(output))
		if result.Reason == "" {
			result.Reason = "test suite returned non-zero status"
		}
		return result
	}
	result.Critical = true
	result.Status = "critical_error"
	result.Reason = strings.TrimSpace(err.Error())
	if result.Reason == "" {
		result.Reason = "unable to execute test suite command"
	}
	return result
}

func (l *Loop) runQCLinterValidation(ctx context.Context, repoRoot string) qcGateToolResult {
	output, err := runQCGateCommand(ctx, repoRoot, "go", "vet", "./...")
	result := qcGateToolResult{
		Tool:    qcGateToolLinter,
		Command: "go vet ./...",
	}
	if err == nil {
		result.Passed = true
		result.Status = "passed"
		result.Value = "passed"
		return result
	}
	if _, ok := err.(*exec.ExitError); ok {
		result.Passed = false
		result.Status = "failed"
		result.Reason = strings.TrimSpace(firstNonEmptyLine(output))
		if result.Reason == "" {
			result.Reason = "linter returned non-zero status"
		}
		return result
	}
	result.Critical = true
	result.Status = "critical_error"
	result.Reason = strings.TrimSpace(err.Error())
	if result.Reason == "" {
		result.Reason = "unable to execute linter command"
	}
	return result
}

func (l *Loop) runQCCoverageValidation(ctx context.Context, repoRoot string, threshold int) qcGateToolResult {
	profileFile, err := os.CreateTemp("", "yolo-runner-qc-coverage-*.out")
	if err != nil {
		return qcGateToolResult{
			Tool:     qcGateToolCoverageChecker,
			Status:   "critical_error",
			Critical: true,
			Reason:   "failed to create coverage profile temp file",
		}
	}
	profilePath := profileFile.Name()
	_ = profileFile.Close()
	defer func() {
		_ = os.Remove(profilePath)
	}()

	_, runErr := runQCGateCommand(ctx, repoRoot, "go", "test", "./...", "-coverprofile="+profilePath)
	result := qcGateToolResult{
		Tool:      qcGateToolCoverageChecker,
		Command:   "go test ./... -coverprofile=<tmp>",
		Threshold: threshold,
	}
	if runErr != nil {
		if _, ok := runErr.(*exec.ExitError); ok {
			result.Passed = false
			result.Status = "failed"
			result.Reason = "coverage test execution failed"
			return result
		}
		result.Critical = true
		result.Status = "critical_error"
		result.Reason = strings.TrimSpace(runErr.Error())
		if result.Reason == "" {
			result.Reason = "unable to execute coverage test command"
		}
		return result
	}

	coverageOutput, err := runQCGateCommand(ctx, repoRoot, "go", "tool", "cover", "-func="+profilePath)
	if err != nil {
		result.Critical = true
		result.Status = "critical_error"
		result.Reason = "failed to collect coverage report"
		return result
	}

	coverage, parseErr := parseCoveragePercentFromReport(coverageOutput)
	if parseErr != nil {
		result.Critical = true
		result.Status = "critical_error"
		result.Reason = parseErr.Error()
		return result
	}
	result.Value = strconv.FormatFloat(coverage, 'f', 1, 64)
	if coverage < float64(threshold) {
		result.Passed = false
		result.Status = "failed"
		result.Reason = fmt.Sprintf("coverage %.1f is below threshold %d", coverage, threshold)
		return result
	}
	result.Passed = true
	result.Status = "passed"
	result.Value = fmt.Sprintf("%.1f", coverage)
	return result
}

func (l *Loop) runQCReviewApproval(result contracts.RunnerResult) qcGateToolResult {
	outcome := qcGateToolResult{
		Tool:    "review_approval",
		Command: "review approval check",
	}
	if !l.options.RequireReview {
		outcome.Status = "not_required"
		outcome.Passed = true
		outcome.Reason = "not_required"
		outcome.Value = "skipped"
		return outcome
	}

	if result.Status != contracts.RunnerResultCompleted {
		outcome.Status = "failed"
		outcome.Passed = false
		outcome.Value = "not_approved"
		outcome.Reason = "review result was not completed"
		return outcome
	}
	if !result.ReviewReady {
		outcome.Status = "failed"
		outcome.Passed = false
		outcome.Value = "not_approved"
		outcome.Reason = "review not ready"
		return outcome
	}

	verdict := reviewVerdictFromArtifacts(result)
	if verdict == "pass" {
		outcome.Status = "passed"
		outcome.Passed = true
		outcome.Value = "pass"
		return outcome
	}
	if verdict == "fail" {
		outcome.Status = "failed"
		outcome.Passed = false
		outcome.Value = "fail"
		if feedback := strings.TrimSpace(reviewFailFeedbackFromArtifacts(result)); feedback != "" {
			outcome.Reason = feedback
		} else {
			outcome.Reason = "review verdict returned fail"
		}
		return outcome
	}
	outcome.Status = "failed"
	outcome.Passed = false
	outcome.Value = "not_approved"
	if feedback := strings.TrimSpace(reviewFailFeedbackFromArtifacts(result)); feedback != "" {
		outcome.Reason = feedback
		return outcome
	}
	outcome.Reason = "review verdict missing"
	return outcome
}

func runQCGateCommand(ctx context.Context, repoRoot string, command string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	if strings.TrimSpace(repoRoot) != "" {
		cmd.Dir = strings.TrimSpace(repoRoot)
	}
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func parseCoveragePercentFromReport(rawReport string) (float64, error) {
	for _, line := range strings.Split(rawReport, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "total:") {
			continue
		}
		for _, field := range strings.Fields(line) {
			if !strings.HasSuffix(field, "%") {
				continue
			}
			value := strings.TrimSuffix(strings.TrimSpace(field), "%")
			if value == "" {
				continue
			}
			score, err := strconv.ParseFloat(value, 64)
			if err != nil {
				continue
			}
			return score, nil
		}
	}
	return 0, fmt.Errorf("coverage report is missing total percentage line")
}

func firstNonEmptyLine(raw string) string {
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

func resolveQCGateTools(rawTools []string) ([]string, error) {
	if len(rawTools) == 0 {
		return nil, nil
	}

	tools := make([]string, 0, len(rawTools))
	seen := map[string]struct{}{}
	for _, tool := range rawTools {
		tool = strings.ToLower(strings.TrimSpace(tool))
		if tool == "" {
			continue
		}
		switch tool {
		case qcGateToolTestRunner, qcGateToolLinter, qcGateToolCoverageChecker:
		default:
			return nil, fmt.Errorf("unsupported quality control gate tool %q", tool)
		}
		if _, exists := seen[tool]; exists {
			continue
		}
		seen[tool] = struct{}{}
		tools = append(tools, tool)
	}
	return tools, nil
}

func (l *Loop) evaluateTaskQuality(ctx context.Context, task contracts.Task, tools []string) (int, []string, bool, error) {
	qualityScore, hasScore := 0, false
	issues := []string{}

	if score, ok := taskQualityScore(task.Metadata); ok {
		qualityScore = score
		hasScore = true
		issues = append(issues, qualityGateIssues(task.Metadata)...)
	}

	if hasQualityTool(tools, qualityGateToolTaskValidator) {
		qualityInput := parseTaskQualityInput(task)
		qualityAssessment := taskquality.AssessTaskQuality(qualityInput)
		qualityScore = qualityAssessment.Score
		hasScore = true
		issues = append(issues, qualityAssessment.Issues...)
	}

	if hasQualityTool(tools, qualityGateToolDependencyChecker) {
		missingDependencies, dependencyIssues, err := l.evaluateTaskDependencies(ctx, task.Metadata["dependencies"], task.ID)
		if err != nil {
			return 0, nil, false, err
		}
		issues = append(issues, dependencyIssues...)
		if !hasScore {
			qualityScore = 100
			hasScore = true
		}
		qualityScore -= missingDependencies * 20
	}

	if !hasScore {
		return 0, nil, false, nil
	}
	if qualityScore < 0 {
		qualityScore = 0
	}
	issues = dedupeQualityIssues(issues)
	return qualityScore, issues, true, nil
}

func (l *Loop) evaluateTaskDependencies(ctx context.Context, rawDependencies string, taskID string) (int, []string, error) {
	dependencies := parseTaskDependencies(rawDependencies)
	if len(dependencies) == 0 {
		return 0, nil, nil
	}

	missingDependencies := 0
	issues := []string{}
	for _, dependencyID := range dependencies {
		if strings.TrimSpace(dependencyID) == "" || dependencyID == strings.TrimSpace(taskID) {
			continue
		}
		dependency, err := l.tasks.GetTask(ctx, dependencyID)
		if err != nil || strings.TrimSpace(dependency.ID) == "" {
			missingDependencies++
			issues = append(issues, "dependency not resolvable: "+dependencyID)
		}
	}
	return missingDependencies, issues, nil
}

func parseTaskDependencies(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}

	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\t' || r == ' '
	})
	seen := map[string]struct{}{}
	dependencies := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		dependencies = append(dependencies, part)
	}
	return dependencies
}

func resolveQualityGateTools(rawTools []string) ([]string, error) {
	if len(rawTools) == 0 {
		return nil, nil
	}

	tools := make([]string, 0, len(rawTools))
	seen := map[string]struct{}{}
	for _, tool := range rawTools {
		tool = strings.ToLower(strings.TrimSpace(tool))
		if tool == "" {
			continue
		}
		switch tool {
		case qualityGateToolTaskValidator, qualityGateToolDependencyChecker:
		default:
			return nil, fmt.Errorf("unsupported quality gate tool %q", tool)
		}
		if _, exists := seen[tool]; exists {
			continue
		}
		seen[tool] = struct{}{}
		tools = append(tools, tool)
	}
	return tools, nil
}

func hasQualityTool(tools []string, expected string) bool {
	for _, tool := range tools {
		if strings.EqualFold(strings.TrimSpace(tool), expected) {
			return true
		}
	}
	return false
}

func parseTaskQualityInput(task contracts.Task) taskquality.TaskInput {
	body := stripTaskFrontmatter(task.Description)
	sections := parseQualitySections(body)
	description := strings.TrimSpace(sections["description"])
	if description == "" {
		description = strings.TrimSpace(body)
	}

	dependenciesContext := strings.TrimSpace(sections["dependencies_context"])
	if dependenciesContext == "" {
		dependenciesContext = strings.TrimSpace(task.Metadata["dependencies"])
		if dependenciesContext != "" {
			dependenciesContext = "Dependencies: " + dependenciesContext
		}
	}

	return taskquality.TaskInput{
		Title:               strings.TrimSpace(task.Title),
		Description:         description,
		AcceptanceCriteria:  strings.TrimSpace(sections["acceptance_criteria"]),
		Deliverables:        strings.TrimSpace(sections["deliverables"]),
		TestingPlan:         strings.TrimSpace(sections["testing_plan"]),
		DefinitionOfDone:    strings.TrimSpace(sections["definition_of_done"]),
		DependenciesContext: dependenciesContext,
	}
}

func parseQualitySections(raw string) map[string]string {
	sections := map[string]string{
		"description":          "",
		"acceptance_criteria":  "",
		"deliverables":         "",
		"testing_plan":         "",
		"definition_of_done":   "",
		"dependencies_context": "",
	}
	current := "description"
	for _, line := range strings.Split(raw, "\n") {
		if section, ok := parseQualitySectionHeader(line); ok {
			current = section
			continue
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		sections[current] = strings.TrimSpace(sections[current] + "\n" + line)
	}
	for key, value := range sections {
		sections[key] = strings.TrimSpace(value)
	}
	return sections
}

func parseQualitySectionHeader(line string) (string, bool) {
	candidate := strings.TrimSpace(strings.TrimPrefix(strings.TrimLeft(line, "#"), " "))
	candidate = strings.TrimSpace(strings.TrimSuffix(candidate, ":"))
	candidate = strings.TrimSpace(candidate)
	switch strings.ToLower(candidate) {
	case "description":
		return "description", true
	case "acceptance criteria", "acceptance":
		return "acceptance_criteria", true
	case "deliverables":
		return "deliverables", true
	case "testing plan", "testing":
		return "testing_plan", true
	case "definition of done", "definition":
		return "definition_of_done", true
	case "dependencies", "dependencies/context", "dependencies and context":
		return "dependencies_context", true
	default:
		return "", false
	}
}

func stripTaskFrontmatter(raw string) string {
	raw = strings.TrimLeft(raw, "\r\n\t ")
	lines := strings.Split(raw, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return strings.TrimSpace(raw)
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			return strings.TrimSpace(strings.Join(lines[i+1:], "\n"))
		}
	}
	return strings.TrimSpace(raw)
}

func dedupeQualityIssues(issues []string) []string {
	seen := map[string]struct{}{}
	unique := make([]string, 0, len(issues))
	for _, issue := range issues {
		issue = strings.TrimSpace(issue)
		if issue == "" {
			continue
		}
		if _, ok := seen[issue]; ok {
			continue
		}
		seen[issue] = struct{}{}
		unique = append(unique, issue)
	}
	return unique
}

func taskCoverageScore(metadata map[string]string) (int, bool) {
	raw := strings.TrimSpace(metadata["coverage"])
	if raw == "" {
		return 0, false
	}
	score, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false
	}
	return score, true
}

func qualityGateComment(metadata map[string]string, score int, threshold int) string {
	parts := []string{
		fmt.Sprintf("quality score %d is below threshold %d", score, threshold),
		"",
	}

	issues := qualityGateIssues(metadata)
	if len(issues) == 0 {
		return strings.Join(append(parts, "Please update the task to address these issues and rerun validation."), "\n")
	}

	formattedIssues := make([]string, 0, len(issues))
	for _, issue := range issues {
		formattedIssues = append(formattedIssues, "- "+issue)
	}
	insertAt := len(parts) - 1
	parts = append(parts[:insertAt], append([]string{"Quality issues:"}, formattedIssues...)...)
	parts = append(parts, "Please update the task to address these issues and rerun validation.")
	return strings.Join(parts, "\n")
}

func qualityGateIssues(metadata map[string]string) []string {
	raw := strings.TrimSpace(metadata["quality_issues"])
	if raw == "" {
		return nil
	}
	lines := strings.Split(raw, "\n")
	issues := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		trimmed = strings.TrimPrefix(trimmed, "- ")
		trimmed = strings.TrimPrefix(trimmed, "* ")
		trimmed = strings.TrimSpace(trimmed)
		if trimmed == "" {
			continue
		}
		issues = append(issues, trimmed)
	}
	return issues
}

func reviewRetryBlockersFromMetadata(metadata map[string]string) string {
	if len(metadata) == 0 {
		return ""
	}
	for _, key := range []string{"review_fail_feedback", "review_feedback", "triage_reason"} {
		if blocker := strings.TrimSpace(metadata[key]); blocker != "" {
			return blocker
		}
	}
	return ""
}

func buildImplementPrompt(task contracts.Task, reviewFeedback string, reviewRetryCount int, completionFeedback string, completionRetryCount int, tddMode bool) string {
	prompt := buildPrompt(task, contracts.RunnerModeImplement, tddMode)
	feedback := strings.TrimSpace(reviewFeedback)
	if feedback != "" && reviewRetryCount > 0 {
		prompt = strings.Join([]string{
			prompt,
			strings.Join([]string{
				fmt.Sprintf("Review Remediation Loop: Attempt %d", reviewRetryCount),
				"A previous review run failed. Address all blocking review comments before requesting review again.",
				"REVIEW_FAIL_FEEDBACK:",
				feedback,
			}, "\n"),
		}, "\n\n")
	}

	completionFeedback = strings.TrimSpace(completionFeedback)
	if completionFeedback != "" && completionRetryCount > 0 {
		prompt = strings.Join([]string{
			prompt,
			strings.Join([]string{
				fmt.Sprintf("Completion Remediation Loop: Attempt %d", completionRetryCount),
				"REMEDIATION_ADDENDUM:",
				completionFeedback,
			}, "\n"),
		}, "\n\n")
	}
	return prompt
}

func buildMergeConflictRemediationPrompt(task contracts.Task, taskBranch string, mergeFailureReason string) string {
	base := buildImplementPrompt(task, "", 0, "", 0, false)
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

func autoLandingCommitMessage(task contracts.Task) string {
	taskID := strings.TrimSpace(task.ID)
	if taskID == "" {
		return "chore(task): auto-commit before landing"
	}
	return fmt.Sprintf("chore(task): auto-commit before landing %s", taskID)
}

func buildReviewVerdictPrompt(task contracts.Task) string {
	sections := []string{
		"Mode: Review",
		"Task ID: " + task.ID,
		"Title: " + task.Title,
		"Verdict-only follow-up:",
		"- Your previous review did not include the required structured verdict.",
		"- Respond with exactly one line and no extra text:",
		"REVIEW_VERDICT: pass",
		"or",
		"REVIEW_VERDICT: fail",
	}
	return strings.Join(sections, "\n")
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
	blockers := strings.TrimSpace(reviewRetryBlockersFromMetadata(retryMetadata))
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
