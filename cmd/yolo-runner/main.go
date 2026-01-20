package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"yolo-runner/internal/beads"
	"yolo-runner/internal/logging"
	"yolo-runner/internal/opencode"
	"yolo-runner/internal/prompt"
	"yolo-runner/internal/runner"
	"yolo-runner/internal/ui/tui"
	gitadapter "yolo-runner/internal/vcs/git"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"
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
	Start(args []string, env map[string]string, stdoutPath string) (opencode.Process, error)
}

type tuiProgram interface {
	Start() error
	Send(event runner.Event)
	SendInput(msg tea.Msg)
	Quit()
}

type tuiEmitter struct {
	program tuiProgram
}

func (t tuiEmitter) Emit(event runner.Event) {
	if t.program == nil {
		return
	}
	go t.program.Send(event)
}

type bubbleTUIProgram struct {
	program *tea.Program
}

func (b bubbleTUIProgram) Start() error {
	if b.program == nil {
		return nil
	}
	return b.program.Start()
}

func (b bubbleTUIProgram) Send(event runner.Event) {
	if b.program == nil {
		return
	}
	b.program.Send(event)
}

func (b bubbleTUIProgram) SendInput(msg tea.Msg) {
	if b.program == nil {
		return
	}
	b.program.Send(msg)
}

func (b bubbleTUIProgram) Quit() {
	if b.program == nil {
		return
	}
	b.program.Quit()
}

var isTerminal = func(writer io.Writer) bool {
	if file, ok := writer.(*os.File); ok {
		return term.IsTerminal(int(file.Fd()))
	}
	return false
}

var newTUIProgram = func(model tea.Model, stdout io.Writer, input io.Reader) tuiProgram {
	if input == nil {
		input = os.Stdin
	}
	program := tea.NewProgram(model, tea.WithInput(input), tea.WithOutput(stdout))
	return bubbleTUIProgram{program: program}
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
	return opencode.Run(issueID, repoRoot, promptText, model, configRoot, configDir, logPath, o.runner)
}

func RunOnceMain(args []string, runOnce runOnceFunc, exit exitFunc, stdout io.Writer, stderr io.Writer, beadsRunner beadsRunner, gitRunner gitRunner) int {
	if len(args) > 0 && args[0] == "init" {
		return InitMain(args[1:], exit, stderr)
	}

	fs := flag.NewFlagSet("yolo-runner", flag.ContinueOnError)
	fs.SetOutput(stderr)

	repoRoot := fs.String("repo", ".", "Repository root path")
	rootID := fs.String("root", "", "Root bead/epic ID")
	model := fs.String("model", "", "OpenCode model")
	dryRun := fs.Bool("dry-run", false, "Print task and prompt without executing")
	headless := fs.Bool("headless", false, "Force plain output without TUI")
	configRoot := fs.String("config-root", "", "OpenCode config root")
	configDir := fs.String("config-dir", "", "OpenCode config dir")

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

	if err := opencode.ValidateAgent(*repoRoot); err != nil {
		fmt.Fprintln(stderr, err)
		if exit != nil {
			exit(1)
		}
		return 1
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

	resolvedRootID := *rootID
	if resolvedRootID == "" {
		inferredRootID, err := inferDefaultRootID(*repoRoot)
		if err != nil {
			fmt.Fprintln(stderr, err)
			if exit != nil {
				exit(1)
			}
			return 1
		}
		resolvedRootID = inferredRootID
	}

	deps := runner.RunOnceDeps{
		Beads:    beadsAdapter,
		Prompt:   promptBuilder{},
		OpenCode: openCodeAdapter,
		Git:      gitAdapter,
		Logger:   runnerLogger{},
	}

	resolvedConfigRoot := *configRoot
	resolvedConfigDir := *configDir
	if resolvedConfigRoot == "" {
		homeDir := os.Getenv("HOME")
		if homeDir != "" {
			resolvedConfigRoot = filepath.Join(homeDir, ".config", "opencode-runner")
		}
	}
	if resolvedConfigDir == "" && resolvedConfigRoot != "" {
		resolvedConfigDir = filepath.Join(resolvedConfigRoot, "opencode")
	}

	options := runner.RunOnceOptions{
		RepoRoot:   *repoRoot,
		RootID:     resolvedRootID,
		Model:      *model,
		ConfigRoot: resolvedConfigRoot,
		ConfigDir:  resolvedConfigDir,
		DryRun:     *dryRun,
		Out:        stdout,
		Stop:       runner.NewStopState(),
	}

	if stdout == nil {
		options.Out = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}

	var program tuiProgram
	if !*headless && isTerminal(stdout) {
		stopCh := make(chan struct{})
		options.Stop.Watch(stopCh)
		program = newTUIProgram(tui.NewModelWithStop(nil, stopCh), stdout, os.Stdin)
		deps.Events = tuiEmitter{program: program}
		go func() {
			if err := program.Start(); err != nil {
				fmt.Fprintln(stderr, err)
				if exit != nil {
					exit(1)
				}
			}
		}()
	}
	if isTerminal(stdout) {
		defer fmt.Fprint(stdout, "\x1b[?25h")
	}

	// Loop until there are no tasks left. Blocked tasks are skipped.
	_, err := runner.RunLoop(options, deps, 0, runOnce)
	if program != nil {
		program.Quit()
	}
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

func InitMain(args []string, exit exitFunc, stderr io.Writer) int {
	fs := flag.NewFlagSet("yolo-runner-init", flag.ContinueOnError)
	fs.SetOutput(stderr)

	repoRoot := fs.String("repo", ".", "Repository root path")

	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(stderr, err)
		if exit != nil {
			exit(1)
		}
		return 1
	}

	if err := opencode.InitAgent(*repoRoot); err != nil {
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

type promptBuilder struct{}

func (promptBuilder) Build(issueID string, title string, description string, acceptance string) string {
	return prompt.Build(issueID, title, description, acceptance)
}

type runnerLogger struct{}

func (runnerLogger) AppendRunnerSummary(repoRoot string, issueID string, title string, status string, commitSHA string) error {
	return logging.AppendRunnerSummary(repoRoot, issueID, title, status, commitSHA)
}

type defaultOpenCodeRunner struct{}

func (defaultOpenCodeRunner) Start(args []string, env map[string]string, stdoutPath string) (opencode.Process, error) {
	return startCommandWithEnv(args, env, stdoutPath)
}

type roadmapCandidate struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Type   string `json:"issue_type"`
	Status string `json:"status"`
}

func inferDefaultRootID(repoRoot string) (string, error) {
	issuesPath := filepath.Join(repoRoot, ".beads", "issues.jsonl")
	file, err := os.Open(issuesPath)
	if err != nil {
		return "", errors.New("missing --root and no readable .beads/issues.jsonl; pass --root explicitly")
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	count := 0
	var match roadmapCandidate
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var item roadmapCandidate
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			continue
		}
		if item.Title == "Roadmap" && item.Type == "epic" && (item.Status == "open" || item.Status == "in_progress") {
			count++
			match = item
		}
	}
	if err := scanner.Err(); err != nil {
		return "", errors.New("missing --root and unable to read .beads/issues.jsonl; pass --root explicitly")
	}
	if count == 1 && match.ID != "" {
		return match.ID, nil
	}
	return "", errors.New("missing --root and no unique Roadmap epic found; pass --root explicitly")
}
