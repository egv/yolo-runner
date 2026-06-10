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
	var gotCtx context.Context
	var gotWorkspace string
	var gotName string
	var gotArgs []string

	arcExec = func(ctx context.Context, workspace string, name string, args ...string) ([]byte, []byte, error) {
		gotCtx = ctx
		gotWorkspace = workspace
		gotName = name
		gotArgs = append([]string{}, args...)
		return nil, nil, nil
	}

	client := ReplyArcanumClient{Workspace: "/arcadia/workspace"}
	err := client.PostCommentReply(ctx, "42", "comment-7", "Fixed in the latest revision.")
	if err != nil {
		t.Fatalf("PostCommentReply() error = %v", err)
	}
	if gotCtx != ctx {
		t.Fatal("PostCommentReply() did not pass through context")
	}
	if gotWorkspace != "/arcadia/workspace" {
		t.Fatalf("PostCommentReply() workspace = %q", gotWorkspace)
	}
	if gotName != "arc" {
		t.Fatalf("PostCommentReply() command = %q", gotName)
	}
	wantArgs := []string{
		"reply",
		"--pr-id", "42",
		"--comment-id", "comment-7",
		"--message", "Fixed in the latest revision.",
	}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("PostCommentReply() args = %#v, want %#v", gotArgs, wantArgs)
	}
}

func TestReplyArcanumClientPostCommentReplySurfacesArcErrors(t *testing.T) {
	oldExec := arcExec
	t.Cleanup(func() {
		arcExec = oldExec
	})

	arcExec = func(context.Context, string, string, ...string) ([]byte, []byte, error) {
		return nil, []byte("comment is closed"), errors.New("exit status 1")
	}

	client := ReplyArcanumClient{Workspace: "/arcadia/workspace"}
	err := client.PostCommentReply(context.Background(), "42", "comment-7", "Fixed in the latest revision.")
	if err == nil {
		t.Fatal("PostCommentReply() error = nil")
	}

	message := err.Error()
	for _, want := range []string{
		"arc reply --pr-id 42 --comment-id comment-7 --message Fixed in the latest revision.",
		"/arcadia/workspace",
		"comment is closed",
		"exit status 1",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("PostCommentReply() error = %q, want substring %q", message, want)
		}
	}
}
