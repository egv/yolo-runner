package startrek

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestClientDoJSONBuildsAuthenticatedRequest(t *testing.T) {
	var capturedRequest *http.Request
	var capturedBody map[string]any
	httpClient := fakeHTTPClient(func(req *http.Request) (*http.Response, error) {
		capturedRequest = req
		if err := json.NewDecoder(req.Body).Decode(&capturedBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}

		return jsonResponse(http.StatusOK, `{"id":"TEST-1"}`), nil
	})

	client, err := NewClient(Config{
		Endpoint:   " https://api.tracker.yandex.net/v3/ ",
		Token:      " tracker-token ",
		HTTPClient: httpClient,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	var response struct {
		ID string `json:"id"`
	}
	err = client.DoJSON(context.Background(), http.MethodPost, "/issues/_search", map[string]any{
		"filter": map[string]any{
			"queue": "TEST",
		},
	}, &response)
	if err != nil {
		t.Fatalf("do json: %v", err)
	}

	if capturedRequest == nil {
		t.Fatalf("expected request to be sent")
	}
	if got := capturedRequest.Method; got != http.MethodPost {
		t.Fatalf("expected POST method, got %s", got)
	}
	if got := capturedRequest.URL.String(); got != "https://api.tracker.yandex.net/v3/issues/_search" {
		t.Fatalf("expected normalized URL, got %q", got)
	}
	if got := capturedRequest.Header.Get("Authorization"); got != "OAuth tracker-token" {
		t.Fatalf("expected OAuth authorization header, got %q", got)
	}
	if got := capturedRequest.Header.Get("Accept"); got != "application/json" {
		t.Fatalf("expected JSON accept header, got %q", got)
	}
	if got := capturedRequest.Header.Get("Content-Type"); !strings.Contains(got, "application/json") {
		t.Fatalf("expected JSON content type, got %q", got)
	}
	filter, ok := capturedBody["filter"].(map[string]any)
	if !ok {
		t.Fatalf("expected filter object in request body, got %#v", capturedBody["filter"])
	}
	if got := filter["queue"]; got != "TEST" {
		t.Fatalf("expected queue filter TEST, got %#v", got)
	}
	if response.ID != "TEST-1" {
		t.Fatalf("expected response id TEST-1, got %q", response.ID)
	}
}

func TestClientDoJSONReturnsJSONErrorMessage(t *testing.T) {
	httpClient := fakeHTTPClient(func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusBadRequest, `{
			"message": "invalid request",
			"errors": [
				{"message": "queue is required"}
			]
		}`), nil
	})

	client, err := NewClient(Config{
		Endpoint:   "https://api.tracker.yandex.net/v3",
		Token:      "tracker-token",
		HTTPClient: httpClient,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	err = client.DoJSON(context.Background(), http.MethodPost, "issues/_search", map[string]any{}, nil)
	if err == nil {
		t.Fatalf("expected JSON API error")
	}
	if !strings.Contains(err.Error(), "http 400") {
		t.Fatalf("expected status in error, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "invalid request") {
		t.Fatalf("expected top-level message in error, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "queue is required") {
		t.Fatalf("expected nested error message in error, got %q", err.Error())
	}
}

type fakeHTTPClient func(*http.Request) (*http.Response, error)

func (f fakeHTTPClient) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

func jsonResponse(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
}
