package arcreview

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	arcreviewstate "github.com/egv/yolo-runner/v2/internal/arcreview/state"
)

func TestReplyApplierPostsRepliesStoresAnsweredIDsAndSkipsOnRetry(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "state.db")
	store, err := arcreviewstate.Open(dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	client := &fakeReplyArcanumClient{}
	applier := ReplyApplier{
		Client: client,
		Store:  store,
	}
	runtimeState := PRRuntimeState{
		PRID: "42",
		Details: PRDetails{
			ID: "42",
		},
		Comments: []PRComment{
			{ID: "comment-1", Body: "Can this race?"},
			{ID: "comment-2", Body: "Please add coverage."},
		},
	}
	payload := []byte(`{
		"replies": [
			{"comment_id": "comment-1", "body": "The lock is held for the full critical section."},
			{"comment_id": "comment-2", "body": "Added a focused regression test."}
		]
	}`)

	result, err := applier.Apply(ctx, runtimeState, payload)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	wantReplies := []ReviewReply{
		{CommentID: "comment-1", Body: "The lock is held for the full critical section."},
		{CommentID: "comment-2", Body: "Added a focused regression test."},
	}
	if !reflect.DeepEqual(result.Replies, wantReplies) {
		t.Fatalf("Apply() replies mismatch:\ngot:  %#v\nwant: %#v", result.Replies, wantReplies)
	}
	if !reflect.DeepEqual(client.replies, []postedReply{
		{prID: "42", commentID: "comment-1", body: "The lock is held for the full critical section."},
		{prID: "42", commentID: "comment-2", body: "Added a focused regression test."},
	}) {
		t.Fatalf("posted replies mismatch:\ngot: %#v", client.replies)
	}

	answered, err := store.ListAnsweredCommentIDs(ctx, "42")
	if err != nil {
		t.Fatalf("ListAnsweredCommentIDs() error = %v", err)
	}
	if !reflect.DeepEqual(answered, []string{"comment-1", "comment-2"}) {
		t.Fatalf("answered IDs mismatch:\ngot:  %#v\nwant: %#v", answered, []string{"comment-1", "comment-2"})
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, err := arcreviewstate.Open(dbPath)
	if err != nil {
		t.Fatalf("reopen Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	retryClient := &fakeReplyArcanumClient{}
	retryApplier := ReplyApplier{
		Client: retryClient,
		Store:  reopened,
	}
	if _, err := retryApplier.Apply(ctx, runtimeState, payload); err != nil {
		t.Fatalf("retry Apply() error = %v", err)
	}
	if len(retryClient.replies) != 0 {
		t.Fatalf("retry posted replies, want none: %#v", retryClient.replies)
	}
}

type fakeReplyArcanumClient struct {
	replies []postedReply
}

type postedReply struct {
	prID      string
	commentID string
	body      string
}

func (c *fakeReplyArcanumClient) PostCommentReply(_ context.Context, prID string, commentID string, body string) error {
	c.replies = append(c.replies, postedReply{prID: prID, commentID: commentID, body: body})
	return nil
}
