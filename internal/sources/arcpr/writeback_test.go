package arcpr

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/egv/yolo-runner/v2/internal/arcreview"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

func TestSourceHandleResultAppliesRepliesReviewAndShipsWhenGateAllows(t *testing.T) {
	ctx := context.Background()
	state := openDiscoveryTestState(t)
	client := &fakeArcPRWritebackClient{}
	fetcher := &fakeArcPRWritebackStateFetcher{
		state: arcPRWritebackRuntimeState(false, []arcreview.PRCheck{{Name: "ci", Status: "passed"}}),
	}
	src := &Source{
		SourceName:   "arcpr-adapta",
		Workspaces:   []string{"/arcadia/project"},
		AllowShip:    true,
		State:        state,
		StateFetcher: fetcher,
		ReplyApplier: arcreview.ReplyApplier{
			Client: client,
			Store:  state,
		},
		ReviewApplier: arcreview.ReviewApplier{
			Client: client,
			Store:  state,
		},
		ShipGate: arcreview.ShipGate{
			Client: client,
		},
	}
	item := workitem.Item{
		Kind:      workitem.KindPRReview,
		SourceRef: "pr:42",
		Payload: mustMarshalArcPRWriteback(t, workitem.PRReviewPayload{
			PRID:     "42",
			Revision: "r7",
			Ship:     true,
		}),
	}
	replyResult := workqueue.Result{
		Status: workqueue.ResultStatusCompleted,
		Payload: mustMarshalArcPRWriteback(t, workitem.PRReviewResult{
			Replies: []workitem.PRReviewReply{
				{CommentID: "comment-1", Body: "The guard now covers this path."},
				{CommentID: "comment-2", Body: "Added the missing regression test."},
			},
		}),
	}

	if _, err := src.HandleResult(ctx, item, replyResult); err != nil {
		t.Fatalf("HandleResult(reply first) error = %v", err)
	}
	if _, err := src.HandleResult(ctx, item, replyResult); err != nil {
		t.Fatalf("HandleResult(reply retry) error = %v", err)
	}
	wantReplies := []arcPRWritebackReply{
		{prID: "42", commentID: "comment-1", body: "The guard now covers this path."},
		{prID: "42", commentID: "comment-2", body: "Added the missing regression test."},
	}
	if !reflect.DeepEqual(client.replies, wantReplies) {
		t.Fatalf("posted replies mismatch:\n got: %#v\nwant: %#v", client.replies, wantReplies)
	}
	answered, err := state.ListAnsweredCommentIDs(ctx, "42")
	if err != nil {
		t.Fatalf("ListAnsweredCommentIDs() error = %v", err)
	}
	if !reflect.DeepEqual(answered, []string{"comment-1", "comment-2"}) {
		t.Fatalf("answered comments = %#v, want both reply IDs", answered)
	}
	if len(client.ships) != 0 {
		t.Fatalf("reply result shipped PRs, want none: %#v", client.ships)
	}

	shipResult := workqueue.Result{
		Status: workqueue.ResultStatusCompleted,
		Payload: mustMarshalArcPRWriteback(t, workitem.PRReviewResult{
			ReviewVerdict:    "ship",
			ShipReady:        true,
			RevisionReviewed: "r7",
		}),
	}
	fetcher.state = arcPRWritebackRuntimeState(true, []arcreview.PRCheck{{Name: "ci", Status: "pending"}})
	if _, err := src.HandleResult(ctx, item, shipResult); err != nil {
		t.Fatalf("HandleResult(ship blocked) error = %v", err)
	}
	if len(client.ships) != 0 {
		t.Fatalf("blocked ship gate shipped PRs, want none: %#v", client.ships)
	}
	if len(client.summaries) != 1 {
		t.Fatalf("review summaries = %d, want 1: %#v", len(client.summaries), client.summaries)
	}
	reviewed, err := state.GetReviewedRevision(ctx, "42")
	if err != nil {
		t.Fatalf("GetReviewedRevision() error = %v", err)
	}
	if reviewed != "r7" {
		t.Fatalf("reviewed revision = %q, want r7", reviewed)
	}

	fetcher.state = arcPRWritebackRuntimeState(true, []arcreview.PRCheck{{Name: "ci", Status: "passed"}})
	if _, err := src.HandleResult(ctx, item, shipResult); err != nil {
		t.Fatalf("HandleResult(ship allowed) error = %v", err)
	}
	if !reflect.DeepEqual(client.ships, []string{"42"}) {
		t.Fatalf("shipped PRs = %#v, want [42]", client.ships)
	}
	if len(client.summaries) != 1 {
		t.Fatalf("retry posted duplicate review summaries: %#v", client.summaries)
	}
}

func arcPRWritebackRuntimeState(commentsAnswered bool, checks []arcreview.PRCheck) arcreview.PRRuntimeState {
	return arcreview.PRRuntimeState{
		PRID:     "42",
		Revision: "r7",
		Details:  arcreview.PRDetails{ID: "42", Status: "open", Revision: "r7"},
		Comments: []arcreview.PRComment{
			{ID: "comment-1", Body: "Can this path return nil?", Answered: commentsAnswered},
			{ID: "comment-2", Body: "Please add coverage.", Answered: commentsAnswered},
		},
		Checks: checks,
	}
}

func mustMarshalArcPRWriteback(t *testing.T, value any) []byte {
	t.Helper()

	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal %T: %v", value, err)
	}
	return raw
}

type fakeArcPRWritebackStateFetcher struct {
	state arcreview.PRRuntimeState
	calls []arcPRWritebackFetchCall
}

type arcPRWritebackFetchCall struct {
	workspace string
	prID      string
}

func (f *fakeArcPRWritebackStateFetcher) FetchPRRuntimeState(_ context.Context, workspace string, prID string) (arcreview.PRRuntimeState, error) {
	f.calls = append(f.calls, arcPRWritebackFetchCall{workspace: workspace, prID: prID})
	return f.state, nil
}

type fakeArcPRWritebackClient struct {
	replies   []arcPRWritebackReply
	summaries []arcPRWritebackSummary
	ships     []string
}

type arcPRWritebackReply struct {
	prID      string
	commentID string
	body      string
}

type arcPRWritebackSummary struct {
	prID     string
	revision string
	body     string
}

func (c *fakeArcPRWritebackClient) PostCommentReply(_ context.Context, prID string, commentID string, body string) error {
	c.replies = append(c.replies, arcPRWritebackReply{prID: prID, commentID: commentID, body: body})
	return nil
}

func (c *fakeArcPRWritebackClient) PostReviewInlineComment(context.Context, string, string, arcreview.ReviewInlineComment) error {
	return nil
}

func (c *fakeArcPRWritebackClient) PostReviewSummary(_ context.Context, prID string, revision string, body string) error {
	c.summaries = append(c.summaries, arcPRWritebackSummary{prID: prID, revision: revision, body: body})
	return nil
}

func (c *fakeArcPRWritebackClient) Ship(_ context.Context, prID string) error {
	c.ships = append(c.ships, prID)
	return nil
}
