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
	OnProgress func(contracts.RunnerProgress)
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
			if shouldPreserveSplitterOutputWhitespace(progress.Metadata) {
				output.WriteString(progress.Message)
				return
			}

			message := strings.TrimSpace(progress.Message)
			if message == "" {
				return
			}
			output.WriteString(message)
			if !strings.HasSuffix(message, "\n") {
				output.WriteByte('\n')
			}
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

func shouldPreserveSplitterOutputWhitespace(metadata map[string]string) bool {
	if metadata == nil {
		return false
	}
	value := strings.TrimSpace(metadata["preserve_whitespace"])
	return strings.EqualFold(value, "true") || value == "1"
}

func buildStrictSplitterPrompt(input RunInput) string {
	return strings.Join([]string{
		"Run the bundled strict task splitter using only the instructions in this prompt.",
		"No external command, skill, file, or repository context is required. Do not run shell commands, inspect files, or search the filesystem. If context is missing, record the gap in Risk notes instead of looking it up.",
		"Return only the strict splitter markdown. Do not edit files, create Tracker tasks, update task status, commit, or push.",
		"Use aggressive micro-splitting: one seam per task, one strict red-green loop per task, explicit dependencies, and only the next intended task ready.",
		strictSplitterRulesPromptSection(),
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

func strictSplitterRulesPromptSection() string {
	return strings.Join([]string{
		"Strict splitter rules:",
		"- Every task must have exactly one primary seam and exactly one strict red-green loop.",
		"- Every task must have a narrow stop condition, explicit out-of-scope boundaries, expected files or subsystem touched, and explicit dependencies.",
		"- Split again if a task mixes implementation, docs, integration wiring, e2e, multiple abstractions, multiple test types, or multiple subsystem boundaries.",
		"- Prefer helper-first decomposition before wiring, happy-path slices before fallback/teardown/e2e, and 1-3 production files plus 1-2 test files per task.",
		"- When unsure, split smaller.",
		"",
		"Required task template for each task:",
		"### Task: <task id> <title>",
		"",
		"Why:",
		"- <one sentence>",
		"",
		"In scope:",
		"- <specific behavior>",
		"- <specific seam>",
		"",
		"Out of scope:",
		"- <explicit exclusions>",
		"",
		"Strict TDD:",
		"1. Add or update one targeted failing test first",
		"2. Run the targeted test and confirm it fails for the intended reason",
		"3. Implement the minimum production change needed to make it pass",
		"4. Re-run the targeted test",
		"5. Run one narrow follow-up verification command",
		"",
		"Done when:",
		"- <specific test or command passes>",
		"- <specific behavior is verified>",
		"",
		"Expected files:",
		"- <prod files>",
		"- <test files>",
		"",
		"Depends on:",
		"- <task IDs or none>",
		"",
		"Unlocks:",
		"- <task IDs or none>",
	}, "\n")
}

func requiredOutputPromptSection() string {
	return strings.Join([]string{
		"Required output:",
		"- ## Epics",
		"- ## Tasks containing only summary list items in the exact form `- <task id>: <title>`",
		"- ## Order",
		"- ## Risk notes",
		"- After ## Risk notes, one full strict task template for every task, using `### Task: <task id> <title>` headings and the exact required task template above.",
		"- Do not place full task templates inside the ## Tasks summary section.",
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
