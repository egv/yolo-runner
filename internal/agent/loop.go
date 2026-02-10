package agent

import (
	"context"
	"fmt"
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

type LoopOptions struct {
	ParentID             string
	MaxRetries           int
	MaxTasks             int
	Concurrency          int
	SchedulerStatePath   string
	DryRun               bool
	Stop                 <-chan struct{}
	RepoRoot             string
	Model                string
	RunnerTimeout        time.Duration
	HeartbeatInterval    time.Duration
	NoOutputWarningAfter time.Duration
	VCS                  contracts.VCS
	RequireReview        bool
	MergeOnSuccess       bool
	CloneManager         CloneManager
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
	if l.options.VCS != nil {
		if err := l.options.VCS.EnsureMain(ctx); err != nil {
			return summary, err
		}
		branch, err := l.options.VCS.CreateTaskBranch(ctx, task.ID)
		if err != nil {
			return summary, err
		}
		taskBranch = branch
		if err := l.options.VCS.Checkout(ctx, branch); err != nil {
			return summary, err
		}
	}

	retries := 0
	for {
		if err := l.tasks.SetTaskStatus(ctx, task.ID, contracts.TaskStatusInProgress); err != nil {
			return summary, err
		}

		_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeRunnerStarted, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Message: string(contracts.RunnerModeImplement), Timestamp: time.Now().UTC()})
		result, err := l.runRunnerWithMonitoring(ctx, contracts.RunnerRequest{
			TaskID:   task.ID,
			ParentID: l.options.ParentID,
			Mode:     contracts.RunnerModeImplement,
			RepoRoot: taskRepoRoot,
			Model:    l.options.Model,
			Timeout:  l.options.RunnerTimeout,
			Prompt:   buildPrompt(task, contracts.RunnerModeImplement),
		}, task.ID, task.Title, worker, taskRepoRoot, queuePos)
		if err != nil {
			return summary, err
		}
		_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeRunnerFinished, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Message: string(result.Status), Timestamp: time.Now().UTC()})

		if result.Status == contracts.RunnerResultCompleted && l.options.RequireReview {
			_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeReviewStarted, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Timestamp: time.Now().UTC()})
			reviewResult, reviewErr := l.runRunnerWithMonitoring(ctx, contracts.RunnerRequest{
				TaskID:   task.ID,
				ParentID: l.options.ParentID,
				Mode:     contracts.RunnerModeReview,
				RepoRoot: taskRepoRoot,
				Model:    l.options.Model,
				Timeout:  l.options.RunnerTimeout,
				Prompt:   buildPrompt(task, contracts.RunnerModeReview),
			}, task.ID, task.Title, worker, taskRepoRoot, queuePos)
			if reviewErr != nil {
				return summary, reviewErr
			}
			_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeReviewFinished, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Message: string(reviewResult.Status), Timestamp: time.Now().UTC()})
			if reviewResult.Status == contracts.RunnerResultCompleted && !reviewResult.ReviewReady {
				reviewResult.Status = contracts.RunnerResultFailed
				reviewResult.Reason = "review verdict missing explicit pass"
			}
			if reviewResult.Status != contracts.RunnerResultCompleted {
				result = reviewResult
			}
		}

		switch result.Status {
		case contracts.RunnerResultCompleted:
			if err := l.markTaskCompleted(task.ID); err != nil {
				return summary, err
			}
			if l.options.MergeOnSuccess && l.options.VCS != nil && taskBranch != "" {
				landingState := scheduler.NewLandingQueueStateMachine(1)
				_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeTaskDataUpdated, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Metadata: map[string]string{"landing_status": string(landingState.State())}, Timestamp: time.Now().UTC()})
				_ = landingState.Apply(scheduler.LandingEventBegin)
				_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeTaskDataUpdated, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Metadata: map[string]string{"landing_status": string(landingState.State())}, Timestamp: time.Now().UTC()})
				if l.landingLock != nil {
					l.landingLock.Lock()
					defer l.landingLock.Unlock()
				}
				if err := l.options.VCS.MergeToMain(ctx, taskBranch); err != nil {
					_ = landingState.Apply(scheduler.LandingEventFailedPermanent)
					_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeTaskDataUpdated, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Metadata: map[string]string{"landing_status": string(landingState.State()), "triage_reason": err.Error()}, Timestamp: time.Now().UTC()})
					return summary, err
				}
				_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeMergeCompleted, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Message: taskBranch, Timestamp: time.Now().UTC()})
				if err := l.options.VCS.PushMain(ctx); err != nil {
					_ = landingState.Apply(scheduler.LandingEventFailedPermanent)
					_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeTaskDataUpdated, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Metadata: map[string]string{"landing_status": string(landingState.State()), "triage_reason": err.Error()}, Timestamp: time.Now().UTC()})
					return summary, err
				}
				_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypePushCompleted, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Timestamp: time.Now().UTC()})
				_ = landingState.Apply(scheduler.LandingEventSucceeded)
				_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeTaskDataUpdated, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Metadata: map[string]string{"landing_status": string(landingState.State())}, Timestamp: time.Now().UTC()})
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
			_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeTaskFinished, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Message: string(contracts.TaskStatusBlocked), Timestamp: time.Now().UTC()})
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
			_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeTaskFinished, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Message: string(contracts.TaskStatusFailed), Timestamp: time.Now().UTC()})
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
			_ = l.emit(ctx, contracts.Event{Type: contracts.EventTypeTaskFinished, TaskID: task.ID, TaskTitle: task.Title, WorkerID: worker, ClonePath: taskRepoRoot, QueuePos: queuePos, Message: string(contracts.TaskStatusFailed), Timestamp: time.Now().UTC()})
			summary.Failed++
			return summary, nil
		}
	}
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
	if strings.TrimSpace(task.Description) != "" {
		sections = append(sections, "Description:\n"+task.Description)
	}
	return strings.Join(sections, "\n\n")
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
