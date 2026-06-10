package arcanum

import (
	"reflect"
	"testing"

	"github.com/egv/yolo-runner/v2/internal/arcreview"
)

func TestParsePRCommentsJSONMapsArcCommentsFixture(t *testing.T) {
	fixture := []byte(`{
  "comments": [
    {
      "id": 501,
      "author": {"login": "alice"},
      "text": "Please explain why this retry is safe.",
      "resolved": false,
      "answered": false
    },
    {
      "id": "502",
      "created_by": "bob",
      "body": "Resolved after the null check was added.",
      "is_resolved": true
    },
    {
      "comment_id": "503",
      "author": "carol",
      "message": "Answered in the follow-up comment.",
      "is_answered": true
    }
  ]
}`)

	got, err := ParsePRCommentsJSON(fixture)
	if err != nil {
		t.Fatalf("ParsePRCommentsJSON() error = %v", err)
	}

	want := []arcreview.PRComment{
		{ID: "501", Author: "alice", Body: "Please explain why this retry is safe."},
		{ID: "502", Author: "bob", Body: "Resolved after the null check was added.", Resolved: true},
		{ID: "503", Author: "carol", Body: "Answered in the follow-up comment.", Answered: true},
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
