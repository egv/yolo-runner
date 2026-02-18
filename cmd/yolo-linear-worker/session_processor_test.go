package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/anomalyco/yolo-runner/internal/contracts"
	"github.com/anomalyco/yolo-runner/internal/linear"
	"github.com/anomalyco/yolo-runner/internal/linear/webhook"
)

type captureLinearRunner struct {
	requests []contracts.RunnerRequest
	result   contracts.RunnerResult
	err      error
}

func (r *captureLinearRunner) Run(_ context.Context, request contracts.RunnerRequest) (contracts.RunnerResult, error) {
	r.requests = append(r.requests, request)
	return r.result, r.err
}

type captureLinearActivities struct {
	thoughts   []linear.ThoughtActivityInput
	responses  []linear.ResponseActivityInput
	thoughtErr error
	replyErr   error
}

func (a *captureLinearActivities) EmitThought(_ context.Context, input linear.ThoughtActivityInput) (string, error) {
	a.thoughts = append(a.thoughts, input)
	return "thought-1", a.thoughtErr
}

func (a *captureLinearActivities) EmitResponse(_ context.Context, input linear.ResponseActivityInput) (string, error) {
	a.responses = append(a.responses, input)
	return "response-1", a.replyErr
}

func TestLinearSessionJobProcessorCreatedThenPrompted_ContinuesWithFollowUpInput(t *testing.T) {
	runner := &captureLinearRunner{
		result: contracts.RunnerResult{Status: contracts.RunnerResultCompleted},
	}
	activities := &captureLinearActivities{}

	processor := &linearSessionJobProcessor{
		repoRoot:   t.TempDir(),
		runner:     runner,
		activities: activities,
	}

	createdJob := webhook.Job{
		ID:             "evt-created-1",
		IdempotencyKey: "session-1:created:event:evt-created-1",
		SessionID:      "session-1",
		StepAction:     linear.AgentSessionEventActionCreated,
		Event: linear.AgentSessionEvent{
			Action: linear.AgentSessionEventActionCreated,
			AgentSession: linear.AgentSession{
				ID:            "session-1",
				PromptContext: "<issue identifier=\"YR-O96Q\"><title>Define Linear agent protocol contract</title></issue>",
				Comment: &linear.AgentComment{
					ID:   "comment-1",
					Body: "@yolo-agent implement this task",
				},
			},
		},
	}

	promptedJob := webhook.Job{
		ID:             "evt-prompted-1",
		IdempotencyKey: "session-1:prompted:activity:activity-1",
		SessionID:      "session-1",
		StepAction:     linear.AgentSessionEventActionPrompted,
		Event: linear.AgentSessionEvent{
			Action: linear.AgentSessionEventActionPrompted,
			AgentSession: linear.AgentSession{
				ID:            "session-1",
				PromptContext: "<issue identifier=\"YR-O96Q\"><title>Define Linear agent protocol contract</title></issue>",
			},
			AgentActivity: &linear.AgentActivity{
				ID: "activity-1",
				Content: linear.AgentActivityContent{
					Type: linear.AgentActivityContentTypePrompt,
					Body: "Please include validation for payloadVersion.",
				},
			},
		},
	}

	ctx := context.Background()
	if err := processor.Process(ctx, createdJob); err != nil {
		t.Fatalf("process created job: %v", err)
	}
	if err := processor.Process(ctx, promptedJob); err != nil {
		t.Fatalf("process prompted job: %v", err)
	}

	if len(runner.requests) != 2 {
		t.Fatalf("expected two runner invocations, got %d", len(runner.requests))
	}

	if !strings.Contains(runner.requests[0].Prompt, "Initial request:") {
		t.Fatalf("expected created run prompt to include initial request, got %q", runner.requests[0].Prompt)
	}
	if !strings.Contains(runner.requests[1].Prompt, "Follow-up input:\nPlease include validation for payloadVersion.") {
		t.Fatalf("expected prompted run prompt to include follow-up input, got %q", runner.requests[1].Prompt)
	}
	if !strings.Contains(runner.requests[1].Prompt, "Continue handling the Linear AgentSession request.") {
		t.Fatalf("expected prompted run prompt to include continuation instruction, got %q", runner.requests[1].Prompt)
	}

	if len(activities.responses) != 2 {
		t.Fatalf("expected two response activity emissions, got %d", len(activities.responses))
	}
	finalResponse := activities.responses[len(activities.responses)-1].Body
	if !strings.Contains(finalResponse, "Finished processing Linear session prompted step.") {
		t.Fatalf("expected prompted step final response, got %q", finalResponse)
	}
}

