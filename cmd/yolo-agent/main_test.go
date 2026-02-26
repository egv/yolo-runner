package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/distributed"
	"github.com/egv/yolo-runner/v2/internal/github"
	"github.com/egv/yolo-runner/v2/internal/linear"
)

type runnerTransportRequest struct {
	TaskID     string               `json:"task_id"`
	ParentID   string               `json:"parent_id"`
	Prompt     string               `json:"prompt"`
	Mode       contracts.RunnerMode `json:"mode"`
	Model      string               `json:"model"`
	RepoRoot   string               `json:"repo_root"`
	Timeout    time.Duration        `json:"timeout"`
	MaxRetries int                  `json:"max_retries"`
	Metadata   map[string]string    `json:"metadata,omitempty"`
}

func TestRunMainParsesFlagsAndInvokesRun(t *testing.T) {
	called := false
	var got runConfig
	run := func(_ context.Context, cfg runConfig) error {
		called = true
		got = cfg
		return nil
	}

	code := RunMain([]string{"--repo", "/repo", "--root", "root-1", "--backend", "codex", "--model", "openai/gpt-5.3-codex", "--max", "2", "--concurrency", "3", "--dry-run", "--runner-timeout", "30s", "--events", "/repo/runner-logs/agent.events.jsonl"}, run)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if !called {
		t.Fatalf("expected run function to be called")
	}
	if got.repoRoot != "/repo" || got.rootID != "root-1" || got.model != "openai/gpt-5.3-codex" {
		t.Fatalf("unexpected config: %#v", got)
	}
	if got.backend != backendCodex {
		t.Fatalf("expected backend=%q, got %q", backendCodex, got.backend)
	}
	if got.maxTasks != 2 || !got.dryRun {
		t.Fatalf("expected max=2 dry-run=true, got %#v", got)
	}
	if got.runnerTimeout != 30*time.Second {
		t.Fatalf("expected runner timeout 30s, got %s", got.runnerTimeout)
	}
	if got.eventsPath != "/repo/runner-logs/agent.events.jsonl" {
		t.Fatalf("expected events path to be parsed, got %q", got.eventsPath)
	}
	if got.concurrency != 3 {
		t.Fatalf("expected concurrency=3, got %d", got.concurrency)
	}
	if got.stream {
		t.Fatalf("expected stream=false by default")
	}
}

func TestRunMainParsesQualityGateFlagsAndOverride(t *testing.T) {
	called := false
	var got runConfig
	run := func(_ context.Context, cfg runConfig) error {
		called = true
		got = cfg
		return nil
	}

	code := RunMain([]string{
		"--repo", "/repo",
		"--root", "root-1",
		"--quality-threshold", "7",
		"--allow-low-quality",
	}, run)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if !called {
		t.Fatalf("expected run function to be called")
	}
	if got.qualityThreshold != 7 {
		t.Fatalf("expected quality-threshold=7, got %d", got.qualityThreshold)
	}
	if !got.allowLowQuality {
		t.Fatalf("expected allow-low-quality=true, got false")
	}
}

func TestRunMainParsesQualityGateTools(t *testing.T) {
	called := false
	var got runConfig
	run := func(_ context.Context, cfg runConfig) error {
		called = true
		got = cfg
		return nil
	}

	code := RunMain([]string{
		"--repo", "/repo",
		"--root", "root-1",
		"--quality-gate-tools", "task_validator,dependency_checker",
	}, run)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if !called {
		t.Fatalf("expected run function to be called")
	}
	if len(got.qualityGateTools) != 2 {
		t.Fatalf("expected two quality gate tools, got %#v", got.qualityGateTools)
	}
	if got.qualityGateTools[0] != "task_validator" || got.qualityGateTools[1] != "dependency_checker" {
		t.Fatalf("unexpected quality gate tools ordering/values: %#v", got.qualityGateTools)
	}
}

func TestRunMainParsesQCGateTools(t *testing.T) {
	called := false
	var got runConfig
	run := func(_ context.Context, cfg runConfig) error {
		called = true
		got = cfg
		return nil
	}

	code := RunMain([]string{
		"--repo", "/repo",
		"--root", "root-1",
		"--qc-gate-tools", "test_runner, linter, coverage_checker",
	}, run)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if !called {
		t.Fatalf("expected run function to be called")
	}
	if len(got.qcGateTools) != 3 {
		t.Fatalf("expected three qc gate tools, got %#v", got.qcGateTools)
	}
	if got.qcGateTools[0] != "test_runner" || got.qcGateTools[1] != "linter" || got.qcGateTools[2] != "coverage_checker" {
		t.Fatalf("unexpected qc gate tools ordering/values: %#v", got.qcGateTools)
	}
}

func TestRunMainRoutesConfigValidateSubcommand(t *testing.T) {
	originalValidate := runConfigValidateCommand
	t.Cleanup(func() {
		runConfigValidateCommand = originalValidate
	})

	called := false
	var gotArgs []string
	runConfigValidateCommand = func(args []string) int {
		called = true
		gotArgs = append([]string(nil), args...)
		return 73
	}

	runCalled := false
	code := RunMain([]string{"config", "validate", "--repo", "/repo"}, func(context.Context, runConfig) error {
		runCalled = true
		return nil
	})
	if code != 73 {
		t.Fatalf("expected validate route exit code 73, got %d", code)
	}
	if !called {
		t.Fatalf("expected validate command handler to be called")
	}
	if runCalled {
		t.Fatalf("expected legacy run function not to be called for config validate")
	}
	if !reflect.DeepEqual(gotArgs, []string{"--repo", "/repo"}) {
		t.Fatalf("unexpected validate args: %#v", gotArgs)
	}
}

func TestRunMainRoutesConfigInitSubcommand(t *testing.T) {
	originalInit := runConfigInitCommand
	t.Cleanup(func() {
		runConfigInitCommand = originalInit
	})

	called := false
	var gotArgs []string
	runConfigInitCommand = func(args []string) int {
		called = true
		gotArgs = append([]string(nil), args...)
		return 29
	}

	runCalled := false
	code := RunMain([]string{"config", "init", "--repo", "/repo", "--force"}, func(context.Context, runConfig) error {
		runCalled = true
		return nil
	})
	if code != 29 {
		t.Fatalf("expected init route exit code 29, got %d", code)
	}
	if !called {
		t.Fatalf("expected init command handler to be called")
	}
	if runCalled {
		t.Fatalf("expected legacy run function not to be called for config init")
	}
	if !reflect.DeepEqual(gotArgs, []string{"--repo", "/repo", "--force"}) {
		t.Fatalf("unexpected init args: %#v", gotArgs)
	}
}

func TestRunMainConfigCommandRequiresSubcommand(t *testing.T) {
	errText := captureStderr(t, func() {
		code := RunMain([]string{"config"}, func(context.Context, runConfig) error { return nil })
		if code != 1 {
			t.Fatalf("expected exit code 1, got %d", code)
		}
	})

	if !strings.Contains(errText, "usage: yolo-agent config <validate|init> [flags]") {
		t.Fatalf("expected config usage guidance, got %q", errText)
	}
}

func TestRunMainRejectsUnknownConfigSubcommand(t *testing.T) {
	errText := captureStderr(t, func() {
		code := RunMain([]string{"config", "unknown"}, func(context.Context, runConfig) error { return nil })
		if code != 1 {
			t.Fatalf("expected exit code 1, got %d", code)
		}
	})

	if !strings.Contains(errText, "unknown config command: unknown") {
		t.Fatalf("expected unknown config command message, got %q", errText)
	}
	if !strings.Contains(errText, "usage: yolo-agent config <validate|init> [flags]") {
		t.Fatalf("expected config usage guidance, got %q", errText)
	}
}

func TestRunMainDefaultsBackendToOpenCode(t *testing.T) {
	called := false
	var got runConfig
	run := func(_ context.Context, cfg runConfig) error {
		called = true
		got = cfg
		return nil
	}

	code := RunMain([]string{"--repo", "/repo", "--root", "root-1"}, run)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if !called {
		t.Fatalf("expected run function to be called")
	}
	if got.backend != backendOpenCode {
		t.Fatalf("expected default backend=%q, got %q", backendOpenCode, got.backend)
	}
}

func TestRunMainAcceptsAgentBackendFlag(t *testing.T) {
	called := false
	var got runConfig
	run := func(_ context.Context, cfg runConfig) error {
		called = true
		got = cfg
		return nil
	}

	code := RunMain([]string{"--repo", "/repo", "--root", "root-1", "--agent-backend", "codex"}, run)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if !called {
		t.Fatalf("expected run function to be called")
	}
	if got.backend != backendCodex {
		t.Fatalf("expected backend=%q, got %q", backendCodex, got.backend)
	}
}

func TestRunMainDefaultsModelFromBackendWhenConfigOmitsModel(t *testing.T) {
	repoRoot := t.TempDir()
	writeTrackerConfigYAML(t, repoRoot, `
profiles:
  default:
    tracker:
      type: tk
agent:
  backend: codex
`)

	called := false
	var got runConfig
	run := func(_ context.Context, cfg runConfig) error {
		called = true
		got = cfg
		return nil
	}

	code := RunMain([]string{"--repo", repoRoot, "--root", "root-1", "--agent-backend", "codex"}, run)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if !called {
		t.Fatalf("expected run function to be called")
	}
	if got.model != "gpt-5.3-codex" {
		t.Fatalf("expected model fallback from backend, got %q", got.model)
	}
}

func TestRunMainParsesProfileFlag(t *testing.T) {
	called := false
	var got runConfig
	run := func(_ context.Context, cfg runConfig) error {
		called = true
		got = cfg
		return nil
	}

	code := RunMain([]string{"--repo", "/repo", "--root", "root-1", "--profile", "linear-dev"}, run)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if !called {
		t.Fatalf("expected run function to be called")
	}
	if got.profile != "linear-dev" {
		t.Fatalf("expected profile=linear-dev, got %q", got.profile)
	}
}

