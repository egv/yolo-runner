package arcanum

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAPIClientGetJSONSendsOAuthHeaderAndDecodesResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Method; got != http.MethodGet {
			t.Fatalf("method = %s, want GET", got)
		}
		if got := r.URL.Path; got != "/api/reviews/42" {
			t.Fatalf("path = %q, want /api/reviews/42", got)
		}
		if got := r.Header.Get("Authorization"); got != "OAuth arc-token" {
			t.Fatalf("Authorization = %q, want OAuth arc-token", got)
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Fatalf("Accept = %q, want application/json", got)
		}

		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"id":"42","state":"open"}`)); err != nil {
			t.Fatalf("write response: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	client, err := NewAPIClient(APIClientConfig{
		BaseURL:    server.URL + "/api",
		HTTPClient: server.Client(),
		TokenSource: func(context.Context) (string, error) {
			return " arc-token ", nil
		},
	})
	if err != nil {
		t.Fatalf("NewAPIClient() error = %v", err)
	}

	var response struct {
		ID    string `json:"id"`
		State string `json:"state"`
	}
	if err := client.GetJSON(context.Background(), "/reviews/42", &response); err != nil {
		t.Fatalf("GetJSON() error = %v", err)
	}

	if response.ID != "42" || response.State != "open" {
		t.Fatalf("response = %#v, want id/state from JSON", response)
	}
}

func TestAPIClientPostJSONSendsJSONBodyAndDecodesResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Method; got != http.MethodPost {
			t.Fatalf("method = %s, want POST", got)
		}
		if got := r.URL.Path; got != "/api/comments" {
			t.Fatalf("path = %q, want /api/comments", got)
		}
		if got := r.Header.Get("Authorization"); got != "OAuth arc-token" {
			t.Fatalf("Authorization = %q, want OAuth arc-token", got)
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Fatalf("Accept = %q, want application/json", got)
		}
		if got := r.Header.Get("Content-Type"); !strings.Contains(got, "application/json") {
			t.Fatalf("Content-Type = %q, want application/json", got)
		}

		var request struct {
			Text string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if request.Text != "looks good" {
			t.Fatalf("request text = %q, want looks good", request.Text)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		if _, err := w.Write([]byte(`{"id":"comment-1"}`)); err != nil {
			t.Fatalf("write response: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	client, err := NewAPIClient(APIClientConfig{
		BaseURL:    server.URL + "/api",
		HTTPClient: server.Client(),
		TokenSource: func(context.Context) (string, error) {
			return "arc-token", nil
		},
	})
	if err != nil {
		t.Fatalf("NewAPIClient() error = %v", err)
	}

	var response struct {
		ID string `json:"id"`
	}
	if err := client.PostJSON(context.Background(), "comments", map[string]string{
		"text": "looks good",
	}, &response); err != nil {
		t.Fatalf("PostJSON() error = %v", err)
	}

	if response.ID != "comment-1" {
		t.Fatalf("response ID = %q, want comment-1", response.ID)
	}
}

func TestAPIClientNon2xxErrorIncludesMethodPathStatusAndBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "OAuth arc-token" {
			t.Fatalf("Authorization = %q, want OAuth arc-token", got)
		}
		w.WriteHeader(http.StatusTeapot)
		if _, err := w.Write([]byte("\n  review not found: 42  \n")); err != nil {
			t.Fatalf("write response: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	client, err := NewAPIClient(APIClientConfig{
		BaseURL:    server.URL + "/api",
		HTTPClient: server.Client(),
		TokenSource: func(context.Context) (string, error) {
			return "arc-token", nil
		},
	})
	if err != nil {
		t.Fatalf("NewAPIClient() error = %v", err)
	}

	err = client.GetJSON(context.Background(), "/reviews/42", nil)
	if err == nil {
		t.Fatal("GetJSON() error = nil, want non-2xx error")
	}

	for _, want := range []string{"GET", "/reviews/42", "418", "review not found: 42"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want substring %q", err.Error(), want)
		}
	}
	if strings.Contains(err.Error(), "\n  review") {
		t.Fatalf("error body excerpt was not trimmed: %q", err.Error())
	}
}

func TestDefaultAPITokenSourceUsesARCTokenEnv(t *testing.T) {
	t.Setenv("ARC_TOKEN", " env-token ")

	token, err := DefaultAPITokenSource(context.Background())
	if err != nil {
		t.Fatalf("DefaultAPITokenSource() error = %v", err)
	}
	if token != "env-token" {
		t.Fatalf("token = %q, want env-token", token)
	}
}

func TestDefaultAPITokenSourceFallsBackToArcTokenFileFirstLine(t *testing.T) {
	t.Setenv("ARC_TOKEN", "")
	home := t.TempDir()
	t.Setenv("HOME", home)

	tokenPath := filepath.Join(home, ".arc", "token")
	if err := os.MkdirAll(filepath.Dir(tokenPath), 0o755); err != nil {
		t.Fatalf("create token directory: %v", err)
	}
	if err := os.WriteFile(tokenPath, []byte(" file-token \nsecond-line\n"), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}

	token, err := DefaultAPITokenSource(context.Background())
	if err != nil {
		t.Fatalf("DefaultAPITokenSource() error = %v", err)
	}
	if token != "file-token" {
		t.Fatalf("token = %q, want file-token", token)
	}
}
