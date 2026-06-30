package executor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/scheduler"
)

// LandingMode controls how the landing section publishes changes.
const (
	// LandingModeNewPR creates a new pull request (default landing behavior).
	LandingModeNewPR = "new_pr"
	// LandingModePushExistingPR force-pushes to an existing PR branch instead of creating a new PR.
	LandingModePushExistingPR = "push_existing_pr"
)

type LandingLock interface {
	Lock()
	Unlock()
}

type TaskRuntimeConfig struct {
	Backend   string
	Model     string
	Skillset  string
	Tools     []string
	Mode      string
	Timeout   time.Duration
	UseConfig bool
}

type LandingDependencies struct {
	Tasks                   contracts.TaskManager
	Runner                  contracts.AgentRunner
	Events                  contracts.EventSink
	VCS                     contracts.VCS
	LandingLock             LandingLock
	MarkTaskBlockedWithData func(taskID string, taskData map[string]string) error
	ClearTaskTerminalState  func(taskID string) error
}

type LandingOptions struct {
	ParentID             string
	Backend              string
	Model                string
	WatchdogTimeout      time.Duration
	WatchdogInterval     time.Duration
	HeartbeatInterval    time.Duration
	NoOutputWarningAfter time.Duration
	Runtime              TaskRuntimeConfig
	LandingMode          string
	PRIDForLanding       string
}

type LandingEventContext struct {
	TaskBranch string
	WorkerID   string
	ClonePath  string
	QueuePos   int
}

type landingPullRequestCreator interface {
	CreatePR(ctx context.Context, title string, body string) (string, error)
}

