package startrek

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/agent/splitter"
)

func TestSplitSubtaskCreationServiceCreatesTrackerSubtasksWithBodiesLabelsAndLinks(t *testing.T) {
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
	if got, want := first.Labels, []string{"yolo-agent-ready"}; !reflect.DeepEqual(got, want) {
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
	if got, want := second.Labels, []string{"yolo-agent-ready"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected second labels:\n got %#v\nwant %#v", got, want)
	}
	if got, want := tracker.links, []IssueLinkCreateOptions{
		{IssueID: "VAY-44", Relationship: "depends_on", RelatedIssueID: "VAY-43"},
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected dependency links:\n got %#v\nwant %#v", got, want)
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

func TestSplitSubtaskCreationServiceEmbedsEpicContextInEverySubtaskBody(t *testing.T) {
	tracker := &fakeSplitSubtaskTracker{
		issueIDs: []string{"VAY-43", "VAY-44"},
	}
	service := SplitSubtaskCreationService{Tracker: tracker}

	_, err := service.Create(context.Background(), SplitSubtasksInput{
		QueueKey:          "VAY",
		ParentID:          "VAY-42",
		ParentTitle:       "Split context epic",
		ParentDescription: "Define shared context for generated subtasks. See [design spec](https://docs.example.com/split-context) before editing.",
		Output: splitter.StrictOutput{
			Tasks: []splitter.Task{
				{
					ID:            "T20",
					Title:         "Define context model",
					Why:           []string{"Generated subtasks need a reusable context model."},
					InScope:       []string{"Extract epic context from the parent issue."},
					OutOfScope:    []string{"Tracker API changes."},
					StrictTDD:     []string{"Add targeted test", "Confirm it fails", "Make it pass"},
					DoneWhen:      []string{"Context model is covered."},
					ExpectedFiles: []string{"internal/startrek/split_subtasks.go"},
					Unlocks:       []string{"T21"},
				},
				{
					ID:            "T21",
					Title:         "Write context into subtask bodies",
					Why:           []string{"Generated subtasks must be self-contained."},
					InScope:       []string{"Render epic context before task sections."},
					OutOfScope:    []string{"Re-splitting existing issues."},
					StrictTDD:     []string{"Add targeted test", "Confirm it fails", "Make it pass"},
					DoneWhen:      []string{"Every generated body includes context."},
					ExpectedFiles: []string{"internal/startrek/split_subtasks_test.go"},
					DependsOn:     []string{"T20"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if len(tracker.creates) != 2 {
		t.Fatalf("expected 2 created subtasks, got %d", len(tracker.creates))
	}

	for _, create := range tracker.creates {
		for _, want := range []string{
			"Context:\n",
			"- Epic summary: VAY-42 Split context epic - Define shared context for generated subtasks.",
			"- Doc: https://docs.example.com/split-context",
		} {
			if !strings.Contains(create.Description, want) {
				t.Fatalf("expected body for %q to contain %q, got:\n%s", create.Title, want, create.Description)
			}
		}
	}

	if want := "- Artifact producer: T21 Write context into subtask bodies -> internal/startrek/split_subtasks_test.go"; !strings.Contains(tracker.creates[0].Description, want) {
		t.Fatalf("expected first body to point at sibling artifact producer %q, got:\n%s", want, tracker.creates[0].Description)
	}
	if want := "- Artifact producer: T20 Define context model -> internal/startrek/split_subtasks.go"; !strings.Contains(tracker.creates[1].Description, want) {
		t.Fatalf("expected second body to point at dependency artifact producer %q, got:\n%s", want, tracker.creates[1].Description)
	}
}

func TestSplitSubtaskCreationServiceEmbedsContextFromMappedStartrekDescription(t *testing.T) {
	tracker := &fakeSplitSubtaskTracker{
		issueIDs: []string{"VAY-43", "VAY-44"},
	}
	service := SplitSubtaskCreationService{Tracker: tracker}
	parentTask := MapIssueToTask(Issue{
		ID:          "VAY-42",
		Title:       "Split mapped context epic",
		Description: "Define mapped shared context. See [design spec](https://docs.example.com/mapped-context) before editing.",
		Labels:      []string{"yolo-agent-ready"},
		Author:      IssueAuthor{ID: "112233", Display: "Ada Lovelace"},
	}, []IssueComment{
		{
			ID:        "comment-1",
			Body:      "Comment-only link https://docs.example.com/comment-context must not become epic description context.",
			Author:    IssueAuthor{ID: "445566", Display: "Grace Hopper"},
			CreatedAt: time.Date(2026, 5, 28, 1, 2, 3, 0, time.UTC),
		},
	}, TaskMappingOptions{
		QueueKey: "VAY",
		RootID:   "VAY-1",
	})

	_, err := service.Create(context.Background(), SplitSubtasksInput{
		QueueKey:          "VAY",
		ParentID:          parentTask.ID,
		ParentTitle:       parentTask.Title,
		ParentDescription: parentTask.Description,
		Output: splitter.StrictOutput{
			Tasks: []splitter.Task{
				{
					ID:            "T20",
					Title:         "Extract context block",
					ExpectedFiles: []string{"internal/startrek/split_subtasks.go"},
					Unlocks:       []string{"T21"},
				},
				{
					ID:            "T21",
					Title:         "Render context block",
					ExpectedFiles: []string{"internal/startrek/split_subtasks_test.go"},
					DependsOn:     []string{"T20"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	for _, create := range tracker.creates {
		for _, want := range []string{
			"- Epic summary: VAY-42 Split mapped context epic - Define mapped shared context.",
			"- Doc: https://docs.example.com/mapped-context",
		} {
			if !strings.Contains(create.Description, want) {
				t.Fatalf("expected body for %q to contain %q, got:\n%s", create.Title, want, create.Description)
			}
		}
		if unwanted := "https://docs.example.com/comment-context"; strings.Contains(create.Description, unwanted) {
			t.Fatalf("expected body for %q to omit comment-only doc link %q, got:\n%s", create.Title, unwanted, create.Description)
		}
	}
}

type fakeSplitSubtaskTracker struct {
	issueIDs []string
	creates  []IssueCreateOptions
	links    []IssueLinkCreateOptions
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

func (f *fakeSplitSubtaskTracker) CreateIssueLink(_ context.Context, opts IssueLinkCreateOptions) error {
	f.links = append(f.links, opts)
	return nil
}
