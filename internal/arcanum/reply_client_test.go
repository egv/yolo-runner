package arcanum

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReplyArcanumClientPostsCommentReply(t *testing.T) {
	const commentID = "comment-123"

	var gotPayload map[string]json.RawMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Method; got != http.MethodPost {
			t.Fatalf("method = %q, want POST", got)
		}
		if got := r.URL.Path; got != "/api/v1/review-requests-comments/"+commentID+"/replies" {
			t.Fatalf("path = %q, want /api/v1/review-requests-comments/%s/replies", got, commentID)
		}
		if got := r.Header.Get("Authorization"); got != "OAuth test-token" {
			t.Fatalf("Authorization = %q, want OAuth test-token", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("Content-Type = %q, want application/json", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotPayload); err != nil {
			t.Fatalf("decode POST body: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"data":{"id":"reply-1"}}`)); err != nil {
			t.Fatalf("write response: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	apiClient, err := NewAPIClient(APIClientConfig{
		BaseURL:    server.URL + "/api",
		HTTPClient: server.Client(),
		TokenSource: func(context.Context) (string, error) {
			return "test-token", nil
		},
	})
	if err != nil {
		t.Fatalf("NewAPIClient() error = %v", err)
	}

	client := NewReplyArcanumClient(apiClient)
	if err := client.PostCommentReply(context.Background(), "13843457", commentID, "Thanks for the catch."); err != nil {
		t.Fatalf("PostCommentReply() error = %v", err)
	}

	assertJSONRawString(t, gotPayload, "content", "Thanks for the catch.")
}

func TestReplyArcanumClientSurfacesPostErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/review-requests-comments/comment-123/replies" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusBadGateway)
		if _, err := w.Write([]byte("upstream unavailable")); err != nil {
			t.Fatalf("write response: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	apiClient, err := NewAPIClient(APIClientConfig{
		BaseURL:    server.URL + "/api",
		HTTPClient: server.Client(),
		TokenSource: func(context.Context) (string, error) {
			return "test-token", nil
		},
	})
	if err != nil {
		t.Fatalf("NewAPIClient() error = %v", err)
	}

	client := NewReplyArcanumClient(apiClient)
	err = client.PostCommentReply(context.Background(), "13843457", "comment-123", "reply")
	if err == nil {
		t.Fatal("PostCommentReply() error = nil")
	}
	for _, want := range []string{"post comment reply", "POST", "/v1/review-requests-comments/comment-123/replies", "502", "upstream unavailable"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("PostCommentReply() error = %q, want substring %q", err.Error(), want)
		}
	}
}
