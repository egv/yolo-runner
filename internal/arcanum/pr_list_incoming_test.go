package arcanum

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestListIncomingReviewPRsRunsCrossProjectArcCommandParsesJSONLAndSurfacesErrors(t *testing.T) {
	oldExec := arcExec
	t.Cleanup(func() {
		arcExec = oldExec
	})

	fixture := []byte(`{"id":13843457,"status":"open","author":"alice","summary":"Fix intent schema","from_branch":"users/alice/fix-intent","from_id":"b73ed5e7504e08ca499d020437f0bbb8582c39da","to_branch":"trunk","reviewers":["genaevstratov",{"login":"bob"}],"issues":["YANGOSWARM-587",{"id":"YT-42"}]}
{"id":"13843458","status":"open","author":{"login":"carol"},"summary":"Wire review source","from_branch":"users/carol/review-source","from_id":"c0ffee","to_branch":"release","reviewers":[{"login":"genaevstratov"}],"issues":[{"key":"ARC-9","summary":"linked tracker ticket"}]}
`)

	ctx := context.WithValue(context.Background(), incomingListContextKey("list"), "value")
	var gotCtx context.Context
	var gotWorkspace string
	var gotName string
	var gotArgs []string

	arcExec = func(ctx context.Context, workspace string, name string, args ...string) ([]byte, []byte, error) {
		gotCtx = ctx
		gotWorkspace = workspace
		gotName = name
		gotArgs = append([]string{}, args...)
		return fixture, nil, nil
	}

	got, err := ListIncomingReviewPRs(ctx)
	if err != nil {
		t.Fatalf("ListIncomingReviewPRs() error = %v", err)
	}

	if gotCtx != ctx {
		t.Fatal("ListIncomingReviewPRs() did not pass through context")
	}
	if gotWorkspace != "" {
		t.Fatalf("ListIncomingReviewPRs() workspace = %q, want empty", gotWorkspace)
	}
	if gotName != "arc" {
		t.Fatalf("ListIncomingReviewPRs() command = %q", gotName)
	}
	if wantArgs := []string{"pr", "list", "--json", "-i", "--status", "open"}; !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("ListIncomingReviewPRs() args = %#v, want %#v", gotArgs, wantArgs)
	}

	want := []PRSummary{
		{
			ID:         "13843457",
			Status:     "open",
			Author:     "alice",
			Title:      "Fix intent schema",
			Summary:    "Fix intent schema",
			Branch:     "trunk",
			FromBranch: "users/alice/fix-intent",
			FromID:     "b73ed5e7504e08ca499d020437f0bbb8582c39da",
			ToBranch:   "trunk",
			Reviewers:  []string{"genaevstratov", "bob"},
			Issues:     []string{"YANGOSWARM-587", "YT-42"},
		},
		{
			ID:         "13843458",
			Status:     "open",
			Author:     "carol",
			Title:      "Wire review source",
			Summary:    "Wire review source",
			Branch:     "release",
			FromBranch: "users/carol/review-source",
			FromID:     "c0ffee",
			ToBranch:   "release",
			Reviewers:  []string{"genaevstratov"},
			Issues:     []string{"ARC-9"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListIncomingReviewPRs() = %#v, want %#v", got, want)
	}

	arcExec = func(context.Context, string, string, ...string) ([]byte, []byte, error) {
		return nil, []byte("arc: authentication failed"), errors.New("exit status 1")
	}

	_, err = ListIncomingReviewPRs(context.Background())
	if err == nil {
		t.Fatal("ListIncomingReviewPRs() error = nil")
	}

	message := err.Error()
	for _, want := range []string{
		"arc pr list --json -i --status open",
		"arc: authentication failed",
		"exit status 1",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("ListIncomingReviewPRs() error = %q, want substring %q", message, want)
		}
	}
}

type incomingListContextKey string
