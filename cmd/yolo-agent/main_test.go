package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/codex"
	"github.com/egv/yolo-runner/v2/internal/codingagents"
	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/linear"
	"github.com/egv/yolo-runner/v2/internal/opencode"
	gitvcs "github.com/egv/yolo-runner/v2/internal/vcs/git"
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

	code := RunMain([]string{"--repo", "/repo", "--root", "root-1", "--backend", "codex", "--model", "openai/gpt-5.3-codex", "--max", "2", "--concurrency", "3", "--dry-run", "--runner-timeout", "30s", "--events", "/repo/runner-logs/agent.events.jsonl", "--queue", "/repo/queue.db"}, run)
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
	if got.queuePath != "/repo/queue.db" {
		t.Fatalf("expected queue path to be parsed, got %q", got.queuePath)
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

func TestRunMainRoutesTrackerWatchSubcommandAndParsesFlags(t *testing.T) {
	originalRun := runTrackerWatch
	t.Cleanup(func() {
		runTrackerWatch = originalRun
	})

	called := false
	var got trackerWatchConfig
	runTrackerWatch = func(_ context.Context, cfg trackerWatchConfig) error {
		called = true
		got = cfg
		return nil
	}

	runCalled := false
	code := RunMain([]string{"tracker-watch", "--repo", "/repo", "--profile", "linear-dev", "--once", "--dry-run"}, func(context.Context, runConfig) error {
		runCalled = true
		return nil
	})
	if code != 0 {
		t.Fatalf("expected tracker-watch exit code 0, got %d", code)
	}
	if !called {
		t.Fatalf("expected tracker-watch handler to be called")
	}
	if runCalled {
		t.Fatalf("expected legacy run function not to be called for tracker-watch")
	}
	if got.repoRoot != "/repo" {
		t.Fatalf("expected repo=/repo, got %q", got.repoRoot)
	}
	if got.profile != "linear-dev" {
		t.Fatalf("expected profile=linear-dev, got %q", got.profile)
	}
	if !got.once {
		t.Fatalf("expected once=true")
	}
	if !got.dryRun {
		t.Fatalf("expected dry-run=true")
	}
}

func TestRunMainRoutesArcReviewWatchSubcommandAndParsesFlags(t *testing.T) {
	originalRun := runArcReviewWatch
	t.Cleanup(func() {
		runArcReviewWatch = originalRun
	})

	called := false
	var got arcReviewWatchCommandConfig
	runArcReviewWatch = func(_ context.Context, cfg arcReviewWatchCommandConfig) error {
		called = true
		got = cfg
		return nil
	}

	runCalled := false
	code := RunMain([]string{"arc-review-watch", "--repo", "/repo", "--profile", "arc-dev", "--once", "--dry-run", "--events", "/tmp/events.jsonl", "--stream"}, func(context.Context, runConfig) error {
		runCalled = true
		return nil
	})
	if code != 0 {
		t.Fatalf("expected arc-review-watch exit code 0, got %d", code)
	}
	if !called {
		t.Fatalf("expected arc-review-watch handler to be called")
	}
	if runCalled {
		t.Fatalf("expected legacy run function not to be called for arc-review-watch")
	}
	if got.repoRoot != "/repo" {
		t.Fatalf("expected repo=/repo, got %q", got.repoRoot)
	}
	if got.profile != "arc-dev" {
		t.Fatalf("expected profile=arc-dev, got %q", got.profile)
	}
	if !got.once {
		t.Fatalf("expected once=true")
	}
	if !got.dryRun {
		t.Fatalf("expected dry-run=true")
	}
	if got.eventsPath != "/tmp/events.jsonl" {
		t.Fatalf("expected events path to be parsed, got %q", got.eventsPath)
	}
	if !got.stream {
		t.Fatalf("expected stream=true")
	}
}

func TestRunMainRoutesWatchSubcommandAndParsesFlags(t *testing.T) {
	originalRun := runWatch
	t.Cleanup(func() {
		runWatch = originalRun
	})

	called := false
	var got watchCommandConfig
	runWatch = func(_ context.Context, cfg watchCommandConfig) error {
		called = true
		got = cfg
		return nil
	}

	runCalled := false
	code := RunMain([]string{
		"watch",
		"--repo", "/repo",
		"--environments", "/repo/environments.yaml",
		"--events", "/tmp/watch.events.jsonl",
		"--stream",
		"--tick-interval", "250ms",
		"--idle-cooldown", "3s",
	}, func(context.Context, runConfig) error {
		runCalled = true
		return nil
	})
	if code != 0 {
		t.Fatalf("expected watch exit code 0, got %d", code)
	}
	if !called {
		t.Fatalf("expected watch handler to be called")
	}
	if runCalled {
		t.Fatalf("expected legacy run function not to be called for watch")
	}
	if got.repoRoot != "/repo" {
		t.Fatalf("expected repo=/repo, got %q", got.repoRoot)
	}
	if got.environmentsPath != "/repo/environments.yaml" {
		t.Fatalf("expected environments path, got %q", got.environmentsPath)
	}
	if got.eventsPath != "/tmp/watch.events.jsonl" {
		t.Fatalf("expected events path to be parsed, got %q", got.eventsPath)
	}
	if !got.stream {
		t.Fatalf("expected stream=true")
	}
	if got.tickInterval != 250*time.Millisecond {
		t.Fatalf("expected tick interval 250ms, got %s", got.tickInterval)
	}
	if got.idleCooldown != 3*time.Second {
		t.Fatalf("expected idle cooldown 3s, got %s", got.idleCooldown)
	}
}

func TestDefaultRunArcReviewWatchDelegatesToSourceArcPR(t *testing.T) {
	repoRoot := t.TempDir()
	eventsPath := filepath.Join(repoRoot, "events.jsonl")

	originalRunSource := runSourceArcPR
	t.Cleanup(func() {
		runSourceArcPR = originalRunSource
	})

	var captured []sourceArcPRCommandConfig
	runSourceArcPR = func(_ context.Context, cfg sourceArcPRCommandConfig) error {
		captured = append(captured, cfg)
		return nil
	}

	stderrText := captureStderr(t, func() {
		err := defaultRunArcReviewWatch(context.Background(), arcReviewWatchCommandConfig{
			repoRoot:   repoRoot,
			profile:    "default",
			once:       true,
			dryRun:     true,
			eventsPath: eventsPath,
		})
		if err != nil {
			t.Fatalf("defaultRunArcReviewWatch failed: %v", err)
		}
	})
	if len(captured) != 1 {
		t.Fatalf("source arcpr calls = %d, want 1", len(captured))
	}
	if got := captured[0]; got.repoRoot != repoRoot || got.profile != "default" || !got.once || got.eventsPath != eventsPath {
		t.Fatalf("source arcpr config mismatch: %#v", got)
	}
	if !strings.Contains(stderrText, "arc-review-watch is deprecated") || !strings.Contains(stderrText, "--dry-run is ignored") {
		t.Fatalf("stderr missing shim warnings: %q", stderrText)
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

func TestRunMainDefaultsBackendToOpencode(t *testing.T) {
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

func TestNormalizeBackendDefaultsToOpencode(t *testing.T) {
	if got := normalizeBackend(""); got != backendOpenCode {
		t.Fatalf("expected empty backend to normalize to %q, got %q", backendOpenCode, got)
	}
	if got := normalizeBackend("   "); got != backendOpenCode {
		t.Fatalf("expected whitespace backend to normalize to %q, got %q", backendOpenCode, got)
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

func TestRunMainAcceptsCodexCLILegacyFallbackBackend(t *testing.T) {
	called := false
	var got runConfig
	run := func(_ context.Context, cfg runConfig) error {
		called = true
		got = cfg
		return nil
	}

	code := RunMain([]string{"--repo", "/repo", "--root", "root-1", "--agent-backend", "codex-cli"}, run)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if !called {
		t.Fatalf("expected run function to be called")
	}
	if got.backend != "codex-cli" {
		t.Fatalf("expected backend=%q, got %q", "codex-cli", got.backend)
	}
}

func TestBuildRunnerAdapterUsesCLIAdapterForOpencodeACPBackend(t *testing.T) {
	catalog, err := codingagents.LoadCatalog("")
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}

	runner, err := buildRunnerAdapter(runConfig{
		backend:      backendOpenCodeACP,
		codingAgents: catalog,
	})
	if err != nil {
		t.Fatalf("build opencode-acp adapter: %v", err)
	}
	if _, ok := runner.(*opencode.CLIRunnerAdapter); !ok {
		t.Fatalf("expected *opencode.CLIRunnerAdapter for %q backend, got %T", backendOpenCodeACP, runner)
	}
}

func TestBuildRunnerAdapterUsesServeAdapterForOpencodeServeBackend(t *testing.T) {
	catalog, err := codingagents.LoadCatalog("")
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}

	runner, err := buildRunnerAdapter(runConfig{
		backend:      "opencode-serve",
		codingAgents: catalog,
	})
	if err != nil {
		t.Fatalf("build opencode-serve adapter: %v", err)
	}
	if _, ok := runner.(*opencode.ServeRunnerAdapter); !ok {
		t.Fatalf("expected *opencode.ServeRunnerAdapter, got %T", runner)
	}
}

func TestBuildRunnerAdapterUsesCodexAppServerAndCodexCLIFallback(t *testing.T) {
	repoRoot := t.TempDir()
	binDir := t.TempDir()
	binaryPath := filepath.Join(binDir, "codex")
	if err := os.WriteFile(binaryPath, []byte("#!/bin/sh\nprintf 'REVIEW_VERDICT: pass\\n'\n"), 0o755); err != nil {
		t.Fatalf("write fake codex binary: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	catalog, err := codingagents.LoadCatalog(repoRoot)
	if err != nil {
		t.Fatalf("load coding agent catalog: %v", err)
	}

	appServerRunner, err := buildRunnerAdapter(runConfig{
		backend:      backendCodex,
		codingAgents: catalog,
	})
	if err != nil {
		t.Fatalf("build codex app-server adapter: %v", err)
	}
	if _, ok := appServerRunner.(*codex.CLIRunnerAdapter); !ok {
		t.Fatalf("expected %q backend to use codex app-server adapter, got %T", backendCodex, appServerRunner)
	}

	fallbackRunner, err := buildRunnerAdapter(runConfig{
		backend:      backendCodexCLI,
		codingAgents: catalog,
	})
	if err != nil {
		t.Fatalf("build codex-cli fallback adapter: %v", err)
	}
	fallback, ok := fallbackRunner.(*codingagents.GenericCLIRunnerAdapter)
	if !ok {
		t.Fatalf("expected %q backend to use generic CLI adapter, got %T", backendCodexCLI, fallbackRunner)
	}

	result, err := fallback.Run(context.Background(), contracts.RunnerRequest{
		TaskID:   "task-1",
		Prompt:   "legacy fallback",
		Mode:     contracts.RunnerModeReview,
		Model:    "openai/gpt-5.3-codex",
		RepoRoot: repoRoot,
	})
	if err != nil {
		t.Fatalf("run codex-cli fallback adapter: %v", err)
	}
	if result.Status != contracts.RunnerResultCompleted {
		t.Fatalf("expected codex-cli fallback to complete, got %s", result.Status)
	}
	if result.Artifacts["backend"] != backendCodexCLI {
		t.Fatalf("expected fallback backend artifact %q, got %q", backendCodexCLI, result.Artifacts["backend"])
	}
	if result.Artifacts["review_verdict"] != "pass" {
		t.Fatalf("expected fallback review verdict pass, got %#v", result.Artifacts)
	}
	if !result.ReviewReady {
		t.Fatalf("expected fallback review run to be marked ready")
	}
}

func TestBuildRunnerAdapterDefaultBackendUsesOpencodeServeAdapter(t *testing.T) {
	catalog, err := codingagents.LoadCatalog("")
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}

	runner, err := buildRunnerAdapter(runConfig{
		backend:      "",
		codingAgents: catalog,
	})
	if err != nil {
		t.Fatalf("build default adapter: %v", err)
	}
	if _, ok := runner.(*opencode.ServeRunnerAdapter); !ok {
		t.Fatalf("expected default backend to use opencode serve adapter, got %T", runner)
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
	if got.model != "gpt-5.6-sol" {
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

func TestRunWithComponentsArcPRLandingUsesConfiguredRepoRootWithoutClone(t *testing.T) {
	repoRoot := t.TempDir()
	writeTrackerConfigYAML(t, repoRoot, `
profiles:
  default:
    tracker:
      type: tk
landing:
  type: arc-pr
`)

	mgr := &testTaskManager{tasks: []contracts.Task{{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen}}}
	runner := &serviceTrackingRunner{result: contracts.RunnerResult{Status: contracts.RunnerResultCompleted, ReviewReady: true}}
	cfg := runConfig{repoRoot: repoRoot, rootID: "root"}

	runErr := runWithComponents(context.Background(), cfg, mgr, runner, nil)
	if runErr != nil {
		t.Fatalf("runWithComponents failed: %v", runErr)
	}

	req, ok := runner.lastRequest()
	if !ok {
		t.Fatalf("expected runner request")
	}
	if req.RepoRoot != repoRoot {
		t.Fatalf("expected runner repo root %q, got %q", repoRoot, req.RepoRoot)
	}
	if _, err := os.Stat(filepath.Join(repoRoot, ".yolo-runner", "clones")); !os.IsNotExist(err) {
		t.Fatalf("expected clone directory not to be created, got stat error %v", err)
	}
}

func TestCloneScopedVCSFactorySelectsLandingAdapter(t *testing.T) {
	tests := []struct {
		name       string
		configYAML string
		wantType   string
	}{
		{
			name:     "git default",
			wantType: "*git.VCSAdapter",
		},
		{
			name: "arc-pr",
			configYAML: `
profiles:
  default:
    tracker:
      type: tk
landing:
  type: arc-pr
`,
			wantType: "*arc.Adapter",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repoRoot := t.TempDir()
			if strings.TrimSpace(tt.configYAML) != "" {
				writeTrackerConfigYAML(t, repoRoot, tt.configYAML)
			}

			rootVCS := gitvcs.NewVCSAdapter(localGitRunner{dir: repoRoot})
			factory := cloneScopedVCSFactory(runConfig{repoRoot: repoRoot}, rootVCS)
			if factory == nil {
				t.Fatalf("expected VCS factory")
			}

			got := factory("")
			if got == nil {
				t.Fatalf("expected scoped VCS")
			}
			if gotType := reflect.TypeOf(got).String(); gotType != tt.wantType {
				t.Fatalf("expected scoped VCS type %s, got %s", tt.wantType, gotType)
			}
		})
	}
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
	runner := &progressRunner{updates: []contracts.RunnerProgress{{Type: "agent_text", Message: "1"}, {Type: "agent_text", Message: "2"}, {Type: "agent_text", Message: "3"}, {Type: "agent_text", Message: "4"}}}
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
	if got := strings.Count(out, `"type":"agent_text"`); got != 2 {
		t.Fatalf("expected coalesced agent_text count=2, got %d output=%q", got, out)
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
	runner := &progressRunner{updates: []contracts.RunnerProgress{{Type: "agent_text", Message: "1"}, {Type: "agent_text", Message: "2"}, {Type: "agent_text", Message: "3"}, {Type: "agent_text", Message: "4"}}}
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
	if got := strings.Count(out, `"type":"agent_text"`); got != 4 {
		t.Fatalf("expected full agent_text count=4, got %d output=%q", got, out)
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

func TestRunWithComponentsTerminalEventOnFatalExit(t *testing.T) {
	if mode := os.Getenv("YOLO_AGENT_TERMINAL_EVENT_CHILD"); mode != "" {
		runTerminalEventChild(t, mode, os.Getenv("YOLO_AGENT_TERMINAL_EVENT_PATH"))
		return
	}

	for _, tc := range []struct {
		name           string
		mode           string
		sendSignal     bool
		expectedReason string
	}{
		{name: "panic", mode: "panic", expectedReason: "panic: terminal event test panic"},
		{name: "sigterm", mode: "sigterm", sendSignal: true, expectedReason: "signal: terminated"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			eventsPath := filepath.Join(t.TempDir(), "events.jsonl")
			exe, err := os.Executable()
			if err != nil {
				t.Fatalf("test executable: %v", err)
			}
			cmd := exec.Command(exe, "-test.run", "^TestRunWithComponentsTerminalEventOnFatalExit$")
			cmd.Env = append(os.Environ(),
				"YOLO_AGENT_TERMINAL_EVENT_CHILD="+tc.mode,
				"YOLO_AGENT_TERMINAL_EVENT_PATH="+eventsPath,
			)
			var output strings.Builder
			cmd.Stdout = &output
			cmd.Stderr = &output
			if err := cmd.Start(); err != nil {
				t.Fatalf("start child: %v", err)
			}

			if tc.sendSignal {
				waitForEventType(t, eventsPath, contracts.EventTypeRunStarted)
				if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
					t.Fatalf("signal child: %v", err)
				}
			}

			done := make(chan error, 1)
			go func() {
				done <- cmd.Wait()
			}()
			select {
			case err := <-done:
				if err == nil {
					t.Fatalf("expected child to exit non-zero")
				}
			case <-time.After(5 * time.Second):
				_ = cmd.Process.Kill()
				t.Fatalf("child did not exit; output=%s", output.String())
			}

			assertTerminalRunFinishedEvent(t, eventsPath, tc.expectedReason)
		})
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

	if err := wrapped.Emit(context.Background(), contracts.Event{Type: contracts.EventTypeAgentText, TaskID: "t-1", Timestamp: time.Now().UTC()}); err != nil {
		t.Fatalf("first emit failed: %v", err)
	}

	start := time.Now()
	if err := wrapped.Emit(context.Background(), contracts.Event{Type: contracts.EventTypeAgentText, TaskID: "t-1", Timestamp: time.Now().UTC()}); err != nil {
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

type terminalEventPanicRunner struct{}

func (terminalEventPanicRunner) Run(context.Context, contracts.RunnerRequest) (contracts.RunnerResult, error) {
	panic("terminal event test panic")
}

type terminalEventBlockingRunner struct{}

func (terminalEventBlockingRunner) Run(ctx context.Context, _ contracts.RunnerRequest) (contracts.RunnerResult, error) {
	<-ctx.Done()
	return contracts.RunnerResult{Status: contracts.RunnerResultFailed}, ctx.Err()
}

func runTerminalEventChild(t *testing.T, mode, eventsPath string) {
	t.Helper()
	if strings.TrimSpace(eventsPath) == "" {
		t.Fatalf("missing terminal event path")
	}
	repoRoot := initGitRepo(t)
	mgr := &testTaskManager{tasks: []contracts.Task{{ID: "t-1", Title: "Task 1", Status: contracts.TaskStatusOpen}}}
	var runner contracts.AgentRunner
	switch mode {
	case "panic":
		runner = terminalEventPanicRunner{}
	case "sigterm":
		runner = terminalEventBlockingRunner{}
	default:
		t.Fatalf("unknown terminal event child mode %q", mode)
	}
	cfg := runConfig{
		repoRoot:             repoRoot,
		rootID:               "root",
		eventsPath:           eventsPath,
		concurrency:          1,
		watchdogTimeout:      10 * time.Minute,
		watchdogInterval:     5 * time.Second,
		streamOutputBuffer:   64,
		streamOutputInterval: 150 * time.Millisecond,
	}
	if err := runWithComponents(context.Background(), cfg, mgr, runner, nil); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func waitForEventType(t *testing.T, path string, eventType contracts.EventType) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if eventLogContainsType(path, eventType) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	raw, _ := os.ReadFile(path)
	t.Fatalf("timed out waiting for %s in %s; log=%q", eventType, path, string(raw))
}

func eventLogContainsType(path string, eventType contracts.EventType) bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		event, err := contracts.ParseEventJSONLLine([]byte(line))
		if err != nil {
			continue
		}
		if event.Type == eventType {
			return true
		}
	}
	return false
}

func assertTerminalRunFinishedEvent(t *testing.T, path string, expectedReason string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read event log: %v", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		event, err := contracts.ParseEventJSONLLine([]byte(line))
		if err != nil {
			t.Fatalf("parse event line %q: %v", line, err)
		}
		if event.Type != contracts.EventTypeRunFinished {
			continue
		}
		if got := event.Metadata["status"]; got != "failed" {
			t.Fatalf("run_finished status = %q, want failed; event=%+v", got, event)
		}
		if got := event.Metadata["reason"]; !strings.Contains(got, expectedReason) {
			t.Fatalf("run_finished reason = %q, want substring %q; event=%+v", got, expectedReason, event)
		}
		return
	}
	t.Fatalf("expected terminal run_finished event in %s, got %q", path, string(raw))
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
