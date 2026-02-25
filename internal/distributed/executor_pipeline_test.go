package distributed

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

type scriptedStageRunner struct {
	stages map[string][]contracts.RunnerResult
	calls  []string
}

func (s *scriptedStageRunner) run(ctx context.Context, req contracts.RunnerRequest, stage string, _ ExecutorConfigStage) (contracts.RunnerResult, error) {
	s.calls = append(s.calls, stage)
	outputs := s.stages[stage]
	if len(outputs) == 0 {
		return contracts.RunnerResult{Status: contracts.RunnerResultFailed, Reason: fmt.Sprintf("no output configured for stage %s", stage)}, fmt.Errorf("missing stage output")
	}
	output := outputs[0]
	s.stages[stage] = outputs[1:]
	return output, nil
}

type eventCapture struct {
	events []contracts.Event
}

func (e *eventCapture) Emit(_ context.Context, event contracts.Event) error {
	e.events = append(e.events, event)
	return nil
}

func TestExecutorPipelineSuccessPathRunsConfiguredStagesInOrder(t *testing.T) {
	runner := &scriptedStageRunner{
		stages: map[string][]contracts.RunnerResult{
			"quality_gate": {
				{
					Status:    contracts.RunnerResultCompleted,
					Reason:    "quality gate complete",
					Artifacts: map[string]string{"quality_score": "92", "quality_threshold": "80", "quality": "pass"},
				},
			},
			"execute": {
				{
					Status:    contracts.RunnerResultCompleted,
					Reason:    "execute complete",
					Artifacts: map[string]string{},
				},
			},
			"qc_gate": {
				{
					Status:    contracts.RunnerResultCompleted,
					Reason:    "qc gate complete",
					Artifacts: map[string]string{},
				},
			},
			"complete": {
				{
					Status:    contracts.RunnerResultCompleted,
					Reason:    "pipeline complete",
					Artifacts: map[string]string{},
				},
			},
		},
	}

	cfg := ExecutorConfig{
		Name:    "test-exec",
		Type:    "task",
		Backend: "codex",
		Pipeline: map[string]ExecutorConfigStage{
			"quality_gate": {
				Tools: []string{"reviewer"},
				Retry: ExecutorConfigRetry{MaxAttempts: 1},
				Transitions: ExecutorConfigTransitions{
					OnSuccess: ExecutorConfigTransition{Action: "next", NextStage: "execute", Condition: "true"},
					OnFailure: ExecutorConfigTransition{Action: "fail", Condition: "true"},
				},
			},
			"execute": {
				Tools: []string{"shell"},
				Retry: ExecutorConfigRetry{MaxAttempts: 1},
				Transitions: ExecutorConfigTransitions{
					OnSuccess: ExecutorConfigTransition{Action: "next", NextStage: "qc_gate", Condition: "true"},
					OnFailure: ExecutorConfigTransition{Action: "fail", Condition: "true"},
				},
			},
			"qc_gate": {
				Tools: []string{"quality-checker"},
				Retry: ExecutorConfigRetry{MaxAttempts: 1},
				Transitions: ExecutorConfigTransitions{
					OnSuccess: ExecutorConfigTransition{Action: "next", NextStage: "complete", Condition: "true"},
					OnFailure: ExecutorConfigTransition{Action: "fail", Condition: "true"},
				},
			},
			"complete": {
				Tools: []string{"git"},
				Retry: ExecutorConfigRetry{MaxAttempts: 1},
				Transitions: ExecutorConfigTransitions{
					OnSuccess: ExecutorConfigTransition{Action: "complete", Condition: "true"},
					OnFailure: ExecutorConfigTransition{Action: "fail", Condition: "true"},
				},
			},
		},
	}

	events := &eventCapture{}
	pipeline := ExecutorPipeline{
		Config:      cfg,
		StageRunner: runner.run,
		EventSink:   events,
	}

	result, err := pipeline.Execute(context.Background(), contracts.RunnerRequest{})
	if err != nil {
		t.Fatalf("pipeline execute: %v", err)
	}
	if result.Result.Status != contracts.RunnerResultCompleted {
		t.Fatalf("expected completed result, got %q", result.Result.Status)
	}
	if result.FinalStage != "complete" {
		t.Fatalf("expected final stage %q, got %q", "complete", result.FinalStage)
	}
	if got := strings.Join(runner.calls, ","); got != "quality_gate,execute,qc_gate,complete" {
		t.Fatalf("expected stage call order quality_gate->execute->qc_gate->complete, got %q", got)
	}
	if len(events.events) != 12 {
		t.Fatalf("expected 12 pipeline events, got %d", len(events.events))
	}

	started := 0
	finished := 0
	transitioned := 0
	for _, event := range events.events {
		switch event.Type {
		case EventTypeExecutorPipelineStageStarted:
			started++
		case EventTypeExecutorPipelineStageFinished:
			finished++
		case EventTypeExecutorPipelineTransitionTaken:
			transitioned++
		}
	}
	if started != 4 || finished != 4 || transitioned != 4 {
		t.Fatalf("expected 4 stage_started/stage_finished/transition_taken events, got started=%d finished=%d transitioned=%d", started, finished, transitioned)
	}
}

