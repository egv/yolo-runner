package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/envpreset"
	"github.com/egv/yolo-runner/v2/internal/executor"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

type runnerImplementExecutor struct {
	Runner  contracts.AgentRunner
	Agent   envpreset.ResolvedAgent
	Landing envpreset.LandingType
	Events  contracts.EventSink
}

type runnerImplementExecutorResolver func(context.Context, workitem.Item, envpreset.Workspace) (runnerImplementExecutor, error)

func newRunnerImplementKindHandler(resolve runnerImplementExecutorResolver) runnerKindHandler {
	if resolve == nil {
		resolve = defaultRunnerImplementExecutorResolver
	}
	return func(ctx context.Context, item workitem.Item, workspace envpreset.Workspace) (workqueue.Result, error) {
		payload, err := workitem.DecodeImplementPayload(item.Payload)
		if err != nil {
			return workqueue.Result{}, fmt.Errorf("decode implement payload for item %q: %w", item.ID, err)
		}

		resolved, err := resolve(ctx, item, workspace)
		if err != nil {
			return workqueue.Result{}, err
		}
		if resolved.Runner == nil {
			return workqueue.Result{}, fmt.Errorf("implement runner for preset %q is nil", item.Preset)
		}

		exec := &executor.Executor{
			Runner:            resolved.Runner,
			Events:            resolved.Events,
			RepoRoot:          strings.TrimSpace(workspace.Path),
			VCS:               workspace.VCS,
			ParentID:          strings.TrimSpace(payload.PromptContext.ParentID),
			Backend:           resolved.Agent.Backend,
			Model:             resolved.Agent.Model,
			RunnerTimeout:     resolved.Agent.RunnerTimeout,
			WatchdogTimeout:   resolved.Agent.WatchdogTimeout,
			WatchdogInterval:  resolved.Agent.WatchdogInterval,
			MaxRetries:        runnerImplementMaxRetries(item),
			RequireReview:     true,
			MergeOnSuccess:    runnerImplementShouldLand(resolved.Landing),
			WorkerID:          strings.TrimSpace(item.ClaimedBy),
			Priority:          item.Priority,
			QualityGateTools:  nil,
			QCGateTools:       nil,
			AllowLowQuality:   false,
			HeartbeatInterval: 0,
		}

		implementResult, err := exec.Execute(ctx, payload)
		if err != nil {
			return workqueue.Result{}, err
		}
		resultPayload, err := json.Marshal(implementResult)
		if err != nil {
			return workqueue.Result{}, fmt.Errorf("encode implement result for item %q: %w", item.ID, err)
		}
		return workqueue.Result{
			Status:  runnerImplementResultStatus(implementResult.Status),
			Payload: resultPayload,
			LogPath: runnerImplementFirstNonEmpty(implementResult.Artifacts["log_path"], implementResult.Artifacts["review_log_path"]),
		}, nil
	}
}

func defaultRunnerImplementExecutorResolver(_ context.Context, item workitem.Item, _ envpreset.Workspace) (runnerImplementExecutor, error) {
	presets, err := envpreset.Load(defaultRunnerEnvironmentsPath)
	if err != nil {
		return runnerImplementExecutor{}, err
	}
	return resolveRunnerImplementExecutorFromPresets(item, presets, defaultRunnerEnvironmentsPath)
}

func newRunnerImplementExecutorResolverForPresets(presets map[string]envpreset.Preset) runnerImplementExecutorResolver {
	return func(_ context.Context, item workitem.Item, _ envpreset.Workspace) (runnerImplementExecutor, error) {
		return resolveRunnerImplementExecutorFromPresets(item, presets, "runner environments")
	}
}

func resolveRunnerImplementExecutorFromPresets(item workitem.Item, presets map[string]envpreset.Preset, source string) (runnerImplementExecutor, error) {
	presetName := strings.TrimSpace(item.Preset)
	preset, ok := presets[presetName]
	if !ok {
		return runnerImplementExecutor{}, fmt.Errorf("preset %q not found in %s", presetName, source)
	}

	resolvedAgent, err := envpreset.ResolveAgent(preset)
	if err != nil {
		return runnerImplementExecutor{}, err
	}
	landing, err := envpreset.ResolveLanding(preset)
	if err != nil {
		return runnerImplementExecutor{}, err
	}

	catalog, err := loadCodingAgentsCatalog("")
	if err != nil {
		return runnerImplementExecutor{}, err
	}
	runner, err := buildAgentRunner(catalog, resolvedAgent.Backend, resolvedAgent.Model, resolvedAgent.RunnerTimeout)
	if err != nil {
		return runnerImplementExecutor{}, err
	}

	return runnerImplementExecutor{
		Runner:  runner,
		Agent:   resolvedAgent,
		Landing: landing,
	}, nil
}

func runnerImplementMaxRetries(item workitem.Item) int {
	if item.MaxAttempts <= 1 {
		return 0
	}
	return item.MaxAttempts - 1
}

func runnerImplementShouldLand(landing envpreset.LandingType) bool {
	switch landing {
	case envpreset.LandingTypeGitMerge, envpreset.LandingTypeArcPR:
		return true
	default:
		return false
	}
}

func runnerImplementResultStatus(status string) workqueue.ResultStatus {
	switch contracts.RunnerResultStatus(strings.TrimSpace(status)) {
	case contracts.RunnerResultBlocked:
		return workqueue.ResultStatusBlocked
	case contracts.RunnerResultFailed:
		return workqueue.ResultStatusFailed
	default:
		return workqueue.ResultStatusCompleted
	}
}

func runnerImplementFirstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
