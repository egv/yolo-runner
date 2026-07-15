package arcanum

import (
	"reflect"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/arcreview"
)

func TestParsePRCommentsJSONMapsArcanumCommentsFixture(t *testing.T) {
	fixture := []byte(`{
  "data": [
    {
      "id": -3614781,
      "user": {"name": "reviewer"},
      "content": "Please explain why this retry is safe.",
      "issue_status": "open",
      "created_at": "2021-03-14T10:02:21.087070Z",
      "updated_at": "2021-03-14T10:03:46.848536Z",
      "anchor": {
        "review_request": {
          "id": 1683795,
          "diff": {
            "diff_set_xid": "29448811",
            "file": {
              "entry_id": {
                "content_id_after": {
                  "path": "internal/arcanum/pr_comments_parser.go",
                  "commit_id": "b73ed5e7504e08ca499d020437f0bbb8582c39da"
                }
              },
              "position": {"side": "new", "line": 27, "size": 1}
            }
          }
        }
      }
    },
    {
      "id": -3614780,
      "user": {"name": "maintainer"},
      "content": "Resolved after the null check was added.",
      "issue_status": "resolved",
      "created_at": "2021-03-14T10:03:29.622807Z",
      "updated_at": "2021-03-14T10:03:39.184798Z"
    },
    {
      "id": -3614779,
      "user": {"name": "reviewer"},
      "content": "FYI only.",
      "issue_status": "not_issue",
      "created_at": "2021-03-14T10:04:00Z",
      "updated_at": "2021-03-14T10:04:00Z"
    },
    {
      "id": -3614778,
      "user": {"name": "author"},
      "content": "Answered in the follow-up comment.",
      "reply_to_id": -3614781,
      "created_at": "2021-03-14T10:05:00Z",
      "updated_at": "2021-03-14T10:05:00Z"
    },
    {
      "id": -3614777,
      "user": {"name": "reviewer"},
      "content": "Please make the terminology consistent.",
      "created_at": "2021-03-14T10:06:00Z",
      "updated_at": "2021-03-14T10:06:00Z"
    }
  ]
}`)

	got, err := ParsePRCommentsJSON(fixture)
	if err != nil {
		t.Fatalf("ParsePRCommentsJSON() error = %v", err)
	}

	want := []arcreview.PRComment{
		{
			ID:          "-3614781",
			Author:      "reviewer",
			Body:        "Please explain why this retry is safe.",
			IssueStatus: "open",
			Path:        "internal/arcanum/pr_comments_parser.go",
			Line:        27,
			Revision:    "29448811",
			CreatedAt:   mustParsePRCommentTestTime(t, "2021-03-14T10:02:21.087070Z"),
			UpdatedAt:   mustParsePRCommentTestTime(t, "2021-03-14T10:03:46.848536Z"),
		},
		{
			ID:          "-3614780",
			Author:      "maintainer",
			Body:        "Resolved after the null check was added.",
			CreatedAt:   mustParsePRCommentTestTime(t, "2021-03-14T10:03:29.622807Z"),
			UpdatedAt:   mustParsePRCommentTestTime(t, "2021-03-14T10:03:39.184798Z"),
			Resolved:    true,
			IssueStatus: "resolved",
		},
		{
			ID:          "-3614779",
			Author:      "reviewer",
			Body:        "FYI only.",
			CreatedAt:   mustParsePRCommentTestTime(t, "2021-03-14T10:04:00Z"),
			UpdatedAt:   mustParsePRCommentTestTime(t, "2021-03-14T10:04:00Z"),
			Answered:    true,
			IssueStatus: "not_issue",
		},
		{
			ID:        "-3614778",
			ThreadID:  "-3614781",
			Author:    "author",
			Body:      "Answered in the follow-up comment.",
			CreatedAt: mustParsePRCommentTestTime(t, "2021-03-14T10:05:00Z"),
			UpdatedAt: mustParsePRCommentTestTime(t, "2021-03-14T10:05:00Z"),
			Answered:  true,
		},
		{
			ID:        "-3614777",
			Author:    "reviewer",
			Body:      "Please make the terminology consistent.",
			CreatedAt: mustParsePRCommentTestTime(t, "2021-03-14T10:06:00Z"),
			UpdatedAt: mustParsePRCommentTestTime(t, "2021-03-14T10:06:00Z"),
		},
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParsePRCommentsJSON() = %#v, want %#v", got, want)
	}

	state := arcreview.PRRuntimeState{
		Revision: "r1",
		Details:  arcreview.PRDetails{Status: "open", Revision: "r1"},
		Comments: got,
		Checks:   []arcreview.PRCheck{{Name: "ci", Status: "passed"}},
	}
	if action := arcreview.PlanNextPRRunnerAction(state, "r1", true); action != arcreview.PRRunnerActionAnswer {
		t.Fatalf("PlanNextPRRunnerAction() = %q, want %q", action, arcreview.PRRunnerActionAnswer)
	}
}

func mustParsePRCommentTestTime(t *testing.T, value string) time.Time {
	t.Helper()

	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		t.Fatalf("parse test time %q: %v", value, err)
	}
	return parsed
}
