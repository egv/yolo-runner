package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/egv/yolo-runner/v2/internal/agent/preflight"
	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/envpreset"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

type runnerPreflightAgent struct {
	Runner   contracts.AgentRunner
	Agent    envpreset.ResolvedAgent
	RepoRoot string
}

type runnerPreflightAgentResolver func(context.Context, workitem.Item) (runnerPreflightAgent, error)

func newRunnerPreflightKindHandler(resolve runnerPreflightAgentResolver) runnerKindHandler {
	if resolve == nil {
		resolve = defaultRunnerPreflightAgentResolver
	}
	return func(ctx context.Context, item workitem.Item, _ envpreset.Workspace) (workqueue.Result, error) {
		var payload workitem.PreflightPayload
		if err := json.Unmarshal(item.Payload, &payload); err != nil {
			return workqueue.Result{}, fmt.Errorf("decode preflight payload for item %q: %w", item.ID, err)
		}

		resolved, err := resolve(ctx, item)
		if err != nil {
			return workqueue.Result{}, err
		}
		if resolved.Runner == nil {
			return workqueue.Result{}, fmt.Errorf("preflight runner for preset %q is nil", item.Preset)
		}

		input := payload.ToRunInput()
		input.Model = resolved.Agent.Model
		input.RepoRoot = strings.TrimSpace(resolved.RepoRoot)
		input.Timeout = resolved.Agent.RunnerTimeout
		input.Metadata = runnerPreflightMetadata(item, resolved.Agent)

		result, err := preflight.NewRunner(resolved.Runner).Run(ctx, input)
		if err != nil {
			return workqueue.Result{}, err
		}
		resultPayload, err := json.Marshal(workitem.PreflightResultFromResult(result))
		if err != nil {
			return workqueue.Result{}, fmt.Errorf("encode preflight result for item %q: %w", item.ID, err)
		}
		return workqueue.Result{Payload: resultPayload}, nil
	}
}

func defaultRunnerPreflightAgentResolver(_ context.Context, item workitem.Item) (runnerPreflightAgent, error) {
	environmentsPath := defaultRunnerDaemonEnvironmentsPath
	presets, err := envpreset.Load(environmentsPath)
	if err != nil {
		return runnerPreflightAgent{}, err
	}

	presetName := strings.TrimSpace(item.Preset)
	preset, ok := presets[presetName]
	if !ok {
		return runnerPreflightAgent{}, fmt.Errorf("preset %q not found in %s", presetName, environmentsPath)
	}

	resolvedAgent, err := envpreset.ResolveAgent(preset)
	if err != nil {
		return runnerPreflightAgent{}, err
	}

	catalog, err := loadCodingAgentsCatalog("")
	if err != nil {
		return runnerPreflightAgent{}, err
	}
	runner, err := buildAgentRunner(catalog, resolvedAgent.Backend, resolvedAgent.Model, resolvedAgent.RunnerTimeout)
	if err != nil {
		return runnerPreflightAgent{}, err
	}

	return runnerPreflightAgent{
		Runner:   runner,
		Agent:    resolvedAgent,
		RepoRoot: runnerPreflightRepoRoot(preset),
	}, nil
}

func runnerPreflightRepoRoot(preset envpreset.Preset) string {
	switch preset.Workspace.Strategy {
	case envpreset.WorkspaceStrategyPath:
		return strings.TrimSpace(preset.Workspace.Path)
	case envpreset.WorkspaceStrategyArcShared:
		return filepath.Join(strings.TrimSpace(preset.Workspace.Mount), strings.TrimSpace(preset.Workspace.Subpath))
	case envpreset.WorkspaceStrategyGitClone:
		return strings.TrimSpace(preset.Workspace.Origin)
	default:
		return ""
	}
}

func runnerPreflightMetadata(item workitem.Item, agent envpreset.ResolvedAgent) map[string]string {
	return map[string]string{
		"phase":      "preflight",
		"item_id":    strings.TrimSpace(item.ID),
		"preset":     strings.TrimSpace(item.Preset),
		"source":     strings.TrimSpace(item.Source),
		"source_ref": strings.TrimSpace(item.SourceRef),
		"backend":    strings.TrimSpace(agent.Backend),
	}
}
