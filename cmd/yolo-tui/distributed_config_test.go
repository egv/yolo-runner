package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveTUIDistributedBusConfigUsesConfigDefaults(t *testing.T) {
	repo := t.TempDir()
	configDir := filepath.Join(repo, ".yolo-runner")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(`distributed_bus:
  backend: nats
  address: nats://127.0.0.1:4222
  prefix: tui
  source: monitor-2
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := resolveTUIDistributedBusConfig(repo, "", "", "", "", func(string) string { return "" })
	if err != nil {
		t.Fatalf("resolve config: %v", err)
	}
	if cfg.Backend != "nats" || cfg.Address != "nats://127.0.0.1:4222" || cfg.Prefix != "tui" || cfg.Source != "monitor-2" {
		t.Fatalf("unexpected resolved config: %#v", cfg)
	}
}
