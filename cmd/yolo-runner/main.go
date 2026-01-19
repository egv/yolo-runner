package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"yolo-runner/internal/beads"
	"yolo-runner/internal/logging"
	"yolo-runner/internal/opencode"
	"yolo-runner/internal/prompt"
	"yolo-runner/internal/runner"
	gitadapter "yolo-runner/internal/vcs/git"
)

type runOnceFunc func(opts runner.RunOnceOptions, deps runner.RunOnceDeps) (string, error)

type exitFunc func(code int)

type beadsRunner interface {
	Run(args ...string) (string, error)
}

type gitRunner interface {
	Run(name string, args ...string) (string, error)
}

type openCodeRunner interface {
	Run(args []string, env map[string]string, stdoutPath string) error
}

type adapterRunner struct{}

func (adapterRunner) Run(args ...string) (string, error) {
	return runCommand(args...)
}

type adapterGitRunner struct{}

func (adapterGitRunner) Run(name string, args ...string) (string, error) {
	return runCommand(append([]string{name}, args...)...)
}

type openCodeAdapter struct {
	runner openCodeRunner
}

func (o openCodeAdapter) Run(issueID string, repoRoot string, promptText string, model string, configRoot string, configDir string, logPath string) error {
	return opencode.Run(issueID, repoRoot, promptText, model, configRoot, configDir, logPath, o.runner.Run)
}

func RunOnceMain(args []string, runOnce runOnceFunc, exit exitFunc, stdout io.Writer, stderr io.Writer, beadsRunner beadsRunner, gitRunner gitRunner) int {
	fs := flag.NewFlagSet("yolo-runner", flag.ContinueOnError)
	fs.SetOutput(stderr)

	repoRoot := fs.String("repo", ".", "Repository root path")
	rootID := fs.String("root", "algi-8bt", "Root bead/epic ID")
	model := fs.String("model", "", "OpenCode model")
	dryRun := fs.Bool("dry-run", false, "Print task and prompt without executing")

	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(stderr, err)
		if exit != nil {
			exit(1)
		}
		return 1
	}

	if runOnce == nil {
		runOnce = runner.RunOnce
	}

	if beadsRunner == nil {
		beadsRunner = adapterRunner{}
	}
	if gitRunner == nil {
		gitRunner = adapterGitRunner{}
	}

	beadsAdapter := beads.New(beadsRunner)
	gitAdapter := gitadapter.New(gitRunner)
	openCodeAdapter := openCodeAdapter{runner: defaultOpenCodeRunner{}}

	deps := runner.RunOnceDeps{
		Beads:    beadsAdapter,
		Prompt:   promptBuilder{},
		OpenCode: openCodeAdapter,
		Git:      gitAdapter,
		Logger:   runnerLogger{},
	}

	options := runner.RunOnceOptions{
		RepoRoot: *repoRoot,
		RootID:   *rootID,
		Model:    *model,
		DryRun:   *dryRun,
		Out:      stdout,
	}

	if stdout == nil {
		options.Out = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}

	_, err := runOnce(options, deps)
	if err != nil {
		fmt.Fprintln(stderr, err)
		if exit != nil {
			exit(1)
		}
		return 1
	}

	if exit != nil {
		exit(0)
	}
	return 0
}

func main() {
	RunOnceMain(os.Args[1:], runner.RunOnce, os.Exit, os.Stdout, os.Stderr, nil, nil)
}

type promptBuilder struct{}

func (promptBuilder) Build(issueID string, title string, description string, acceptance string) string {
	return prompt.Build(issueID, title, description, acceptance)
}

type runnerLogger struct{}

func (runnerLogger) AppendRunnerSummary(repoRoot string, issueID string, title string, status string, commitSHA string) error {
	return logging.AppendRunnerSummary(repoRoot, issueID, title, status, commitSHA)
}

type defaultOpenCodeRunner struct{}

func (defaultOpenCodeRunner) Run(args []string, env map[string]string, stdoutPath string) error {
	return runCommandWithEnv(args, env, stdoutPath)
}
