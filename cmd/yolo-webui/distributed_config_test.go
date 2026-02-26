package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveWebUIDistributedBusConfigUsesConfigDefaults(t *testing.T) {
	repo := t.TempDir()
	configDir := filepath.Join(repo, ".yolo-runner")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(`distributed_bus:
  backend: redis
  address: redis://127.0.0.1:6380
  prefix: web
  source: webui-1
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := resolveWebUIDistributedBusConfig(repo, "", "", "", "", func(string) string { return "" })
	if err != nil {
		t.Fatalf("resolve config: %v", err)
	}
	if cfg.Backend != "redis" || cfg.Address != "redis://127.0.0.1:6380" || cfg.Prefix != "web" || cfg.Source != "webui-1" {
		t.Fatalf("unexpected resolved config: %#v", cfg)
	}
}
