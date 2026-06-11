package startrek

import (
	"testing"
	"time"
)

func TestShouldResumeNeedsInfoWait(t *testing.T) {
	markerAt := time.Date(2026, 5, 28, 10, 0, 0, 0, time.UTC)
	wait := NeedsInfoWaitState{
		MarkerCreatedAt: markerAt,
		Author:          IssueAuthor{ID: "author-1", Display: "Author"},
		Assignee:        IssueAuthor{ID: "assignee-1", Display: "Assignee"},
		AgentAuthorIDs:  []string{"agent-1"},
	}

	for _, tc := range []struct {
		name     string
		comments []IssueComment
		want     bool
	}{
		{
			name: "no reply",
			comments: []IssueComment{
				{
					ID:        "old-author",
					Body:      "Earlier context.",
					Author:    IssueAuthor{ID: "author-1"},
					CreatedAt: markerAt.Add(-time.Minute),
				},
			},
			want: false,
		},
		{
			name: "agent-only reply",
			comments: []IssueComment{
				{
					ID:        "agent-after-marker",
					Body:      "Still waiting for input.",
					Author:    IssueAuthor{ID: "agent-1"},
					CreatedAt: markerAt.Add(time.Minute),
				},
			},
			want: false,
		},
		{
			name: "author marker reply",
			comments: []IssueComment{
				{
					ID:        "author-marker-after-marker",
					Body:      "<!-- yolo-runner:needs-info -->\n\nStill waiting for input.",
					Author:    IssueAuthor{ID: "author-1"},
					CreatedAt: markerAt.Add(time.Minute),
				},
			},
			want: false,
		},
		{
			name: "author reply",
			comments: []IssueComment{
				{
					ID:        "author-after-marker",
					Body:      "Here is the requested detail.",
					Author:    IssueAuthor{ID: "author-1"},
					CreatedAt: markerAt.Add(time.Minute),
				},
			},
			want: true,
		},
		{
			name: "assignee reply",
			comments: []IssueComment{
				{
					ID:        "assignee-after-marker",
					Body:      "I can answer this.",
					Author:    IssueAuthor{ID: "assignee-1"},
					CreatedAt: markerAt.Add(time.Minute),
				},
			},
			want: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ShouldResumeNeedsInfoWait(wait, tc.comments)
			if got != tc.want {
				t.Fatalf("expected resume=%t, got %t", tc.want, got)
			}
		})
	}
}
