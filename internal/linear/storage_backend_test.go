package linear

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

func TestNewStorageBackendAcceptsValidAuth(t *testing.T) {
	t.Parallel()

	backend := newLinearStorageTestBackend(t, func(t *testing.T, query string, w http.ResponseWriter) {
		t.Helper()
		t.Fatalf("unexpected non-auth query: %q", query)
	})

	if backend == nil {
		t.Fatalf("expected non-nil storage backend")
	}
}

func TestStorageBackendGetTaskReturnsNilWhenIssueMissing(t *testing.T) {
	t.Parallel()

	backend := newLinearStorageTestBackend(t, func(t *testing.T, query string, w http.ResponseWriter) {
		t.Helper()
		if query == "" {
			t.Fatalf("expected query payload")
		}
		_, _ = w.Write([]byte(`{"data":{"issue":null}}`))
	})

	task, err := backend.GetTask(context.Background(), "iss-missing")
	if err != nil {
		t.Fatalf("GetTask returned error: %v", err)
	}
	if task != nil {
		t.Fatalf("expected nil task for missing issue, got %#v", task)
	}
}

func TestStorageBackendGetTaskTreeReturnsHierarchyWithDependencies(t *testing.T) {
	t.Parallel()

	backend := newLinearStorageTestBackend(t, func(t *testing.T, query string, w http.ResponseWriter) {
		t.Helper()
		switch {
		case strings.Contains(query, `project(id: "iss-root")`):
			_, _ = w.Write([]byte(`{"data":{"project":null}}`))
		case strings.Contains(query, "ReadIssueChildren") && strings.Contains(query, `issue(id: "iss-root")`):
			_, _ = w.Write([]byte(`{
  "data": {
    "issue": {
      "children": {
        "nodes": [
          {
            "id": "iss-child",
            "project": {"id": "proj-1"},
            "parent": {"id": "iss-root"},
            "title": "Child issue",
            "description": "",
            "priority": 2,
            "state": {"type": "backlog", "name": "Backlog"},
            "relations": {
              "nodes": [
                {"type": "blocked_by", "relatedIssue": {"id": "iss-dep"}}
              ]
            }
          },
          {
            "id": "iss-dep",
            "project": {"id": "proj-1"},
            "parent": {"id": "iss-root"},
            "title": "Dependency issue",
            "description": "",
            "priority": 2,
            "state": {"type": "completed", "name": "Done"},
            "relations": {"nodes": []}
          }
        ]
      }
    }
  }
}`))
		case strings.Contains(query, "ReadIssueChildren") && strings.Contains(query, `issue(id: "iss-child")`):
			_, _ = w.Write([]byte(`{"data":{"issue":{"children":{"nodes":[]}}}}`))
		case strings.Contains(query, "ReadIssueChildren") && strings.Contains(query, `issue(id: "iss-dep")`):
			_, _ = w.Write([]byte(`{"data":{"issue":{"children":{"nodes":[]}}}}`))
		case strings.Contains(query, "ReadIssue {") && strings.Contains(query, `issue(id: "iss-root")`):
			_, _ = w.Write([]byte(`{
  "data": {
    "issue": {
      "id": "iss-root",
      "project": {"id": "proj-1"},
      "parent": null,
      "title": "Root issue",
      "description": "",
      "priority": 2,
      "state": {"type": "backlog", "name": "Backlog"},
      "relations": {"nodes": []}
    }
  }
}`))
		default:
			t.Fatalf("unexpected query: %q", query)
		}
	})

	tree, err := backend.GetTaskTree(context.Background(), "iss-root")
	if err != nil {
		t.Fatalf("GetTaskTree returned error: %v", err)
	}
	if tree == nil {
		t.Fatalf("expected non-nil task tree")
	}
	if tree.Root.ID != "iss-root" {
		t.Fatalf("expected root iss-root, got %q", tree.Root.ID)
	}
	if len(tree.Tasks) != 3 {
		t.Fatalf("expected 3 tasks, got %d", len(tree.Tasks))
	}
	if deps := tree.Tasks["iss-child"].Metadata["dependencies"]; deps != "iss-dep" {
		t.Fatalf("expected dependency metadata iss-dep, got %q", deps)
	}
	assertLinearStorageRelation(t, tree.Relations, contracts.TaskRelation{
		FromID: "iss-root",
		ToID:   "iss-child",
		Type:   contracts.RelationParent,
	})
	assertLinearStorageRelation(t, tree.Relations, contracts.TaskRelation{
		FromID: "iss-child",
		ToID:   "iss-dep",
		Type:   contracts.RelationDependsOn,
	})
	assertLinearStorageRelation(t, tree.Relations, contracts.TaskRelation{
		FromID: "iss-dep",
		ToID:   "iss-child",
		Type:   contracts.RelationBlocks,
	})
}

func newLinearStorageTestBackend(t *testing.T, handler func(t *testing.T, query string, w http.ResponseWriter)) *StorageBackend {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Helper()
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		query := decodeGraphQLQuery(t, body)
		if strings.Contains(query, "AuthProbe") {
			_, _ = w.Write([]byte(`{"data":{"viewer":{"id":"usr_123"}}}`))
			return
		}
		handler(t, query, w)
	}))
	t.Cleanup(server.Close)

	backend, err := NewStorageBackend(Config{
		Workspace:  "acme",
		Token:      "lin_api_valid",
		Endpoint:   server.URL,
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("build storage backend: %v", err)
	}
	return backend
}

func assertLinearStorageRelation(t *testing.T, relations []contracts.TaskRelation, want contracts.TaskRelation) {
	t.Helper()
	for _, relation := range relations {
		if relation.FromID == want.FromID && relation.ToID == want.ToID && relation.Type == want.Type {
			return
		}
	}
	t.Fatalf("expected relation %#v, got %#v", want, relations)
}
