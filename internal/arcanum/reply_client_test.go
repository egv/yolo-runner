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

func TestReplyArcanumClientPostCommentReplyRunsArcReply(t *testing.T) {
	oldExec := arcExec
	t.Cleanup(func() {
		arcExec = oldExec
	})

	ctx := context.WithValue(context.Background(), contextKey("reply"), "value")
	var gotCall replyClientExecCall
	var callCount int

	arcExec = func(ctx context.Context, workspace string, name string, args ...string) ([]byte, []byte, error) {
		callCount++
		gotCall = replyClientExecCall{
			ctx:       ctx,
			workspace: workspace,
			name:      name,
			args:      append([]string{}, args...),
		}
		return []byte("ok\n"), nil, nil
	}

	client := ReplyArcanumClient{Workspace: "/arcadia/workspace"}
	err := client.PostCommentReply(ctx, "42", "comment-7", "Fixed in the latest revision.")
	if err != nil {
		t.Fatalf("PostCommentReply() error = %v", err)
	}
	if callCount != 1 {
		t.Fatalf("PostCommentReply() call count = %d, want 1", callCount)
	}

	wantCall := replyClientExecCall{
		ctx:       ctx,
		workspace: "/arcadia/workspace",
		name:      "arc",
		args: []string{
			"reply",
			"comment-7",
			"Fixed in the latest revision.",
		},
	}
	if !reflect.DeepEqual(gotCall, wantCall) {
		t.Fatalf("PostCommentReply() call = %#v, want %#v", gotCall, wantCall)
	}
}

func TestReplyArcanumClientPostCommentReplySurfacesArcReplyErrors(t *testing.T) {
	oldExec := arcExec
	t.Cleanup(func() {
		arcExec = oldExec
	})

	arcExec = func(context.Context, string, string, ...string) ([]byte, []byte, error) {
		return nil, []byte("comment is closed\n"), errors.New("exit status 1")
	}

	client := ReplyArcanumClient{Workspace: "/arcadia/workspace"}
	err := client.PostCommentReply(context.Background(), "42", "comment-7", "Fixed in the latest revision.")
	if err == nil {
		t.Fatal("PostCommentReply() error = nil")
	}

	message := err.Error()
	for _, want := range []string{
		"arc reply comment-7 Fixed in the latest revision.",
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
