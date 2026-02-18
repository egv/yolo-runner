package main

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
)

func TestRunConfigValidateCommandValidConfigReturnsZeroWithDeterministicOutput(t *testing.T) {
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
          token_env: LINEAR_TOKEN
agent:
  backend: codex
  concurrency: 2
  watchdog_timeout: 10m
  watchdog_interval: 5s
`)
	t.Setenv("LINEAR_TOKEN", "lin_api_token")

	stdoutText, stderrText := captureOutput(t, func() {
		code := runConfigValidateCommand([]string{"--repo", repoRoot})
		if code != 0 {
			t.Fatalf("expected exit code 0, got %d", code)
		}
	})

	if stdoutText != "config is valid\n" {
		t.Fatalf("expected deterministic success output, got %q", stdoutText)
	}
	if stderrText != "" {
		t.Fatalf("expected no stderr output for valid config, got %q", stderrText)
	}
}

func TestRunConfigValidateCommandInvalidConfigReturnsOneWithDeterministicOutput(t *testing.T) {
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
		code := runConfigValidateCommand([]string{"--repo", repoRoot})
		if code != 1 {
			t.Fatalf("expected exit code 1, got %d", code)
		}
	})

	if stdoutText != "" {
		t.Fatalf("expected no stdout output for invalid config, got %q", stdoutText)
	}
	if !strings.Contains(stderrText, "config is invalid") {
		t.Fatalf("expected deterministic invalid prefix, got %q", stderrText)
	}
	if !strings.Contains(stderrText, "field: agent.concurrency") {
		t.Fatalf("expected failing field in output, got %q", stderrText)
	}
	if !strings.Contains(stderrText, "reason: must be greater than 0") {
		t.Fatalf("expected validation reason in output, got %q", stderrText)
	}
	if !strings.Contains(stderrText, "remediation:") {
		t.Fatalf("expected remediation guidance in output, got %q", stderrText)
	}
}

func TestRunConfigValidateCommandInvalidConfigJSONOutputIsMachineReadable(t *testing.T) {
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
		code := runConfigValidateCommand([]string{"--repo", repoRoot, "--format", "json"})
		if code != 1 {
			t.Fatalf("expected exit code 1, got %d", code)
		}
	})
	if stderrText != "" {
		t.Fatalf("expected no stderr output for machine-readable mode, got %q", stderrText)
	}

	var payload configValidateResultPayload
	if err := json.Unmarshal([]byte(stdoutText), &payload); err != nil {
		t.Fatalf("expected valid JSON payload, got %q (%v)", stdoutText, err)
	}
	if payload.SchemaVersion != configValidateSchemaVersion {
		t.Fatalf("expected schema version %q, got %q", configValidateSchemaVersion, payload.SchemaVersion)
	}
	if payload.Status != "invalid" {
		t.Fatalf("expected invalid status, got %q", payload.Status)
	}
	if len(payload.Diagnostics) != 1 {
		t.Fatalf("expected one diagnostic, got %d", len(payload.Diagnostics))
	}
	diag := payload.Diagnostics[0]
	if diag.Field != "agent.concurrency" {
		t.Fatalf("expected field agent.concurrency, got %q", diag.Field)
	}
	if !strings.Contains(diag.Reason, "greater than 0") {
		t.Fatalf("expected reason to describe numeric constraint, got %q", diag.Reason)
	}
	if strings.TrimSpace(diag.Remediation) == "" {
		t.Fatalf("expected remediation guidance to be present")
	}
}

func TestRunConfigValidateCommandValidConfigJSONOutputUsesStableSchema(t *testing.T) {
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
          token_env: LINEAR_TOKEN
agent:
  backend: codex
  concurrency: 2
`)
	t.Setenv("LINEAR_TOKEN", "lin_api_token")

	stdoutText, stderrText := captureOutput(t, func() {
		code := runConfigValidateCommand([]string{"--repo", repoRoot, "--format", "json"})
		if code != 0 {
			t.Fatalf("expected exit code 0, got %d", code)
		}
	})
	if stderrText != "" {
		t.Fatalf("expected no stderr output for machine-readable mode, got %q", stderrText)
	}

	var payload configValidateResultPayload
	if err := json.Unmarshal([]byte(stdoutText), &payload); err != nil {
		t.Fatalf("expected valid JSON payload, got %q (%v)", stdoutText, err)
	}
	if payload.SchemaVersion != configValidateSchemaVersion {
		t.Fatalf("expected schema version %q, got %q", configValidateSchemaVersion, payload.SchemaVersion)
	}
	if payload.Status != "valid" {
		t.Fatalf("expected valid status, got %q", payload.Status)
	}
	if len(payload.Diagnostics) != 0 {
		t.Fatalf("expected no diagnostics for valid config, got %d", len(payload.Diagnostics))
	}
}

