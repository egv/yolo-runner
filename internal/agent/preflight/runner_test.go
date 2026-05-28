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

type fakeAgentRunner struct {
	output   string
	requests []contracts.RunnerRequest
}

func (f *fakeAgentRunner) Run(_ context.Context, request contracts.RunnerRequest) (contracts.RunnerResult, error) {
	f.requests = append(f.requests, request)
	if request.OnProgress != nil {
		request.OnProgress(contracts.RunnerProgress{
			Type:    string(contracts.EventTypeRunnerOutput),
			Message: f.output,
		})
	}
	return contracts.RunnerResult{Status: contracts.RunnerResultCompleted}, nil
}
