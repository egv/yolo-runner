package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"yolo-runner/internal/opencode"
	"yolo-runner/internal/runner"
)

type fakeRunner struct {
	calls []string
}

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
	env map[string]string
}

type fakeOpenCodeProcess struct{}

func (fakeOpenCodeProcess) Wait() error { return nil }

func (fakeOpenCodeProcess) Kill() error { return nil }

func (f *fakeOpenCodeRunner) Start(args []string, env map[string]string, stdoutPath string) (opencode.Process, error) {
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
	runner := &fakeRunOnce{err: errors.New("boom")}
	exit := &fakeExit{}
	out := &bytes.Buffer{}

	code := RunOnceMain([]string{"--repo", "/repo", "--root", "root"}, runner.Run, exit.Exit, out, out, nil, nil)

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
	runner := &fakeRunOnce{result: "no_tasks"}
	exit := &fakeExit{}
	out := &bytes.Buffer{}
	beadsRunner := &fakeRunner{}
	gitRunner := &fakeGitRunner{}

	RunOnceMain([]string{"--repo", "/repo", "--root", "root", "--model", "model", "--dry-run"}, runner.Run, exit.Exit, out, out, beadsRunner, gitRunner)

	if !runner.called {
		t.Fatalf("expected run once to be called")
	}
	if runner.opts.RepoRoot != "/repo" || runner.opts.RootID != "root" || runner.opts.Model != "model" || !runner.opts.DryRun {
		t.Fatalf("unexpected options: %#v", runner.opts)
	}
	if runner.opts.Out == nil {
		t.Fatalf("expected output writer")
	}
	if runner.deps.Beads == nil || runner.deps.Git == nil || runner.deps.Prompt == nil || runner.deps.OpenCode == nil || runner.deps.Logger == nil {
		t.Fatalf("expected deps to be wired")
	}
	if exit.code != 0 {
		t.Fatalf("expected exit code 0, got %d", exit.code)
	}
}

func TestRunOnceMainDefaultsConfigPaths(t *testing.T) {
	runner := &fakeRunOnce{result: "no_tasks"}
	exit := &fakeExit{}
	out := &bytes.Buffer{}
	beadsRunner := &fakeRunner{}
	gitRunner := &fakeGitRunner{}

	t.Setenv("HOME", "/home/user")

	RunOnceMain([]string{"--repo", "/repo", "--root", "root"}, runner.Run, exit.Exit, out, out, beadsRunner, gitRunner)

	if !runner.called {
		t.Fatalf("expected run once to be called")
	}
	if runner.opts.ConfigRoot == "" {
		t.Fatalf("expected config root to be set")
	}
	if runner.opts.ConfigDir == "" {
		t.Fatalf("expected config dir to be set")
	}
	expectedConfigRoot := filepath.Join("/home/user", ".config", "opencode-runner")
	if runner.opts.ConfigRoot != expectedConfigRoot {
		t.Fatalf("expected config root %q, got %q", expectedConfigRoot, runner.opts.ConfigRoot)
	}
	expectedConfigDir := filepath.Join(expectedConfigRoot, "opencode")
	if runner.opts.ConfigDir != expectedConfigDir {
		t.Fatalf("expected config dir %q, got %q", expectedConfigDir, runner.opts.ConfigDir)
	}
	if exit.code != 0 {
		t.Fatalf("expected exit code 0, got %d", exit.code)
	}
}

type fakeTUIProgram struct {
	started chan struct{}
	quit    chan struct{}
	events  chan runner.Event
}

func newFakeTUIProgram() *fakeTUIProgram {
	return &fakeTUIProgram{
		started: make(chan struct{}),
		quit:    make(chan struct{}),
		events:  make(chan runner.Event, 1),
	}
}

func (f *fakeTUIProgram) Start() error {
	close(f.started)
	return nil
}

func (f *fakeTUIProgram) Send(event runner.Event) {
	f.events <- event
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

func TestRunOnceMainUsesTUIOnTTYByDefault(t *testing.T) {
	fakeProgram := newFakeTUIProgram()
	prevIsTerminal := isTerminal
	prevNewTUIProgram := newTUIProgram
	isTerminal = func(io.Writer) bool { return true }
	newTUIProgram = func(stdout io.Writer) tuiProgram { return fakeProgram }
	t.Cleanup(func() {
		isTerminal = prevIsTerminal
		newTUIProgram = prevNewTUIProgram
	})

	runOnce := &fakeRunOnce{result: "no_tasks"}
	exit := &fakeExit{}
	out := &bytes.Buffer{}
	beadsRunner := &fakeRunner{}
	gitRunner := &fakeGitRunner{}

	code := RunOnceMain([]string{"--repo", "/repo", "--root", "root"}, runOnce.Run, exit.Exit, out, out, beadsRunner, gitRunner)

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

func TestRunOnceMainHeadlessDisablesTUI(t *testing.T) {
	called := false
	prevIsTerminal := isTerminal
	prevNewTUIProgram := newTUIProgram
	isTerminal = func(io.Writer) bool { return true }
	newTUIProgram = func(stdout io.Writer) tuiProgram {
		called = true
		return newFakeTUIProgram()
	}
	t.Cleanup(func() {
		isTerminal = prevIsTerminal
		newTUIProgram = prevNewTUIProgram
	})

	runOnce := &fakeRunOnce{result: "no_tasks"}
	exit := &fakeExit{}
	out := &bytes.Buffer{}
	beadsRunner := &fakeRunner{}
	gitRunner := &fakeGitRunner{}
	stderr := &bytes.Buffer{}

	RunOnceMain([]string{"--repo", "/repo", "--root", "root", "--headless"}, runOnce.Run, exit.Exit, out, stderr, beadsRunner, gitRunner)

	if called {
		t.Fatalf("expected TUI program not to start")
	}
	if runOnce.deps.Events != nil {
		t.Fatalf("expected no events emitter in headless mode")
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
	openCodeAdapter := openCodeAdapter{runner: openCodeRunner}

	configRoot := filepath.Join(tempDir, ".config", "opencode-runner")
	configDir := filepath.Join(configRoot, "opencode")
	logPath := filepath.Join(tempDir, "runner-logs", "opencode", "issue-1.jsonl")

	if err := openCodeAdapter.Run("issue-1", repoRoot, "prompt", "", configRoot, configDir, logPath); err != nil {
		t.Fatalf("open code run error: %v", err)
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
