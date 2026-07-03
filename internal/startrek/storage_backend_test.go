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
	var capturedRequests []string
	var capturedFilter map[string]any
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
		capturedFilter = filter

		return jsonResponseWithHeaders(http.StatusOK, `[
			{
				"id": "64200b5f7b5b7c0011223344",
				"key": "VAY-42",
				"summary": "Parent issue ready for splitting",
				"description": "Implement the parent issue.",
				"tags": ["yolo-agent-ready"],
				"status": {"key": "open", "display": "Open"},
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

	tree, err := backend.GetTaskTreeForQueue(context.Background(), QueueSearchOptions{
		QueueKey: " VAY ",
		Assignee: "bot-1",
		Label:    "yolo-agent-ready",
	})
	if err != nil {
		t.Fatalf("GetTaskTreeForQueue returned error: %v", err)
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

	// Discovery now issues a single search filtered by queue + label + assignee.
	if got := capturedFilter["tags"]; got != "yolo-agent-ready" {
		t.Fatalf("expected single search tagged yolo-agent-ready, got %#v", capturedFilter["tags"])
	}
	if got := capturedFilter["assignee"]; got != "bot-1" {
		t.Fatalf("expected assignee filter bot-1, got %#v", got)
	}

	wantRequests := []string{
		"POST https://api.tracker.yandex.net/v3/issues/_search?page=1&perPage=50",
	}
	if strings.Join(capturedRequests, "\n") != strings.Join(wantRequests, "\n") {
		t.Fatalf("unexpected requests:\n%s", strings.Join(capturedRequests, "\n"))
	}
}

func TestStorageBackendSetTaskStatusExecutesConfiguredTransitionOnly(t *testing.T) {
	var operations []string
	httpClient := fakeHTTPClient(func(req *http.Request) (*http.Response, error) {
		switch req.Method + " " + req.URL.Path {
		case "GET /v3/issues/VAY-42/transitions":
			return jsonResponse(http.StatusOK, `[{"id":"close"}]`), nil
		case "POST /v3/issues/VAY-42/transitions/close/_execute":
			var body map[string]string
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatalf("decode transition body: %v", err)
			}
			operations = append(operations, "transition close resolution="+body["resolution"])
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
		ReadyLabel: "ready",
		StatusTransitions: StatusTransitionNames{
			Completed:           "closed",
			CompletedResolution: "fixed",
		},
	})
	if err != nil {
		t.Fatalf("new storage backend: %v", err)
	}

	if err := backend.SetTaskStatus(context.Background(), " VAY-42 ", contracts.TaskStatusClosed); err != nil {
		t.Fatalf("SetTaskStatus returned error: %v", err)
	}

	// SetTaskStatus now drives only the native workflow transition; the 4 status
	// labels are gone, so there are no PATCH/label operations.
	want := []string{
		"transition close resolution=fixed",
	}
	if !reflect.DeepEqual(operations, want) {
		t.Fatalf("unexpected operations:\n got %#v\nwant %#v", operations, want)
	}
}

func TestStorageBackendSetTaskStatusDoesNotRelabelWhenTransitionFails(t *testing.T) {
	var operations []string
	httpClient := fakeHTTPClient(func(req *http.Request) (*http.Response, error) {
		operations = append(operations, req.Method+" "+req.URL.Path)
		if req.Method == http.MethodGet {
			return jsonResponse(http.StatusOK, `[{"id":"closed"}]`), nil
		}
		if req.Method == http.MethodPost {
			return jsonResponse(http.StatusBadRequest, `{"message":"transition is not available"}`), nil
		}
		t.Fatalf("unexpected label request after failed transition: %s %s", req.Method, req.URL.Path)
		return nil, nil
	})

	backend, err := NewStorageBackend(Config{
		Endpoint:   "https://api.tracker.yandex.net/v3",
		Token:      "tracker-token",
		HTTPClient: httpClient,
		StatusTransitions: StatusTransitionNames{
			Completed: "closed",
		},
	})
	if err != nil {
		t.Fatalf("new storage backend: %v", err)
	}

	if err := backend.SetTaskStatus(context.Background(), "VAY-42", contracts.TaskStatusClosed); err == nil {
		t.Fatalf("expected SetTaskStatus to fail when transition fails")
	}

	want := []string{
		"GET /v3/issues/VAY-42/transitions",
		"POST /v3/issues/VAY-42/transitions/closed/_execute",
		"POST /v3/issues/VAY-42/transitions/closed",
	}
	if !reflect.DeepEqual(operations, want) {
		t.Fatalf("unexpected operations after failed transition:\n got %#v\nwant %#v", operations, want)
	}
}

func TestStorageBackendSetTaskStatusInProgressIsIdempotentWhenWorkflowAlreadyStarted(t *testing.T) {
	var operations []string
	httpClient := fakeHTTPClient(func(req *http.Request) (*http.Response, error) {
		operations = append(operations, req.Method+" "+req.URL.Path)
		switch req.Method + " " + req.URL.Path {
		case "GET /v3/issues/VAY-42/transitions":
			return jsonResponse(http.StatusOK, `[{"id":"close"},{"id":"need_info"},{"id":"stop_progress"}]`), nil
		case "GET /v3/issues/VAY-42":
			return jsonResponse(http.StatusOK, `{
				"key": "VAY-42",
				"summary": "Task",
				"description": "",
				"tags": ["running"],
				"status": {"key": "inProgress", "display": "In Progress"},
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
		ReadyLabel: "ready",
		StatusTransitions: StatusTransitionNames{
			InProgress: "inProgress",
		},
	})
	if err != nil {
		t.Fatalf("new storage backend: %v", err)
	}

	if err := backend.SetTaskStatus(context.Background(), "VAY-42", contracts.TaskStatusInProgress); err != nil {
		t.Fatalf("SetTaskStatus returned error: %v", err)
	}

	// No matching in-progress transition; the issue is already inProgress, so
	// the transition is a no-op via native status. No label PATCHes occur.
	want := []string{
		"GET /v3/issues/VAY-42/transitions",
		"GET /v3/issues/VAY-42",
	}
	if !reflect.DeepEqual(operations, want) {
		t.Fatalf("unexpected operations for idempotent in-progress transition:\n got %#v\nwant %#v", operations, want)
	}
}

