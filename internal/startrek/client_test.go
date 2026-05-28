package startrek

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
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

func TestClientSearchIssuesMapsReadyQueueCandidates(t *testing.T) {
	var capturedRequest *http.Request
	var capturedBody map[string]any
	httpClient := fakeHTTPClient(func(req *http.Request) (*http.Response, error) {
		capturedRequest = req
		if err := json.NewDecoder(req.Body).Decode(&capturedBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}

		return jsonResponseWithHeaders(http.StatusOK, `[
			{
				"id": "64200b5f7b5b7c0011223344",
				"key": "VAY-42",
				"summary": "Add tracker search",
				"tags": ["ready-for-yolo", "backend"],
				"createdBy": {
					"id": "112233",
					"display": "Ada Lovelace"
				},
				"updatedAt": "2026-05-28T01:02:03.456+0000"
			}
		]`, http.Header{
			"X-Total-Count": []string{"51"},
			"X-Total-Pages": []string{"3"},
		}), nil
	})

	client, err := NewClient(Config{
		Endpoint:   "https://api.tracker.yandex.net/v3",
		Token:      "tracker-token",
		HTTPClient: httpClient,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	page, err := client.SearchIssues(context.Background(), IssueSearchOptions{
		QueueKey:   "VAY",
		ReadyLabel: "ready-for-yolo",
		Page:       2,
		PerPage:    25,
	})
	if err != nil {
		t.Fatalf("search issues: %v", err)
	}

	if capturedRequest == nil {
		t.Fatalf("expected request to be sent")
	}
	if got := capturedRequest.Method; got != http.MethodPost {
		t.Fatalf("expected POST method, got %s", got)
	}
	if got := capturedRequest.URL.String(); got != "https://api.tracker.yandex.net/v3/issues/_search?page=2&perPage=25" {
		t.Fatalf("expected paginated search URL, got %q", got)
	}
	filter, ok := capturedBody["filter"].(map[string]any)
	if !ok {
		t.Fatalf("expected filter object in request body, got %#v", capturedBody["filter"])
	}
	if got := filter["queue"]; got != "VAY" {
		t.Fatalf("expected queue filter VAY, got %#v", got)
	}
	if got := filter["tags"]; got != "ready-for-yolo" {
		t.Fatalf("expected ready label tag filter, got %#v", got)
	}

	if page.Page != 2 || page.PerPage != 25 || page.TotalCount != 51 || page.TotalPages != 3 {
		t.Fatalf("unexpected pagination metadata: %#v", page)
	}
	if len(page.Issues) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(page.Issues))
	}

	issue := page.Issues[0]
	if issue.ID != "VAY-42" {
		t.Fatalf("expected issue ID VAY-42, got %q", issue.ID)
	}
	if issue.Title != "Add tracker search" {
		t.Fatalf("expected mapped title, got %q", issue.Title)
	}
	if got := strings.Join(issue.Labels, ","); got != "ready-for-yolo,backend" {
		t.Fatalf("expected mapped labels, got %q", got)
	}
	if issue.Author.ID != "112233" || issue.Author.Display != "Ada Lovelace" {
		t.Fatalf("expected mapped author, got %#v", issue.Author)
	}
	wantUpdatedAt := time.Date(2026, 5, 28, 1, 2, 3, 456000000, time.UTC)
	if !issue.UpdatedAt.Equal(wantUpdatedAt) {
		t.Fatalf("expected updated timestamp %s, got %s", wantUpdatedAt, issue.UpdatedAt)
	}
}

type fakeHTTPClient func(*http.Request) (*http.Response, error)

func (f fakeHTTPClient) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

func jsonResponse(statusCode int, body string) *http.Response {
	return jsonResponseWithHeaders(statusCode, body, nil)
}

func jsonResponseWithHeaders(statusCode int, body string, headers http.Header) *http.Response {
	if headers == nil {
		headers = http.Header{}
	}
	headers.Set("Content-Type", "application/json")
	return &http.Response{
		StatusCode: statusCode,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     headers,
	}
}
