package main

import (
	"strings"
	"testing"
)

func TestE2E_ConfigCommands_InitThenValidateHappyPath(t *testing.T) {
	repoRoot := t.TempDir()

	initStdout, initStderr := captureOutput(t, func() {
		code := RunMain([]string{"config", "init", "--repo", repoRoot}, nil)
		if code != 0 {
			t.Fatalf("expected config init exit code 0, got %d", code)
		}
	})
	if initStderr != "" {
		t.Fatalf("expected no stderr for config init happy path, got %q", initStderr)
	}
	if initStdout != "wrote .yolo-runner/config.yaml\n" {
		t.Fatalf("expected deterministic init output, got %q", initStdout)
	}

	validateStdout, validateStderr := captureOutput(t, func() {
		code := RunMain([]string{"config", "validate", "--repo", repoRoot}, nil)
		if code != 0 {
			t.Fatalf("expected config validate exit code 0, got %d", code)
		}
	})
	if validateStdout != "config is valid\n" {
		t.Fatalf("expected deterministic validate output, got %q", validateStdout)
	}
	if validateStderr != "" {
		t.Fatalf("expected no stderr for valid config, got %q", validateStderr)
	}
}

func TestE2E_ConfigCommands_ValidateMissingFileFallsBackToDefaults(t *testing.T) {
	repoRoot := t.TempDir()

	stdoutText, stderrText := captureOutput(t, func() {
		code := RunMain([]string{"config", "validate", "--repo", repoRoot}, nil)
		if code != 0 {
			t.Fatalf("expected exit code 0 when config file is missing, got %d", code)
		}
	})
	if stdoutText != "config is valid\n" {
		t.Fatalf("expected deterministic success output for missing config file, got %q", stdoutText)
	}
	if stderrText != "" {
		t.Fatalf("expected no stderr output for missing config fallback, got %q", stderrText)
	}
}

func TestE2E_ConfigCommands_ValidateInvalidValuesReportsDeterministicDiagnostics(t *testing.T) {
	repoRoot := t.TempDir()
	writeTrackerConfigYAML(t, repoRoot, `
profiles:
  default:
    tracker:
      type: tk
agent:
  concurrency: 0
`)

	stdoutText, stderrText := captureOutput(t, func() {
		code := RunMain([]string{"config", "validate", "--repo", repoRoot}, nil)
		if code != 1 {
			t.Fatalf("expected exit code 1 for invalid config, got %d", code)
		}
	})
	if stdoutText != "" {
		t.Fatalf("expected no stdout output for invalid config, got %q", stdoutText)
	}
	if !strings.Contains(stderrText, "config is invalid") {
		t.Fatalf("expected invalid prefix, got %q", stderrText)
	}
	if !strings.Contains(stderrText, "field: agent.concurrency") {
		t.Fatalf("expected field diagnostics, got %q", stderrText)
	}
	if !strings.Contains(stderrText, "reason: must be greater than 0") {
		t.Fatalf("expected reason diagnostics, got %q", stderrText)
	}
	if !strings.Contains(stderrText, "remediation:") {
		t.Fatalf("expected remediation diagnostics, got %q", stderrText)
	}
}

func TestE2E_ConfigCommands_ValidateMissingAuthEnvReportsRemediation(t *testing.T) {
	repoRoot := t.TempDir()
	writeTrackerConfigYAML(t, repoRoot, `
profiles:
  default:
    tracker:
      type: linear
      linear:
        scope:
          workspace: anomaly
        auth:
          token_env: LINEAR_TOKEN_E2E_MISSING
`)
	t.Setenv("LINEAR_TOKEN_E2E_MISSING", "")

	stdoutText, stderrText := captureOutput(t, func() {
		code := RunMain([]string{"config", "validate", "--repo", repoRoot}, nil)
		if code != 1 {
			t.Fatalf("expected exit code 1 for missing auth env, got %d", code)
		}
	})
	if stdoutText != "" {
		t.Fatalf("expected no stdout output for missing auth env, got %q", stdoutText)
	}
	if !strings.Contains(stderrText, "config is invalid") {
		t.Fatalf("expected invalid prefix, got %q", stderrText)
	}
	if !strings.Contains(stderrText, "missing auth token from LINEAR_TOKEN_E2E_MISSING") {
		t.Fatalf("expected missing token diagnostics, got %q", stderrText)
	}
	if !strings.Contains(stderrText, "field: linear.auth.token_env") {
		t.Fatalf("expected auth field diagnostics, got %q", stderrText)
	}
	if !strings.Contains(stderrText, "export that variable with your Linear API token") {
		t.Fatalf("expected remediation guidance, got %q", stderrText)
	}
}
