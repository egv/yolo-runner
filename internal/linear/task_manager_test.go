package linear

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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

	if _, err := manager.NextTasks(context.Background(), "root-1"); err == nil {
		t.Fatalf("expected next tasks to be explicitly unsupported until read ops ship")
	}
}
