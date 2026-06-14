package arcpr

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
		t.Fatalf("incoming PR list calls = %d, want 2", listCalls)
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
