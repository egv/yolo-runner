package github

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

func TestStorageBackendGetTaskTreeReturnsOnlyRootDescendants(t *testing.T) {
	t.Parallel()

	backend := newGitHubStorageTestBackend(t, func(t *testing.T, r *http.Request, w http.ResponseWriter) {
		t.Helper()
		switch r.URL.Path {
		case "/repos/egv/yolo-runner/issues/52":
			_, _ = w.Write([]byte(`{"number":52,"title":"Distribution & Installation","body":"Root epic","state":"open","labels":[]}`))
		case "/repos/egv/yolo-runner/issues":
			if got := r.URL.Query().Get("state"); got != "all" {
				t.Fatalf("expected state=all, got %q", got)
			}
			if got := r.URL.Query().Get("page"); got != "1" {
				t.Fatalf("expected page=1, got %q", got)
			}
			_, _ = w.Write([]byte(`[
				{"number":52,"title":"Distribution & Installation","body":"Root epic","state":"open","labels":[]},
				{"number":58,"title":"Task 58","body":"blocked-by:#59","state":"open","parent_issue_id":52,"labels":[{"name":"p1"}]},
				{"number":59,"title":"Task 59","body":"","state":"closed","parent_issue_id":52,"labels":[]},
				{"number":60,"title":"Task 60","body":"depends-on:#59\nblocked-by:#999","state":"open","parent_issue_id":58,"labels":[{"name":"depends-on:#59"}]},
				{"number":53,"title":"Unrelated Epic","body":"","state":"open","labels":[]},
				{"number":70,"title":"Child of unrelated","body":"","state":"open","parent_issue_id":53,"labels":[]},
				{"number":80,"title":"PR","body":"","state":"open","labels":[],"pull_request":{"url":"https://api.github.com/repos/egv/yolo-runner/pulls/80"}}
			]`))
		default:
			t.Fatalf("unexpected request path %q", r.URL.Path)
		}
	})

	tree, err := backend.GetTaskTree(context.Background(), "52")
	if err != nil {
		t.Fatalf("GetTaskTree returned error: %v", err)
	}

	if tree.Root.ID != "52" {
		t.Fatalf("expected root task ID 52, got %q", tree.Root.ID)
	}

	gotIDs := make([]string, 0, len(tree.Tasks))
	for id := range tree.Tasks {
		gotIDs = append(gotIDs, id)
	}
	sort.Strings(gotIDs)
	wantIDs := []string{"52", "58", "59", "60"}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("expected task IDs %v, got %v", wantIDs, gotIDs)
	}

	if got := tree.Tasks["58"].ParentID; got != "52" {
		t.Fatalf("expected task 58 parent 52, got %q", got)
	}
	if got := tree.Tasks["59"].ParentID; got != "52" {
		t.Fatalf("expected task 59 parent 52, got %q", got)
	}
	if got := tree.Tasks["60"].ParentID; got != "58" {
		t.Fatalf("expected task 60 parent 58, got %q", got)
	}
	if deps := tree.Tasks["60"].Metadata["dependencies"]; deps != "59" {
		t.Fatalf("expected task 60 dependencies metadata %q, got %q", "59", deps)
	}

	assertRelation(t, tree.Relations, contracts.TaskRelation{FromID: "52", ToID: "58", Type: contracts.RelationParent})
	assertRelation(t, tree.Relations, contracts.TaskRelation{FromID: "52", ToID: "59", Type: contracts.RelationParent})
	assertRelation(t, tree.Relations, contracts.TaskRelation{FromID: "58", ToID: "60", Type: contracts.RelationParent})
	assertRelation(t, tree.Relations, contracts.TaskRelation{FromID: "60", ToID: "59", Type: contracts.RelationDependsOn})
	assertRelation(t, tree.Relations, contracts.TaskRelation{FromID: "59", ToID: "60", Type: contracts.RelationBlocks})
}