func TestRunMainUsesProfileFromEnvWhenFlagUnset(t *testing.T) {
	t.Setenv("YOLO_PROFILE", "team-default")
	called := false
	var got runConfig
	run := func(_ context.Context, cfg runConfig) error {
		called = true
		got = cfg
		return nil
	}

	code := RunMain([]string{"--repo", "/repo", "--root", "root-1"}, run)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if !called {
		t.Fatalf("expected run function to be called")
	}
	if got.profile != "team-default" {
		t.Fatalf("expected profile from env, got %q", got.profile)
	}
}

func TestRunMainUsesProfileDefaultBackendWhenBackendFlagsAreUnset(t *testing.T) {
	t.Setenv("YOLO_AGENT_BACKEND", backendClaude)
	called := false
	var got runConfig
	run := func(_ context.Context, cfg runConfig) error {
		called = true
		got = cfg
		return nil
	}

	code := RunMain([]string{"--repo", "/repo", "--root", "root-1"}, run)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if !called {
		t.Fatalf("expected run function to be called")
	}
	if got.backend != backendClaude {
		t.Fatalf("expected profile default backend=%q, got %q", backendClaude, got.backend)
	}
}

func TestRunMainLoadsBackendDefinitionFromCustomCatalog(t *testing.T) {
	repoRoot := t.TempDir()
	catalogDir := filepath.Join(repoRoot, ".yolo-runner", "coding-agents")
	if err := os.MkdirAll(catalogDir, 0o755); err != nil {
		t.Fatalf("create custom catalog dir: %v", err)
	}
	customPath := filepath.Join(catalogDir, "custom-cli.yaml")
	if err := os.WriteFile(customPath, []byte(`
name: custom
adapter: command
binary: /usr/bin/custom-cli
args:
  - --prompt
  - "{{prompt}}"
supports_review: true
supports_stream: true
required_credentials:
  - CUSTOM_AGENT_TOKEN
supported_models:
  - custom-*
`), 0o644); err != nil {
		t.Fatalf("write custom backend definition: %v", err)
	}
	t.Setenv("CUSTOM_AGENT_TOKEN", "custom-token")

	called := false
	var got runConfig
	run := func(_ context.Context, cfg runConfig) error {
		called = true
		got = cfg
		return nil
	}

	code := RunMain([]string{
		"--repo", repoRoot,
		"--root", "root-1",
		"--agent-backend", "custom",
		"--model", "custom-model",
	}, run)
	if code != 0 {
		t.Fatalf("expected exit code 0 for catalog-defined backend, got %d", code)
	}
	if !called {
		t.Fatalf("expected run function to be called")
	}
	if got.backend != "custom" {
		t.Fatalf("expected backend=%q, got %q", "custom", got.backend)
	}
}

func TestRunMainRejectsCatalogBackendWithMissingCredentials(t *testing.T) {
	repoRoot := t.TempDir()
	catalogDir := filepath.Join(repoRoot, ".yolo-runner", "coding-agents")
	if err := os.MkdirAll(catalogDir, 0o755); err != nil {
		t.Fatalf("create custom catalog dir: %v", err)
	}
	customPath := filepath.Join(catalogDir, "custom-cli.yaml")
	if err := os.WriteFile(customPath, []byte(`
name: custom
adapter: command
binary: /usr/bin/custom-cli
args:
  - --prompt
  - "{{prompt}}"
supports_review: true
supports_stream: true
required_credentials:
  - CUSTOM_AGENT_TOKEN
`), 0o644); err != nil {
		t.Fatalf("write custom backend definition: %v", err)
	}

	called := false
	stderrText := captureStderr(t, func() {
		code := RunMain([]string{
			"--repo", repoRoot,
			"--root", "root-1",
			"--agent-backend", "custom",
		}, func(context.Context, runConfig) error {
			called = true
			return nil
		})
		if code != 1 {
			t.Fatalf("expected exit code 1 for missing credential, got %d", code)
		}
	})
	if called {
		t.Fatalf("expected run function not to be called when backend validation fails")
	}
	if !strings.Contains(stderrText, "missing auth token from CUSTOM_AGENT_TOKEN") {
		t.Fatalf("expected missing credential validation error, got %q", stderrText)
	}
}

func TestRunMainUsesModeFromConfigWhenModeFlagUnset(t *testing.T) {
	repoRoot := t.TempDir()
	writeTrackerConfigYAML(t, repoRoot, `
profiles:
  default:
    tracker:
      type: tk
agent:
  mode: ui
`)

	called := false
	var got runConfig
	run := func(_ context.Context, cfg runConfig) error {
		called = true
		got = cfg
		return nil
	}

	code := RunMain([]string{"--repo", repoRoot, "--root", "root-1"}, run)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if !called {
		t.Fatalf("expected run function to be called")
	}
	if got.mode != agentModeUI {
		t.Fatalf("expected mode from config=%q, got %q", agentModeUI, got.mode)
	}
	if !got.stream {
		t.Fatalf("expected mode ui to enable streaming")
	}
}

func TestRunMainAgentBackendFlagOverridesLegacyAndProfileBackends(t *testing.T) {
	t.Setenv("YOLO_AGENT_BACKEND", backendKimi)
	called := false
	var got runConfig
	run := func(_ context.Context, cfg runConfig) error {
		called = true
		got = cfg
		return nil
	}

	code := RunMain([]string{"--repo", "/repo", "--root", "root-1", "--backend", "claude", "--agent-backend", "codex"}, run)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if !called {
		t.Fatalf("expected run function to be called")
	}
	if got.backend != backendCodex {
		t.Fatalf("expected backend=%q, got %q", backendCodex, got.backend)
	}
}

func TestRunMainLoadsAgentDefaultsFromConfigFile(t *testing.T) {
	repoRoot := t.TempDir()
	writeTrackerConfigYAML(t, repoRoot, `
profiles:
  default:
    tracker:
      type: tk
agent:
  backend: codex
  model: openai/gpt-5.3-codex
  concurrency: 3
  runner_timeout: 25m
  watchdog_timeout: 2m
  watchdog_interval: 3s
  retry_budget: 4
`)

	called := false
	var got runConfig
	run := func(_ context.Context, cfg runConfig) error {
		called = true
		got = cfg
		return nil
	}

	code := RunMain([]string{"--repo", repoRoot, "--root", "root-1"}, run)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if !called {
		t.Fatalf("expected run function to be called")
	}
	if got.backend != backendCodex {
		t.Fatalf("expected backend from config=%q, got %q", backendCodex, got.backend)
	}
	if got.model != "openai/gpt-5.3-codex" {
		t.Fatalf("expected model from config, got %q", got.model)
	}
	if got.concurrency != 3 {
		t.Fatalf("expected concurrency from config=3, got %d", got.concurrency)
	}
	if got.runnerTimeout != 25*time.Minute {
		t.Fatalf("expected runner timeout from config 25m, got %s", got.runnerTimeout)
	}
	if got.watchdogTimeout != 2*time.Minute {
		t.Fatalf("expected watchdog timeout from config 2m, got %s", got.watchdogTimeout)
	}
	if got.watchdogInterval != 3*time.Second {
		t.Fatalf("expected watchdog interval from config 3s, got %s", got.watchdogInterval)
	}
	if got.retryBudget != 4 {
		t.Fatalf("expected retry budget from config=4, got %d", got.retryBudget)
	}
}

func TestRunMainFlagAndEnvPrecedenceOverAgentConfigDefaults(t *testing.T) {
	repoRoot := t.TempDir()
	writeTrackerConfigYAML(t, repoRoot, `
profiles:
  default:
    tracker:
      type: tk
agent:
  backend: codex
  model: openai/gpt-5.3-codex
  concurrency: 3
  runner_timeout: 25m
  watchdog_timeout: 2m
  watchdog_interval: 3s
  retry_budget: 4
`)

	t.Setenv("YOLO_AGENT_BACKEND", backendClaude)

	called := false
	var got runConfig
	run := func(_ context.Context, cfg runConfig) error {
		called = true
		got = cfg
		return nil
	}

	code := RunMain([]string{
		"--repo", repoRoot,
		"--root", "root-1",
		"--agent-backend", "kimi",
		"--model", "kimi-k2",
		"--concurrency", "7",
		"--runner-timeout", "11m",
		"--watchdog-timeout", "12m",
		"--watchdog-interval", "4s",
		"--retry-budget", "9",
	}, run)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if !called {
		t.Fatalf("expected run function to be called")
	}
	if got.backend != backendKimi {
		t.Fatalf("expected agent-backend flag to win, got %q", got.backend)
	}
	if got.model != "kimi-k2" {
		t.Fatalf("expected model from flag, got %q", got.model)
	}
	if got.concurrency != 7 {
		t.Fatalf("expected concurrency from flag=7, got %d", got.concurrency)
	}
	if got.runnerTimeout != 11*time.Minute {
		t.Fatalf("expected runner timeout from flag 11m, got %s", got.runnerTimeout)
	}
	if got.watchdogTimeout != 12*time.Minute {
		t.Fatalf("expected watchdog timeout from flag 12m, got %s", got.watchdogTimeout)
	}
	if got.watchdogInterval != 4*time.Second {
		t.Fatalf("expected watchdog interval from flag 4s, got %s", got.watchdogInterval)
	}
	if got.retryBudget != 9 {
		t.Fatalf("expected retry budget from flag=9, got %d", got.retryBudget)
	}
}

func TestRunMainEnvBackendOverridesAgentConfigDefaultBackend(t *testing.T) {
	repoRoot := t.TempDir()
	writeTrackerConfigYAML(t, repoRoot, `
profiles:
  default:
    tracker:
      type: tk
agent:
  backend: codex
`)
	t.Setenv("YOLO_AGENT_BACKEND", backendClaude)

	called := false
	var got runConfig
	run := func(_ context.Context, cfg runConfig) error {
		called = true
		got = cfg
		return nil
	}

	code := RunMain([]string{"--repo", repoRoot, "--root", "root-1"}, run)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if !called {
		t.Fatalf("expected run function to be called")
	}
	if got.backend != backendClaude {
		t.Fatalf("expected env backend=%q to override config backend, got %q", backendClaude, got.backend)
	}
}

