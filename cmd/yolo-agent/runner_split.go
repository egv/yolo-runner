package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/egv/yolo-runner/v2/internal/agent/splitter"
	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/envpreset"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

type runnerSplitAgent struct {
	Runner   contracts.AgentRunner
	Agent    envpreset.ResolvedAgent
	RepoRoot string
}

type runnerSplitAgentResolver func(context.Context, workitem.Item) (runnerSplitAgent, error)

func newRunnerSplitHandler(agent contracts.AgentRunner) runnerKindHandler {
	return func(ctx context.Context, item workitem.Item, _ envpreset.Workspace) (workqueue.Result, error) {
		return runRunnerSplit(ctx, item, runnerSplitAgent{Runner: agent})
	}
}

func newRunnerSplitKindHandler(resolve runnerSplitAgentResolver) runnerKindHandler {
	if resolve == nil {
		resolve = defaultRunnerSplitAgentResolver
	}
	return func(ctx context.Context, item workitem.Item, _ envpreset.Workspace) (workqueue.Result, error) {
		resolved, err := resolve(ctx, item)
		if err != nil {
			return workqueue.Result{}, err
		}
		if resolved.Runner == nil {
			return workqueue.Result{}, fmt.Errorf("split runner for preset %q is nil", item.Preset)
		}
		return runRunnerSplit(ctx, item, resolved)
	}
}

func runRunnerSplit(ctx context.Context, item workitem.Item, resolved runnerSplitAgent) (workqueue.Result, error) {
	if item.Kind != workitem.KindSplit {
		return workqueue.Result{}, fmt.Errorf("split handler received kind %q", item.Kind)
	}
	if resolved.Runner == nil {
		return workqueue.Result{}, fmt.Errorf("split runner for preset %q is nil", item.Preset)
	}

	var payload workitem.SplitPayload
	if err := json.Unmarshal(item.Payload, &payload); err != nil {
		return workqueue.Result{}, fmt.Errorf("decode split payload for item %q: %w", item.ID, err)
	}

	input := payload.ToRunInput()
	input.Model = resolved.Agent.Model
	input.RepoRoot = strings.TrimSpace(resolved.RepoRoot)
	input.Timeout = resolved.Agent.RunnerTimeout
	input.Metadata = runnerSplitMetadata(item, resolved.Agent)

	output, err := splitter.NewRunner(resolved.Runner).Run(ctx, input)
	if err != nil {
		return workqueue.Result{}, err
	}

	resultPayload, err := json.Marshal(workitem.SplitResultFromStrictOutput(output))
	if err != nil {
		return workqueue.Result{}, fmt.Errorf("marshal split result for item %q: %w", item.ID, err)
	}
	return workqueue.Result{Payload: resultPayload}, nil
}

func defaultRunnerSplitAgentResolver(_ context.Context, item workitem.Item) (runnerSplitAgent, error) {
	presets, err := envpreset.Load(defaultRunnerDaemonEnvironmentsPath)
	if err != nil {
		return runnerSplitAgent{}, err
	}
	return resolveRunnerSplitAgentFromPresets(item, presets, defaultRunnerDaemonEnvironmentsPath)
}

func newRunnerSplitAgentResolverForPresets(presets map[string]envpreset.Preset) runnerSplitAgentResolver {
	return func(_ context.Context, item workitem.Item) (runnerSplitAgent, error) {
		return resolveRunnerSplitAgentFromPresets(item, presets, "runner environments")
	}
}

func resolveRunnerSplitAgentFromPresets(item workitem.Item, presets map[string]envpreset.Preset, source string) (runnerSplitAgent, error) {
	presetName := strings.TrimSpace(item.Preset)
	preset, ok := presets[presetName]
	if !ok {
		return runnerSplitAgent{}, fmt.Errorf("preset %q not found in %s", presetName, source)
	}

	resolvedAgent, err := envpreset.ResolveAgent(preset)
	if err != nil {
		return runnerSplitAgent{}, err
	}

	catalog, err := loadCodingAgentsCatalog("")
	if err != nil {
		return runnerSplitAgent{}, err
	}
	runner, err := buildAgentRunner(catalog, resolvedAgent.Backend, resolvedAgent.Model, resolvedAgent.RunnerTimeout)
	if err != nil {
		return runnerSplitAgent{}, err
	}

	return runnerSplitAgent{
		Runner:   runner,
		Agent:    resolvedAgent,
		RepoRoot: runnerPreflightRepoRoot(preset),
	}, nil
}

func runnerSplitMetadata(item workitem.Item, agent envpreset.ResolvedAgent) map[string]string {
	return map[string]string{
		"phase":      "split",
		"item_id":    strings.TrimSpace(item.ID),
		"preset":     strings.TrimSpace(item.Preset),
		"source":     strings.TrimSpace(item.Source),
		"source_ref": strings.TrimSpace(item.SourceRef),
		"backend":    strings.TrimSpace(agent.Backend),
	}
}
