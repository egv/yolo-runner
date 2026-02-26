package distributed

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDistributedBusConfigParsesYaml(t *testing.T) {
	repo := t.TempDir()
	path := filepath.Join(repo, ".yolo-runner")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := []byte(`distributed_bus:
  backend: nats
  address: nats://localhost:4222
  prefix: team
  stream: tasks-stream
  group: workers
  durable: worker-durable
`)
	if err := os.WriteFile(filepath.Join(path, "config.yaml"), content, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadDistributedBusConfig(repo)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Backend != "nats" || cfg.Address != "nats://localhost:4222" || cfg.Prefix != "team" {
		t.Fatalf("unexpected core config: %#v", cfg)
	}
	if cfg.Stream != "tasks-stream" || cfg.Group != "workers" || cfg.Durable != "worker-durable" {
		t.Fatalf("unexpected queue naming config: %#v", cfg)
	}
}