func TestRunMainFailsFastOnInvalidAgentDefaultsInConfig(t *testing.T) {
	repoRoot := t.TempDir()
	writeTrackerConfigYAML(t, repoRoot, `
profiles:
  default:
    tracker:
      type: tk
agent:
  watchdog_timeout: 0s
`)

	called := false
	errText := captureStderr(t, func() {
		code := RunMain([]string{"--repo", repoRoot, "--root", "root-1"}, func(context.Context, runConfig) error {
			called = true
			return nil
		})
		if code != 1 {
			t.Fatalf("expected exit code 1, got %d", code)
		}
	})
	if called {
		t.Fatalf("expected run function not to be called when config defaults are invalid")
	}
	if !strings.Contains(errText, "agent.watchdog_timeout") {
		t.Fatalf("expected field-specific error, got %q", errText)
	}
	if !strings.Contains(errText, ".yolo-runner/config.yaml") {
		t.Fatalf("expected config path in error, got %q", errText)
	}
}

func TestRunMainAcceptsKimiBackend(t *testing.T) {
	called := false
	var got runConfig
	run := func(_ context.Context, cfg runConfig) error {
		called = true
		got = cfg
		return nil
	}

	code := RunMain([]string{"--repo", "/repo", "--root", "root-1", "--backend", "kimi"}, run)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if !called {
		t.Fatalf("expected run function to be called")
	}
	if got.backend != backendKimi {
		t.Fatalf("expected backend=%q, got %q", backendKimi, got.backend)
	}
}

func TestRunMainAcceptsGeminiBackend(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "token")
	called := false
	var got runConfig
	run := func(_ context.Context, cfg runConfig) error {
		called = true
		got = cfg
		return nil
	}

	code := RunMain([]string{"--repo", "/repo", "--root", "root-1", "--backend", "gemini", "--model", "gemini-flash"}, run)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if !called {
		t.Fatalf("expected run function to be called")
	}
	if got.backend != backendGemini {
		t.Fatalf("expected backend=%q, got %q", backendGemini, got.backend)
	}
}

func TestRunMainRejectsGeminiBackendWithoutAuthToken(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	called := false
	stderrText := captureStderr(t, func() {
		code := RunMain([]string{"--repo", "/repo", "--root", "root-1", "--backend", "gemini", "--model", "gemini-2.0-pro"}, func(context.Context, runConfig) error {
			called = true
			return nil
		})
		if code != 1 {
			t.Fatalf("expected exit code 1, got %d", code)
		}
	})
	if called {
		t.Fatalf("expected run function not to be called when auth is missing")
	}
	if !strings.Contains(stderrText, "missing auth token from GEMINI_API_KEY") {
		t.Fatalf("expected missing auth token error, got %q", stderrText)
	}
}

func TestRunMainRejectsGeminiBackendWithUnsupportedModel(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "token")
	called := false
	stderrText := captureStderr(t, func() {
		code := RunMain([]string{"--repo", "/repo", "--root", "root-1", "--backend", "gemini", "--model", "gpt-5.3-codex"}, func(context.Context, runConfig) error {
			called = true
			return nil
		})
		if code != 1 {
			t.Fatalf("expected exit code 1, got %d", code)
		}
	})
	if called {
		t.Fatalf("expected run function not to be called for unsupported model")
	}
	if !strings.Contains(stderrText, "unsupported model") || !strings.Contains(stderrText, "supported:") {
		t.Fatalf("expected unsupported-model validation error, got %q", stderrText)
	}
}

func TestRunMainAcceptsClaudeBackend(t *testing.T) {
	called := false
	var got runConfig
	run := func(_ context.Context, cfg runConfig) error {
		called = true
		got = cfg
		return nil
	}

	code := RunMain([]string{"--repo", "/repo", "--root", "root-1", "--backend", "claude"}, run)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if !called {
		t.Fatalf("expected run function to be called")
	}
	if got.backend != backendClaude {
		t.Fatalf("expected backend=%q, got %q", backendClaude, got.backend)
	}
}

func TestRunMainRejectsUnsupportedBackend(t *testing.T) {
	called := false
	code := RunMain([]string{"--repo", "/repo", "--root", "root-1", "--backend", "unknown"}, func(context.Context, runConfig) error {
		called = true
		return nil
	})
	if code != 1 {
		t.Fatalf("expected exit code 1 for invalid backend, got %d", code)
	}
	if called {
		t.Fatalf("expected run function not to be called for invalid backend")
	}
}

func TestRunMainParsesStreamFlag(t *testing.T) {
	called := false
	var got runConfig
	run := func(_ context.Context, cfg runConfig) error {
		called = true
		got = cfg
		return nil
	}

	code := RunMain([]string{"--repo", "/repo", "--root", "root-1", "--stream"}, run)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if !called {
		t.Fatalf("expected run function to be called")
	}
	if !got.stream {
		t.Fatalf("expected stream=true")
	}
}

func TestRunMainParsesDistributedExecutorRoleAndConfig(t *testing.T) {
	tempDir := t.TempDir()
	called := false
	var got runConfig
	run := func(_ context.Context, cfg runConfig) error {
		called = true
		got = cfg
		return nil
	}

	code := RunMain([]string{
		"--repo", tempDir,
		"--role", agentRoleWorker,
		"--distributed-bus-backend", distributedBusNATS,
		"--distributed-bus-address", "nats://localhost:4222",
		"--distributed-bus-prefix", "team",
		"--distributed-executor-id", "executor-42",
		"--distributed-executor-capabilities", "implement,review,service_proxy",
		"--distributed-heartbeat-interval", "7s",
		"--distributed-request-timeout", "31s",
		"--distributed-registry-ttl", "40s",
	}, run)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if !called {
		t.Fatalf("expected run function to be called")
	}
	if got.role != agentRoleWorker {
		t.Fatalf("expected role=%q, got %q", agentRoleWorker, got.role)
	}
	if got.distributedBusBackend != distributedBusNATS {
		t.Fatalf("expected distributed bus backend %q, got %q", distributedBusNATS, got.distributedBusBackend)
	}
	if got.distributedBusAddress != "nats://localhost:4222" {
		t.Fatalf("unexpected distributed bus address %q", got.distributedBusAddress)
	}
	if got.distributedBusPrefix != "team" {
		t.Fatalf("expected prefix team, got %q", got.distributedBusPrefix)
	}
	if got.distributedRoleID != "executor-42" {
		t.Fatalf("unexpected executor id %q", got.distributedRoleID)
	}
	if got.distributedHeartbeatInterval != 7*time.Second {
		t.Fatalf("expected heartbeat 7s, got %s", got.distributedHeartbeatInterval)
	}
	if got.distributedRequestTimeout != 31*time.Second {
		t.Fatalf("expected request timeout 31s, got %s", got.distributedRequestTimeout)
	}
	if got.distributedRegistryTTL != 40*time.Second {
		t.Fatalf("expected registry TTL 40s, got %s", got.distributedRegistryTTL)
	}
	if !reflect.DeepEqual(got.distributedExecutorCapabilities, []distributed.Capability{distributed.CapabilityImplement, distributed.CapabilityReview, distributed.CapabilityServiceProxy}) {
		t.Fatalf("unexpected capabilities: %#v", got.distributedExecutorCapabilities)
	}
}

