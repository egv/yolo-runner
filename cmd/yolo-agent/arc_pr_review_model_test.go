package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/arcreview"
	"github.com/egv/yolo-runner/v2/internal/contracts"
)

func TestArcPRReviewModelReturnsRunnerPayload(t *testing.T) {
	payload := []byte(`{"summary":"looks good","inline_comments":[],"replies":[],"blockers":[],"ship":{"verdict":"ship","reason":"green"}}`)
	runner := &fakeArcPRReviewModelRunner{payload: payload}
	state := arcreview.PRRuntimeState{
		PRID:     "PR-123",
		Revision: "rev-2",
		Details: arcreview.PRDetails{
			Title:    "Add review helper",
			Revision: "rev-2",
		},
		ChangedFiles: []arcreview.PRChangedFile{{
			Path:   "cmd/yolo-agent/arc_pr_review_model.go",
			Status: "modified",
			Diff:   "@@ -0,0 +1 @@\n+package main",
		}},
	}

	got, err := runArcPRReviewModel(context.Background(), runner, arcPRReviewModelInput{
		State:      state,
		Model:      "gpt-review",
		RepoRoot:   "/repo",
		Timeout:    time.Minute,
		MaxRetries: 2,
		Metadata:   map[string]string{"phase": "arc_pr_review"},
	})
	if err != nil {
		t.Fatalf("runArcPRReviewModel() returned error: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("runArcPRReviewModel() payload = %q, want %q", string(got), string(payload))
	}

	if len(runner.requests) != 1 {
		t.Fatalf("expected one runner request, got %d", len(runner.requests))
	}
	request := runner.requests[0]
	if request.Mode != contracts.RunnerModeReview {
		t.Fatalf("expected review mode, got %q", request.Mode)
	}
	if request.TaskID != "PR-123" {
		t.Fatalf("expected PR ID task id, got %q", request.TaskID)
	}
	if request.Model != "gpt-review" || request.RepoRoot != "/repo" || request.Timeout != time.Minute || request.MaxRetries != 2 {
		t.Fatalf("unexpected runner request routing fields: %#v", request)
	}
	if request.Metadata["phase"] != "arc_pr_review" {
		t.Fatalf("expected metadata to be forwarded, got %#v", request.Metadata)
	}
	if !strings.Contains(request.Prompt, "Action: review_revision") || !strings.Contains(request.Prompt, "ID: PR-123") {
		t.Fatalf("expected review prompt to include action and PR ID, got:\n%s", request.Prompt)
	}
}

type fakeArcPRReviewModelRunner struct {
	payload  []byte
	requests []contracts.RunnerRequest
}

func (f *fakeArcPRReviewModelRunner) Run(_ context.Context, request contracts.RunnerRequest) (contracts.RunnerResult, error) {
	f.requests = append(f.requests, request)
	if request.OnProgress != nil {
		request.OnProgress(contracts.RunnerProgress{
			Type:    string(contracts.EventTypeRunnerOutput),
			Message: string(f.payload),
		})
	}
	return contracts.RunnerResult{Status: contracts.RunnerResultCompleted}, nil
}
