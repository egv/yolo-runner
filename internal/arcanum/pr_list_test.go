package arcanum

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func TestPRListArcanumClientReviewerQueryUsesOAuthAndFilters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Method; got != http.MethodGet {
			t.Fatalf("method = %s, want GET", got)
		}
		if got := r.URL.Path; got != "/api/v1/review-requests" {
			t.Fatalf("path = %q, want /api/v1/review-requests", got)
		}
		query := r.URL.Query()
		if got := query.Get("query"); got != "subscriber(alice);open()" {
			t.Fatalf("query = %q, want subscriber(alice);open()", got)
		}
		if fields := query.Get("fields"); !strings.Contains(fields, "review_requests(") {
			t.Fatalf("fields = %q, want a review_requests(...) projection", fields)
		}
		if got := query.Get("status"); got != "" {
			t.Fatalf("status = %q, want empty (open-ness is the open() predicate)", got)
		}
		if got := query.Get("reviewer"); got != "" {
			t.Fatalf("reviewer = %q, want empty (filter is in the query DSL)", got)
		}
		if got := r.Header.Get("Authorization"); got != "OAuth api-token" {
			t.Fatalf("Authorization = %q, want OAuth api-token", got)
		}

		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"data":[{"id":"100","status":"open","summary":"reviewer PR","author":"alice","reviewers":["alice"],"from_id":"rev-100"}]}`)); err != nil {
			t.Fatalf("write response: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	client, err := NewAPIClient(APIClientConfig{
		BaseURL:    server.URL + "/api",
		HTTPClient: server.Client(),
		TokenSource: func(context.Context) (string, error) {
			return "api-token", nil
		},
	})
	if err != nil {
		t.Fatalf("NewAPIClient() error = %v", err)
	}

	listClient := NewPRListArcanumClient(client)
	got, err := listClient.ListReviewerReviewPRs(context.Background(), "alice")
	if err != nil {
		t.Fatalf("ListReviewerReviewPRs() error = %v", err)
	}

	if len(got) != 1 || got[0].ID != "100" || got[0].Summary != "reviewer PR" {
		t.Fatalf("ListReviewerReviewPRs() = %#v", got)
	}
}

func TestPRListArcanumClientAuthorQueryUsesOAuthAndFilters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Method; got != http.MethodGet {
			t.Fatalf("method = %s, want GET", got)
		}
		if got := r.URL.Path; got != "/api/v1/review-requests" {
			t.Fatalf("path = %q, want /api/v1/review-requests", got)
		}
		query := r.URL.Query()
		if got := query.Get("query"); got != "author(alice);open()" {
			t.Fatalf("query = %q, want author(alice);open()", got)
		}
		if fields := query.Get("fields"); !strings.Contains(fields, "review_requests(") {
			t.Fatalf("fields = %q, want a review_requests(...) projection", fields)
		}
		if got := query.Get("author"); got != "" {
			t.Fatalf("author = %q, want empty (filter is in the query DSL)", got)
		}
		if got := r.Header.Get("Authorization"); got != "OAuth api-token" {
			t.Fatalf("Authorization = %q, want OAuth api-token", got)
		}

		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"data":[{"id":"200","status":"open","summary":"authored PR","author":"alice","reviewers":["bob"],"from_id":"rev-200"}]}`)); err != nil {
			t.Fatalf("write response: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	client, err := NewAPIClient(APIClientConfig{
		BaseURL:    server.URL + "/api",
		HTTPClient: server.Client(),
		TokenSource: func(context.Context) (string, error) {
			return "api-token", nil
		},
	})
	if err != nil {
		t.Fatalf("NewAPIClient() error = %v", err)
	}

	listClient := NewPRListArcanumClient(client)
	got, err := listClient.ListAuthorReviewPRs(context.Background(), "alice")
	if err != nil {
		t.Fatalf("ListAuthorReviewPRs() error = %v", err)
	}

	if len(got) != 1 || got[0].ID != "200" || got[0].Summary != "authored PR" {
		t.Fatalf("ListAuthorReviewPRs() = %#v", got)
	}
}

func TestListReviewPRsWithClientDedupeReviewerThenAuthor(t *testing.T) {
	requests := make([]string, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil {
			t.Fatalf("parse query: %v", err)
		}
		method := r.Method
		if method != http.MethodGet {
			t.Fatalf("method = %s, want GET", method)
		}
		if got := r.URL.Path; got != "/api/v1/review-requests" {
			t.Fatalf("path = %q, want /api/v1/review-requests", got)
		}
		if got := r.Header.Get("Authorization"); got != "OAuth test-token" {
			t.Fatalf("Authorization = %q, want OAuth test-token", got)
		}

		switch q := query.Get("query"); {
		case q == "subscriber(alice);open()":
			requests = append(requests, "reviewer")
			w.Header().Set("Content-Type", "application/json")
			if _, err := w.Write([]byte(`{
  "data":[
    {"id":"100","status":"open","summary":"reviewer PR","author":"alice","reviewers":["alice"],"from_id":"rev-100"},
    {"id":"150","status":"open","summary":"another reviewer PR","author":"dave","reviewers":["alice"],"from_id":"rev-150"}
  ]
}`)); err != nil {
				t.Fatalf("write reviewer response: %v", err)
			}

		case q == "author(alice);open()":
			requests = append(requests, "author")
			w.Header().Set("Content-Type", "application/json")
			if _, err := w.Write([]byte(`{"data":[
  {"id":"100","status":"open","summary":"also mine","author":"alice","reviewers":["alice"],"from_id":"rev-100"},
  {"id":"200","status":"open","summary":"my outgoing PR","author":"alice","reviewers":["alice"],"from_id":"rev-200"}
]}`)); err != nil {
				t.Fatalf("write author response: %v", err)
			}

		default:
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
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

	got, err := ListReviewPRsWithClient(context.Background(), client, "alice")
	if err != nil {
		t.Fatalf("ListReviewPRsWithClient() error = %v", err)
	}

	wantRequestOrder := []string{"reviewer", "author"}
	if !reflect.DeepEqual(requests, wantRequestOrder) {
		t.Fatalf("request order = %#v, want %#v", requests, wantRequestOrder)
	}

	wantIDs := []string{"100", "150", "200"}
	gotIDs := make([]string, 0, len(got))
	for _, pr := range got {
		gotIDs = append(gotIDs, pr.ID)
	}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("ids = %#v, want %#v", gotIDs, wantIDs)
	}
	if got[0].Summary != "reviewer PR" {
		t.Fatalf("dedupe kept wrong summary for PR 100: %q", got[0].Summary)
	}
}

func TestListReviewPRsWithClientBlankUserMakesNoAPICalls(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	t.Cleanup(server.Close)

	client, err := NewAPIClient(APIClientConfig{
		BaseURL:    server.URL + "/api",
		HTTPClient: server.Client(),
		TokenSource: func(context.Context) (string, error) {
			return "token", nil
		},
	})
	if err != nil {
		t.Fatalf("NewAPIClient() error = %v", err)
	}

	got, err := ListReviewPRsWithClient(context.Background(), client, " ")
	if err != nil {
		t.Fatalf("ListReviewPRsWithClient() error = %v", err)
	}
	if called {
		t.Fatal("expected no API calls for blank user")
	}
	if len(got) != 0 {
		t.Fatalf("ListReviewPRsWithClient() = %#v, want empty", got)
	}
}
