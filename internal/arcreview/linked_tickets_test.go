package arcreview

import (
	"context"
	"reflect"
	"strings"
	"testing"

	trackerstartrek "github.com/egv/yolo-runner/v2/internal/startrek"
)

func TestFetchLinkedTicketsSummariesFetchesPRIssuesAndSkipsEmptyInput(t *testing.T) {
	tracker := &fakeLinkedTicketTracker{
		issues: map[string]trackerstartrek.Issue{
			"YT-42": {
				ID:     "YT-42",
				Title:  "Build review prompt context",
				Status: "open",
				Description: strings.Join([]string{
					"Intent:",
					"Reviewers need the task intent before judging diffs.",
					"",
					"Acceptance Criteria:",
					"- Linked Startrek tickets are fetched from the PR issues field.",
					"- The prompt receives compact intent and AC context.",
					"",
					"Implementation notes:",
					"Do not wire the prompt in this task.",
				}, "\n"),
			},
			"ARC-9": {
				ID:          "ARC-9",
				Title:       "Handle sparse linked ticket descriptions",
				Status:      "closed",
				Description: "Keep the fetcher tolerant when tickets are small.",
			},
		},
	}

	got, err := FetchLinkedTicketSummaries(context.Background(), tracker, []PRIssue{
		{ID: " YT-42 "},
		{ID: ""},
		{ID: "ARC-9"},
	})
	if err != nil {
		t.Fatalf("FetchLinkedTicketSummaries() error = %v", err)
	}

	wantCalls := []string{"YT-42", "ARC-9"}
	if !reflect.DeepEqual(tracker.calls, wantCalls) {
		t.Fatalf("tracker calls = %#v, want %#v", tracker.calls, wantCalls)
	}

	want := []LinkedTicketSummary{
		{
			ID:                 "YT-42",
			Title:              "Build review prompt context",
			Status:             "open",
			Intent:             "Reviewers need the task intent before judging diffs.",
			AcceptanceCriteria: "- Linked Startrek tickets are fetched from the PR issues field.\n- The prompt receives compact intent and AC context.",
		},
		{
			ID:     "ARC-9",
			Title:  "Handle sparse linked ticket descriptions",
			Status: "closed",
			Intent: "Keep the fetcher tolerant when tickets are small.",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FetchLinkedTicketSummaries() = %#v, want %#v", got, want)
	}

	if got, err := FetchLinkedTicketSummaries(context.Background(), tracker, nil); err != nil {
		t.Fatalf("FetchLinkedTicketSummaries(nil) error = %v", err)
	} else if len(got) != 0 {
		t.Fatalf("FetchLinkedTicketSummaries(nil) returned %#v, want empty", got)
	}
	if !reflect.DeepEqual(tracker.calls, wantCalls) {
		t.Fatalf("empty input should not fetch; tracker calls = %#v, want %#v", tracker.calls, wantCalls)
	}
}

type fakeLinkedTicketTracker struct {
	issues map[string]trackerstartrek.Issue
	calls  []string
}

func (f *fakeLinkedTicketTracker) GetIssue(_ context.Context, issueID string) (trackerstartrek.Issue, error) {
	f.calls = append(f.calls, issueID)
	return f.issues[issueID], nil
}
