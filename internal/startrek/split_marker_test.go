package startrek

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/egv/yolo-runner/v2/internal/agent/splitter"
	"github.com/egv/yolo-runner/v2/internal/contracts"
)

func TestIdempotentSplitSubtaskCreationServicePersistsMarkerAndReusesExistingSubtasks(t *testing.T) {
	tracker := &fakeSplitMarkerTracker{
		issueIDs: []string{"VAY-43", "VAY-44"},
		tasks: map[string]contracts.Task{
			"VAY-42": {
				ID:       "VAY-42",
				Metadata: map[string]string{},
			},
		},
	}
	service := IdempotentSplitSubtaskCreationService{
		Tracker:      tracker,
		SplitVersion: "strict-v1",
	}
	input := SplitSubtasksInput{
		QueueKey: "VAY",
		ParentID: "VAY-42",
		Output: splitter.StrictOutput{
			Tasks: []splitter.Task{
				{
					ID:    "T20",
					Title: "Invoke strict splitter",
				},
				{
					ID:        "T21",
					Title:     "Parse strict splitter output",
					DependsOn: []string{"T20"},
				},
			},
		},
	}

	first, err := service.Create(context.Background(), input)
	if err != nil {
		t.Fatalf("first Create returned error: %v", err)
	}
	if got, want := first.IssueIDsBySplitTaskID, map[string]string{"T20": "VAY-43", "T21": "VAY-44"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected first split task ID mapping:\n got %#v\nwant %#v", got, want)
	}
	if len(tracker.creates) != 2 {
		t.Fatalf("expected first split to create 2 subtasks, got %d", len(tracker.creates))
	}

	wantMarkerData := map[string]string{
		splitMarkerVersionKey:    "strict-v1",
		splitMarkerSubtaskIDsKey: "VAY-43,VAY-44",
	}
	if got := tracker.tasks["VAY-42"].Metadata; !reflect.DeepEqual(got, wantMarkerData) {
		t.Fatalf("unexpected persisted split marker:\n got %#v\nwant %#v", got, wantMarkerData)
	}

	second, err := service.Create(context.Background(), input)
	if err != nil {
		t.Fatalf("second Create returned error: %v", err)
	}
	if got, want := second.IssueIDsBySplitTaskID, map[string]string{"T20": "VAY-43", "T21": "VAY-44"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected second split task ID mapping:\n got %#v\nwant %#v", got, want)
	}
	if got, want := len(tracker.creates), 2; got != want {
		t.Fatalf("expected rerun to reuse marker without creating more subtasks, got %d creates want %d", got, want)
	}
	if got, want := splitMarkerIssueIDs(second.Issues), []string{"VAY-43", "VAY-44"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected reused issue IDs:\n got %#v\nwant %#v", got, want)
	}
}

func TestIdempotentSplitSubtaskCreationServiceUsesStorageBackedStartrekMarker(t *testing.T) {
	var createIssueCount int
	var createLinkBodies []startrekIssueLinkCreateRequest
	var markerCommentTexts []string

	httpClient := fakeHTTPClient(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/v3/issues/VAY-42":
			return jsonResponse(http.StatusOK, `{
				"id": "64200b5f7b5b7c0011223344",
				"key": "VAY-42",
				"summary": "Split parent issue",
				"description": "Generate implementation subtasks.",
				"tags": ["yolo-agent-ready"],
				"createdBy": {"id": "112233", "display": "Ada Lovelace"},
				"updatedAt": "2026-05-28T01:02:03.000+0000"
			}`), nil
		case req.Method == http.MethodGet && req.URL.Path == "/v3/issues/VAY-42/comments":
			rawComments, err := json.Marshal(startrekMarkerCommentsResponse(markerCommentTexts))
			if err != nil {
				t.Fatalf("marshal comments: %v", err)
			}
			return jsonResponse(http.StatusOK, string(rawComments)), nil
		case req.Method == http.MethodPost && req.URL.Path == "/v3/issues/":
			createIssueCount++
			issueID := map[int]string{
				1: "VAY-43",
				2: "VAY-44",
				3: "VAY-45",
				4: "VAY-46",
			}[createIssueCount]
			return jsonResponse(http.StatusCreated, `{
				"id": "64200b5f7b5b7c0011223345",
				"key": "`+issueID+`",
				"summary": "Generated subtask",
				"description": "Generated subtask body.",
				"tags": ["yolo-agent-ready", "agent:subtask"],
				"parent": {"key": "VAY-42"},
				"createdBy": {"id": "112233", "display": "Ada Lovelace"},
				"updatedAt": "2026-05-28T01:03:03.000+0000"
			}`), nil
		case req.Method == http.MethodPost && req.URL.Path == "/v3/issues/VAY-44/links":
			var body startrekIssueLinkCreateRequest
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatalf("decode issue link request: %v", err)
			}
			createLinkBodies = append(createLinkBodies, body)
			return jsonResponse(http.StatusCreated, `{}`), nil
		case req.Method == http.MethodPost && req.URL.Path == "/v3/issues/VAY-42/comments":
			var body startrekIssueCommentCreateRequest
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatalf("decode marker comment request: %v", err)
			}
			markerCommentTexts = append(markerCommentTexts, body.Text)
			return jsonResponse(http.StatusCreated, `{
				"id": "marker-comment",
				"text": `+strconvQuote(body.Text)+`,
				"createdBy": {"id": "runner", "display": "YOLO Runner"},
				"createdAt": "2026-05-28T01:04:03.000+0000",
				"updatedAt": "2026-05-28T01:04:03.000+0000"
			}`), nil
		default:
			t.Fatalf("unexpected request %s %s", req.Method, req.URL.Path)
			return nil, nil
		}
	})

	input := SplitSubtasksInput{
		QueueKey: "VAY",
		ParentID: "VAY-42",
		Output: splitter.StrictOutput{
			Tasks: []splitter.Task{
				{ID: "T20", Title: "Invoke strict splitter"},
				{ID: "T21", Title: "Parse strict splitter output", DependsOn: []string{"T20"}},
			},
		},
	}

	firstService := startrekSplitServiceForHTTPClient(t, httpClient)
	first, err := firstService.Create(context.Background(), input)
	if err != nil {
		t.Fatalf("first Create returned error: %v", err)
	}
	if got, want := first.IssueIDsBySplitTaskID, map[string]string{"T20": "VAY-43", "T21": "VAY-44"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected first split task ID mapping:\n got %#v\nwant %#v", got, want)
	}
	if got, want := createIssueCount, 2; got != want {
		t.Fatalf("expected first split to create %d subtasks, got %d", want, got)
	}
	if got, want := createLinkBodies, []startrekIssueLinkCreateRequest{{Relationship: "depends_on", Issue: "VAY-43"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected first split dependency links:\n got %#v\nwant %#v", got, want)
	}
	if got, want := len(markerCommentTexts), 1; got != want {
		t.Fatalf("expected first split to persist one marker comment, got %d", got)
	}
	markerComment := markerCommentTexts[0]
	for _, want := range []string{"<!-- yolo-runner:split-marker -->", "strict-v1", "VAY-43", "VAY-44"} {
		if !strings.Contains(markerComment, want) {
			t.Fatalf("expected marker comment to contain %q, got %q", want, markerComment)
		}
	}

	secondService := startrekSplitServiceForHTTPClient(t, httpClient)
	second, err := secondService.Create(context.Background(), input)
	if err != nil {
		t.Fatalf("second Create returned error: %v", err)
	}
	if got, want := second.IssueIDsBySplitTaskID, map[string]string{"T20": "VAY-43", "T21": "VAY-44"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected second split task ID mapping:\n got %#v\nwant %#v", got, want)
	}
	if got, want := createIssueCount, 2; got != want {
		t.Fatalf("expected rerun to reuse persisted marker without creating more subtasks, got %d creates want %d", got, want)
	}
	if got, want := len(markerCommentTexts), 1; got != want {
		t.Fatalf("expected rerun not to write another marker comment, got %d comments want %d", got, want)
	}
	if got, want := len(createLinkBodies), 1; got != want {
		t.Fatalf("expected rerun not to create another issue link, got %d links want %d", got, want)
	}
}

