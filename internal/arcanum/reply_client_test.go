package arcanum

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/egv/yolo-runner/v2/internal/arcreview"
)

var _ arcreview.ReplyArcanumClient = ReplyArcanumClient{}

func TestReplyArcanumClientPostCommentReplyPostsReviewRequestCommentReply(t *testing.T) {
	oldExec := arcExec
	t.Cleanup(func() {
		arcExec = oldExec
	})

	ctx := context.WithValue(context.Background(), contextKey("reply"), "value")
	var gotCalls []replyClientExecCall
	var gotRequest *http.Request
	var gotBody map[string]any

	arcExec = func(ctx context.Context, workspace string, name string, args ...string) ([]byte, []byte, error) {
		gotCalls = append(gotCalls, replyClientExecCall{
			ctx:       ctx,
			workspace: workspace,
			name:      name,
			args:      append([]string{}, args...),
		})
		return []byte(`{"token":"arc-token","type":"Arc"}`), nil, nil
	}

	httpClient := replyClientFakeHTTPClient(func(req *http.Request) (*http.Response, error) {
		gotRequest = req
		if req.Context().Value(contextKey("reply")) != "value" {
			t.Fatalf("HTTP request did not use caller context")
		}
		if err := json.NewDecoder(req.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		return replyClientJSONResponse(http.StatusOK, `{"id": "reply-1"}`), nil
	})

	client := ReplyArcanumClient{
		Workspace:  "/arcadia/workspace",
		Endpoint:   "https://arcanum.example.test/",
		HTTPClient: httpClient,
	}
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
				"token",
				"show",
				"--json",
			},
		},
	}
	if !reflect.DeepEqual(gotCalls, wantCalls) {
		t.Fatalf("PostCommentReply() calls = %#v, want %#v", gotCalls, wantCalls)
	}
	if gotRequest == nil {
		t.Fatal("PostCommentReply() did not send HTTP request")
	}
	if got := gotRequest.Method; got != http.MethodPost {
		t.Fatalf("PostCommentReply() method = %q, want %q", got, http.MethodPost)
	}
	if got := gotRequest.URL.String(); got != "https://arcanum.example.test/api/v1/review-requests-comments/comment-7/replies" {
		t.Fatalf("PostCommentReply() URL = %q", got)
	}
	if got := gotRequest.Header.Get("Authorization"); got != "OAuth arc-token" {
		t.Fatalf("PostCommentReply() Authorization = %q, want OAuth token", got)
	}
	if got := gotRequest.Header.Get("Accept"); got != "application/json" {
		t.Fatalf("PostCommentReply() Accept = %q, want application/json", got)
	}
	if got := gotRequest.Header.Get("Content-Type"); !strings.Contains(got, "application/json") {
		t.Fatalf("PostCommentReply() Content-Type = %q, want JSON", got)
	}
	if got := gotBody["content"]; got != "Fixed in the latest revision." {
		t.Fatalf("PostCommentReply() body content = %#v", got)
	}
}

func TestReplyArcanumClientPostCommentReplySurfacesArcTokenErrors(t *testing.T) {
	oldExec := arcExec
	t.Cleanup(func() {
		arcExec = oldExec
	})

	arcExec = func(context.Context, string, string, ...string) ([]byte, []byte, error) {
		return nil, []byte("arc: authentication failed"), errors.New("exit status 1")
	}

	client := ReplyArcanumClient{Workspace: "/arcadia/workspace"}
	err := client.PostCommentReply(context.Background(), "42", "comment-7", "Fixed in the latest revision.")
	if err == nil {
		t.Fatal("PostCommentReply() error = nil")
	}

	message := err.Error()
	for _, want := range []string{
		"arc token show --json",
		"/arcadia/workspace",
		"authentication failed",
		"exit status 1",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("PostCommentReply() error = %q, want substring %q", message, want)
		}
	}
}

func TestReplyArcanumClientPostCommentReplySurfacesArcanumErrors(t *testing.T) {
	oldExec := arcExec
	t.Cleanup(func() {
		arcExec = oldExec
	})

	arcExec = func(context.Context, string, string, ...string) ([]byte, []byte, error) {
		return []byte(`{"token":"arc-token","type":"Arc"}`), nil, nil
	}
	httpClient := replyClientFakeHTTPClient(func(*http.Request) (*http.Response, error) {
		return replyClientJSONResponse(http.StatusConflict, `{"message":"comment is closed"}`), nil
	})

	client := ReplyArcanumClient{
		Workspace:  "/arcadia/workspace",
		Endpoint:   "https://arcanum.example.test",
		HTTPClient: httpClient,
	}
	err := client.PostCommentReply(context.Background(), "42", "comment-7", "Fixed in the latest revision.")
	if err == nil {
		t.Fatal("PostCommentReply() error = nil")
	}

	message := err.Error()
	for _, want := range []string{
		"post Arcanum comment reply",
		"POST https://arcanum.example.test/api/v1/review-requests-comments/comment-7/replies",
		"http 409",
		"comment is closed",
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

type replyClientFakeHTTPClient func(req *http.Request) (*http.Response, error)

func (c replyClientFakeHTTPClient) Do(req *http.Request) (*http.Response, error) {
	return c(req)
}

func replyClientJSONResponse(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
		},
		Body: io.NopCloser(strings.NewReader(body)),
	}
}
