package arcanum

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestListWorkspacePRsRunsArcPRListJSONAndParsesSummaries(t *testing.T) {
	oldExec := arcExec
	t.Cleanup(func() {
		arcExec = oldExec
	})

	fixture := []byte(`{"id":2293787,"title":"ARW2-03 Add PR list adapter","author":{"login":"alice"},"reviewers":[{"login":"bob"},{"login":"carol"}],"target_branch":"trunk","status":"open"}
{"id":"2293791","summary":"ARW2-05 Wire discovery","createdBy":"dave","reviewer_logins":["erin"],"baseBranch":"users/dave/arw2-05","state":"open"}
`)

	ctx := context.WithValue(context.Background(), contextKey("list"), "value")
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

	got, err := ListWorkspacePRs(ctx, "/arcadia/workspace")
	if err != nil {
		t.Fatalf("ListWorkspacePRs() error = %v", err)
	}

	if gotCtx != ctx {
		t.Fatal("ListWorkspacePRs() did not pass through context")
	}
	if gotWorkspace != "/arcadia/workspace" {
		t.Fatalf("ListWorkspacePRs() workspace = %q", gotWorkspace)
	}
	if gotName != "arc" {
		t.Fatalf("ListWorkspacePRs() command = %q", gotName)
	}
	if wantArgs := []string{"pr", "list", "--json"}; !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("ListWorkspacePRs() args = %#v, want %#v", gotArgs, wantArgs)
	}

	want := []PRSummary{
		{
			ID:        "2293787",
			Title:     "ARW2-03 Add PR list adapter",
			Author:    "alice",
			Reviewers: []string{"bob", "carol"},
			Branch:    "trunk",
			Status:    "open",
		},
		{
			ID:        "2293791",
			Title:     "ARW2-05 Wire discovery",
			Author:    "dave",
			Reviewers: []string{"erin"},
			Branch:    "users/dave/arw2-05",
			Status:    "open",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListWorkspacePRs() = %#v, want %#v", got, want)
	}
}

func TestListWorkspacePRsSurfacesArcErrors(t *testing.T) {
	oldExec := arcExec
	t.Cleanup(func() {
		arcExec = oldExec
	})

	arcExec = func(context.Context, string, string, ...string) ([]byte, []byte, error) {
		return nil, []byte("arc: authentication failed"), errors.New("exit status 1")
	}

	_, err := ListWorkspacePRs(context.Background(), "/arcadia/workspace")
	if err == nil {
		t.Fatal("ListWorkspacePRs() error = nil")
	}

	message := err.Error()
	for _, want := range []string{
		"arc pr list --json",
		"/arcadia/workspace",
		"arc: authentication failed",
		"exit status 1",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("ListWorkspacePRs() error = %q, want substring %q", message, want)
		}
	}
}
