package arcanum

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/egv/yolo-runner/v2/internal/arcreview"
)

func TestReviewArcanumClientPostsSummaryAndInlineComment(t *testing.T) {
	const (
		prID             = "13843457"
		revision         = "rev-42"
		reviewFields     = "active_diff_set(id,gsid,status,patch_vcs_ids(source_commit_id,arc_branch_heads(from_id,to_id,merge_id))),diff_sets(id,gsid,status,patch_vcs_ids(source_commit_id,arc_branch_heads(from_id,to_id,merge_id)))"
		changelistFields = "path,entry_id"
	)

	var posts []map[string]json.RawMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "OAuth test-token" {
			t.Fatalf("Authorization = %q, want OAuth test-token", got)
		}

		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/review-requests/"+prID+"/comments":
			var payload map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode POST body: %v", err)
			}
			posts = append(posts, payload)
			w.Header().Set("Content-Type", "application/json")
			if _, err := w.Write([]byte(`{"data":{"id":"comment-1"}}`)); err != nil {
				t.Fatalf("write POST response: %v", err)
			}

		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/review-requests/"+prID:
			if got := r.URL.Query().Get("fields"); got != reviewFields {
				t.Fatalf("review request fields = %q, want %q", got, reviewFields)
			}
			w.Header().Set("Content-Type", "application/json")
			if _, err := w.Write([]byte(`{
  "data": {
    "active_diff_set": {
      "id": 29448811,
      "gsid": "diff-gsid-42",
      "status": "published",
      "patch_vcs_ids": {
        "source_commit_id": "rev-42",
        "arc_branch_heads": {"from_id": "rev-42", "to_id": "trunk-rev"}
      }
    },
    "diff_sets": [
      {
        "id": 29448810,
        "gsid": "diff-gsid-old",
        "status": "discarded",
        "patch_vcs_ids": {"source_commit_id": "rev-41"}
      },
      {
        "id": 29448811,
        "gsid": "diff-gsid-42",
        "status": "published",
        "patch_vcs_ids": {
          "source_commit_id": "rev-42",
          "arc_branch_heads": {"from_id": "rev-42", "to_id": "trunk-rev"}
        }
      }
    ]
  }
}`)); err != nil {
				t.Fatalf("write review request response: %v", err)
			}

		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/review-requests/"+prID+"/diff-sets/29448811/changelist":
			if got := r.URL.Query().Get("fields"); got != changelistFields {
				t.Fatalf("changelist fields = %q, want %q", got, changelistFields)
			}
			w.Header().Set("Content-Type", "application/json")
			if _, err := w.Write([]byte(`{
  "data": [
    {"path": "internal/arcanum/review_client.go", "entry_id": "entry-123"},
    {"path": "internal/arcanum/other.go", "entry_id": "entry-999"}
  ]
}`)); err != nil {
				t.Fatalf("write changelist response: %v", err)
			}

		default:
			t.Fatalf("unexpected request: %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
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

	client := NewReviewArcanumClient(apiClient)
	if err := client.PostReviewSummary(context.Background(), prID, revision, "## Review\nLGTM"); err != nil {
		t.Fatalf("PostReviewSummary() error = %v", err)
	}
	if err := client.PostReviewInlineComment(context.Background(), prID, revision, arcreview.ReviewInlineComment{
		Path: "internal/arcanum/review_client.go",
		Line: 44,
		Body: "Please handle API errors.",
	}); err != nil {
		t.Fatalf("PostReviewInlineComment() error = %v", err)
	}

	if len(posts) != 2 {
		t.Fatalf("POST count = %d, want 2", len(posts))
	}
	assertJSONRawString(t, posts[0], "content", "## Review\nLGTM")
	assertJSONRawMissing(t, posts[0], "entry_id")
	assertJSONRawMissing(t, posts[0], "diff_side")
	assertJSONRawMissing(t, posts[0], "diff_line")
	assertJSONRawMissing(t, posts[0], "diff_set_xid")

	assertJSONRawString(t, posts[1], "content", "Please handle API errors.")
	assertJSONRawString(t, posts[1], "entry_id", "entry-123")
	assertJSONRawString(t, posts[1], "diff_side", "new")
	assertJSONRawInt(t, posts[1], "diff_line", 44)
	assertJSONRawString(t, posts[1], "diff_set_xid", "29448811")
}

func TestReviewArcanumClientSurfacesPostErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/review-requests/42/comments" {
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

	client := NewReviewArcanumClient(apiClient)
	err = client.PostReviewSummary(context.Background(), "42", "rev-1", "summary")
	if err == nil {
		t.Fatal("PostReviewSummary() error = nil")
	}
	for _, want := range []string{"post review summary", "POST", "/v1/review-requests/42/comments", "502", "upstream unavailable"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("PostReviewSummary() error = %q, want substring %q", err.Error(), want)
		}
	}
}

func TestReviewArcanumClientSurfacesInlineResolutionErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/review-requests/42" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusNotFound)
		if _, err := w.Write([]byte("review not found")); err != nil {
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

	client := NewReviewArcanumClient(apiClient)
	err = client.PostReviewInlineComment(context.Background(), "42", "rev-1", arcreview.ReviewInlineComment{
		Path: "file.go",
		Line: 7,
		Body: "comment",
	})
	if err == nil {
		t.Fatal("PostReviewInlineComment() error = nil")
	}
	for _, want := range []string{"resolve inline comment anchor", "GET", "/v1/review-requests/42", "404", "review not found"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("PostReviewInlineComment() error = %q, want substring %q", err.Error(), want)
		}
	}
}

func assertJSONRawString(t *testing.T, payload map[string]json.RawMessage, key string, want string) {
	t.Helper()

	raw, ok := payload[key]
	if !ok {
		t.Fatalf("payload missing %q: %#v", key, payload)
	}
	var got string
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("payload[%q] is not a string: %v", key, err)
	}
	if got != want {
		t.Fatalf("payload[%q] = %q, want %q", key, got, want)
	}
}

func assertJSONRawInt(t *testing.T, payload map[string]json.RawMessage, key string, want int) {
	t.Helper()

	raw, ok := payload[key]
	if !ok {
		t.Fatalf("payload missing %q: %#v", key, payload)
	}
	var got int
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("payload[%q] is not an int: %v", key, err)
	}
	if got != want {
		t.Fatalf("payload[%q] = %d, want %d", key, got, want)
	}
}

func assertJSONRawMissing(t *testing.T, payload map[string]json.RawMessage, key string) {
	t.Helper()

	if _, ok := payload[key]; ok {
		t.Fatalf("payload[%q] present, want omitted: %#v", key, payload)
	}
}
