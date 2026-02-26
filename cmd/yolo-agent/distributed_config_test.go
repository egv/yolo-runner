package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveAgentDistributedBusConfigUsesConfigDefaults(t *testing.T) {
	repo := t.TempDir()
	configDir := filepath.Join(repo, ".yolo-runner")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(`profiles:
  default:
    tracker:
      type: tk
agent:
  backend: codex
distributed_bus:
  backend: nats
  address: nats://cfg:4222
  prefix: team
  stream: task-stream
  group: agents
  durable: durable-agent
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := resolveAgentDistributedBusConfig(repo, "", "", "", func(string) string { return "" })
	if err != nil {
		t.Fatalf("resolve distributed bus config: %v", err)
	}
	if cfg.Backend != "nats" || cfg.Address != "nats://cfg:4222" || cfg.Prefix != "team" {
		t.Fatalf("unexpected resolved bus config: %#v", cfg)
	}
	if cfg.Stream != "task-stream" || cfg.Group != "agents" || cfg.Durable != "durable-agent" {
		t.Fatalf("expected stream/group/durable from config, got %#v", cfg)
	}
}
