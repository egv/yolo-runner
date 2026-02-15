package main

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anomalyco/yolo-runner/internal/contracts"
)

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

func TestRunMainInjectsSharedConfigService(t *testing.T) {
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
	if got.configService == nil {
		t.Fatalf("expected run config to include shared config service")
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
