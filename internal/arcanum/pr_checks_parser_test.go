package arcanum

import (
	"reflect"
	"testing"

	"github.com/egv/yolo-runner/v2/internal/arcreview"
)

func TestParsePRChecksJSONMapsArcStatusFixture(t *testing.T) {
	fixture := []byte(`{
  "id": 13843457,
  "checks": [
    {
      "name": "required-reviewers",
      "status": "SUCCESS",
      "description": "All required reviewers approved."
    },
    {
      "name": "large-tests",
      "status": "PENDING",
      "description": "Large tests are still running."
    },
    {
      "name": "ya-make",
      "status": "FAILED",
      "description": "ya make failed on linux-x86_64."
    }
  ]
}`)

	got, err := ParsePRChecksJSON(fixture)
	if err != nil {
		t.Fatalf("ParsePRChecksJSON() error = %v", err)
	}

	want := []arcreview.PRCheck{
		{Name: "required-reviewers", Status: "success", Summary: "All required reviewers approved."},
		{Name: "large-tests", Status: "pending", Summary: "Large tests are still running."},
		{Name: "ya-make", Status: "failed", Summary: "ya make failed on linux-x86_64."},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParsePRChecksJSON() = %#v, want %#v", got, want)
	}

	state := arcreview.PRRuntimeState{
		Revision: "r1",
		Details:  arcreview.PRDetails{ID: "13843457", Status: "open", Revision: "r1"},
		Checks:   []arcreview.PRCheck{got[0]},
	}
	if action := arcreview.PlanNextPRRunnerAction(state, "r1", true); action != arcreview.PRRunnerActionShip {
		t.Fatalf("success check action = %q, want %q", action, arcreview.PRRunnerActionShip)
	}

	state.Checks = []arcreview.PRCheck{got[1]}
	if action := arcreview.PlanNextPRRunnerAction(state, "r1", true); action != arcreview.PRRunnerActionWait {
		t.Fatalf("pending check action = %q, want %q", action, arcreview.PRRunnerActionWait)
	}

	state.Checks = []arcreview.PRCheck{got[2]}
	if action := arcreview.PlanNextPRRunnerAction(state, "r1", true); action != arcreview.PRRunnerActionReview {
		t.Fatalf("failed check action = %q, want %q", action, arcreview.PRRunnerActionReview)
	}
}
