package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	thoughts             []linear.ThoughtActivityInput
	actions              []linear.ActionActivityInput
	responses            []linear.ResponseActivityInput
	externalURLUpdates   []linear.SessionExternalURLsInput
	thoughtErr           error
	actionErr            error
	replyErr             error
	externalURLUpdateErr error
}

func (a *captureLinearActivities) EmitThought(_ context.Context, input linear.ThoughtActivityInput) (string, error) {
	a.thoughts = append(a.thoughts, input)
	return "thought-1", a.thoughtErr
}

func (a *captureLinearActivities) EmitResponse(_ context.Context, input linear.ResponseActivityInput) (string, error) {
	a.responses = append(a.responses, input)
	return "response-1", a.replyErr
}

func (a *captureLinearActivities) EmitAction(_ context.Context, input linear.ActionActivityInput) (string, error) {
	a.actions = append(a.actions, input)
	return "action-1", a.actionErr
}

func (a *captureLinearActivities) UpdateSessionExternalURLs(_ context.Context, input linear.SessionExternalURLsInput) error {
	a.externalURLUpdates = append(a.externalURLUpdates, input)
	return a.externalURLUpdateErr
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
		ContractVersion: webhook.JobContractVersion1,
		ID:              "evt-created-1",
		IdempotencyKey:  "session-1:created:event:evt-created-1",
		SessionID:       "session-1",
		StepID:          "step-created-1",
		StepAction:      linear.AgentSessionEventActionCreated,
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
		ContractVersion: webhook.JobContractVersion1,
		ID:              "evt-prompted-1",
		IdempotencyKey:  "session-1:prompted:activity:activity-1",
		SessionID:       "session-1",
		StepID:          "step-prompted-1",
		StepAction:      linear.AgentSessionEventActionPrompted,
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
	if len(activities.actions) != 2 {
		t.Fatalf("expected two action activity emissions, got %d", len(activities.actions))
	}
	finalResponse := activities.responses[len(activities.responses)-1].Body
	if !strings.Contains(finalResponse, "Finished processing Linear session prompted step.") {
		t.Fatalf("expected prompted step final response, got %q", finalResponse)
	}
}

func TestLinearSessionJobProcessorProcessPublishesRunnerExternalURLs(t *testing.T) {
	runner := &captureLinearRunner{
		result: contracts.RunnerResult{
			Status:  contracts.RunnerResultCompleted,
			LogPath: "/repo/runner-logs/codex/evt-created-1.jsonl",
			Artifacts: map[string]string{
				"session_url": "https://runner.example/sessions/ses_1",
				"log_url":     "file:///repo/runner-logs/codex/evt-created-1.jsonl",
			},
		},
	}
	activities := &captureLinearActivities{}

	processor := &linearSessionJobProcessor{
		repoRoot:   t.TempDir(),
		runner:     runner,
		activities: activities,
	}

	if err := processor.Process(context.Background(), queuedLinearJobFixture(linear.AgentSessionEventActionCreated)); err != nil {
		t.Fatalf("process created job: %v", err)
	}

	if len(activities.externalURLUpdates) != 1 {
		t.Fatalf("expected one external URL update, got %d", len(activities.externalURLUpdates))
	}
	update := activities.externalURLUpdates[0]
	if update.AgentSessionID != "session-1" {
		t.Fatalf("expected update for session-1, got %q", update.AgentSessionID)
	}
	if len(update.ExternalURLs) != 2 {
		t.Fatalf("expected two unique external urls (session + log), got %#v", update.ExternalURLs)
	}
}

