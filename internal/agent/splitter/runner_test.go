package splitter

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

func TestRunnerInvokesStrictSplitterAndParsesTasks(t *testing.T) {
	output := strictJSONOutputFixture(
		strictJSONTaskFixture("T20", "Invoke strict splitter", "Call the strict splitter prompt.", []string{"none"}, []string{"T21"}),
		strictJSONTaskFixture("T21", "Parse strict splitter output", "Generated Tracker subtasks need structured task sections, not prose blobs.", []string{"T20"}, []string{"T22"}),
		strictJSONTaskFixture("T22", "Create Tracker subtasks", "Tracker needs concrete child tasks from parsed splitter output.", []string{"T21"}, []string{"none"}),
	)
	agent := &fakeSplitterAgentRunner{output: output}
	runner := NewRunner(agent)

	task := contracts.Task{
		ID:          "parent-123",
		Title:       "Implement broad tracker automation",
		Description: "Create a pipeline that splits ready parent issues before implementation.",
		Status:      contracts.TaskStatusOpen,
		ParentID:    "root-1",
	}
	queueRoot := contracts.Task{
		ID:          "root-1",
		Title:       "Tracker automation",
		Description: "Automate tracker-driven runner workflows.",
		Status:      contracts.TaskStatusOpen,
	}

	got, err := runner.Run(context.Background(), RunInput{
		Task:       task,
		QueueRoot:  queueRoot,
		Model:      "gpt-test",
		RepoRoot:   "/repo",
		Timeout:    2 * time.Minute,
		MaxRetries: 1,
		Metadata:   map[string]string{"source": "test"},
	})
	if err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	if len(agent.requests) != 1 {
		t.Fatalf("expected one runner request, got %d", len(agent.requests))
	}
	request := agent.requests[0]
	if request.TaskID != task.ID || request.ParentID != queueRoot.ID {
		t.Fatalf("unexpected task ids in request: %#v", request)
	}
	if request.Mode != contracts.RunnerModeReview {
		t.Fatalf("expected review mode for non-mutating splitting, got %q", request.Mode)
	}
	if request.Model != "gpt-test" || request.RepoRoot != "/repo" || request.Timeout != 2*time.Minute || request.MaxRetries != 1 {
		t.Fatalf("unexpected runner request routing fields: %#v", request)
	}
	if request.Metadata["source"] != "test" {
		t.Fatalf("expected metadata to be forwarded, got %#v", request.Metadata)
	}
	for _, want := range []string{
		"using only the instructions in this prompt",
		"Do not run shell commands, inspect files, or search the filesystem",
		"Return only valid JSON matching the required schema",
		"Do not wrap the JSON in markdown or code fences",
		"Detected parent task language: English.",
		"Write every generated epic name, epic goal, task title, why, in_scope, out_of_scope, strict_tdd, done_when, and risk_notes item in that language.",
		`"epics"`,
		`"depends_on"`,
		"Do not include markdown headings, task templates, comments, trailing prose, or any field not shown in the schema",
		"ID: parent-123",
		"Title: Implement broad tracker automation",
		"Create a pipeline that splits ready parent issues before implementation.",
	} {
		if !strings.Contains(request.Prompt, want) {
			t.Fatalf("expected splitter prompt to contain %q, got:\n%s", want, request.Prompt)
		}
	}
	for _, forbidden := range []string{
		"split-tasks-strict",
		"task-splitting",
		"SKILL.md",
	} {
		if strings.Contains(request.Prompt, forbidden) {
			t.Fatalf("splitter prompt must not trigger external discovery via %q, got:\n%s", forbidden, request.Prompt)
		}
	}

	taskT21 := got.TaskByID("T21")
	if taskT21 == nil {
		t.Fatalf("expected parsed T21 task, got %#v", got.Tasks)
	}
	if taskT21.Title != "Parse strict splitter output" {
		t.Fatalf("T21 title = %q", taskT21.Title)
	}
	if !reflect.DeepEqual(taskT21.DependsOn, []string{"T20"}) {
		t.Fatalf("T21 DependsOn = %#v", taskT21.DependsOn)
	}
	if !reflect.DeepEqual(got.Order, []Dependency{{From: "T20", To: "T21"}, {From: "T21", To: "T22"}}) {
		t.Fatalf("Order = %#v", got.Order)
	}
}

func TestRunnerPromptUsesRussianForRussianParentTask(t *testing.T) {
	agent := &fakeSplitterAgentRunner{output: strictJSONOutputFixture(
		strictJSONTaskFixture("ADAPTABOT-1-001", "Ввести транспортно-независимую модель входящего сообщения", "Общая модель создает минимальный шов.", []string{"none"}, []string{"none"}),
	)}
	runner := NewRunner(agent)

	_, err := runner.Run(context.Background(), RunInput{
		Task: contracts.Task{
			ID:          "ADAPTABOT-1",
			Title:       "Перенести бот в Yandex Messenger",
			Description: "Нужно реализовать параллельного бота в Yandex Messenger.",
			Status:      contracts.TaskStatusOpen,
			ParentID:    "ADAPTABOT",
		},
		QueueRoot: contracts.Task{ID: "ADAPTABOT", Title: "ADAPTABOT", Status: contracts.TaskStatusOpen},
		Model:     "gpt-test",
		RepoRoot:  "/repo",
		Timeout:   2 * time.Minute,
	})
	if err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}
	if len(agent.requests) != 1 {
		t.Fatalf("expected one runner request, got %d", len(agent.requests))
	}
	prompt := agent.requests[0].Prompt
	for _, want := range []string{
		"Detected parent task language: Russian.",
		"Write every generated epic name, epic goal, task title, why, in_scope, out_of_scope, strict_tdd, done_when, and risk_notes item in that language.",
		"Preserve queue keys, task IDs, product names, API names, code identifiers, file paths, commands, labels, and quoted literals verbatim.",
		"Title: Перенести бот в Yandex Messenger",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected splitter prompt to contain %q, got:\n%s", want, prompt)
		}
	}
}

