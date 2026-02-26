package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveRunnerDistributedBusConfigUsesConfigDefaults(t *testing.T) {
	repo := t.TempDir()
	configDir := filepath.Join(repo, ".yolo-runner")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(`distributed_bus:
  backend: nats
  address: nats://config:4222
  prefix: cfg
  stream: jobs
  group: workers
  durable: runner-durable
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := resolveRunnerDistributedBusConfig(repo, "", "", "", func(string) string { return "" })
	if err != nil {
		t.Fatalf("resolve config: %v", err)
	}
	if cfg.Backend != "nats" || cfg.Address != "nats://config:4222" || cfg.Prefix != "cfg" {
		t.Fatalf("unexpected resolved config: %#v", cfg)
	}
	if cfg.Stream != "jobs" || cfg.Group != "workers" || cfg.Durable != "runner-durable" {
		t.Fatalf("expected stream/group/durable from config, got %#v", cfg)
	}
}