func TestStorageBackendGetTaskTreeSupportsParentIssueURL(t *testing.T) {
	t.Parallel()

	backend := newGitHubStorageTestBackend(t, func(t *testing.T, r *http.Request, w http.ResponseWriter) {
		t.Helper()
		switch r.URL.Path {
		case "/repos/egv/yolo-runner/issues/52":
			_, _ = w.Write([]byte(`{"number":52,"title":"Root epic","body":"","state":"open","labels":[]}`))
		case "/repos/egv/yolo-runner/issues":
			_, _ = w.Write([]byte(`[
				{"number":52,"title":"Root epic","body":"","state":"open","labels":[]},
				{"number":58,"title":"Child A","body":"","state":"open","parent_issue_url":"https://api.github.com/repos/egv/yolo-runner/issues/52","labels":[]},
				{"number":60,"title":"Grandchild","body":"","state":"open","parent_issue_url":"https://api.github.com/repos/egv/yolo-runner/issues/58","labels":[]},
				{"number":53,"title":"Unrelated root","body":"","state":"open","labels":[]},
				{"number":70,"title":"Unrelated child","body":"","state":"open","parent_issue_url":"https://api.github.com/repos/egv/yolo-runner/issues/53","labels":[]}
			]`))
		default:
			t.Fatalf("unexpected request path %q", r.URL.Path)
		}
	})

	tree, err := backend.GetTaskTree(context.Background(), "52")
	if err != nil {
		t.Fatalf("GetTaskTree returned error: %v", err)
	}

	gotIDs := make([]string, 0, len(tree.Tasks))
	for id := range tree.Tasks {
		gotIDs = append(gotIDs, id)
	}
	sort.Strings(gotIDs)
	wantIDs := []string{"52", "58", "60"}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("expected task IDs %v, got %v", wantIDs, gotIDs)
	}

	if got := tree.Tasks["58"].ParentID; got != "52" {
		t.Fatalf("expected task 58 parent 52, got %q", got)
	}
	if got := tree.Tasks["60"].ParentID; got != "58" {
		t.Fatalf("expected task 60 parent 58, got %q", got)
	}
	assertRelation(t, tree.Relations, contracts.TaskRelation{FromID: "52", ToID: "58", Type: contracts.RelationParent})
	assertRelation(t, tree.Relations, contracts.TaskRelation{FromID: "58", ToID: "60", Type: contracts.RelationParent})
}

func TestStorageBackendGetTaskTreePrefersParentIssueIDOverParentIssueURL(t *testing.T) {
	t.Parallel()

	backend := newGitHubStorageTestBackend(t, func(t *testing.T, r *http.Request, w http.ResponseWriter) {
		t.Helper()
		switch r.URL.Path {
		case "/repos/egv/yolo-runner/issues/52":
			_, _ = w.Write([]byte(`{"number":52,"title":"Root epic","body":"","state":"open","labels":[]}`))
		case "/repos/egv/yolo-runner/issues":
			_, _ = w.Write([]byte(`[
				{"number":52,"title":"Root epic","body":"","state":"open","labels":[]},
				{"number":53,"title":"Unrelated epic","body":"","state":"open","labels":[]},
				{"number":58,"title":"Child with conflicting parent fields","body":"","state":"open","parent_issue_id":52,"parent_issue_url":"https://api.github.com/repos/egv/yolo-runner/issues/53","labels":[]}
			]`))
		default:
			t.Fatalf("unexpected request path %q", r.URL.Path)
		}
	})

	tree, err := backend.GetTaskTree(context.Background(), "52")
	if err != nil {
		t.Fatalf("GetTaskTree returned error: %v", err)
	}

	gotIDs := make([]string, 0, len(tree.Tasks))
	for id := range tree.Tasks {
		gotIDs = append(gotIDs, id)
	}
	sort.Strings(gotIDs)
	wantIDs := []string{"52", "58"}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("expected task IDs %v, got %v", wantIDs, gotIDs)
	}

	if got := tree.Tasks["58"].ParentID; got != "52" {
		t.Fatalf("expected task 58 parent 52, got %q", got)
	}
	assertRelation(t, tree.Relations, contracts.TaskRelation{FromID: "52", ToID: "58", Type: contracts.RelationParent})
}