func TestRunnerPreservesStreamingSplitterWhitespace(t *testing.T) {
	output := strictJSONOutputFixture(
		strictJSONTaskFixture("T20", "Invoke strict splitter", "Call the strict splitter prompt.", []string{"none"}, []string{"T21"}),
		strictJSONTaskFixture("T21", "Parse strict splitter output", "Generated Tracker subtasks need structured task sections, not prose blobs.", []string{"T20"}, []string{"T22"}),
		strictJSONTaskFixture("T22", "Create Tracker subtasks", "Tracker needs concrete child tasks from parsed splitter output.", []string{"T21"}, []string{"none"}),
	)
	trackerIndex := strings.Index(output, "Tracker task generation")
	if trackerIndex < 0 {
		t.Fatalf("fixture missing expected split point: %s", output)
	}
	chunks := []string{
		output[:trackerIndex+7],
		output[trackerIndex+7 : trackerIndex+12],
		output[trackerIndex+12:],
	}
	progress := make([]contracts.RunnerProgress, 0, len(chunks))
	for _, chunk := range chunks {
		progress = append(progress, contracts.RunnerProgress{
			Type:    string(contracts.EventTypeAgentText),
			Message: chunk,
		})
	}
	agent := &fakeSplitterAgentRunner{progress: progress}
	runner := NewRunner(agent)

	got, err := runner.Run(context.Background(), RunInput{
		Task:      contracts.Task{ID: "parent-123", Title: "Implement broad tracker automation", Status: contracts.TaskStatusOpen},
		QueueRoot: contracts.Task{ID: "root-1", Title: "Tracker automation", Status: contracts.TaskStatusOpen},
		Model:     "gpt-test",
		RepoRoot:  "/repo",
		Timeout:   2 * time.Minute,
	})
	if err != nil {
		t.Fatalf("Run() returned error: %v\nfull output:\n%s", err, output)
	}
	if got.TaskByID("T20") == nil {
		t.Fatalf("expected parsed T20 task, got %#v", got.Tasks)
	}
}

func TestRunnerNormalizesCommandLifecycleToSingleCommandRun(t *testing.T) {
	output := strictJSONOutputFixture(
		strictJSONTaskFixture("T20", "Invoke strict splitter", "Call the strict splitter prompt.", []string{"none"}, []string{"none"}),
	)
	agent := &fakeSplitterAgentRunner{progress: []contracts.RunnerProgress{
		{Type: string(contracts.EventTypeToolInvoked), Message: "cmd start"},
		{Type: string(contracts.EventTypeCommandRun), Message: "cmd finish", Metadata: map[string]string{"exit_code": "0", "duration_ms": "125"}},
		{Type: string(contracts.EventTypeAgentText), Message: output},
	}}
	runner := NewRunner(agent)
	var progress []contracts.RunnerProgress

	_, err := runner.Run(context.Background(), RunInput{
		Task:       contracts.Task{ID: "parent-123", Title: "Implement broad tracker automation", Status: contracts.TaskStatusOpen},
		QueueRoot:  contracts.Task{ID: "root-1", Title: "Tracker automation", Status: contracts.TaskStatusOpen},
		OnProgress: func(event contracts.RunnerProgress) { progress = append(progress, event) },
	})
	if err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	var commands []contracts.RunnerProgress
	for _, event := range progress {
		if event.Type == string(contracts.EventTypeCommandRun) {
			commands = append(commands, event)
		}
	}
	if len(commands) != 1 {
		t.Fatalf("expected one command_run progress event, got %d: %#v", len(commands), progress)
	}
	if commands[0].Message != "cmd finish" || commands[0].Metadata["exit_code"] != "0" || commands[0].Metadata["duration_ms"] != "125" {
		t.Fatalf("command_run did not preserve finished payload: %#v", commands[0])
	}
}

type fakeSplitterAgentRunner struct {
	output   string
	progress []contracts.RunnerProgress
	requests []contracts.RunnerRequest
}

func (f *fakeSplitterAgentRunner) Run(_ context.Context, request contracts.RunnerRequest) (contracts.RunnerResult, error) {
	f.requests = append(f.requests, request)
	if request.OnProgress != nil {
		request.OnProgress(contracts.RunnerProgress{
			Type:     string(contracts.EventTypeAgentText),
			Message:  "debug should be ignored",
			Metadata: map[string]string{"source": "stderr"},
		})
		if len(f.progress) > 0 {
			for _, progress := range f.progress {
				request.OnProgress(progress)
			}
			return contracts.RunnerResult{Status: contracts.RunnerResultCompleted}, nil
		}
		request.OnProgress(contracts.RunnerProgress{
			Type:    string(contracts.EventTypeAgentText),
			Message: f.output,
		})
	}
	return contracts.RunnerResult{Status: contracts.RunnerResultCompleted}, nil
}
