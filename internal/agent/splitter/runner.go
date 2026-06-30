package splitter

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"

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
			progress = normalizeAgentProgress(progress)
			if input.OnProgress != nil {
				input.OnProgress(progress)
			}
			if progress.Type != string(contracts.EventTypeAgentText) {
				return
			}
			if progress.Metadata != nil && strings.EqualFold(strings.TrimSpace(progress.Metadata["source"]), "stderr") {
				return
			}
			outputMu.Lock()
			defer outputMu.Unlock()
			if progress.Message == "" {
				return
			}
			output.WriteString(progress.Message)
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

	parsed, err := ParseStrictJSONOutput(capturedOutput)
	if err != nil {
		return StrictOutput{}, fmt.Errorf("parse strict splitter output: %w", err)
	}
	return parsed, nil
}

func normalizeAgentProgress(progress contracts.RunnerProgress) contracts.RunnerProgress {
	switch contracts.EventType(strings.TrimSpace(progress.Type)) {
	case contracts.EventTypeRunnerOutput:
		progress.Type = string(contracts.EventTypeAgentText)
	case contracts.EventTypeRunnerWarning:
		progress.Type = string(contracts.EventTypeAgentBlocked)
	case contracts.EventTypeRunnerCommandStarted, contracts.EventTypeRunnerCommandFinished:
		progress.Type = string(contracts.EventTypeCommandRun)
	case contracts.EventTypeRunnerProgress:
		progress.Type = string(contracts.EventTypeAgentProgress)
	case contracts.EventTypeRunnerHeartbeat:
		progress.Type = string(contracts.EventTypeAgentHeartbeat)
	}
	return progress
}

func splitterParentID(input RunInput) string {
	if strings.TrimSpace(input.QueueRoot.ID) != "" {
		return input.QueueRoot.ID
	}
	return input.Task.ParentID
}

func buildStrictSplitterPrompt(input RunInput) string {
	return strings.Join([]string{
		"Run the bundled strict task splitter using only the instructions in this prompt.",
		"No external command, skill, file, or repository context is required. Do not run shell commands, inspect files, or search the filesystem. If context is missing, record the gap in Risk notes instead of looking it up.",
		"Return only valid JSON matching the required schema. Do not wrap the JSON in markdown or code fences. Do not edit files, create Tracker tasks, update task status, commit, or push.",
		"Use aggressive micro-splitting: one seam per task, one strict red-green loop per task, explicit dependencies, and only the next intended task ready.",
		strictSplitterRulesPromptSection(),
		outputLanguagePromptSection(input.Task),
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
		"- All fields that are arrays must be JSON arrays of strings or objects, never comma-delimited prose.",
		"- Use depends_on: [\"none\"] only for the next intended ready task. Later tasks should depend on earlier task IDs.",
		"- Use unlocks: [\"none\"] when a task unlocks no later split task.",
	}, "\n")
}

func outputLanguagePromptSection(task contracts.Task) string {
	return strings.Join([]string{
		"Output language:",
		"- Detected parent task language: " + detectedTaskLanguage(task) + ".",
		"- Write every generated epic name, epic goal, task title, why, in_scope, out_of_scope, strict_tdd, done_when, and risk_notes item in that language.",
		"- Preserve queue keys, task IDs, product names, API names, code identifiers, file paths, commands, labels, and quoted literals verbatim.",
		"- If the parent task title and description mix languages, prefer the human language used by the title.",
	}, "\n")
}

func requiredOutputPromptSection() string {
	return strings.Join([]string{
		"Required JSON response:",
		"Return only one JSON object with exactly these top-level fields:",
		`{"epics":[{"name":"<epic name>","goal":"<goal>"}],"tasks":[{"id":"<task id>","title":"<title>","why":["<one sentence>"],"in_scope":["<specific behavior>","<specific seam>"],"out_of_scope":["<explicit exclusions>"],"strict_tdd":["Add or update one targeted failing test first","Run the targeted test and confirm it fails for the intended reason","Implement the minimum production change needed to make it pass","Re-run the targeted test","Run one narrow follow-up verification command"],"done_when":["<specific test or command passes>","<specific behavior is verified>"],"expected_files":["<prod files>","<test files>"],"depends_on":["none"],"unlocks":["<task id>"]}],"order":[{"from":"<task id>","to":"<task id>"}],"risk_notes":["<risk or missing context>"]}`,
		"Schema rules:",
		"- epics must contain at least one object with name and goal.",
		"- tasks must contain at least one object and every task object must contain exactly: id, title, why, in_scope, out_of_scope, strict_tdd, done_when, expected_files, depends_on, unlocks.",
		"- order must be an array of dependency edge objects. Use [] when there are no edges. Do not write readiness prose such as Ready now or Blocked by.",
		"- risk_notes must contain at least one concrete risk string, or [\"none\"] when there are no known risks.",
		"- Do not include markdown headings, task templates, comments, trailing prose, or any field not shown in the schema.",
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

func detectedTaskLanguage(task contracts.Task) string {
	title := strings.TrimSpace(task.Title)
	description := strings.TrimSpace(task.Description)
	if containsCyrillic(title) || (title == "" && containsCyrillic(description)) {
		return "Russian"
	}
	if containsLatin(title) || (title == "" && containsLatin(description)) {
		return "English"
	}
	return "the same human language as the parent task"
}

func containsCyrillic(value string) bool {
	for _, r := range value {
		if unicode.In(r, unicode.Cyrillic) {
			return true
		}
	}
	return false
}

func containsLatin(value string) bool {
	for _, r := range value {
		if unicode.In(r, unicode.Latin) {
			return true
		}
	}
	return false
}

func promptFallback(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "None"
	}
	return value
}