func TestStorageBackendGetTaskIncludesParentAndDependencyMetadata(t *testing.T) {
	t.Parallel()

	backend := newGitHubStorageTestBackend(t, func(t *testing.T, r *http.Request, w http.ResponseWriter) {
		t.Helper()
		switch r.URL.Path {
		case "/repos/egv/yolo-runner/issues/60":
			_, _ = w.Write([]byte(`{
				"number": 60,
				"title": "Task 60",
				"body": "depends-on:#59\nblocked-by:#999",
				"state": "open",
				"parent_issue_id": 58,
				"labels": [{"name":"depends-on:#59"}]
			}`))
		case "/repos/egv/yolo-runner/issues":
			_, _ = w.Write([]byte(`[
				{"number":58,"title":"Task 58","body":"","state":"open","parent_issue_id":52,"labels":[]},
				{"number":59,"title":"Task 59","body":"","state":"closed","parent_issue_id":52,"labels":[]},
				{"number":60,"title":"Task 60","body":"depends-on:#59\nblocked-by:#999","state":"open","parent_issue_id":58,"labels":[{"name":"depends-on:#59"}]}
			]`))
		default:
			t.Fatalf("unexpected request path %q", r.URL.Path)
		}
	})

	task, err := backend.GetTask(context.Background(), "60")
	if err != nil {
		t.Fatalf("GetTask returned error: %v", err)
	}
	if task == nil {
		t.Fatalf("expected non-nil task")
	}
	if task.ParentID != "58" {
		t.Fatalf("expected parent ID 58, got %q", task.ParentID)
	}
	if deps := task.Metadata["dependencies"]; deps != "59" {
		t.Fatalf("expected dependencies metadata %q, got %q", "59", deps)
	}
}

func TestStorageBackendGetTaskSupportsParentIssueURL(t *testing.T) {
	t.Parallel()

	backend := newGitHubStorageTestBackend(t, func(t *testing.T, r *http.Request, w http.ResponseWriter) {
		t.Helper()
		switch r.URL.Path {
		case "/repos/egv/yolo-runner/issues/60":
			_, _ = w.Write([]byte(`{
				"number": 60,
				"title": "Task 60",
				"body": "",
				"state": "open",
				"parent_issue_url": "https://api.github.com/repos/egv/yolo-runner/issues/58",
				"labels": []
			}`))
		case "/repos/egv/yolo-runner/issues":
			_, _ = w.Write([]byte(`[
				{"number":58,"title":"Task 58","body":"","state":"open","labels":[]},
				{"number":60,"title":"Task 60","body":"","state":"open","parent_issue_url":"https://api.github.com/repos/egv/yolo-runner/issues/58","labels":[]}
			]`))
		default:
			t.Fatalf("unexpected request path %q", r.URL.Path)
		}
	})

	task, err := backend.GetTask(context.Background(), "60")
	if err != nil {
		t.Fatalf("GetTask returned error: %v", err)
	}
	if task == nil {
		t.Fatalf("expected non-nil task")
	}
	if task.ParentID != "58" {
		t.Fatalf("expected parent ID 58, got %q", task.ParentID)
	}
}

func TestStorageBackendBacksOffWhenRateLimitApproaching(t *testing.T) {
	backend := newGitHubStorageTestBackend(t, func(t *testing.T, r *http.Request, w http.ResponseWriter) {
		t.Helper()
		switch r.URL.Path {
		case "/repos/egv/yolo-runner/issues/60":
			w.Header().Set("X-RateLimit-Remaining", "1")
			w.Header().Set("X-RateLimit-Reset", "1700000003")
			_, _ = w.Write([]byte(`{"number":60,"title":"Task 60","body":"","state":"open","labels":[]}`))
		case "/repos/egv/yolo-runner/issues":
			_, _ = w.Write([]byte(`[{"number":60,"title":"Task 60","body":"","state":"open","labels":[]}]`))
		default:
			t.Fatalf("unexpected request path %q", r.URL.Path)
		}
	})

	backend.manager.now = func() time.Time { return time.Unix(1700000000, 0) }
	slept := []time.Duration{}
	backend.manager.sleep = func(d time.Duration) {
		slept = append(slept, d)
	}

	task, err := backend.GetTask(context.Background(), "60")
	if err != nil {
		t.Fatalf("GetTask returned error: %v", err)
	}
	if task == nil || task.ID != "60" {
		t.Fatalf("unexpected task: %#v", task)
	}
	if len(slept) == 0 {
		t.Fatalf("expected backoff sleep when rate limit is low")
	}
	if slept[0] < 3*time.Second {
		t.Fatalf("expected at least 3s backoff, got %s", slept[0])
	}
}

