package distributed

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const trackerConfigRelPath = ".yolo-runner/config.yaml"

type DistributedBusConfig struct {
	Backend string `yaml:"backend"`
	Address string `yaml:"address"`
	Prefix  string `yaml:"prefix"`
	Source  string `yaml:"source,omitempty"`
	Stream  string `yaml:"stream,omitempty"`
	Group   string `yaml:"group,omitempty"`
	Durable string `yaml:"durable,omitempty"`
}

type trackerDistributedConfigModel struct {
	DistributedBus DistributedBusConfig `yaml:"distributed_bus,omitempty"`
}

func LoadDistributedBusConfig(repoRoot string) (DistributedBusConfig, error) {
	root := strings.TrimSpace(repoRoot)
	if root == "" {
		root = "."
	}
	configPath := filepath.Join(root, trackerConfigRelPath)
	content, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return DistributedBusConfig{}, nil
		}
		return DistributedBusConfig{}, fmt.Errorf("cannot read config file at %s: %w", trackerConfigRelPath, err)
	}

	var model trackerDistributedConfigModel
	decoder := yaml.NewDecoder(strings.NewReader(string(content)))
	if err := decoder.Decode(&model); err != nil {
		return DistributedBusConfig{}, fmt.Errorf("cannot parse config file at %s: %w", trackerConfigRelPath, err)
	}
	cfg := model.DistributedBus
	cfg.Backend = strings.TrimSpace(cfg.Backend)
	cfg.Address = strings.TrimSpace(cfg.Address)
	cfg.Prefix = strings.TrimSpace(cfg.Prefix)
	cfg.Source = strings.TrimSpace(cfg.Source)
	cfg.Stream = strings.TrimSpace(cfg.Stream)
	cfg.Group = strings.TrimSpace(cfg.Group)
	cfg.Durable = strings.TrimSpace(cfg.Durable)
	return cfg, nil
}

func (c DistributedBusConfig) ApplyDefaults(defaultBackend string, defaultPrefix string) DistributedBusConfig {
	out := c
	if strings.TrimSpace(out.Backend) == "" {
		out.Backend = strings.TrimSpace(defaultBackend)
	}
	if strings.TrimSpace(out.Prefix) == "" {
		out.Prefix = strings.TrimSpace(defaultPrefix)
	}
	return out
}

func (c DistributedBusConfig) BackendOptions() BusBackendOptions {
	return BusBackendOptions{
		Stream:  strings.TrimSpace(c.Stream),
		Group:   strings.TrimSpace(c.Group),
		Durable: strings.TrimSpace(c.Durable),
	}
}
