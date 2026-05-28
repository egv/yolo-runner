package splitter

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

type RunInput struct {
	Task       contracts.Task
	QueueRoot  contracts.Task
	Model      string
	RepoRoot   string
	Timeout    time.Duration
	MaxRetries int
	Metadata   map[string]string
}

type Runner struct {
	agent contracts.AgentRunner
}

func NewRunner(agent contracts.AgentRunner) *Runner {
	return &Runner{agent: agent}
}

func (r *Runner) Run(ctx context.Context, input RunInput) (StrictOutput, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if r == nil || r.agent == nil {
		return StrictOutput{}, errors.New("nil splitter runner")
	}

	var outputMu sync.Mutex
	var output strings.Builder
	request := contracts.RunnerRequest{
		TaskID:     input.Task.ID,
		ParentID:   splitterParentID(input),
		Prompt:     buildStrictSplitterPrompt(input),
		Mode:       contracts.RunnerModeReview,
		Model:      input.Model,
		RepoRoot:   input.RepoRoot,
		Timeout:    input.Timeout,
		MaxRetries: input.MaxRetries,
		Metadata:   cloneRunMetadata(input.Metadata),
		OnProgress: func(progress contracts.RunnerProgress) {
			if progress.Type != string(contracts.EventTypeRunnerOutput) {
				return
			}
			if progress.Metadata != nil && strings.EqualFold(strings.TrimSpace(progress.Metadata["source"]), "stderr") {
				return
			}
			message := strings.TrimSpace(progress.Message)
			if message == "" {
				return
			}

			outputMu.Lock()
			defer outputMu.Unlock()
			if output.Len() > 0 {
				output.WriteByte('\n')
			}
			output.WriteString(message)
		},
	}

	result, err := r.agent.Run(ctx, request)
	if err != nil {
		return StrictOutput{}, err
	}
	if result.Status != "" && result.Status != contracts.RunnerResultCompleted {
		return StrictOutput{}, fmt.Errorf("splitter runner finished with status %s: %s", result.Status, strings.TrimSpace(result.Reason))
	}

	outputMu.Lock()
	capturedOutput := output.String()
	outputMu.Unlock()

	parsed, err := ParseStrictOutput(capturedOutput)
	if err != nil {
		return StrictOutput{}, fmt.Errorf("parse strict splitter output: %w", err)
	}
	return parsed, nil
}

func splitterParentID(input RunInput) string {
	if strings.TrimSpace(input.QueueRoot.ID) != "" {
		return input.QueueRoot.ID
	}
	return input.Task.ParentID
}

func buildStrictSplitterPrompt(input RunInput) string {
	return strings.Join([]string{
		"Run the bundled strict task splitter.",
		"Invoke the `split-tasks-strict` command or load the `task-splitting` skill through the configured runner.",
		"Return only the strict splitter markdown. Do not edit files, create Tracker tasks, update task status, commit, or push.",
		"Use aggressive micro-splitting: one seam per task, one strict red-green loop per task, explicit dependencies, and only the next intended task ready.",
		taskPromptSection(input.Task),
		queueRootPromptSection(input.QueueRoot),
		requiredOutputPromptSection(),
	}, "\n\n")
}

func taskPromptSection(task contracts.Task) string {
	return strings.Join([]string{
		"Task to split:",
		"ID: " + promptFallback(task.ID),
		"Title: " + promptFallback(task.Title),
		"Status: " + promptFallback(string(task.Status)),
		"Parent ID: " + promptFallback(task.ParentID),
		"",
		"Description:",
		promptFallback(task.Description),
	}, "\n")
}

func queueRootPromptSection(root contracts.Task) string {
	return strings.Join([]string{
		"Queue root:",
		"ID: " + promptFallback(root.ID),
		"Title: " + promptFallback(root.Title),
		"Status: " + promptFallback(string(root.Status)),
		"",
		"Description:",
		promptFallback(root.Description),
	}, "\n")
}

func requiredOutputPromptSection() string {
	return strings.Join([]string{
		"Required output:",
		"- ## Epics",
		"- ## Tasks",
		"- ## Order",
		"- ## Risk notes",
		"- One strict task template for every task, using all required sections from the task-splitting skill.",
	}, "\n")
}

func cloneRunMetadata(metadata map[string]string) map[string]string {
	if len(metadata) == 0 {
		return nil
	}
	clone := make(map[string]string, len(metadata))
	for key, value := range metadata {
		clone[key] = value
	}
	return clone
}

func promptFallback(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "None"
	}
	return value
}
