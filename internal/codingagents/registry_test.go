package codingagents

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/distributed"
)

func TestAgentRegistryLoadIncludesCustomAgents(t *testing.T) {
	repoRoot := t.TempDir()
	customDir := filepath.Join(repoRoot, ".yolo-runner", "coding-agents")
	if err := os.MkdirAll(customDir, 0o755); err != nil {
		t.Fatalf("create custom agent directory: %v", err)
	}

	customPath := filepath.Join(customDir, "custom-cli.yaml")
	if err := os.WriteFile(customPath, []byte(`
name: custom-cli
adapter: command
binary: /usr/bin/custom-cli
args:
  - "--prompt"
  - "{{prompt}}"
supports_review: true
supports_stream: true
distributed_capabilities:
  - implement
`), 0o644); err != nil {
		t.Fatalf("write custom agent: %v", err)
	}

	registry := NewAgentRegistry(repoRoot)
	if err := registry.Load(); err != nil {
		t.Fatalf("load registry: %v", err)
	}

	catalog := registry.Catalog()
	if _, ok := catalog.Backend("custom-cli"); !ok {
		t.Fatalf("expected custom agent to be loaded")
	}
	if _, ok := catalog.Backend("codex"); !ok {
		t.Fatalf("expected builtin codex agent to be loaded")
	}
}

func TestAgentRegistryDiscoverByCapability(t *testing.T) {
	registry := NewAgentRegistry("")
	if err := registry.Register(BackendDefinition{
		Name:            "implementer",
		Adapter:         "command",
		Binary:          "/usr/bin/implementer",
		Health:          &BackendHealthConfig{Enabled: false},
		DistributedCaps: []distributed.Capability{distributed.CapabilityImplement},
	}); err != nil {
		t.Fatalf("register implementer: %v", err)
	}
	if err := registry.Register(BackendDefinition{
		Name:    "reviewer",
		Adapter: "command",
		Binary:  "/usr/bin/reviewer",
		Health:  &BackendHealthConfig{Enabled: false},
		DistributedCaps: []distributed.Capability{
			distributed.CapabilityImplement,
			distributed.CapabilityReview,
		},
	}); err != nil {
		t.Fatalf("register reviewer: %v", err)
	}
	if err := registry.Register(BackendDefinition{
		Name:            "review-only",
		Adapter:         "command",
		Binary:          "/usr/bin/review-only",
		Health:          &BackendHealthConfig{Enabled: false},
		DistributedCaps: []distributed.Capability{distributed.CapabilityReview},
	}); err != nil {
		t.Fatalf("register review-only: %v", err)
	}

	implementations := registry.Discover(distributed.CapabilityImplement)
	if len(implementations) != 2 {
		t.Fatalf("expected 2 implement-capable agents, got %d", len(implementations))
	}
	reviews := registry.Discover(distributed.CapabilityReview)
	if len(reviews) != 2 {
		t.Fatalf("expected 2 review-capable agents, got %d", len(reviews))
	}
	reviewImplement := registry.Discover(distributed.CapabilityImplement, distributed.CapabilityReview)
	if len(reviewImplement) != 1 {
		t.Fatalf("expected 1 implement+review agent, got %d", len(reviewImplement))
	}
	if reviewImplement[0].Name != "reviewer" {
		t.Fatalf("expected reviewer to be returned for implement+review, got %q", reviewImplement[0].Name)
	}
}