func TestExecutorPipelineConditionalBranching(t *testing.T) {
	runner := &scriptedStageRunner{
		stages: map[string][]contracts.RunnerResult{
			"quality_gate": {
				{
					Status:    contracts.RunnerResultCompleted,
					Reason:    "quality gate result",
					Artifacts: map[string]string{"quality_score": "90", "quality_threshold": "75"},
				},
			},
			"execute": {
				{
					Status:    contracts.RunnerResultCompleted,
					Reason:    "execute complete",
					Artifacts: map[string]string{},
				},
			},
			"complete": {
				{
					Status:    contracts.RunnerResultCompleted,
					Reason:    "complete result",
					Artifacts: map[string]string{},
				},
			},
			"qc_gate": {
				{
					Status:    contracts.RunnerResultCompleted,
					Reason:    "qc should not run",
					Artifacts: map[string]string{},
				},
			},
		},
	}

	cfg := ExecutorConfig{
		Name:    "test-exec",
		Type:    "task",
		Backend: "codex",
		Pipeline: map[string]ExecutorConfigStage{
			"quality_gate": {
				Tools: []string{"reviewer"},
				Retry: ExecutorConfigRetry{MaxAttempts: 1},
				Transitions: ExecutorConfigTransitions{
					OnSuccess: ExecutorConfigTransition{Action: "next", NextStage: "execute", Condition: "quality_score >= threshold"},
					OnFailure: ExecutorConfigTransition{Action: "fail", Condition: "true"},
				},
			},
			"execute": {
				Tools: []string{"shell"},
				Retry: ExecutorConfigRetry{MaxAttempts: 1},
				Transitions: ExecutorConfigTransitions{
					OnSuccess: ExecutorConfigTransition{Action: "next", NextStage: "complete", Condition: "true"},
					OnFailure: ExecutorConfigTransition{Action: "fail", Condition: "true"},
				},
			},
			"qc_gate": {
				Tools: []string{"quality-checker"},
				Retry: ExecutorConfigRetry{MaxAttempts: 1},
				Transitions: ExecutorConfigTransitions{
					OnSuccess: ExecutorConfigTransition{Action: "next", NextStage: "complete", Condition: "true"},
					OnFailure: ExecutorConfigTransition{Action: "fail", Condition: "true"},
				},
			},
			"complete": {
				Tools: []string{"git"},
				Retry: ExecutorConfigRetry{MaxAttempts: 1},
				Transitions: ExecutorConfigTransitions{
					OnSuccess: ExecutorConfigTransition{Action: "complete", Condition: "true"},
					OnFailure: ExecutorConfigTransition{Action: "fail", Condition: "true"},
				},
			},
		},
	}

	events := &eventCapture{}
	pipeline := ExecutorPipeline{
		Config:      cfg,
		StageRunner: runner.run,
		EventSink:   events,
	}

	result, err := pipeline.Execute(context.Background(), contracts.RunnerRequest{})
	if err != nil {
		t.Fatalf("pipeline execute: %v", err)
	}
	if result.FinalStage != "complete" {
		t.Fatalf("expected final stage %q, got %q", "complete", result.FinalStage)
	}
	if got := strings.Join(runner.calls, ","); got != "quality_gate,execute,complete" {
		t.Fatalf("expected conditional path quality_gate->execute->complete, got %q", got)
	}
	if len(events.events) != 9 {
		t.Fatalf("expected 9 events, got %d", len(events.events))
	}
}

