package preflight

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

type RunInput struct {
	Task       contracts.Task
	Comments   []Comment
	QueueRoot  contracts.Task
	Model      string
	RepoRoot   string
	Timeout    time.Duration
	MaxRetries int
	Metadata   map[string]string
	OnProgress func(contracts.RunnerProgress)
}

type Runner struct {
	agent contracts.AgentRunner
}

func NewRunner(agent contracts.AgentRunner) *Runner {
	return &Runner{agent: agent}
}

func (r *Runner) Run(ctx context.Context, input RunInput) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if r == nil || r.agent == nil {
		return Result{Decision: DecisionNeedsInfo}, errors.New("nil preflight runner")
	}

	var outputMu sync.Mutex
	var output strings.Builder
	request := contracts.RunnerRequest{
		TaskID:     input.Task.ID,
		ParentID:   preflightParentID(input),
		Prompt:     BuildPrompt(BuildPromptInput{Task: input.Task, Comments: input.Comments, QueueRoot: input.QueueRoot}),
		Mode:       contracts.RunnerModeReview,
		Model:      input.Model,
		RepoRoot:   input.RepoRoot,
		Timeout:    input.Timeout,
		MaxRetries: input.MaxRetries,
		Metadata:   cloneMetadata(input.Metadata),
		OnProgress: func(progress contracts.RunnerProgress) {
			progress, ok := normalizeAgentProgress(progress)
			if !ok {
				return
			}
			if input.OnProgress != nil {
				input.OnProgress(progress)
			}
			if progress.Type != string(contracts.EventTypeAgentText) {
				return
			}
			if progress.Metadata != nil && strings.EqualFold(strings.TrimSpace(progress.Metadata["source"]), "stderr") {
				return
			}
			message := progress.Message
			if message == "" {
				return
			}
			outputMu.Lock()
			defer outputMu.Unlock()
			output.WriteString(message)
		},
	}

	result, err := r.agent.Run(ctx, request)
	if err != nil {
		return Result{Decision: DecisionNeedsInfo}, err
	}
	if result.Status != "" && result.Status != contracts.RunnerResultCompleted {
		return Result{Decision: DecisionNeedsInfo}, fmt.Errorf("preflight runner finished with status %s: %s", result.Status, strings.TrimSpace(result.Reason))
	}
	outputMu.Lock()
	capturedOutput := output.String()
	outputMu.Unlock()
	return parseRunnerOutput(capturedOutput), nil
}

func normalizeAgentProgress(progress contracts.RunnerProgress) (contracts.RunnerProgress, bool) {
	switch contracts.EventType(strings.TrimSpace(progress.Type)) {
	case contracts.EventTypeAgentText:
		progress.Type = string(contracts.EventTypeAgentText)
	case contracts.EventTypeAgentBlocked:
		progress.Type = string(contracts.EventTypeAgentBlocked)
	case contracts.EventTypeToolInvoked:
		return contracts.RunnerProgress{}, false
	case contracts.EventTypeCommandRun:
		progress.Type = string(contracts.EventTypeCommandRun)
	case contracts.EventTypeAgentProgress:
		progress.Type = string(contracts.EventTypeAgentProgress)
	case contracts.EventTypeAgentHeartbeat:
		progress.Type = string(contracts.EventTypeAgentHeartbeat)
	}
	return progress, true
}

func preflightParentID(input RunInput) string {
	if strings.TrimSpace(input.QueueRoot.ID) != "" {
		return input.QueueRoot.ID
	}
	return input.Task.ParentID
}

func parseRunnerOutput(output string) Result {
	output = strings.TrimSpace(output)
	if output == "" {
		return Result{Decision: DecisionNeedsInfo}
	}
	if json.Valid([]byte(output)) {
		return ParseResult(output)
	}
	if compacted := compactLineDelimitedJSONTokens(output); json.Valid([]byte(compacted)) {
		return ParseResult(compacted)
	}
	if candidate, ok := lastJSONObject(output); ok {
		return ParseResult(candidate)
	}
	return ParseResult(output)
}

func compactLineDelimitedJSONTokens(output string) string {
	lines := strings.Split(output, "\n")
	var compacted strings.Builder
	for _, line := range lines {
		compacted.WriteString(strings.TrimSuffix(line, "\r"))
	}
	return compacted.String()
}

func lastJSONObject(output string) (string, bool) {
	starts := make([]int, 0)
	ends := make([]int, 0)
	for i, r := range output {
		switch r {
		case '{':
			starts = append(starts, i)
		case '}':
			ends = append(ends, i)
		}
	}
	for i := len(starts) - 1; i >= 0; i-- {
		for j := len(ends) - 1; j >= 0; j-- {
			if ends[j] <= starts[i] {
				continue
			}
			candidate := strings.TrimSpace(output[starts[i] : ends[j]+1])
			if json.Valid([]byte(candidate)) {
				return candidate, true
			}
		}
	}
	return "", false
}

func cloneMetadata(metadata map[string]string) map[string]string {
	if len(metadata) == 0 {
		return nil
	}
	clone := make(map[string]string, len(metadata))
	for key, value := range metadata {
		clone[key] = value
	}
	return clone
}
