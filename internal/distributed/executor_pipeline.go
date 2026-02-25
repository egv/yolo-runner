package distributed

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

const (
	EventTypeExecutorPipelineStageStarted    contracts.EventType = "stage_started"
	EventTypeExecutorPipelineStageFinished   contracts.EventType = "stage_finished"
	EventTypeExecutorPipelineTransitionTaken contracts.EventType = "transition_taken"
)

type ExecutorPipelineStageRunner func(context.Context, contracts.RunnerRequest, string, ExecutorConfigStage) (contracts.RunnerResult, error)

type ExecutorPipeline struct {
	Config      ExecutorConfig
	StageRunner ExecutorPipelineStageRunner
	EventSink   contracts.EventSink
	Clock       func() time.Time
	Wait        func(context.Context, time.Duration) error
}

type ExecutorPipelineResult struct {
	Result     contracts.RunnerResult
	FinalStage string
	StageRuns  map[string]int
}

func (p *ExecutorPipeline) Execute(ctx context.Context, request contracts.RunnerRequest) (ExecutorPipelineResult, error) {
	if p == nil {
		return ExecutorPipelineResult{}, fmt.Errorf("executor pipeline is required")
	}
	if p.StageRunner == nil {
		return ExecutorPipelineResult{}, fmt.Errorf("executor pipeline stage runner is required")
	}
	if p.Clock == nil {
		p.Clock = func() time.Time { return time.Now().UTC() }
	}
	if p.Wait == nil {
		p.Wait = waitForContext
	}
	if ctx == nil {
		ctx = context.Background()
	}

	request = cloneRunnerRequest(request)
	if request.Metadata == nil {
		request.Metadata = map[string]string{}
	}
	pipelineMetadata := cloneStringMap(request.Metadata)
	taskID := strings.TrimSpace(request.TaskID)

	stageRuns := map[string]int{}
	currentStage := "quality_gate"
	for {
		if strings.TrimSpace(currentStage) == "" {
			return ExecutorPipelineResult{}, fmt.Errorf("invalid empty stage")
		}

		stageCfg, ok := p.Config.Pipeline[currentStage]
		if !ok {
			return ExecutorPipelineResult{}, fmt.Errorf("stage %q is missing from executor config", currentStage)
		}

		maxAttempts := stageCfg.Retry.MaxAttempts
		if maxAttempts <= 0 {
			maxAttempts = 1
		}
		attemptFailures := []string{}

		for {
			stageRuns[currentStage]++
			attempt := stageRuns[currentStage]

			if ctx.Err() != nil {
				return ExecutorPipelineResult{}, ctx.Err()
			}

			attemptRequest := cloneRunnerRequest(request)
			if attemptRequest.Metadata == nil {
				attemptRequest.Metadata = map[string]string{}
			}
			attemptRequest.Metadata["executor_stage"] = currentStage
			attemptRequest.Metadata["executor_attempt"] = strconv.Itoa(attempt)

			stageStartedContext := mergeMetadata(pipelineMetadata, attemptRequest.Metadata)
			p.emit(ctx, taskID, EventTypeExecutorPipelineStageStarted, stageStartedContext)
			startedAt := p.Clock()

			result, runErr := p.StageRunner(ctx, attemptRequest, currentStage, stageCfg)
			if result.Status == "" {
				result.Status = contracts.RunnerResultFailed
			}
			result.Reason = strings.TrimSpace(result.Reason)
			runErr = normalizeRunnerError(&result, runErr)

			resultMetadata := mergeMetadata(stageStartedContext, result.Artifacts)
			resultMetadata["attempt"] = strconv.Itoa(attempt)
			resultMetadata["stage"] = currentStage
			resultMetadata["max_attempts"] = strconv.Itoa(maxAttempts)
			result.Artifacts = resultMetadata

			stageFinishedContext := mergeMetadata(resultMetadata, map[string]string{
				"duration_ms": strconv.FormatInt(time.Since(startedAt).Milliseconds(), 10),
			})
			p.emit(ctx, taskID, EventTypeExecutorPipelineStageFinished, stageFinishedContext)

			if result.Status == contracts.RunnerResultCompleted && runErr == nil {
				transition, branchErr := takeTransition(stageCfg.Transitions.OnSuccess)
				if branchErr != nil {
					return ExecutorPipelineResult{
						FinalStage: currentStage,
						StageRuns:  cloneAttemptMap(stageRuns),
					}, branchErr
				}

				pipelineMetadata = mergeMetadata(pipelineMetadata, resultMetadata)
				conditionMet, evaluateErr := evaluateExecutorCondition(transition.Condition, pipelineMetadata)
				p.emitTransition(ctx, taskID, currentStage, "on_success", transition, conditionMet, attempt)
				if evaluateErr != nil {
					return ExecutorPipelineResult{
						FinalStage: currentStage,
						StageRuns:  cloneAttemptMap(stageRuns),
					}, evaluateErr
				}
				if !conditionMet {
					return ExecutorPipelineResult{
						Result:     contracts.RunnerResult{Status: contracts.RunnerResultFailed, Reason: "on_success transition condition evaluated to false"},
						FinalStage: currentStage,
						StageRuns:  cloneAttemptMap(stageRuns),
					}, nil
				}

				nextStage, terminal, actionErr := executeTransitionAction(transition.Action, currentStage, transition.NextStage)
				if actionErr != nil {
					return ExecutorPipelineResult{}, actionErr
				}
				if terminal {
					return ExecutorPipelineResult{
						Result:     finalizeResult(result, p.Clock),
						FinalStage: currentStage,
						StageRuns:  cloneAttemptMap(stageRuns),
					}, nil
				}
				currentStage = nextStage
				break
			}

			transition, branchErr := takeTransition(stageCfg.Transitions.OnFailure)
			if branchErr != nil {
				return ExecutorPipelineResult{
					FinalStage: currentStage,
					StageRuns:  cloneAttemptMap(stageRuns),
				}, branchErr
			}

			pipelineMetadata = mergeMetadata(pipelineMetadata, resultMetadata)
			resultReason := result.Reason
			if resultReason == "" {
				resultReason = "stage execution failed"
			}
			attemptFailures = append(attemptFailures, formatAttemptFailure(attempt, resultReason))

			conditionMet, evaluateErr := evaluateExecutorCondition(transition.Condition, pipelineMetadata)
			p.emitTransition(ctx, taskID, currentStage, "on_failure", transition, conditionMet, attempt)
			if evaluateErr != nil {
				return ExecutorPipelineResult{
					FinalStage: currentStage,
					StageRuns:  cloneAttemptMap(stageRuns),
				}, evaluateErr
			}
			if !conditionMet {
				return ExecutorPipelineResult{
					Result:     contracts.RunnerResult{Status: contracts.RunnerResultFailed, Reason: "on_failure transition condition evaluated to false"},
					FinalStage: currentStage,
					StageRuns:  cloneAttemptMap(stageRuns),
				}, nil
			}

			switch transition.Action {
			case "retry":
				if attempt >= maxAttempts {
					result.Reason = strings.Join(attemptFailures, "\n")
					return ExecutorPipelineResult{
						Result:     finalizeResult(result, p.Clock),
						FinalStage: currentStage,
						StageRuns:  cloneAttemptMap(stageRuns),
					}, nil
				}
				delay := executorRetryDelay(stageCfg.Retry, attempt+1)
				if err := p.Wait(ctx, delay); err != nil {
					return ExecutorPipelineResult{}, err
				}
				continue
			case "fail":
				result.Reason = strings.Join(attemptFailures, "\n")
				return ExecutorPipelineResult{
					Result:     finalizeResult(result, p.Clock),
					FinalStage: currentStage,
					StageRuns:  cloneAttemptMap(stageRuns),
				}, nil
			default:
				return ExecutorPipelineResult{}, fmt.Errorf("unknown transition action %q", transition.Action)
			}
		}
	}
}

