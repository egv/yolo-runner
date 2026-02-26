package docs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMakefileHasAgentTUISmokeTarget(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	makefilePath := filepath.Join(repoRoot, "Makefile")
	contents, err := os.ReadFile(makefilePath)
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}

	makefile := string(contents)
	if !strings.Contains(makefile, "smoke-agent-tui:") {
		t.Fatalf("Makefile missing smoke-agent-tui target")
	}
	if !strings.Contains(makefile, "go test ./cmd/yolo-agent ./cmd/yolo-tui") {
		t.Fatalf("smoke-agent-tui target must run yolo-agent/yolo-tui smoke tests")
	}
	if !strings.Contains(makefile, "$(MAKE) smoke-config-commands") {
		t.Fatalf("smoke-agent-tui target must include smoke-config-commands coverage")
	}
}

func TestMakefileHasEventStreamSmokeTarget(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	makefilePath := filepath.Join(repoRoot, "Makefile")
	contents, err := os.ReadFile(makefilePath)
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}

	makefile := string(contents)
	if !strings.Contains(makefile, "smoke-event-stream:") {
		t.Fatalf("Makefile missing smoke-event-stream target")
	}
	if !strings.Contains(makefile, "$(MAKE) smoke-agent-tui") {
		t.Fatalf("smoke-event-stream target must run smoke-agent-tui")
	}
}

func TestMakefileHasDistributedSmokeTarget(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	makefilePath := filepath.Join(repoRoot, "Makefile")
	contents, err := os.ReadFile(makefilePath)
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}

	makefile := string(contents)
	if !strings.Contains(makefile, "smoke-distributed-e2e:") {
		t.Fatalf("Makefile missing smoke-distributed-e2e target")
	}
	if !strings.Contains(makefile, "./scripts/distributed-smoke.sh") {
		t.Fatalf("smoke-distributed-e2e target must invoke scripts/distributed-smoke.sh")
	}
	if !strings.Contains(makefile, "distributed-dev-up:") {
		t.Fatalf("Makefile missing distributed-dev-up target")
	}
	if !strings.Contains(makefile, "docker compose -f dev/distributed/docker-compose.yml up -d redis nats") {
		t.Fatalf("distributed-dev-up target must start redis and nats via docker compose")
	}
}

func TestMakefileHasConfigCommandSmokeTarget(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	makefilePath := filepath.Join(repoRoot, "Makefile")
	contents, err := os.ReadFile(makefilePath)
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}

	makefile := string(contents)
	if !strings.Contains(makefile, "smoke-config-commands:") {
		t.Fatalf("Makefile missing smoke-config-commands target")
	}
	requiredTests := []string{
		"TestE2E_ConfigCommands_(",
		"InitThenValidateHappyPath",
		"ValidateMissingFileFallsBackToDefaults",
		"ValidateInvalidValuesReportsDeterministicDiagnostics",
		"ValidateMissingAuthEnvReportsRemediation",
	}
	for _, testName := range requiredTests {
		if !strings.Contains(makefile, testName) {
			t.Fatalf("smoke-config-commands target missing %q", testName)
		}
	}
}
