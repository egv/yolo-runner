package main

import (
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/codingagents"
	"github.com/egv/yolo-runner/v2/internal/opencode"
)

func TestBuildAgentRunnerUsesCatalogBackendDefinition(t *testing.T) {
	catalog, err := codingagents.LoadCatalog("")
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}

	runner, err := buildAgentRunner(catalog, backendOpenCodeACP, "model-from-config", 30*time.Second)
	if err != nil {
		t.Fatalf("build agent runner: %v", err)
	}
	if _, ok := runner.(*opencode.CLIRunnerAdapter); !ok {
		t.Fatalf("expected *opencode.CLIRunnerAdapter for %q backend, got %T", backendOpenCodeACP, runner)
	}
}
