package arcanum

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/egv/yolo-runner/v2/internal/arcreview"
)

var _ arcreview.ReplyArcanumClient = ReplyArcanumClient{}

func TestReplyArcanumClientPostCommentReplyInvokesArcReply(t *testing.T) {
	oldExec := arcExec
	t.Cleanup(func() {
		arcExec = oldExec
	})

	ctx := context.WithValue(context.Background(), contextKey("reply"), "value")
	var gotCalls []replyClientExecCall

	arcExec = func(ctx context.Context, workspace string, name string, args ...string) ([]byte, []byte, error) {
		gotCalls = append(gotCalls, replyClientExecCall{
			ctx:       ctx,
			workspace: workspace,
			name:      name,
			args:      append([]string{}, args...),
		})
		return nil, nil, nil
	}

	client := ReplyArcanumClient{Workspace: "/arcadia/workspace"}
	err := client.PostCommentReply(ctx, "42", "comment-7", "Fixed in the latest revision.")
	if err != nil {
		t.Fatalf("PostCommentReply() error = %v", err)
	}
	if len(gotCalls) != 1 {
		t.Fatalf("PostCommentReply() call count = %d, want 1: %#v", len(gotCalls), gotCalls)
	}

	wantCalls := []replyClientExecCall{
		{
			ctx:       ctx,
			workspace: "/arcadia/workspace",
			name:      "arc",
			args: []string{
				"reply",
				"--comment-id", "comment-7",
				"--message", "Fixed in the latest revision.",
			},
		},
	}
	if !reflect.DeepEqual(gotCalls, wantCalls) {
		t.Fatalf("PostCommentReply() calls = %#v, want %#v", gotCalls, wantCalls)
	}
}

func TestReplyArcanumClientPostCommentReplySurfacesArcReplyErrors(t *testing.T) {
	oldExec := arcExec
	t.Cleanup(func() {
		arcExec = oldExec
	})

	arcExec = func(context.Context, string, string, ...string) ([]byte, []byte, error) {
		return nil, []byte("arc: comment is closed"), errors.New("exit status 1")
	}

	client := ReplyArcanumClient{Workspace: "/arcadia/workspace"}
	err := client.PostCommentReply(context.Background(), "42", "comment-7", "Fixed in the latest revision.")
	if err == nil {
		t.Fatal("PostCommentReply() error = nil")
	}

	message := err.Error()
	for _, want := range []string{
		"arc reply --comment-id comment-7 --message Fixed in the latest revision.",
		"/arcadia/workspace",
		"comment is closed",
		"exit status 1",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("PostCommentReply() error = %q, want substring %q", message, want)
		}
	}
}

type replyClientExecCall struct {
	ctx       context.Context
	workspace string
	name      string
	args      []string
}
