package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/egv/yolo-runner/v2/internal/agent/splitter"
	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

func newRunnerSplitHandler(agent contracts.AgentRunner) runnerKindHandler {
	return func(ctx context.Context, item workitem.Item) (workqueue.Result, error) {
		return runRunnerSplit(ctx, agent, item)
	}
}

func runRunnerSplit(ctx context.Context, agent contracts.AgentRunner, item workitem.Item) (workqueue.Result, error) {
	if item.Kind != workitem.KindSplit {
		return workqueue.Result{}, fmt.Errorf("split handler received kind %q", item.Kind)
	}

	var payload workitem.SplitPayload
	if err := json.Unmarshal(item.Payload, &payload); err != nil {
		return workqueue.Result{}, fmt.Errorf("decode split payload for item %q: %w", item.ID, err)
	}

	output, err := splitter.NewRunner(agent).Run(ctx, payload.ToRunInput())
	if err != nil {
		return workqueue.Result{}, err
	}

	resultPayload, err := json.Marshal(workitem.SplitResultFromStrictOutput(output))
	if err != nil {
		return workqueue.Result{}, fmt.Errorf("marshal split result for item %q: %w", item.ID, err)
	}
	return workqueue.Result{Payload: resultPayload}, nil
}
