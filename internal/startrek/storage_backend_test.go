package startrek

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

func TestStorageBackendGetTaskTreeReturnsSyntheticQueueRootWithEligibleParent(t *testing.T) {
	var capturedBody map[string]any
	var capturedRequests []string
	httpClient := fakeHTTPClient(func(req *http.Request) (*http.Response, error) {
		capturedRequests = append(capturedRequests, req.Method+" "+req.URL.String())

		if req.URL.Path != "/v3/issues/_search" {
			t.Fatalf("unexpected request path %s", req.URL.Path)
		}
		if err := json.NewDecoder(req.Body).Decode(&capturedBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}

		return jsonResponseWithHeaders(http.StatusOK, `[
			{
				"id": "64200b5f7b5b7c0011223344",
				"key": "VAY-42",
				"summary": "Parent issue ready for splitting",
				"description": "Implement the parent issue.",
				"tags": ["yolo-agent-ready"],
				"createdBy": {
					"id": "112233",
					"display": "Ada Lovelace"
				},
				"updatedAt": "2026-05-28T01:02:03.000+0000"
			}
		]`, http.Header{
			"X-Total-Count": []string{"1"},
			"X-Total-Pages": []string{"1"},
		}), nil
	})

	backend, err := NewStorageBackend(Config{
		Endpoint:   "https://api.tracker.yandex.net/v3",
		Token:      "tracker-token",
		HTTPClient: httpClient,
	})
	if err != nil {
		t.Fatalf("new storage backend: %v", err)
	}

	tree, err := backend.GetTaskTree(context.Background(), " VAY ")
	if err != nil {
		t.Fatalf("GetTaskTree returned error: %v", err)
	}

	if tree.Root.ID != "VAY" {
		t.Fatalf("expected synthetic queue root ID VAY, got %q", tree.Root.ID)
	}
	if tree.Root.Title != "VAY" {
		t.Fatalf("expected synthetic queue root title VAY, got %q", tree.Root.Title)
	}
	if tree.Root.Status != contracts.TaskStatusOpen {
		t.Fatalf("expected synthetic queue root to be open, got %q", tree.Root.Status)
	}

	child, ok := tree.Tasks["VAY-42"]
	if !ok {
		t.Fatalf("expected eligible parent issue VAY-42 in task tree, got tasks %#v", tree.Tasks)
	}
	if child.Title != "Parent issue ready for splitting" {
		t.Fatalf("expected mapped child title, got %q", child.Title)
	}
	if child.ParentID != "VAY" {
		t.Fatalf("expected child parent ID VAY, got %q", child.ParentID)
	}
	if child.Status != contracts.TaskStatusOpen {
		t.Fatalf("expected child status open, got %q", child.Status)
	}

	assertStartrekRelation(t, tree.Relations, contracts.TaskRelation{
		FromID: "VAY",
		ToID:   "VAY-42",
		Type:   contracts.RelationParent,
	})

	filter, ok := capturedBody["filter"].(map[string]any)
	if !ok {
		t.Fatalf("expected search filter object, got %#v", capturedBody["filter"])
	}
	if got := filter["queue"]; got != "VAY" {
		t.Fatalf("expected queue filter VAY, got %#v", got)
	}
	if got := filter["tags"]; got != "yolo-agent-ready" {
		t.Fatalf("expected ready label filter yolo-agent-ready, got %#v", got)
	}

	wantRequests := []string{
		"POST https://api.tracker.yandex.net/v3/issues/_search?page=1&perPage=50",
	}
	if strings.Join(capturedRequests, "\n") != strings.Join(wantRequests, "\n") {
		t.Fatalf("unexpected requests:\n%s", strings.Join(capturedRequests, "\n"))
	}
}

func assertStartrekRelation(t *testing.T, relations []contracts.TaskRelation, want contracts.TaskRelation) {
	t.Helper()
	for _, got := range relations {
		if got == want {
			return
		}
	}
	t.Fatalf("expected relation %#v in %#v", want, relations)
}