func TestStorageBackendSetTaskStatusInProgressIsIdempotentWhenLabelsLagBehindWorkflow(t *testing.T) {
	// Review retry swaps the ready label back without a configured "open"
	// workflow transition; the issue is still In Progress in the workflow, so
	// the in-progress claim is a no-op via native status.
	var operations []string
	httpClient := fakeHTTPClient(func(req *http.Request) (*http.Response, error) {
		operations = append(operations, req.Method+" "+req.URL.Path)
		switch req.Method + " " + req.URL.Path {
		case "GET /v3/issues/VAY-42/transitions":
			return jsonResponse(http.StatusOK, `[{"id":"close"},{"id":"need_info"},{"id":"stop_progress"}]`), nil
		case "GET /v3/issues/VAY-42":
			return jsonResponse(http.StatusOK, `{
				"key": "VAY-42",
				"summary": "Task",
				"description": "",
				"tags": ["ready"],
				"status": {"key": "inProgress", "display": "In Progress"},
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
		ReadyLabel: "ready",
		StatusTransitions: StatusTransitionNames{
			InProgress: "inProgress",
		},
	})
	if err != nil {
		t.Fatalf("new storage backend: %v", err)
	}

	if err := backend.SetTaskStatus(context.Background(), "VAY-42", contracts.TaskStatusInProgress); err != nil {
		t.Fatalf("SetTaskStatus returned error: %v", err)
	}

	want := []string{
		"GET /v3/issues/VAY-42/transitions",
		"GET /v3/issues/VAY-42",
	}
	if !reflect.DeepEqual(operations, want) {
		t.Fatalf("unexpected operations for workflow-status idempotent transition:\n got %#v\nwant %#v", operations, want)
	}
}

func TestStorageBackendDerivesStatusFromNativeWorkflowStatus(t *testing.T) {
	httpClient := fakeHTTPClient(func(req *http.Request) (*http.Response, error) {
		switch req.Method + " " + req.URL.Path {
		case "GET /v3/issues/VAY-42":
			return jsonResponse(http.StatusOK, `{
				"id": "64200b5f7b5b7c0011223344",
				"key": "VAY-42",
				"summary": "In-progress issue",
				"description": "Implement the issue.",
				"tags": ["yolo-agent-ready"],
				"status": {"key": "inProgress", "display": "In Progress"},
				"createdBy": {
					"id": "112233",
					"display": "Ada Lovelace"
				},
				"updatedAt": "2026-05-28T01:02:03.000+0000"
			}`), nil
		case "GET /v3/issues/VAY-42/comments":
			return jsonResponse(http.StatusOK, `[]`), nil
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

	// Native workflow status is the single source of truth: an issue in
	// inProgress maps to TaskStatusInProgress regardless of its tags.
	task, err := backend.GetTask(context.Background(), "VAY-42")
	if err != nil {
		t.Fatalf("GetTask returned error: %v", err)
	}
	if task.Status != contracts.TaskStatusInProgress {
		t.Fatalf("expected native inProgress to map to TaskStatusInProgress, got %q", task.Status)
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
				"status": {"key": "open", "display": "Open"},
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
				"status": {"key": "open", "display": "Open"},
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
				"status": {"key": "open", "display": "Open"},
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
	createdLinks := map[string][]string{}
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
		case "POST /v3/issues/VAY-44/links":
			var body startrekIssueLinkCreateRequest
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatalf("decode create link request body: %v", err)
			}
			if body.Relationship != "depends_on" || body.Issue != "VAY-43" {
				t.Fatalf("unexpected create link body: %#v", body)
			}
			createdLinks["VAY-44"] = append(createdLinks["VAY-44"], body.Issue)
			return jsonResponse(http.StatusCreated, `{}`), nil
		case "POST /v3/issues/_search":
			payload := []map[string]any{
				{
					"id":          "64200b5f7b5b7c0011223344",
					"key":         "VAY-42",
					"summary":     "Split parent issue",
					"description": "Parent issue already split into subtasks.",
					"tags":        []string{"yolo-agent-ready"},
					"status":      map[string]any{"key": "open", "display": "Open"},
					"createdBy": map[string]any{
						"id":      "112233",
						"display": "Ada Lovelace",
					},
					"updatedAt": "2026-05-28T01:02:03.000+0000",
				},
			}
			for i, issue := range createdIssues {
				item := map[string]any{
					"key":         issue.Key,
					"summary":     issue.Summary,
					"description": issue.Description,
					"tags":        issue.Tags,
					"status":      map[string]any{"key": "open", "display": "Open"},
					"parent": map[string]any{
						"key": issue.Parent,
					},
					"createdBy": map[string]any{
						"id":      "112233",
						"display": "Ada Lovelace",
					},
					"updatedAt": fmt.Sprintf("2026-05-28T01:%02d:00.000+0000", 3+i),
				}
				if dependencies := createdLinks[issue.Key]; len(dependencies) > 0 {
					refs := make([]map[string]string, 0, len(dependencies))
					for _, dependency := range dependencies {
						refs = append(refs, map[string]string{"key": dependency})
					}
					item["dependsOn"] = refs
				}
				payload = append(payload, item)
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
