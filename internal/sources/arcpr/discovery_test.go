package arcpr

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/egv/yolo-runner/v2/internal/arcanum"
	"github.com/egv/yolo-runner/v2/internal/arcreview"
	arcreviewstate "github.com/egv/yolo-runner/v2/internal/arcreview/state"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

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
	assertPRReviewSubmission(t, first[0], "arcpr-adapta", "adapta", "101", "rev-1", nil, true)
	assertPRReviewSubmission(t, first[1], "arcpr-adapta", "adapta", "103", "rev-3", nil, true)

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
	assertPRReviewSubmission(t, first[0], "arcpr-adapta", "adapta", "101", "rev-1", []string{"c-new"}, false)

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

func TestSourcePollUsesDefaultIncomingDiscoveryAndRuntimeStateWithoutWorkspacePinning(t *testing.T) {
	ctx := context.Background()
	state := openDiscoveryTestState(t)
	if err := state.StoreReviewedRevision(ctx, "101", "rev-1"); err != nil {
		t.Fatalf("StoreReviewedRevision(101) error = %v", err)
	}
	if err := state.StoreAnsweredCommentIDs(ctx, "101", []string{"c-old"}); err != nil {
		t.Fatalf("StoreAnsweredCommentIDs() error = %v", err)
	}

	binDir := t.TempDir()
	callsPath := filepath.Join(t.TempDir(), "calls.log")
	t.Setenv("ARC_TOKEN", "test-token")
	t.Setenv("ARC_SOURCE_TEST_CALLS", callsPath)

	discoveryMu := sync.Mutex{}
	discoveryCalls := make([]string, 0, 4)
	discoveryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		discoveryMu.Lock()
		discoveryCalls = append(discoveryCalls, r.URL.RequestURI())
		discoveryMu.Unlock()

		query := r.URL.Query()
		reviewer := strings.TrimSpace(query.Get("reviewer"))
		author := strings.TrimSpace(query.Get("author"))
		if query.Get("status") != "open" {
			http.Error(w, "status=open is required", http.StatusBadRequest)
			return
		}

		switch {
		case reviewer == "alice":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`[
				{"id":"101","from_id":"rev-1","status":"open","summary":"reviewed head with comment","reviewers":["alice"]},
				{"id":"102","from_id":"rev-2","status":"open","summary":"new head","reviewers":["alice"]},
				{"id":"101","from_id":"rev-1","status":"open","summary":"duplicate reviewed head","reviewers":["alice"]}
			]`))
		case author == "alice":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`[
				{"id":"103","from_id":"rev-3","status":"open","summary":"authored head","author":"alice"}
			]`))
		default:
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte("[]"))
		}
	}))
	defer discoveryServer.Close()

	apiClient, err := arcanum.NewAPIClient(arcanum.APIClientConfig{BaseURL: discoveryServer.URL})
	if err != nil {
		t.Fatalf("NewAPIClient() error = %v", err)
	}

	writeDiscoveryFakeExecutable(t, binDir, "arc", `#!/bin/sh
set -eu
printf 'unexpected arc discovery call: %s\n' "$*" >&2
exit 7
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
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	src := &Source{
		SourceName: "arcpr-adapta",
		Preset:     "adapta",
		Reviewer:   "alice",
		State:      state,
		APIClient:  apiClient,
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
	if len(first) != 3 {
		t.Fatalf("Poll() returned %d submissions, want 3: %#v", len(first), first)
	}
	assertPRReviewSubmission(t, first[0], "arcpr-adapta", "adapta", "101", "rev-1", []string{"c-new"}, false)
	assertPRReviewSubmission(t, first[1], "arcpr-adapta", "adapta", "102", "rev-2", nil, false)
	assertPRReviewSubmission(t, first[2], "arcpr-adapta", "adapta", "103", "rev-3", nil, false)

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

	discoveryMu.Lock()
	calls := append([]string(nil), discoveryCalls...)
	discoveryMu.Unlock()
	if len(calls) != 6 {
		t.Fatalf("discovery API request count = %d, want 6", len(calls))
	}
	assertStringsEqualInOrder(t, calls,
		"/v1/review-requests?reviewer=alice&status=open",
		"/v1/review-requests?author=alice&status=open",
		"/v1/review-requests?reviewer=alice&status=open",
		"/v1/review-requests?author=alice&status=open",
		"/v1/review-requests?reviewer=alice&status=open",
		"/v1/review-requests?author=alice&status=open",
	)

	rawCalls, err := os.ReadFile(callsPath)
	if err != nil {
		t.Fatalf("ReadFile(calls) error = %v", err)
	}
	arcCalls := strings.Split(strings.TrimSpace(string(rawCalls)), "\n")
	for _, call := range arcCalls {
		switch {
		case strings.Contains(call, "arc mount --list --json"):
			t.Fatalf("discovery should not call arc mount --list --json: %v", arcCalls)
		case strings.Contains(call, "arc pr list --json --reviewer alice --status open"):
			t.Fatalf("discovery should not call arc pr list for reviewer: %v", arcCalls)
		case strings.Contains(call, "arc pr list --json --author alice --status open"):
			t.Fatalf("discovery should not call arc pr list for author: %v", arcCalls)
		}
	}
}

func TestSourcePollSkipsDefaultIncomingDiscoveryWithMissingReviewer(t *testing.T) {
	ctx := context.Background()
	state := openDiscoveryTestState(t)

	discoveryMu := sync.Mutex{}
	discoveryCalls := make([]string, 0, 1)
	discoveryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		discoveryMu.Lock()
		discoveryCalls = append(discoveryCalls, r.URL.RequestURI())
		discoveryMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("[]"))
	}))
	defer discoveryServer.Close()

	apiClient, err := arcanum.NewAPIClient(arcanum.APIClientConfig{BaseURL: discoveryServer.URL})
	if err != nil {
		t.Fatalf("NewAPIClient() error = %v", err)
	}

	src := &Source{
		SourceName: "arcpr-adapta",
		Preset:     "adapta",
		Reviewer:   "",
		State:      state,
		APIClient:  apiClient,
	}

	submissions, err := src.Poll(ctx)
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	if len(submissions) != 0 {
		t.Fatalf("Poll() with missing reviewer = %#v, want none", submissions)
	}

	discoveryMu.Lock()
	calls := len(discoveryCalls)
	discoveryMu.Unlock()
	if calls != 0 {
		t.Fatalf("discovery API request count = %d, want 0 for missing reviewer", calls)
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

func assertPRReviewSubmission(t *testing.T, got workqueue.Submission, sourceName string, preset string, prID string, revision string, comments []string, ship bool) {
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

	wantKey := strings.Join([]string{sourceName, "pr-review", prID, revision, testCommentSetHash(comments)}, "/")
	if got.IdempotencyKey != wantKey {
		t.Fatalf("submission idempotency key = %q, want %q", got.IdempotencyKey, wantKey)
	}

	payload, err := workitem.DecodePRReviewPayload(got.Payload)
	if err != nil {
		t.Fatalf("DecodePRReviewPayload() error = %v", err)
	}
	if payload.PRID != prID || payload.Revision != revision || payload.Ship != ship || !reflect.DeepEqual(payload.UnansweredCommentIDs, comments) {
		t.Fatalf("payload = %#v, want PR %q revision %q comments %#v ship %v", payload, prID, revision, comments, ship)
	}
}

func testCommentSetHash(commentIDs []string) string {
	normalized := append([]string(nil), commentIDs...)
	sort.Strings(normalized)
	hash := sha256.New()
	for _, id := range normalized {
		hash.Write([]byte(id))
		hash.Write([]byte{0})
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

func assertStringsEqualInOrder(t *testing.T, got []string, want ...string) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d in order: %#v", len(got), len(want), got)
	}
	for i, wantValue := range want {
		if got[i] != wantValue {
			t.Fatalf("entry %d = %q, want %q", i, got[i], wantValue)
		}
	}
}
