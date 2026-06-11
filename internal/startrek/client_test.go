package startrek

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
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

func TestClientRetriesTransientSearchFailuresOnlyForIdempotentRequests(t *testing.T) {
	var searchAttempts int32
	var mutationAttempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == http.MethodPost && req.URL.Path == "/v3/issues/_search":
			attempt := atomic.AddInt32(&searchAttempts, 1)
			if attempt < 3 {
				http.Error(w, `{"message":"temporary startrek failure"}`, http.StatusInternalServerError)
				return
			}

			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Total-Count", "0")
			w.Header().Set("X-Total-Pages", "0")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
		case req.Method == http.MethodPatch && req.URL.Path == "/v3/issues/VAY-42":
			atomic.AddInt32(&mutationAttempts, 1)
			http.Error(w, `{"message":"temporary startrek failure"}`, http.StatusInternalServerError)
		default:
			t.Errorf("unexpected request %s %s", req.Method, req.URL.Path)
			http.NotFound(w, req)
		}
	}))
	defer server.Close()

	client, err := NewClient(Config{
		Endpoint: server.URL + "/v3",
		Token:    "tracker-token",
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	if _, err := client.SearchIssues(context.Background(), IssueSearchOptions{
		QueueKey:   "VAY",
		ReadyLabel: "ready-for-yolo",
	}); err != nil {
		t.Fatalf("search issues after transient failures: %v", err)
	}
	if got := atomic.LoadInt32(&searchAttempts); got != 3 {
		t.Fatalf("expected search to be attempted 3 times, got %d", got)
	}

	err = client.AddLabel(context.Background(), "VAY-42", "ready-for-yolo")
	if err == nil {
		t.Fatalf("expected mutating request to fail on first 500")
	}
	if got := atomic.LoadInt32(&mutationAttempts); got != 1 {
		t.Fatalf("expected mutating request to be attempted once, got %d", got)
	}
}

func TestClientRetriesConnectionErrorsForIdempotentRequests(t *testing.T) {
	var attempts int
	httpClient := fakeHTTPClient(func(req *http.Request) (*http.Response, error) {
		attempts++
		if attempts < 3 {
			return nil, errors.New("temporary connection failure")
		}
		return jsonResponse(http.StatusOK, `{"ok":true}`), nil
	})

	client, err := NewClient(Config{
		Endpoint:   "https://api.tracker.yandex.net/v3",
		Token:      "tracker-token",
		HTTPClient: httpClient,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	var response struct {
		OK bool `json:"ok"`
	}
	if err := client.DoJSON(context.Background(), http.MethodGet, "issues/VAY-42", nil, &response); err != nil {
		t.Fatalf("get issue after connection errors: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("expected GET to be attempted 3 times, got %d", attempts)
	}
	if !response.OK {
		t.Fatalf("expected successful response to be decoded")
	}
}

func TestClientRetryHonorsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var attempts int
	httpClient := fakeHTTPClient(func(req *http.Request) (*http.Response, error) {
		attempts++
		cancel()
		return jsonResponse(http.StatusInternalServerError, `{"message":"temporary startrek failure"}`), nil
	})

	client, err := NewClient(Config{
		Endpoint:   "https://api.tracker.yandex.net/v3",
		Token:      "tracker-token",
		HTTPClient: httpClient,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	err = client.DoJSON(ctx, http.MethodGet, "issues/VAY-42", nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
	if attempts != 1 {
		t.Fatalf("expected cancellation to stop retry after first attempt, got %d attempts", attempts)
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

func TestClientGetIssueAndCommentsMapsDiscussionContext(t *testing.T) {
	var capturedRequests []string
	httpClient := fakeHTTPClient(func(req *http.Request) (*http.Response, error) {
		capturedRequests = append(capturedRequests, req.Method+" "+req.URL.String())

		switch req.URL.Path {
		case "/v3/issues/VAY-42":
			return jsonResponse(http.StatusOK, `{
				"id": "64200b5f7b5b7c0011223344",
				"key": "VAY-42",
				"summary": "Add Startrek issue fetch",
				"description": "  Include description in agent context.  ",
				"tags": ["ready-for-yolo", "backend"],
				"createdBy": {
					"id": "112233",
					"display": "Ada Lovelace"
				},
				"updatedAt": "2026-05-28T02:03:04.000+0000"
			}`), nil
		case "/v3/issues/VAY-42/comments":
			return jsonResponse(http.StatusOK, `[
				{
					"id": "comment-new",
					"text": "Newest comment",
					"createdBy": {
						"id": "445566",
						"display": "Grace Hopper"
					},
					"createdAt": "2026-05-28T04:00:00.000+0000",
					"updatedAt": "2026-05-28T04:10:00.000+0000"
				},
				{
					"id": "comment-empty",
					"text": "   ",
					"createdBy": {
						"id": "778899",
						"display": "Ignored Author"
					},
					"createdAt": "2026-05-28T02:00:00.000+0000"
				},
				{
					"id": "comment-old",
					"text": "Oldest comment",
					"createdBy": {
						"id": "112233",
						"display": "Ada Lovelace"
					},
					"createdAt": "2026-05-28T01:00:00.000+0000",
					"updatedAt": "2026-05-28T01:05:00.000+0000"
				},
				{
					"id": "comment-middle",
					"text": "Middle comment",
					"createdBy": {
						"id": "445566",
						"display": "Grace Hopper"
					},
					"createdAt": "2026-05-28T03:00:00.000+0000"
				}
			]`), nil
		default:
			t.Fatalf("unexpected request path %s", req.URL.Path)
			return nil, nil
		}
	})

	client, err := NewClient(Config{
		Endpoint:   "https://api.tracker.yandex.net/v3",
		Token:      "tracker-token",
		HTTPClient: httpClient,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	issue, err := client.GetIssue(context.Background(), " VAY-42 ")
	if err != nil {
		t.Fatalf("get issue: %v", err)
	}
	comments, err := client.GetIssueComments(context.Background(), "VAY-42")
	if err != nil {
		t.Fatalf("get issue comments: %v", err)
	}

	wantRequests := []string{
		"GET https://api.tracker.yandex.net/v3/issues/VAY-42",
		"GET https://api.tracker.yandex.net/v3/issues/VAY-42/comments",
	}
	if strings.Join(capturedRequests, "\n") != strings.Join(wantRequests, "\n") {
		t.Fatalf("unexpected requests:\n%s", strings.Join(capturedRequests, "\n"))
	}

	if issue.ID != "VAY-42" {
		t.Fatalf("expected issue ID VAY-42, got %q", issue.ID)
	}
	if issue.Title != "Add Startrek issue fetch" {
		t.Fatalf("expected mapped title, got %q", issue.Title)
	}
	if issue.Description != "Include description in agent context." {
		t.Fatalf("expected trimmed description, got %q", issue.Description)
	}

	if len(comments) != 3 {
		t.Fatalf("expected 3 non-empty comments, got %d: %#v", len(comments), comments)
	}
	if got := comments[0].Body; got != "Oldest comment" {
		t.Fatalf("expected oldest comment first, got %q", got)
	}
	if got := comments[1].Body; got != "Middle comment" {
		t.Fatalf("expected middle comment second, got %q", got)
	}
	if got := comments[2].Body; got != "Newest comment" {
		t.Fatalf("expected newest comment last, got %q", got)
	}
	if comments[0].Author.ID != "112233" || comments[0].Author.Display != "Ada Lovelace" {
		t.Fatalf("expected mapped oldest comment author, got %#v", comments[0].Author)
	}
	wantCreatedAt := time.Date(2026, 5, 28, 1, 0, 0, 0, time.UTC)
	if !comments[0].CreatedAt.Equal(wantCreatedAt) {
		t.Fatalf("expected oldest comment timestamp %s, got %s", wantCreatedAt, comments[0].CreatedAt)
	}
}

func TestClientCreateIssueCommentPostsMarkedBodyAndReturnsID(t *testing.T) {
	var capturedRequest *http.Request
	var capturedBody struct {
		Text       string   `json:"text"`
		Summonees  []string `json:"summonees"`
		MarkupType string   `json:"markupType"`
	}
	httpClient := fakeHTTPClient(func(req *http.Request) (*http.Response, error) {
		capturedRequest = req
		if err := json.NewDecoder(req.Body).Decode(&capturedBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}

		return jsonResponse(http.StatusCreated, `{
			"id": 626,
			"text": "Runner update ready.",
			"createdBy": {
				"id": "runner",
				"display": "YOLO Runner"
			},
			"createdAt": "2026-05-28T05:00:00.000+0000",
			"updatedAt": "2026-05-28T05:00:00.000+0000"
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

	comment, err := client.CreateIssueComment(context.Background(), " VAY-42 ", IssueCommentCreateOptions{
		Body:     " Runner update ready. ",
		AuthorID: " 112233 ",
		Marker:   " split-summary ",
	})
	if err != nil {
		t.Fatalf("create issue comment: %v", err)
	}

	if capturedRequest == nil {
		t.Fatalf("expected request to be sent")
	}
	if got := capturedRequest.Method; got != http.MethodPost {
		t.Fatalf("expected POST method, got %s", got)
	}
	if got := capturedRequest.URL.String(); got != "https://api.tracker.yandex.net/v3/issues/VAY-42/comments" {
		t.Fatalf("expected comments URL, got %q", got)
	}

	wantText := strings.Join([]string{
		"<!-- yolo-runner:split-summary -->",
		"",
		"Runner update ready.",
	}, "\n")
	if capturedBody.Text != wantText {
		t.Fatalf("expected marked comment text %q, got %q", wantText, capturedBody.Text)
	}
	if got := strings.Join(capturedBody.Summonees, ","); got != "112233" {
		t.Fatalf("expected author summonee 112233, got %q", got)
	}
	if capturedBody.MarkupType != "md" {
		t.Fatalf("expected markdown markup type for marker support, got %q", capturedBody.MarkupType)
	}
	if comment.ID != "626" {
		t.Fatalf("expected returned comment ID 626, got %q", comment.ID)
	}
}

func TestClientIssueLabelMutations(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name          string
		call          func(*Client) error
		wantOperation string
		statusCode    int
		responseBody  string
	}{
		{
			name: "add",
			call: func(client *Client) error {
				return client.AddLabel(ctx, " VAY-42 ", " ready-for-yolo ")
			},
			wantOperation: "add",
			statusCode:    http.StatusOK,
			responseBody:  `{}`,
		},
		{
			name: "remove",
			call: func(client *Client) error {
				return client.RemoveLabel(ctx, " VAY-42 ", " ready-for-yolo ")
			},
			wantOperation: "remove",
			statusCode:    http.StatusOK,
			responseBody:  `{}`,
		},
		{
			name: "already-present",
			call: func(client *Client) error {
				return client.AddLabel(ctx, " VAY-42 ", " ready-for-yolo ")
			},
			wantOperation: "add",
			statusCode:    http.StatusConflict,
			responseBody:  `{"message":"label \"ready-for-yolo\" is already present"}`,
		},
		{
			name: "already-absent",
			call: func(client *Client) error {
				return client.RemoveLabel(ctx, " VAY-42 ", " ready-for-yolo ")
			},
			wantOperation: "remove",
			statusCode:    http.StatusConflict,
			responseBody:  `{"message":"label \"ready-for-yolo\" is not present"}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var capturedRequest *http.Request
			var capturedBody map[string]map[string][]string
			httpClient := fakeHTTPClient(func(req *http.Request) (*http.Response, error) {
				capturedRequest = req
				if err := json.NewDecoder(req.Body).Decode(&capturedBody); err != nil {
					t.Fatalf("decode request body: %v", err)
				}
				return jsonResponse(tc.statusCode, tc.responseBody), nil
			})

			client, err := NewClient(Config{
				Endpoint:   "https://api.tracker.yandex.net/v3",
				Token:      "tracker-token",
				HTTPClient: httpClient,
			})
			if err != nil {
				t.Fatalf("new client: %v", err)
			}

			if err := tc.call(client); err != nil {
				t.Fatalf("mutate label: %v", err)
			}

			if capturedRequest == nil {
				t.Fatalf("expected request to be sent")
			}
			if got := capturedRequest.Method; got != http.MethodPatch {
				t.Fatalf("expected PATCH method, got %s", got)
			}
			if got := capturedRequest.URL.String(); got != "https://api.tracker.yandex.net/v3/issues/VAY-42" {
				t.Fatalf("expected issue URL, got %q", got)
			}
			tags := capturedBody["tags"]
			if len(tags) != 1 {
				t.Fatalf("expected one tags operation, got %#v", tags)
			}
			values := tags[tc.wantOperation]
			if len(values) != 1 || values[0] != "ready-for-yolo" {
				t.Fatalf("expected %s label ready-for-yolo, got %#v", tc.wantOperation, values)
			}
		})
	}
}

func TestClientExecuteIssueTransitionUsesExecuteEndpointWithResolution(t *testing.T) {
	var capturedRequest *http.Request
	var capturedBody map[string]string
	httpClient := fakeHTTPClient(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet && req.URL.Path == "/v3/issues/VAY-42/transitions" {
			return jsonResponse(http.StatusOK, `[{"id":"closed"}]`), nil
		}
		capturedRequest = req
		if err := json.NewDecoder(req.Body).Decode(&capturedBody); err != nil {
			t.Fatalf("decode transition body: %v", err)
		}
		return jsonResponse(http.StatusOK, `{}`), nil
	})

	client, err := NewClient(Config{
		Endpoint:   "https://api.tracker.yandex.net/v3",
		Token:      "tracker-token",
		HTTPClient: httpClient,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	if err := client.ExecuteIssueTransition(context.Background(), " VAY-42 ", IssueTransitionOptions{
		Transition: " closed ",
		Resolution: " fixed ",
	}); err != nil {
		t.Fatalf("ExecuteIssueTransition returned error: %v", err)
	}

	if capturedRequest == nil {
		t.Fatalf("expected transition request to be sent")
	}
	if got := capturedRequest.Method; got != http.MethodPost {
		t.Fatalf("expected POST method, got %s", got)
	}
	if got := capturedRequest.URL.String(); got != "https://api.tracker.yandex.net/v3/issues/VAY-42/transitions/closed/_execute" {
		t.Fatalf("expected transition URL, got %q", got)
	}
	if capturedBody["resolution"] != "fixed" {
		t.Fatalf("expected fixed resolution, got %#v", capturedBody)
	}
}

func TestClientExecuteIssueTransitionFallsBackToLegacyEndpoint(t *testing.T) {
	var paths []string
	httpClient := fakeHTTPClient(func(req *http.Request) (*http.Response, error) {
		paths = append(paths, req.URL.Path)
		if req.Method == http.MethodGet && req.URL.Path == "/v3/issues/VAY-42/transitions" {
			return jsonResponse(http.StatusOK, `[{"id":"inProgress"}]`), nil
		}
		if req.URL.Path == "/v3/issues/VAY-42/transitions/inProgress/_execute" {
			return jsonResponse(http.StatusNotFound, `{"message":"not found"}`), nil
		}
		return jsonResponse(http.StatusOK, `{}`), nil
	})

	client, err := NewClient(Config{
		Endpoint:   "https://api.tracker.yandex.net/v3",
		Token:      "tracker-token",
		HTTPClient: httpClient,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	if err := client.ExecuteIssueTransition(context.Background(), "VAY-42", IssueTransitionOptions{
		Transition: "inProgress",
	}); err != nil {
		t.Fatalf("ExecuteIssueTransition returned error: %v", err)
	}

	want := []string{
		"/v3/issues/VAY-42/transitions",
		"/v3/issues/VAY-42/transitions/inProgress/_execute",
		"/v3/issues/VAY-42/transitions/inProgress",
	}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("unexpected transition paths:\n got %#v\nwant %#v", paths, want)
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
