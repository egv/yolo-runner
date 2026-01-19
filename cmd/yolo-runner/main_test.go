package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"yolo-runner/internal/runner"
)

type fakeRunner struct {
	calls []string
}

func (f *fakeRunner) Run(args ...string) (string, error) {
	f.calls = append(f.calls, strings.Join(args, " "))
	return "", nil
}

type fakeOpenCodeRunner struct {
	called bool
}

func (f *fakeOpenCodeRunner) Run(args []string, env map[string]string, stdoutPath string) error {
	f.called = true
	return nil
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
	openCodeRunner := &fakeOpenCodeRunner{}

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
	_ = openCodeRunner
	_ = beadsRunner
	_ = gitRunner
	if exit.code != 0 {
		t.Fatalf("expected exit code 0, got %d", exit.code)
	}
}