func TestAgentRegistryHealthCheckAll(t *testing.T) {
	registry := NewAgentRegistry("")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			w.WriteHeader(http.StatusOK)
		case "/bad":
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	if err := registry.Register(BackendDefinition{
		Name:    "endpoint-ok",
		Adapter: "command",
		Binary:  "/usr/bin/endpoint-ok",
		Health: &BackendHealthConfig{
			Enabled:  true,
			Endpoint: server.URL + "/ok",
			Timeout:  "250ms",
		},
	}); err != nil {
		t.Fatalf("register endpoint-ok: %v", err)
	}
	if err := registry.Register(BackendDefinition{
		Name:    "endpoint-bad",
		Adapter: "command",
		Binary:  "/usr/bin/endpoint-bad",
		Health: &BackendHealthConfig{
			Enabled:  true,
			Endpoint: server.URL + "/bad",
			Timeout:  "250ms",
		},
	}); err != nil {
		t.Fatalf("register endpoint-bad: %v", err)
	}
	if err := registry.Register(BackendDefinition{
		Name:    "command-ok",
		Adapter: "command",
		Binary:  "/usr/bin/command-ok",
		Health: &BackendHealthConfig{
			Enabled: true,
			Command: "ping",
			Timeout: "250ms",
		},
	}); err != nil {
		t.Fatalf("register command-ok: %v", err)
	}
	if err := registry.Register(BackendDefinition{
		Name:    "command-bad",
		Adapter: "command",
		Binary:  "/usr/bin/command-bad",
		Health: &BackendHealthConfig{
			Enabled: true,
			Command: "fail",
			Timeout: "250ms",
		},
	}); err != nil {
		t.Fatalf("register command-bad: %v", err)
	}
	registry.runCommand = func(_ context.Context, name string, args ...string) ([]byte, error) {
		switch name {
		case "ping":
			return []byte("ok"), nil
		case "fail":
			return []byte("oops"), errors.New("command failed")
		default:
			return nil, errors.New("unexpected command")
		}
	}

	results := registry.HealthCheckAll(context.Background())
	byName := map[string]AgentHealthResult{}
	for _, result := range results {
		byName[result.Name] = result
	}

	if got, ok := byName["endpoint-ok"]; !ok || !got.Healthy || got.Check != "endpoint" {
		t.Fatalf("endpoint-ok should be healthy endpoint: %#v", got)
	}
	if got, ok := byName["endpoint-bad"]; !ok || got.Healthy || got.Check != "endpoint" {
		t.Fatalf("endpoint-bad should be unhealthy endpoint: %#v", got)
	}
	if got, ok := byName["command-ok"]; !ok || !got.Healthy || got.Check != "command" {
		t.Fatalf("command-ok should be healthy command: %#v", got)
	}
	if got, ok := byName["command-bad"]; !ok || got.Healthy || got.Check != "command" {
		t.Fatalf("command-bad should be unhealthy command: %#v", got)
	}
}

func TestAgentRegistryWatchReloadsOnConfigChange(t *testing.T) {
	repoRoot := t.TempDir()
	customDir := filepath.Join(repoRoot, ".yolo-runner", "coding-agents")
	if err := os.MkdirAll(customDir, 0o755); err != nil {
		t.Fatalf("create custom agent directory: %v", err)
	}

	initialPath := filepath.Join(customDir, "agent-a.yaml")
	if err := os.WriteFile(initialPath, []byte(`
name: agent-a
adapter: command
binary: /usr/bin/agent-a
`), 0o644); err != nil {
		t.Fatalf("write initial agent: %v", err)
	}

	registry := NewAgentRegistry(repoRoot)
	events := make(chan RegistryWatchEvent, 4)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- registry.Watch(ctx, 25*time.Millisecond, func(event RegistryWatchEvent) {
			events <- event
		})
	}()

	first := waitForRegistryEvent(t, events, 2*time.Second)
	if first.Err != nil {
		t.Fatalf("initial watch event error: %v", first.Err)
	}
	if _, ok := first.Catalog.Backend("agent-a"); !ok {
		t.Fatalf("expected initial catalog to include agent-a")
	}

	updatedPath := filepath.Join(customDir, "agent-b.yaml")
	if err := os.WriteFile(updatedPath, []byte(`
name: agent-b
adapter: command
binary: /usr/bin/agent-b
`), 0o644); err != nil {
		t.Fatalf("write updated agent: %v", err)
	}

	second := waitForRegistryEvent(t, events, 2*time.Second)
	if second.Err != nil {
		t.Fatalf("watch event error after update: %v", second.Err)
	}
	if _, ok := second.Catalog.Backend("agent-b"); !ok {
		t.Fatalf("expected updated catalog to include agent-b")
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("watch returned unexpected error: %v", err)
	}
}

func waitForRegistryEvent(t *testing.T, events <-chan RegistryWatchEvent, timeout time.Duration) RegistryWatchEvent {
	t.Helper()
	select {
	case event := <-events:
		return event
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for registry event")
	}
	return RegistryWatchEvent{}
}
