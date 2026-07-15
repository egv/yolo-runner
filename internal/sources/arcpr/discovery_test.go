package arcpr

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/arcanum"
	"github.com/egv/yolo-runner/v2/internal/arcreview"
	arcreviewstate "github.com/egv/yolo-runner/v2/internal/arcreview/state"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

func TestSourcePollCancelsSupersededPendingAuthorImplementItem(t *testing.T) {
	ctx := context.Background()
	state := openDiscoveryTestState(t)
	queue, err := workqueue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatalf("workqueue.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = queue.Close() })
	payload := func(title string) json.RawMessage {
		raw, err := json.Marshal(workitem.ImplementPayload{
			Title: title,
			PromptContext: workitem.ImplementPromptContext{Metadata: map[string]string{
				"origin":         "arcpr-author",
				"arc_pr_id":      "42",
				"arc_comment_id": "comment-1",
			}},
		})
		if err != nil {
			t.Fatalf("marshal implement payload: %v", err)
		}
		return raw
	}
	oldItem, err := queue.Enqueue(workqueue.Submission{
		Kind:           workitem.KindImplement,
		Source:         "arcpr-adapta",
		SourceRef:      "pr:42",
		IdempotencyKey: "arcpr/42/implement/comment-1/old",
		Preset:         "adapta",
		Payload:        payload("old"),
	})
	if err != nil {
		t.Fatalf("enqueue old implement: %v", err)
	}
	currentItem, err := queue.Enqueue(workqueue.Submission{
		Kind:           workitem.KindImplement,
		Source:         "arcpr-adapta",
		SourceRef:      "pr:42",
		IdempotencyKey: "arcpr/42/implement/comment-1/current",
		Preset:         "adapta",
		Payload:        payload("current"),
	})
	if err != nil {
		t.Fatalf("enqueue current implement: %v", err)
	}
	emptyAuthorReviewPayload, err := json.Marshal(workitem.PRReviewPayload{
		PRID: "42", Revision: "active", Mode: workitem.PRReviewModeAuthor,
	})
	if err != nil {
		t.Fatalf("marshal empty author review payload: %v", err)
	}
	emptyAuthorReview, err := queue.Enqueue(workqueue.Submission{
		Kind:           workitem.KindPRReview,
		Source:         "arcpr-adapta",
		SourceRef:      "pr:42",
		IdempotencyKey: "arcpr-adapta/pr-review/42/active/author",
		Preset:         "adapta",
		Payload:        emptyAuthorReviewPayload,
	})
	if err != nil {
		t.Fatalf("enqueue empty author review: %v", err)
	}
	if err := state.RecordCommentImplementItem(ctx, CommentImplementItemRecord{
		PRID:            "42",
		CommentID:       "comment-1",
		ImplementItemID: currentItem.ID,
		IdempotencyKey:  "arcpr/42/implement/comment-1/current",
		ReviewItemID:    "review-current",
	}); err != nil {
		t.Fatalf("RecordCommentImplementItem() error = %v", err)
	}

	src := &Source{
		SourceName: "arcpr-adapta",
		Preset:     "adapta",
		Author:     "alice",
		State:      state,
		Queue:      queue,
		Lister: PRListerFunc(func(context.Context) ([]arcanum.PRSummary, error) {
			return []arcanum.PRSummary{{ID: "42", FromID: "active", Author: "alice"}}, nil
		}),
		StateFetcher: PRStateFetcherFunc(func(_ context.Context, _ string, prID string) (arcreview.PRRuntimeState, error) {
			return arcreview.PRRuntimeState{PRID: prID, Details: arcreview.PRDetails{ID: prID}}, nil
		}),
	}
	if _, err := src.Poll(ctx); err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	oldDetail, err := queue.GetItem(oldItem.ID)
	if err != nil {
		t.Fatalf("GetItem(old): %v", err)
	}
	if oldDetail.Item.State != "cancelled" {
		t.Fatalf("old item state = %q, want cancelled", oldDetail.Item.State)
	}
	currentDetail, err := queue.GetItem(currentItem.ID)
	if err != nil {
		t.Fatalf("GetItem(current): %v", err)
	}
	if currentDetail.Item.State != "pending" {
		t.Fatalf("current item state = %q, want pending", currentDetail.Item.State)
	}
	reviewDetail, err := queue.GetItem(emptyAuthorReview.ID)
	if err != nil {
		t.Fatalf("GetItem(empty review): %v", err)
	}
	if reviewDetail.Item.State != "cancelled" {
		t.Fatalf("empty author review state = %q, want cancelled", reviewDetail.Item.State)
	}
}

func TestSourcePollSubmitsPRReviewItemsAndKeepsStableKeysAcrossPolls(t *testing.T) {
	ctx := context.Background()
	state := openDiscoveryTestState(t)
	if err := state.StoreReviewedRevision(ctx, "102", "rev-2"); err != nil {
		t.Fatalf("StoreReviewedRevision(102) error = %v", err)
	}
	if err := state.StoreReviewedRevision(ctx, "103", "rev-old"); err != nil {
		t.Fatalf("StoreReviewedRevision(103) error = %v", err)
	}

	var listCalls int
	writebackClient := &fakeArcPRWritebackClient{}
	src := &Source{
		SourceName: "arcpr-adapta",
		Preset:     "adapta",
		Reviewer:   "alice",
		AllowShip:  true,
		State:      state,
		ReplyApplier: arcreview.ReplyApplier{
			Client: writebackClient,
			Store:  state,
		},
		ReviewApplier: arcreview.ReviewApplier{
			Client: writebackClient,
			Store:  state,
		},
		ShipGate: arcreview.ShipGate{
			Client: writebackClient,
		},
		Lister: PRListerFunc(func(_ context.Context) ([]arcanum.PRSummary, error) {
			listCalls++
			return []arcanum.PRSummary{
				{ID: "101", FromID: "rev-1", Status: "open"},
				{ID: "102", FromID: "rev-2", Status: "open"},
				{ID: "103", FromID: "rev-3", Status: "open"},
				{ID: "101", FromID: "rev-1", Status: "open"},
			}, nil
		}),
		StateFetcher: PRStateFetcherFunc(func(_ context.Context, _ string, prID string) (arcreview.PRRuntimeState, error) {
			return arcreview.PRRuntimeState{
				PRID:     prID,
				Details:  arcreview.PRDetails{ID: prID},
				Comments: nil,
			}, nil
		}),
	}

	first, err := src.Poll(ctx)
	if err != nil {
		t.Fatalf("Poll(first) error = %v", err)
	}
	second, err := src.Poll(ctx)
	if err != nil {
		t.Fatalf("Poll(second) error = %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("Poll() was not stable across polls\nfirst:  %#v\nsecond: %#v", first, second)
	}

	if listCalls != 2 {
		t.Fatalf("review PR list calls = %d, want 2", listCalls)
	}

	if len(first) != 2 {
		t.Fatalf("Poll() returned %d submissions, want 2: %#v", len(first), first)
	}
	assertPRReviewSubmission(t, first[0], "arcpr-adapta", "adapta", "101", "rev-1", workitem.PRReviewModeReviewer, nil, true)
	assertPRReviewSubmission(t, first[1], "arcpr-adapta", "adapta", "103", "rev-3", workitem.PRReviewModeReviewer, nil, true)

	for _, submission := range first {
		payload, err := workitem.DecodePRReviewPayload(submission.Payload)
		if err != nil {
			t.Fatalf("DecodePRReviewPayload(%q) error = %v", submission.IdempotencyKey, err)
		}
		if err := state.StoreReviewedRevision(ctx, payload.PRID, payload.Revision); err != nil {
			t.Fatalf("StoreReviewedRevision(%q) error = %v", payload.PRID, err)
		}
	}

	afterResult, err := src.Poll(ctx)
	if err != nil {
		t.Fatalf("Poll(after result) error = %v", err)
	}
	if len(afterResult) != 0 {
		t.Fatalf("Poll(after result) returned %#v, want none", afterResult)
	}
}

func TestSourcePollSubmitsReviewedIncomingPRWhenUnansweredCommentsRemain(t *testing.T) {
	ctx := context.Background()
	state := openDiscoveryTestState(t)
	if err := state.StoreReviewedRevision(ctx, "101", "rev-1"); err != nil {
		t.Fatalf("StoreReviewedRevision() error = %v", err)
	}
	if err := state.StoreAnsweredCommentIDs(ctx, "101", []string{"c-old"}); err != nil {
		t.Fatalf("StoreAnsweredCommentIDs() error = %v", err)
	}

	var fetched []string
	src := &Source{
		SourceName: "arcpr-adapta",
		Preset:     "adapta",
		State:      state,
		Lister: PRListerFunc(func(_ context.Context) ([]arcanum.PRSummary, error) {
			return []arcanum.PRSummary{
				{ID: "101", FromID: "rev-1", Status: "open"},
			}, nil
		}),
		StateFetcher: PRStateFetcherFunc(func(_ context.Context, workspace string, prID string) (arcreview.PRRuntimeState, error) {
			fetched = append(fetched, workspace+":"+prID)
			return arcreview.PRRuntimeState{
				PRID:     prID,
				Revision: "rev-1",
				Details:  arcreview.PRDetails{ID: prID, Status: "open", Revision: "rev-1"},
				Comments: []arcreview.PRComment{
					{ID: "c-new", Body: "please address this"},
					{ID: "c-old", Body: "already answered"},
					{ID: "c-resolved", Resolved: true},
					{ID: "c-answered", Answered: true},
				},
			}, nil
		}),
	}

	first, err := src.Poll(ctx)
	if err != nil {
		t.Fatalf("Poll(first) error = %v", err)
	}
	second, err := src.Poll(ctx)
	if err != nil {
		t.Fatalf("Poll(second) error = %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("Poll() was not stable across polls\nfirst:  %#v\nsecond: %#v", first, second)
	}
	if want := []string{":101", ":101"}; !reflect.DeepEqual(fetched, want) {
		t.Fatalf("fetched PRs = %#v, want %#v", fetched, want)
	}
	if len(first) != 1 {
		t.Fatalf("Poll() returned %d submissions, want 1: %#v", len(first), first)
	}
	assertPRReviewSubmission(t, first[0], "arcpr-adapta", "adapta", "101", "rev-1", workitem.PRReviewModeReviewer, []string{"c-new"}, false)

	if err := state.StoreAnsweredCommentIDs(ctx, "101", []string{"c-new"}); err != nil {
		t.Fatalf("StoreAnsweredCommentIDs(c-new) error = %v", err)
	}
	afterAnswer, err := src.Poll(ctx)
	if err != nil {
		t.Fatalf("Poll(after answer) error = %v", err)
	}
	if len(afterAnswer) != 0 {
		t.Fatalf("Poll(after answer) returned %#v, want none", afterAnswer)
	}
}

// Automated reviewers stamp per-revision status summaries with
// `<!-- ai-reviewer: ... -->`. Those summaries ask for nothing and must never
// be triaged (each one answered = one thank-you reply posted per push,
// forever). The same bot's genuine review comments carry no marker and stay
// fully in scope — they must keep triggering triage.
func TestSourcePollIgnoresAIReviewerStatusSummaries(t *testing.T) {
	ctx := context.Background()
	state := openDiscoveryTestState(t)

	src := &Source{
		SourceName:        "arcpr-adapta",
		Preset:            "adapta",
		Author:            "alice",
		AuthorModeEnabled: true,
		State:             state,
		Lister: PRListerFunc(func(_ context.Context) ([]arcanum.PRSummary, error) {
			return []arcanum.PRSummary{
				{ID: "42", FromID: "rev-1", Status: "open", Author: "alice"},
			}, nil
		}),
		StateFetcher: PRStateFetcherFunc(func(_ context.Context, _ string, prID string) (arcreview.PRRuntimeState, error) {
			return arcreview.PRRuntimeState{
				PRID:     prID,
				Revision: "rev-1",
				Details:  arcreview.PRDetails{ID: prID, Status: "open", Revision: "rev-1", Author: "alice"},
				Comments: []arcreview.PRComment{
					{
						ID:     "summary-1",
						Author: "robot-auto-ai-minion",
						Body:   "Глянул новую ревизию, всё норм.\n\n<!-- ai-reviewer: reviewed_from_id=deadbeef -->",
					},
					{
						ID:          "real-1",
						Author:      "robot-auto-ai-minion",
						Body:        "скрипт не идемпотентен, на втором прогоне упадёт",
						IssueStatus: "open",
					},
					{
						ID:     "musing-1",
						Author: "robot-auto-ai-minion",
						Body:   "просто мысли вслух, без issue — реагировать не нужно",
					},
					{
						ID:     "human-1",
						Author: "colleague-bob",
						Body:   "а можно тут комментарий поправить?",
					},
				},
			}, nil
		}),
	}

	submissions, err := src.Poll(ctx)
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	if len(submissions) != 1 {
		t.Fatalf("Poll() = %d submissions, want 1: %#v", len(submissions), submissions)
	}
	payload, err := workitem.DecodePRReviewPayload(submissions[0].Payload)
	if err != nil {
		t.Fatalf("DecodePRReviewPayload() error = %v", err)
	}
	// The bot's open issue and the human comment need answers; the bot's
	// marker summary and issue-less musing must be invisible to triage.
	if !reflect.DeepEqual(payload.UnansweredCommentIDs, []string{"human-1", "real-1"}) {
		t.Fatalf("unanswered = %#v, want [human-1 real-1] (summary and issue-less bot musing ignored)", payload.UnansweredCommentIDs)
	}

	// A poll where only the marker summary and the issue-less bot musing
	// remain unanswered enqueues nothing.
	if err := state.StoreAnsweredCommentIDs(ctx, "42", []string{"real-1", "human-1"}); err != nil {
		t.Fatalf("StoreAnsweredCommentIDs() error = %v", err)
	}
	after, err := src.Poll(ctx)
	if err != nil {
		t.Fatalf("Poll(after answer) error = %v", err)
	}
	if len(after) != 0 {
		t.Fatalf("Poll(after answer) = %#v, want none — bot summaries and issue-less musings alone must not trigger triage", after)
	}
}

func TestSourcePollUsesDefaultIncomingDiscoveryAndRuntimeStateWithoutWorkspacePinning(t *testing.T) {
	ctx := context.Background()
	state := openDiscoveryTestState(t)
	if err := state.StoreReviewedRevision(ctx, "101", "rev-1"); err != nil {
		t.Fatalf("StoreReviewedRevision(101) error = %v", err)
	}
	if err := state.StoreAnsweredCommentIDs(ctx, "101", []string{"c-old"}); err != nil {
		t.Fatalf("StoreAnsweredCommentIDs() error = %v", err)
	}

	listCalls := make([]string, 0, 4)
	var listCallsMu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		listCallsMu.Lock()
		defer listCallsMu.Unlock()
		if got := r.Method; got != http.MethodGet {
			t.Fatalf("method = %s, want GET", got)
		}
		if got := r.URL.Path; got != "/api/v1/review-requests" {
			t.Fatalf("path = %q, want /api/v1/review-requests", got)
		}
		query := r.URL.Query()
		switch q := query.Get("query"); {
		case q == "subscriber(alice);open()":
			listCalls = append(listCalls, "reviewer")
			w.Header().Set("Content-Type", "application/json")
			if _, err := w.Write([]byte(`[
  {"id":"101","from_id":"rev-1","status":"open","summary":"reviewed head with comment","author":"bob","reviewers":[{"user":{"name":"alice"}}]},
  {"id":"102","from_id":"rev-2","status":"open","summary":"new head","author":"alice","reviewers":[{"user":{"name":"alice"}}]},
  {"id":"101","from_id":"rev-1","status":"open","summary":"duplicate reviewed head","author":"alice","reviewers":[{"user":{"name":"alice"}}]}
]`)); err != nil {
				t.Fatalf("write reviewer response: %v", err)
			}
		case q == "author(alice);open()":
			listCalls = append(listCalls, "author")
			w.Header().Set("Content-Type", "application/json")
			if _, err := w.Write([]byte(`{"data":[{"id":"103","from_id":"rev-3","status":"open","summary":"authored head","author":"alice"}]}`)); err != nil {
				t.Fatalf("write author response: %v", err)
			}
		default:
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
	}))
	t.Cleanup(server.Close)

	apiClient, err := arcanum.NewAPIClient(arcanum.APIClientConfig{
		BaseURL:    server.URL + "/api",
		HTTPClient: server.Client(),
		TokenSource: func(context.Context) (string, error) {
			return "test-token", nil
		},
	})
	if err != nil {
		t.Fatalf("NewAPIClient() error = %v", err)
	}

	binDir := t.TempDir()
	writeDiscoveryFakeExecutable(t, binDir, "arc", `#!/bin/sh
set -eu
printf 'arc %s\n' "$*" >> "$ARC_SOURCE_TEST_CALLS"
case "$*" in
"mount --list --json")
  echo "unexpected arc mount --list during discovery" >&2
  exit 7
  ;;
"pr list --json --reviewer alice --status open")
  echo "unexpected arc reviewer discovery via arc CLI" >&2
  exit 7
  ;;
"pr list --json --author alice --status open")
  echo "unexpected arc author discovery via arc CLI" >&2
  exit 7
  ;;
*)
  printf 'unexpected arc args: %s\n' "$*" >&2
  exit 7
  ;;
esac
`)
	writeDiscoveryFakeExecutable(t, binDir, "curl", `#!/bin/sh
set -eu
printf 'curl %s\n' "$*" >> "$ARC_SOURCE_TEST_CALLS"
case "$*" in
"-fsSL -H Authorization: OAuth test-token https://a.yandex-team.ru/api/v1/public/review-requests/101/comments")
  printf '%s\n' '{"data":[{"id":"c-new","content":"please answer","issue_status":"open"},{"id":"c-old","content":"already answered","issue_status":"open"},{"id":"c-resolved","content":"done","issue_status":"resolved"}]}'
  ;;
"-fsSL -H Authorization: OAuth test-token https://a.yandex-team.ru/api/v1/public/review-requests/102/comments")
  printf '%s\n' '{"data":[]}'
  ;;
"-fsSL -H Authorization: OAuth test-token https://a.yandex-team.ru/api/v1/public/review-requests/103/comments")
  printf '%s\n' '{"data":[]}'
  ;;
*)
  printf 'unexpected curl args: %s\n' "$*" >&2
  exit 7
  ;;
esac
`)
	callsPath := filepath.Join(t.TempDir(), "calls.log")
	t.Setenv("ARC_SOURCE_TEST_CALLS", callsPath)
	t.Setenv("ARC_TOKEN", "test-token")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	src := &Source{
		SourceName: "arcpr-adapta",
		Preset:     "adapta",
		Reviewer:   "alice",
		Author:     "alice",
		APIClient:  apiClient,
		State:      state,
	}

	first, err := src.Poll(ctx)
	if err != nil {
		t.Fatalf("Poll(first) error = %v", err)
	}
	second, err := src.Poll(ctx)
	if err != nil {
		t.Fatalf("Poll(second) error = %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("Poll() was not stable across polls\nfirst:  %#v\nsecond: %#v", first, second)
	}
	if len(first) != 1 {
		t.Fatalf("Poll() returned %d submissions, want 1: %#v", len(first), first)
	}
	assertPRReviewSubmission(t, first[0], "arcpr-adapta", "adapta", "101", "rev-1", workitem.PRReviewModeReviewer, []string{"c-new"}, false)

	for _, submission := range first {
		payload, err := workitem.DecodePRReviewPayload(submission.Payload)
		if err != nil {
			t.Fatalf("DecodePRReviewPayload(%q) error = %v", submission.IdempotencyKey, err)
		}
		if err := state.StoreReviewedRevision(ctx, payload.PRID, payload.Revision); err != nil {
			t.Fatalf("StoreReviewedRevision(%q) error = %v", payload.PRID, err)
		}
		if len(payload.UnansweredCommentIDs) > 0 {
			if err := state.StoreAnsweredCommentIDs(ctx, payload.PRID, payload.UnansweredCommentIDs); err != nil {
				t.Fatalf("StoreAnsweredCommentIDs(%q) error = %v", payload.PRID, err)
			}
		}
	}

	afterResult, err := src.Poll(ctx)
	if err != nil {
		t.Fatalf("Poll(after result) error = %v", err)
	}
	if len(afterResult) != 0 {
		t.Fatalf("Poll(after result) returned %#v, want none", afterResult)
	}

	listCallsMu.Lock()
	wantListCalls := []string{"reviewer", "author", "reviewer", "author", "reviewer", "author"}
	if !reflect.DeepEqual(listCalls, wantListCalls) {
		t.Fatalf("review list call order = %#v, want %#v", listCalls, wantListCalls)
	}
	listCallsMu.Unlock()
}

// The reviewer role covers only PRs where the login is an assigned reviewer.
// PRs the login merely watches as a subscriber must never be discovered,
// fetched, or submitted — Arcanum has no reviewer() DSL predicate, so the
// lister fetches the subscriber() superset and must narrow it client-side.
func TestSourcePollNeverSubmitsSubscriberOnlyPRs(t *testing.T) {
	ctx := context.Background()
	state := openDiscoveryTestState(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		switch q := query.Get("query"); q {
		case "subscriber(alice);open()":
			// PR 201: alice is an assigned reviewer. PR 202: alice only
			// subscribes — other people are the reviewers.
			if _, err := w.Write([]byte(`{"data":{"review_requests":[
  {"id":201,"status":"open","summary":"assigned to alice","author":{"name":"bob"},"reviewers":[{"user":{"name":"alice"}}],"active_diff_set":{"id":"rev-a"}},
  {"id":202,"status":"open","summary":"alice merely subscribes","author":{"name":"carol"},"reviewers":[{"user":{"name":"dave"}}],"active_diff_set":{"id":"rev-b"}}
]}}`)); err != nil {
				t.Fatalf("write reviewer response: %v", err)
			}
		case "author(alice);open()":
			if _, err := w.Write([]byte(`{"data":{"review_requests":[]}}`)); err != nil {
				t.Fatalf("write author response: %v", err)
			}
		default:
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
	}))
	t.Cleanup(server.Close)

	apiClient, err := arcanum.NewAPIClient(arcanum.APIClientConfig{
		BaseURL:    server.URL + "/api",
		HTTPClient: server.Client(),
		TokenSource: func(context.Context) (string, error) {
			return "test-token", nil
		},
	})
	if err != nil {
		t.Fatalf("NewAPIClient() error = %v", err)
	}

	binDir := t.TempDir()
	writeDiscoveryFakeExecutable(t, binDir, "arc", `#!/bin/sh
set -eu
printf 'unexpected arc args: %s\n' "$*" >&2
exit 7
`)
	// The comments endpoint may be called only for the assigned PR; a fetch
	// for the subscriber-only PR 202 fails the poll via the default case.
	writeDiscoveryFakeExecutable(t, binDir, "curl", `#!/bin/sh
set -eu
case "$*" in
"-fsSL -H Authorization: OAuth test-token https://a.yandex-team.ru/api/v1/public/review-requests/201/comments")
  printf '%s\n' '{"data":[]}'
  ;;
*)
  printf 'unexpected curl args: %s\n' "$*" >&2
  exit 7
  ;;
esac
`)
	t.Setenv("ARC_TOKEN", "test-token")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	src := &Source{
		SourceName: "arcpr-adapta",
		Preset:     "adapta",
		Reviewer:   "alice",
		Author:     "alice",
		APIClient:  apiClient,
		State:      state,
	}

	submissions, err := src.Poll(ctx)
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	if len(submissions) != 1 {
		t.Fatalf("Poll() returned %d submissions, want only the assigned-reviewer PR: %#v", len(submissions), submissions)
	}
	assertPRReviewSubmission(t, submissions[0], "arcpr-adapta", "adapta", "201", "rev-a", workitem.PRReviewModeReviewer, nil, false)
}

func TestSourcePollUsesAPIDefaultDiscoveryWhenReviewerMissing(t *testing.T) {
	ctx := context.Background()
	state := openDiscoveryTestState(t)

	listCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		listCalled = true
		t.Fatalf("expected no API list calls for blank reviewer, got %q %q", r.Method, r.URL.String())
	}))
	t.Cleanup(server.Close)

	apiClient, err := arcanum.NewAPIClient(arcanum.APIClientConfig{
		BaseURL:    server.URL + "/api",
		HTTPClient: server.Client(),
		TokenSource: func(context.Context) (string, error) {
			return "test-token", nil
		},
	})
	if err != nil {
		t.Fatalf("NewAPIClient() error = %v", err)
	}

	src := &Source{
		SourceName: "arcpr-adapta",
		Preset:     "adapta",
		Reviewer:   "  ",
		APIClient:  apiClient,
		State:      state,
	}
	prs, err := src.Poll(ctx)
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	if len(prs) != 0 {
		t.Fatalf("Poll() returned %#v, want no submissions", prs)
	}
	if listCalled {
		t.Fatalf("expected blank reviewer to skip API list calls")
	}
}

