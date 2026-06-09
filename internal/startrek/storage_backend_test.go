package startrek

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/egv/yolo-runner/v2/internal/agent/splitter"
	"github.com/egv/yolo-runner/v2/internal/contracts"
	enginepkg "github.com/egv/yolo-runner/v2/internal/engine"
)

func TestStorageBackendGetTaskTreeReturnsSyntheticQueueRootWithEligibleParent(t *testing.T) {
	var capturedLabels []string
	var capturedRequests []string
	httpClient := fakeHTTPClient(func(req *http.Request) (*http.Response, error) {
		capturedRequests = append(capturedRequests, req.Method+" "+req.URL.String())

		if req.URL.Path != "/v3/issues/_search" {
			t.Fatalf("unexpected request path %s", req.URL.Path)
		}
		var capturedBody map[string]any
		if err := json.NewDecoder(req.Body).Decode(&capturedBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		filter, ok := capturedBody["filter"].(map[string]any)
		if !ok {
			t.Fatalf("expected search filter object, got %#v", capturedBody["filter"])
		}
		if got := filter["queue"]; got != "VAY" {
			t.Fatalf("expected queue filter VAY, got %#v", got)
		}
		capturedLabels = append(capturedLabels, strings.TrimSpace(fmt.Sprint(filter["tags"])))

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

	wantLabels := []string{
		"yolo-agent-ready",
		"yolo-agent-in-progress",
		"yolo-agent-completed",
		"yolo-agent-blocked",
		"yolo-agent-failed",
	}
	if !reflect.DeepEqual(capturedLabels, wantLabels) {
		t.Fatalf("unexpected status label searches:\n got %#v\nwant %#v", capturedLabels, wantLabels)
	}

	wantRequests := []string{
		"POST https://api.tracker.yandex.net/v3/issues/_search?page=1&perPage=50",
		"POST https://api.tracker.yandex.net/v3/issues/_search?page=1&perPage=50",
		"POST https://api.tracker.yandex.net/v3/issues/_search?page=1&perPage=50",
		"POST https://api.tracker.yandex.net/v3/issues/_search?page=1&perPage=50",
		"POST https://api.tracker.yandex.net/v3/issues/_search?page=1&perPage=50",
	}
	if strings.Join(capturedRequests, "\n") != strings.Join(wantRequests, "\n") {
		t.Fatalf("unexpected requests:\n%s", strings.Join(capturedRequests, "\n"))
	}
}

func TestStorageBackendSetTaskStatusMapsStatusToLabels(t *testing.T) {
	var operations []string
	httpClient := fakeHTTPClient(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPatch || req.URL.Path != "/v3/issues/VAY-42" {
			t.Fatalf("unexpected request %s %s", req.Method, req.URL.Path)
		}
		var body struct {
			Tags map[string][]string `json:"tags"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatalf("decode label patch: %v", err)
		}
		for _, label := range body.Tags["remove"] {
			operations = append(operations, "remove "+label)
		}
		for _, label := range body.Tags["add"] {
			operations = append(operations, "add "+label)
		}
		return jsonResponse(http.StatusOK, `{}`), nil
	})

	backend, err := NewStorageBackend(Config{
		Endpoint:        "https://api.tracker.yandex.net/v3",
		Token:           "tracker-token",
		HTTPClient:      httpClient,
		ReadyLabel:      "ready",
		InProgressLabel: "running",
		CompletedLabel:  "done",
		BlockedLabel:    "blocked",
		FailedLabel:     "failed",
	})
	if err != nil {
		t.Fatalf("new storage backend: %v", err)
	}

	if err := backend.SetTaskStatus(context.Background(), " VAY-42 ", contracts.TaskStatusClosed); err != nil {
		t.Fatalf("SetTaskStatus returned error: %v", err)
	}

	want := []string{
		"remove ready",
		"remove running",
		"remove blocked",
		"remove failed",
		"add done",
	}
	if !reflect.DeepEqual(operations, want) {
		t.Fatalf("unexpected label operations:\n got %#v\nwant %#v", operations, want)
	}
}

func TestStorageBackendGetTaskTreeIncludesLocalStatusOverrideWhenSearchLags(t *testing.T) {
	httpClient := fakeHTTPClient(func(req *http.Request) (*http.Response, error) {
		switch req.Method + " " + req.URL.Path {
		case "PATCH /v3/issues/VAY-42":
			return jsonResponse(http.StatusOK, `{}`), nil
		case "POST /v3/issues/_search":
			return jsonResponseWithHeaders(http.StatusOK, `[]`, http.Header{
				"X-Total-Count": []string{"0"},
				"X-Total-Pages": []string{"1"},
			}), nil
		case "GET /v3/issues/VAY-42":
			return jsonResponse(http.StatusOK, `{
				"id": "64200b5f7b5b7c0011223344",
				"key": "VAY-42",
				"summary": "Ready issue with lagged search index",
				"description": "Implement the issue.",
				"tags": ["yolo-agent-in-progress"],
				"createdBy": {
					"id": "112233",
					"display": "Ada Lovelace"
				},
				"updatedAt": "2026-05-28T01:02:03.000+0000"
			}`), nil
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

	if err := backend.AddLabel(context.Background(), "VAY-42", "yolo-agent-ready"); err != nil {
		t.Fatalf("AddLabel returned error: %v", err)
	}

	tree, err := backend.GetTaskTree(context.Background(), "VAY")
	if err != nil {
		t.Fatalf("GetTaskTree returned error: %v", err)
	}

	task, ok := tree.Tasks["VAY-42"]
	if !ok {
		t.Fatalf("expected override issue VAY-42 in task tree, got tasks %#v", tree.Tasks)
	}
	if task.Status != contracts.TaskStatusOpen {
		t.Fatalf("expected local ready override to make VAY-42 open, got %q", task.Status)
	}

	taskEngine := enginepkg.NewTaskEngine()
	graph, err := taskEngine.BuildGraph(tree)
	if err != nil {
		t.Fatalf("BuildGraph returned error: %v", err)
	}
	if got, want := startrekSummaryIDs(taskEngine.GetNextAvailable(graph)), []string{"VAY-42"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("expected locally ready issue runnable, got %v want %v", got, want)
	}
}

func TestStorageBackendGetTaskTreeExpandsSplitSubtasksAndSkipsParentAsWork(t *testing.T) {
	httpClient := fakeHTTPClient(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/v3/issues/_search" {
			t.Fatalf("unexpected request path %s", req.URL.Path)
		}

		return jsonResponseWithHeaders(http.StatusOK, `[
			{
				"id": "64200b5f7b5b7c0011223344",
				"key": "VAY-42",
				"summary": "Split parent issue",
				"description": "Parent issue already split into subtasks.",
				"tags": ["yolo-agent-ready"],
				"createdBy": {
					"id": "112233",
					"display": "Ada Lovelace"
				},
				"updatedAt": "2026-05-28T01:02:03.000+0000"
			},
			{
				"id": "64200b5f7b5b7c0011223345",
				"key": "VAY-43",
				"summary": "Implement first leaf",
				"description": "First generated subtask.",
				"tags": ["yolo-agent-ready", "agent:subtask"],
				"parent": {"key": "VAY-42"},
				"createdBy": {
					"id": "112233",
					"display": "Ada Lovelace"
				},
				"updatedAt": "2026-05-28T01:03:03.000+0000"
			},
			{
				"id": "64200b5f7b5b7c0011223346",
				"key": "VAY-44",
				"summary": "Implement dependent leaf",
				"description": "Second generated subtask.",
				"tags": ["yolo-agent-ready", "agent:subtask", "depends-on:VAY-43"],
				"parent": {"key": "VAY-42"},
				"createdBy": {
					"id": "112233",
					"display": "Ada Lovelace"
				},
				"updatedAt": "2026-05-28T01:04:03.000+0000"
			}
		]`, http.Header{
			"X-Total-Count": []string{"3"},
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

	tree, err := backend.GetTaskTree(context.Background(), "VAY")
	if err != nil {
		t.Fatalf("GetTaskTree returned error: %v", err)
	}

	parent := tree.Tasks["VAY-42"]
	if parent.ParentID != "VAY" {
		t.Fatalf("expected split parent to remain under queue root, got parent ID %q", parent.ParentID)
	}

	firstLeaf := tree.Tasks["VAY-43"]
	if firstLeaf.ParentID != "VAY-42" {
		t.Fatalf("expected first leaf parent ID VAY-42, got %q", firstLeaf.ParentID)
	}
	secondLeaf := tree.Tasks["VAY-44"]
	if secondLeaf.ParentID != "VAY-42" {
		t.Fatalf("expected second leaf parent ID VAY-42, got %q", secondLeaf.ParentID)
	}
	if deps := secondLeaf.Metadata["dependencies"]; deps != "VAY-43" {
		t.Fatalf("expected second leaf dependency metadata VAY-43, got %q", deps)
	}

	assertStartrekRelation(t, tree.Relations, contracts.TaskRelation{
		FromID: "VAY-42",
		ToID:   "VAY-43",
		Type:   contracts.RelationParent,
	})
	assertStartrekRelation(t, tree.Relations, contracts.TaskRelation{
		FromID: "VAY-42",
		ToID:   "VAY-44",
		Type:   contracts.RelationParent,
	})
	assertStartrekRelation(t, tree.Relations, contracts.TaskRelation{
		FromID: "VAY-44",
		ToID:   "VAY-43",
		Type:   contracts.RelationDependsOn,
	})
	assertStartrekRelation(t, tree.Relations, contracts.TaskRelation{
		FromID: "VAY-43",
		ToID:   "VAY-44",
		Type:   contracts.RelationBlocks,
	})

	taskEngine := enginepkg.NewTaskEngine()
	graph, err := taskEngine.BuildGraph(tree)
	if err != nil {
		t.Fatalf("BuildGraph returned error: %v", err)
	}

	if got, want := startrekSummaryIDs(taskEngine.GetNextAvailable(graph)), []string{"VAY-43"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("expected only unblocked leaf subtask runnable, got %v want %v", got, want)
	}
}

func TestStorageBackendSplitSubtasksOrderDependenciesGateAvailability(t *testing.T) {
	type createdIssue struct {
		Key         string
		Summary     string
		Description string
		Tags        []string
		Parent      string
	}

	createdIssues := make([]createdIssue, 0, 2)
	httpClient := fakeHTTPClient(func(req *http.Request) (*http.Response, error) {
		switch req.Method + " " + req.URL.Path {
		case "POST /v3/issues/":
			var body startrekIssueCreateRequest
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatalf("decode create request body: %v", err)
			}

			key := fmt.Sprintf("VAY-%d", 43+len(createdIssues))
			createdIssues = append(createdIssues, createdIssue{
				Key:         key,
				Summary:     body.Summary,
				Description: body.Description,
				Tags:        append([]string(nil), body.Tags...),
				Parent:      body.Parent,
			})

			raw, err := json.Marshal(map[string]any{
				"key":         key,
				"summary":     body.Summary,
				"description": body.Description,
				"tags":        body.Tags,
				"parent": map[string]any{
					"key": body.Parent,
				},
				"createdBy": map[string]any{
					"id":      "112233",
					"display": "Ada Lovelace",
				},
				"updatedAt": fmt.Sprintf("2026-05-28T01:%02d:00.000+0000", 2+len(createdIssues)),
			})
			if err != nil {
				t.Fatalf("marshal create response: %v", err)
			}
			return jsonResponse(http.StatusOK, string(raw)), nil
		case "POST /v3/issues/_search":
			payload := []map[string]any{
				{
					"id":          "64200b5f7b5b7c0011223344",
					"key":         "VAY-42",
					"summary":     "Split parent issue",
					"description": "Parent issue already split into subtasks.",
					"tags":        []string{"yolo-agent-ready"},
					"createdBy": map[string]any{
						"id":      "112233",
						"display": "Ada Lovelace",
					},
					"updatedAt": "2026-05-28T01:02:03.000+0000",
				},
			}
			for i, issue := range createdIssues {
				payload = append(payload, map[string]any{
					"key":         issue.Key,
					"summary":     issue.Summary,
					"description": issue.Description,
					"tags":        issue.Tags,
					"parent": map[string]any{
						"key": issue.Parent,
					},
					"createdBy": map[string]any{
						"id":      "112233",
						"display": "Ada Lovelace",
					},
					"updatedAt": fmt.Sprintf("2026-05-28T01:%02d:00.000+0000", 3+i),
				})
			}

			raw, err := json.Marshal(payload)
			if err != nil {
				t.Fatalf("marshal search response: %v", err)
			}
			return jsonResponseWithHeaders(http.StatusOK, string(raw), http.Header{
				"X-Total-Count": []string{fmt.Sprint(len(payload))},
				"X-Total-Pages": []string{"1"},
			}), nil
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

	_, err = (SplitSubtaskCreationService{Tracker: backend}).Create(context.Background(), SplitSubtasksInput{
		QueueKey: "VAY",
		ParentID: "VAY-42",
		Output: splitter.StrictOutput{
			Tasks: []splitter.Task{
				{
					ID:            "T20",
					Title:         "Implement first slice",
					Why:           []string{"First generated subtask."},
					InScope:       []string{"Implement the first slice."},
					OutOfScope:    []string{"Later slices."},
					StrictTDD:     []string{"Add targeted test", "Confirm it fails", "Make it pass"},
					DoneWhen:      []string{"First slice passes."},
					ExpectedFiles: []string{"internal/startrek/split_subtasks.go"},
				},
				{
					ID:            "T21",
					Title:         "Implement dependent slice",
					Why:           []string{"Second generated subtask."},
					InScope:       []string{"Implement the dependent slice."},
					OutOfScope:    []string{"Arc execution."},
					StrictTDD:     []string{"Add targeted test", "Confirm it fails", "Make it pass"},
					DoneWhen:      []string{"Dependent slice passes."},
					ExpectedFiles: []string{"internal/startrek/split_subtasks.go"},
				},
			},
			Order: []splitter.Dependency{
				{From: "T20", To: "T21"},
			},
		},
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	tree, err := backend.GetTaskTree(context.Background(), "VAY")
	if err != nil {
		t.Fatalf("GetTaskTree returned error: %v", err)
	}

	taskEngine := enginepkg.NewTaskEngine()
	graph, err := taskEngine.BuildGraph(tree)
	if err != nil {
		t.Fatalf("BuildGraph returned error: %v", err)
	}

	if got, want := startrekSummaryIDs(taskEngine.GetNextAvailable(graph)), []string{"VAY-43"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("expected only first split subtask runnable, got %v want %v", got, want)
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

func startrekSummaryIDs(summaries []contracts.TaskSummary) []string {
	ids := make([]string, 0, len(summaries))
	for _, summary := range summaries {
		ids = append(ids, summary.ID)
	}
	return ids
}