func TestLinearSessionJobProcessorRuntimeFailureAddsActionableResponse(t *testing.T) {
	runner := &captureLinearRunner{
		err: errors.New("opencode stall category=no_output"),
	}
	activities := &captureLinearActivities{}
	processor := &linearSessionJobProcessor{
		repoRoot:   t.TempDir(),
		runner:     runner,
		activities: activities,
	}

	job := webhook.Job{
		ID:             "evt-prompted-1",
		IdempotencyKey: "session-1:prompted:activity:activity-1",
		SessionID:      "session-1",
		StepAction:     linear.AgentSessionEventActionPrompted,
		Event: linear.AgentSessionEvent{
			Action: linear.AgentSessionEventActionPrompted,
			AgentSession: linear.AgentSession{
				ID: "session-1",
			},
		},
	}

	err := processor.Process(context.Background(), job)
	if err == nil {
		t.Fatalf("expected runtime failure from runner")
	}

	if len(activities.responses) != 1 {
		t.Fatalf("expected one response activity emission, got %d", len(activities.responses))
	}
	body := activities.responses[0].Body
	if !strings.Contains(body, "Failed processing Linear session prompted step.") {
		t.Fatalf("expected response failure headline, got %q", body)
	}
	if !strings.Contains(body, "Category: runtime") {
		t.Fatalf("expected runtime category in response body, got %q", body)
	}
	if !strings.Contains(body, "Next step:") {
		t.Fatalf("expected remediation guidance in response body, got %q", body)
	}
}

func TestLinearSessionJobProcessorNonCompletedResultWithoutRunnerErrorIsFailure(t *testing.T) {
	tests := []struct {
		name   string
		status contracts.RunnerResultStatus
		reason string
	}{
		{
			name:   "failed status",
			status: contracts.RunnerResultFailed,
			reason: "lint failed in workspace",
		},
		{
			name:   "blocked status",
			status: contracts.RunnerResultBlocked,
			reason: "waiting for manual approval",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runner := &captureLinearRunner{
				result: contracts.RunnerResult{
					Status: tc.status,
					Reason: tc.reason,
				},
			}
			activities := &captureLinearActivities{}
			processor := &linearSessionJobProcessor{
				repoRoot:   t.TempDir(),
				runner:     runner,
				activities: activities,
			}

			job := webhook.Job{
				ID:             "evt-prompted-1",
				IdempotencyKey: "session-1:prompted:activity:activity-1",
				SessionID:      "session-1",
				StepAction:     linear.AgentSessionEventActionPrompted,
				Event: linear.AgentSessionEvent{
					Action: linear.AgentSessionEventActionPrompted,
					AgentSession: linear.AgentSession{
						ID: "session-1",
					},
				},
			}

			err := processor.Process(context.Background(), job)
			if err == nil {
				t.Fatalf("expected runtime failure for status %q", tc.status)
			}
			if !strings.Contains(err.Error(), tc.reason) {
				t.Fatalf("expected returned error to include reason %q, got %q", tc.reason, err.Error())
			}

			if len(activities.responses) != 1 {
				t.Fatalf("expected one response activity emission, got %d", len(activities.responses))
			}
			body := activities.responses[0].Body
			if !strings.Contains(body, "Failed processing Linear session prompted step.") {
				t.Fatalf("expected response failure headline, got %q", body)
			}
			if !strings.Contains(body, "Category: runtime") {
				t.Fatalf("expected runtime category in response body, got %q", body)
			}
			if !strings.Contains(body, "Cause: run linear session job:") {
				t.Fatalf("expected response cause prefix in body, got %q", body)
			}
			if !strings.Contains(body, tc.reason) {
				t.Fatalf("expected response cause to include reason %q, got %q", tc.reason, body)
			}
			if !strings.Contains(body, "Next step:") {
				t.Fatalf("expected remediation guidance in response body, got %q", body)
			}
		})
	}
}
