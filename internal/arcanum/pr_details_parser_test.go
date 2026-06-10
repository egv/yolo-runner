package arcanum

import (
	"reflect"
	"testing"

	"github.com/egv/yolo-runner/v2/internal/arcreview"
)

func TestParsePRDetailsJSONMapsArcStatusFixture(t *testing.T) {
	fixture := []byte(`{
  "id":13843457,
  "url":"https://a.yandex-team.ru/review/13843457",
  "author":"genaevstratov",
  "summary":"fix swarm-generator: drop number bounds from FSM intent schema for anthropic output_config (YANGOSWARM-587)",
  "description":"Recorded fixture trimmed from arc pr status --json 13843457.",
  "status":"open",
  "issues":
    [
      "YANGOSWARM-587"
    ],
  "merge_allowed":true,
  "from_branch":"users/genaevstratov/YANGOSWARM-587-anthropic-intent-schema",
  "from_id":"b73ed5e7504e08ca499d020437f0bbb8582c39da",
  "to_branch":"trunk",
  "reviewers":
    [
      "aostrikov",
      "defendend",
      "zaxarello"
    ],
  "created_at":
    {
      "seconds":1781101617,
      "nanos":246088000
    }
}`)

	got, err := ParsePRDetailsJSON(fixture)
	if err != nil {
		t.Fatalf("ParsePRDetailsJSON() error = %v", err)
	}

	want := arcreview.PRDetails{
		ID:           "13843457",
		Title:        "fix swarm-generator: drop number bounds from FSM intent schema for anthropic output_config (YANGOSWARM-587)",
		Author:       "genaevstratov",
		Branch:       "trunk",
		SourceBranch: "users/genaevstratov/YANGOSWARM-587-anthropic-intent-schema",
		TargetBranch: "trunk",
		Status:       "open",
		Revision:     "b73ed5e7504e08ca499d020437f0bbb8582c39da",
		URL:          "https://a.yandex-team.ru/review/13843457",
		Description:  "Recorded fixture trimmed from arc pr status --json 13843457.",
		Issues: []arcreview.PRIssue{
			{ID: "YANGOSWARM-587", Status: "open", Message: "YANGOSWARM-587"},
		},
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParsePRDetailsJSON() = %#v, want %#v", got, want)
	}
}
