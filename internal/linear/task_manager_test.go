package linear

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anomalyco/yolo-runner/internal/contracts"
)

func TestNewTaskManagerRejectsMissingToken(t *testing.T) {
	_, err := NewTaskManager(Config{Workspace: "acme"})
	if err == nil {
		t.Fatalf("expected missing token to fail")
	}
	if !strings.Contains(err.Error(), "token") {
		t.Fatalf("expected token validation error, got %q", err.Error())
	}
}

func TestNewTaskManagerRejectsInvalidAuthResponse(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"errors":[{"message":"Invalid authentication"}]}`))
	}))
	t.Cleanup(server.Close)

	_, err := NewTaskManager(Config{
		Workspace:  "acme",
		Token:      "lin_api_invalid",
		Endpoint:   server.URL,
		HTTPClient: server.Client(),
	})
	if err == nil {
		t.Fatalf("expected invalid auth to fail")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "invalid authentication") {
		t.Fatalf("expected invalid auth details, got %q", err.Error())
	}
}

func TestNewTaskManagerAcceptsValidAuth(t *testing.T) {
	t.Parallel()
	gotAuthorization := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthorization = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"data":{"viewer":{"id":"usr_123"}}}`))
	}))
	t.Cleanup(server.Close)

	manager, err := NewTaskManager(Config{
		Workspace:  "acme",
		Token:      "lin_api_valid",
		Endpoint:   server.URL,
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("expected valid auth to pass, got %v", err)
	}
	if manager == nil {
		t.Fatalf("expected non-nil task manager")
	}
	if gotAuthorization != "lin_api_valid" {
		t.Fatalf("expected Authorization header to contain token, got %q", gotAuthorization)
	}
}