func (p *ExecutorPipeline) emit(ctx context.Context, taskID string, eventType contracts.EventType, metadata map[string]string) {
	if p.EventSink == nil {
		return
	}
	_ = p.EventSink.Emit(ctx, contracts.Event{
		Type:      eventType,
		TaskID:    taskID,
		Metadata:  cloneStringMap(metadata),
		Timestamp: p.Clock(),
	})
}

func (p *ExecutorPipeline) emitTransition(ctx context.Context, taskID string, stage string, branch string, transition ExecutorConfigTransition, condition bool, attempt int) {
	if p.EventSink == nil {
		return
	}
	metadata := map[string]string{
		"from_stage":   stage,
		"to_stage":     transition.NextStage,
		"action":       transition.Action,
		"branch":       branch,
		"condition":    transition.Condition,
		"condition_ok": strconv.FormatBool(condition),
		"attempt":      strconv.Itoa(attempt),
	}
	_ = p.EventSink.Emit(ctx, contracts.Event{
		Type:      EventTypeExecutorPipelineTransitionTaken,
		TaskID:    taskID,
		Metadata:  metadata,
		Timestamp: p.Clock(),
	})
}

func takeTransition(transition ExecutorConfigTransition) (ExecutorConfigTransition, error) {
	if strings.TrimSpace(transition.Action) == "" || strings.TrimSpace(transition.Condition) == "" {
		return ExecutorConfigTransition{}, fmt.Errorf("invalid transition configuration")
	}
	return transition, nil
}

func executeTransitionAction(action string, currentStage string, nextStage string) (string, bool, error) {
	switch strings.TrimSpace(action) {
	case "next":
		if strings.TrimSpace(nextStage) == "" {
			return "", false, fmt.Errorf("stage %q transition requires next_stage", currentStage)
		}
		return nextStage, false, nil
	case "complete":
		return "", true, nil
	case "fail", "retry":
		return "", false, fmt.Errorf("retry/fail transitions are handled by caller")
	default:
		return "", false, fmt.Errorf("unsupported transition action %q", action)
	}
}

