package main

import (
	"context"
	"path/filepath"
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

type captureLinearSessions struct {
	updates []linear.UpdateAgentSessionExternalURLsInput
	err     error
}

func (s *captureLinearSessions) SetExternalURLs(_ context.Context, input linear.UpdateAgentSessionExternalURLsInput) error {
	s.updates = append(s.updates, input)
	return s.err
}

func TestLinearSessionJobProcessorCreatedThenPrompted_ContinuesWithFollowUpInput(t *testing.T) {
	runner := &captureLinearRunner{
		result: contracts.RunnerResult{Status: contracts.RunnerResultCompleted},
	}
	activities := &captureLinearActivities{}
	sessions := &captureLinearSessions{}
	repoRoot := t.TempDir()

	processor := &linearSessionJobProcessor{
		repoRoot:   repoRoot,
		backend:    "codex",
		runner:     runner,
		activities: activities,
		sessions:   sessions,
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

	if len(sessions.updates) != 2 {
		t.Fatalf("expected two session externalUrls updates, got %d", len(sessions.updates))
	}
	expectedSessionURL := fileURLForPath(filepath.Join(repoRoot, "runner-logs", "codex"))
	expectedLogURLs := map[string]struct{}{
		fileURLForPath(filepath.Join(repoRoot, "runner-logs", "codex", "evt-created-1.jsonl")):  {},
		fileURLForPath(filepath.Join(repoRoot, "runner-logs", "codex", "evt-prompted-1.jsonl")): {},
	}
	for i, update := range sessions.updates {
		if update.AgentSessionID != "session-1" {
			t.Fatalf("expected update[%d] session id to be session-1, got %q", i, update.AgentSessionID)
		}
		if len(update.ExternalURLs) < 2 {
			t.Fatalf("expected update[%d] to include runner session/log urls, got %#v", i, update.ExternalURLs)
		}
		seen := map[string]struct{}{}
		sawSessionURL := false
		sawLogURL := false
		for _, entry := range update.ExternalURLs {
			key := entry.Label + "\n" + entry.URL
			if _, exists := seen[key]; exists {
				t.Fatalf("expected update[%d] external urls to be unique, duplicate=%#v", i, entry)
			}
			seen[key] = struct{}{}
			if entry.Label == runnerSessionExternalURLLabel && entry.URL == expectedSessionURL {
				sawSessionURL = true
			}
			if entry.Label == runnerLogExternalURLLabel {
				if _, ok := expectedLogURLs[entry.URL]; ok {
					sawLogURL = true
				}
			}
		}
		if !sawSessionURL {
			t.Fatalf("expected update[%d] to include session url %q", i, expectedSessionURL)
		}
		if !sawLogURL {
			t.Fatalf("expected update[%d] to include one of runner log urls %#v", i, expectedLogURLs)
		}
	}
}

func TestMergeLinearExternalURLs_PreservesExistingAndDeduplicates(t *testing.T) {
	existing := []linear.AgentExternalURL{
		{Label: "Session Notes", URL: "https://example.test/session-notes"},
		{Label: "Runner Session", URL: "file:///tmp/runner-logs/codex"},
		{Label: "Session Notes", URL: "https://example.test/session-notes"},
	}
	additions := []linear.AgentExternalURL{
		{Label: "Runner Session", URL: "file:///tmp/runner-logs/codex"},
		{Label: "Runner Log", URL: "file:///tmp/runner-logs/codex/job-1.jsonl"},
		{Label: "Runner Log", URL: "file:///tmp/runner-logs/codex/job-1.jsonl"},
	}

	merged := mergeLinearExternalURLs(existing, additions)
	if len(merged) != 3 {
		t.Fatalf("expected 3 unique urls, got %d %#v", len(merged), merged)
	}
	if merged[0].Label != "Session Notes" || merged[0].URL != "https://example.test/session-notes" {
		t.Fatalf("expected first merged entry to preserve existing custom url, got %#v", merged[0])
	}
	if merged[1].Label != "Runner Session" || merged[1].URL != "file:///tmp/runner-logs/codex" {
		t.Fatalf("expected second merged entry to preserve unique runner session url, got %#v", merged[1])
	}
	if merged[2].Label != "Runner Log" || merged[2].URL != "file:///tmp/runner-logs/codex/job-1.jsonl" {
		t.Fatalf("expected third merged entry to append unique runner log url, got %#v", merged[2])
	}
}
