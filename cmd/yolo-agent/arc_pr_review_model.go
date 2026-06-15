package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
		Prompt:     arcreview.BuildReviewRevisionPrompt(input.State, input.ProjectContext),
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
			// Streamed model deltas are whitespace-significant fragments that
			// must be concatenated verbatim — inserting a newline between them
			// corrupts JSON (a string value split across two deltas would gain
			// a raw '\n'). Only line-oriented output gets a newline separator.
			preserveWhitespace := progress.Metadata != nil &&
				strings.EqualFold(strings.TrimSpace(progress.Metadata["preserve_whitespace"]), "true")
			if output.Len() > 0 && !preserveWhitespace {
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
	raw := append([]byte(nil), output.Bytes()...)
	// Optional dry-run/debug hook: persist the raw model output so a review can
	// be inspected even if downstream parsing or application fails.
	if dir := strings.TrimSpace(os.Getenv("YOLO_PRREVIEW_RAW_DUMP_DIR")); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
		_ = os.WriteFile(filepath.Join(dir, arcPRReviewModelTaskID(input.State)+".out"), raw, 0o644)
	}
	return raw, nil
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
