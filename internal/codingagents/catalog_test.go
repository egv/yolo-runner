package codingagents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadCatalogIncludesBuiltinAndCustomBackendDefinitions(t *testing.T) {
	repoRoot := t.TempDir()
	customDir := filepath.Join(repoRoot, ".yolo-runner", "coding-agents")
	if err := os.MkdirAll(customDir, 0o755); err != nil {
		t.Fatalf("create custom backend directory: %v", err)
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
`), 0o644); err != nil {
		t.Fatalf("write custom backend definition: %v", err)
	}

	catalog, err := LoadCatalog(repoRoot)
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	if _, ok := catalog.Backend("custom-cli"); !ok {
		t.Fatalf("expected custom backend to be discovered")
	}
	if _, ok := catalog.Backend("opencode"); !ok {
		t.Fatalf("expected builtin opencode backend to be discovered")
	}
	if _, ok := catalog.Backend("codex-cli"); !ok {
		t.Fatalf("expected builtin codex-cli backend to be discovered")
	}
}

func TestCatalogValidateBackendUsageChecksModelAndCredentials(t *testing.T) {
	repoRoot := t.TempDir()
	customDir := filepath.Join(repoRoot, ".yolo-runner", "coding-agents")
	if err := os.MkdirAll(customDir, 0o755); err != nil {
		t.Fatalf("create custom backend directory: %v", err)
	}

	customPath := filepath.Join(customDir, "guarded.yaml")
	if err := os.WriteFile(customPath, []byte(`
name: guarded
adapter: command
binary: /usr/bin/guarded
args:
  - "--model"
  - "{{model}}"
supported_models:
  - custom-*
required_credentials:
  - CUSTOM_AGENT_TOKEN
supports_review: true
supports_stream: true
`), 0o644); err != nil {
		t.Fatalf("write custom backend definition: %v", err)
	}

	catalog, err := LoadCatalog(repoRoot)
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}

	if err := catalog.ValidateBackendUsage("guarded", "other-model", func(string) string { return "token" }); err == nil {
		t.Fatalf("expected unsupported model validation error")
	}
	if err := catalog.ValidateBackendUsage("guarded", "custom-model", func(string) string { return "" }); err == nil {
		t.Fatalf("expected missing credential validation error")
	}
	if err := catalog.ValidateBackendUsage("guarded", "custom-model", func(name string) string {
		if name == "CUSTOM_AGENT_TOKEN" {
			return "secret-token"
		}
		return ""
	}); err != nil {
		t.Fatalf("expected backend usage validation to succeed with valid model and credential, got %v", err)
	}
}

func containsStringSlice(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func TestCatalogLoadsUnifiedBackendFieldsFromConfig(t *testing.T) {
	repoRoot := t.TempDir()
	customDir := filepath.Join(repoRoot, ".yolo-runner", "coding-agents")
	if err := os.MkdirAll(customDir, 0o755); err != nil {
		t.Fatalf("create custom backend directory: %v", err)
	}

	customPath := filepath.Join(customDir, "unified.yaml")
	if err := os.WriteFile(customPath, []byte(`
name: unified
type: command
backend: unified
model: unified-v1
capabilities:
  languages:
    - go
  features:
    - implement
    - review
    - service_proxy
    - stream
config:
  binary: /usr/bin/unified
  api_key_env: UNIFIED_API_TOKEN
  args: ["--backend=unified"]
  timeout: 10s
distributed_capabilities:
  - implement
  - review
`), 0o644); err != nil {
		t.Fatalf("write unified backend definition: %v", err)
	}

	catalog, err := LoadCatalog(repoRoot)
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}

	definition, ok := catalog.Backend("unified")
	if !ok {
		t.Fatalf("expected unified backend to be discovered")
	}
	if definition.Adapter != "command" {
		t.Fatalf("expected adapter to normalize from type=command, got %q", definition.Adapter)
	}
	if definition.Binary != "/usr/bin/unified" {
		t.Fatalf("expected binary from config to normalize, got %q", definition.Binary)
	}
	if !definition.SupportsReview {
		t.Fatalf("expected review feature to map to SupportsReview")
	}
	if !definition.SupportsStream {
		t.Fatalf("expected stream feature to map to SupportsStream")
	}
	if !strings.Contains(strings.Join(definition.RequiredCredentials, ","), "UNIFIED_API_TOKEN") {
		t.Fatalf("expected api_key_env to map to required credentials, got %#v", definition.RequiredCredentials)
	}
	if !strings.Contains(definition.Model, "unified-v1") {
		t.Fatalf("expected model field to retain unified model, got %#v", definition.Model)
	}
	if !containsStringSlice(definition.RequiredCredentials, "UNIFIED_API_TOKEN") {
		t.Fatalf("expected api_key_env to map to required credentials, got %#v", definition.RequiredCredentials)
	}
	if !strings.Contains(definition.Model, "unified-v1") {
		t.Fatalf("expected configured model to be retained, got %q", definition.Model)
	}
}

func TestCatalogBuiltinOpencodeServeIsRegistered(t *testing.T) {
	catalog, err := LoadCatalog("")
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}

	backend, ok := catalog.Backend("opencode-serve")
	if !ok {
		t.Fatal("expected builtin opencode-serve backend to be registered in the catalog")
	}
	if backend.Adapter != "opencode-serve" {
		t.Errorf("expected adapter %q, got %q", "opencode-serve", backend.Adapter)
	}
	if backend.Binary != "opencode" {
		t.Errorf("expected binary %q, got %q", "opencode", backend.Binary)
	}
	if !backend.SupportsReview {
		t.Error("expected opencode-serve to support review")
	}
	if !backend.SupportsStream {
		t.Error("expected opencode-serve to support stream")
	}
}

func TestCatalogBuiltinOpencodeUsesServeAdapter(t *testing.T) {
	catalog, err := LoadCatalog("")
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}

	backend, ok := catalog.Backend("opencode")
	if !ok {
		t.Fatal("expected builtin opencode backend to be registered in the catalog")
	}
	if backend.Adapter != "opencode-serve" {
		t.Errorf("expected opencode adapter %q, got %q", "opencode-serve", backend.Adapter)
	}
	if len(backend.Args) == 0 || backend.Args[0] != "serve" {
		t.Errorf("expected opencode args to start with %q, got %v", "serve", backend.Args)
	}
}

func TestCatalogBuiltinCodexUsesAppServerAdapter(t *testing.T) {
	catalog, err := LoadCatalog("")
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}

	codex, ok := catalog.Backend("codex")
	if !ok {
		t.Fatal("expected builtin codex backend")
	}
	if codex.Adapter != "codex-app-server" {
		t.Errorf("expected codex adapter %q, got %q", "codex-app-server", codex.Adapter)
	}
	if len(codex.Args) != 3 || codex.Args[0] != "-c" || codex.Args[1] != `model_reasoning_effort="medium"` || codex.Args[2] != "app-server" {
		t.Errorf("expected codex args to configure medium reasoning before app-server, got %v", codex.Args)
	}
	if codex.Model != "gpt-5.6-sol" {
		t.Errorf("expected builtin codex model %q, got %q", "gpt-5.6-sol", codex.Model)
	}
}