func TestLinearSessionJobProcessorCreated_AutoTransitionsDelegatedIssueToFirstStartedState(t *testing.T) {
	var (
		sawReadIssueWorkflowStates bool
		updatedStateID             string
	)

	activityAndWorkflowServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Helper()
		if got := strings.TrimSpace(r.Header.Get("Authorization")); got != "Bearer lin_api_test" {
			t.Fatalf("expected Authorization header with bearer token, got %q", got)
		}

		var payload struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode GraphQL request body: %v", err)
		}
		query := payload.Query

		switch {
		case strings.Contains(query, "agentActivityCreate"):
			_, _ = w.Write([]byte(`{"data":{"agentActivityCreate":{"success":true,"agentActivity":{"id":"activity-1"}}}}`))
		case strings.Contains(query, "agentSessionUpdate"):
			_, _ = w.Write([]byte(`{"data":{"agentSessionUpdate":{"success":true,"agentSession":{"id":"session-1"}}}}`))
		case strings.Contains(query, `issue(id: "iss-delegated-1")`) && strings.Contains(query, "states") && !strings.Contains(query, "issueUpdate"):
			sawReadIssueWorkflowStates = true
			_, _ = w.Write([]byte(`{
  "data": {
    "issue": {
      "id": "iss-delegated-1",
      "state": {"type": "backlog", "name": "Backlog"},
      "team": {
        "states": {
          "nodes": [
            {"id": "st-started-b", "type": "started", "name": "In Progress B"},
            {"id": "st-started-a", "type": "started", "name": "In Progress A"},
            {"id": "st-done", "type": "completed", "name": "Done"}
          ]
        }
      }
    }
  }
}`))
		case strings.Contains(query, "issueUpdate"):
			switch {
			case strings.Contains(query, `stateId: "st-started-b"`):
				updatedStateID = "st-started-b"
			case strings.Contains(query, `stateId: "st-started-a"`):
				updatedStateID = "st-started-a"
			case strings.Contains(query, `stateId: "st-done"`):
				updatedStateID = "st-done"
			default:
				t.Fatalf("unexpected issueUpdate query: %q", query)
			}
			_, _ = w.Write([]byte(`{"data":{"issueUpdate":{"success":true}}}`))
		default:
			t.Fatalf("unexpected GraphQL query: %q", query)
		}
	}))
	t.Cleanup(activityAndWorkflowServer.Close)

	repoRoot := t.TempDir()
	t.Setenv(envLinearWorkerBackend, "codex")
	t.Setenv(envLinearWorkerBinary, writeFakeCodexBinary(t))
	t.Setenv(envLinearWorkerRepoRoot, repoRoot)
	t.Setenv(envLinearWorkerModel, "openai/gpt-5.3-codex")
	t.Setenv(envLinearToken, "lin_api_test")
	t.Setenv(envLinearAPIEndpoint, activityAndWorkflowServer.URL)

	processor, err := newLinearSessionJobProcessorFromEnv()
	if err != nil {
		t.Fatalf("build processor from env: %v", err)
	}

	job := webhook.Job{
		ContractVersion: webhook.JobContractVersion1,
		ID:              "evt-created-1",
		IdempotencyKey:  "session-1:created:event:evt-created-1",
		SessionID:       "session-1",
		StepID:          "step-created-1",
		StepAction:      linear.AgentSessionEventActionCreated,
		Event: linear.AgentSessionEvent{
			Action: linear.AgentSessionEventActionCreated,
			AgentSession: linear.AgentSession{
				ID:            "session-1",
				PromptContext: "<issue identifier=\"YR-O96Q\"><title>Define Linear agent protocol contract</title></issue>",
				Issue: &linear.AgentIssue{
					ID:         "iss-delegated-1",
					Identifier: "YR-O96Q",
				},
				Comment: &linear.AgentComment{
					ID:   "comment-1",
					Body: "@yolo-agent implement this task",
				},
			},
		},
	}

	if err := processor.Process(context.Background(), job); err != nil {
		t.Fatalf("process created job: %v", err)
	}

	if !sawReadIssueWorkflowStates {
		t.Fatalf("expected delegated issue workflow states to be read before run")
	}
	if updatedStateID != "st-started-b" {
		t.Fatalf("expected transition to first started workflow state, got %q", updatedStateID)
	}
}

