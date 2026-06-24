package arcanum

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResolveArcanumClientResolvesCommentViaPOST(t *testing.T) {
	const commentID = "comment-123"

	var gotPayload map[string]json.RawMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Method; got != http.MethodPost {
			t.Fatalf("method = %q, want POST", got)
		}
		if got := r.URL.Path; got != "/api/v1/review-requests-comments/"+commentID+"/resolve" {
			t.Fatalf("path = %q, want /api/v1/review-requests-comments/%s/resolve", got, commentID)
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
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte(`{"data":{"id":"comment-123"}}`)); err != nil {
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

	client := NewResolveArcanumClient(apiClient)
	if err := client.ResolveComment(context.Background(), "13843457", commentID); err != nil {
		t.Fatalf("ResolveComment() error = %v", err)
	}

	// Resolve sends an empty JSON object — the request shape is confirmed later
	// against the live Arcanum API (see resolve_client.go candidate alternatives).
	if len(gotPayload) != 0 {
		t.Fatalf("resolve request body = %#v, want empty object {}", gotPayload)
	}
}

func TestResolveArcanumClientRejectsEmptyCommentID(t *testing.T) {
	apiClient, err := NewAPIClient(APIClientConfig{
		BaseURL: "https://example.test/api",
		TokenSource: func(context.Context) (string, error) {
			return "test-token", nil
		},
	})
	if err != nil {
		t.Fatalf("NewAPIClient() error = %v", err)
	}

	client := NewResolveArcanumClient(apiClient)
	for _, commentID := range []string{"", "   "} {
		if err := client.ResolveComment(context.Background(), "13843457", commentID); err == nil {
			t.Fatalf("ResolveComment(%q) error = nil, want comment ID required error", commentID)
		}
	}
}

func TestResolveArcanumClientSurfacesResolveErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/review-requests-comments/comment-123/resolve" {
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

	client := NewResolveArcanumClient(apiClient)
	err = client.ResolveComment(context.Background(), "13843457", "comment-123")
	if err == nil {
		t.Fatal("ResolveComment() error = nil")
	}
	for _, want := range []string{"resolve comment", "POST", "/v1/review-requests-comments/comment-123/resolve", "502", "upstream unavailable"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("ResolveComment() error = %q, want substring %q", err.Error(), want)
		}
	}
}

func TestAPIClientPatchJSONIssuesPATCH(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Method; got != http.MethodPatch {
			t.Fatalf("method = %s, want PATCH", got)
		}
		if got := r.URL.Path; got != "/api/comments/42" {
			t.Fatalf("path = %q, want /api/comments/42", got)
		}
		if got := r.Header.Get("Authorization"); got != "OAuth test-token" {
			t.Fatalf("Authorization = %q, want OAuth test-token", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("Content-Type = %q, want application/json", got)
		}

		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if request["resolved"] != true {
			t.Fatalf("request = %#v, want resolved=true", request)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte(`{"id":"comment-42"}`)); err != nil {
			t.Fatalf("write response: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	client, err := NewAPIClient(APIClientConfig{
		BaseURL:    server.URL + "/api",
		HTTPClient: server.Client(),
		TokenSource: func(context.Context) (string, error) {
			return "test-token", nil
		},
	})
	if err != nil {
		t.Fatalf("NewAPIClient() error = %v", err)
	}

	var response struct {
		ID string `json:"id"`
	}
	if err := client.PatchJSON(context.Background(), "/comments/42", map[string]any{"resolved": true}, &response); err != nil {
		t.Fatalf("PatchJSON() error = %v", err)
	}
	if response.ID != "comment-42" {
		t.Fatalf("response ID = %q, want comment-42", response.ID)
	}
}
