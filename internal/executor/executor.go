package executor

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
	"github.com/egv/yolo-runner/v2/internal/tk"
	"github.com/egv/yolo-runner/v2/internal/workitem"
)

type CloneManager interface {
	CloneForTask(ctx context.Context, taskID string, repoRoot string) (string, error)
	Cleanup(taskID string) error
}

type VCSFactory func(repoRoot string) contracts.VCS

type Executor struct {
	Tasks  contracts.TaskManager
	Runner contracts.AgentRunner
	Events contracts.EventSink

	VCS          contracts.VCS
	VCSFactory   VCSFactory
	CloneManager CloneManager
	LandingLock  LandingLock

	RepoRoot             string
	ParentID             string
	Backend              string
	Model                string
	FallbackModel        string
	MaxRetries           int
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
	RequireReview        bool
	MergeOnSuccess       bool
	LandingMode          string
	PRIDForLanding       string

	WorkerID string
	QueuePos int
	Priority int

	MarkTaskBlockedWithData func(taskID string, taskData map[string]string) error
	MarkTaskCompleted       func(taskID string) error
	ClearTaskTerminalState  func(taskID string) error
	ClearTaskInFlight       func(taskID string) error
}

func (e *Executor) Execute(ctx context.Context, payload workitem.ImplementPayload) (result workitem.ImplementResult, err error) {
	if e == nil {
		return workitem.ImplementResult{}, fmt.Errorf("executor is required")
	}
	if e.Runner == nil {
		return workitem.ImplementResult{}, fmt.Errorf("runner is required")
	}

	task := implementTaskFromPayload(payload, e.ParentID)
	parentID := strings.TrimSpace(task.ParentID)
	if parentID == "" {
		parentID = strings.TrimSpace(e.ParentID)
	}
	workerID := e.workerID()
	events := &executorEventRecorder{downstream: e.Events}

	repoRoot := strings.TrimSpace(e.RepoRoot)
	taskManager := e.taskManager(task)
	gateDeps := GateDependencies{
		Tasks:                   taskManager,
		Events:                  events,
		MarkTaskBlockedWithData: e.MarkTaskBlockedWithData,
		ClearTaskTerminalState:  e.ClearTaskTerminalState,
	}
	gateOptions := GateOptions{
		RepoRoot:             repoRoot,
		QualityGateThreshold: e.QualityGateThreshold,
		QualityGateTools:     append([]string{}, e.QualityGateTools...),
		QCGateTools:          append([]string{}, e.QCGateTools...),
		AllowLowQuality:      e.AllowLowQuality,
		RequireReview:        e.RequireReview,
	}

	_ = emitExecutorEvent(ctx, events, contracts.Event{
		Type:      contracts.EventTypeTaskStarted,
		TaskID:    task.ID,
		TaskTitle: task.Title,
		WorkerID:  workerID,
		QueuePos:  e.QueuePos,
		Priority:  e.Priority,
		Message:   task.Title,
		Metadata:  taskMonitoringMetadata(task, repoRoot),
		Timestamp: time.Now().UTC(),
	})

	runtime, err := e.resolveRuntime(task)
	if err != nil {
		return workitem.ImplementResult{}, err
	}

	if e.shouldRunQualityGate(payload) {
		blocked, err := RunQualityGate(ctx, task, gateDeps, gateOptions, GateEventContext{
			WorkerID:  workerID,
			ClonePath: repoRoot,
			QueuePos:  e.QueuePos,
		})
		if err != nil {
			return workitem.ImplementResult{}, err
		}
		if blocked {
			return e.resultFromStatus(contracts.RunnerResultBlocked, firstNonEmptyString(events.value("triage_reason"), events.value("reason")), "", events.snapshot()), nil
		}
	}

	if e.TDDMode || payload.TDD {
		blocked, reason, err := e.runTDDGate(ctx, task, taskManager, events, workerID, repoRoot)
		if err != nil {
			return workitem.ImplementResult{}, err
		}
		if blocked {
			return e.resultFromStatus(contracts.RunnerResultBlocked, reason, "", events.snapshot()), nil
		}
	}

	if e.CloneManager != nil {
		clonePath, cloneErr := e.CloneManager.CloneForTask(ctx, task.ID, repoRoot)
		if cloneErr != nil {
			return workitem.ImplementResult{}, cloneErr
		}
		repoRoot = clonePath
		defer func() {
			if cleanupErr := e.CloneManager.Cleanup(task.ID); cleanupErr != nil && err == nil {
				err = cleanupErr
			}
		}()
	}

	taskBranch := ""
	taskVCS := e.vcsForRepo(repoRoot)
	if taskVCS != nil {
		if strings.TrimSpace(e.LandingMode) == LandingModePushExistingPR {
			// PR-branch landing: the workspace is a per-PR checkout that already
			// has the PR branch current. Preserve it — do NOT EnsureMain or
			// CreateTaskBranch (which would branch off main) — and resolve the
			// current branch name so the landing guard admits the item.
			branch, err := taskVCS.CheckoutPRBranch(ctx, strings.TrimSpace(e.PRIDForLanding))
			if err != nil {
				return workitem.ImplementResult{}, err
			}
			taskBranch = strings.TrimSpace(branch)
		} else {
			if err := taskVCS.EnsureMain(ctx); err != nil {
				return workitem.ImplementResult{}, err
			}
			branch, err := taskVCS.CreateTaskBranch(ctx, task.ID)
			if err != nil {
				return workitem.ImplementResult{}, err
			}
			taskBranch = branch
			if err := taskVCS.Checkout(ctx, branch); err != nil {
				return workitem.ImplementResult{}, err
			}
		}
	}

	reviewRetries := retryCount(task.Metadata, "review_retry_count")
	reviewRetryFeedback := ReviewRetryBlockersFromMetadata(task.Metadata)
	completionRetries := retryCount(task.Metadata, "completion_retry_count")
	completionAddendum := strings.TrimSpace(task.Metadata["completion_addendum"])

	implementModel := strings.TrimSpace(runtime.Model)
	if implementModel == "" {
		implementModel = strings.TrimSpace(e.Model)
	}
	taskBackend := strings.TrimSpace(runtime.Backend)
	if taskBackend == "" {
		taskBackend = strings.TrimSpace(e.Backend)
	}
	fallbackModel := strings.TrimSpace(e.FallbackModel)
	usedModelFallback := false
	modelBeforeFallback := ""
	modelFallbackReason := ""

	for {
		reviewFailed := false
		if err := setExecutorTaskStatus(ctx, taskManager, task.ID, contracts.TaskStatusInProgress); err != nil {
			return workitem.ImplementResult{}, err
		}

		implementLogPath := defaultRunnerLogPath(repoRoot, task.ID, parentID, taskBackend)
		if err := ensureRunnerLogDirectory(repoRoot, implementLogPath); err != nil {
			return workitem.ImplementResult{}, err
		}
		implementStartMeta := buildRunnerStartedMetadata(contracts.RunnerModeImplement, taskBackend, implementModel, repoRoot, implementLogPath, time.Now().UTC())
		implementStartMeta = appendTaskRuntimeMetadata(implementStartMeta, runtime)
		if usedModelFallback {
			implementStartMeta = appendExecutorDecisionMetadata(implementStartMeta, "model_fallback", modelFallbackReason)
			implementStartMeta["model_previous"] = modelBeforeFallback
			if fallbackModel != "" {
				implementStartMeta["model_fallback"] = fallbackModel
			}
		}
		_ = emitExecutorEvent(ctx, events, buildAgentStartedEvent(task.ID, task.Title, workerID, repoRoot, e.QueuePos, contracts.RunnerModeImplement, implementStartMeta, completionRetries+1, completionRetries, e.MaxRetries+1))

		requestMetadata := map[string]string{"log_path": implementLogPath, "clone_path": repoRoot}
		requestMetadata = appendTaskRuntimeMetadata(requestMetadata, runtime)
		if e.WatchdogTimeout > 0 {
			requestMetadata["watchdog_timeout"] = e.WatchdogTimeout.String()
		}
		if e.WatchdogInterval > 0 {
			requestMetadata["watchdog_interval"] = e.WatchdogInterval.String()
		}

		runnerResult, err := RunWithMonitoring(ctx, e.Runner, events, contracts.RunnerRequest{
			TaskID:   task.ID,
			ParentID: parentID,
			Mode:     contracts.RunnerModeImplement,
			RepoRoot: repoRoot,
			Model:    implementModel,
			Timeout:  runtime.Timeout,
			Prompt: buildExecutorImplementPrompt(
				task,
				payload.PromptContext.Prompt,
				reviewRetryFeedback,
				reviewRetries,
				completionAddendum,
				completionRetries,
				e.TDDMode || payload.TDD,
			),
			Metadata: requestMetadata,
		}, MonitorEventContext{
			TaskID:      task.ID,
			TaskTitle:   task.Title,
			WorkerID:    workerID,
			ClonePath:   repoRoot,
			QueuePos:    e.QueuePos,
			Attempt:     completionRetries + 1,
			RetryCount:  completionRetries,
			MaxAttempts: e.MaxRetries + 1,
		}, MonitorOptions{
			HeartbeatInterval:    e.HeartbeatInterval,
			NoOutputWarningAfter: e.NoOutputWarningAfter,
		})
		if err != nil {
			return workitem.ImplementResult{}, err
		}
		runnerResult = ensureResultArtifact(runnerResult, "log_path", implementLogPath)
		_ = emitExecutorEvent(ctx, events, buildAgentFinishedEvent(task.ID, task.Title, workerID, repoRoot, e.QueuePos, runnerResult, buildRunnerFinishedMetadata(runnerResult), completionRetries+1, completionRetries, e.MaxRetries+1))

		if runnerResult.Status == contracts.RunnerResultCompleted && e.RequireReview {
			reviewAttempt := reviewRetries + 1
			reviewResult, err := e.runReview(ctx, task, runtime, events, repoRoot, parentID, workerID, reviewAttempt, reviewRetries, taskBackend, implementModel)
			if err != nil {
				return workitem.ImplementResult{}, err
			}
			if reviewResult.Status == contracts.RunnerResultCompleted && reviewResult.ReviewReady {
				if ReviewVerdictFromArtifacts(reviewResult) == "pass" {
					runnerResult.ReviewReady = true
				}
				runnerResult.Artifacts = mergeStringMaps(runnerResult.Artifacts, reviewResult.Artifacts)
				if reviewResult.LogPath != "" {
					runnerResult.Artifacts = ensureStringMap(runnerResult.Artifacts)
					runnerResult.Artifacts["review_log_path"] = reviewResult.LogPath
				}
			}
			if reviewResult.Status != contracts.RunnerResultCompleted {
				runnerResult = reviewResult
				if reviewResult.Status == contracts.RunnerResultFailed {
					reviewFailed = true
				}
			}
		}

		switch runnerResult.Status {
		case contracts.RunnerResultCompleted:
			blocked, err := RunQCGate(ctx, task, runnerResult, gateDeps, gateOptions, GateEventContext{
				WorkerID:  workerID,
				ClonePath: repoRoot,
				QueuePos:  e.QueuePos,
			})
			if err != nil {
				return workitem.ImplementResult{}, err
			}
			if blocked {
				return e.resultFromRunnerResult(contracts.RunnerResult{Status: contracts.RunnerResultBlocked, Reason: firstNonEmptyString(events.value("triage_reason"), events.value("reason")), Artifacts: events.snapshot()}, taskBranch), nil
			}

			if e.MergeOnSuccess && taskVCS != nil && taskBranch != "" {
				if err := markExecutorTaskCompleted(e, task.ID); err != nil {
					return workitem.ImplementResult{}, err
				}
				blocked, err := RunLanding(ctx, task, LandingDependencies{
					Tasks:                   taskManager,
					Runner:                  e.Runner,
					Events:                  events,
					VCS:                     taskVCS,
					LandingLock:             e.LandingLock,
					MarkTaskBlockedWithData: e.MarkTaskBlockedWithData,
					ClearTaskTerminalState:  e.ClearTaskTerminalState,
				}, LandingOptions{
					ParentID:             parentID,
					Backend:              e.Backend,
					Model:                e.Model,
					WatchdogTimeout:      e.WatchdogTimeout,
					WatchdogInterval:     e.WatchdogInterval,
					HeartbeatInterval:    e.HeartbeatInterval,
					NoOutputWarningAfter: e.NoOutputWarningAfter,
					Runtime:              runtime,
					LandingMode:          e.LandingMode,
					PRIDForLanding:       e.PRIDForLanding,
				}, LandingEventContext{
					TaskBranch: taskBranch,
					WorkerID:   workerID,
					ClonePath:  repoRoot,
					QueuePos:   e.QueuePos,
				})
				if err != nil {
					return workitem.ImplementResult{}, err
				}
				if blocked {
					return e.resultFromRunnerResult(contracts.RunnerResult{Status: contracts.RunnerResultBlocked, Reason: firstNonEmptyString(events.value("triage_reason"), events.value("reason")), Artifacts: mergeStringMaps(runnerResult.Artifacts, events.snapshot())}, taskBranch), nil
				}
				runnerResult.Artifacts = mergeStringMaps(runnerResult.Artifacts, events.snapshot())
			}

			return e.resultFromRunnerResult(runnerResult, taskBranch), nil

		case contracts.RunnerResultBlocked:
			runnerResult.Artifacts = mergeStringMaps(runnerResult.Artifacts, events.snapshot())
			return e.resultFromRunnerResult(runnerResult, taskBranch), nil

		case contracts.RunnerResultFailed:
			if !reviewFailed && !usedModelFallback && isRecoverableModelFailureResult(runnerResult, implementModel, fallbackModel) {
				usedModelFallback = true
				modelFallbackReason = strings.TrimSpace(runnerResult.Reason)
				modelBeforeFallback = implementModel
				implementModel = fallbackModel
				continue
			}

			reviewFail := reviewFailed || isExecutorReviewFailResult(runnerResult)
			if reviewFail {
				feedback := strings.TrimSpace(ReviewFailFeedbackFromArtifacts(runnerResult))
				if feedback == "" {
					feedback = strings.TrimSpace(runnerResult.Reason)
				}
				reviewRetryFeedback = feedback
				if reviewRetries < e.MaxRetries {
					reviewRetries++
					retryData := map[string]string{"review_retry_count": fmt.Sprintf("%d", reviewRetries)}
					if reviewRetryFeedback != "" {
						retryData["review_feedback"] = reviewRetryFeedback
					}
					retryData = appendExecutorReviewOutcomeMetadata(retryData, runnerResult)
					if strings.TrimSpace(runnerResult.Reason) != "" {
						retryData["triage_reason"] = strings.TrimSpace(runnerResult.Reason)
					}
					retryData = appendExecutorDecisionMetadata(retryData, "retry", runnerResult.Reason)
					if err := setExecutorTaskData(ctx, taskManager, task.ID, retryData); err != nil {
						return workitem.ImplementResult{}, err
					}
					if task.Metadata == nil {
						task.Metadata = map[string]string{}
					}
					for key, value := range retryData {
						task.Metadata[key] = value
					}
					_ = emitExecutorEvent(ctx, events, contracts.Event{Type: contracts.EventTypeTaskDataUpdated, TaskID: task.ID, TaskTitle: task.Title, WorkerID: workerID, ClonePath: repoRoot, QueuePos: e.QueuePos, Metadata: retryData, Timestamp: time.Now().UTC()})
					if err := setExecutorTaskStatus(ctx, taskManager, task.ID, contracts.TaskStatusOpen); err != nil {
						return workitem.ImplementResult{}, err
					}
					continue
				}
			}

			if !reviewFail {
				completionReason := strings.TrimSpace(runnerResult.Reason)
				if completionReason == "" {
					completionReason = "implementation completion failed"
				}
				if completionRetries < e.MaxRetries {
					completionRetries++
					completionAddendum = appendCompletionAddendum(completionAddendum, completionRetries, completionReason)
					retryData := map[string]string{"completion_retry_count": fmt.Sprintf("%d", completionRetries), "completion_addendum": completionAddendum, "triage_reason": completionReason}
					retryData = appendExecutorDecisionMetadata(retryData, "retry", completionReason)
					retryData = appendExecutorReviewOutcomeMetadata(retryData, runnerResult)
					if err := setExecutorTaskData(ctx, taskManager, task.ID, retryData); err != nil {
						return workitem.ImplementResult{}, err
					}
					if task.Metadata == nil {
						task.Metadata = map[string]string{}
					}
					for key, value := range retryData {
						task.Metadata[key] = value
					}
					_ = emitExecutorEvent(ctx, events, contracts.Event{Type: contracts.EventTypeTaskDataUpdated, TaskID: task.ID, TaskTitle: task.Title, WorkerID: workerID, ClonePath: repoRoot, QueuePos: e.QueuePos, Metadata: retryData, Timestamp: time.Now().UTC()})
					if err := setExecutorTaskStatus(ctx, taskManager, task.ID, contracts.TaskStatusOpen); err != nil {
						return workitem.ImplementResult{}, err
					}
					continue
				}

				completionAddendum = appendCompletionAddendum(completionAddendum, completionRetries+1, completionReason)
				artifacts := mergeStringMaps(runnerResult.Artifacts, map[string]string{
					"completion_retry_count": fmt.Sprintf("%d", completionRetries),
					"completion_addendum":    completionAddendum,
				})
				return e.resultFromRunnerResult(contracts.RunnerResult{Status: contracts.RunnerResultBlocked, Reason: completionReason, Artifacts: artifacts}, taskBranch), nil
			}

			artifacts := runnerResult.Artifacts
			if reviewFail || reviewRetries > 0 {
				artifacts = mergeStringMaps(artifacts, map[string]string{"review_retry_count": fmt.Sprintf("%d", reviewRetries)})
			}
			return e.resultFromRunnerResult(contracts.RunnerResult{Status: runnerResult.Status, Reason: runnerResult.Reason, LogPath: runnerResult.LogPath, Artifacts: artifacts}, taskBranch), nil

		default:
			reason := strings.TrimSpace(runnerResult.Reason)
			if reason == "" {
				reason = fmt.Sprintf("invalid runner result status %q", runnerResult.Status)
			}
			failedResult := contracts.RunnerResult{Status: contracts.RunnerResultFailed, Reason: reason, Artifacts: runnerResult.Artifacts}
			return e.resultFromRunnerResult(failedResult, taskBranch), nil
		}
	}
}

