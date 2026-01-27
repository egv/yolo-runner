package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anomalyco/yolo-runner/internal/opencode"
	"github.com/anomalyco/yolo-runner/internal/runner"
	tea "github.com/charmbracelet/bubbletea"
)

type fakeRunner struct {
	calls []string
}

var _ runner.OpenCodeContextRunner = openCodeAdapter{}

func (f *fakeRunner) Run(args ...string) (string, error) {
	f.calls = append(f.calls, strings.Join(args, " "))
	return "", nil
}

type fakeOpenCodeRunLogger struct {
	called     bool
	issueID    string
	repoRoot   string
	prompt     string
	model      string
	configRoot string
	configDir  string
	logPath    string
}

func (f *fakeOpenCodeRunLogger) Run(issueID string, repoRoot string, prompt string, model string, configRoot string, configDir string, logPath string) error {
	f.called = true
	f.issueID = issueID
	f.repoRoot = repoRoot
	f.prompt = prompt
	f.model = model
	f.configRoot = configRoot
	f.configDir = configDir
	f.logPath = logPath
	return nil
}

type fakeOpenCodeRunner struct {
	env      map[string]string
	args     []string
	original opencode.Runner
}

type fakeOpenCodeProcess struct{}

func (fakeOpenCodeProcess) Wait() error { return nil }

func (fakeOpenCodeProcess) Kill() error { return nil }

func (f *fakeOpenCodeRunner) Start(args []string, env map[string]string, stdoutPath string) (opencode.Process, error) {
	f.args = args
	f.env = env
	return fakeOpenCodeProcess{}, nil
}

type fakeGitRunner struct {
	calls  []string
	output string
	err    error
}

func (f *fakeGitRunner) Run(name string, args ...string) (string, error) {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	return f.output, f.err
}

type fakeExit struct {
	code int
}

func (f *fakeExit) Exit(code int) {
	f.code = code
}

type fakeRunOnce struct {
	called bool
	opts   runner.RunOnceOptions
	deps   runner.RunOnceDeps
	result string
	err    error
}

func (f *fakeRunOnce) Run(opts runner.RunOnceOptions, deps runner.RunOnceDeps) (string, error) {
	f.called = true
	f.opts = opts
	f.deps = deps
	return f.result, f.err
}

func TestRunOnceMainReturnsErrorCodeOnFailure(t *testing.T) {
	tempDir := t.TempDir()
	writeAgentFile(t, tempDir, "---\npermission: allow\n---\n")
	runOnce := &fakeRunOnce{err: errors.New("boom")}
	exit := &fakeExit{}
	out := &bytes.Buffer{}

	code := RunOnceMain([]string{"--repo", tempDir, "--root", "root"}, runOnce.Run, exit.Exit, out, out, nil, nil)

	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	if exit.code != 1 {
		t.Fatalf("expected exit to be called with 1, got %d", exit.code)
	}
	if !strings.Contains(out.String(), "boom") {
		t.Fatalf("expected error output, got %q", out.String())
	}
}

func TestRunOnceMainWiresDependencies(t *testing.T) {
	tempDir := t.TempDir()
	writeAgentFile(t, tempDir, "---\npermission: allow\n---\n")
	runOnce := &fakeRunOnce{result: "no_tasks"}
	exit := &fakeExit{}
	out := &bytes.Buffer{}
	beadsRunner := &fakeRunner{}
	gitRunner := &fakeGitRunner{}

	RunOnceMain([]string{"--repo", tempDir, "--root", "root", "--model", "model", "--dry-run"}, runOnce.Run, exit.Exit, out, out, beadsRunner, gitRunner)

	if !runOnce.called {
		t.Fatalf("expected run once to be called")
	}
	if runOnce.opts.RepoRoot != tempDir || runOnce.opts.RootID != "root" || runOnce.opts.Model != "model" || !runOnce.opts.DryRun {
		t.Fatalf("unexpected options: %#v", runOnce.opts)
	}
	if runOnce.opts.Out == nil {
		t.Fatalf("expected output writer")
	}
	if runOnce.deps.Beads == nil || runOnce.deps.Git == nil || runOnce.deps.Prompt == nil || runOnce.deps.OpenCode == nil || runOnce.deps.Logger == nil {
		t.Fatalf("expected deps to be wired")
	}
	if exit.code != 0 {
		t.Fatalf("expected exit code 0, got %d", exit.code)
	}
}