func TestLinearSessionJobProcessorProcessTreatsFailedAndBlockedResultsAsErrors(t *testing.T) {
	testCases := []struct {
		name           string
		status         contracts.RunnerResultStatus
		reason         string
		expectCategory string
	}{
		{name: "failed status", status: contracts.RunnerResultFailed, reason: "opencode stall waiting for output", expectCategory: "runtime"},
		{name: "blocked status", status: contracts.RunnerResultBlocked, reason: "runner timeout after 5m", expectCategory: "runtime"},
	}

	for _, tc := range testCases {
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

			err := processor.Process(context.Background(), queuedLinearJobFixture(linear.AgentSessionEventActionCreated))
			if err == nil {
				t.Fatalf("expected non-completed runner status to return an error")
			}
			if !strings.Contains(err.Error(), tc.reason) {
				t.Fatalf("expected returned error to include runner reason %q, got %q", tc.reason, err.Error())
			}

			if len(activities.responses) != 1 {
				t.Fatalf("expected response activity to be emitted once, got %d", len(activities.responses))
			}
			responseBody := activities.responses[0].Body
			if !strings.Contains(responseBody, "Failed processing Linear session") {
				t.Fatalf("expected failure preface in response activity body, got %q", responseBody)
			}
			if !strings.Contains(responseBody, "Category: "+tc.expectCategory) {
				t.Fatalf("expected actionable category in response activity body, got %q", responseBody)
			}
			if !strings.Contains(responseBody, "Next step:") {
				t.Fatalf("expected remediation guidance in response activity body, got %q", responseBody)
			}
		})
	}
}

func TestFirstStartedWorkflowStateIDUsesWorkflowOrder(t *testing.T) {
	states := []struct {
		ID   string `json:"id"`
		Type string `json:"type"`
		Name string `json:"name"`
	}{
		{ID: "st-started-z", Type: "started", Name: "In Progress Z"},
		{ID: "st-started-a", Type: "started", Name: "In Progress A"},
		{ID: "st-done", Type: "completed", Name: "Done"},
	}

	stateID, ok := firstStartedWorkflowStateID(states)
	if !ok {
		t.Fatalf("expected started workflow state to be found")
	}
	if stateID != "st-started-z" {
		t.Fatalf("expected first started workflow state by position, got %q", stateID)
	}
}

func TestLinearIssueStarterClientSkipsTransitionWhenIssueAlreadyStartedOrTerminal(t *testing.T) {
	testCases := []struct {
		name      string
		stateType string
		stateName string
	}{
		{name: "started", stateType: "started", stateName: "In Progress"},
		{name: "completed", stateType: "completed", stateName: "Done"},
		{name: "canceled", stateType: "canceled", stateName: "Canceled"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var updateCalls int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Helper()
				if got := strings.TrimSpace(r.Header.Get("Authorization")); got != "Bearer lin_api_test" {
					t.Fatalf("expected Authorization header with bearer token, got %q", got)
				}

				var payload struct {
					Query string `json:"query"`
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Fatalf("decode GraphQL request body: %v", err)
				}

				switch {
				case strings.Contains(payload.Query, "ReadIssueWorkflowForDelegatedRun"):
					_, _ = w.Write([]byte(`{
  "data": {
    "issue": {
      "id": "iss-delegated-2",
      "state": {"type": "` + tc.stateType + `", "name": "` + tc.stateName + `"},
      "team": {
        "states": {
          "nodes": [
            {"id": "st-started-1", "type": "started", "name": "In Progress"},
            {"id": "st-done", "type": "completed", "name": "Done"}
          ]
        }
      }
    }
  }
}`))
				case strings.Contains(payload.Query, "UpdateIssueWorkflowStateForDelegatedRun"):
					updateCalls++
					_, _ = w.Write([]byte(`{"data":{"issueUpdate":{"success":true}}}`))
				default:
					t.Fatalf("unexpected GraphQL query: %q", payload.Query)
				}
			}))
			t.Cleanup(server.Close)

			client := &linearIssueStarterClient{
				endpoint:   server.URL,
				token:      "lin_api_test",
				httpClient: server.Client(),
			}
			if err := client.EnsureIssueStarted(context.Background(), "iss-delegated-2"); err != nil {
				t.Fatalf("EnsureIssueStarted returned error: %v", err)
			}
			if updateCalls != 0 {
				t.Fatalf("expected no state transition mutation for %s state, got %d updates", tc.name, updateCalls)
			}
		})
	}
}

func queuedLinearJobFixture(action linear.AgentSessionEventAction) webhook.Job {
	return webhook.Job{
		ContractVersion: webhook.JobContractVersion1,
		ID:              "evt-created-1",
		IdempotencyKey:  "session-1:created:event:evt-created-1",
		SessionID:       "session-1",
		StepID:          "step-created-1",
		StepAction:      action,
		Event: linear.AgentSessionEvent{
			Action: action,
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
}
