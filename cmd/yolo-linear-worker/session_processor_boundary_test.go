package main

import (
	"context"
	"strings"
	"testing"

	"github.com/anomalyco/yolo-runner/internal/contracts"
	"github.com/anomalyco/yolo-runner/internal/linear"
	"github.com/anomalyco/yolo-runner/internal/linear/webhook"
)

type boundaryTestRunner struct {
	runCalls int
}

func (r *boundaryTestRunner) Run(context.Context, contracts.RunnerRequest) (contracts.RunnerResult, error) {
	r.runCalls++
	return contracts.RunnerResult{}, nil
}

type boundaryTestActivities struct {
	thoughtCalls  int
	responseCalls int
}

func (a *boundaryTestActivities) EmitThought(context.Context, linear.ThoughtActivityInput) (string, error) {
	a.thoughtCalls++
	return "thought-1", nil
}

func (a *boundaryTestActivities) EmitResponse(context.Context, linear.ResponseActivityInput) (string, error) {
	a.responseCalls++
	return "response-1", nil
}

func (a *boundaryTestActivities) UpdateSessionExternalURLs(context.Context, linear.AgentSessionExternalURLsInput) error {
	return nil
}

func TestLinearSessionJobProcessorRejectsNonWebhookContractBeforeExecution(t *testing.T) {
	runner := &boundaryTestRunner{}
	activities := &boundaryTestActivities{}
	processor := &linearSessionJobProcessor{
		repoRoot:   t.TempDir(),
		runner:     runner,
		activities: activities,
	}

	err := processor.Process(context.Background(), webhook.Job{
		ID:        "inline-job-1",
		SessionID: "session-1",
	})
	if err == nil {
		t.Fatal("expected non-webhook contract job to be rejected")
	}
	if !strings.Contains(err.Error(), "queued linear webhook job") {
		t.Fatalf("expected queued webhook contract error, got %v", err)
	}
	if activities.thoughtCalls != 0 {
		t.Fatalf("expected no thought emissions, got %d", activities.thoughtCalls)
	}
	if activities.responseCalls != 0 {
		t.Fatalf("expected no response emissions, got %d", activities.responseCalls)
	}
	if runner.runCalls != 0 {
		t.Fatalf("expected no runner invocations, got %d", runner.runCalls)
	}
}