func TestRunOnceMainDefaultsConfigPaths(t *testing.T) {
	tempDir := t.TempDir()
	writeAgentFile(t, tempDir, "---\npermission: allow\n---\n")
	runOnce := &fakeRunOnce{result: "no_tasks"}
	exit := &fakeExit{}
	out := &bytes.Buffer{}
	beadsRunner := &fakeRunner{}
	gitRunner := &fakeGitRunner{}

	t.Setenv("HOME", "/home/user")
	t.Setenv("XDG_CONFIG_HOME", "")

	RunOnceMain([]string{"--repo", tempDir, "--root", "root"}, runOnce.Run, exit.Exit, out, out, beadsRunner, gitRunner)

	if !runOnce.called {
		t.Fatalf("expected run once to be called")
	}
	if runOnce.opts.ConfigRoot == "" {
		t.Fatalf("expected config root to be set")
	}
	if runOnce.opts.ConfigDir == "" {
		t.Fatalf("expected config dir to be set")
	}
	expectedConfigRoot := filepath.Join("/home/user", ".config", "opencode-runner")
	if runOnce.opts.ConfigRoot != expectedConfigRoot {
		t.Fatalf("expected config root %q, got %q", expectedConfigRoot, runOnce.opts.ConfigRoot)
	}
	expectedConfigDir := filepath.Join(expectedConfigRoot, "opencode")
	if runOnce.opts.ConfigDir != expectedConfigDir {
		t.Fatalf("expected config dir %q, got %q", expectedConfigDir, runOnce.opts.ConfigDir)
	}
	if exit.code != 0 {
		t.Fatalf("expected exit code 0, got %d", exit.code)
	}
}

func TestRunOnceMainFailsWhenYoloAgentMissing(t *testing.T) {
	tempDir := t.TempDir()
	runOnce := &fakeRunOnce{result: "no_tasks"}
	exit := &fakeExit{}
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	beadsRunner := &fakeRunner{}
	gitRunner := &fakeGitRunner{}

	code := RunOnceMain([]string{"--repo", tempDir, "--root", "root"}, runOnce.Run, exit.Exit, stdout, stderr, beadsRunner, gitRunner)

	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	if exit.code != 1 {
		t.Fatalf("expected exit to be called with 1, got %d", exit.code)
	}
	if runOnce.called {
		t.Fatalf("expected run once not to be called")
	}
	if !strings.Contains(stderr.String(), "yolo.md") {
		t.Fatalf("expected error to mention yolo.md, got %q", stderr.String())
	}
}

func TestRunOnceMainFailsWhenYoloAgentMissingPermission(t *testing.T) {
	tempDir := t.TempDir()
	writeAgentFile(t, tempDir, "---\nname: yolo\n---\n")
	runOnce := &fakeRunOnce{result: "no_tasks"}
	exit := &fakeExit{}
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	beadsRunner := &fakeRunner{}
	gitRunner := &fakeGitRunner{}

	code := RunOnceMain([]string{"--repo", tempDir, "--root", "root"}, runOnce.Run, exit.Exit, stdout, stderr, beadsRunner, gitRunner)

	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	if exit.code != 1 {
		t.Fatalf("expected exit to be called with 1, got %d", exit.code)
	}
	if runOnce.called {
		t.Fatalf("expected run once not to be called")
	}
	if !strings.Contains(stderr.String(), "permission: allow") {
		t.Fatalf("expected error to mention permission allow, got %q", stderr.String())
	}
	if !strings.Contains(strings.ToLower(stderr.String()), "init") {
		t.Fatalf("expected guidance to run init, got %q", stderr.String())
	}
}

func TestRunOnceMainInfersRoadmapRootWhenMissing(t *testing.T) {
	tempDir := t.TempDir()
	writeIssuesFile(t, tempDir, `{"id":"roadmap-1","title":"Roadmap","issue_type":"epic","status":"open"}`)
	writeAgentFile(t, tempDir, "---\npermission: allow\n---\n")
	runOnce := &fakeRunOnce{result: "no_tasks"}
	exit := &fakeExit{}
	out := &bytes.Buffer{}
	beadsRunner := &fakeRunner{}
	gitRunner := &fakeGitRunner{}

	code := RunOnceMain([]string{"--repo", tempDir}, runOnce.Run, exit.Exit, out, out, beadsRunner, gitRunner)

	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if !runOnce.called {
		t.Fatalf("expected run once to be called")
	}
	if runOnce.opts.RootID != "roadmap-1" {
		t.Fatalf("expected root id to be inferred, got %q", runOnce.opts.RootID)
	}
}

