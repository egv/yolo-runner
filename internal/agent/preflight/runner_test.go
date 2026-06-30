package preflight

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

func TestRunnerReturnsReadyAndNeedsInfoFromRunnerOutput(t *testing.T) {
	task := contracts.Task{
		ID:          "task-123",
		Title:       "Add retry guard",
		Description: "Wire the retry guard into the agent loop.",
		Status:      contracts.TaskStatusOpen,
		ParentID:    "epic-1",
	}
	queueRoot := contracts.Task{
		ID:          "epic-1",
		Title:       "Agent preflight",
		Description: "Preflight checks for queued tasks.",
		Status:      contracts.TaskStatusOpen,
	}

	tests := []struct {
		name   string
		output string
		want   Result
	}{
		{
			name:   "ready",
			output: `{"decision":"ready","confidence":0.91,"summary":"Task is actionable.","questions":[]}`,
			want: Result{
				Decision:   DecisionReady,
				Confidence: 0.91,
				Summary:    "Task is actionable.",
				Questions:  []string{},
			},
		},
		{
			name:   "needs info",
			output: `{"decision":"needs_info","confidence":0.73,"summary":"Ownership is unclear.","questions":["Which package owns the behavior?"]}`,
			want: Result{
				Decision:   DecisionNeedsInfo,
				Confidence: 0.73,
				Summary:    "Ownership is unclear.",
				Questions:  []string{"Which package owns the behavior?"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent := &fakeAgentRunner{output: tt.output}
			runner := NewRunner(agent)

			got, err := runner.Run(context.Background(), RunInput{
				Task:      task,
				Comments:  []Comment{{Author: "alice", Body: "Keep it scoped."}},
				QueueRoot: queueRoot,
				Model:     "gpt-test",
				RepoRoot:  "/repo",
				Metadata:  map[string]string{"source": "test"},
			})
			if err != nil {
				t.Fatalf("Run() returned error: %v", err)
			}

			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Run() = %#v, want %#v", got, tt.want)
			}
			if len(agent.requests) != 1 {
				t.Fatalf("expected one runner request, got %d", len(agent.requests))
			}
			request := agent.requests[0]
			if request.Mode != contracts.RunnerModeReview {
				t.Fatalf("expected review mode, got %q", request.Mode)
			}
			if request.TaskID != task.ID || request.ParentID != queueRoot.ID {
				t.Fatalf("unexpected task ids in request: %#v", request)
			}
			if request.Model != "gpt-test" || request.RepoRoot != "/repo" {
				t.Fatalf("unexpected runner request routing fields: %#v", request)
			}
			if request.Metadata["source"] != "test" {
				t.Fatalf("expected metadata to be forwarded, got %#v", request.Metadata)
			}
			if !strings.Contains(request.Prompt, "Read only.") || !strings.Contains(request.Prompt, "Task:\nID: task-123") {
				t.Fatalf("expected preflight prompt to include read-only task context, got:\n%s", request.Prompt)
			}
		})
	}
}

func TestParseRunnerOutputCompactsStreamedJSONTokens(t *testing.T) {
	output := strings.Join([]string{
		`{"`,
		`decision`,
		`":"`,
		`ready`,
		`","`,
		`confidence`,
		`":`,
		`0`,
		`.`,
		`84`,
		`,"`,
		`summary`,
		`":"`,
		`Task`,
		` `,
		`is`,
		` `,
		`actionable.`,
		`","`,
		`questions`,
		`":`,
		`[]}`,
	}, "\n")

	got := parseRunnerOutput(output)
	want := Result{
		Decision:   DecisionReady,
		Confidence: 0.84,
		Summary:    "Task is actionable.",
		Questions:  []string{},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseRunnerOutput() = %#v, want %#v", got, want)
	}
}

func TestRunnerPreservesStreamedWhitespaceTokens(t *testing.T) {
	agent := &fakeAgentRunner{outputChunks: []string{
		`{"decision":"needs_info","confidence":0.73,"summary":"The`,
		` `,
		`task`,
		` `,
		`is`,
		` `,
		`unclear.","questions":["Which`,
		` `,
		`package`,
		` `,
		`owns`,
		` `,
		`this?"]}`,
	}}
	runner := NewRunner(agent)

	got, err := runner.Run(context.Background(), RunInput{
		Task: contracts.Task{
			ID:          "task-123",
			Title:       "Add retry guard",
			Description: "Wire the retry guard into the agent loop.",
			Status:      contracts.TaskStatusOpen,
			ParentID:    "epic-1",
		},
		QueueRoot: contracts.Task{ID: "epic-1"},
	})
	if err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	want := Result{
		Decision:   DecisionNeedsInfo,
		Confidence: 0.73,
		Summary:    "The task is unclear.",
		Questions:  []string{"Which package owns this?"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Run() = %#v, want %#v", got, want)
	}
}

func TestRunnerNormalizesCommandLifecycleToSingleCommandRun(t *testing.T) {
	agent := &fakeAgentRunner{
		progress: []contracts.RunnerProgress{
			{Type: string(contracts.EventTypeRunnerCommandStarted), Message: "cmd start"},
			{Type: string(contracts.EventTypeRunnerCommandFinished), Message: "cmd finish", Metadata: map[string]string{"exit_code": "0", "duration_ms": "125"}},
			{Type: string(contracts.EventTypeRunnerOutput), Message: `{"decision":"ready","confidence":1,"summary":"ok","questions":[]}`},
		},
	}
	runner := NewRunner(agent)
	var progress []contracts.RunnerProgress

	_, err := runner.Run(context.Background(), RunInput{
		Task:       contracts.Task{ID: "task-123", Title: "Add retry guard", Status: contracts.TaskStatusOpen},
		QueueRoot:  contracts.Task{ID: "epic-1"},
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

type fakeAgentRunner struct {
	output       string
	outputChunks []string
	progress     []contracts.RunnerProgress
	requests     []contracts.RunnerRequest
}

func (f *fakeAgentRunner) Run(_ context.Context, request contracts.RunnerRequest) (contracts.RunnerResult, error) {
	f.requests = append(f.requests, request)
	if request.OnProgress != nil {
		if len(f.progress) > 0 {
			for _, progress := range f.progress {
				request.OnProgress(progress)
			}
			return contracts.RunnerResult{Status: contracts.RunnerResultCompleted}, nil
		}
		if len(f.outputChunks) > 0 {
			for _, chunk := range f.outputChunks {
				request.OnProgress(contracts.RunnerProgress{
					Type:    string(contracts.EventTypeAgentText),
					Message: chunk,
				})
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
