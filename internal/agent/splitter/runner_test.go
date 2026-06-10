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
	output := strictOutputFixture(
		strictTaskFixture("T20", "Invoke strict splitter", "Call the strict splitter prompt.", []string{"none"}, []string{"T21"}),
		strictTaskFixture("T21", "Parse strict splitter output", "Generated Tracker subtasks need structured task sections, not prose blobs.", []string{"T20"}, []string{"T22"}),
		strictTaskFixture("T22", "Create Tracker subtasks", "Tracker needs concrete child tasks from parsed splitter output.", []string{"T21"}, []string{"none"}),
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
		"Required task template for each task",
		"## Tasks containing only summary list items",
		"Do not place full task templates inside the ## Tasks summary section",
		"do not write readiness prose",
		"Return only the strict splitter markdown",
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

func TestRunnerPreservesStreamingSplitterWhitespace(t *testing.T) {
	taskT20 := strictTaskFixture("T20", "Invoke strict splitter", "Call the strict splitter prompt.", []string{"none"}, []string{"T21"})
	taskT21 := strictTaskFixture("T21", "Parse strict splitter output", "Generated Tracker subtasks need structured task sections, not prose blobs.", []string{"T20"}, []string{"T22"})
	taskT22 := strictTaskFixture("T22", "Create Tracker subtasks", "Tracker needs concrete child tasks from parsed splitter output.", []string{"T21"}, []string{"none"})
	output := strictOutputFixture(
		taskT20,
		taskT21,
		taskT22,
	)
	chunks := []string{
		"Introductory prose should be ignored.\n\n#",
		"# Epics\n- Tracker task generation",
		": Generate strict Tracker subtasks from a broad task.\n\n## Tas",
		"ks\n- T20: Invoke strict splitter\n- T21: Parse strict splitter output\n- T22: Create Tracker subtasks\n\n## Order\n- T20 -> T21 -> T22\n\n## Risk notes\n- Model output may include stray prose before the first heading.\n\n",
		taskT20,
		taskT21,
		taskT22,
	}
	progress := make([]contracts.RunnerProgress, 0, len(chunks))
	for _, chunk := range chunks {
		progress = append(progress, contracts.RunnerProgress{
			Type:     string(contracts.EventTypeRunnerOutput),
			Message:  chunk,
			Metadata: map[string]string{"preserve_whitespace": "true"},
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

type fakeSplitterAgentRunner struct {
	output   string
	progress []contracts.RunnerProgress
	requests []contracts.RunnerRequest
}

func (f *fakeSplitterAgentRunner) Run(_ context.Context, request contracts.RunnerRequest) (contracts.RunnerResult, error) {
	f.requests = append(f.requests, request)
	if request.OnProgress != nil {
		request.OnProgress(contracts.RunnerProgress{
			Type:     string(contracts.EventTypeRunnerOutput),
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
			Type:    string(contracts.EventTypeRunnerOutput),
			Message: f.output,
		})
	}
	return contracts.RunnerResult{Status: contracts.RunnerResultCompleted}, nil
}