func TestRunOnceMainInfersRoadmapRootWhenInProgress(t *testing.T) {
	tempDir := t.TempDir()
	writeIssuesFile(t, tempDir, `{"id":"roadmap-2","title":"Roadmap","issue_type":"epic","status":"in_progress"}`)
	writeAgentFile(t, tempDir, "---\npermission: allow\n---\n")
	runOnce := &fakeRunOnce{result: "no_tasks"}
	exit := &fakeExit{}
	out := &bytes.Buffer{}
	beadsRunner := &fakeRunner{}
	gitRunner := &fakeGitRunner{}

	code := RunOnceMain([]string{"--repo", tempDir}, runOnce.Run, exit.Exit, out, out, beadsRunner, gitRunner)

	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if !runOnce.called {
		t.Fatalf("expected run once to be called")
	}
	if runOnce.opts.RootID != "roadmap-2" {
		t.Fatalf("expected root id to be inferred, got %q", runOnce.opts.RootID)
	}
}

func TestRunOnceMainMissingRootRequiresExplicitFlag(t *testing.T) {
	tempDir := t.TempDir()
	writeIssuesFile(t, tempDir, `{"id":"epic-1","title":"Other","issue_type":"epic","status":"open"}`)
	writeAgentFile(t, tempDir, "---\npermission: allow\n---\n")
	runOnce := &fakeRunOnce{result: "no_tasks"}
	exit := &fakeExit{}
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	beadsRunner := &fakeRunner{}
	gitRunner := &fakeGitRunner{}

	code := RunOnceMain([]string{"--repo", tempDir}, runOnce.Run, exit.Exit, stdout, stderr, beadsRunner, gitRunner)

	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	if exit.code != 1 {
		t.Fatalf("expected exit to be called with 1, got %d", exit.code)
	}
	if runOnce.called {
		t.Fatalf("expected run once not to be called")
	}
	if !strings.Contains(stderr.String(), "--root") {
		t.Fatalf("expected error to mention --root, got %q", stderr.String())
	}
	if !strings.Contains(strings.ToLower(stderr.String()), "roadmap") {
		t.Fatalf("expected error to mention roadmap, got %q", stderr.String())
	}
}

type fakeTUIProgram struct {
	started chan struct{}
	quit    chan struct{}
	events  chan runner.Event
	inputs  chan tea.Msg
}

func newFakeTUIProgram() *fakeTUIProgram {
	return &fakeTUIProgram{
		started: make(chan struct{}),
		quit:    make(chan struct{}),
		events:  make(chan runner.Event, 1),
		inputs:  make(chan tea.Msg, 1),
	}
}

func (f *fakeTUIProgram) Start() error {
	close(f.started)
	return nil
}

func (f *fakeTUIProgram) Send(event runner.Event) {
	f.events <- event
}

func (f *fakeTUIProgram) SendInput(msg tea.Msg) {
	f.inputs <- msg
}

func (f *fakeTUIProgram) Quit() {
	close(f.quit)
}

func waitForSignal(t *testing.T, signal chan struct{}, label string) {
	t.Helper()
	select {
	case <-signal:
		return
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("expected %s", label)
	}
}

func writeIssuesFile(t *testing.T, repoRoot string, payload string) {
	t.Helper()
	beadsDir := filepath.Join(repoRoot, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatalf("mkdir .beads: %v", err)
	}
	issuesPath := filepath.Join(beadsDir, "issues.jsonl")
	if err := os.WriteFile(issuesPath, []byte(payload+"\n"), 0o644); err != nil {
		t.Fatalf("write issues: %v", err)
	}
}

func writeAgentFile(t *testing.T, repoRoot string, payload string) {
	t.Helper()
	agentDir := filepath.Join(repoRoot, ".opencode", "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatalf("mkdir agent dir: %v", err)
	}
	agentPath := filepath.Join(agentDir, "yolo.md")
	if err := os.WriteFile(agentPath, []byte(payload), 0o644); err != nil {
		t.Fatalf("write agent file: %v", err)
	}
}

