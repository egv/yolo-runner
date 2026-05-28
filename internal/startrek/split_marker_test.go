package startrek

import (
	"context"
	"reflect"
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

type fakeSplitMarkerTracker struct {
	issueIDs []string
	creates  []IssueCreateOptions
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

func splitMarkerIssueIDs(issues []Issue) []string {
	ids := make([]string, 0, len(issues))
	for _, issue := range issues {
		ids = append(ids, issue.ID)
	}
	return ids
}