func TestRunConfigValidateCommandValidatesBackendFromConfigNotEnvOverride(t *testing.T) {
	repoRoot := t.TempDir()
	writeTrackerConfigYAML(t, repoRoot, `
profiles:
  default:
    tracker:
      type: tk
agent:
  backend: unsupported
`)
	t.Setenv("YOLO_AGENT_BACKEND", "codex")

	stdoutText, stderrText := captureOutput(t, func() {
		code := runConfigValidateCommand([]string{"--repo", repoRoot})
		if code != 1 {
			t.Fatalf("expected exit code 1, got %d", code)
		}
	})

	if stdoutText != "" {
		t.Fatalf("expected no stdout output for invalid config, got %q", stdoutText)
	}
	if !strings.Contains(stderrText, "field: agent.backend") {
		t.Fatalf("expected backend failure from config value, got %q", stderrText)
	}
}

func TestRunConfigValidateCommandProfileFlagOverridesYOLOProfileEnv(t *testing.T) {
	repoRoot := t.TempDir()
	writeTrackerConfigYAML(t, repoRoot, `
default_profile: default
profiles:
  default:
    tracker:
      type: tk
  qa:
    tracker:
      type: linear
      linear:
        scope:
          workspace: anomaly
        auth:
          token_env: LINEAR_TOKEN
`)
	t.Setenv("YOLO_PROFILE", "qa")

	stdoutText, stderrText := captureOutput(t, func() {
		code := runConfigValidateCommand([]string{"--repo", repoRoot, "--profile", "default"})
		if code != 0 {
			t.Fatalf("expected exit code 0 when profile flag overrides env, got %d", code)
		}
	})

	if stdoutText != "config is valid\n" {
		t.Fatalf("expected deterministic success output, got %q", stdoutText)
	}
	if stderrText != "" {
		t.Fatalf("expected no stderr output for valid config, got %q", stderrText)
	}
}

func captureOutput(t *testing.T, fn func()) (string, string) {
	t.Helper()

	originalStdout := os.Stdout
	originalStderr := os.Stderr
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}

	os.Stdout = stdoutWriter
	os.Stderr = stderrWriter
	defer func() {
		os.Stdout = originalStdout
		os.Stderr = originalStderr
	}()

	fn()

	if err := stdoutWriter.Close(); err != nil {
		t.Fatalf("close stdout writer: %v", err)
	}
	if err := stderrWriter.Close(); err != nil {
		t.Fatalf("close stderr writer: %v", err)
	}
	stdoutBytes, err := io.ReadAll(stdoutReader)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	stderrBytes, err := io.ReadAll(stderrReader)
	if err != nil {
		t.Fatalf("read stderr: %v", err)
	}
	if err := stdoutReader.Close(); err != nil {
		t.Fatalf("close stdout reader: %v", err)
	}
	if err := stderrReader.Close(); err != nil {
		t.Fatalf("close stderr reader: %v", err)
	}
	return string(stdoutBytes), string(stderrBytes)
}