func writeRootYoloFile(t *testing.T, repoRoot string, payload string) {
	t.Helper()
	agentPath := filepath.Join(repoRoot, "yolo.md")
	if err := os.WriteFile(agentPath, []byte(payload), 0o644); err != nil {
		t.Fatalf("write yolo.md: %v", err)
	}
}

func TestRunOnceMainUsesTUIOnTTYByDefault(t *testing.T) {
	fakeProgram := newFakeTUIProgram()
	prevIsTerminal := isTerminal
	prevNewTUIProgram := newTUIProgram
	isTerminal = func(io.Writer) bool { return true }
	newTUIProgram = func(model tea.Model, stdout io.Writer, input io.Reader) tuiProgram { return fakeProgram }
	t.Cleanup(func() {
		isTerminal = prevIsTerminal
		newTUIProgram = prevNewTUIProgram
	})

	tempDir := t.TempDir()
	writeAgentFile(t, tempDir, "---\npermission: allow\n---\n")
	runOnce := &fakeRunOnce{result: "no_tasks"}
	exit := &fakeExit{}
	out := &bytes.Buffer{}
	beadsRunner := &fakeRunner{}
	gitRunner := &fakeGitRunner{}

	code := RunOnceMain([]string{"--repo", tempDir, "--root", "root"}, runOnce.Run, exit.Exit, out, out, beadsRunner, gitRunner)

	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if runOnce.deps.Events == nil {
		t.Fatalf("expected events emitter to be set")
	}
	runOnce.deps.Events.Emit(runner.Event{Type: runner.EventSelectTask})
	waitForSignal(t, fakeProgram.started, "tui start")
	waitForSignal(t, fakeProgram.quit, "tui quit")
	select {
	case <-fakeProgram.events:
		// ok
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("expected event to be forwarded to TUI")
	}
}

func TestRunOnceMainWiresStopCleanupHooks(t *testing.T) {
	fakeProgram := newFakeTUIProgram()
	prevIsTerminal := isTerminal
	prevNewTUIProgram := newTUIProgram
	isTerminal = func(io.Writer) bool { return true }
	newTUIProgram = func(model tea.Model, stdout io.Writer, input io.Reader) tuiProgram { return fakeProgram }
	t.Cleanup(func() {
		isTerminal = prevIsTerminal
		newTUIProgram = prevNewTUIProgram
	})

	tempDir := t.TempDir()
	writeAgentFile(t, tempDir, "---\npermission: allow\n---\n")
	runOnce := &fakeRunOnce{result: "no_tasks"}
	exit := &fakeExit{}
	out := &bytes.Buffer{}
	beadsRunner := &fakeRunner{}
	gitRunner := &fakeGitRunner{}

	code := RunOnceMain([]string{"--repo", tempDir, "--root", "root"}, runOnce.Run, exit.Exit, out, out, beadsRunner, gitRunner)

	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if runOnce.opts.Stop == nil {
		t.Fatalf("expected stop channel to be wired")
	}
	if runOnce.opts.CleanupConfirm == nil {
		t.Fatalf("expected cleanup confirmation to be wired")
	}
	if runOnce.opts.StatusPorcelain == nil {
		t.Fatalf("expected status porcelain hook to be wired")
	}
	if runOnce.opts.GitRestoreAll == nil {
		t.Fatalf("expected git restore hook to be wired")
	}
	if runOnce.opts.GitCleanAll == nil {
		t.Fatalf("expected git clean hook to be wired")
	}
	waitForSignal(t, fakeProgram.started, "tui start")
	waitForSignal(t, fakeProgram.quit, "tui quit")
}

func TestRunOnceMainHeadlessDisablesTUI(t *testing.T) {
	called := false
	prevIsTerminal := isTerminal
	prevNewTUIProgram := newTUIProgram
	isTerminal = func(io.Writer) bool { return true }
	newTUIProgram = func(model tea.Model, stdout io.Writer, input io.Reader) tuiProgram {
		called = true
		return newFakeTUIProgram()
	}
	t.Cleanup(func() {
		isTerminal = prevIsTerminal
		newTUIProgram = prevNewTUIProgram
	})

	tempDir := t.TempDir()
	writeAgentFile(t, tempDir, "---\npermission: allow\n---\n")
	runOnce := &fakeRunOnce{result: "no_tasks"}
	exit := &fakeExit{}
	out := &bytes.Buffer{}
	beadsRunner := &fakeRunner{}
	gitRunner := &fakeGitRunner{}
	stderr := &bytes.Buffer{}

	RunOnceMain([]string{"--repo", tempDir, "--root", "root", "--headless"}, runOnce.Run, exit.Exit, out, stderr, beadsRunner, gitRunner)

	if called {
		t.Fatalf("expected TUI program not to start")
	}
	if runOnce.deps.Events != nil {
		t.Fatalf("expected no events emitter in headless mode")
	}
}

