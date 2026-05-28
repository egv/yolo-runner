package startrek

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/egv/yolo-runner/v2/internal/agent/splitter"
)

func TestSplitSubtaskCreationServiceCreatesTrackerSubtasksWithBodiesAndLabels(t *testing.T) {
	tracker := &fakeSplitSubtaskTracker{
		issueIDs: []string{"VAY-43", "VAY-44"},
	}
	service := SplitSubtaskCreationService{Tracker: tracker}

	result, err := service.Create(context.Background(), SplitSubtasksInput{
		QueueKey: " VAY ",
		ParentID: " VAY-42 ",
		Output: splitter.StrictOutput{
			Tasks: []splitter.Task{
				{
					ID:            "T20",
					Title:         "Invoke strict splitter",
					Why:           []string{"Call the strict splitter prompt."},
					InScope:       []string{"Implement the strict invocation seam."},
					OutOfScope:    []string{"Tracker task creation."},
					StrictTDD:     []string{"Add targeted test", "Confirm it fails", "Make it pass"},
					DoneWhen:      []string{"Targeted test passes."},
					ExpectedFiles: []string{"internal/agent/splitter/runner.go"},
					Unlocks:       []string{"T21"},
				},
				{
					ID:            "T21",
					Title:         "Parse strict splitter output",
					Why:           []string{"Generated subtasks need structure."},
					InScope:       []string{"Parse strict task sections."},
					OutOfScope:    []string{"Tracker task creation."},
					StrictTDD:     []string{"Add parser test", "Confirm it fails", "Make it pass"},
					DoneWhen:      []string{"Parser test passes."},
					ExpectedFiles: []string{"internal/agent/splitter/parser.go"},
					DependsOn:     []string{"T20"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	if got, want := result.IssueIDsBySplitTaskID, map[string]string{"T20": "VAY-43", "T21": "VAY-44"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected split task ID mapping:\n got %#v\nwant %#v", got, want)
	}
	if len(tracker.creates) != 2 {
		t.Fatalf("expected 2 created subtasks, got %d", len(tracker.creates))
	}

	first := tracker.creates[0]
	if first.QueueKey != "VAY" || first.ParentID != "VAY-42" || first.Title != "T20 Invoke strict splitter" {
		t.Fatalf("unexpected first create options: %#v", first)
	}
	if got, want := first.Labels, []string{"yolo-agent-ready", "agent:subtask"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected first labels:\n got %#v\nwant %#v", got, want)
	}
	for _, want := range []string{
		"### Task: T20 Invoke strict splitter",
		"Why:\n- Call the strict splitter prompt.",
		"In scope:\n- Implement the strict invocation seam.",
		"Out of scope:\n- Tracker task creation.",
		"Strict TDD:\n1. Add targeted test\n2. Confirm it fails\n3. Make it pass",
		"Done when:\n- Targeted test passes.",
		"Expected files:\n- internal/agent/splitter/runner.go",
		"Depends on:\n- none",
		"Unlocks:\n- T21",
	} {
		if !strings.Contains(first.Description, want) {
			t.Fatalf("expected first body to contain %q, got:\n%s", want, first.Description)
		}
	}

	second := tracker.creates[1]
	if second.QueueKey != "VAY" || second.ParentID != "VAY-42" || second.Title != "T21 Parse strict splitter output" {
		t.Fatalf("unexpected second create options: %#v", second)
	}
	if got, want := second.Labels, []string{"yolo-agent-ready", "agent:subtask", "depends-on:VAY-43"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected second labels:\n got %#v\nwant %#v", got, want)
	}
	for _, want := range []string{
		"### Task: T21 Parse strict splitter output",
		"Why:\n- Generated subtasks need structure.",
		"In scope:\n- Parse strict task sections.",
		"Out of scope:\n- Tracker task creation.",
		"Strict TDD:\n1. Add parser test\n2. Confirm it fails\n3. Make it pass",
		"Done when:\n- Parser test passes.",
		"Expected files:\n- internal/agent/splitter/parser.go",
		"Depends on:\n- T20",
		"Unlocks:\n- none",
	} {
		if !strings.Contains(second.Description, want) {
			t.Fatalf("expected second body to contain %q, got:\n%s", want, second.Description)
		}
	}
}

type fakeSplitSubtaskTracker struct {
	issueIDs []string
	creates  []IssueCreateOptions
}

func (f *fakeSplitSubtaskTracker) CreateIssue(_ context.Context, opts IssueCreateOptions) (Issue, error) {
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
