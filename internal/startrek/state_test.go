package startrek

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestNeedsInfoTransitionServiceAppliesLabelsCommentAndMarkerData(t *testing.T) {
	createdAt := time.Date(2026, 5, 28, 12, 34, 56, 789000000, time.UTC)
	tracker := &fakeNeedsInfoTransitionTracker{
		comment: IssueComment{
			ID:        "comment-626",
			CreatedAt: createdAt,
		},
	}
	service := NeedsInfoTransitionService{Tracker: tracker}

	result, err := service.Apply(context.Background(), NeedsInfoTransitionInput{
		IssueID:    " VAY-42 ",
		Summary:    " Ownership is unclear. ",
		Questions:  []string{" Which package owns the behavior? ", "", "Should existing labels be preserved?"},
		SummoneeID: " author-1 ",
	})
	if err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}

	wantOps := []string{
		"remove VAY-42 processing",
		"add VAY-42 needs-info",
		"comment VAY-42",
		"data VAY-42",
	}
	if !reflect.DeepEqual(tracker.ops, wantOps) {
		t.Fatalf("unexpected operations:\n got %#v\nwant %#v", tracker.ops, wantOps)
	}

	wantBody := strings.Join([]string{
		"Needs more information before yolo-runner can run this task.",
		"",
		"Summary:",
		"Ownership is unclear.",
		"",
		"Questions:",
		"1. Which package owns the behavior?",
		"2. Should existing labels be preserved?",
	}, "\n")
	if tracker.commentOptions.Body != wantBody {
		t.Fatalf("unexpected comment body:\n%s", tracker.commentOptions.Body)
	}
	if tracker.commentOptions.Marker != "needs-info" {
		t.Fatalf("expected needs-info marker, got %q", tracker.commentOptions.Marker)
	}
	if tracker.commentOptions.AuthorID != "author-1" {
		t.Fatalf("expected trimmed summonee author ID, got %q", tracker.commentOptions.AuthorID)
	}

	wantMarkerData := map[string]string{
		"needs_info_marker":            "needs-info",
		"needs_info_marker_comment_id": "comment-626",
		"needs_info_marker_created_at": "2026-05-28T12:34:56.789Z",
	}
	if !reflect.DeepEqual(tracker.data, wantMarkerData) {
		t.Fatalf("unexpected marker data:\n got %#v\nwant %#v", tracker.data, wantMarkerData)
	}
	if !reflect.DeepEqual(result.MarkerData, wantMarkerData) {
		t.Fatalf("unexpected result marker data:\n got %#v\nwant %#v", result.MarkerData, wantMarkerData)
	}
}

func TestNeedsInfoTransitionServiceUsesRussianBodyForRussianQuestions(t *testing.T) {
	tracker := &fakeNeedsInfoTransitionTracker{
		comment: IssueComment{ID: "comment-ru"},
	}
	service := NeedsInfoTransitionService{Tracker: tracker}

	_, err := service.Apply(context.Background(), NeedsInfoTransitionInput{
		IssueID:   "ADAPTABOT-1",
		Summary:   "Не указан секрет с токеном Messenger.",
		Questions: []string{"В каком секрете и под каким ключом хранится токен?"},
	})
	if err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}

	wantBody := strings.Join([]string{
		"Перед запуском yolo-runner нужно уточнить детали.",
		"",
		"Кратко:",
		"Не указан секрет с токеном Messenger.",
		"",
		"Вопросы:",
		"1. В каком секрете и под каким ключом хранится токен?",
	}, "\n")
	if tracker.commentOptions.Body != wantBody {
		t.Fatalf("unexpected Russian comment body:\n%s", tracker.commentOptions.Body)
	}
}

type fakeNeedsInfoTransitionTracker struct {
	ops            []string
	commentOptions IssueCommentCreateOptions
	comment        IssueComment
	data           map[string]string
}

func (f *fakeNeedsInfoTransitionTracker) RemoveLabel(_ context.Context, issueID string, label string) error {
	f.ops = append(f.ops, "remove "+issueID+" "+label)
	return nil
}

func (f *fakeNeedsInfoTransitionTracker) AddLabel(_ context.Context, issueID string, label string) error {
	f.ops = append(f.ops, "add "+issueID+" "+label)
	return nil
}

func (f *fakeNeedsInfoTransitionTracker) CreateIssueComment(_ context.Context, issueID string, opts IssueCommentCreateOptions) (IssueComment, error) {
	f.ops = append(f.ops, "comment "+issueID)
	f.commentOptions = opts
	return f.comment, nil
}

func (f *fakeNeedsInfoTransitionTracker) SetTaskData(_ context.Context, taskID string, data map[string]string) error {
	f.ops = append(f.ops, "data "+taskID)
	f.data = data
	return nil
}