func TestRunOnceMainInitOverwritesAgentFile(t *testing.T) {
	tempDir := t.TempDir()
	writeRootYoloFile(t, tempDir, "root-agent")
	writeAgentFile(t, tempDir, "stale")

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	exit := &fakeExit{}

	code := RunOnceMain([]string{"init", "--repo", tempDir}, nil, exit.Exit, stdout, stderr, nil, nil)

	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if exit.code != 0 {
		t.Fatalf("expected exit code 0, got %d", exit.code)
	}
	agentPath := filepath.Join(tempDir, ".opencode", "agent", "yolo.md")
	content, err := os.ReadFile(agentPath)
	if err != nil {
		t.Fatalf("read agent file: %v", err)
	}
	if string(content) != "root-agent" {
		t.Fatalf("expected agent file to be overwritten")
	}
}

func TestRunOnceMainInitCreatesAgentDir(t *testing.T) {
	tempDir := t.TempDir()
	writeRootYoloFile(t, tempDir, "root-agent")

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	exit := &fakeExit{}

	code := RunOnceMain([]string{"init", "--repo", tempDir}, nil, exit.Exit, stdout, stderr, nil, nil)

	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	agentPath := filepath.Join(tempDir, ".opencode", "agent", "yolo.md")
	content, err := os.ReadFile(agentPath)
	if err != nil {
		t.Fatalf("read agent file: %v", err)
	}
	if string(content) != "root-agent" {
		t.Fatalf("expected agent file to be created")
	}
}

func TestRunOnceMainRunModePassesAfterInit(t *testing.T) {
	tempDir := t.TempDir()
	writeRootYoloFile(t, tempDir, "---\npermission: allow\n---\n")

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	exit := &fakeExit{}

	code := RunOnceMain([]string{"init", "--repo", tempDir}, nil, exit.Exit, stdout, stderr, nil, nil)
	if code != 0 {
		t.Fatalf("expected init exit code 0, got %d", code)
	}

	runOnce := &fakeRunOnce{result: "no_tasks"}
	code = RunOnceMain([]string{"--repo", tempDir, "--root", "root"}, runOnce.Run, exit.Exit, stdout, stderr, nil, nil)

	if code != 0 {
		t.Fatalf("expected run mode exit code 0, got %d", code)
	}
	if !runOnce.called {
		t.Fatalf("expected run once to be called")
	}
}

