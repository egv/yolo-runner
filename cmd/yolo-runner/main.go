package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/egv/yolo-runner/internal/beads"
	"github.com/egv/yolo-runner/internal/contracts"
	"github.com/egv/yolo-runner/internal/exec"
	"github.com/egv/yolo-runner/internal/logging"
	"github.com/egv/yolo-runner/internal/opencode"
	"github.com/egv/yolo-runner/internal/prompt"
	"github.com/egv/yolo-runner/internal/runner"
	"github.com/egv/yolo-runner/internal/tk"
	"github.com/egv/yolo-runner/internal/ui/tui"
	gitadapter "github.com/egv/yolo-runner/internal/vcs/git"
	"gopkg.in/yaml.v3"

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

type runnerUIDispatcher struct {
	writer *runnerUIEventWriter
}

func (t tuiEmitter) Emit(event runner.Event) {
	if t.program == nil {
		return
	}
	go t.program.Send(event)
}

func (d runnerUIDispatcher) Emit(event runner.Event) {
	if d.writer == nil {
		return
	}
	d.writer.Emit(event)
}

type bubbleTUIProgram struct {
	program *tea.Program
}

type runnerUIEventWriter struct {
	mu     sync.Mutex
	writer io.WriteCloser
	buffer strings.Builder
}

type runnerConfig struct {
	Agent runnerConfigAgent `yaml:"agent"`
}

type runnerConfigAgent struct {
	Mode string `yaml:"mode"`
}

const (
	runnerModeUI      = "ui"
	runnerModeHeadless = "headless"
	runnerConfigPath   = ".yolo-runner/config.yaml"
)

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

var launchYoloTUI = func() (io.WriteCloser, func() error, error) {
	cmd := osexec.Command("yolo-tui", "--events-stdin")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, err
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, nil, err
	}
	return stdin, func() error {
		_ = stdin.Close()
		return cmd.Wait()
	}, nil
}

var isTerminal = func(writer io.Writer) bool {
	if file, ok := writer.(*os.File); ok {
		return term.IsTerminal(int(file.Fd()))
	}
	return false
}

var yoloRunnerVersion = "dev"

func isVersionRequest(args []string) bool {
	return len(args) == 1 && (args[0] == "--version" || args[0] == "-version")
}

var newTUIProgram = func(model tea.Model, stdout io.Writer, input io.Reader) tuiProgram {
	if input == nil {
		input = os.Stdin
	}
	program := tea.NewProgram(model, tea.WithInput(input), tea.WithOutput(stdout))
	return bubbleTUIProgram{program: program}
}

type adapterRunner struct {
	runner *exec.CommandRunner
}

func (a adapterRunner) Run(args ...string) (string, error) {
	if a.runner == nil {
		return runCommand(args...)
	}
	return a.runner.Run(args...)
}

func normalizeRunnerMode(raw string, field string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" {
		return "", nil
	}
	switch value {
	case runnerModeUI, runnerModeHeadless:
		return value, nil
	}
	return "", fmt.Errorf("%s in %s must be one of: %s, %s", field, runnerConfigPath, runnerModeUI, runnerModeHeadless)
}

func resolveRunnerMode(repoRoot string, modeFlag string, headlessFlag bool) (string, error) {
	mode, err := normalizeRunnerMode(modeFlag, "mode")
	if err != nil {
		return "", err
	}
	if mode != "" {
		return mode, nil
	}
	if headlessFlag {
		return runnerModeHeadless, nil
	}

	configMode, err := resolveRunnerModeFromConfig(repoRoot)
	if err != nil {
		return "", err
	}
	return configMode, nil
}

func resolveRunnerModeFromConfig(repoRoot string) (string, error) {
	configPath := filepath.Join(repoRoot, runnerConfigPath)
	content, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("cannot read config file at %s: %w", runnerConfigPath, err)
	}

	var config runnerConfig
	decoder := yaml.NewDecoder(strings.NewReader(string(content)))
	if err := decoder.Decode(&config); err != nil {
		return "", fmt.Errorf("cannot parse config file at %s: %w", runnerConfigPath, err)
	}
	return normalizeRunnerMode(config.Agent.Mode, "agent.mode")
}

func (w *runnerUIEventWriter) Emit(event runner.Event) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()

	message := strings.TrimSpace(event.RunnerEventMessage())
	if message == "" {
		message = strings.TrimSpace(event.RunnerEventThought())
	}
	if title := strings.TrimSpace(event.RunnerEventTitle()); title != "" {
		if message == "" {
			message = title
		} else {
			message = title + " " + message
		}
	}
	if eventType := event.RunnerEventType(); eventType != "" {
		if message == "" {
			message = eventType
		} else {
			message = eventType + ": " + message
		}
	}
	if phase := strings.TrimSpace(event.Phase); phase != "" {
		if message == "" {
			message = phase
		} else {
			message = message + " (" + phase + ")"
		}
	}
	w.emitLine(message)
}