type fakeSplitMarkerTracker struct {
	issueIDs []string
	creates  []IssueCreateOptions
	links    []IssueLinkCreateOptions
	tasks    map[string]contracts.Task
}

func (f *fakeSplitMarkerTracker) GetTask(_ context.Context, taskID string) (*contracts.Task, error) {
	task, ok := f.tasks[taskID]
	if !ok {
		return &contracts.Task{ID: taskID, Metadata: map[string]string{}}, nil
	}
	metadata := make(map[string]string, len(task.Metadata))
	for key, value := range task.Metadata {
		metadata[key] = value
	}
	task.Metadata = metadata
	return &task, nil
}

func (f *fakeSplitMarkerTracker) SetTaskData(_ context.Context, taskID string, data map[string]string) error {
	task := f.tasks[taskID]
	task.ID = taskID
	task.Metadata = make(map[string]string, len(data))
	for key, value := range data {
		task.Metadata[key] = value
	}
	if f.tasks == nil {
		f.tasks = map[string]contracts.Task{}
	}
	f.tasks[taskID] = task
	return nil
}

func (f *fakeSplitMarkerTracker) CreateIssue(_ context.Context, opts IssueCreateOptions) (Issue, error) {
	opts.Labels = append([]string(nil), opts.Labels...)
	f.creates = append(f.creates, opts)

	issueID := f.issueIDs[len(f.creates)-1]
	return Issue{
		ID:          issueID,
		Title:       opts.Title,
		Description: opts.Description,
		Labels:      append([]string(nil), opts.Labels...),
		ParentID:    opts.ParentID,
	}, nil
}

func (f *fakeSplitMarkerTracker) CreateIssueLink(_ context.Context, opts IssueLinkCreateOptions) error {
	f.links = append(f.links, opts)
	return nil
}

func splitMarkerIssueIDs(issues []Issue) []string {
	ids := make([]string, 0, len(issues))
	for _, issue := range issues {
		ids = append(ids, issue.ID)
	}
	return ids
}

func startrekSplitServiceForHTTPClient(t *testing.T, httpClient HTTPClient) IdempotentSplitSubtaskCreationService {
	t.Helper()
	backend, err := NewStorageBackend(Config{
		Endpoint:   "https://api.tracker.yandex.net/v3",
		Token:      "tracker-token",
		HTTPClient: httpClient,
	})
	if err != nil {
		t.Fatalf("new storage backend: %v", err)
	}
	return IdempotentSplitSubtaskCreationService{
		Tracker:      backend,
		SplitVersion: "strict-v1",
	}
}

func startrekMarkerCommentsResponse(texts []string) []map[string]any {
	comments := make([]map[string]any, 0, len(texts))
	for i, text := range texts {
		comments = append(comments, map[string]any{
			"id":   i + 1,
			"text": text,
			"createdBy": map[string]string{
				"id":      "runner",
				"display": "YOLO Runner",
			},
			"createdAt": "2026-05-28T01:04:03.000+0000",
			"updatedAt": "2026-05-28T01:04:03.000+0000",
		})
	}
	return comments
}

func strconvQuote(value string) string {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(raw)
}
