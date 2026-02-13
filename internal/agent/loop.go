package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/anomalyco/yolo-runner/internal/contracts"
	"github.com/anomalyco/yolo-runner/internal/scheduler"
)

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
	RunnerTimeout        time.Duration
	WatchdogTimeout      time.Duration
	WatchdogInterval     time.Duration
	HeartbeatInterval    time.Duration
	NoOutputWarningAfter time.Duration
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

func (l *Loop) Run(ctx context.Context) (contracts.LoopSummary, error) {
	summary := contracts.LoopSummary{}
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
		summary  contracts.LoopSummary
		err      error
	}
	type taskJob struct {
		taskID   string
		queuePos int
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
				func(taskID string, queuePos int) {
					defer func() {
						if l.taskLock != nil {
							l.taskLock.Unlock(taskID)
						}
					}()
					resultSummary, taskErr := l.runTask(ctx, taskID, id, queuePos)
					results <- taskResult{taskID: taskID, workerID: id, queuePos: queuePos, summary: resultSummary, err: taskErr}
				}(job.taskID, job.queuePos)
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
			for _, candidate := range next {
				if _, running := inFlight[candidate.ID]; !running {
					if l.taskLock != nil && !l.taskLock.TryLock(candidate.ID) {
						continue
					}
					taskID = candidate.ID
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
			tasksCh <- taskJob{taskID: taskID, queuePos: queueCounter}
		}

		if len(inFlight) == 0 {
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

func (l *Loop) runTask(ctx context.Context, taskID string, workerID int, queuePos int) (summary contracts.LoopSummary, err error) {
	summary = contracts.LoopSummary{}
	worker := fmt.Sprintf("worker-%d", workerID)

	task, err := l.tasks.GetTask(ctx, taskID)
	if err != nil {
		return summary, err
	}
	_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeTaskStarted, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, QueuePos: queuePos, Message: task.Title, Timestamp: time.Now().UTC()})

	taskRepoRoot := l.options.RepoRoot
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

	retries := 0
	for {
		if err := l.tasks.SetTaskStatus(ctx, task.ID, contracts.TaskStatusInProgress); err != nil {
			return summary, err
		}

		implementLogPath := defaultRunnerLogPath(taskRepoRoot, task.ID, l.options.Backend)
		implementStartMeta := buildRunnerStartedMetadata(contracts.RunnerModeImplement, l.options.Backend, l.options.Model, taskRepoRoot, implementLogPath, time.Now().UTC())
		_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeRunnerStarted, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Message: string(contracts.RunnerModeImplement), Metadata: implementStartMeta, Timestamp: time.Now().UTC()})
		requestMetadata := map[string]string{"log_path": implementLogPath, "clone_path": taskRepoRoot}
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
			Model:    l.options.Model,
			Timeout:  l.options.RunnerTimeout,
			Prompt:   buildPrompt(task, contracts.RunnerModeImplement),
			Metadata: requestMetadata,
		}, task.ID, task.Title, worker, taskRepoRoot, queuePos)
		if err != nil {
			return summary, err
		}
		_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeRunnerFinished, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Message: string(result.Status), Metadata: buildRunnerFinishedMetadata(result), Timestamp: time.Now().UTC()})

		if result.Status == contracts.RunnerResultCompleted && l.options.RequireReview {
			_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeReviewStarted, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Timestamp: time.Now().UTC()})
			reviewLogPath := defaultRunnerLogPath(taskRepoRoot, task.ID, l.options.Backend)
			reviewStartMeta := buildRunnerStartedMetadata(contracts.RunnerModeReview, l.options.Backend, l.options.Model, taskRepoRoot, reviewLogPath, time.Now().UTC())
			_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeRunnerStarted, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Message: string(contracts.RunnerModeReview), Metadata: reviewStartMeta, Timestamp: time.Now().UTC()})
			reviewMetadata := map[string]string{"log_path": reviewLogPath, "clone_path": taskRepoRoot}
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
				Model:    l.options.Model,
				Timeout:  l.options.RunnerTimeout,
				Prompt:   buildPrompt(task, contracts.RunnerModeReview),
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
				verdictStartMeta := buildRunnerStartedMetadata(contracts.RunnerModeReview, l.options.Backend, l.options.Model, taskRepoRoot, reviewLogPath, time.Now().UTC())
				verdictStartMeta["review_phase"] = "verdict_retry"
				_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeRunnerStarted, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Message: string(contracts.RunnerModeReview), Metadata: verdictStartMeta, Timestamp: time.Now().UTC()})

				verdictResult, verdictErr := l.runRunnerWithMonitoring(ctx, contracts.RunnerRequest{
					TaskID:   task.ID,
					ParentID: l.options.ParentID,
					Mode:     contracts.RunnerModeReview,
					RepoRoot: taskRepoRoot,
					Model:    l.options.Model,
					Timeout:  l.options.RunnerTimeout,
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
					finalReviewResult.Reason = "review verdict returned fail"
				} else {
					finalReviewResult.Reason = "review verdict missing explicit pass"
				}
			}
			reviewFinishedMetadata := map[string]string{}
			if strings.TrimSpace(finalReviewResult.Reason) != "" {
				reviewFinishedMetadata["reason"] = strings.TrimSpace(finalReviewResult.Reason)
			}
			if verdict := reviewVerdictFromArtifacts(finalReviewResult); verdict != "" {
				reviewFinishedMetadata["review_verdict"] = verdict
			}
			if len(reviewFinishedMetadata) == 0 {
				reviewFinishedMetadata = nil
			}
			_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeReviewFinished, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Message: string(finalReviewResult.Status), Metadata: reviewFinishedMetadata, Timestamp: time.Now().UTC()})
			if finalReviewResult.Status != contracts.RunnerResultCompleted {
				result = finalReviewResult
			}
		}

		switch result.Status {
		case contracts.RunnerResultCompleted:
			if err := l.markTaskCompleted(task.ID); err != nil {
				return summary, err
			}
			if l.options.MergeOnSuccess && taskVCS != nil && taskBranch != "" {
				landingState := scheduler.NewLandingQueueStateMachine(2)
				_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeTaskDataUpdated, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Metadata: map[string]string{"landing_status": string(landingState.State())}, Timestamp: time.Now().UTC()})
				if l.landingLock != nil {
					l.landingLock.Lock()
					defer l.landingLock.Unlock()
				}
				landingBlocked := false
				landingReason := ""
				for attempt := 1; attempt <= 2; attempt++ {
					_ = landingState.Apply(scheduler.LandingEventBegin)
					_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeTaskDataUpdated, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Metadata: map[string]string{"landing_status": string(landingState.State()), "landing_attempt": fmt.Sprintf("%d", attempt)}, Timestamp: time.Now().UTC()})

					if err := taskVCS.MergeToMain(ctx, taskBranch); err != nil {
						landingReason = err.Error()
						_ = landingState.Apply(scheduler.LandingEventFailedRetryable)
						_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeTaskDataUpdated, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Metadata: map[string]string{"landing_status": string(landingState.State()), "triage_reason": landingReason}, Timestamp: time.Now().UTC()})
						if attempt < 2 {
							_ = landingState.Apply(scheduler.LandingEventRequeued)
							_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeTaskDataUpdated, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Metadata: map[string]string{"landing_status": string(landingState.State())}, Timestamp: time.Now().UTC()})
							continue
						}
						landingBlocked = true
						break
					}

					_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeMergeCompleted, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Message: taskBranch, Timestamp: time.Now().UTC()})
					if err := taskVCS.PushMain(ctx); err != nil {
						landingReason = err.Error()
						_ = landingState.Apply(scheduler.LandingEventFailedPermanent)
						_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeTaskDataUpdated, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Metadata: map[string]string{"landing_status": string(landingState.State()), "triage_reason": landingReason}, Timestamp: time.Now().UTC()})
						landingBlocked = true
						break
					}
					_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypePushCompleted, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Timestamp: time.Now().UTC()})
					_ = landingState.Apply(scheduler.LandingEventSucceeded)
					_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeTaskDataUpdated, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Metadata: map[string]string{"landing_status": string(landingState.State())}, Timestamp: time.Now().UTC()})
					break
				}

				if landingBlocked {
					blockedData := map[string]string{"triage_status": "blocked", "landing_status": string(landingState.State())}
					if landingReason != "" {
						blockedData["triage_reason"] = landingReason
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
			retries++
			if retries <= l.options.MaxRetries {
				if err := l.tasks.SetTaskData(ctx, task.ID, map[string]string{"retry_count": fmt.Sprintf("%d", retries)}); err != nil {
					return summary, err
				}
				if err := l.tasks.SetTaskStatus(ctx, task.ID, contracts.TaskStatusOpen); err != nil {
					return summary, err
				}
				continue
			}
			failedData := map[string]string{"triage_status": "failed"}
			if result.Reason != "" {
				failedData["triage_reason"] = result.Reason
			}
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
			_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeTaskFinished, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Message: string(contracts.TaskStatusFailed), Metadata: finishedMetadata, Timestamp: time.Now().UTC()})
			summary.Failed++
			return summary, nil
		default:
			failedData := map[string]string{"triage_status": "failed"}
			if result.Reason != "" {
				failedData["triage_reason"] = result.Reason
			}
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

func buildPrompt(task contracts.Task, mode contracts.RunnerMode) string {
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
			"- If fail, explain the blocking gaps and required fixes.",
		}, "\n"))
	}
	if strings.TrimSpace(task.Description) != "" {
		sections = append(sections, "Description:\n"+task.Description)
	}
	return strings.Join(sections, "\n\n")
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

func defaultRunnerLogPath(repoRoot string, taskID string, backend string) string {
	if strings.TrimSpace(repoRoot) == "" || strings.TrimSpace(taskID) == "" {
		return ""
	}
	return filepath.Join(repoRoot, "runner-logs", runnerLogBackendDir(backend), taskID+".jsonl")
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
