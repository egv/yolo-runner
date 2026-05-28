package startrek

import (
	"context"
	"strings"
	"testing"
)

func TestProgressCommentsPostStructuredMarkers(t *testing.T) {
	tracker := &fakeProgressCommentTracker{}

	if err := PostSplitCreatedComment(context.Background(), tracker, " VAY-42 ", []string{" VAY-43 ", "VAY-44"}); err != nil {
		t.Fatalf("post split-created comment: %v", err)
	}
	if err := PostFailureComment(context.Background(), tracker, "VAY-43", "tests failed"); err != nil {
		t.Fatalf("post failure comment: %v", err)
	}
	if err := PostParentPRCreatedComment(context.Background(), tracker, "VAY-42", " https://arc.example.test/review/123 ", []string{"VAY-43", "VAY-44"}); err != nil {
		t.Fatalf("post parent PR-created comment: %v", err)
	}

	if got, want := len(tracker.comments), 3; got != want {
		t.Fatalf("expected %d comments, got %d", want, got)
	}

	assertProgressComment(t, tracker.comments[0], "VAY-42", "split-created", []string{"VAY-43", "VAY-44"})
	assertProgressComment(t, tracker.comments[1], "VAY-43", "failure", []string{"tests failed"})
	assertProgressComment(t, tracker.comments[2], "VAY-42", "parent-pr-created", []string{"https://arc.example.test/review/123", "VAY-43", "VAY-44"})
}

type fakeProgressCommentTracker struct {
	comments []progressCommentCall
}

type progressCommentCall struct {
	issueID string
	opts    IssueCommentCreateOptions
}

func (f *fakeProgressCommentTracker) CreateIssueComment(_ context.Context, issueID string, opts IssueCommentCreateOptions) (IssueComment, error) {
	f.comments = append(f.comments, progressCommentCall{issueID: issueID, opts: opts})
	return IssueComment{ID: "comment-1", Body: opts.Body}, nil
}

func assertProgressComment(t *testing.T, call progressCommentCall, issueID string, marker string, wants []string) {
	t.Helper()
	if call.issueID != issueID {
		t.Fatalf("expected issue ID %q, got %q", issueID, call.issueID)
	}
	if call.opts.Marker != marker {
		t.Fatalf("expected marker %q, got %q", marker, call.opts.Marker)
	}
	for _, want := range wants {
		if !strings.Contains(call.opts.Body, want) {
			t.Fatalf("expected comment body to contain %q, got:\n%s", want, call.opts.Body)
		}
	}
}
