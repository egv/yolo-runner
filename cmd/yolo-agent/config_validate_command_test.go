package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
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
arc_review_watch:
  reviewer: alice
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

func TestRunConfigValidateCommandValidatesTrackerAgentConfig(t *testing.T) {
	tests := []struct {
		name      string
		config    string
		wantField string
		wantCause string
	}{
		{
			name: "non positive poll interval",
			config: `
profiles:
  default:
    tracker:
      type: tk
tracker_agent:
  poll_interval: 0s
`,
			wantField: "tracker_agent.poll_interval",
			wantCause: "must be greater than 0",
		},
		{
			name: "empty lock path",
			config: `
profiles:
  default:
    tracker:
      type: tk
tracker_agent:
  lock_path: ""
`,
			wantField: "tracker_agent.lock_path",
			wantCause: "must not be empty",
		},
		{
			name: "empty label",
			config: `
profiles:
  default:
    tracker:
      type: tk
tracker_agent:
  labels:
    ready: ""
`,
			wantField: "tracker_agent.labels.ready",
			wantCause: "must not be empty",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repoRoot := t.TempDir()
			writeTrackerConfigYAML(t, repoRoot, tc.config)

			stdoutText, stderrText := captureOutput(t, func() {
				code := runConfigValidateCommand([]string{"--repo", repoRoot})
				if code != 1 {
					t.Fatalf("expected exit code 1, got %d", code)
				}
			})

			if stdoutText != "" {
				t.Fatalf("expected no stdout output for invalid config, got %q", stdoutText)
			}
			if !strings.Contains(stderrText, "field: "+tc.wantField) {
				t.Fatalf("expected field %s in output, got %q", tc.wantField, stderrText)
			}
			if !strings.Contains(stderrText, "reason: "+tc.wantCause) {
				t.Fatalf("expected reason to contain %q, got %q", tc.wantCause, stderrText)
			}
			if !strings.Contains(stderrText, "remediation:") {
				t.Fatalf("expected remediation guidance in output, got %q", stderrText)
			}
		})
	}
}

func TestRunConfigValidateCommandValidatesArcReviewWatchConfig(t *testing.T) {
	tests := []struct {
		name      string
		config    string
		wantField string
		wantCause string
	}{
		{
			name: "non positive poll interval",
			config: `
profiles:
  default:
    tracker:
      type: tk
arc_review_watch:
  poll_interval: 0s
`,
			wantField: "arc_review_watch.poll_interval",
			wantCause: "must be greater than 0",
		},
		{
			name: "empty state path",
			config: `
profiles:
  default:
    tracker:
      type: tk
arc_review_watch:
  state_path: ""
`,
			wantField: "arc_review_watch.state_path",
			wantCause: "must not be empty",
		},
		{
			name: "invalid allow ship",
			config: `
profiles:
  default:
    tracker:
      type: tk
arc_review_watch:
  allow_ship: maybe
`,
			wantField: "arc_review_watch.allow_ship",
			wantCause: "must be true or false",
		},
		{
			name: "removed workspaces",
			config: `
profiles:
  default:
    tracker:
      type: tk
arc_review_watch:
  workspaces:
    - /arcadia/reviews/a
`,
			wantField: "arc_review_watch.workspaces",
			wantCause: "no longer supported",
		},
		{
			name: "removed branches",
			config: `
profiles:
  default:
    tracker:
      type: tk
arc_review_watch:
  branches:
    - trunk
`,
			wantField: "arc_review_watch.branches",
			wantCause: "no longer supported",
		},
		{
			name: "empty objects base dir",
			config: `
profiles:
  default:
    tracker:
      type: tk
arc_review_watch:
  objects_base_dir: ""
`,
			wantField: "arc_review_watch.objects_base_dir",
			wantCause: "must not be empty",
		},
		{
			name: "empty mounts base dir",
			config: `
profiles:
  default:
    tracker:
      type: tk
arc_review_watch:
  mounts_base_dir: ""
`,
			wantField: "arc_review_watch.mounts_base_dir",
			wantCause: "must not be empty",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repoRoot := t.TempDir()
			writeTrackerConfigYAML(t, repoRoot, tc.config)

			stdoutText, stderrText := captureOutput(t, func() {
				code := runConfigValidateCommand([]string{"--repo", repoRoot})
				if code != 1 {
					t.Fatalf("expected exit code 1, got %d", code)
				}
			})

			if stdoutText != "" {
				t.Fatalf("expected no stdout output for invalid config, got %q", stdoutText)
			}
			if !strings.Contains(stderrText, "field: "+tc.wantField) {
				t.Fatalf("expected field %s in output, got %q", tc.wantField, stderrText)
			}
			if !strings.Contains(stderrText, "reason: "+tc.wantCause) {
				t.Fatalf("expected reason to contain %q, got %q", tc.wantCause, stderrText)
			}
			if !strings.Contains(stderrText, "remediation:") {
				t.Fatalf("expected remediation guidance in output, got %q", stderrText)
			}
		})
	}
}

