package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/egv/yolo-runner/v2/internal/arcanum"
	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/envpreset"
	"github.com/egv/yolo-runner/v2/internal/executor"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

// runnerImplementPreparePRCheckout is a seam over arcanum.PreparePRCheckout so
// the author-mode (arcpr-author) branch can be tested without a real arc mount.
var runnerImplementPreparePRCheckout = arcanum.PreparePRCheckout

// runnerImplementPRLanding captures the per-PR checkout used when an implement
// item targets an existing Arc PR (metadata origin == "arcpr-author"). When
// disabled, the handler uses the normal materialized workspace + task branch.
type runnerImplementPRLanding struct {
	enabled   bool
	prID      string
	mountPath string
	cleanupFn func() error
}

func (l runnerImplementPRLanding) cleanup() {
	if l.cleanupFn != nil {
		_ = l.cleanupFn()
	}
}

// resolveRunnerImplementPRLanding prepares a per-PR checkout for author-mode
// implement items so their fixes land on the existing PR branch (via
// push_existing_pr) instead of creating a new task branch off main.
func resolveRunnerImplementPRLanding(payload workitem.ImplementPayload, itemID string) (runnerImplementPRLanding, error) {
	none := runnerImplementPRLanding{}
	meta := payload.PromptContext.Metadata
	if strings.TrimSpace(meta["origin"]) != "arcpr-author" {
		return none, nil
	}
	prID := strings.TrimSpace(meta["arc_pr_id"])
	if prID == "" {
		return none, fmt.Errorf("arcpr-author implement item %q missing arc_pr_id metadata", itemID)
	}
	checkout, err := runnerImplementPreparePRCheckout(prID)
	if err != nil {
		return none, fmt.Errorf("prepare arc PR checkout for %q: %w", prID, err)
	}
	mountPath := strings.TrimSpace(checkout.MountPath)
	if mountPath == "" {
		if checkout.Cleanup != nil {
			_ = checkout.Cleanup()
		}
		return none, fmt.Errorf("arc PR checkout for %q returned empty mount path", prID)
	}
	return runnerImplementPRLanding{
		enabled:   true,
		prID:      prID,
		mountPath: mountPath,
		cleanupFn: checkout.Cleanup,
	}, nil
}

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
		if workspace.VCS == nil {
			return workqueue.Result{}, fmt.Errorf("implement item %q requires an isolated VCS-bearing workspace; preset %q materialized none (path strategy is not allowed for code-writing kinds)", item.ID, item.Preset)
		}
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

		prLanding, err := resolveRunnerImplementPRLanding(payload, item.ID)
		if err != nil {
			return workqueue.Result{}, err
		}
		defer prLanding.cleanup()

		repoRoot := strings.TrimSpace(workspace.Path)
		landingMode := ""
		mergeOnSuccess := runnerImplementShouldLand(resolved.Landing)
		prIDForLanding := ""
		if prLanding.enabled {
			// Author-mode implement items land on the existing PR branch via a
			// per-PR checkout + push_existing_pr, not a task branch off main.
			repoRoot = prLanding.mountPath
			landingMode = executor.LandingModePushExistingPR
			mergeOnSuccess = true
			prIDForLanding = prLanding.prID
		}

		exec := &executor.Executor{
			Runner:            resolved.Runner,
			Events:            resolved.Events,
			RepoRoot:          repoRoot,
			VCS:               workspace.VCS,
			ParentID:          strings.TrimSpace(payload.PromptContext.ParentID),
			Backend:           resolved.Agent.Backend,
			Model:             resolved.Agent.Model,
			RunnerTimeout:     resolved.Agent.RunnerTimeout,
			WatchdogTimeout:   resolved.Agent.WatchdogTimeout,
			WatchdogInterval:  resolved.Agent.WatchdogInterval,
			MaxRetries:        runnerImplementMaxRetries(item),
			RequireReview:     true,
			MergeOnSuccess:    mergeOnSuccess,
			LandingMode:       landingMode,
			PRIDForLanding:    prIDForLanding,
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
	presets, err := envpreset.Load(defaultRunnerDaemonEnvironmentsPath)
	if err != nil {
		return runnerImplementExecutor{}, err
	}
	return resolveRunnerImplementExecutorFromPresets(item, presets, defaultRunnerDaemonEnvironmentsPath)
}

func newRunnerImplementExecutorResolverForPresets(presets map[string]envpreset.Preset, events contracts.EventSink) runnerImplementExecutorResolver {
	return func(_ context.Context, item workitem.Item, _ envpreset.Workspace) (runnerImplementExecutor, error) {
		resolved, err := resolveRunnerImplementExecutorFromPresets(item, presets, "runner environments")
		if err != nil {
			return runnerImplementExecutor{}, err
		}
		resolved.Events = events
		return resolved, nil
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