func RunLanding(ctx context.Context, task contracts.Task, deps LandingDependencies, options LandingOptions, eventContext LandingEventContext) (bool, error) {
	taskVCS := deps.VCS
	taskBranch := strings.TrimSpace(eventContext.TaskBranch)
	taskRepoRoot := strings.TrimSpace(eventContext.ClonePath)
	if taskVCS == nil || taskBranch == "" {
		return false, nil
	}

	landingState := scheduler.NewLandingQueueStateMachine(2)
	autoCommitSHA := ""
	buildLandingMetadata := func(status string, attempt int, reason string) map[string]string {
		metadata := map[string]string{"landing_status": status}
		metadata = appendLandingDecisionMetadata(metadata, status, reason)
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
		_ = emitLandingEvent(ctx, deps.Events, contracts.Event{
			Type:      eventType,
			TaskID:    task.ID,
			TaskTitle: task.Title,
			WorkerID:  eventContext.WorkerID,
			ClonePath: taskRepoRoot,
			QueuePos:  eventContext.QueuePos,
			Metadata:  compactLandingMetadata(merged),
			Timestamp: time.Now().UTC(),
		})
	}
	emitMergeQueueEvent(contracts.EventTypeMergeQueued, appendLandingDecisionMetadata(map[string]string{"landing_status": string(landingState.State())}, string(landingState.State()), ""))
	_ = emitLandingEvent(ctx, deps.Events, contracts.Event{Type: contracts.EventTypeTaskDataUpdated, TaskID: task.ID, TaskTitle: task.Title, WorkerID: eventContext.WorkerID, ClonePath: taskRepoRoot, QueuePos: eventContext.QueuePos, Metadata: buildLandingMetadata(string(landingState.State()), 0, ""), Timestamp: time.Now().UTC()})
	if deps.LandingLock != nil {
		deps.LandingLock.Lock()
		defer deps.LandingLock.Unlock()
	}
	landingBlocked := false
	landingReason := ""
	autoCommitDone := false
	for attempt := 1; attempt <= 2; attempt++ {
		_ = landingState.Apply(scheduler.LandingEventBegin)
		_ = emitLandingEvent(ctx, deps.Events, contracts.Event{Type: contracts.EventTypeTaskDataUpdated, TaskID: task.ID, TaskTitle: task.Title, WorkerID: eventContext.WorkerID, ClonePath: taskRepoRoot, QueuePos: eventContext.QueuePos, Metadata: buildLandingMetadata(string(landingState.State()), attempt, ""), Timestamp: time.Now().UTC()})

		if !autoCommitDone {
			sha, err := taskVCS.CommitAll(ctx, AutoLandingCommitMessage(task, options.ParentID))
			if err != nil {
				landingReason = err.Error()
				_ = landingState.Apply(scheduler.LandingEventFailedPermanent)
				_ = emitLandingEvent(ctx, deps.Events, contracts.Event{Type: contracts.EventTypeTaskDataUpdated, TaskID: task.ID, TaskTitle: task.Title, WorkerID: eventContext.WorkerID, ClonePath: taskRepoRoot, QueuePos: eventContext.QueuePos, Metadata: buildLandingMetadata(string(landingState.State()), attempt, landingReason), Timestamp: time.Now().UTC()})
				landingBlocked = true
				break
			}
			autoCommitDone = true
			autoCommitSHA = strings.TrimSpace(sha)
			if autoCommitSHA != "" {
				_ = emitLandingEvent(ctx, deps.Events, contracts.Event{Type: contracts.EventTypeTaskDataUpdated, TaskID: task.ID, TaskTitle: task.Title, WorkerID: eventContext.WorkerID, ClonePath: taskRepoRoot, QueuePos: eventContext.QueuePos, Metadata: buildLandingMetadata(string(landingState.State()), attempt, ""), Timestamp: time.Now().UTC()})
			}
		}

		if strings.TrimSpace(options.LandingMode) == LandingModePushExistingPR {
			prID := strings.TrimSpace(options.PRIDForLanding)
			if err := taskVCS.PushPRBranch(ctx, prID); err != nil {
				landingReason = err.Error()
				_ = landingState.Apply(scheduler.LandingEventFailedPermanent)
				_ = emitLandingEvent(ctx, deps.Events, contracts.Event{Type: contracts.EventTypeTaskDataUpdated, TaskID: task.ID, TaskTitle: task.Title, WorkerID: eventContext.WorkerID, ClonePath: taskRepoRoot, QueuePos: eventContext.QueuePos, Metadata: buildLandingMetadata(string(landingState.State()), attempt, landingReason), Timestamp: time.Now().UTC()})
				landingBlocked = true
				break
			}
			prURL := existingArcPRURL(prID)
			_ = landingState.Apply(scheduler.LandingEventSucceeded)
			_ = emitLandingEvent(ctx, deps.Events, contracts.Event{Type: contracts.EventTypeTaskDataUpdated, TaskID: task.ID, TaskTitle: task.Title, WorkerID: eventContext.WorkerID, ClonePath: taskRepoRoot, QueuePos: eventContext.QueuePos, Metadata: buildLandingMetadata(string(landingState.State()), 0, ""), Timestamp: time.Now().UTC()})
			emitMergeQueueEvent(contracts.EventTypeMergeLanded, appendLandingDecisionMetadata(map[string]string{
				"landing_status":  string(landingState.State()),
				"landing_attempt": fmt.Sprintf("%d", attempt),
				"pr_url":          prURL,
				"landing_mode":    LandingModePushExistingPR,
			}, "landed", landingReason))
			break
		}

		if isDeferredPRLandingVCS(taskVCS) {
			_ = landingState.Apply(scheduler.LandingEventSucceeded)
			_ = emitLandingEvent(ctx, deps.Events, contracts.Event{Type: contracts.EventTypeTaskDataUpdated, TaskID: task.ID, TaskTitle: task.Title, WorkerID: eventContext.WorkerID, ClonePath: taskRepoRoot, QueuePos: eventContext.QueuePos, Metadata: buildLandingMetadata(string(landingState.State()), 0, ""), Timestamp: time.Now().UTC()})
			emitMergeQueueEvent(contracts.EventTypeMergeLanded, appendLandingDecisionMetadata(map[string]string{
				"landing_status":  string(landingState.State()),
				"landing_attempt": fmt.Sprintf("%d", attempt),
			}, "landed", landingReason))
			break
		}

		if err := taskVCS.MergeToMain(ctx, taskBranch); err != nil {
			landingReason = err.Error()
			_ = landingState.Apply(scheduler.LandingEventFailedRetryable)
			_ = emitLandingEvent(ctx, deps.Events, contracts.Event{Type: contracts.EventTypeTaskDataUpdated, TaskID: task.ID, TaskTitle: task.Title, WorkerID: eventContext.WorkerID, ClonePath: taskRepoRoot, QueuePos: eventContext.QueuePos, Metadata: buildLandingMetadata(string(landingState.State()), attempt, landingReason), Timestamp: time.Now().UTC()})
			if attempt < 2 {
				emitMergeQueueEvent(contracts.EventTypeMergeRetry, appendLandingDecisionMetadata(map[string]string{
					"landing_status":  string(landingState.State()),
					"landing_attempt": fmt.Sprintf("%d", attempt),
					"triage_reason":   landingReason,
				}, "retry", landingReason))
				if IsMergeConflictError(landingReason) {
					remediationResult := runLandingMergeConflictRemediation(ctx, task, deps, options, eventContext, landingReason)
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
				_ = emitLandingEvent(ctx, deps.Events, contracts.Event{Type: contracts.EventTypeTaskDataUpdated, TaskID: task.ID, TaskTitle: task.Title, WorkerID: eventContext.WorkerID, ClonePath: taskRepoRoot, QueuePos: eventContext.QueuePos, Metadata: buildLandingMetadata(string(landingState.State()), 0, ""), Timestamp: time.Now().UTC()})
				emitMergeQueueEvent(contracts.EventTypeMergeQueued, appendLandingDecisionMetadata(map[string]string{
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
		_ = emitLandingEvent(ctx, deps.Events, contracts.Event{Type: contracts.EventTypeMergeCompleted, TaskID: task.ID, TaskTitle: task.Title, WorkerID: eventContext.WorkerID, ClonePath: taskRepoRoot, QueuePos: eventContext.QueuePos, Message: taskBranch, Metadata: mergeMetadata, Timestamp: time.Now().UTC()})
		if err := taskVCS.PushMain(ctx); err != nil {
			landingReason = err.Error()
			_ = landingState.Apply(scheduler.LandingEventFailedPermanent)
			_ = emitLandingEvent(ctx, deps.Events, contracts.Event{Type: contracts.EventTypeTaskDataUpdated, TaskID: task.ID, TaskTitle: task.Title, WorkerID: eventContext.WorkerID, ClonePath: taskRepoRoot, QueuePos: eventContext.QueuePos, Metadata: buildLandingMetadata(string(landingState.State()), attempt, landingReason), Timestamp: time.Now().UTC()})
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
		_ = emitLandingEvent(ctx, deps.Events, contracts.Event{Type: contracts.EventTypePushCompleted, TaskID: task.ID, TaskTitle: task.Title, WorkerID: eventContext.WorkerID, ClonePath: taskRepoRoot, QueuePos: eventContext.QueuePos, Metadata: pushMetadata, Timestamp: time.Now().UTC()})
		_ = landingState.Apply(scheduler.LandingEventSucceeded)
		_ = emitLandingEvent(ctx, deps.Events, contracts.Event{Type: contracts.EventTypeTaskDataUpdated, TaskID: task.ID, TaskTitle: task.Title, WorkerID: eventContext.WorkerID, ClonePath: taskRepoRoot, QueuePos: eventContext.QueuePos, Metadata: buildLandingMetadata(string(landingState.State()), 0, ""), Timestamp: time.Now().UTC()})
		emitMergeQueueEvent(contracts.EventTypeMergeLanded, appendLandingDecisionMetadata(map[string]string{
			"landing_status":  string(landingState.State()),
			"landing_attempt": fmt.Sprintf("%d", attempt),
		}, "landed", landingReason))
		break
	}

	if landingBlocked {
		emitMergeQueueEvent(contracts.EventTypeMergeBlocked, appendLandingDecisionMetadata(map[string]string{
			"landing_status": string(landingState.State()),
			"triage_reason":  landingReason,
		}, "blocked", landingReason))
		blockedData := map[string]string{"triage_status": "blocked", "landing_status": string(landingState.State())}
		if landingReason != "" {
			blockedData["triage_reason"] = landingReason
		}
		blockedData = appendLandingDecisionMetadata(blockedData, "blocked", landingReason)
		if autoCommitSHA != "" {
			blockedData["auto_commit_sha"] = autoCommitSHA
		}
		if err := markLandingTaskBlockedWithData(deps, task.ID, blockedData); err != nil {
			return true, err
		}
		if err := setLandingTaskStatus(ctx, deps, task.ID, contracts.TaskStatusBlocked); err != nil {
			return true, err
		}
		finishedMetadata := map[string]string{"triage_status": "blocked"}
		if landingReason != "" {
			finishedMetadata["triage_reason"] = landingReason
		}
		finishedMetadata = appendLandingDecisionMetadata(finishedMetadata, "blocked", landingReason)
		_ = emitLandingEvent(ctx, deps.Events, contracts.Event{Type: contracts.EventTypeTaskFinished, TaskID: task.ID, TaskTitle: task.Title, WorkerID: eventContext.WorkerID, ClonePath: taskRepoRoot, QueuePos: eventContext.QueuePos, Message: string(contracts.TaskStatusBlocked), Metadata: finishedMetadata, Timestamp: time.Now().UTC()})
		if err := setLandingTaskData(ctx, deps, task.ID, blockedData); err != nil {
			return true, err
		}
		_ = emitLandingEvent(ctx, deps.Events, contracts.Event{Type: contracts.EventTypeTaskDataUpdated, TaskID: task.ID, TaskTitle: task.Title, WorkerID: eventContext.WorkerID, ClonePath: taskRepoRoot, QueuePos: eventContext.QueuePos, Metadata: blockedData, Timestamp: time.Now().UTC()})
		if err := clearLandingTaskTerminalState(deps, task.ID); err != nil {
			return true, err
		}
		return true, nil
	}

	return false, nil
}

func isDeferredPRLandingVCS(vcs contracts.VCS) bool {
	if vcs == nil {
		return false
	}
	_, ok := vcs.(landingPullRequestCreator)
	return ok
}

// arcPRReviewURLPrefix is the Arcanum web base used to build an existing
// PR URL from its id.
const arcPRReviewURLPrefix = "https://a.yandex-team.ru/review/"

// existingArcPRURL builds the Arcanum review URL for an existing PR id.
func existingArcPRURL(prID string) string {
	prID = strings.TrimSpace(prID)
	if prID == "" {
		return ""
	}
	return arcPRReviewURLPrefix + prID
}

func runLandingMergeConflictRemediation(ctx context.Context, task contracts.Task, deps LandingDependencies, options LandingOptions, eventContext LandingEventContext, mergeFailureReason string) contracts.RunnerResult {
	taskBranch := strings.TrimSpace(eventContext.TaskBranch)
	taskRepoRoot := strings.TrimSpace(eventContext.ClonePath)
	if deps.VCS != nil && taskBranch != "" {
		if err := deps.VCS.Checkout(ctx, taskBranch); err != nil {
			return contracts.RunnerResult{Status: contracts.RunnerResultFailed, Reason: fmt.Sprintf("git checkout %s failed: %v", taskBranch, err)}
		}
	}

	epicID := strings.TrimSpace(task.ParentID)
	if epicID == "" {
		epicID = strings.TrimSpace(options.ParentID)
	}

	runtime := options.Runtime
	runtimeBackend := strings.TrimSpace(runtime.Backend)
	if runtimeBackend == "" {
		runtimeBackend = strings.TrimSpace(options.Backend)
	}
	runtimeModel := strings.TrimSpace(runtime.Model)
	if runtimeModel == "" {
		runtimeModel = strings.TrimSpace(options.Model)
	}

	remediationLogPath := defaultRunnerLogPath(taskRepoRoot, task.ID, epicID, runtimeBackend)
	if err := ensureRunnerLogDirectory(taskRepoRoot, remediationLogPath); err != nil {
		return contracts.RunnerResult{Status: contracts.RunnerResultFailed, Reason: err.Error()}
	}
	remediationStartMeta := buildRunnerStartedMetadata(contracts.RunnerModeImplement, runtimeBackend, runtimeModel, taskRepoRoot, remediationLogPath, time.Now().UTC())
	remediationStartMeta = appendTaskRuntimeMetadata(remediationStartMeta, runtime)
	remediationStartMeta["landing_phase"] = "merge_conflict_remediation"
	_ = emitLandingEvent(ctx, deps.Events, buildAgentStartedEvent(task.ID, task.Title, eventContext.WorkerID, taskRepoRoot, eventContext.QueuePos, contracts.RunnerModeImplement, remediationStartMeta, 1, 0, 1))

	remediationMetadata := map[string]string{"log_path": remediationLogPath, "clone_path": taskRepoRoot, "landing_phase": "merge_conflict_remediation"}
	remediationMetadata = appendTaskRuntimeMetadata(remediationMetadata, runtime)
	if options.WatchdogTimeout > 0 {
		remediationMetadata["watchdog_timeout"] = options.WatchdogTimeout.String()
	}
	if options.WatchdogInterval > 0 {
		remediationMetadata["watchdog_interval"] = options.WatchdogInterval.String()
	}

	result := contracts.RunnerResult{Status: contracts.RunnerResultFailed, Reason: "runner is required for merge conflict remediation"}
	if deps.Runner != nil {
		var err error
		result, err = RunWithMonitoring(ctx, deps.Runner, deps.Events, contracts.RunnerRequest{
			TaskID:   task.ID,
			ParentID: options.ParentID,
			Mode:     contracts.RunnerModeImplement,
			RepoRoot: taskRepoRoot,
			Model:    runtimeModel,
			Timeout:  runtime.Timeout,
			Prompt:   BuildMergeConflictRemediationPrompt(task, taskBranch, mergeFailureReason),
			Metadata: remediationMetadata,
		}, MonitorEventContext{
			TaskID:      task.ID,
			TaskTitle:   task.Title,
			WorkerID:    eventContext.WorkerID,
			ClonePath:   taskRepoRoot,
			QueuePos:    eventContext.QueuePos,
			Attempt:     metadataInt(remediationMetadata, "attempt", "landing_attempt"),
			RetryCount:  metadataInt(remediationMetadata, "retry_count"),
			MaxAttempts: metadataInt(remediationMetadata, "max_attempts"),
		}, MonitorOptions{
			HeartbeatInterval:    options.HeartbeatInterval,
			NoOutputWarningAfter: options.NoOutputWarningAfter,
		})
		if err != nil {
			result = contracts.RunnerResult{Status: contracts.RunnerResultFailed, Reason: err.Error()}
		}
	}

	_ = emitLandingEvent(ctx, deps.Events, buildAgentFinishedEvent(task.ID, task.Title, eventContext.WorkerID, taskRepoRoot, eventContext.QueuePos, result, buildRunnerFinishedMetadata(result), 1, 0, 1))
	return result
}

func BuildMergeConflictRemediationPrompt(task contracts.Task, taskBranch string, mergeFailureReason string) string {
	base := BuildImplementPrompt(task, "", 0, "", 0, false)
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

func IsMergeConflictError(reason string) bool {
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

func AutoLandingCommitMessage(task contracts.Task, fallbackParentID string) string {
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
	return compactLandingMetadata(metadata)
}

func appendTaskRuntimeMetadata(metadata map[string]string, runtime TaskRuntimeConfig) map[string]string {
	if metadata == nil {
		metadata = map[string]string{}
	}
	if runtime.UseConfig {
		metadata["runtime_config"] = "true"
	}
	if strings.TrimSpace(runtime.Backend) != "" {
		if strings.TrimSpace(metadata["backend"]) == "" {
			metadata["backend"] = strings.TrimSpace(runtime.Backend)
		}
		metadata["runtime_backend"] = strings.TrimSpace(runtime.Backend)
	}
	if strings.TrimSpace(runtime.Model) != "" {
		if strings.TrimSpace(metadata["model"]) == "" {
			metadata["model"] = strings.TrimSpace(runtime.Model)
		}
		metadata["runtime_model"] = strings.TrimSpace(runtime.Model)
	}
	if strings.TrimSpace(runtime.Skillset) != "" {
		metadata["skillset"] = strings.TrimSpace(runtime.Skillset)
		metadata["runtime_skillset"] = strings.TrimSpace(runtime.Skillset)
	}
	if runtime.Timeout >= 0 {
		metadata["timeout"] = runtime.Timeout.String()
		metadata["runtime_timeout"] = runtime.Timeout.String()
	}
	if len(runtime.Tools) > 0 {
		tools := strings.Join(runtime.Tools, ",")
		metadata["tools"] = tools
		metadata["runtime_tools"] = tools
	}
	if strings.TrimSpace(runtime.Mode) != "" {
		metadata["task_mode"] = strings.TrimSpace(strings.ToLower(runtime.Mode))
		metadata["runtime_mode"] = strings.TrimSpace(strings.ToLower(runtime.Mode))
	}
	return compactLandingMetadata(metadata)
}

func appendLandingDecisionMetadata(metadata map[string]string, decision string, reason string) map[string]string {
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
	return compactLandingMetadata(metadata)
}

func buildAgentEvent(eventType contracts.EventType, taskID string, taskTitle string, workerID string, clonePath string, queuePos int, message string, metadata map[string]string, timestamp time.Time) contracts.Event {
	if timestamp.IsZero() {
		timestamp = time.Now().UTC()
	}
	event := contracts.NewEvent(eventType, contracts.EventIdentity{})
	event.TaskID = taskID
	event.TaskTitle = taskTitle
	event.WorkerID = workerID
	event.ClonePath = clonePath
	event.QueuePos = queuePos
	event.Message = message
	event.Metadata = compactLandingMetadata(metadata)
	event.Timestamp = timestamp.UTC()
	promoteAttemptFields(&event)
	if eventType == contracts.EventTypeAgentBlocked && event.Reason == "" {
		event.Reason = blockReasonFromMetadata(event.Metadata)
	}
	if eventType == contracts.EventTypeAgentBlocked && event.Detail == "" {
		event.Detail = strings.TrimSpace(event.Metadata["detail"])
	}
	return event
}

func buildAgentStartedEvent(taskID string, taskTitle string, workerID string, clonePath string, queuePos int, mode contracts.RunnerMode, metadata map[string]string, attempt int, retryCount int, maxAttempts int) contracts.Event {
	event := buildAgentEvent(contracts.EventTypeAgentStarted, taskID, taskTitle, workerID, clonePath, queuePos, string(mode), metadata, time.Now().UTC())
	event.Attempt = positiveOrZero(attempt)
	event.RetryCount = positiveOrZero(retryCount)
	event.MaxAttempts = positiveOrZero(maxAttempts)
	return event
}

func buildAgentFinishedEvent(taskID string, taskTitle string, workerID string, clonePath string, queuePos int, result contracts.RunnerResult, metadata map[string]string, attempt int, retryCount int, maxAttempts int) contracts.Event {
	event := buildAgentEvent(contracts.EventTypeAgentFinished, taskID, taskTitle, workerID, clonePath, queuePos, string(result.Status), metadata, time.Now().UTC())
	event.Attempt = positiveOrZero(attempt)
	event.RetryCount = positiveOrZero(retryCount)
	event.MaxAttempts = positiveOrZero(maxAttempts)
	if strings.TrimSpace(result.Reason) != "" {
		event.Detail = strings.TrimSpace(result.Reason)
	}
	return event
}

func promoteAttemptFields(event *contracts.Event) {
	if event == nil {
		return
	}
	if event.Attempt == 0 {
		event.Attempt = metadataInt(event.Metadata, "attempt", "review_attempt", "landing_attempt")
	}
	if event.RetryCount == 0 {
		event.RetryCount = metadataInt(event.Metadata, "retry_count", "review_retry_count", "completion_retry_count")
	}
	if event.MaxAttempts == 0 {
		event.MaxAttempts = metadataInt(event.Metadata, "max_attempts")
	}
}

func metadataInt(metadata map[string]string, keys ...string) int {
	for _, key := range keys {
		raw := strings.TrimSpace(metadata[key])
		if raw == "" {
			continue
		}
		value, err := strconv.Atoi(raw)
		if err == nil && value > 0 {
			return value
		}
	}
	return 0
}

func positiveOrZero(value int) int {
	if value < 0 {
		return 0
	}
	return value
}

func blockReasonFromMetadata(metadata map[string]string) contracts.BlockReason {
	raw := strings.TrimSpace(strings.ToLower(firstNonEmptyMetadata(metadata, "block_reason", "reason", "triage_reason")))
	switch {
	case strings.Contains(raw, "permission"):
		return contracts.BlockReasonPermissionDenied
	case strings.Contains(raw, "no output") || strings.Contains(raw, "timeout") || strings.Contains(raw, "stall"):
		return contracts.BlockReasonNoOutput
	case strings.Contains(raw, "rate limit"):
		return contracts.BlockReasonRateLimited
	case strings.Contains(raw, "auth") || strings.Contains(raw, "token") || strings.Contains(raw, "credential"):
		return contracts.BlockReasonAuth
	case strings.Contains(raw, "stuck"):
		return contracts.BlockReasonStuck
	default:
		return contracts.BlockReasonOther
	}
}

func firstNonEmptyMetadata(metadata map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(metadata[key]); value != "" {
			return value
		}
	}
	return ""
}

func compactLandingMetadata(metadata map[string]string) map[string]string {
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

func emitLandingEvent(ctx context.Context, events contracts.EventSink, event contracts.Event) error {
	if events == nil {
		return nil
	}
	return events.Emit(ctx, event)
}

func markLandingTaskBlockedWithData(deps LandingDependencies, taskID string, taskData map[string]string) error {
	if deps.MarkTaskBlockedWithData == nil {
		return nil
	}
	return deps.MarkTaskBlockedWithData(taskID, taskData)
}

func clearLandingTaskTerminalState(deps LandingDependencies, taskID string) error {
	if deps.ClearTaskTerminalState == nil {
		return nil
	}
	return deps.ClearTaskTerminalState(taskID)
}

func setLandingTaskStatus(ctx context.Context, deps LandingDependencies, taskID string, status contracts.TaskStatus) error {
	if deps.Tasks == nil {
		return fmt.Errorf("task manager is required for landing status update")
	}
	return deps.Tasks.SetTaskStatus(ctx, taskID, status)
}

func setLandingTaskData(ctx context.Context, deps LandingDependencies, taskID string, data map[string]string) error {
	if deps.Tasks == nil {
		return fmt.Errorf("task manager is required for landing data update")
	}
	return deps.Tasks.SetTaskData(ctx, taskID, data)
}
