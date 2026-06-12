package arcpr

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
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
	if err := state.StoreReviewedRevision(ctx, "103", "rev-3"); err != nil {
		t.Fatalf("StoreReviewedRevision(103) error = %v", err)
	}
	if err := state.StoreAnsweredCommentIDs(ctx, "102", []string{"c-old"}); err != nil {
		t.Fatalf("StoreAnsweredCommentIDs(102) error = %v", err)
	}
	if err := state.StoreAnsweredCommentIDs(ctx, "103", []string{"c-old"}); err != nil {
		t.Fatalf("StoreAnsweredCommentIDs(103) error = %v", err)
	}

	var listedWorkspaces []string
	var fetchedPRs []string
	writebackClient := &fakeArcPRWritebackClient{}
	src := &Source{
		SourceName: "arcpr-adapta",
		Preset:     "adapta",
		Reviewer:   "alice",
		Workspaces: []string{"/arcadia/reviews/a", "/arcadia/reviews/b"},
		Branches:   []string{"trunk"},
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
		Lister: PRListerFunc(func(_ context.Context, workspace string) ([]arcanum.PRSummary, error) {
			listedWorkspaces = append(listedWorkspaces, workspace)
			switch workspace {
			case "/arcadia/reviews/a":
				return []arcanum.PRSummary{
					{ID: "101", Reviewers: []string{"alice"}, Branch: "trunk", Status: "open"},
					{ID: "102", Reviewers: []string{"alice"}, Branch: "trunk", Status: "open"},
					{ID: "wrong-reviewer", Reviewers: []string{"bob"}, Branch: "trunk", Status: "open"},
				}, nil
			case "/arcadia/reviews/b":
				return []arcanum.PRSummary{
					{ID: "101", Reviewers: []string{"alice"}, Branch: "trunk", Status: "open"},
					{ID: "103", Reviewers: []string{"alice"}, Branch: "trunk", Status: "open"},
					{ID: "wrong-branch", Reviewers: []string{"alice"}, Branch: "release", Status: "open"},
				}, nil
			default:
				t.Fatalf("unexpected workspace %q", workspace)
				return nil, nil
			}
		}),
		StateFetcher: PRStateFetcherFunc(func(_ context.Context, workspace string, prID string) (arcreview.PRRuntimeState, error) {
			fetchedPRs = append(fetchedPRs, workspace+":"+prID)
			switch prID {
			case "101":
				return arcreview.PRRuntimeState{
					PRID:     "101",
					Revision: "rev-1",
					Details:  arcreview.PRDetails{ID: "101", Status: "open", Revision: "rev-1"},
					Comments: []arcreview.PRComment{
						{ID: "c2", Body: "second open comment"},
						{ID: "c1", Body: "first open comment"},
						{ID: "c-resolved", Resolved: true},
						{ID: "c-already-answered", Answered: true},
					},
				}, nil
			case "102":
				return arcreview.PRRuntimeState{
					PRID:     "102",
					Revision: "rev-2",
					Details:  arcreview.PRDetails{ID: "102", Status: "open", Revision: "rev-2"},
					Comments: []arcreview.PRComment{{ID: "c-old", Body: "already answered"}},
				}, nil
			case "103":
				return arcreview.PRRuntimeState{
					PRID:     "103",
					Revision: "rev-3",
					Details:  arcreview.PRDetails{ID: "103", Status: "open", Revision: "rev-3"},
					Comments: []arcreview.PRComment{
						{ID: "c-old", Body: "already answered"},
						{ID: "c-new", Body: "new open comment"},
					},
				}, nil
			default:
				t.Fatalf("unexpected PR fetch %q", prID)
				return arcreview.PRRuntimeState{}, nil
			}
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

	wantWorkspaces := []string{"/arcadia/reviews/a", "/arcadia/reviews/b", "/arcadia/reviews/a", "/arcadia/reviews/b"}
	if !reflect.DeepEqual(listedWorkspaces, wantWorkspaces) {
		t.Fatalf("listed workspaces = %#v, want %#v", listedWorkspaces, wantWorkspaces)
	}
	wantFetched := []string{
		"/arcadia/reviews/a:101",
		"/arcadia/reviews/a:102",
		"/arcadia/reviews/b:103",
		"/arcadia/reviews/a:101",
		"/arcadia/reviews/a:102",
		"/arcadia/reviews/b:103",
	}
	if !reflect.DeepEqual(fetchedPRs, wantFetched) {
		t.Fatalf("fetched PRs = %#v, want %#v", fetchedPRs, wantFetched)
	}

	if len(first) != 2 {
		t.Fatalf("Poll() returned %d submissions, want 2: %#v", len(first), first)
	}
	assertPRReviewSubmission(t, first[0], "arcpr-adapta", "adapta", "101", "rev-1", []string{"c1", "c2"}, true)
	assertPRReviewSubmission(t, first[1], "arcpr-adapta", "adapta", "103", "rev-3", []string{"c-new"}, true)

	for _, submission := range first {
		payload, err := workitem.DecodePRReviewPayload(submission.Payload)
		if err != nil {
			t.Fatalf("DecodePRReviewPayload(%q) error = %v", submission.IdempotencyKey, err)
		}
		resultPayload := workitem.PRReviewResult{
			Replies:          prReviewReplies(payload.UnansweredCommentIDs),
			RevisionReviewed: payload.Revision,
		}
		rawResult, err := json.Marshal(resultPayload)
		if err != nil {
			t.Fatalf("marshal result payload: %v", err)
		}
		if _, err := src.HandleResult(ctx, workitem.Item{
			Kind:           workitem.KindPRReview,
			SourceRef:      submission.SourceRef,
			IdempotencyKey: submission.IdempotencyKey,
			Payload:        submission.Payload,
		}, workqueue.Result{
			Status:  workqueue.ResultStatusCompleted,
			Payload: rawResult,
		}); err != nil {
			t.Fatalf("HandleResult(%q) error = %v", submission.IdempotencyKey, err)
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
