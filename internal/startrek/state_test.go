package startrek

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
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
		"transition VAY-42 blocked",
		"comment VAY-42",
		"data VAY-42",
	}
	if !reflect.DeepEqual(tracker.ops, wantOps) {
		t.Fatalf("unexpected operations:\n got %#v\nwant %#v", tracker.ops, wantOps)
	}

	wantBody := strings.Join([]string{
		"Needs more information before yolo-runner can run this task.",
		englishNeedsInfoProxyNotice,
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
		russianNeedsInfoProxyNotice,
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

func TestNeedsInfoTransitionServiceDeduplicatesLatestMatchingQuestions(t *testing.T) {
	tracker := &fakeNeedsInfoTransitionTracker{}
	service := NeedsInfoTransitionService{Tracker: tracker}

	_, err := service.Apply(context.Background(), NeedsInfoTransitionInput{
		IssueID: "ADAPTABOT-5",
		Summary: "Ownership is unclear.",
		Questions: []string{
			"Which package owns this behavior?",
			"Who should answer follow-up questions?",
		},
		SummoneeID: "author-1",
	})
	if err != nil {
		t.Fatalf("first Apply returned error: %v", err)
	}
	if got := countNeedsInfoMarkerComments(tracker.comments); got != 1 {
		t.Fatalf("expected first apply to post one needs-info comment, got %d", got)
	}

	_, err = service.Apply(context.Background(), NeedsInfoTransitionInput{
		IssueID: "ADAPTABOT-5",
		Summary: "Ownership is still unclear.",
		Questions: []string{
			" 1. which   package owns this behavior? ",
			"2) WHO should answer follow-up questions?",
		},
		SummoneeID: "author-1",
	})
	if err != nil {
		t.Fatalf("second Apply returned error: %v", err)
	}
	if got := countNeedsInfoMarkerComments(tracker.comments); got != 1 {
		t.Fatalf("expected duplicate questions to reuse the latest marker comment, got %d comments", got)
	}

	_, err = service.Apply(context.Background(), NeedsInfoTransitionInput{
		IssueID: "ADAPTABOT-5",
		Summary: "Ownership is still unclear.",
		Questions: []string{
			"Which package owns this behavior?",
			"Who should answer follow-up questions?",
			"What deadline applies?",
		},
		SummoneeID: "author-1",
	})
	if err != nil {
		t.Fatalf("third Apply returned error: %v", err)
	}
	if got := countNeedsInfoMarkerComments(tracker.comments); got != 2 {
		t.Fatalf("expected changed questions to post a second needs-info comment, got %d", got)
	}
}

type fakeNeedsInfoTransitionTracker struct {
	ops            []string
	commentOptions IssueCommentCreateOptions
	comment        IssueComment
	comments       []IssueComment
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
	comment := f.comment
	if strings.TrimSpace(comment.ID) == "" {
		comment.ID = fmt.Sprintf("comment-%d", len(f.comments)+1)
	}
	if comment.CreatedAt.IsZero() {
		comment.CreatedAt = time.Date(2026, 5, 28, 12, 0, len(f.comments)+1, 0, time.UTC)
	}
	body, _, err := issueCommentCreateText(opts.Body, opts.Marker)
	if err != nil {
		return IssueComment{}, err
	}
	comment.Body = body
	f.comments = append(f.comments, comment)
	return comment, nil
}

func (f *fakeNeedsInfoTransitionTracker) SetTaskData(_ context.Context, taskID string, data map[string]string) error {
	f.ops = append(f.ops, "data "+taskID)
	f.data = data
	return nil
}

func (f *fakeNeedsInfoTransitionTracker) SetTaskStatus(_ context.Context, taskID string, status contracts.TaskStatus) error {
	f.ops = append(f.ops, "transition "+taskID+" "+string(status))
	return nil
}

func (f *fakeNeedsInfoTransitionTracker) GetIssueComments(_ context.Context, _ string) ([]IssueComment, error) {
	return append([]IssueComment(nil), f.comments...), nil
}

func countNeedsInfoMarkerComments(comments []IssueComment) int {
	count := 0
	for _, comment := range comments {
		if strings.Contains(comment.Body, "<!-- yolo-runner:needs-info -->") {
			count++
		}
	}
	return count
}
