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

func TestReplyArcanumClientPostCommentReplyInvokesArcanumReplyAPI(t *testing.T) {
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
		if name == "arc" {
			return []byte("token-123\n"), nil, nil
		}
		return nil, nil, nil
	}

	client := ReplyArcanumClient{Workspace: "/arcadia/workspace"}
	err := client.PostCommentReply(ctx, "42", "comment-7", "Fixed in the latest revision.")
	if err != nil {
		t.Fatalf("PostCommentReply() error = %v", err)
	}
	if len(gotCalls) != 2 {
		t.Fatalf("PostCommentReply() call count = %d, want 2: %#v", len(gotCalls), gotCalls)
	}

	wantCalls := []replyClientExecCall{
		{
			ctx:       ctx,
			workspace: "/arcadia/workspace",
			name:      "arc",
			args:      []string{"token", "show"},
		},
		{
			ctx:       ctx,
			workspace: "/arcadia/workspace",
			name:      "curl",
			args: []string{
				"--fail-with-body",
				"--silent",
				"--show-error",
				"--request", "POST",
				"--header", "Authorization: OAuth token-123",
				"--header", "Content-Type: application/json",
				"--data-binary", `{"content":"Fixed in the latest revision.","draft":false}`,
				"https://arcanum.yandex.net/api/v1/public/review-requests-comments/comment-7/replies",
			},
		},
	}
	if !reflect.DeepEqual(gotCalls, wantCalls) {
		t.Fatalf("PostCommentReply() calls = %#v, want %#v", gotCalls, wantCalls)
	}
}

func TestReplyArcanumClientPostCommentReplySurfacesArcanumAPIErrors(t *testing.T) {
	oldExec := arcExec
	t.Cleanup(func() {
		arcExec = oldExec
	})

	arcExec = func(_ context.Context, _ string, name string, _ ...string) ([]byte, []byte, error) {
		if name == "arc" {
			return []byte("token-123\n"), nil, nil
		}
		return []byte(`{"message":"comment is closed"}`), []byte("curl: (22) HTTP response code said error"), errors.New("exit status 22")
	}

	client := ReplyArcanumClient{Workspace: "/arcadia/workspace"}
	err := client.PostCommentReply(context.Background(), "42", "comment-7", "Fixed in the latest revision.")
	if err == nil {
		t.Fatal("PostCommentReply() error = nil")
	}

	message := err.Error()
	for _, want := range []string{
		"curl --fail-with-body --silent --show-error --request POST",
		"Authorization: OAuth <redacted>",
		"https://arcanum.yandex.net/api/v1/public/review-requests-comments/comment-7/replies",
		"/arcadia/workspace",
		"comment is closed",
		"exit status 22",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("PostCommentReply() error = %q, want substring %q", message, want)
		}
	}
	if strings.Contains(message, "token-123") {
		t.Fatalf("PostCommentReply() error leaked token: %q", message)
	}
}

type replyClientExecCall struct {
	ctx       context.Context
	workspace string
	name      string
	args      []string
}