func TestOpenCodeRunDefaultsCreateConfigAndEnv(t *testing.T) {
	tempDir := t.TempDir()
	repoRoot := filepath.Join(tempDir, "repo")
	if err := os.MkdirAll(repoRoot, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	t.Setenv("HOME", tempDir)

	openCodeRunner := &fakeOpenCodeRunner{}
	acpCalled := false
	acpClient := opencode.ACPClientFunc(func(ctx context.Context, issueID string, logPath string) error {
		acpCalled = true
		return nil
	})
	openCodeAdapter := openCodeAdapter{runner: openCodeRunner, acpClient: acpClient}

	configRoot := filepath.Join(tempDir, ".config", "opencode-runner")
	configDir := filepath.Join(configRoot, "opencode")
	logPath := filepath.Join(tempDir, "runner-logs", "opencode", "issue-1.jsonl")

	if err := openCodeAdapter.Run("issue-1", repoRoot, "prompt", "", configRoot, configDir, logPath); err != nil {
		t.Fatalf("open code run error: %v", err)
	}
	if !acpCalled {
		t.Fatalf("expected acp client to be called")
	}

	configFile := filepath.Join(configDir, "opencode.json")
	if _, err := os.Stat(configFile); err != nil {
		t.Fatalf("expected opencode.json to exist: %v", err)
	}
	expectedConfigFile := filepath.Join(configDir, "opencode.json")
	if openCodeRunner.env["XDG_CONFIG_HOME"] != configRoot {
		t.Fatalf("expected XDG_CONFIG_HOME set")
	}
	if openCodeRunner.env["OPENCODE_CONFIG_DIR"] != configDir {
		t.Fatalf("expected OPENCODE_CONFIG_DIR set")
	}
	if openCodeRunner.env["OPENCODE_CONFIG"] != expectedConfigFile {
		t.Fatalf("expected OPENCODE_CONFIG set")
	}
	if openCodeRunner.env["OPENCODE_CONFIG_CONTENT"] != "{}" {
		t.Fatalf("expected OPENCODE_CONFIG_CONTENT set")
	}
}

func TestOpenCodeRunWithModelInACPMode(t *testing.T) {
	tempDir := t.TempDir()
	repoRoot := filepath.Join(tempDir, "repo")
	if err := os.MkdirAll(repoRoot, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}

	openCodeRunner := &fakeOpenCodeRunner{}
	acpCalled := false
	acpClient := opencode.ACPClientFunc(func(ctx context.Context, issueID string, logPath string) error {
		acpCalled = true
		return nil
	})
	openCodeAdapter := openCodeAdapter{runner: openCodeRunner, acpClient: acpClient}

	configRoot := filepath.Join(tempDir, ".config", "opencode-runner")
	configDir := filepath.Join(configRoot, "opencode")
	logPath := filepath.Join(tempDir, "runner-logs", "opencode", "issue-1.jsonl")

	if err := openCodeAdapter.Run("issue-1", repoRoot, "prompt", "zai-coding-plan/glm-4.7", configRoot, configDir, logPath); err != nil {
		t.Fatalf("open code run error: %v", err)
	}
	if !acpCalled {
		t.Fatalf("expected acp client to be called")
	}

	// Check that --model flag is in args
	foundModel := false
	for i, arg := range openCodeRunner.args {
		if arg == "--model" && i+1 < len(openCodeRunner.args) && openCodeRunner.args[i+1] == "zai-coding-plan/glm-4.7" {
			foundModel = true
			break
		}
	}
	if !foundModel {
		t.Fatalf("expected --model flag with value in args: %v", openCodeRunner.args)
	}

	// Check that model is also set in OPENCODE_CONFIG_CONTENT
	if openCodeRunner.env["OPENCODE_CONFIG_CONTENT"] != "{\"model\":\"zai-coding-plan/glm-4.7\"}" {
		t.Fatalf("expected OPENCODE_CONFIG_CONTENT with model, got %q", openCodeRunner.env["OPENCODE_CONFIG_CONTENT"])
	}
}

func TestOpenCodeRunWithEmptyModelInACPMode(t *testing.T) {
	tempDir := t.TempDir()
	repoRoot := filepath.Join(tempDir, "repo")
	if err := os.MkdirAll(repoRoot, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}

	openCodeRunner := &fakeOpenCodeRunner{}
	acpCalled := false
	acpClient := opencode.ACPClientFunc(func(ctx context.Context, issueID string, logPath string) error {
		acpCalled = true
		return nil
	})
	openCodeAdapter := openCodeAdapter{runner: openCodeRunner, acpClient: acpClient}

	configRoot := filepath.Join(tempDir, ".config", "opencode-runner")
	configDir := filepath.Join(configRoot, "opencode")
	logPath := filepath.Join(tempDir, "runner-logs", "opencode", "issue-1.jsonl")

	if err := openCodeAdapter.Run("issue-1", repoRoot, "prompt", "", configRoot, configDir, logPath); err != nil {
		t.Fatalf("open code run error: %v", err)
	}
	if !acpCalled {
		t.Fatalf("expected acp client to be called")
	}

	// Check that --model flag is NOT in args when model is empty
	for _, arg := range openCodeRunner.args {
		if arg == "--model" {
			t.Fatalf("did not expect --model in args when model is empty: %v", openCodeRunner.args)
		}
	}

	// Check that OPENCODE_CONFIG_CONTENT is empty object when model is empty
	if openCodeRunner.env["OPENCODE_CONFIG_CONTENT"] != "{}" {
		t.Fatalf("expected OPENCODE_CONFIG_CONTENT to be empty, got %q", openCodeRunner.env["OPENCODE_CONFIG_CONTENT"])
	}
}