func (e *Executor) runReview(ctx context.Context, task contracts.Task, runtime TaskRuntimeConfig, events contracts.EventSink, repoRoot string, parentID string, workerID string, reviewAttempt int, reviewRetries int, taskBackend string, implementModel string) (contracts.RunnerResult, error) {
	reviewTelemetry := map[string]string{
		"review_attempt":     fmt.Sprintf("%d", reviewAttempt),
		"review_retry_count": fmt.Sprintf("%d", reviewRetries),
	}
	_ = emitExecutorEvent(ctx, events, contracts.Event{Type: contracts.EventTypeReviewStarted, TaskID: task.ID, TaskTitle: task.Title, WorkerID: workerID, ClonePath: repoRoot, QueuePos: e.QueuePos, Metadata: reviewTelemetry, Timestamp: time.Now().UTC()})
	reviewLogPath := defaultRunnerLogPath(repoRoot, task.ID, parentID, taskBackend)
	if err := ensureRunnerLogDirectory(repoRoot, reviewLogPath); err != nil {
		return contracts.RunnerResult{}, err
	}
	reviewStartMeta := buildRunnerStartedMetadata(contracts.RunnerModeReview, taskBackend, implementModel, repoRoot, reviewLogPath, time.Now().UTC())
	reviewStartMeta = appendTaskRuntimeMetadata(reviewStartMeta, runtime)
	_ = emitExecutorEvent(ctx, events, buildAgentStartedEvent(task.ID, task.Title, workerID, repoRoot, e.QueuePos, contracts.RunnerModeReview, reviewStartMeta, reviewAttempt, reviewRetries, e.MaxRetries+1))
	reviewMetadata := map[string]string{"log_path": reviewLogPath, "clone_path": repoRoot}
	reviewMetadata = appendTaskRuntimeMetadata(reviewMetadata, runtime)
	if e.WatchdogTimeout > 0 {
		reviewMetadata["watchdog_timeout"] = e.WatchdogTimeout.String()
	}
	if e.WatchdogInterval > 0 {
		reviewMetadata["watchdog_interval"] = e.WatchdogInterval.String()
	}

	reviewResult, err := RunWithMonitoring(ctx, e.Runner, events, contracts.RunnerRequest{
		TaskID:   task.ID,
		ParentID: parentID,
		Mode:     contracts.RunnerModeReview,
		RepoRoot: repoRoot,
		Model:    implementModel,
		Timeout:  runtime.Timeout,
		Prompt:   BuildPrompt(task, contracts.RunnerModeReview, false),
		Metadata: reviewMetadata,
	}, MonitorEventContext{
		TaskID:      task.ID,
		TaskTitle:   task.Title,
		WorkerID:    workerID,
		ClonePath:   repoRoot,
		QueuePos:    e.QueuePos,
		Attempt:     reviewAttempt,
		RetryCount:  reviewRetries,
		MaxAttempts: e.MaxRetries + 1,
	}, MonitorOptions{
		HeartbeatInterval:    e.HeartbeatInterval,
		NoOutputWarningAfter: e.NoOutputWarningAfter,
	})
	if err != nil {
		return contracts.RunnerResult{}, err
	}
	reviewResult = ensureResultArtifact(reviewResult, "log_path", reviewLogPath)
	_ = emitExecutorEvent(ctx, events, buildAgentFinishedEvent(task.ID, task.Title, workerID, repoRoot, e.QueuePos, reviewResult, buildRunnerFinishedMetadata(reviewResult), reviewAttempt, reviewRetries, e.MaxRetries+1))

	finalReviewResult := normalizeReviewReady(reviewResult)
	if finalReviewResult.Status == contracts.RunnerResultCompleted && !finalReviewResult.ReviewReady && ReviewVerdictFromArtifacts(finalReviewResult) == "" {
		verdictMetadata := map[string]string{
			"log_path":     reviewLogPath,
			"clone_path":   repoRoot,
			"review_phase": "verdict_retry",
		}
		if e.WatchdogTimeout > 0 {
			verdictMetadata["watchdog_timeout"] = e.WatchdogTimeout.String()
		}
		if e.WatchdogInterval > 0 {
			verdictMetadata["watchdog_interval"] = e.WatchdogInterval.String()
		}
		verdictStartMeta := buildRunnerStartedMetadata(contracts.RunnerModeReview, taskBackend, implementModel, repoRoot, reviewLogPath, time.Now().UTC())
		verdictStartMeta = appendTaskRuntimeMetadata(verdictStartMeta, runtime)
		verdictStartMeta["review_phase"] = "verdict_retry"
		_ = emitExecutorEvent(ctx, events, buildAgentStartedEvent(task.ID, task.Title, workerID, repoRoot, e.QueuePos, contracts.RunnerModeReview, verdictStartMeta, reviewAttempt, reviewRetries, e.MaxRetries+1))

		verdictResult, verdictErr := RunWithMonitoring(ctx, e.Runner, events, contracts.RunnerRequest{
			TaskID:   task.ID,
			ParentID: parentID,
			Mode:     contracts.RunnerModeReview,
			RepoRoot: repoRoot,
			Model:    implementModel,
			Timeout:  runtime.Timeout,
			Prompt:   BuildReviewVerdictPrompt(task),
			Metadata: verdictMetadata,
		}, MonitorEventContext{
			TaskID:      task.ID,
			TaskTitle:   task.Title,
			WorkerID:    workerID,
			ClonePath:   repoRoot,
			QueuePos:    e.QueuePos,
			Attempt:     reviewAttempt,
			RetryCount:  reviewRetries,
			MaxAttempts: e.MaxRetries + 1,
		}, MonitorOptions{
			HeartbeatInterval:    e.HeartbeatInterval,
			NoOutputWarningAfter: e.NoOutputWarningAfter,
		})
		if verdictErr != nil {
			return contracts.RunnerResult{}, verdictErr
		}
		verdictResult = ensureResultArtifact(verdictResult, "log_path", reviewLogPath)
		verdictResult = normalizeReviewReady(verdictResult)
		_ = emitExecutorEvent(ctx, events, buildAgentFinishedEvent(task.ID, task.Title, workerID, repoRoot, e.QueuePos, verdictResult, buildRunnerFinishedMetadata(verdictResult), reviewAttempt, reviewRetries, e.MaxRetries+1))
		finalReviewResult = verdictResult
	}

	if finalReviewResult.Status == contracts.RunnerResultCompleted && !finalReviewResult.ReviewReady {
		finalReviewResult.Status = contracts.RunnerResultFailed
		if verdict := ReviewVerdictFromArtifacts(finalReviewResult); verdict == "fail" {
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
	if verdict := ReviewVerdictFromArtifacts(finalReviewResult); verdict != "" {
		reviewFinishedMetadata["review_verdict"] = verdict
	}
	if feedback := ReviewFailFeedbackFromArtifacts(finalReviewResult); feedback != "" {
		reviewFinishedMetadata["review_fail_feedback"] = feedback
	}
	_ = emitExecutorEvent(ctx, events, contracts.Event{Type: contracts.EventTypeReviewFinished, TaskID: task.ID, TaskTitle: task.Title, WorkerID: workerID, ClonePath: repoRoot, QueuePos: e.QueuePos, Message: string(finalReviewResult.Status), Metadata: reviewFinishedMetadata, Timestamp: time.Now().UTC()})
	return finalReviewResult, nil
}

func implementTaskFromPayload(payload workitem.ImplementPayload, fallbackParentID string) contracts.Task {
	metadata := cloneStringMap(payload.PromptContext.Metadata)
	if metadata == nil {
		metadata = map[string]string{}
	}
	if payload.RetryContext.Attempt > 0 {
		metadata["review_retry_count"] = strconv.Itoa(payload.RetryContext.Attempt)
	}
	if strings.TrimSpace(payload.RetryContext.ReviewFeedback) != "" {
		metadata["review_feedback"] = strings.TrimSpace(payload.RetryContext.ReviewFeedback)
	}
	if strings.TrimSpace(payload.RetryContext.PreviousReason) != "" {
		metadata["triage_reason"] = strings.TrimSpace(payload.RetryContext.PreviousReason)
	}
	if strings.TrimSpace(payload.RetryContext.PreviousBranch) != "" {
		metadata["previous_branch"] = strings.TrimSpace(payload.RetryContext.PreviousBranch)
	}
	if strings.TrimSpace(payload.RetryContext.PreviousCommitSHA) != "" {
		metadata["previous_commit_sha"] = strings.TrimSpace(payload.RetryContext.PreviousCommitSHA)
	}
	parentID := strings.TrimSpace(payload.PromptContext.ParentID)
	if parentID == "" {
		parentID = strings.TrimSpace(fallbackParentID)
	}
	return contracts.Task{
		ID:          strings.TrimSpace(payload.TaskID),
		Title:       strings.TrimSpace(payload.Title),
		Description: payload.Description,
		Status:      contracts.TaskStatusOpen,
		ParentID:    parentID,
		Metadata:    metadata,
	}
}

func (e *Executor) resolveRuntime(task contracts.Task) (TaskRuntimeConfig, error) {
	runtime := TaskRuntimeConfig{
		Backend: strings.TrimSpace(e.Backend),
		Model:   strings.TrimSpace(e.Model),
		Timeout: e.RunnerTimeout,
	}
	overrides, hasOverrides, err := tk.ParseTicketFrontmatterFromDescription(task.Description)
	if err != nil {
		return runtime, err
	}
	if !hasOverrides {
		return runtime, nil
	}
	runtime.UseConfig = true
	if strings.TrimSpace(overrides.Backend) != "" {
		runtime.Backend = strings.TrimSpace(overrides.Backend)
	}
	if strings.TrimSpace(overrides.Model) != "" {
		runtime.Model = strings.TrimSpace(overrides.Model)
	}
	if strings.TrimSpace(overrides.Skillset) != "" {
		runtime.Skillset = strings.TrimSpace(overrides.Skillset)
	}
	if len(overrides.Tools) > 0 {
		runtime.Tools = append([]string{}, overrides.Tools...)
	}
	if strings.TrimSpace(overrides.Mode) != "" {
		runtime.Mode = strings.TrimSpace(overrides.Mode)
	}
	if overrides.HasTimeout {
		runtime.Timeout = overrides.Timeout
	}
	return runtime, nil
}

func (e *Executor) shouldRunQualityGate(payload workitem.ImplementPayload) bool {
	return payload.QualityGate || len(e.QualityGateTools) > 0
}

func (e *Executor) runTDDGate(ctx context.Context, task contracts.Task, tasks contracts.TaskManager, events contracts.EventSink, workerID string, repoRoot string) (bool, string, error) {
	testsPresent, testsFailing, err := hasTestsForTDDMode(repoRoot)
	if err != nil {
		return false, "", err
	}
	if testsFailing {
		return false, "", nil
	}
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
	blockedData = appendExecutorDecisionMetadata(blockedData, "blocked", reason)
	if err := markExecutorTaskBlockedWithData(e, task.ID, blockedData); err != nil {
		return false, "", err
	}
	if err := setExecutorTaskStatus(ctx, tasks, task.ID, contracts.TaskStatusBlocked); err != nil {
		return false, "", err
	}
	if err := setExecutorTaskData(ctx, tasks, task.ID, blockedData); err != nil {
		return false, "", err
	}
	_ = emitExecutorEvent(ctx, events, contracts.Event{Type: contracts.EventTypeTaskFinished, TaskID: task.ID, TaskTitle: task.Title, WorkerID: workerID, ClonePath: repoRoot, QueuePos: e.QueuePos, Message: string(contracts.TaskStatusBlocked), Metadata: blockedData, Timestamp: time.Now().UTC()})
	_ = emitExecutorEvent(ctx, events, contracts.Event{Type: contracts.EventTypeTaskDataUpdated, TaskID: task.ID, TaskTitle: task.Title, WorkerID: workerID, ClonePath: repoRoot, QueuePos: e.QueuePos, Metadata: blockedData, Timestamp: time.Now().UTC()})
	if err := clearExecutorTerminalState(e, task.ID); err != nil {
		return false, "", err
	}
	return true, reason, nil
}

func (e *Executor) vcsForRepo(repoRoot string) contracts.VCS {
	if e == nil {
		return nil
	}
	if e.VCSFactory != nil {
		if scoped := e.VCSFactory(repoRoot); scoped != nil {
			return scoped
		}
	}
	return e.VCS
}

func (e *Executor) workerID() string {
	if e == nil {
		return "worker-0"
	}
	if workerID := strings.TrimSpace(e.WorkerID); workerID != "" {
		return workerID
	}
	return "worker-0"
}

func (e *Executor) taskManager(task contracts.Task) contracts.TaskManager {
	if e != nil && e.Tasks != nil {
		return e.Tasks
	}
	return newExecutorMemoryTaskManager(task)
}

func (e *Executor) resultFromStatus(status contracts.RunnerResultStatus, reason string, branch string, artifacts map[string]string) workitem.ImplementResult {
	return e.resultFromRunnerResult(contracts.RunnerResult{Status: status, Reason: reason, Artifacts: artifacts}, branch)
}

func (e *Executor) resultFromRunnerResult(result contracts.RunnerResult, branch string) workitem.ImplementResult {
	artifacts := cloneStringMap(result.Artifacts)
	if artifacts == nil {
		artifacts = map[string]string{}
	}
	if strings.TrimSpace(result.LogPath) != "" && strings.TrimSpace(artifacts["log_path"]) == "" {
		artifacts["log_path"] = strings.TrimSpace(result.LogPath)
	}
	branch = firstNonEmptyString(branch, artifacts["branch"])
	commitSHA := firstNonEmptyString(artifacts["commit_sha"], artifacts["auto_commit_sha"])
	prURL := firstNonEmptyString(artifacts["pr_url"], artifacts["pull_request_url"])
	reviewVerdict := ReviewVerdictFromArtifacts(result)
	return workitem.ImplementResult{
		Status:        string(result.Status),
		Reason:        strings.TrimSpace(result.Reason),
		Branch:        branch,
		CommitSHA:     commitSHA,
		PRURL:         prURL,
		ReviewVerdict: reviewVerdict,
		Artifacts:     compactLandingMetadata(artifacts),
	}
}

func buildExecutorImplementPrompt(task contracts.Task, payloadPrompt string, reviewFeedback string, reviewRetryCount int, completionFeedback string, completionRetryCount int, tddMode bool) string {
	prompt := BuildImplementPrompt(task, reviewFeedback, reviewRetryCount, completionFeedback, completionRetryCount, tddMode)
	payloadPrompt = strings.TrimSpace(payloadPrompt)
	if payloadPrompt == "" {
		return prompt
	}
	return strings.Join([]string{
		prompt,
		"Source Prompt Context:\n" + payloadPrompt,
	}, "\n\n")
}

func normalizeReviewReady(result contracts.RunnerResult) contracts.RunnerResult {
	if result.Status == contracts.RunnerResultCompleted && ReviewVerdictFromArtifacts(result) == "pass" {
		result.ReviewReady = true
	}
	return result
}

func buildReviewFailReason(result contracts.RunnerResult) string {
	feedback := ReviewFailFeedbackFromArtifacts(result)
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
	blockers := strings.TrimSpace(ReviewRetryBlockersFromMetadata(retryMetadata))
	if blockers == "" {
		return trimmed
	}
	lower := strings.ToLower(blockers)
	if strings.HasPrefix(lower, "review rejected") {
		return blockers
	}
	return "review rejected: " + blockers
}

func isExecutorReviewFailResult(result contracts.RunnerResult) bool {
	if verdict := ReviewVerdictFromArtifacts(result); verdict == "fail" {
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

func isRecoverableModelFailureResult(result contracts.RunnerResult, currentModel string, fallbackModel string) bool {
	return isRecoverableModelFailureReason(result.Reason) && strings.TrimSpace(currentModel) != "" && strings.TrimSpace(fallbackModel) != "" && !strings.EqualFold(strings.TrimSpace(currentModel), strings.TrimSpace(fallbackModel))
}

func isRecoverableModelFailureReason(reason string) bool {
	text := strings.ToLower(strings.TrimSpace(reason))
	if text == "" {
		return false
	}
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

func retryCount(metadata map[string]string, key string) int {
	if len(metadata) == 0 {
		return 0
	}
	retryCount, err := strconv.Atoi(strings.TrimSpace(metadata[key]))
	if err != nil || retryCount < 0 {
		return 0
	}
	return retryCount
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

func appendExecutorReviewOutcomeMetadata(metadata map[string]string, result contracts.RunnerResult) map[string]string {
	if metadata == nil {
		metadata = map[string]string{}
	}
	if verdict := ReviewVerdictFromArtifacts(result); verdict != "" {
		metadata["review_verdict"] = verdict
	}
	if feedback := ReviewFailFeedbackFromArtifacts(result); feedback != "" {
		metadata["review_fail_feedback"] = feedback
	}
	return metadata
}

func appendExecutorDecisionMetadata(metadata map[string]string, decision string, reason string) map[string]string {
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

func taskMonitoringMetadata(task contracts.Task, repoRoot string) map[string]string {
	metadata := cloneStringMap(task.Metadata)
	if metadata == nil {
		metadata = map[string]string{}
	}
	if parentID := strings.TrimSpace(task.ParentID); parentID != "" {
		metadata["parent_id"] = parentID
	}
	if repoRoot = strings.TrimSpace(repoRoot); repoRoot != "" {
		metadata["repo_root"] = repoRoot
	}
	return compactLandingMetadata(metadata)
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

func mergeStringMaps(left map[string]string, right map[string]string) map[string]string {
	if len(left) == 0 {
		return cloneStringMap(right)
	}
	out := cloneStringMap(left)
	if out == nil {
		out = map[string]string{}
	}
	for key, value := range right {
		if strings.TrimSpace(value) == "" {
			continue
		}
		out[key] = value
	}
	return out
}

func ensureStringMap(input map[string]string) map[string]string {
	if input != nil {
		return input
	}
	return map[string]string{}
}

func ensureResultArtifact(result contracts.RunnerResult, key string, value string) contracts.RunnerResult {
	if strings.TrimSpace(value) == "" || strings.TrimSpace(key) == "" {
		return result
	}
	if strings.TrimSpace(result.Artifacts[key]) != "" {
		return result
	}
	result.Artifacts = ensureStringMap(result.Artifacts)
	result.Artifacts[key] = strings.TrimSpace(value)
	return result
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func emitExecutorEvent(ctx context.Context, events contracts.EventSink, event contracts.Event) error {
	if events == nil {
		return nil
	}
	return events.Emit(ctx, event)
}

func markExecutorTaskBlockedWithData(e *Executor, taskID string, taskData map[string]string) error {
	if e == nil || e.MarkTaskBlockedWithData == nil {
		return nil
	}
	return e.MarkTaskBlockedWithData(taskID, taskData)
}

func markExecutorTaskCompleted(e *Executor, taskID string) error {
	if e == nil || e.MarkTaskCompleted == nil {
		return nil
	}
	return e.MarkTaskCompleted(taskID)
}

func clearExecutorTerminalState(e *Executor, taskID string) error {
	if e == nil || e.ClearTaskTerminalState == nil {
		return nil
	}
	return e.ClearTaskTerminalState(taskID)
}

func clearExecutorInFlight(e *Executor, taskID string) error {
	if e == nil {
		return nil
	}
	if e.ClearTaskInFlight != nil {
		return e.ClearTaskInFlight(taskID)
	}
	if e.ClearTaskTerminalState != nil {
		return e.ClearTaskTerminalState(taskID)
	}
	return nil
}

func setExecutorTaskStatus(ctx context.Context, tasks contracts.TaskManager, taskID string, status contracts.TaskStatus) error {
	if tasks == nil {
		return nil
	}
	return tasks.SetTaskStatus(ctx, taskID, status)
}

func setExecutorTaskData(ctx context.Context, tasks contracts.TaskManager, taskID string, data map[string]string) error {
	if tasks == nil {
		return nil
	}
	return tasks.SetTaskData(ctx, taskID, data)
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

type executorMemoryTaskManager struct {
	task       contracts.Task
	statusByID map[string]contracts.TaskStatus
	dataByID   map[string]map[string]string
}

func newExecutorMemoryTaskManager(task contracts.Task) *executorMemoryTaskManager {
	return &executorMemoryTaskManager{
		task:       task,
		statusByID: map[string]contracts.TaskStatus{task.ID: task.Status},
		dataByID:   map[string]map[string]string{},
	}
}

func (m *executorMemoryTaskManager) NextTasks(context.Context, string) ([]contracts.TaskSummary, error) {
	return nil, nil
}

func (m *executorMemoryTaskManager) GetTask(_ context.Context, taskID string) (contracts.Task, error) {
	if taskID != m.task.ID {
		return contracts.Task{}, fmt.Errorf("task %q not found", taskID)
	}
	return m.task, nil
}

func (m *executorMemoryTaskManager) SetTaskStatus(_ context.Context, taskID string, status contracts.TaskStatus) error {
	m.statusByID[taskID] = status
	return nil
}

func (m *executorMemoryTaskManager) SetTaskData(_ context.Context, taskID string, data map[string]string) error {
	if m.dataByID[taskID] == nil {
		m.dataByID[taskID] = map[string]string{}
	}
	for key, value := range data {
		m.dataByID[taskID][key] = value
		if taskID == m.task.ID {
			if m.task.Metadata == nil {
				m.task.Metadata = map[string]string{}
			}
			m.task.Metadata[key] = value
		}
	}
	return nil
}

type executorEventRecorder struct {
	downstream contracts.EventSink
	mu         sync.Mutex
	metadata   map[string]string
}

func (r *executorEventRecorder) Emit(ctx context.Context, event contracts.Event) error {
	r.record(event.Metadata)
	if err := emitExecutorEvent(ctx, r.downstream, event); err != nil {
		return err
	}
	return nil
}

func (r *executorEventRecorder) record(metadata map[string]string) {
	if len(metadata) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.metadata == nil {
		r.metadata = map[string]string{}
	}
	for key, value := range metadata {
		if strings.TrimSpace(value) == "" {
			continue
		}
		switch key {
		case "auto_commit_sha", "commit_sha", "pr_url", "pull_request_url", "triage_reason", "reason", "review_verdict", "review_fail_feedback", "landing_status":
			r.metadata[key] = strings.TrimSpace(value)
		}
	}
}

func (r *executorEventRecorder) snapshot() map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneStringMap(r.metadata)
}

func (r *executorEventRecorder) value(key string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.TrimSpace(r.metadata[key])
}