func TestRunDistributedExecutorSelectsAgentFromCatalogByMetadataQuery(t *testing.T) {
	repoRoot := t.TempDir()
	catalogDir := filepath.Join(repoRoot, ".yolo-runner", "coding-agents")
	if err := os.MkdirAll(catalogDir, 0o755); err != nil {
		t.Fatalf("create catalog dir: %v", err)
	}
	if err := writeAgentCatalogForTest(t, catalogDir, `
name: agent-go-alpha
adapter: command
binary: /bin/true
capabilities:
  languages:
    - go
  features:
    - implement
`); err != nil {
		t.Fatalf("write alpha agent: %v", err)
	}
	if err := writeAgentCatalogForTest(t, catalogDir, `
name: agent-go-zeta
adapter: command
binary: /bin/true
capabilities:
  languages:
    - go
  features:
    - implement
`); err != nil {
		t.Fatalf("write zeta agent: %v", err)
	}
	if err := writeAgentCatalogForTest(t, catalogDir, `
name: agent-python
adapter: command
binary: /bin/true
capabilities:
  languages:
    - python
  features:
    - implement
`); err != nil {
		t.Fatalf("write python agent: %v", err)
	}

	originalBusFactory := newDistributedBus
	t.Cleanup(func() {
		newDistributedBus = originalBusFactory
	})
	bus := distributed.NewMemoryBus()
	newDistributedBus = func(_ string, _ string, _ distributed.BusBackendOptions) (distributed.Bus, error) {
		return bus, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	executorErrCh := make(chan error, 1)
	go func() {
		defer close(done)
		executorErrCh <- runDistributedExecutor(ctx, runConfig{
			repoRoot:                        repoRoot,
			role:                            agentRoleWorker,
			backend:                         "agent-go-alpha",
			distributedBusPrefix:            "unit",
			distributedExecutorCapabilities: []distributed.Capability{distributed.CapabilityImplement},
			distributedHeartbeatInterval:    50 * time.Millisecond,
			distributedRequestTimeout:       500 * time.Millisecond,
			distributedRegistryTTL:          1 * time.Second,
		})
	}()
	t.Cleanup(func() {
		cancel()
		<-done
		executorErr := <-executorErrCh
		if executorErr != nil && !errors.Is(executorErr, context.Canceled) {
			t.Fatalf("executor returned error: %v", executorErr)
		}
	})

	subjects := distributed.DefaultEventSubjects("unit")
	resultCh, unsubscribeResult, err := bus.Subscribe(ctx, subjects.TaskResult)
	if err != nil {
		t.Fatalf("subscribe task result: %v", err)
	}
	defer unsubscribeResult()

	registerCh, unsubscribeRegister, err := bus.Subscribe(ctx, subjects.Register)
	if err != nil {
		t.Fatalf("subscribe register: %v", err)
	}
	defer unsubscribeRegister()
	select {
	case raw := <-registerCh:
		if raw.Type != distributed.EventTypeExecutorRegistered {
			t.Fatalf("unexpected event type on register subject: %q", raw.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for executor registration")
	case executorErr := <-executorErrCh:
		t.Fatalf("executor returned error before registration: %v", executorErr)
	}
	select {
	case executorErr := <-executorErrCh:
		t.Fatalf("executor exited before task dispatch: %v", executorErr)
	default:
	}
	monitorCh, unsubscribeMonitor, err := bus.Subscribe(ctx, subjects.MonitorEvent)
	if err != nil {
		t.Fatalf("subscribe monitor: %v", err)
	}
	defer unsubscribeMonitor()

	dispatchTask := func(taskID string, correlationID string, metadata map[string]string) distributed.TaskResultPayload {
		requestRaw, err := json.Marshal(runnerTransportRequest{
			TaskID:   taskID,
			Metadata: metadata,
		})
		if err != nil {
			t.Fatalf("marshal runner request: %v", err)
		}
		dispatch := distributed.TaskDispatchPayload{
			CorrelationID:        correlationID,
			TaskID:               taskID,
			RequiredCapabilities: []distributed.Capability{distributed.CapabilityImplement},
			Request:              requestRaw,
		}
		dispatchEnv, err := distributed.NewEventEnvelope(distributed.EventTypeTaskDispatch, "client", correlationID, dispatch)
		if err != nil {
			t.Fatalf("build dispatch envelope: %v", err)
		}
		if err := bus.Publish(ctx, subjects.TaskDispatch, dispatchEnv); err != nil {
			t.Fatalf("publish dispatch: %v", err)
		}
		return readDistributedTaskResult(t, resultCh, correlationID)
	}

	readEventForTask := func(taskID, backend string) {
		hasSelection := false
		hasExecution := false
		timeout := time.After(2 * time.Second)
		for !(hasSelection && hasExecution) {
			select {
			case raw := <-monitorCh:
				if raw.Type != distributed.EventTypeMonitorEvent {
					continue
				}
				payload := distributed.MonitorEventPayload{}
				if len(raw.Payload) == 0 {
					continue
				}
				if err := json.Unmarshal(raw.Payload, &payload); err != nil {
					t.Fatalf("unmarshal monitor payload: %v", err)
				}
				if payload.Event.TaskID != taskID {
					continue
				}
				if payload.Event.Metadata["backend"] != backend {
					continue
				}
				switch payload.Event.Type {
				case contracts.EventTypeRunnerStarted:
					hasSelection = true
				case contracts.EventTypeRunnerFinished:
					hasExecution = true
				}
			case <-timeout:
				t.Fatalf("timed out waiting for monitor events for task %q", taskID)
			}
		}
	}

	capabilityResult := dispatchTask("capability-task", "cap-correlation", map[string]string{
		"language": "go",
		"feature":  "implement",
	})
	if capabilityResult.Result.Status != contracts.RunnerResultCompleted {
		t.Fatalf("expected capability query result completed, got %q", capabilityResult.Result.Status)
	}
	if got := capabilityResult.Result.Artifacts["backend"]; got != "agent-go-alpha" {
		t.Fatalf("expected lexical agent selection agent-go-alpha, got %q", got)
	}
	readEventForTask("capability-task", "agent-go-alpha")

	explicitResult := dispatchTask("explicit-task", "explicit-correlation", map[string]string{
		"agent": "agent-go-zeta",
	})
	if explicitResult.Result.Status != contracts.RunnerResultCompleted {
		t.Fatalf("expected explicit selection result completed, got %q", explicitResult.Result.Status)
	}
	if explicitResult.Result.Artifacts["backend"] != "agent-go-zeta" {
		t.Fatalf("expected explicit backend agent-go-zeta, got %q", explicitResult.Result.Artifacts["backend"])
	}
	readEventForTask("explicit-task", "agent-go-zeta")
}

func TestRunDistributedExecutorRejectsAgentSelectionWhenCredentialsMissing(t *testing.T) {
	repoRoot := t.TempDir()
	catalogDir := filepath.Join(repoRoot, ".yolo-runner", "coding-agents")
	if err := os.MkdirAll(catalogDir, 0o755); err != nil {
		t.Fatalf("create catalog dir: %v", err)
	}
	markerPath := filepath.Join(repoRoot, "should-not-run.txt")
	if err := writeAgentCatalogForTest(t, catalogDir, fmt.Sprintf(`
name: locked-go
adapter: command
binary: /bin/sh
args:
  - -c
  - "touch %s"
capabilities:
  languages:
    - go
  features:
    - implement
required_credentials:
  - LOCKED_BACKEND_TOKEN
`, markerPath)); err != nil {
		t.Fatalf("write locked backend: %v", err)
	}

	originalBusFactory := newDistributedBus
	t.Cleanup(func() {
		newDistributedBus = originalBusFactory
	})
	bus := distributed.NewMemoryBus()
	newDistributedBus = func(_ string, _ string, _ distributed.BusBackendOptions) (distributed.Bus, error) {
		return bus, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	executorErrCh := make(chan error, 1)
	go func() {
		defer close(done)
		executorErrCh <- runDistributedExecutor(ctx, runConfig{
			repoRoot:                        repoRoot,
			role:                            agentRoleWorker,
			backend:                         "locked-go",
			distributedBusPrefix:            "unit",
			distributedExecutorCapabilities: []distributed.Capability{distributed.CapabilityImplement},
			distributedHeartbeatInterval:    50 * time.Millisecond,
			distributedRequestTimeout:       500 * time.Millisecond,
			distributedRegistryTTL:          1 * time.Second,
		})
	}()
	t.Cleanup(func() {
		cancel()
		<-done
		executorErr := <-executorErrCh
		if executorErr != nil && !errors.Is(executorErr, context.Canceled) {
			t.Fatalf("executor returned error: %v", executorErr)
		}
	})

	subjects := distributed.DefaultEventSubjects("unit")
	resultCh, unsubscribeResult, err := bus.Subscribe(ctx, subjects.TaskResult)
	if err != nil {
		t.Fatalf("subscribe task result: %v", err)
	}
	defer unsubscribeResult()
	registerCh, unsubscribeRegister, err := bus.Subscribe(ctx, subjects.Register)
	if err != nil {
		t.Fatalf("subscribe register: %v", err)
	}
	defer unsubscribeRegister()
	select {
	case raw := <-registerCh:
		if raw.Type != distributed.EventTypeExecutorRegistered {
			t.Fatalf("unexpected event type on register subject: %q", raw.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for executor registration")
	case executorErr := <-executorErrCh:
		t.Fatalf("executor returned error before registration: %v", executorErr)
	}
	select {
	case executorErr := <-executorErrCh:
		t.Fatalf("executor exited before task dispatch: %v", executorErr)
	default:
	}

	requestRaw, err := json.Marshal(runnerTransportRequest{
		TaskID:   "missing-credential-task",
		Metadata: map[string]string{"agent": "locked-go"},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	dispatch := distributed.TaskDispatchPayload{
		CorrelationID:        "missing-credential",
		TaskID:               "missing-credential-task",
		RequiredCapabilities: []distributed.Capability{distributed.CapabilityImplement},
		Request:              requestRaw,
	}
	dispatchEnv, err := distributed.NewEventEnvelope(distributed.EventTypeTaskDispatch, "client", "missing-credential", dispatch)
	if err != nil {
		t.Fatalf("build dispatch envelope: %v", err)
	}
	if err := bus.Publish(ctx, subjects.TaskDispatch, dispatchEnv); err != nil {
		t.Fatalf("publish dispatch: %v", err)
	}

	result := readDistributedTaskResult(t, resultCh, "missing-credential")
	if result.Result.Status != contracts.RunnerResultFailed {
		t.Fatalf("expected failed result when credential missing, got %q", result.Result.Status)
	}
	if !strings.Contains(strings.TrimSpace(result.Error), "missing auth token from LOCKED_BACKEND_TOKEN") {
		t.Fatalf("expected missing credential error, got %q", result.Error)
	}
	if _, err := os.Stat(markerPath); err == nil {
		t.Fatalf("expected backend command not to run when credentials are missing")
	}
}

func writeAgentCatalogForTest(t *testing.T, catalogDir string, payload string) error {
	t.Helper()
	name := fmt.Sprintf("agent-%d.yaml", time.Now().UnixNano())
	return os.WriteFile(filepath.Join(catalogDir, name), []byte(payload), 0o644)
}

func readDistributedTaskResult(t *testing.T, ch <-chan distributed.EventEnvelope, correlationID string) distributed.TaskResultPayload {
	t.Helper()
	timeout := time.After(2 * time.Second)
	for {
		select {
		case raw := <-ch:
			if raw.Type != distributed.EventTypeTaskResult {
				continue
			}
			if raw.CorrelationID != correlationID {
				continue
			}
			payload := distributed.TaskResultPayload{}
			if len(raw.Payload) == 0 {
				continue
			}
			if err := json.Unmarshal(raw.Payload, &payload); err != nil {
				t.Fatalf("unmarshal task result: %v", err)
			}
			return payload
		case <-timeout:
			t.Fatalf("timed out waiting for task result")
		}
	}
}

func TestMaybeWrapWithMastermindProvidesServiceHandlerToExecuteRequests(t *testing.T) {
	originalBusFactory := newDistributedBus
	t.Cleanup(func() {
		newDistributedBus = originalBusFactory
	})

	bus := distributed.NewMemoryBus()
	newDistributedBus = func(_ string, _ string, _ distributed.BusBackendOptions) (distributed.Bus, error) {
		return bus, nil
	}

	serviceRunner := &serviceTrackingRunner{
		result: contracts.RunnerResult{
			Status:    contracts.RunnerResultCompleted,
			Artifacts: map[string]string{"local": "service"},
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	wrappedRunner, distributedBus, closeDistributed, err := maybeWrapWithMastermind(ctx, runConfig{
		role:                 agentRoleMaster,
		distributedBusPrefix: "unit",
	}, serviceRunner, nil)
	if distributedBus == nil {
		t.Fatalf("expected distributed bus from mastermind wrapper")
	}
	if err != nil {
		t.Fatalf("expected mastermind setup to succeed, got %v", err)
	}
	if closeDistributed == nil {
		t.Fatalf("expected close callback for mastermind wrapper")
	}
	t.Cleanup(func() {
		_ = closeDistributed()
	})

	mm, ok := wrappedRunner.(distributedMastermindRunner)
	if !ok {
		t.Fatalf("expected wrapped runner to be distributed mastermind runner, got %T", wrappedRunner)
	}
	if err := mm.mastermind.Start(ctx); err != nil {
		t.Fatalf("start mastermind: %v", err)
	}

	executor := distributed.NewExecutorWorker(distributed.ExecutorWorkerOptions{
		ID:           "executor",
		Bus:          bus,
		Runner:       serviceRunner,
		Subjects:     distributed.DefaultEventSubjects("unit"),
		Capabilities: []distributed.Capability{distributed.CapabilityReview},
	})
	go func() {
		_ = executor.Start(ctx)
	}()

	time.Sleep(20 * time.Millisecond)

	response, err := executor.RequestService(ctx, distributed.ServiceRequestPayload{
		TaskID:  "task-1",
		Service: "review-with-larger-model",
		Metadata: map[string]string{
			"prompt":    "Please review the change",
			"parent_id": "parent-1",
			"repo_root": "/workspace",
			"timeout":   "500ms",
			"model":     "larger",
		},
	})
	if err != nil {
		t.Fatalf("executor RequestService failed: %v", err)
	}
	if response.Artifacts["service"] != "review-with-larger-model" {
		t.Fatalf("expected service artifact %q, got %q", "review-with-larger-model", response.Artifacts["service"])
	}
	if response.Artifacts["mode"] != string(contracts.RunnerModeReview) {
		t.Fatalf("expected mode artifact %q, got %q", contracts.RunnerModeReview, response.Artifacts["mode"])
	}

	req, ok := serviceRunner.lastRequest()
	if !ok {
		t.Fatalf("expected local runner request")
	}
	if req.ParentID != "parent-1" {
		t.Fatalf("expected parent_id %q, got %q", "parent-1", req.ParentID)
	}
	if req.TaskID != "task-1" {
		t.Fatalf("expected task_id %q, got %q", "task-1", req.TaskID)
	}
	if req.Mode != contracts.RunnerModeReview {
		t.Fatalf("expected runner mode %q, got %q", contracts.RunnerModeReview, req.Mode)
	}
	if req.Model != "larger" {
		t.Fatalf("expected model %q, got %q", "larger", req.Model)
	}
	if req.RepoRoot != "/workspace" {
		t.Fatalf("expected repo_root %q, got %q", "/workspace", req.RepoRoot)
	}
	if req.Timeout != 500*time.Millisecond {
		t.Fatalf("expected timeout %s, got %s", 500*time.Millisecond, req.Timeout)
	}
}

func TestMaybeWrapWithMastermindRejectsUnsupportedServiceNames(t *testing.T) {
	originalBusFactory := newDistributedBus
	t.Cleanup(func() {
		newDistributedBus = originalBusFactory
	})

	bus := distributed.NewMemoryBus()
	newDistributedBus = func(_ string, _ string, _ distributed.BusBackendOptions) (distributed.Bus, error) {
		return bus, nil
	}

	serviceRunner := &serviceTrackingRunner{result: contracts.RunnerResult{Status: contracts.RunnerResultCompleted}}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	wrappedRunner, distributedBus, closeDistributed, err := maybeWrapWithMastermind(ctx, runConfig{
		role:                 agentRoleMaster,
		distributedBusPrefix: "unit",
	}, serviceRunner, nil)
	if distributedBus == nil {
		t.Fatalf("expected distributed bus from mastermind wrapper")
	}
	if err != nil {
		t.Fatalf("expected mastermind setup to succeed, got %v", err)
	}
	t.Cleanup(func() {
		_ = closeDistributed()
	})

	mm, ok := wrappedRunner.(distributedMastermindRunner)
	if !ok {
		t.Fatalf("expected wrapped runner to be distributed mastermind runner, got %T", wrappedRunner)
	}
	if err := mm.mastermind.Start(ctx); err != nil {
		t.Fatalf("start mastermind: %v", err)
	}

	executor := distributed.NewExecutorWorker(distributed.ExecutorWorkerOptions{
		ID:           "executor",
		Bus:          bus,
		Runner:       serviceRunner,
		Subjects:     distributed.DefaultEventSubjects("unit"),
		Capabilities: []distributed.Capability{distributed.CapabilityReview},
	})
	go func() {
		_ = executor.Start(ctx)
	}()

	time.Sleep(20 * time.Millisecond)

	_, err = executor.RequestService(ctx, distributed.ServiceRequestPayload{
		TaskID:   "task-2",
		Service:  "unsupported-service",
		Metadata: map[string]string{"prompt": "noop"},
	})
	if err == nil {
		t.Fatalf("expected unsupported service request to fail")
	}
}

func TestDiscoverTaskStatusBackendsForMastermindIncludesAdditionalTrackerProfiles(t *testing.T) {
	repoRoot := t.TempDir()
	writeTrackerConfigYAML(t, repoRoot, `
profiles:
  default:
    tracker:
      type: tk
  linear:
    tracker:
      type: linear
      linear:
        scope:
          workspace: playground
        auth:
          token_env: LINEAR_TOKEN
  github:
    tracker:
      type: github
      github:
        scope:
          owner: org
          repo: repo
        auth:
          token_env: GITHUB_TOKEN
`)
	t.Setenv("LINEAR_TOKEN", "linear-token")
	t.Setenv("GITHUB_TOKEN", "github-token")

	originalTKStorage := newTKStorageBackend
	originalLinearStorage := newLinearStorageBackend
	originalGitHubStorage := newGitHubStorageBackend
	t.Cleanup(func() {
		newTKStorageBackend = originalTKStorage
		newLinearStorageBackend = originalLinearStorage
		newGitHubStorageBackend = originalGitHubStorage
	})

	discoveredByID := map[string]contracts.StorageBackend{}
	newTKStorageBackend = func(string) (contracts.StorageBackend, error) {
		backend := &testStorageBackend{}
		discoveredByID["tk"] = backend
		return backend, nil
	}
	newLinearStorageBackend = func(_ linear.Config) (contracts.StorageBackend, error) {
		backend := &testStorageBackend{}
		discoveredByID["linear"] = backend
		return backend, nil
	}
	newGitHubStorageBackend = func(_ github.Config) (contracts.StorageBackend, error) {
		backend := &testStorageBackend{}
		discoveredByID["github"] = backend
		return backend, nil
	}

	taskStatusBackends := discoverTaskStatusBackendsForMastermind(runConfig{
		repoRoot: repoRoot,
		rootID:   "root-1",
		role:     agentRoleMaster,
		profile:  "default",
	}, map[string]contracts.StorageBackend{})

	if len(taskStatusBackends) != 3 {
		t.Fatalf("expected 3 backends, got %d", len(taskStatusBackends))
	}
	if _, ok := discoveredByID["tk"]; !ok {
		t.Fatalf("expected tk backend to be built")
	}
	if _, ok := discoveredByID["linear"]; !ok {
		t.Fatalf("expected linear backend to be built")
	}
	if _, ok := discoveredByID["github"]; !ok {
		t.Fatalf("expected github backend to be built")
	}
}

func TestMaybeWrapWithMastermindWiresDiscoveredBackendsForStatusWrites(t *testing.T) {
	t.Setenv("YOLO_INBOX_WRITE_TOKEN", "token")
	t.Setenv("LINEAR_TOKEN", "linear-token")
	t.Setenv("GITHUB_TOKEN", "github-token")
	repoRoot := t.TempDir()
	writeTrackerConfigYAML(t, repoRoot, `
profiles:
  default:
    tracker:
      type: tk
  linear:
    tracker:
      type: linear
      linear:
        scope:
          workspace: playground
        auth:
          token_env: LINEAR_TOKEN
  github:
    tracker:
      type: github
      github:
        scope:
          owner: org
          repo: repo
        auth:
          token_env: GITHUB_TOKEN
`)

	originalBusFactory := newDistributedBus
	t.Cleanup(func() {
		newDistributedBus = originalBusFactory
	})
	bus := distributed.NewMemoryBus()
	newDistributedBus = func(_ string, _ string, _ distributed.BusBackendOptions) (distributed.Bus, error) {
		return bus, nil
	}

	tkBackend := &testStorageBackend{}
	linearBackend := &testStorageBackend{}
	githubBackend := &testStorageBackend{}
	originalTKStorage := newTKStorageBackend
	originalLinearStorage := newLinearStorageBackend
	originalGitHubStorage := newGitHubStorageBackend
	t.Cleanup(func() {
		newTKStorageBackend = originalTKStorage
		newLinearStorageBackend = originalLinearStorage
		newGitHubStorageBackend = originalGitHubStorage
	})
	newTKStorageBackend = func(_ string) (contracts.StorageBackend, error) { return tkBackend, nil }
	newLinearStorageBackend = func(_ linear.Config) (contracts.StorageBackend, error) { return linearBackend, nil }
	newGitHubStorageBackend = func(_ github.Config) (contracts.StorageBackend, error) { return githubBackend, nil }

	serviceRunner := &serviceTrackingRunner{}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	wrappedRunner, distributedBus, closeDistributed, err := maybeWrapWithMastermind(ctx, runConfig{
		role:                 agentRoleMaster,
		repoRoot:             repoRoot,
		rootID:               "root-1",
		profile:              "default",
		distributedBusPrefix: "unit",
	}, serviceRunner, map[string]contracts.StorageBackend{
		"tk": tkBackend,
	})
	if distributedBus == nil {
		t.Fatalf("expected distributed bus from mastermind wrapper")
	}
	if err != nil {
		t.Fatalf("expected mastermind setup to succeed, got %v", err)
	}
	if closeDistributed == nil {
		t.Fatalf("expected close callback for mastermind wrapper")
	}
	t.Cleanup(func() {
		_ = closeDistributed()
	})

	mm, ok := wrappedRunner.(distributedMastermindRunner)
	if !ok {
		t.Fatalf("expected wrapped runner to be distributed mastermind runner, got %T", wrappedRunner)
	}
	_, err = mm.mastermind.PublishTaskStatusUpdate(ctx, distributed.TaskStatusUpdatePayload{
		TaskID:    "task-1",
		Status:    contracts.TaskStatusClosed,
		AuthToken: "token",
	})
	if err != nil {
		t.Fatalf("publish task status update: %v", err)
	}

	deadline := time.Now().Add(1 * time.Second)
	for {
		tkCalls := tkBackend.callsFor("task-1")
		linearCalls := linearBackend.callsFor("task-1")
		githubCalls := githubBackend.callsFor("task-1")
		if tkCalls == 1 && linearCalls == 1 && githubCalls == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected status writes on discovered backends, got tk=%d linear=%d github=%d", tkCalls, linearCalls, githubCalls)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRunMainRejectsInvalidDistributedRole(t *testing.T) {
	code := RunMain([]string{"--repo", t.TempDir(), "--role", "unknown"}, func(context.Context, runConfig) error {
		t.Fatalf("run function should not be called")
		return nil
	})
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
}

func TestRunMainParsesModeFlag(t *testing.T) {
	repoRoot := t.TempDir()
	writeTrackerConfigYAML(t, repoRoot, `
profiles:
  default:
    tracker:
      type: tk
`)
	called := false
	var got runConfig
	run := func(_ context.Context, cfg runConfig) error {
		called = true
		got = cfg
		return nil
	}

	code := RunMain([]string{"--repo", repoRoot, "--root", "root-1", "--mode", "ui"}, run)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if !called {
		t.Fatalf("expected run function to be called")
	}
	if got.mode != agentModeUI {
		t.Fatalf("expected mode=%q, got %q", agentModeUI, got.mode)
	}
	if !got.stream {
		t.Fatalf("expected mode ui to enable streaming")
	}
}

func TestRunMainParsesTDDFlag(t *testing.T) {
	called := false
	var got runConfig
	run := func(_ context.Context, cfg runConfig) error {
		called = true
		got = cfg
		return nil
	}

	code := RunMain([]string{"--repo", "/repo", "--root", "root-1", "--tdd"}, run)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if !called {
		t.Fatalf("expected run function to be called")
	}
	if !got.tddMode {
		t.Fatalf("expected tdd mode to be true")
	}
}

func TestRunMainUsesZeroRunnerTimeoutByDefault(t *testing.T) {
	called := false
	var got runConfig
	run := func(_ context.Context, cfg runConfig) error {
		called = true
		got = cfg
		return nil
	}

	code := RunMain([]string{"--repo", "/repo", "--root", "root-1"}, run)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if !called {
		t.Fatalf("expected run function to be called")
	}
	if got.runnerTimeout != 0 {
		t.Fatalf("expected default runner timeout 0, got %s", got.runnerTimeout)
	}
	if got.watchdogTimeout != 10*time.Minute {
		t.Fatalf("expected default watchdog timeout 10m, got %s", got.watchdogTimeout)
	}
	if got.watchdogInterval != 5*time.Second {
		t.Fatalf("expected default watchdog interval 5s, got %s", got.watchdogInterval)
	}
}

func TestRunMainParsesWatchdogFlags(t *testing.T) {
	called := false
	var got runConfig
	run := func(_ context.Context, cfg runConfig) error {
		called = true
		got = cfg
		return nil
	}

	code := RunMain([]string{"--repo", "/repo", "--root", "root-1", "--watchdog-timeout", "90s", "--watchdog-interval", "1s"}, run)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if !called {
		t.Fatalf("expected run function to be called")
	}
	if got.watchdogTimeout != 90*time.Second {
		t.Fatalf("expected watchdog timeout 90s, got %s", got.watchdogTimeout)
	}
	if got.watchdogInterval != 1*time.Second {
		t.Fatalf("expected watchdog interval 1s, got %s", got.watchdogInterval)
	}
}

func TestRunMainParsesRetryBudgetFlag(t *testing.T) {
	called := false
	var got runConfig
	run := func(_ context.Context, cfg runConfig) error {
		called = true
		got = cfg
		return nil
	}

	code := RunMain([]string{"--repo", "/repo", "--root", "root-1", "--retry-budget", "2"}, run)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if !called {
		t.Fatalf("expected run function to be called")
	}
	if got.retryBudget != 2 {
		t.Fatalf("expected retryBudget=2, got %d", got.retryBudget)
	}
}

func TestRunMainUsesDefaultRetryBudget(t *testing.T) {
	called := false
	var got runConfig
	run := func(_ context.Context, cfg runConfig) error {
		called = true
		got = cfg
		return nil
	}

	code := RunMain([]string{"--repo", "/repo", "--root", "root-1"}, run)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if !called {
		t.Fatalf("expected run function to be called")
	}
	if got.retryBudget != 5 {
		t.Fatalf("expected default retryBudget=5, got %d", got.retryBudget)
	}
}

func TestRunMainParsesVerboseStreamFlag(t *testing.T) {
	called := false
	var got runConfig
	run := func(_ context.Context, cfg runConfig) error {
		called = true
		got = cfg
		return nil
	}

	code := RunMain([]string{"--repo", "/repo", "--root", "root-1", "--stream", "--verbose-stream"}, run)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if !called {
		t.Fatalf("expected run function to be called")
	}
	if !got.stream {
		t.Fatalf("expected stream=true")
	}
	if !got.verboseStream {
		t.Fatalf("expected verboseStream=true")
	}
}

func TestResolveEventsPathDisablesDefaultFileInStreamMode(t *testing.T) {
	got := resolveEventsPath(runConfig{repoRoot: "/repo", stream: true, eventsPath: ""})
	if got != "" {
		t.Fatalf("expected no default file path in stream mode, got %q", got)
	}
}

func TestResolveEventsPathKeepsDefaultFileWhenNotStreaming(t *testing.T) {
	got := resolveEventsPath(runConfig{repoRoot: "/repo", stream: false, eventsPath: ""})
	expected := "/repo/runner-logs/agent.events.jsonl"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestRunWithComponentsStreamWritesNDJSONToStdout(t *testing.T) {
	originalStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() {
		os.Stdout = originalStdout
	}()

	mgr := &testTaskManager{
		tasks: []contracts.Task{{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen}},
	}
	runner := &testRunner{}
	cfg := runConfig{repoRoot: t.TempDir(), rootID: "root", dryRun: true, stream: true}

	runErr := runWithComponents(context.Background(), cfg, mgr, runner, nil)
	if runErr != nil {
		t.Fatalf("runWithComponents failed: %v", runErr)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("close reader: %v", err)
	}
	out := string(data)
	if !strings.Contains(out, `"type":"task_started"`) {
		t.Fatalf("expected task_started event in stdout, got %q", out)
	}
	if strings.Contains(out, "Category:") {
		t.Fatalf("expected stdout to contain JSON events only, got %q", out)
	}
	_ = filepath.Join
}

func TestRunWithComponentsStreamEmitsRunStartedWithParameters(t *testing.T) {
	originalStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() {
		os.Stdout = originalStdout
	}()

	repoRoot := initGitRepo(t)
	mgr := &testTaskManager{tasks: []contracts.Task{{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen}}}
	runner := &testRunner{}
	cfg := runConfig{
		repoRoot:             repoRoot,
		rootID:               "yr-2y0b",
		dryRun:               true,
		stream:               true,
		concurrency:          2,
		model:                "openai/gpt-5.3-codex",
		runnerTimeout:        15 * time.Minute,
		watchdogTimeout:      10 * time.Minute,
		watchdogInterval:     5 * time.Second,
		verboseStream:        false,
		streamOutputBuffer:   64,
		streamOutputInterval: 150 * time.Millisecond,
	}

	runErr := runWithComponents(context.Background(), cfg, mgr, runner, nil)
	if runErr != nil {
		t.Fatalf("runWithComponents failed: %v", runErr)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("close reader: %v", err)
	}
	out := string(data)
	if !strings.Contains(out, `"type":"run_started"`) {
		t.Fatalf("expected run_started event in stdout, got %q", out)
	}
	if !strings.Contains(out, `"type":"run_finished"`) {
		t.Fatalf("expected run_finished event in stdout, got %q", out)
	}
	if !strings.Contains(out, `"status":"completed"`) {
		t.Fatalf("expected completed status in run_finished metadata, got %q", out)
	}
	if !strings.Contains(out, `"root_id":"yr-2y0b"`) {
		t.Fatalf("expected root_id in run_started metadata, got %q", out)
	}
	if !strings.Contains(out, `"concurrency":"2"`) {
		t.Fatalf("expected concurrency in run_started metadata, got %q", out)
	}
	if !strings.Contains(out, `"watchdog_timeout":"10m0s"`) {
		t.Fatalf("expected watchdog_timeout in run_started metadata, got %q", out)
	}
	if !strings.Contains(out, `"watchdog_interval":"5s"`) {
		t.Fatalf("expected watchdog_interval in run_started metadata, got %q", out)
	}
}

func TestRunWithComponentsStreamCoalescesRunnerOutputByDefault(t *testing.T) {
	originalStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() {
		os.Stdout = originalStdout
	}()

	repoRoot := initGitRepo(t)
	mgr := &testTaskManager{tasks: []contracts.Task{{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen}}}
	runner := &progressRunner{updates: []contracts.RunnerProgress{{Type: "runner_output", Message: "1"}, {Type: "runner_output", Message: "2"}, {Type: "runner_output", Message: "3"}, {Type: "runner_output", Message: "4"}}}
	cfg := runConfig{repoRoot: repoRoot, rootID: "root", stream: true, streamOutputInterval: time.Hour, streamOutputBuffer: 2}

	runErr := runWithComponents(context.Background(), cfg, mgr, runner, nil)
	if runErr != nil {
		t.Fatalf("runWithComponents failed: %v", runErr)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("close reader: %v", err)
	}

	out := string(data)
	if got := strings.Count(out, `"type":"runner_output"`); got != 2 {
		t.Fatalf("expected coalesced runner_output count=2, got %d output=%q", got, out)
	}
	if !strings.Contains(out, `"coalesced_outputs":"1"`) {
		t.Fatalf("expected coalescing metadata in output, got %q", out)
	}
}

func TestRunWithComponentsVerboseStreamEmitsAllRunnerOutput(t *testing.T) {
	originalStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() {
		os.Stdout = originalStdout
	}()

	repoRoot := initGitRepo(t)
	mgr := &testTaskManager{tasks: []contracts.Task{{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen}}}
	runner := &progressRunner{updates: []contracts.RunnerProgress{{Type: "runner_output", Message: "1"}, {Type: "runner_output", Message: "2"}, {Type: "runner_output", Message: "3"}, {Type: "runner_output", Message: "4"}}}
	cfg := runConfig{repoRoot: repoRoot, rootID: "root", stream: true, verboseStream: true, streamOutputInterval: time.Hour, streamOutputBuffer: 2}

	runErr := runWithComponents(context.Background(), cfg, mgr, runner, nil)
	if runErr != nil {
		t.Fatalf("runWithComponents failed: %v", runErr)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("close reader: %v", err)
	}

	out := string(data)
	if got := strings.Count(out, `"type":"runner_output"`); got != 4 {
		t.Fatalf("expected full runner_output count=4, got %d output=%q", got, out)
	}
}

func TestRunWithComponentsModeUILaunchesYoloTUIAndRoutesOutput(t *testing.T) {
	originalLaunch := launchYoloTUI
	t.Cleanup(func() {
		launchYoloTUI = originalLaunch
	})

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	launched := false
	launchYoloTUI = func() (io.WriteCloser, func() error, error) {
		launched = true
		return writer, func() error { return writer.Close() }, nil
	}

	repoRoot := initGitRepo(t)
	mgr := &testTaskManager{tasks: []contracts.Task{{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen}}}
	runner := &testRunner{}
	cfg := runConfig{
		repoRoot:             repoRoot,
		rootID:               "root",
		dryRun:               true,
		stream:               true,
		mode:                 agentModeUI,
		concurrency:          1,
		watchdogTimeout:      10 * time.Minute,
		watchdogInterval:     5 * time.Second,
		streamOutputBuffer:   64,
		streamOutputInterval: 150 * time.Millisecond,
	}

	runErr := runWithComponents(context.Background(), cfg, mgr, runner, nil)
	if runErr != nil {
		t.Fatalf("runWithComponents failed: %v", runErr)
	}
	raw, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read reader: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("close reader: %v", err)
	}
	if !launched {
		t.Fatalf("expected yolo-tui launch for ui mode")
	}
	if !strings.Contains(string(raw), `"type":"run_started"`) {
		t.Fatalf("expected run_started output in ui sink, got %q", string(raw))
	}
	if !strings.Contains(string(raw), `"type":"run_finished"`) {
		t.Fatalf("expected run_finished output in ui sink, got %q", string(raw))
	}
}

func TestRunWithComponentsStreamKeepsRunningWhenMirrorSinkFails(t *testing.T) {
	originalStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() {
		os.Stdout = originalStdout
	}()

	repoRoot := initGitRepo(t)
	mgr := &testTaskManager{tasks: []contracts.Task{{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen}}}
	runner := &testRunner{}

	notDir := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(notDir, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	invalidMirrorPath := filepath.Join(notDir, "events.jsonl")
	cfg := runConfig{repoRoot: repoRoot, rootID: "root", dryRun: true, stream: true, eventsPath: invalidMirrorPath}

	runErr := runWithComponents(context.Background(), cfg, mgr, runner, nil)
	if runErr != nil {
		t.Fatalf("expected stream mode to continue when mirror sink fails, got: %v", runErr)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("close reader: %v", err)
	}

	out := string(data)
	if !strings.Contains(out, `"type":"task_started"`) {
		t.Fatalf("expected primary stdout stream to remain active, got %q", out)
	}
}

func TestMirrorEventSinkEmitDoesNotBlockWhenQueueFull(t *testing.T) {
	block := make(chan struct{})
	wrapped := newMirrorEventSink(blockingSink{block: block}, 1)

	if err := wrapped.Emit(context.Background(), contracts.Event{Type: contracts.EventTypeRunnerOutput, TaskID: "t-1", Timestamp: time.Now().UTC()}); err != nil {
		t.Fatalf("first emit failed: %v", err)
	}

	start := time.Now()
	if err := wrapped.Emit(context.Background(), contracts.Event{Type: contracts.EventTypeRunnerOutput, TaskID: "t-1", Timestamp: time.Now().UTC()}); err != nil {
		t.Fatalf("second emit failed: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 20*time.Millisecond {
		t.Fatalf("expected non-blocking emit, took %s", elapsed)
	}

	close(block)
	wrapped.Close()
}

type testTaskManager struct {
	tasks []contracts.Task
	idx   int
}

func (m *testTaskManager) NextTasks(context.Context, string) ([]contracts.TaskSummary, error) {
	if m.idx >= len(m.tasks) {
		return nil, nil
	}
	task := m.tasks[m.idx]
	m.idx++
	return []contracts.TaskSummary{{ID: task.ID, Title: task.Title}}, nil
}

func (m *testTaskManager) GetTask(_ context.Context, taskID string) (contracts.Task, error) {
	for _, task := range m.tasks {
		if task.ID == taskID {
			return task, nil
		}
	}
	return contracts.Task{}, errors.New("task not found")
}

func (m *testTaskManager) SetTaskStatus(context.Context, string, contracts.TaskStatus) error {
	return nil
}
func (m *testTaskManager) SetTaskData(context.Context, string, map[string]string) error { return nil }

type testRunner struct{}

func (testRunner) Run(context.Context, contracts.RunnerRequest) (contracts.RunnerResult, error) {
	return contracts.RunnerResult{Status: contracts.RunnerResultCompleted}, nil
}

type serviceTrackingRunner struct {
	mu       sync.Mutex
	result   contracts.RunnerResult
	err      error
	requests []contracts.RunnerRequest
}

func (r *serviceTrackingRunner) Run(_ context.Context, req contracts.RunnerRequest) (contracts.RunnerResult, error) {
	r.mu.Lock()
	r.requests = append(r.requests, req)
	r.mu.Unlock()
	return r.result, r.err
}

func (r *serviceTrackingRunner) lastRequest() (contracts.RunnerRequest, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.requests) == 0 {
		return contracts.RunnerRequest{}, false
	}
	return r.requests[len(r.requests)-1], true
}

type progressRunner struct {
	updates []contracts.RunnerProgress
	calls   int
}

func (r *progressRunner) Run(_ context.Context, request contracts.RunnerRequest) (contracts.RunnerResult, error) {
	r.calls++
	if request.OnProgress != nil && r.calls == 1 {
		for _, update := range r.updates {
			request.OnProgress(update)
		}
	}
	if r.calls == 1 {
		return contracts.RunnerResult{Status: contracts.RunnerResultCompleted}, nil
	}
	return contracts.RunnerResult{Status: contracts.RunnerResultCompleted, ReviewReady: true}, nil
}

type testStorageBackend struct {
	mu       sync.Mutex
	statuses map[string]contracts.TaskStatus
	calls    map[string]int
}

func (b *testStorageBackend) GetTaskTree(context.Context, string) (*contracts.TaskTree, error) {
	return &contracts.TaskTree{
		Root: contracts.Task{
			ID:     "root-1",
			Status: contracts.TaskStatusOpen,
		},
		Tasks: map[string]contracts.Task{
			"root-1": {ID: "root-1", Status: contracts.TaskStatusOpen},
		},
	}, nil
}

func (b *testStorageBackend) GetTask(_ context.Context, taskID string) (*contracts.Task, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil, fmt.Errorf("task id is required")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	status, ok := b.statuses[taskID]
	if !ok {
		return &contracts.Task{ID: taskID}, nil
	}
	return &contracts.Task{ID: taskID, Status: status}, nil
}

func (b *testStorageBackend) SetTaskStatus(_ context.Context, taskID string, status contracts.TaskStatus) error {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return fmt.Errorf("task id is required")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.statuses == nil {
		b.statuses = map[string]contracts.TaskStatus{}
	}
	if b.calls == nil {
		b.calls = map[string]int{}
	}
	b.calls[taskID]++
	b.statuses[taskID] = status
	return nil
}

func (b *testStorageBackend) SetTaskData(_ context.Context, taskID string, data map[string]string) error {
	return nil
}

func (b *testStorageBackend) callsFor(taskID string) int {
	taskID = strings.TrimSpace(taskID)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.calls == nil {
		return 0
	}
	return b.calls[taskID]
}

func TestRunMainRequiresRoot(t *testing.T) {
	code := RunMain([]string{"--repo", "/repo"}, func(context.Context, runConfig) error { return nil })
	if code != 1 {
		t.Fatalf("expected exit code 1 when root missing, got %d", code)
	}
}

func TestRunMainRejectsNonPositiveConcurrency(t *testing.T) {
	called := false
	code := RunMain([]string{"--repo", "/repo", "--root", "root-1", "--concurrency", "0"}, func(context.Context, runConfig) error {
		called = true
		return nil
	})

	if code != 1 {
		t.Fatalf("expected exit code 1 when concurrency is non-positive, got %d", code)
	}
	if called {
		t.Fatalf("expected run function not to be called for invalid concurrency")
	}
}

func TestRunMainRejectsNonPositiveWatchdogTimeout(t *testing.T) {
	called := false
	code := RunMain([]string{"--repo", "/repo", "--root", "root-1", "--watchdog-timeout", "0s"}, func(context.Context, runConfig) error {
		called = true
		return nil
	})

	if code != 1 {
		t.Fatalf("expected exit code 1 when watchdog-timeout is non-positive, got %d", code)
	}
	if called {
		t.Fatalf("expected run function not to be called for invalid watchdog-timeout")
	}
}

func TestRunMainRejectsNonPositiveWatchdogInterval(t *testing.T) {
	called := false
	code := RunMain([]string{"--repo", "/repo", "--root", "root-1", "--watchdog-interval", "0s"}, func(context.Context, runConfig) error {
		called = true
		return nil
	})

	if code != 1 {
		t.Fatalf("expected exit code 1 when watchdog-interval is non-positive, got %d", code)
	}
	if called {
		t.Fatalf("expected run function not to be called for invalid watchdog-interval")
	}
}

func TestRunMainRejectsNegativeRetryBudget(t *testing.T) {
	called := false
	code := RunMain([]string{"--repo", "/repo", "--root", "root-1", "--retry-budget", "-1"}, func(context.Context, runConfig) error {
		called = true
		return nil
	})

	if code != 1 {
		t.Fatalf("expected exit code 1 when retry-budget is negative, got %d", code)
	}
	if called {
		t.Fatalf("expected run function not to be called for invalid retry-budget")
	}
}

func TestRunMainRejectsNegativeQualityThreshold(t *testing.T) {
	called := false
	code := RunMain([]string{"--repo", "/repo", "--root", "root-1", "--quality-threshold", "-1"}, func(context.Context, runConfig) error {
		called = true
		return nil
	})

	if code != 1 {
		t.Fatalf("expected exit code 1 when quality-threshold is negative, got %d", code)
	}
	if called {
		t.Fatalf("expected run function not to be called for invalid quality-threshold")
	}
}

func TestRunMainPrintsActionableTaxonomyMessageOnRunError(t *testing.T) {
	run := func(context.Context, runConfig) error {
		return errors.New("git checkout task/t-1 failed")
	}

	errText := captureStderr(t, func() {
		code := RunMain([]string{"--repo", "/repo", "--root", "root-1"}, run)
		if code != 1 {
			t.Fatalf("expected exit code 1, got %d", code)
		}
	})

	if !strings.Contains(errText, "Category: git/vcs") {
		t.Fatalf("expected category in stderr, got %q", errText)
	}
	if !strings.Contains(errText, "Cause: git checkout task/t-1 failed") {
		t.Fatalf("expected cause in stderr, got %q", errText)
	}
	if !strings.Contains(errText, "Next step:") {
		t.Fatalf("expected next step in stderr, got %q", errText)
	}
}

func TestRunMainHidesGenericExitStatusInActionableMessage(t *testing.T) {
	run := func(context.Context, runConfig) error {
		return errors.Join(
			errors.New("git checkout main failed: error: Your local changes would be overwritten by checkout"),
			errors.New("exit status 1"),
		)
	}

	errText := captureStderr(t, func() {
		code := RunMain([]string{"--repo", "/repo", "--root", "root-1"}, run)
		if code != 1 {
			t.Fatalf("expected exit code 1, got %d", code)
		}
	})

	if strings.Contains(errText, "exit status 1") {
		t.Fatalf("expected generic exit status to be removed, got %q", errText)
	}
	if !strings.Contains(errText, "Category: git/vcs") {
		t.Fatalf("expected categorized error, got %q", errText)
	}
}

func TestRunMainLinearStartupValidationReportsActionableConfigGuidance(t *testing.T) {
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
`)

	errText := captureStderr(t, func() {
		code := RunMain([]string{"--repo", repoRoot, "--root", "root-1"}, defaultRun)
		if code != 1 {
			t.Fatalf("expected exit code 1, got %d", code)
		}
	})

	if !strings.Contains(errText, "Category: auth_profile_config") {
		t.Fatalf("expected auth/profile category, got %q", errText)
	}
	if !strings.Contains(errText, ".yolo-runner/config.yaml") {
		t.Fatalf("expected config file guidance, got %q", errText)
	}
	if !strings.Contains(errText, "export LINEAR_TOKEN=<linear-api-token>") {
		t.Fatalf("expected token export guidance, got %q", errText)
	}
}

func TestDefaultRunRestoresWorkingDirectory(t *testing.T) {
	repoRoot := initGitRepo(t)
	writeTrackerConfigYAML(t, repoRoot, `
profiles:
  default:
    tracker:
      type: tk
`)

	originalFactory := newTKStorageBackend
	t.Cleanup(func() {
		newTKStorageBackend = originalFactory
	})
	manager := &countingNoReadyTaskManager{
		rootTask: contracts.Task{
			ID:     "root",
			Title:  "Root",
			Status: contracts.TaskStatusClosed,
		},
	}
	newTKStorageBackend = func(string) (contracts.StorageBackend, error) {
		return taskManagerStorageBackend{taskManager: manager}, nil
	}

	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd before defaultRun: %v", err)
	}

	runErr := defaultRun(context.Background(), runConfig{
		repoRoot:         repoRoot,
		rootID:           "root",
		backend:          backendCodex,
		concurrency:      1,
		dryRun:           true,
		watchdogTimeout:  10 * time.Minute,
		watchdogInterval: 5 * time.Second,
	})
	if runErr != nil {
		t.Fatalf("defaultRun failed: %v", runErr)
	}

	restoredWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd after defaultRun: %v", err)
	}
	if restoredWD != originalWD {
		t.Fatalf("expected defaultRun to restore cwd to %q, got %q", originalWD, restoredWD)
	}
}

func TestDefaultRunTrackerProfilesUseStorageBackendPathWhenNoReadyChildren(t *testing.T) {
	tests := []struct {
		name        string
		configYAML  string
		installMock func(t *testing.T, manager contracts.TaskManager)
	}{
		{
			name: "tk",
			configYAML: `
profiles:
  default:
    tracker:
      type: tk
`,
			installMock: func(t *testing.T, manager contracts.TaskManager) {
				t.Helper()
				originalFactory := newTKStorageBackend
				t.Cleanup(func() {
					newTKStorageBackend = originalFactory
				})
				newTKStorageBackend = func(string) (contracts.StorageBackend, error) {
					return taskManagerStorageBackend{taskManager: manager}, nil
				}
			},
		},
		{
			name: "linear",
			configYAML: `
profiles:
  default:
    tracker:
      type: linear
      linear:
        scope:
          workspace: anomaly
        auth:
          token_env: LINEAR_TOKEN
`,
			installMock: func(t *testing.T, manager contracts.TaskManager) {
				t.Helper()
				t.Setenv("LINEAR_TOKEN", "lin_api_test")
				originalFactory := newLinearStorageBackend
				t.Cleanup(func() {
					newLinearStorageBackend = originalFactory
				})
				newLinearStorageBackend = func(linear.Config) (contracts.StorageBackend, error) {
					return taskManagerStorageBackend{taskManager: manager}, nil
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repoRoot := initGitRepo(t)
			writeTrackerConfigYAML(t, repoRoot, tc.configYAML)

			originalWD, err := os.Getwd()
			if err != nil {
				t.Fatalf("getwd: %v", err)
			}
			t.Cleanup(func() {
				_ = os.Chdir(originalWD)
			})

			manager := &countingNoReadyTaskManager{
				rootTask: contracts.Task{
					ID:     "root",
					Title:  "Root",
					Status: contracts.TaskStatusClosed,
				},
			}
			tc.installMock(t, manager)

			runErr := defaultRun(context.Background(), runConfig{
				repoRoot:         repoRoot,
				rootID:           "root",
				backend:          backendCodex,
				concurrency:      1,
				dryRun:           true,
				watchdogTimeout:  10 * time.Minute,
				watchdogInterval: 5 * time.Second,
			})
			if runErr != nil {
				t.Fatalf("defaultRun failed: %v", runErr)
			}
			if manager.nextTasksCalls == 0 {
				t.Fatalf("expected NextTasks to be called")
			}
			if manager.getTaskCalls == 0 {
				t.Fatalf("expected storage-backed path to consult root task via GetTask")
			}
		})
	}
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	original := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w
	defer func() {
		os.Stderr = original
	}()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read stderr: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("close reader: %v", err)
	}
	return string(data)
}

func initGitRepo(t *testing.T) string {
	t.Helper()
	repoRoot := t.TempDir()
	cmd := exec.Command("git", "init", repoRoot)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git init failed: %v output=%s", err, string(out))
	}
	return repoRoot
}

type blockingSink struct {
	block <-chan struct{}
}

func (b blockingSink) Emit(context.Context, contracts.Event) error {
	<-b.block
	return nil
}

type countingNoReadyTaskManager struct {
	rootTask       contracts.Task
	nextTasksCalls int
	getTaskCalls   int
}

func (m *countingNoReadyTaskManager) NextTasks(context.Context, string) ([]contracts.TaskSummary, error) {
	m.nextTasksCalls++
	return nil, nil
}

func (m *countingNoReadyTaskManager) GetTask(_ context.Context, taskID string) (contracts.Task, error) {
	m.getTaskCalls++
	if taskID == m.rootTask.ID {
		return m.rootTask, nil
	}
	return contracts.Task{}, errors.New("task not found")
}

func (m *countingNoReadyTaskManager) SetTaskStatus(context.Context, string, contracts.TaskStatus) error {
	return nil
}

func (m *countingNoReadyTaskManager) SetTaskData(context.Context, string, map[string]string) error {
	return nil
}
