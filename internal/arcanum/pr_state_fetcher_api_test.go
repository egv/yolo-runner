package arcanum

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchPRRuntimeStateWithClientNeedsNoWorkspace(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/api/v1/review-requests/14330209" {
			t.Fatalf("path = %q, want /api/v1/review-requests/14330209", got)
		}
		if fields := r.URL.Query().Get("fields"); !strings.Contains(fields, "checks(") {
			t.Fatalf("fields = %q, want checks projected", fields)
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"data":{
  "id":14330209,
  "summary":"rewrite dora in Go",
  "author":{"name":"alice"},
  "state":"open",
  "vcs":{"from_branch":"users/alice/dora","to_branch":"trunk"},
  "active_diff_set":{"id":34183397},
  "checks":[
    {"system":"CI","type":"Arcadia Common Checks","required":true,"status":"success"},
    {"system":"CI","type":"autocheck","required":true,"status":"failure"}
  ]
}}`)); err != nil {
			t.Fatalf("write response: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	// Comments come from the public API via the arcExec seam — no workspace.
	oldExec := arcExec
	t.Cleanup(func() { arcExec = oldExec })
	var execCalls [][]string
	arcExec = func(_ context.Context, workspace string, name string, args ...string) ([]byte, []byte, error) {
		execCalls = append(execCalls, append([]string{workspace, name}, args...))
		if workspace != "" {
			t.Fatalf("state fetch ran %q in workspace %q, want no workspace", name, workspace)
		}
		if name != "curl" {
			t.Fatalf("state fetch ran %q, want only the comments curl", name)
		}
		return []byte(`{"data":[{"id":"c-1","content":"please fix","issue_status":"open"}]}`), nil, nil
	}
	t.Setenv("ARC_TOKEN", "test-token")

	client, err := NewAPIClient(APIClientConfig{
		BaseURL:     server.URL + "/api",
		HTTPClient:  server.Client(),
		TokenSource: func(context.Context) (string, error) { return "test-token", nil },
	})
	if err != nil {
		t.Fatalf("NewAPIClient() error = %v", err)
	}

	state, err := FetchPRRuntimeStateWithClient(context.Background(), client, "14330209")
	if err != nil {
		t.Fatalf("FetchPRRuntimeStateWithClient() error = %v", err)
	}
	if state.PRID != "14330209" || state.Revision != "34183397" {
		t.Fatalf("state id/revision = %q/%q, want 14330209/34183397", state.PRID, state.Revision)
	}
	if state.Details.Author != "alice" || state.Details.Branch != "users/alice/dora" || state.Details.TargetBranch != "trunk" {
		t.Fatalf("details = %#v, want author alice, branch users/alice/dora -> trunk", state.Details)
	}
	if state.Details.Status != "open" {
		t.Fatalf("details status = %q, want open", state.Details.Status)
	}
	if len(state.Comments) != 1 || state.Comments[0].ID != "c-1" {
		t.Fatalf("comments = %#v, want the fetched c-1", state.Comments)
	}
	if len(state.Checks) != 2 || state.Checks[1].Status != "failure" {
		t.Fatalf("checks = %#v, want 2 checks with the autocheck failure preserved", state.Checks)
	}
	if len(execCalls) != 1 {
		t.Fatalf("exec calls = %#v, want exactly one comments curl", execCalls)
	}
}
