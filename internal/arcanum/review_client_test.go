package arcanum

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/egv/yolo-runner/v2/internal/arcreview"
)

func TestReviewArcanumClientPostsInlineAndSummaryComments(t *testing.T) {
	oldExec := arcExec
	t.Cleanup(func() {
		arcExec = oldExec
	})

	ctx := context.WithValue(context.Background(), contextKey("review-client"), "value")
	var calls []arcReviewClientExecCall
	arcExec = func(ctx context.Context, workspace string, name string, args ...string) ([]byte, []byte, error) {
		calls = append(calls, arcReviewClientExecCall{
			ctx:       ctx,
			workspace: workspace,
			name:      name,
			args:      append([]string{}, args...),
		})
		return nil, nil, nil
	}

	client := ReviewArcanumClient{Workspace: "/arcadia/workspace"}
	err := client.PostReviewInlineComment(ctx, "42", "r7", arcreview.ReviewInlineComment{
		Path:     "internal/arcreview/review_applier.go",
		Line:     27,
		Body:     "Persist the revision only after comments are posted.",
		Severity: "blocker",
	})
	if err != nil {
		t.Fatalf("PostReviewInlineComment() error = %v", err)
	}
	if err := client.PostReviewSummary(ctx, "42", "r7", "Found one blocking issue."); err != nil {
		t.Fatalf("PostReviewSummary() error = %v", err)
	}

	wantArgs := [][]string{
		{
			"comment",
			"--pr", "42",
			"--message", "internal/arcreview/review_applier.go:27 [blocker] (revision r7)\n\nPersist the revision only after comments are posted.",
		},
		{
			"comment",
			"--pr", "42",
			"--message", "Review summary for revision r7:\n\nFound one blocking issue.",
		},
	}
	if len(calls) != len(wantArgs) {
		t.Fatalf("arc calls = %d, want %d: %#v", len(calls), len(wantArgs), calls)
	}
	for i, call := range calls {
		if call.ctx != ctx {
			t.Fatalf("call %d context was not passed through", i)
		}
		if call.workspace != "/arcadia/workspace" {
			t.Fatalf("call %d workspace = %q, want /arcadia/workspace", i, call.workspace)
		}
		if call.name != "arc" {
			t.Fatalf("call %d command = %q, want arc", i, call.name)
		}
		if !reflect.DeepEqual(call.args, wantArgs[i]) {
			t.Fatalf("call %d args mismatch:\ngot:  %#v\nwant: %#v", i, call.args, wantArgs[i])
		}
	}
}

func TestReviewArcanumClientSurfacesArcErrors(t *testing.T) {
	tests := []struct {
		name          string
		post          func(ReviewArcanumClient) error
		wantSubstring string
	}{
		{
			name: "inline",
			post: func(client ReviewArcanumClient) error {
				return client.PostReviewInlineComment(context.Background(), "42", "r7", arcreview.ReviewInlineComment{
					Path: "internal/arcreview/review_applier.go",
					Line: 27,
					Body: "Persist the revision only after comments are posted.",
				})
			},
			wantSubstring: "post review inline comment",
		},
		{
			name: "summary",
			post: func(client ReviewArcanumClient) error {
				return client.PostReviewSummary(context.Background(), "42", "r7", "Found one blocking issue.")
			},
			wantSubstring: "post review summary",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldExec := arcExec
			t.Cleanup(func() {
				arcExec = oldExec
			})

			arcExec = func(context.Context, string, string, ...string) ([]byte, []byte, error) {
				return nil, []byte("arc comment failed"), errors.New("exit status 1")
			}

			client := ReviewArcanumClient{Workspace: "/arcadia/workspace"}
			err := tt.post(client)
			if err == nil {
				t.Fatal("post error = nil")
			}
			for _, want := range []string{tt.wantSubstring, "arc comment --pr 42 --message", "arc comment failed", "exit status 1"} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("post error = %q, want substring %q", err, want)
				}
			}
		})
	}
}

type arcReviewClientExecCall struct {
	ctx       context.Context
	workspace string
	name      string
	args      []string
}