func TestTaskManagerNextTasksFiltersUnsatisfiedDependenciesAndSortsByPriority(t *testing.T) {
	t.Parallel()

	manager := newLinearTestManager(t, func(t *testing.T, query string, w http.ResponseWriter) {
		t.Helper()
		if !strings.Contains(query, "ReadProjectBacklog") {
			t.Fatalf("expected ReadProjectBacklog query, got %q", query)
		}
		if !strings.Contains(query, `project(id: "proj-1")`) {
			t.Fatalf("expected query to include parent project ID, got %q", query)
		}
		_, _ = w.Write([]byte(`{
  "data": {
    "project": {
      "id": "proj-1",
      "name": "Roadmap",
      "issues": {
        "nodes": [
          {
            "id": "iss-dep-open",
            "project": {"id": "proj-1"},
            "title": "Dependency open",
            "description": "",
            "priority": 2,
            "state": {"type": "started", "name": "In Progress"}
          },
          {
            "id": "iss-dep-closed",
            "project": {"id": "proj-1"},
            "title": "Dependency done",
            "description": "",
            "priority": 2,
            "state": {"type": "completed", "name": "Done"}
          },
          {
            "id": "iss-ready-high",
            "project": {"id": "proj-1"},
            "title": "Ready high",
            "description": "",
            "priority": 1,
            "state": {"type": "backlog", "name": "Backlog"}
          },
          {
            "id": "iss-ready-low",
            "project": {"id": "proj-1"},
            "title": "Ready low",
            "description": "",
            "priority": 3,
            "state": {"type": "backlog", "name": "Backlog"},
            "relations": {
              "nodes": [
                {"type": "blocked_by", "relatedIssue": {"id": "iss-dep-closed"}}
              ]
            }
          },
          {
            "id": "iss-blocked",
            "project": {"id": "proj-1"},
            "title": "Blocked",
            "description": "",
            "priority": 2,
            "state": {"type": "backlog", "name": "Backlog"},
            "relations": {
              "nodes": [
                {"type": "blocked_by", "relatedIssue": {"id": "iss-dep-open"}}
              ]
            }
          }
        ]
      }
    }
  }
}`))
	})

	tasks, err := manager.NextTasks(context.Background(), "proj-1")
	if err != nil {
		t.Fatalf("NextTasks returned error: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("expected 2 runnable tasks, got %#v", tasks)
	}
	if tasks[0].ID != "iss-ready-high" || tasks[0].Title != "Ready high" {
		t.Fatalf("expected first task iss-ready-high, got %#v", tasks[0])
	}
	if tasks[0].Priority == nil || *tasks[0].Priority != 0 {
		t.Fatalf("expected first task priority 0, got %#v", tasks[0].Priority)
	}
	if tasks[1].ID != "iss-ready-low" || tasks[1].Title != "Ready low" {
		t.Fatalf("expected second task iss-ready-low, got %#v", tasks[1])
	}
	if tasks[1].Priority == nil || *tasks[1].Priority != 2 {
		t.Fatalf("expected second task priority 2, got %#v", tasks[1].Priority)
	}
}

func TestTaskManagerNextTasksReturnsOpenLeafParentIssueWhenNoChildren(t *testing.T) {
	t.Parallel()

	manager := newLinearTestManager(t, func(t *testing.T, query string, w http.ResponseWriter) {
		t.Helper()
		switch {
		case strings.Contains(query, `project(id: "iss-parent")`):
			_, _ = w.Write([]byte(`{"data":{"project":null}}`))
		case strings.Contains(query, `issue(id: "iss-parent")`):
			_, _ = w.Write([]byte(`{
  "data": {
    "issue": {
      "id": "iss-parent",
      "project": {"id": "proj-1"},
      "parent": null,
      "title": "Leaf task",
      "description": "single issue root",
      "priority": 2,
      "state": {"type": "backlog", "name": "Backlog"},
      "relations": {"nodes": []}
    }
  }
}`))
		case strings.Contains(query, `project(id: "proj-1")`):
			_, _ = w.Write([]byte(`{
  "data": {
    "project": {
      "id": "proj-1",
      "name": "Roadmap",
      "issues": {
        "nodes": [
          {
            "id": "iss-parent",
            "project": {"id": "proj-1"},
            "parent": null,
            "title": "Leaf task",
            "description": "single issue root",
            "priority": 2,
            "state": {"type": "backlog", "name": "Backlog"},
            "relations": {"nodes": []}
          }
        ]
      }
    }
  }
}`))
		default:
			t.Fatalf("unexpected query: %q", query)
		}
	})

	tasks, err := manager.NextTasks(context.Background(), "iss-parent")
	if err != nil {
		t.Fatalf("NextTasks returned error: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected one leaf task, got %#v", tasks)
	}
	if tasks[0].ID != "iss-parent" || tasks[0].Title != "Leaf task" {
		t.Fatalf("unexpected leaf task summary: %#v", tasks[0])
	}
	if tasks[0].Priority == nil || *tasks[0].Priority != 1 {
		t.Fatalf("expected normalized priority 1, got %#v", tasks[0].Priority)
	}
}

func TestTaskManagerGetTaskMapsIssueDetailsAndDependencyMetadata(t *testing.T) {
	t.Parallel()

	manager := newLinearTestManager(t, func(t *testing.T, query string, w http.ResponseWriter) {
		t.Helper()
		if !strings.Contains(query, "ReadIssue") {
			t.Fatalf("expected ReadIssue query, got %q", query)
		}
		if !strings.Contains(query, `issue(id: "iss-2")`) {
			t.Fatalf("expected issue query to include issue ID, got %q", query)
		}
		_, _ = w.Write([]byte(`{
  "data": {
    "issue": {
      "id": "iss-2",
      "project": {"id": "proj-1"},
      "parent": {"id": "iss-1"},
      "title": "Implement read path",
      "description": "Load issue details",
      "priority": 2,
      "state": {"type": "started", "name": "In Progress"},
      "relations": {
        "nodes": [
          {"type": "blocked_by", "relatedIssue": {"id": "dep-2"}},
          {"type": "blocked_by", "relatedIssue": {"id": "dep-1"}},
          {"type": "blocked_by", "relatedIssue": {"id": "dep-2"}},
          {"type": "blocks", "relatedIssue": {"id": "child-1"}},
          {"type": "blocked_by", "relatedIssue": {"id": "iss-2"}}
        ]
      }
    }
  }
}`))
	})

	task, err := manager.GetTask(context.Background(), "iss-2")
	if err != nil {
		t.Fatalf("GetTask returned error: %v", err)
	}
	if task.ID != "iss-2" {
		t.Fatalf("expected task ID iss-2, got %q", task.ID)
	}
	if task.Title != "Implement read path" {
		t.Fatalf("expected task title, got %q", task.Title)
	}
	if task.Description != "Load issue details" {
		t.Fatalf("expected task description, got %q", task.Description)
	}
	if task.Status != contracts.TaskStatusInProgress {
		t.Fatalf("expected task status in_progress, got %q", task.Status)
	}
	if task.ParentID != "iss-1" {
		t.Fatalf("expected parent ID iss-1, got %q", task.ParentID)
	}
	if deps := task.Metadata["dependencies"]; deps != "dep-1,dep-2" {
		t.Fatalf("expected sorted dependency metadata, got %q", deps)
	}
}

func newLinearTestManager(t *testing.T, handler func(t *testing.T, query string, w http.ResponseWriter)) *TaskManager {
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

	manager, err := NewTaskManager(Config{
		Workspace:  "acme",
		Token:      "lin_api_valid",
		Endpoint:   server.URL,
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("build test task manager: %v", err)
	}
	return manager
}

func decodeGraphQLQuery(t *testing.T, body []byte) string {
	t.Helper()

	var payload struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode GraphQL payload: %v", err)
	}
	return payload.Query
}
