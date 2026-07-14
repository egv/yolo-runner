package arcpr

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/arcanum"
	"github.com/egv/yolo-runner/v2/internal/arcreview"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

// Writeback must fetch PR state over the API, never by preparing an Arc
// checkout: a checkout takes the per-PR lock, and while an implement worker
// holds it for a full agent run, a source blocked on it stops polling and
// discovering PRs entirely (this starved discovery in production on
// 2026-07-14 and a review window was missed).
func TestSourceHandleResultFanOutUsesAPIStateWithoutArcCheckout(t *testing.T) {
	ctx := context.Background()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/review-requests/42" {
			t.Fatalf("unexpected API path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"data":{
  "id":42,"summary":"the PR","author":{"name":"alice"},"state":"open",
  "vcs":{"from_branch":"users/alice/pr-42","to_branch":"trunk"},
  "active_diff_set":{"id":"r7"}
}}`)); err != nil {
			t.Fatalf("write response: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	apiClient, err := arcanum.NewAPIClient(arcanum.APIClientConfig{
		BaseURL:     server.URL + "/api",
		HTTPClient:  server.Client(),
		TokenSource: func(context.Context) (string, error) { return "test-token", nil },
	})
	if err != nil {
		t.Fatalf("NewAPIClient() error = %v", err)
	}

	binDir := t.TempDir()
	// Any arc invocation means writeback tried to mount a checkout — fail loud.
	writeDiscoveryFakeExecutable(t, binDir, "arc", `#!/bin/sh
set -eu
printf 'writeback must not invoke arc (got: %s)\n' "$*" >&2
exit 7
`)
	writeDiscoveryFakeExecutable(t, binDir, "curl", `#!/bin/sh
set -eu
case "$*" in
"-fsSL -H Authorization: OAuth test-token https://a.yandex-team.ru/api/v1/public/review-requests/42/comments")
  printf '%s\n' '{"data":[{"id":"comment-1","content":"please add a nil guard","issue_status":"open"}]}'
  ;;
*)
  printf 'unexpected curl args: %s\n' "$*" >&2
  exit 7
  ;;
esac
`)
	t.Setenv("ARC_TOKEN", "test-token")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	client := &fakeArcPRWritebackClient{}
	src := arcPRAuthorImplementTestSource(t, client, true, true)
	src.StateFetcher = nil // force the default writeback state path
	src.APIClient = apiClient
	src.ResolveApplier = arcreview.ResolveApplier{Client: client, Store: src.State}

	item := workitem.Item{
		ID:        "review-item-1",
		Kind:      workitem.KindPRReview,
		SourceRef: "pr:42",
		Preset:    "adapta",
		Payload: mustMarshalArcPRWriteback(t, workitem.PRReviewPayload{
			PRID:     "42",
			Revision: "r7",
			Mode:     workitem.PRReviewModeAuthor,
		}),
	}
	result := workqueue.Result{
		Status: workqueue.ResultStatusCompleted,
		Payload: mustMarshalArcPRWriteback(t, workitem.PRReviewResult{
			CommentDecisions: []workitem.PRReviewCommentDecision{
				{
					CommentID: "comment-1",
					Decision:  workitem.PRReviewCommentDecisionImplement,
					Scope: &workitem.PRReviewImplementScope{
						Title:        "Add nil guard",
						Instructions: "Return early when the value is nil.",
					},
				},
			},
		}),
	}

	if _, err := src.HandleResult(ctx, item, result); err != nil {
		t.Fatalf("HandleResult() error = %v", err)
	}

	claimed, err := src.Queue.Claim("runner-a", []string{"adapta"}, time.Minute)
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if claimed == nil || claimed.Kind != workitem.KindImplement {
		t.Fatalf("claimed = %#v, want the fanned-out implement item", claimed)
	}
	implement, err := workitem.DecodeImplementPayload(claimed.Payload)
	if err != nil {
		t.Fatalf("DecodeImplementPayload() error = %v", err)
	}
	meta := implement.PromptContext.Metadata
	if meta["arc_pr_branch"] != "users/alice/pr-42" || meta["arc_pr_author"] != "alice" {
		t.Fatalf("implement metadata = %#v, want branch/author from the API state", meta)
	}
}
