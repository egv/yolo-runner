package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/egv/yolo-runner/v2/internal/envpreset"
	"github.com/egv/yolo-runner/v2/internal/executor"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

func newRunnerFinalizeKindHandler() runnerKindHandler {
	return func(ctx context.Context, item workitem.Item, workspace envpreset.Workspace) (workqueue.Result, error) {
		payload, err := workitem.DecodeFinalizePayload(item.Payload)
		if err != nil {
			return workqueue.Result{}, fmt.Errorf("decode finalize payload for item %q: %w", item.ID, err)
		}

		exec := &executor.Executor{
			RepoRoot: strings.TrimSpace(workspace.Path),
			VCS:      workspace.VCS,
			WorkerID: strings.TrimSpace(item.ClaimedBy),
			Priority: item.Priority,
		}
		finalizeResult, err := exec.Finalize(ctx, payload)
		if err != nil {
			return workqueue.Result{}, err
		}
		resultPayload, err := json.Marshal(finalizeResult)
		if err != nil {
			return workqueue.Result{}, fmt.Errorf("encode finalize result for item %q: %w", item.ID, err)
		}
		return workqueue.Result{
			Status:  workqueue.ResultStatusCompleted,
			Payload: resultPayload,
		}, nil
	}
}