func finalizeResult(result contracts.RunnerResult, now func() time.Time) contracts.RunnerResult {
	if result.StartedAt.IsZero() {
		result.StartedAt = now()
	}
	if result.FinishedAt.IsZero() {
		result.FinishedAt = now()
	}
	if result.Artifacts == nil {
		result.Artifacts = map[string]string{}
	}
	return result
}

func normalizeRunnerError(result *contracts.RunnerResult, err error) error {
	if err == nil {
		if result.Status == "" {
			result.Status = contracts.RunnerResultFailed
		}
		return nil
	}
	if strings.TrimSpace(result.Reason) == "" {
		result.Reason = strings.TrimSpace(err.Error())
	}
	if result.Status == "" || result.Status == contracts.RunnerResultCompleted {
		result.Status = contracts.RunnerResultFailed
	}
	return err
}

func evaluateExecutorCondition(condition string, metadata map[string]string) (bool, error) {
	expression := strings.TrimSpace(condition)
	if !executorConfigConditionPattern.MatchString(expression) {
		return false, fmt.Errorf("invalid transition condition %q", condition)
	}

	switch expression {
	case "true":
		return true, nil
	case "false":
		return false, nil
	case "tests_failed":
		return parseConditionFlag(metadata, "tests_failed")
	case "review_failed":
		return parseConditionFlag(metadata, "review_failed")
	}

	parts := strings.Fields(expression)
	if len(parts) != 3 || parts[0] != "quality_score" {
		return false, fmt.Errorf("unsupported condition %q", expression)
	}

	score, err := parseFloatMetadata(metadata, "quality_score")
	if err != nil {
		return false, fmt.Errorf("cannot evaluate %q: %w", expression, err)
	}

	rawThreshold := strings.TrimSpace(parts[2])
	if rawThreshold == "threshold" {
		rawThreshold = resolveThresholdValue(metadata)
		if strings.TrimSpace(rawThreshold) == "" {
			return false, fmt.Errorf("missing threshold value for condition %q", expression)
		}
	}
	threshold, err := strconv.ParseFloat(rawThreshold, 64)
	if err != nil {
		return false, fmt.Errorf("cannot parse threshold in condition %q: %w", expression, err)
	}

	switch parts[1] {
	case "==":
		return math.Abs(score-threshold) < 1e-9, nil
	case "!=":
		return score != threshold, nil
	case ">=":
		return score >= threshold, nil
	case "<=":
		return score <= threshold, nil
	case ">":
		return score > threshold, nil
	case "<":
		return score < threshold, nil
	}
	return false, fmt.Errorf("unsupported condition operator %q", parts[1])
}

func parseConditionFlag(metadata map[string]string, key string) (bool, error) {
	raw := strings.TrimSpace(metadata[key])
	if raw == "" {
		return false, nil
	}
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("cannot parse %s metadata value %q", key, raw)
	}
	return parsed, nil
}

func parseFloatMetadata(metadata map[string]string, key string) (float64, error) {
	raw := strings.TrimSpace(metadata[key])
	if raw == "" {
		return 0, fmt.Errorf("missing metadata %q", key)
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid metadata %q value %q", key, raw)
	}
	return value, nil
}

func resolveThresholdValue(metadata map[string]string) string {
	if value := strings.TrimSpace(metadata["quality_threshold"]); value != "" {
		return value
	}
	if value := strings.TrimSpace(metadata["qc_gate_threshold"]); value != "" {
		return value
	}
	return strings.TrimSpace(metadata["threshold"])
}

func executorRetryDelay(cfg ExecutorConfigRetry, attempt int) time.Duration {
	if attempt <= 1 {
		return 0
	}
	delayMs := cfg.InitialDelayMs
	if cfg.BackoffMs > 0 {
		delayMs += (attempt - 2) * cfg.BackoffMs
	}
	if cfg.MaxDelayMs > 0 && delayMs > cfg.MaxDelayMs {
		delayMs = cfg.MaxDelayMs
	}
	if delayMs < 0 {
		delayMs = 0
	}
	return time.Duration(delayMs) * time.Millisecond
}

func waitForContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func formatAttemptFailure(attempt int, reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "stage failed"
	}
	return fmt.Sprintf("Attempt %d failure: %s", attempt, reason)
}

func mergeMetadata(base map[string]string, extra map[string]string) map[string]string {
	merged := make(map[string]string, len(base)+len(extra))
	for key, value := range base {
		merged[key] = value
	}
	for key, value := range extra {
		merged[key] = value
	}
	return merged
}

func cloneAttemptMap(values map[string]int) map[string]int {
	clone := make(map[string]int, len(values))
	for key, value := range values {
		clone[key] = value
	}
	return clone
}
