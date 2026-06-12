package main

import (
	"fmt"
	"time"

	"github.com/egv/yolo-runner/v2/internal/claude"
	"github.com/egv/yolo-runner/v2/internal/codex"
	"github.com/egv/yolo-runner/v2/internal/codingagents"
	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/kimi"
	"github.com/egv/yolo-runner/v2/internal/opencode"
)

func buildAgentRunner(catalog codingagents.Catalog, backend string, model string, timeout time.Duration) (contracts.AgentRunner, error) {
	selectedBackend := normalizeBackend(backend)
	if selectedBackend == "" {
		return nil, fmt.Errorf("unsupported runner backend %q", backend)
	}
	definition, ok := catalog.Backend(selectedBackend)
	if !ok {
		return nil, fmt.Errorf("unsupported runner backend %q", backend)
	}

	switch definition.Adapter {
	case "opencode":
		command := append([]string{}, definition.Args...)
		return opencode.NewCLIRunnerAdapter(opencode.CommandRunner{}, nil, defaultConfigRoot(), defaultConfigDir(), definition.Binary, command...), nil
	case "opencode-serve":
		return opencode.NewServeRunnerAdapter(definition.Binary, definition.Args...), nil
	case "codex", "codex-app-server":
		return codex.NewCLIRunnerAdapter(definition.Binary, nil, definition.Args...), nil
	case "claude":
		return claude.NewSessionRunnerAdapter(definition.Binary), nil
	case "kimi":
		return kimi.NewCLIRunnerAdapter(definition.Binary, nil, definition.Args...), nil
	case "command":
		return codingagents.NewGenericCLIRunnerAdapter(definition.Name, definition.Binary, definition.Args, nil).WithHealthConfig(definition.Health), nil
	default:
		return nil, fmt.Errorf("unsupported runner backend adapter %q", definition.Adapter)
	}
}