func openDiscoveryTestState(t *testing.T) *arcreviewstate.Store {
	t.Helper()

	store, err := arcreviewstate.Open(filepath.Join(t.TempDir(), "arcpr-source.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return store
}

func writeDiscoveryFakeExecutable(t *testing.T, dir string, name string, content string) {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake executable %s: %v", name, err)
	}
}

func assertPRReviewSubmission(t *testing.T, got workqueue.Submission, sourceName string, preset string, prID string, revision string, mode string, comments []string, ship bool) {
	t.Helper()

	if got.Kind != workitem.KindPRReview {
		t.Fatalf("submission kind = %q, want %q", got.Kind, workitem.KindPRReview)
	}
	if got.Source != sourceName {
		t.Fatalf("submission source = %q, want %q", got.Source, sourceName)
	}
	if got.SourceRef != "pr:"+prID {
		t.Fatalf("submission source_ref = %q, want %q", got.SourceRef, "pr:"+prID)
	}
	if got.Preset != preset {
		t.Fatalf("submission preset = %q, want %q", got.Preset, preset)
	}

	wantKey := strings.Join([]string{sourceName, "pr-review", prID, revision, mode, testCommentSetHash(comments, nil)}, "/")
	if got.IdempotencyKey != wantKey {
		t.Fatalf("submission idempotency key = %q, want %q", got.IdempotencyKey, wantKey)
	}

	payload, err := workitem.DecodePRReviewPayload(got.Payload)
	if err != nil {
		t.Fatalf("DecodePRReviewPayload() error = %v", err)
	}
	if payload.Mode != mode {
		t.Fatalf("payload mode = %q, want %q", payload.Mode, mode)
	}
	if payload.PRID != prID || payload.Revision != revision || payload.Ship != ship || !reflect.DeepEqual(payload.UnansweredCommentIDs, comments) {
		t.Fatalf("payload = %#v, want PR %q revision %q comments %#v ship %v", payload, prID, revision, comments, ship)
	}
}

func testCommentSetHash(commentIDs []string, triggering map[string]string) string {
	normalized := append([]string(nil), commentIDs...)
	sort.Strings(normalized)
	hash := sha256.New()
	for _, id := range normalized {
		hash.Write([]byte(id))
		hash.Write([]byte{0})
		if reply := strings.TrimSpace(triggering[id]); reply != "" {
			hash.Write([]byte("reply="))
			hash.Write([]byte(reply))
			hash.Write([]byte{0})
		}
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func prReviewReplies(commentIDs []string) []workitem.PRReviewReply {
	replies := make([]workitem.PRReviewReply, 0, len(commentIDs))
	for _, commentID := range commentIDs {
		replies = append(replies, workitem.PRReviewReply{
			CommentID: commentID,
			Body:      "handled",
		})
	}
	return replies
}

func arcPRReviewSourceWithComments(state *arcreviewstate.Store, reviewer string, comments []arcreview.PRComment) *Source {
	return &Source{
		SourceName: "arcpr-adapta",
		Preset:     "adapta",
		Reviewer:   reviewer,
		State:      state,
		Lister: PRListerFunc(func(_ context.Context) ([]arcanum.PRSummary, error) {
			return []arcanum.PRSummary{{ID: "101", FromID: "rev-1", Status: "open"}}, nil
		}),
		StateFetcher: PRStateFetcherFunc(func(_ context.Context, _ string, prID string) (arcreview.PRRuntimeState, error) {
			return arcreview.PRRuntimeState{
				PRID:     prID,
				Revision: "rev-1",
				Details:  arcreview.PRDetails{ID: prID, Status: "open", Revision: "rev-1"},
				Comments: comments,
			}, nil
		}),
	}
}

func threadContinuationBaseState(t *testing.T, ctx context.Context) *arcreviewstate.Store {
	t.Helper()
	state := openDiscoveryTestState(t)
	if err := state.StoreReviewedRevision(ctx, "101", "rev-1"); err != nil {
		t.Fatalf("StoreReviewedRevision() error = %v", err)
	}
	if err := state.StoreAnsweredCommentIDs(ctx, "101", []string{"c1"}); err != nil {
		t.Fatalf("StoreAnsweredCommentIDs() error = %v", err)
	}
	return state
}

func TestSourcePollReSurfacesAnsweredThreadOnNewNonSelfReply(t *testing.T) {
	ctx := context.Background()
	state := threadContinuationBaseState(t, ctx)

	lastSeen := time.Date(2026, 6, 24, 10, 0, 0, 0, time.UTC)
	if err := state.RecordThreadAnswered(ctx, "101", "c1", "r-agent", lastSeen); err != nil {
		t.Fatalf("RecordThreadAnswered() error = %v", err)
	}

	comments := []arcreview.PRComment{
		{ID: "c1", Author: "reviewer-bob", Body: "please fix", CreatedAt: lastSeen.Add(-2 * time.Hour)},
		{ID: "r-agent", ThreadID: "c1", Author: "yolo-agent", Body: "on it", Answered: true, CreatedAt: lastSeen},
		{ID: "r-new", ThreadID: "c1", Author: "reviewer-bob", Body: "still broken", Answered: true, CreatedAt: lastSeen.Add(time.Hour)},
	}
	src := arcPRReviewSourceWithComments(state, "yolo-agent", comments)

	first, err := src.Poll(ctx)
	if err != nil {
		t.Fatalf("Poll(first) error = %v", err)
	}
	second, err := src.Poll(ctx)
	if err != nil {
		t.Fatalf("Poll(second) error = %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("Poll() was not stable across polls\nfirst:  %#v\nsecond: %#v", first, second)
	}
	if len(first) != 1 {
		t.Fatalf("Poll() returned %d submissions, want 1 re-surfaced thread: %#v", len(first), first)
	}

	wantKey := strings.Join([]string{"arcpr-adapta", "pr-review", "101", "rev-1", "", testCommentSetHash([]string{"c1"}, map[string]string{"c1": "r-new"})}, "/")
	if first[0].IdempotencyKey != wantKey {
		t.Fatalf("IdempotencyKey = %q, want %q", first[0].IdempotencyKey, wantKey)
	}
	payload, err := workitem.DecodePRReviewPayload(first[0].Payload)
	if err != nil {
		t.Fatalf("DecodePRReviewPayload() error = %v", err)
	}
	if want := []string{"c1"}; !reflect.DeepEqual(payload.UnansweredCommentIDs, want) {
		t.Fatalf("UnansweredCommentIDs = %#v, want %#v", payload.UnansweredCommentIDs, want)
	}
}

func TestSourcePollDoesNotReSurfaceAnsweredThreadOnNewSelfReply(t *testing.T) {
	ctx := context.Background()
	state := threadContinuationBaseState(t, ctx)

	lastSeen := time.Date(2026, 6, 24, 10, 0, 0, 0, time.UTC)
	if err := state.RecordThreadAnswered(ctx, "101", "c1", "r-agent", lastSeen); err != nil {
		t.Fatalf("RecordThreadAnswered() error = %v", err)
	}

	comments := []arcreview.PRComment{
		{ID: "c1", Author: "reviewer-bob", Body: "please fix", CreatedAt: lastSeen.Add(-2 * time.Hour)},
		{ID: "r-agent", ThreadID: "c1", Author: "yolo-agent", Body: "on it", Answered: true, CreatedAt: lastSeen},
		{ID: "r-self", ThreadID: "c1", Author: "yolo-agent", Body: "already handled", Answered: true, CreatedAt: lastSeen.Add(time.Hour)},
	}
	src := arcPRReviewSourceWithComments(state, "yolo-agent", comments)

	subs, err := src.Poll(ctx)
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	if len(subs) != 0 {
		t.Fatalf("Poll() returned %#v, want no submissions for a self reply", subs)
	}
}

func TestSourcePollSilencesAnsweredThreadAfterLastSeenAdvances(t *testing.T) {
	ctx := context.Background()
	state := threadContinuationBaseState(t, ctx)

	lastSeen := time.Date(2026, 6, 24, 10, 0, 0, 0, time.UTC)
	rNewAt := lastSeen.Add(time.Hour)
	if err := state.RecordThreadAnswered(ctx, "101", "c1", "r-new", rNewAt); err != nil {
		t.Fatalf("RecordThreadAnswered() error = %v", err)
	}

	comments := []arcreview.PRComment{
		{ID: "c1", Author: "reviewer-bob", Body: "please fix", CreatedAt: lastSeen.Add(-2 * time.Hour)},
		{ID: "r-agent", ThreadID: "c1", Author: "yolo-agent", Body: "on it", Answered: true, CreatedAt: lastSeen},
		{ID: "r-new", ThreadID: "c1", Author: "reviewer-bob", Body: "still broken", Answered: true, CreatedAt: rNewAt},
	}
	src := arcPRReviewSourceWithComments(state, "yolo-agent", comments)

	subs, err := src.Poll(ctx)
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	if len(subs) != 0 {
		t.Fatalf("Poll() returned %#v, want no submissions once last_seen advanced", subs)
	}
}

func TestSourcePollReSurfacesAnsweredThreadOnNestedNonSelfReply(t *testing.T) {
	ctx := context.Background()
	state := threadContinuationBaseState(t, ctx)

	lastSeen := time.Date(2026, 6, 24, 10, 0, 0, 0, time.UTC)
	if err := state.RecordThreadAnswered(ctx, "101", "c1", "r-agent", lastSeen); err != nil {
		t.Fatalf("RecordThreadAnswered() error = %v", err)
	}

	comments := []arcreview.PRComment{
		{ID: "c1", Author: "reviewer-bob", Body: "please fix", CreatedAt: lastSeen.Add(-2 * time.Hour)},
		{ID: "r-agent", ThreadID: "c1", Author: "yolo-agent", Body: "on it", Answered: true, CreatedAt: lastSeen},
		{ID: "r-nested", ThreadID: "r-agent", Author: "reviewer-bob", Body: "reply to the agent", Answered: true, CreatedAt: lastSeen.Add(time.Hour)},
	}
	src := arcPRReviewSourceWithComments(state, "yolo-agent", comments)

	subs, err := src.Poll(ctx)
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	if len(subs) != 1 {
		t.Fatalf("Poll() returned %d submissions, want 1 nested re-surface: %#v", len(subs), subs)
	}
	wantKey := strings.Join([]string{"arcpr-adapta", "pr-review", "101", "rev-1", "", testCommentSetHash([]string{"c1"}, map[string]string{"c1": "r-nested"})}, "/")
	if subs[0].IdempotencyKey != wantKey {
		t.Fatalf("IdempotencyKey = %q, want %q", subs[0].IdempotencyKey, wantKey)
	}
}

func TestSourcePollSelectsAuthorModeForPRsAuthoredByConfiguredAuthor(t *testing.T) {
	ctx := context.Background()
	state := openDiscoveryTestState(t)
	src := &Source{
		SourceName: "arcpr-adapta",
		Preset:     "adapta",
		Reviewer:   "alice",
		Author:     "alice",
		State:      state,
		Lister: PRListerFunc(func(_ context.Context) ([]arcanum.PRSummary, error) {
			return []arcanum.PRSummary{
				{ID: "101", FromID: "rev-1", Status: "open", Author: "alice"}, // authored by alice -> author mode
				{ID: "102", FromID: "rev-2", Status: "open", Author: "bob"},   // authored by bob -> reviewer mode
				{ID: "103", FromID: "rev-3", Status: "open", Author: "Alice"}, // case-insensitive match -> author mode
			}, nil
		}),
		StateFetcher: PRStateFetcherFunc(func(_ context.Context, _ string, prID string) (arcreview.PRRuntimeState, error) {
			state := arcreview.PRRuntimeState{PRID: prID, Details: arcreview.PRDetails{ID: prID}}
			if prID == "101" || prID == "103" {
				state.Comments = []arcreview.PRComment{{ID: "comment-" + prID, Body: "Please address this", Answered: false}}
			}
			return state, nil
		}),
	}

	subs, err := src.Poll(ctx)
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	if len(subs) != 3 {
		t.Fatalf("Poll() returned %d submissions, want 3", len(subs))
	}

	modesByID := map[string]string{}
	for _, sub := range subs {
		payload, err := workitem.DecodePRReviewPayload(sub.Payload)
		if err != nil {
			t.Fatalf("decode payload for %q: %v", sub.SourceRef, err)
		}
		modesByID[payload.PRID] = payload.Mode
	}
	if modesByID["101"] != workitem.PRReviewModeAuthor {
		t.Fatalf("PR 101 (authored by alice) mode = %q, want author", modesByID["101"])
	}
	if modesByID["102"] != workitem.PRReviewModeReviewer {
		t.Fatalf("PR 102 (authored by bob) mode = %q, want reviewer", modesByID["102"])
	}
	if modesByID["103"] != workitem.PRReviewModeAuthor {
		t.Fatalf("PR 103 (authored by Alice, case differ) mode = %q, want author", modesByID["103"])
	}

	// Author and reviewer mode must produce distinct idempotency keys for the
	// same PR/revision so they don't collide in the queue.
	authKey := subs[0].IdempotencyKey
	reviewKey := subs[1].IdempotencyKey
	if authKey == reviewKey {
		t.Fatalf("author and reviewer idempotency keys collide: %q", authKey)
	}
}

func TestSourcePollDefaultsToReviewerModeWhenAuthorNotConfigured(t *testing.T) {
	ctx := context.Background()
	state := openDiscoveryTestState(t)
	src := &Source{
		SourceName: "arcpr-adapta",
		Preset:     "adapta",
		Reviewer:   "alice",
		State:      state,
		Lister: PRListerFunc(func(_ context.Context) ([]arcanum.PRSummary, error) {
			return []arcanum.PRSummary{
				{ID: "101", FromID: "rev-1", Status: "open", Author: "alice"},
			}, nil
		}),
		StateFetcher: PRStateFetcherFunc(func(_ context.Context, _ string, prID string) (arcreview.PRRuntimeState, error) {
			return arcreview.PRRuntimeState{PRID: prID, Details: arcreview.PRDetails{ID: prID}}, nil
		}),
	}

	subs, err := src.Poll(ctx)
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	if len(subs) != 1 {
		t.Fatalf("Poll() returned %d submissions, want 1", len(subs))
	}
	payload, err := workitem.DecodePRReviewPayload(subs[0].Payload)
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	// No author configured -> even a PR authored by the reviewer runs in
	// reviewer mode (back-compat with the pre-author-mode behavior).
	if payload.Mode != workitem.PRReviewModeReviewer {
		t.Fatalf("mode = %q, want reviewer (empty) when Author unset", payload.Mode)
	}
}