func TestRunConfigValidateCommandValidWatchConfig(t *testing.T) {
	repoRoot := t.TempDir()
	writeTrackerConfigYAML(t, repoRoot, `
profiles:
  default:
    tracker:
      type: tk
watch:
  queue_path: queue/watch.db
  sources:
    - name: arcpr-source
      type: arcpr
      profile: arc-review
  runner_pools:
    - name: arcpr-pool
      source: arcpr-source
      presets: [arc-review]
      min_capacity: 1
      max_capacity: 2
  autoscale:
    min_runners: 1
    max_runners: 2
  tui:
    default_mode: stream
`)

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

func TestRunConfigValidateCommandValidWorkspaceWideBRWatchConfig(t *testing.T) {
	repoRoot := t.TempDir()
	mkdirBeadsWorkspace(t, repoRoot)
	writeTrackerConfigYAML(t, repoRoot, `
profiles:
  default:
    tracker:
      type: tk
watch:
  queue_path: queue/watch.db
  sources:
    - name: br-source
      type: br
      preset: yolo-runner
  runner_pools:
    - name: br-pool
      source: br-source
      presets: [yolo-runner]
      min_capacity: 1
      max_capacity: 2
`)

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

func TestRunConfigValidateCommandValidRootScopedBRWatchConfig(t *testing.T) {
	repoRoot := t.TempDir()
	mkdirBeadsWorkspace(t, repoRoot)
	writeTrackerConfigYAML(t, repoRoot, `
profiles:
  default:
    tracker:
      type: tk
watch:
  queue_path: queue/watch.db
  sources:
    - name: br-source
      type: br
      preset: yolo-runner
      root: yolo-epic
  runner_pools:
    - name: br-pool
      source: br-source
      presets: [yolo-runner]
`)

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

func TestRunConfigValidateCommandRejectsInvalidWatchConfig(t *testing.T) {
	tests := []struct {
		name      string
		config    string
		setup     func(*testing.T, string)
		wantField string
		wantCause string
	}{
		{
			name: "missing queue path",
			config: `
profiles:
  default:
    tracker:
      type: tk
watch:
  queue_path: ""
  sources:
    - name: arcpr-source
      type: arcpr
      profile: arc-review
  runner_pools:
    - name: arcpr-pool
      source: arcpr-source
      presets: [arc-review]
      min_capacity: 2
      max_capacity: 4
`,
			wantField: "watch.queue_path",
			wantCause: "must not be empty",
		},
		{
			name: "missing source profile",
			config: `
profiles:
  default:
    tracker:
      type: tk
watch:
  queue_path: queue/watch.db
  sources:
    - name: arcpr-source
      type: arcpr
  runner_pools:
    - name: arcpr-pool
      source: arcpr-source
      presets: [arc-review]
      min_capacity: 2
      max_capacity: 4
`,
			wantField: "watch.sources[0].profile",
			wantCause: "must not be empty",
		},
		{
			name: "invalid source type",
			config: `
profiles:
  default:
    tracker:
      type: tk
watch:
  queue_path: queue/watch.db
  sources:
    - name: unknown-source
      type: invalid
      profile: arc-review
  runner_pools:
    - name: arcpr-pool
      source: unknown-source
      presets: [arc-review]
      min_capacity: 2
      max_capacity: 4
`,
			wantField: "watch.sources[0].type",
			wantCause: "must be one of",
		},
		{
			name: "invalid runner pool bounds",
			config: `
profiles:
  default:
    tracker:
      type: tk
watch:
  queue_path: queue/watch.db
  sources:
    - name: arcpr-source
      type: arcpr
      profile: arc-review
  runner_pools:
    - name: arcpr-pool
      source: arcpr-source
      presets: [arc-review]
      min_capacity: 6
      max_capacity: 5
			`,
			wantField: "watch.runner_pools[0].max_capacity",
			wantCause: "must be greater than or equal",
		},
		{
			name: "invalid autoscale limits",
			config: `
profiles:
  default:
    tracker:
      type: tk
watch:
  queue_path: queue/watch.db
  sources:
    - name: arcpr-source
      type: arcpr
      profile: arc-review
  runner_pools:
    - name: arcpr-pool
      source: arcpr-source
      presets: [arc-review]
      min_capacity: 2
      max_capacity: 4
  autoscale:
    min_runners: 5
    max_runners: 2
			`,
			wantField: "watch.autoscale.max_runners",
			wantCause: "must be greater than or equal",
		},
		{
			name: "missing br preset",
			config: `
profiles:
  default:
    tracker:
      type: tk
watch:
  queue_path: queue/watch.db
  sources:
    - name: br-source
      type: br
  runner_pools:
    - name: br-pool
      source: br-source
      presets: [yolo-runner]
`,
			setup:     mkdirBeadsWorkspace,
			wantField: "watch.sources[0].preset",
			wantCause: "must not be empty",
		},
		{
			name: "missing br workspace",
			config: `
profiles:
  default:
    tracker:
      type: tk
watch:
  queue_path: queue/watch.db
  sources:
    - name: br-source
      type: br
      preset: yolo-runner
  runner_pools:
    - name: br-pool
      source: br-source
      presets: [yolo-runner]
`,
			wantField: "watch.sources[0].repo",
			wantCause: "must contain a .beads workspace",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repoRoot := t.TempDir()
			if tc.setup != nil {
				tc.setup(t, repoRoot)
			}
			writeTrackerConfigYAML(t, repoRoot, tc.config)

			stdoutText, stderrText := captureOutput(t, func() {
				code := runConfigValidateCommand([]string{"--repo", repoRoot})
				if code != 1 {
					t.Fatalf("expected exit code 1, got %d", code)
				}
			})

			if stdoutText != "" {
				t.Fatalf("expected no stdout output for invalid config, got %q", stdoutText)
			}
			if !strings.Contains(stderrText, "field: "+tc.wantField) {
				t.Fatalf("expected field %s in output, got %q", tc.wantField, stderrText)
			}
			if !strings.Contains(stderrText, "reason: "+tc.wantCause) {
				t.Fatalf("expected reason to contain %q, got %q", tc.wantCause, stderrText)
			}
			if !strings.Contains(stderrText, "remediation:") {
				t.Fatalf("expected remediation guidance in output, got %q", stderrText)
			}
		})
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

func mkdirBeadsWorkspace(t *testing.T, repoRoot string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(repoRoot, ".beads"), 0o755); err != nil {
		t.Fatalf("mkdir .beads: %v", err)
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