func newRunnerUIEventWriter(w io.WriteCloser) *runnerUIEventWriter {
	return &runnerUIEventWriter{writer: w}
}

func (w *runnerUIEventWriter) Write(p []byte) (int, error) {
	if w == nil {
		return len(p), nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buffer.Write(p)
	buffered := normalizeLineBreaks(w.buffer.String())
	lines := strings.Split(buffered, "\n")
	for i := 0; i < len(lines)-1; i++ {
		w.emitLine(strings.TrimRight(lines[i], "\r"))
	}
	remaining := lines[len(lines)-1]
	w.buffer.Reset()
	if remaining != "" {
		w.buffer.WriteString(remaining)
	}
	return len(p), nil
}

func (w *runnerUIEventWriter) Flush() {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	remaining := normalizeLineBreaks(w.buffer.String())
	w.buffer.Reset()
	if remaining == "" {
		return
	}
	lines := strings.Split(remaining, "\n")
	for i, line := range lines {
		if i == len(lines)-1 && line == "" {
			continue
		}
		w.emitLine(strings.TrimRight(line, "\r"))
	}
}

func (w *runnerUIEventWriter) Close() error {
	if w == nil {
		return nil
	}
	w.Flush()
	if w.writer == nil {
		return nil
	}
	return w.writer.Close()
}

func (w *runnerUIEventWriter) emitLine(line string) {
	if w == nil || strings.TrimSpace(line) == "" {
		return
	}
	if w.writer == nil {
		return
	}
	event := contracts.Event{Type: contracts.EventTypeRunnerOutput, Message: line, Timestamp: time.Now().UTC()}
	payload, err := contracts.MarshalEventJSONL(event)
	if err != nil {
		return
	}
	_, _ = w.writer.Write([]byte(payload))
}

type adapterGitRunner struct {
	runner *gitadapter.GitCommandAdapter
}

func (a adapterGitRunner) Run(name string, args ...string) (string, error) {
	if a.runner == nil {
		return runCommand(append([]string{name}, args...)...)
	}
	return a.runner.Run(name, args...)
}

type openCodeAdapter struct {
	runner    openCodeRunner
	acpClient opencode.ACPClient
}

func (o openCodeAdapter) Run(issueID string, repoRoot string, promptText string, model string, configRoot string, configDir string, logPath string) error {
	return opencode.RunWithACP(context.Background(), issueID, repoRoot, promptText, model, configRoot, configDir, logPath, o.runner, o.acpClient)
}

func (o openCodeAdapter) RunWithContext(ctx context.Context, issueID string, repoRoot string, promptText string, model string, configRoot string, configDir string, logPath string) error {
	return opencode.RunWithACP(ctx, issueID, repoRoot, promptText, model, configRoot, configDir, logPath, o.runner, o.acpClient)
}

func RunOnceMain(args []string, runOnce runOnceFunc, exit exitFunc, stdout io.Writer, stderr io.Writer, beadsRunner beadsRunner, gitRunner gitRunner) int {
	if isVersionRequest(args) {
		if stdout == nil {
			stdout = io.Discard
		}
		fmt.Fprintf(stdout, "yolo-runner %s\n", yoloRunnerVersion)
		if exit != nil {
			exit(0)
		}
		return 0
	}

	if stderr != nil {
		fmt.Fprintln(stderr, compatibilityNotice())
	}
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
	mode := fs.String("mode", "", "Output mode for runner events (ui, headless)")
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

	resolvedMode, err := resolveRunnerMode(*repoRoot, *mode, *headless)
	if err != nil {
		fmt.Fprintln(stderr, err)
		if exit != nil {
			exit(1)
		}
		return 1
	}

	if err := opencode.ValidateAgent(*repoRoot); err != nil {
		fmt.Fprintln(stderr, err)
		if exit != nil {
			exit(1)
		}
		return 1
	}

	if beadsRunner == nil {
		logDir := filepath.Join(*repoRoot, "runner-logs")
		beadsRunner = adapterRunner{runner: exec.NewCommandRunner(logDir, stdout)}
	}
	if gitRunner == nil {
		logDir := filepath.Join(*repoRoot, "runner-logs")
		commandRunner := exec.NewCommandRunner(logDir, stdout)
		gitCommandAdapter := gitadapter.NewGitCommandAdapter(commandRunner)
		gitRunner = adapterGitRunner{runner: gitCommandAdapter}
	}

	// Detect which task tracker to use: tk first, then beads
	var taskTrackerAdapter runner.BeadsClient
	var trackerType string

	// Allow override via environment variable for testing
	if os.Getenv("YOLO_RUNNER_TASK_TRACKER") == "beads" {
		taskTrackerAdapter = beads.New(beadsRunner)
		trackerType = "beads"
	} else if tk.IsAvailable() {
		taskTrackerAdapter = tk.New(beadsRunner)
		trackerType = "tk"
	} else if beads.IsAvailable(*repoRoot) {
		taskTrackerAdapter = beads.New(beadsRunner)
		trackerType = "beads"
	} else {
		fmt.Fprintln(stderr, "Error: no task tracker found. Install tk (preferred) or initialize beads.")
		if exit != nil {
			exit(1)
		}
		return 1
	}

	gitAdapter := gitadapter.New(gitRunner)
	openCodeAdapter := openCodeAdapter{runner: defaultOpenCodeRunner{}}

	resolvedRootID := *rootID
	if resolvedRootID == "" {
		inferredRootID, err := inferDefaultRootID(*repoRoot, trackerType)
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
		Beads:    taskTrackerAdapter,
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
	}
	options.StatusPorcelain = func(context.Context) (string, error) {
		return gitAdapter.StatusPorcelain()
	}
	options.GitRestoreAll = func(context.Context) error {
		return gitAdapter.RestoreAll()
	}
	options.GitCleanAll = func(context.Context) error {
		return gitAdapter.CleanAll()
	}
	options.CleanupConfirm = func(summary string) (bool, error) {
		return cleanupConfirmPrompt(summary, os.Stdin, stdout)
	}

	if stdout == nil {
		options.Out = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}

	var program tuiProgram
	var tuiWriter *tuiLogWriter
	var uiWriter *runnerUIEventWriter
	var closeUIDispatcher func()
	previousCommandOutput := commandOutput
	if resolvedMode == runnerModeUI {
		stdin, closeFn, err := launchYoloTUI()
		if err != nil {
			fmt.Fprintln(stderr, err)
			if exit != nil {
				exit(1)
			}
			return 1
		}
		uiWriter = newRunnerUIEventWriter(stdin)
		deps.Events = runnerUIDispatcher{writer: uiWriter}
		options.Out = uiWriter
		commandOutput = uiWriter
		closeUIDispatcher = func() {
			_ = uiWriter.Close()
			_ = closeFn()
		}
	} else if resolvedMode == "" && isTerminal(stdout) {
		stopCh := make(chan struct{})
		options.Stop = stopCh
		program = newTUIProgram(tui.NewModelWithStop(nil, stopCh), stdout, os.Stdin)
		deps.Events = tuiEmitter{program: program}
		tuiWriter = newTUILogWriter(program)
		options.Out = tuiWriter
		commandOutput = tuiWriter
		go func() {
			if err := program.Start(); err != nil {
				fmt.Fprintln(stderr, err)
				if exit != nil {
					exit(1)
				}
			}
		}()
	}
	defer func() {
		commandOutput = previousCommandOutput
		if tuiWriter != nil {
			tuiWriter.Flush()
		}
		if closeUIDispatcher != nil {
			closeUIDispatcher()
		}
	}()
	if isTerminal(stdout) {
		defer fmt.Fprint(stdout, "\x1b[?25h")
	}

	// Loop until there are no tasks left. Blocked tasks are skipped.
	_, err = runner.RunLoop(options, deps, 0, runOnce)
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

func compatibilityNotice() string {
	return "yolo-runner compatibility mode: prefer yolo-agent for orchestration, yolo-task for tracker actions, and yolo-tui for read-only monitoring"
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

func inferDefaultRootID(repoRoot string, trackerType string) (string, error) {
	if trackerType == "tk" {
		return inferRootIDFromTK(repoRoot)
	}
	return inferRootIDFromBeads(repoRoot)
}

func inferRootIDFromBeads(repoRoot string) (string, error) {
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
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var item roadmapCandidate
		if err := json.Unmarshal(line, &item); err != nil {
			fmt.Fprintln(os.Stderr, "Error parsing line in issues.jsonl:", err)
			fmt.Fprintf(os.Stderr, "Line content (first 100 bytes): %q\n", string(line[:min(100, len(line))]))
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

func inferRootIDFromTK(repoRoot string) (string, error) {
	// For TK, we look for a ticket with "Roadmap" in the title
	// This is a simplified approach - in practice, you might want to use tags
	cmd := osexec.Command("tk", "list", "--status=open")
	cmd.Dir = repoRoot
	output, err := cmd.Output()
	if err != nil {
		return "", errors.New("missing --root and unable to list tk tickets; pass --root explicitly")
	}

	lines := strings.Split(string(output), "\n")
	var candidates []string
	for _, line := range lines {
		if strings.Contains(line, "Roadmap") || strings.Contains(line, "roadmap") {
			fields := strings.Fields(line)
			if len(fields) > 0 {
				candidates = append(candidates, fields[0])
			}
		}
	}

	if len(candidates) == 1 {
		return candidates[0], nil
	}
	return "", errors.New("missing --root and no unique Roadmap ticket found in tk; pass --root explicitly")
}