func TestStorageBackendGetTaskTreeOverlaysPersistedBlockedStatusAndData(t *testing.T) {
	backend := newGitHubStorageTestBackend(t, func(t *testing.T, r *http.Request, w http.ResponseWriter) {
		t.Helper()
		switch r.URL.Path {
		case "/repos/egv/yolo-runner/issues/40":
			_, _ = w.Write([]byte(`{"number":40,"title":"Superepic","body":"","state":"open","labels":[]}`))
		case "/repos/egv/yolo-runner/issues":
			_, _ = w.Write([]byte(`[
				{"number":40,"title":"Superepic","body":"","state":"open","labels":[]},
				{"number":1,"title":"Epic 1","body":"","state":"open","parent_issue_id":40,"labels":[]},
				{"number":6,"title":"Task 6","body":"","state":"open","parent_issue_id":1,"labels":[]}
			]`))
		default:
			t.Fatalf("unexpected request path %q", r.URL.Path)
		}
	})

	if err := backend.PersistTaskStatusChange(context.Background(), "6", contracts.TaskStatusBlocked); err != nil {
		t.Fatalf("PersistTaskStatusChange returned error: %v", err)
	}
	if err := backend.PersistTaskDataChange(context.Background(), "6", map[string]string{"triage_status": "blocked", "triage_reason": "needs tests first"}); err != nil {
		t.Fatalf("PersistTaskDataChange returned error: %v", err)
	}

	tree, err := backend.GetTaskTree(context.Background(), "40")
	if err != nil {
		t.Fatalf("GetTaskTree returned error: %v", err)
	}
	task := tree.Tasks["6"]
	if task.Status != contracts.TaskStatusBlocked {
		t.Fatalf("expected task 6 to be blocked from persisted local state, got %s", task.Status)
	}
	if task.Metadata["triage_reason"] != "needs tests first" {
		t.Fatalf("expected persisted triage_reason, got %#v", task.Metadata)
	}
}

func TestStorageBackendSetTaskDataSkipsDuplicatePersistedWrite(t *testing.T) {
	posts := 0
	backend := newGitHubStorageTestBackend(t, func(t *testing.T, r *http.Request, w http.ResponseWriter) {
		t.Helper()
		if r.URL.Path == "/repos/egv/yolo-runner/issues/8/comments" && r.Method == http.MethodPost {
			posts++
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":1}`))
			return
		}
		t.Fatalf("unexpected request path %q", r.URL.Path)
	})

	seed := map[string]string{"triage_status": "blocked", "triage_reason": "needs manual input"}
	if err := backend.PersistTaskDataChange(context.Background(), "8", seed); err != nil {
		t.Fatalf("PersistTaskDataChange returned error: %v", err)
	}
	if err := backend.SetTaskData(context.Background(), "8", seed); err != nil {
		t.Fatalf("SetTaskData returned error: %v", err)
	}
	if posts != 0 {
		t.Fatalf("expected duplicate task data write to be skipped, got %d POSTs", posts)
	}
}

func assertRelation(t *testing.T, relations []contracts.TaskRelation, want contracts.TaskRelation) {
	t.Helper()
	for _, rel := range relations {
		if rel.FromID == want.FromID && rel.ToID == want.ToID && rel.Type == want.Type {
			return
		}
	}
	t.Fatalf("expected relation %#v, got %#v", want, relations)
}

func newGitHubStorageTestBackend(t *testing.T, handler func(t *testing.T, r *http.Request, w http.ResponseWriter)) *StorageBackend {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Helper()
		if r.URL.Path == "/repos/egv/yolo-runner" {
			_, _ = w.Write([]byte(`{"full_name":"egv/yolo-runner"}`))
			return
		}
		handler(t, r, w)
	}))
	t.Cleanup(server.Close)

	backend, err := NewStorageBackend(Config{
		Owner:       "egv",
		Repo:        "yolo-runner",
		Token:       "ghp_test",
		APIEndpoint: server.URL,
		HTTPClient:  server.Client(),
		StatePath:   filepath.Join(t.TempDir(), "github-state.json"),
	})
	if err != nil {
		t.Fatalf("build storage backend: %v", err)
	}
	return backend
}
