package arcanum

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/egv/yolo-runner/v2/internal/arcreview"
)

func TestFetchPRRuntimeStateRunsArcCommandsAndNormalizesState(t *testing.T) {
	oldExec := arcExec
	t.Cleanup(func() {
		arcExec = oldExec
	})

	detailsFixture := []byte(`{
  "id":13843457,
  "url":"https://a.yandex-team.ru/review/13843457",
  "author":"alice",
  "summary":"Compose PR runtime state fetcher",
  "description":"Fetch details, comments, diff, and checks.",
  "status":"open",
  "issues":[
    {"id":"ARW2-10","status":"open","message":"state fetcher missing"},
    {"id":"ARW2-09","status":"resolved","message":"checks parser done"}
  ],
  "checks": [
    {
      "name": "required-reviewers",
      "status": "SUCCESS",
      "description": "All required reviewers approved."
    }
  ],
  "from_branch":"users/alice/arw2-10",
  "from_id":"rev-42",
  "to_branch":"trunk"
}`)
	commentsFixture := []byte(`{
  "data": [
    {
      "id": -3614781,
      "user": {"name": "reviewer"},
      "content": "Please include checks in the state.",
      "issue_status": "open",
      "created_at": "2021-03-14T10:02:21.087070Z",
      "updated_at": "2021-03-14T10:03:46.848536Z",
      "anchor": {
        "review_request": {
          "diff": {
            "diff_set_xid": "rev-42",
            "file": {
              "path": "internal/arcanum/pr_state_fetcher.go",
              "position": {"line": 27}
            }
          }
        }
      }
    }
  ]
}`)
	diffFixture := []byte(`diff --git a/internal/arcanum/pr_state_fetcher.go b/internal/arcanum/pr_state_fetcher.go
new file mode 100644
index 0000000..1111111
--- /dev/null
+++ b/internal/arcanum/pr_state_fetcher.go
@@ -0,0 +1,5 @@
+package arcanum
+
+func FetchPRRuntimeState() {}
`)

	ctx := context.WithValue(context.Background(), contextKey("state"), "value")
	var gotCtx []context.Context
	var gotWorkspace []string
	var gotName []string
	var gotArgs [][]string

	fixtures := map[string][]byte{
		"arc pr status --json 13843457": detailsFixture,
		"curl -fsSL https://a.yandex-team.ru/api/v1/public/review-requests/13843457/comments": commentsFixture,
		"arc pr changes 13843457": diffFixture,
	}

	arcExec = func(ctx context.Context, workspace string, name string, args ...string) ([]byte, []byte, error) {
		if name == "arc" && len(args) >= 2 && args[0] == "pr" && (args[1] == "comments" || args[1] == "checks") {
			return nil, []byte("unsupported arc pr mode"), fmt.Errorf("unsupported arc pr mode: %s", args[1])
		}

		gotCtx = append(gotCtx, ctx)
		gotWorkspace = append(gotWorkspace, workspace)
		gotName = append(gotName, name)
		gotArgs = append(gotArgs, append([]string{}, args...))

		key := strings.Join(append([]string{name}, args...), " ")
		fixture, ok := fixtures[key]
		if !ok {
			return nil, []byte("unexpected args"), fmt.Errorf("unexpected args: %s", key)
		}
		return fixture, nil, nil
	}

	got, err := FetchPRRuntimeState(ctx, "/arcadia/workspace", "13843457")
	if err != nil {
		t.Fatalf("FetchPRRuntimeState() error = %v", err)
	}

	for i, got := range gotCtx {
		if got != ctx {
			t.Fatalf("FetchPRRuntimeState() call %d did not pass through context", i)
		}
	}
	if !reflect.DeepEqual(gotWorkspace, []string{"/arcadia/workspace", "/arcadia/workspace", "/arcadia/workspace"}) {
		t.Fatalf("FetchPRRuntimeState() workspaces = %#v", gotWorkspace)
	}
	if !reflect.DeepEqual(gotName, []string{"arc", "curl", "arc"}) {
		t.Fatalf("FetchPRRuntimeState() commands = %#v", gotName)
	}
	wantArgs := [][]string{
		{"pr", "status", "--json", "13843457"},
		{"-fsSL", "https://a.yandex-team.ru/api/v1/public/review-requests/13843457/comments"},
		{"pr", "changes", "13843457"},
	}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("FetchPRRuntimeState() args = %#v, want %#v", gotArgs, wantArgs)
	}

	want := arcreview.PRRuntimeState{
		PRID:     "13843457",
		Revision: "rev-42",
		Details: arcreview.PRDetails{
			ID:           "13843457",
			Title:        "Compose PR runtime state fetcher",
			Author:       "alice",
			Branch:       "trunk",
			SourceBranch: "users/alice/arw2-10",
			TargetBranch: "trunk",
			Status:       "open",
			Revision:     "rev-42",
			URL:          "https://a.yandex-team.ru/review/13843457",
			Description:  "Fetch details, comments, diff, and checks.",
			Issues: []arcreview.PRIssue{
				{ID: "ARW2-10", Status: "open", Message: "state fetcher missing"},
				{ID: "ARW2-09", Status: "resolved", Message: "checks parser done"},
			},
		},
		Comments: []arcreview.PRComment{
			{
				ID:        "-3614781",
				Author:    "reviewer",
				Body:      "Please include checks in the state.",
				Path:      "internal/arcanum/pr_state_fetcher.go",
				Line:      27,
				Revision:  "rev-42",
				CreatedAt: mustParsePRCommentTestTime(t, "2021-03-14T10:02:21.087070Z"),
				UpdatedAt: mustParsePRCommentTestTime(t, "2021-03-14T10:03:46.848536Z"),
			},
		},
		OpenIssues: []arcreview.PRIssue{
			{ID: "ARW2-10", Status: "open", Message: "state fetcher missing"},
		},
		ChangedFiles: []arcreview.PRChangedFile{
			{
				Path: "internal/arcanum/pr_state_fetcher.go",
				Diff: string(diffFixture),
			},
		},
		Checks: []arcreview.PRCheck{
			{Name: "required-reviewers", Status: "success", Summary: "All required reviewers approved."},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FetchPRRuntimeState() = %#v, want %#v", got, want)
	}
}