func TestExecutorPipelineRetryWithAddendumWhenRetriesExhausted(t *testing.T) {
	runner := &scriptedStageRunner{
		stages: map[string][]contracts.RunnerResult{
			"quality_gate": {
				{Status: contracts.RunnerResultFailed, Reason: "first attempt failed"},
				{Status: contracts.RunnerResultFailed, Reason: "second attempt failed"},
			},
			"execute": {
				{Status: contracts.RunnerResultCompleted},
			},
			"qc_gate": {
				{Status: contracts.RunnerResultCompleted},
			},
			"complete": {
				{Status: contracts.RunnerResultCompleted},
			},
		},
	}

	cfg := ExecutorConfig{
		Name:    "test-exec",
		Type:    "task",
		Backend: "codex",
		Pipeline: map[string]ExecutorConfigStage{
			"quality_gate": {
				Tools: []string{"reviewer"},
				Retry: ExecutorConfigRetry{MaxAttempts: 2, InitialDelayMs: 1},
				Transitions: ExecutorConfigTransitions{
					OnSuccess: ExecutorConfigTransition{Action: "complete", Condition: "true"},
					OnFailure: ExecutorConfigTransition{Action: "retry", Condition: "true"},
				},
			},
			"execute": {
				Tools: []string{"shell"},
				Retry: ExecutorConfigRetry{MaxAttempts: 1},
				Transitions: ExecutorConfigTransitions{
					OnSuccess: ExecutorConfigTransition{Action: "next", NextStage: "complete", Condition: "true"},
					OnFailure: ExecutorConfigTransition{Action: "fail", Condition: "true"},
				},
			},
			"qc_gate": {
				Tools: []string{"quality-checker"},
				Retry: ExecutorConfigRetry{MaxAttempts: 1},
				Transitions: ExecutorConfigTransitions{
					OnSuccess: ExecutorConfigTransition{Action: "next", NextStage: "complete", Condition: "true"},
					OnFailure: ExecutorConfigTransition{Action: "fail", Condition: "true"},
				},
			},
			"complete": {
				Tools: []string{"git"},
				Retry: ExecutorConfigRetry{MaxAttempts: 1},
				Transitions: ExecutorConfigTransitions{
					OnSuccess: ExecutorConfigTransition{Action: "complete", Condition: "true"},
					OnFailure: ExecutorConfigTransition{Action: "fail", Condition: "true"},
				},
			},
		},
	}

	events := &eventCapture{}
	pipeline := ExecutorPipeline{
		Config:      cfg,
		StageRunner: runner.run,
		EventSink:   events,
	}

	result, err := pipeline.Execute(context.Background(), contracts.RunnerRequest{})
	if err != nil {
		t.Fatalf("pipeline execute: %v", err)
	}
	if result.Result.Status != contracts.RunnerResultFailed {
		t.Fatalf("expected failed result after retries exhausted, got %q", result.Result.Status)
	}
	if strings.Count(result.Result.Reason, "Attempt") != 2 {
		t.Fatalf("expected two attempt failures in reason, got %q", result.Result.Reason)
	}
	if result.StageRuns["quality_gate"] != 2 {
		t.Fatalf("expected two attempts on quality_gate, got %d", result.StageRuns["quality_gate"])
	}
	if result.FinalStage != "quality_gate" {
		t.Fatalf("expected final stage %q, got %q", "quality_gate", result.FinalStage)
	}
	if len(events.events) != 6 {
		t.Fatalf("expected 6 events (2 attempts x 3 events), got %d", len(events.events))
	}
}

func TestExecutorPipelineFailureTransitionProducesTerminalFailure(t *testing.T) {
	runner := &scriptedStageRunner{
		stages: map[string][]contracts.RunnerResult{
			"quality_gate": {
				{Status: contracts.RunnerResultFailed, Reason: "quality gate hard failure"},
			},
			"execute": {
				{Status: contracts.RunnerResultCompleted},
			},
			"qc_gate": {
				{Status: contracts.RunnerResultCompleted},
			},
			"complete": {
				{Status: contracts.RunnerResultCompleted},
			},
		},
	}

	cfg := ExecutorConfig{
		Name:    "test-exec",
		Type:    "task",
		Backend: "codex",
		Pipeline: map[string]ExecutorConfigStage{
			"quality_gate": {
				Tools: []string{"reviewer"},
				Retry: ExecutorConfigRetry{MaxAttempts: 1},
				Transitions: ExecutorConfigTransitions{
					OnSuccess: ExecutorConfigTransition{Action: "next", NextStage: "execute", Condition: "true"},
					OnFailure: ExecutorConfigTransition{Action: "fail", Condition: "true"},
				},
			},
			"execute": {
				Tools: []string{"shell"},
				Retry: ExecutorConfigRetry{MaxAttempts: 1},
				Transitions: ExecutorConfigTransitions{
					OnSuccess: ExecutorConfigTransition{Action: "next", NextStage: "complete", Condition: "true"},
					OnFailure: ExecutorConfigTransition{Action: "fail", Condition: "true"},
				},
			},
			"qc_gate": {
				Tools: []string{"quality-checker"},
				Retry: ExecutorConfigRetry{MaxAttempts: 1},
				Transitions: ExecutorConfigTransitions{
					OnSuccess: ExecutorConfigTransition{Action: "next", NextStage: "complete", Condition: "true"},
					OnFailure: ExecutorConfigTransition{Action: "fail", Condition: "true"},
				},
			},
			"complete": {
				Tools: []string{"git"},
				Retry: ExecutorConfigRetry{MaxAttempts: 1},
				Transitions: ExecutorConfigTransitions{
					OnSuccess: ExecutorConfigTransition{Action: "complete", Condition: "true"},
					OnFailure: ExecutorConfigTransition{Action: "fail", Condition: "true"},
				},
			},
		},
	}

	pipeline := ExecutorPipeline{
		Config:      cfg,
		StageRunner: runner.run,
	}

	result, err := pipeline.Execute(context.Background(), contracts.RunnerRequest{})
	if err != nil {
		t.Fatalf("pipeline execute: %v", err)
	}
	if result.Result.Status != contracts.RunnerResultFailed {
		t.Fatalf("expected failed result, got %q", result.Result.Status)
	}
	if !strings.Contains(result.Result.Reason, "hard failure") {
		t.Fatalf("expected failure reason to include runner reason, got %q", result.Result.Reason)
	}
	if got := strings.Join(runner.calls, ","); got != "quality_gate" {
		t.Fatalf("expected only quality_gate stage to run, got %q", got)
	}
}
