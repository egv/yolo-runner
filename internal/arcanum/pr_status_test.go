package arcanum

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchReviewRequestStateWithClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/api/v1/review-requests/14330209" {
			t.Fatalf("path = %q, want /api/v1/review-requests/14330209", got)
		}
		if fields := r.URL.Query().Get("fields"); fields != "state" {
			t.Fatalf("fields = %q, want state", fields)
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"data":{"id":14330209,"state":"merged"}}`)); err != nil {
			t.Fatalf("write response: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	client, err := NewAPIClient(APIClientConfig{
		BaseURL:     server.URL + "/api",
		HTTPClient:  server.Client(),
		TokenSource: func(context.Context) (string, error) { return "test-token", nil },
	})
	if err != nil {
		t.Fatalf("NewAPIClient() error = %v", err)
	}

	state, err := FetchReviewRequestStateWithClient(context.Background(), client, "14330209")
	if err != nil {
		t.Fatalf("FetchReviewRequestStateWithClient() error = %v", err)
	}
	if state != "merged" {
		t.Fatalf("state = %q, want merged", state)
	}
}

func TestFetchReviewRequestStateNotFoundIsTyped(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"review request not found"}`, http.StatusNotFound)
	}))
	t.Cleanup(server.Close)

	client, err := NewAPIClient(APIClientConfig{
		BaseURL:     server.URL + "/api",
		HTTPClient:  server.Client(),
		TokenSource: func(context.Context) (string, error) { return "test-token", nil },
	})
	if err != nil {
		t.Fatalf("NewAPIClient() error = %v", err)
	}

	_, err = FetchReviewRequestStateWithClient(context.Background(), client, "999")
	if err == nil {
		t.Fatal("FetchReviewRequestStateWithClient() error = nil, want a 404 error")
	}
	if !IsAPINotFound(err) {
		t.Fatalf("IsAPINotFound(%v) = false, want true", err)
	}
}

func TestIsAPINotFoundIgnoresOtherStatuses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	client, err := NewAPIClient(APIClientConfig{
		BaseURL:     server.URL + "/api",
		HTTPClient:  server.Client(),
		TokenSource: func(context.Context) (string, error) { return "test-token", nil },
	})
	if err != nil {
		t.Fatalf("NewAPIClient() error = %v", err)
	}

	_, err = FetchReviewRequestStateWithClient(context.Background(), client, "999")
	if err == nil {
		t.Fatal("FetchReviewRequestStateWithClient() error = nil, want a 500 error")
	}
	if IsAPINotFound(err) {
		t.Fatalf("IsAPINotFound(%v) = true for a 500, want false", err)
	}
}

func TestReviewRequestStateClosed(t *testing.T) {
	cases := []struct {
		state string
		want  bool
	}{
		{"open", false},
		{"", false},
		{"draft", false},
		{"merged", true},
		{"MERGED", true},
		{" discarded ", true},
		{"closed", true},
		{"abandoned", true},
	}
	for _, tc := range cases {
		if got := ReviewRequestStateClosed(tc.state); got != tc.want {
			t.Errorf("ReviewRequestStateClosed(%q) = %v, want %v", tc.state, got, tc.want)
		}
	}
}
