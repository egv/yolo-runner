package github

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewTaskManagerRequiresOwner(t *testing.T) {
	_, err := NewTaskManager(Config{Repo: "yolo-runner", Token: "ghp_test"})
	if err == nil {
		t.Fatalf("expected missing owner to fail")
	}
	if !strings.Contains(err.Error(), "owner") {
		t.Fatalf("expected owner validation error, got %q", err.Error())
	}
}

func TestNewTaskManagerRequiresRepo(t *testing.T) {
	_, err := NewTaskManager(Config{Owner: "anomalyco", Token: "ghp_test"})
	if err == nil {
		t.Fatalf("expected missing repository to fail")
	}
	if !strings.Contains(err.Error(), "repository") {
		t.Fatalf("expected repository validation error, got %q", err.Error())
	}
}

func TestNewTaskManagerRequiresToken(t *testing.T) {
	_, err := NewTaskManager(Config{Owner: "anomalyco", Repo: "yolo-runner"})
	if err == nil {
		t.Fatalf("expected missing token to fail")
	}
	if !strings.Contains(err.Error(), "token") {
		t.Fatalf("expected token validation error, got %q", err.Error())
	}
}

func TestNewTaskManagerProbesConfiguredRepository(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/anomalyco/yolo-runner" {
			t.Fatalf("expected probe path /repos/anomalyco/yolo-runner, got %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer ghp_test" {
			t.Fatalf("expected bearer authorization, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"full_name":"anomalyco/yolo-runner"}`))
	}))
	t.Cleanup(server.Close)

	manager, err := NewTaskManager(Config{
		Owner:       "anomalyco",
		Repo:        "yolo-runner",
		Token:       "ghp_test",
		APIEndpoint: server.URL,
		HTTPClient:  server.Client(),
	})
	if err != nil {
		t.Fatalf("expected valid auth probe, got %v", err)
	}
	if manager == nil {
		t.Fatalf("expected non-nil task manager")
	}
}

func TestNewTaskManagerWrapsProbeAuthErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":"Bad credentials"}`))
	}))
	t.Cleanup(server.Close)

	_, err := NewTaskManager(Config{
		Owner:       "anomalyco",
		Repo:        "yolo-runner",
		Token:       "ghp_invalid",
		APIEndpoint: server.URL,
		HTTPClient:  server.Client(),
	})
	if err == nil {
		t.Fatalf("expected auth probe failure")
	}
	if !strings.Contains(err.Error(), "github auth validation failed") {
		t.Fatalf("expected wrapped auth failure, got %q", err.Error())
	}
	if !strings.Contains(strings.ToLower(err.Error()), "bad credentials") {
		t.Fatalf("expected probe details to be preserved, got %q", err.Error())
	}
}
