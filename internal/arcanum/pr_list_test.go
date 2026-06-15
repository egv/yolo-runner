package arcanum

import (
	"context"
	"reflect"
	"testing"
)

func TestListReviewPRsMergesIncomingAndOutgoingDeduped(t *testing.T) {
	oldExec := arcExec
	t.Cleanup(func() {
		arcExec = oldExec
	})

	reviewerPRs := []byte(`{"id":"100","status":"open","summary":"reviewer PR","from_id":"rev-100"}` + "\n" +
		`{"id":"150","status":"open","summary":"another reviewer PR","from_id":"rev-150"}` + "\n")
	authorPRs := []byte(`{"id":"100","status":"open","summary":"also mine","from_id":"rev-100"}` + "\n" +
		`{"id":"200","status":"open","summary":"my outgoing PR","from_id":"rev-200"}` + "\n")

	var gotArgs [][]string
	var gotWorkspaces []string
	arcExec = func(_ context.Context, workspace string, _ string, args ...string) ([]byte, []byte, error) {
		gotWorkspaces = append(gotWorkspaces, workspace)
		gotArgs = append(gotArgs, append([]string{}, args...))
		switch {
		case reflect.DeepEqual(args, []string{"mount", "--list", "--json"}):
			return []byte(`[{"status":"mounted","mount":"/arcadia"}]`), nil, nil
		case reflect.DeepEqual(args, []string{"pr", "list", "--json", "--reviewer", "alice", "--status", "open"}):
			return reviewerPRs, nil, nil
		case reflect.DeepEqual(args, []string{"pr", "list", "--json", "--author", "alice", "--status", "open"}):
			return authorPRs, nil, nil
		default:
			t.Fatalf("unexpected arc args = %#v", args)
			return nil, nil, nil
		}
	}

	got, err := ListReviewPRs(context.Background(), "alice")
	if err != nil {
		t.Fatalf("ListReviewPRs() error = %v", err)
	}

	wantArgs := [][]string{
		{"mount", "--list", "--json"},
		{"pr", "list", "--json", "--reviewer", "alice", "--status", "open"},
		{"pr", "list", "--json", "--author", "alice", "--status", "open"},
	}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("ListReviewPRs() arc calls = %#v, want %#v", gotArgs, wantArgs)
	}
	wantWorkspaces := []string{"", "/arcadia", "/arcadia"}
	if !reflect.DeepEqual(gotWorkspaces, wantWorkspaces) {
		t.Fatalf("ListReviewPRs() workspaces = %#v, want %#v", gotWorkspaces, wantWorkspaces)
	}

	// PR 100 appears in both lists; the reviewer entry wins, then 150
	// (reviewer) and 200 (author) are appended in order.
	wantIDs := []string{"100", "150", "200"}
	var gotIDs []string
	for _, pr := range got {
		gotIDs = append(gotIDs, pr.ID)
	}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("ListReviewPRs() ids = %#v, want %#v", gotIDs, wantIDs)
	}
	if got[0].Summary != "reviewer PR" {
		t.Fatalf("dedup kept the wrong PR 100 entry: %q", got[0].Summary)
	}
}

func TestListReviewPRsWithBlankUserDiscoversNothing(t *testing.T) {
	oldExec := arcExec
	t.Cleanup(func() {
		arcExec = oldExec
	})

	arcExec = func(context.Context, string, string, ...string) ([]byte, []byte, error) {
		t.Fatal("blank user should not call arc")
		return nil, nil, nil
	}

	got, err := ListReviewPRs(context.Background(), " ")
	if err != nil {
		t.Fatalf("ListReviewPRs(blank) error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("ListReviewPRs(blank) = %#v, want none", got)
	}
}
