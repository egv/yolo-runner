package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/egv/yolo-runner/v2/internal/arcreview"
	"github.com/egv/yolo-runner/v2/internal/contracts"
)

func runArcPRReviewModel(ctx context.Context, runner contracts.AgentRunner, input arcPRReviewModelInput) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if runner == nil {
		return nil, errors.New("nil arc PR review model runner")
	}

	var outputMu sync.Mutex
	var output bytes.Buffer
	request := contracts.RunnerRequest{
		TaskID:     arcPRReviewModelTaskID(input.State),
		Prompt:     arcreview.BuildReviewRevisionPrompt(input.State),
		Mode:       contracts.RunnerModeReview,
		Model:      input.Model,
		RepoRoot:   input.RepoRoot,
		Timeout:    input.Timeout,
		MaxRetries: input.MaxRetries,
		Metadata:   cloneArcPRReviewModelMetadata(input.Metadata),
		OnProgress: func(progress contracts.RunnerProgress) {
			if input.OnProgress != nil {
				input.OnProgress(progress)
			}
			if progress.Type != string(contracts.EventTypeRunnerOutput) {
				return
			}
			if progress.Metadata != nil && strings.EqualFold(strings.TrimSpace(progress.Metadata["source"]), "stderr") {
				return
			}
			outputMu.Lock()
			defer outputMu.Unlock()
			if output.Len() > 0 {
				output.WriteByte('\n')
			}
			output.WriteString(progress.Message)
		},
	}

	result, err := runner.Run(ctx, request)
	if err != nil {
		return nil, err
	}
	if result.Status != "" && result.Status != contracts.RunnerResultCompleted {
		return nil, fmt.Errorf("arc PR review model runner finished with status %s: %s", result.Status, strings.TrimSpace(result.Reason))
	}

	outputMu.Lock()
	defer outputMu.Unlock()
	return append([]byte(nil), output.Bytes()...), nil
}

func arcPRReviewModelTaskID(state arcreview.PRRuntimeState) string {
	if id := strings.TrimSpace(state.PRID); id != "" {
		return id
	}
	return strings.TrimSpace(state.Details.ID)
}

func cloneArcPRReviewModelMetadata(metadata map[string]string) map[string]string {
	if len(metadata) == 0 {
		return nil
	}
	out := make(map[string]string, len(metadata))
	for key, value := range metadata {
		out[key] = value
	}
	return out
}
