package docs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadmeMentionsCloseEligible(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	readmePath := filepath.Join(repoRoot, "README.md")
	contents, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("read README: %v", err)
	}

	if !strings.Contains(string(contents), "bd epic close-eligible") {
		t.Fatalf("README missing bd epic close-eligible step")
	}
}

func TestReadmeDocumentsRunnerTimeoutProfiles(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	readmePath := filepath.Join(repoRoot, "README.md")
	contents, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("read README: %v", err)
	}

	readme := string(contents)
	if !strings.Contains(readme, "--runner-timeout") {
		t.Fatalf("README missing --runner-timeout guidance")
	}
	if !strings.Contains(readme, "Local profile") {
		t.Fatalf("README missing local timeout profile")
	}
	if !strings.Contains(readme, "CI profile") {
		t.Fatalf("README missing CI timeout profile")
	}
	if !strings.Contains(readme, "Long-task profile") {
		t.Fatalf("README missing long-task timeout profile")
	}
	if !strings.Contains(readme, "0s") {
		t.Fatalf("README missing default timeout behavior")
	}
	if !strings.Contains(readme, "10m") {
		t.Fatalf("README missing local timeout recommendation")
	}
	if !strings.Contains(readme, "20m") {
		t.Fatalf("README missing CI timeout recommendation")
	}
	if !strings.Contains(readme, "45m") {
		t.Fatalf("README missing long-task timeout recommendation")
	}
}

func TestReadmeDocumentsYoloAgentConfigPrecedenceAndValidation(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	readmePath := filepath.Join(repoRoot, "README.md")
	contents, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("read README: %v", err)
	}

	readme := string(contents)
	required := []string{
		".yolo-runner/config.yaml",
		"--agent-backend > --backend > YOLO_AGENT_BACKEND > agent.backend > opencode",
		"--profile > YOLO_PROFILE > default_profile > default",
		"agent.concurrency",
		"agent.runner_timeout",
		"agent.watchdog_timeout",
		"agent.watchdog_interval",
		"agent.retry_budget",
	}
	for _, needle := range required {
		if !strings.Contains(readme, needle) {
			t.Fatalf("README missing yolo-agent config guidance: %q", needle)
		}
	}
}

func TestMigrationDocumentsYoloAgentConfigDefaults(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	migrationPath := filepath.Join(repoRoot, "MIGRATION.md")
	contents, err := os.ReadFile(migrationPath)
	if err != nil {
		t.Fatalf("read MIGRATION: %v", err)
	}

	migration := string(contents)
	required := []string{
		"Config Defaults and Precedence",
		".yolo-runner/config.yaml",
		"YOLO_AGENT_BACKEND",
		"YOLO_PROFILE",
		"agent.watchdog_timeout",
	}
	for _, needle := range required {
		if !strings.Contains(migration, needle) {
			t.Fatalf("MIGRATION missing yolo-agent config guidance: %q", needle)
		}
	}
}

func TestConfigWorkflowDocsCoverValidateInitAndTroubleshooting(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	readmePath := filepath.Join(repoRoot, "README.md")
	readmeContents, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("read README: %v", err)
	}

	readme := string(readmeContents)
	readmeRequired := []string{
		"yolo-agent config init --repo .",
		"yolo-agent config validate --repo .",
		"--agent-backend > --backend > YOLO_AGENT_BACKEND > agent.backend > opencode",
		"config is invalid",
		"remediation:",
	}
	for _, needle := range readmeRequired {
		if !strings.Contains(readme, needle) {
			t.Fatalf("README missing validate/init workflow guidance: %q", needle)
		}
	}

	runbookPath := filepath.Join(repoRoot, "docs", "config-workflow.md")
	runbookContents, err := os.ReadFile(runbookPath)
	if err != nil {
		t.Fatalf("read config workflow runbook: %v", err)
	}

	runbook := string(runbookContents)
	runbookRequired := []string{
		"Command Usage",
		"Precedence",
		"Common Failures",
		"Remediation",
		"yolo-agent config init",
		"yolo-agent config validate",
		"already exists; rerun with --force to overwrite",
		"unsupported --format value",
		"missing auth token from",
	}
	for _, needle := range runbookRequired {
		if !strings.Contains(runbook, needle) {
			t.Fatalf("config workflow runbook missing %q", needle)
		}
	}
}
