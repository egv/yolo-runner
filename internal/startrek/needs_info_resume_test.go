package startrek

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestStorageBackendResumeNeedsInfoTasksReopensAfterAuthorReply(t *testing.T) {
	var operations []string
	httpClient := fakeHTTPClient(func(req *http.Request) (*http.Response, error) {
		switch req.Method + " " + req.URL.Path {
		case "POST /v3/issues/_search":
			var capturedBody map[string]any
			if err := json.NewDecoder(req.Body).Decode(&capturedBody); err != nil {
				t.Fatalf("decode search body: %v", err)
			}
			filter, _ := capturedBody["filter"].(map[string]any)
			if got := strings.TrimSpace(filter["tags"].(string)); got != "needs-info" {
				t.Fatalf("expected needs-info search, got %q", got)
			}
			return jsonResponseWithHeaders(http.StatusOK, `[
				{
					"id": "64200b5f7b5b7c0011223344",
					"key": "VAY-42",
					"summary": "Needs info",
					"description": "Waiting on user.",
					"tags": ["needs-info"],
					"createdBy": {"id": "author-1", "display": "Ada Lovelace"},
					"assignee": {"id": "assignee-1", "display": "Grace Hopper"},
					"updatedAt": "2026-05-28T05:02:00.000+0000"
				}
			]`, http.Header{
				"X-Total-Count": []string{"1"},
				"X-Total-Pages": []string{"1"},
			}), nil
		case "GET /v3/issues/VAY-42/comments":
			return jsonResponse(http.StatusOK, `[
				{
					"id": 1,
					"text": "<!-- yolo-runner:needs-info -->\n\nQuestions:\n1. Which secret?",
					"createdBy": {"id": "runner", "display": "YOLO Runner"},
					"createdAt": "2026-05-28T05:00:00.000+0000",
					"updatedAt": "2026-05-28T05:00:00.000+0000"
				},
				{
					"id": 2,
					"text": "Use secret sec-123.",
					"createdBy": {"id": "author-1", "display": "Ada Lovelace"},
					"createdAt": "2026-05-28T05:01:00.000+0000",
					"updatedAt": "2026-05-28T05:01:00.000+0000"
				}
			]`), nil
		case "PATCH /v3/issues/VAY-42":
			var body struct {
				Tags map[string][]string `json:"tags"`
			}
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatalf("decode label patch: %v", err)
			}
			for _, label := range body.Tags["add"] {
				operations = append(operations, "add "+label)
			}
			for _, label := range body.Tags["remove"] {
				operations = append(operations, "remove "+label)
			}
			return jsonResponse(http.StatusOK, `{}`), nil
		default:
			t.Fatalf("unexpected request %s %s", req.Method, req.URL.Path)
			return nil, nil
		}
	})

	backend, err := NewStorageBackend(Config{
		Endpoint:   "https://api.tracker.yandex.net/v3",
		Token:      "tracker-token",
		HTTPClient: httpClient,
	})
	if err != nil {
		t.Fatalf("new storage backend: %v", err)
	}

	resumed, err := backend.ResumeNeedsInfoTasks(context.Background(), NeedsInfoResumeInput{
		QueueKey: "VAY",
	})
	if err != nil {
		t.Fatalf("ResumeNeedsInfoTasks returned error: %v", err)
	}
	if !reflect.DeepEqual(resumed, []string{"VAY-42"}) {
		t.Fatalf("unexpected resumed issues: %#v", resumed)
	}

	wantOps := []string{"add yolo-agent-ready", "remove needs-info"}
	if !reflect.DeepEqual(operations, wantOps) {
		t.Fatalf("unexpected label operations:\n got %#v\nwant %#v", operations, wantOps)
	}
}

func TestStorageBackendResumeNeedsInfoTasksWaitsWithoutAuthorReply(t *testing.T) {
	var patchCount int
	httpClient := fakeHTTPClient(func(req *http.Request) (*http.Response, error) {
		switch req.Method + " " + req.URL.Path {
		case "POST /v3/issues/_search":
			return jsonResponseWithHeaders(http.StatusOK, `[
				{
					"id": "64200b5f7b5b7c0011223344",
					"key": "VAY-42",
					"summary": "Needs info",
					"description": "Waiting on user.",
					"tags": ["needs-info"],
					"createdBy": {"id": "author-1", "display": "Ada Lovelace"},
					"updatedAt": "2026-05-28T05:02:00.000+0000"
				}
			]`, http.Header{
				"X-Total-Count": []string{"1"},
				"X-Total-Pages": []string{"1"},
			}), nil
		case "GET /v3/issues/VAY-42/comments":
			return jsonResponse(http.StatusOK, `[
				{
					"id": 1,
					"text": "<!-- yolo-runner:needs-info -->\n\nQuestions:\n1. Which secret?",
					"createdBy": {"id": "runner", "display": "YOLO Runner"},
					"createdAt": "2026-05-28T05:00:00.000+0000",
					"updatedAt": "2026-05-28T05:00:00.000+0000"
				}
			]`), nil
		case "PATCH /v3/issues/VAY-42":
			patchCount++
			return jsonResponse(http.StatusOK, `{}`), nil
		default:
			t.Fatalf("unexpected request %s %s", req.Method, req.URL.Path)
			return nil, nil
		}
	})

	backend, err := NewStorageBackend(Config{
		Endpoint:   "https://api.tracker.yandex.net/v3",
		Token:      "tracker-token",
		HTTPClient: httpClient,
	})
	if err != nil {
		t.Fatalf("new storage backend: %v", err)
	}

	resumed, err := backend.ResumeNeedsInfoTasks(context.Background(), NeedsInfoResumeInput{
		QueueKey: "VAY",
	})
	if err != nil {
		t.Fatalf("ResumeNeedsInfoTasks returned error: %v", err)
	}
	if len(resumed) != 0 {
		t.Fatalf("expected no resumed issues, got %#v", resumed)
	}
	if patchCount != 0 {
		t.Fatalf("expected no label patches without author reply, got %d", patchCount)
	}
}
