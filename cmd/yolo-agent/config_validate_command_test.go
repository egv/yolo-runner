package main

import (
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
	if !strings.Contains(stderrText, "agent.concurrency") {
		t.Fatalf("expected validation reason in output, got %q", stderrText)
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
