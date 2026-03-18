package codingagents

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

// CommandSpec matches the command invocation contract used by built-in adapters.
type CommandSpec struct {
	Binary string
	Args   []string
	Env    []string
	Dir    string
	Stdout io.Writer
	Stderr io.Writer
}

type CommandRunner interface {
	Run(ctx context.Context, spec CommandSpec) error
}

type commandRunnerFunc func(ctx context.Context, spec CommandSpec) error

func (fn commandRunnerFunc) Run(ctx context.Context, spec CommandSpec) error {
	return fn(ctx, spec)
}

type CommandStarter interface {
	Start(context.Context, CommandSpec) (SupervisedProcess, error)
}

type commandStarterFunc func(context.Context, CommandSpec) (SupervisedProcess, error)

func (fn commandStarterFunc) Start(ctx context.Context, spec CommandSpec) (SupervisedProcess, error) {
	return fn(ctx, spec)
}

// GenericCLIRunnerAdapter executes command-based coding agents.
type GenericCLIRunnerAdapter struct {
	backend          string
	binary           string
	args             []string
	runner           CommandRunner
	starter          CommandStarter
	health           *BackendHealthConfig
	httpClient       *http.Client
	runHealthCommand func(context.Context, string, ...string) ([]byte, error)
	waitReady        func(context.Context, SupervisedProcess) error
	gracePeriod      time.Duration
	now              func() time.Time
}

var structuredReviewVerdictLinePattern = regexp.MustCompile(`(?i)^\s*REVIEW_VERDICT\s*:\s*(pass|fail)(?:\s*DONE)?\s*$`)

func NewGenericCLIRunnerAdapter(backend string, binary string, args []string, runner CommandRunner) *GenericCLIRunnerAdapter {
	if strings.TrimSpace(backend) == "" {
		backend = "coding-agent"
	}
	adapter := &GenericCLIRunnerAdapter{
		backend:          strings.ToLower(strings.TrimSpace(backend)),
		binary:           strings.TrimSpace(binary),
		args:             append([]string(nil), normalizeStringSlice(args)...),
		httpClient:       &http.Client{},
		runHealthCommand: runAgentHealthCommand,
		now:              time.Now,
	}
	if runner != nil {
		adapter.runner = runner
	} else {
		adapter.starter = commandStarterFunc(startManagedCommand)
	}
	return adapter
}

func (a *GenericCLIRunnerAdapter) WithStarter(starter CommandStarter) *GenericCLIRunnerAdapter {
	if a == nil {
		return nil
	}
	a.starter = starter
	return a
}

func (a *GenericCLIRunnerAdapter) WithHealthConfig(cfg *BackendHealthConfig) *GenericCLIRunnerAdapter {
	if a == nil {
		return nil
	}
	if cfg == nil {
		a.health = nil
		return a
	}
	copyCfg := *cfg
	a.health = &copyCfg
	return a
}

func (a *GenericCLIRunnerAdapter) Run(ctx context.Context, request contracts.RunnerRequest) (contracts.RunnerResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if a == nil {
		return contracts.RunnerResult{}, errors.New("nil command runner adapter")
	}
	if strings.TrimSpace(a.binary) == "" {
		return contracts.RunnerResult{}, errors.New("binary is required")
	}
	if a.runner == nil && a.starter == nil {
		a.starter = commandStarterFunc(startManagedCommand)
	}
	if a.now == nil {
		a.now = time.Now
	}
	if a.httpClient == nil {
		a.httpClient = &http.Client{}
	}
	if a.runHealthCommand == nil {
		a.runHealthCommand = runAgentHealthCommand
	}
	request = requestWithBackend(request, a.backend)

	startedAt := a.now().UTC()
	logPath := resolveLogPath(request, a.backend)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return contracts.RunnerResult{}, err
	}

	stdoutFile, err := os.Create(logPath)
	if err != nil {
		return contracts.RunnerResult{}, err
	}
	defer stdoutFile.Close()

	stderrPath := contracts.BackendLogSidecarPath(logPath, contracts.BackendLogStderr)
	stderrFile, err := os.Create(stderrPath)
	if err != nil {
		return contracts.RunnerResult{}, err
	}
	defer stderrFile.Close()

	emitProgress := func(source string, line string) {
		if request.OnProgress == nil {
			return
		}
		progress, ok := contracts.NewRunnerOutputProgress(source, line, a.now().UTC())
		if !ok {
			return
		}
		request.OnProgress(progress)
	}

	stdoutWriter := newLineWriter(stdoutFile, func(line string) {
		emitProgress("stdout", line)
	})
	stderrWriter := newLineWriter(stderrFile, func(line string) {
		emitProgress("stderr", line)
	})

	commandArgs := resolveCommandArgs(a.args, request)
	runCtx, cancel := contracts.WithOptionalTimeout(ctx, request.Timeout)
	defer cancel()

	spec := CommandSpec{
		Binary: a.binary,
		Args:   commandArgs,
		Dir:    request.RepoRoot,
		Stdout: stdoutWriter,
		Stderr: stderrWriter,
	}

	var runErr error
	if a.starter != nil {
		runErr = a.runManaged(runCtx, spec)
	} else {
		runErr = a.runner.Run(runCtx, spec)
	}
	stdoutWriter.Flush()
	stderrWriter.Flush()

	runErr = contracts.FinalizeRunError(runCtx, runErr)

	finishedAt := a.now().UTC()
	result := contracts.NormalizeBackendRunnerResult(startedAt, finishedAt, request, runErr, nil)
	result.LogPath = logPath
	extras := map[string]string{}
	if request.Mode == contracts.RunnerModeReview {
		if verdict, ok := structuredReviewVerdict(logPath); ok {
			extras["review_verdict"] = verdict
			result.ReviewReady = strings.EqualFold(verdict, "pass")
		}
	}
	result.Artifacts = contracts.BuildRunnerArtifacts(a.backend, request, result, extras)
	return result, nil
}

func (a *GenericCLIRunnerAdapter) runManaged(ctx context.Context, spec CommandSpec) error {
	supervisor := ProcessSupervisor{
		Start: func(ctx context.Context) (SupervisedProcess, error) {
			starter := a.starter
			if starter == nil {
				starter = commandStarterFunc(startManagedCommand)
			}
			return starter.Start(ctx, spec)
		},
		WaitReady: func(ctx context.Context, proc SupervisedProcess) error {
			return a.waitUntilReady(ctx, proc)
		},
		GracePeriod: a.gracePeriod,
	}
	return supervisor.Run(ctx, func(ctx context.Context, proc SupervisedProcess) error {
		if waitable, ok := proc.(interface{ WaitChan() <-chan error }); ok {
			waitCh := waitable.WaitChan()
			select {
			case err := <-waitCh:
				return err
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return proc.Wait()
	})
}

func (a *GenericCLIRunnerAdapter) waitUntilReady(ctx context.Context, proc SupervisedProcess) error {
	_ = proc
	if a.waitReady != nil {
		return a.waitReady(ctx, proc)
	}
	if a.health == nil || !a.health.Enabled {
		return nil
	}
	target := strings.TrimSpace(a.health.Endpoint)
	command := strings.TrimSpace(a.health.Command)
	if target == "" && command == "" {
		return nil
	}
	timeout, err := parseAgentHealthTimeout(a.health.Timeout)
	if err != nil {
		return err
	}
	interval, err := parseAgentHealthInterval(a.health.Interval)
	if err != nil {
		return err
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		attemptCtx, cancel := context.WithTimeout(ctx, timeout)
		err := a.healthCheck(attemptCtx)
		cancel()
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (a *GenericCLIRunnerAdapter) healthCheck(ctx context.Context) error {
	if a.health == nil {
		return nil
	}
	if endpoint := strings.TrimSpace(a.health.Endpoint); endpoint != "" {
		return contracts.CheckHTTPReadiness(ctx, a.httpClient, contracts.HTTPReadinessCheck{
			Endpoint: endpoint,
			Method:   a.health.Method,
			Headers:  a.health.Headers,
		})
	}
	if command := strings.TrimSpace(a.health.Command); command != "" {
		return contracts.CheckStdioReadiness(ctx, contracts.StdioReadinessCheck{
			Command: command,
			Run:     a.runHealthCommand,
		})
	}
	return nil
}

func structuredReviewVerdict(logPath string) (string, bool) {
	if strings.TrimSpace(logPath) == "" {
		return "", false
	}
	content, err := os.ReadFile(logPath)
	if err != nil {
		return "", false
	}
	normalized := strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(string(content))
	if normalized == "" {
		return "", false
	}
	lastVerdict := ""
	found := false
	for _, line := range strings.Split(normalized, "\n") {
		matches := structuredReviewVerdictLinePattern.FindStringSubmatch(line)
		if len(matches) < 2 {
			continue
		}
		lastVerdict = strings.ToLower(matches[1])
		found = true
	}
	if !found {
		return "", false
	}
	return lastVerdict, true
}

func resolveLogPath(request contracts.RunnerRequest, backend string) string {
	if request.Metadata != nil {
		if path := strings.TrimSpace(request.Metadata["log_path"]); path != "" {
			return path
		}
	}
	backend = normalizeBackend(backend)
	if backend == "" {
		backend = "coding-agent"
	}
	if strings.TrimSpace(request.RepoRoot) != "" && strings.TrimSpace(request.TaskID) != "" {
		return filepath.Join(request.RepoRoot, "runner-logs", backend, request.TaskID+".jsonl")
	}
	if strings.TrimSpace(request.TaskID) != "" {
		return filepath.Join("runner-logs", backend, request.TaskID+".jsonl")
	}
	return filepath.Join("runner-logs", backend, backend+"-run.jsonl")
}

func requestWithBackend(request contracts.RunnerRequest, backend string) contracts.RunnerRequest {
	if strings.TrimSpace(backend) == "" {
		return request
	}
	metadata := map[string]string{}
	for key, value := range request.Metadata {
		metadata[key] = value
	}
	if strings.TrimSpace(metadata["backend"]) == "" {
		metadata["backend"] = backend
	}
	request.Metadata = metadata
	return request
}

func runCommand(ctx context.Context, spec CommandSpec) error {
	resolvedBinary, err := resolveCommandBinary(spec.Binary)
	if err != nil {
		return err
	}
	if strings.TrimSpace(resolvedBinary) == "" {
		return errors.New("binary is required")
	}
	cmd := exec.CommandContext(ctx, resolvedBinary, spec.Args...)
	if strings.TrimSpace(spec.Dir) != "" {
		cmd.Dir = spec.Dir
	}
	if len(spec.Env) > 0 {
		cmd.Env = append(os.Environ(), spec.Env...)
	}
	cmd.Stdout = spec.Stdout
	cmd.Stderr = spec.Stderr
	err = cmd.Run()
	if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	if err != nil && errors.Is(ctx.Err(), context.Canceled) {
		return context.Canceled
	}
	return err
}

type managedCommandProcess struct {
	cmd         *exec.Cmd
	stopFn      func(*exec.Cmd) error
	killFn      func(*exec.Cmd) error
	cleanupFn   func()
	waitOnce    sync.Once
	waitErr     error
	waitDone    chan struct{}
	cleanupOnce sync.Once
}

func (p *managedCommandProcess) Wait() error {
	p.waitOnce.Do(func() {
		p.waitErr = p.cmd.Wait()
		p.cleanup()
		close(p.waitDone)
	})
	<-p.waitDone
	return p.waitErr
}

func (p *managedCommandProcess) WaitChan() <-chan error {
	done := make(chan error, 1)
	go func() {
		done <- p.Wait()
	}()
	return done
}

func (p *managedCommandProcess) Stop() error {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return nil
	}
	if p.cmd.ProcessState != nil && p.cmd.ProcessState.Exited() {
		return nil
	}
	if p.stopFn != nil {
		return p.stopFn(p.cmd)
	}
	return stopManagedCommand(p.cmd)
}

func (p *managedCommandProcess) Kill() error {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return nil
	}
	if p.cmd.ProcessState != nil && p.cmd.ProcessState.Exited() {
		return nil
	}
	if p.killFn != nil {
		return p.killFn(p.cmd)
	}
	return killManagedCommand(p.cmd)
}

func (p *managedCommandProcess) cleanup() {
	if p == nil {
		return
	}
	p.cleanupOnce.Do(func() {
		if p.cleanupFn != nil {
			p.cleanupFn()
		}
	})
}

func startManagedCommand(ctx context.Context, spec CommandSpec) (SupervisedProcess, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	resolvedBinary, err := resolveCommandBinary(spec.Binary)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(resolvedBinary) == "" {
		return nil, errors.New("binary is required")
	}
	cmd := exec.Command(resolvedBinary, spec.Args...)
	configureManagedCommand(cmd)
	if strings.TrimSpace(spec.Dir) != "" {
		cmd.Dir = spec.Dir
	}
	if len(spec.Env) > 0 {
		cmd.Env = append(os.Environ(), spec.Env...)
	}
	cmd.Stdout = spec.Stdout
	cmd.Stderr = spec.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	proc := &managedCommandProcess{
		cmd:      cmd,
		stopFn:   stopManagedCommand,
		killFn:   killManagedCommand,
		waitDone: make(chan struct{}),
	}
	if err := attachManagedCommand(proc); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		proc.cleanup()
		return nil, err
	}
	return proc, nil
}

func resolveCommandBinary(raw string) (string, error) {
	binary := strings.TrimSpace(raw)
	if binary == "" {
		return "", errors.New("binary is required")
	}
	if filepath.IsAbs(binary) {
		if _, err := os.Stat(binary); err == nil {
			return binary, nil
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("stat binary %q: %w", binary, err)
		}
		fallback, lookErr := exec.LookPath(filepath.Base(binary))
		if lookErr == nil {
			return fallback, nil
		}
		return "", fmt.Errorf("binary %q not found", binary)
	}
	return binary, nil
}

func resolveCommandArgs(raw []string, request contracts.RunnerRequest) []string {
	out := make([]string, 0, len(raw))
	backend := strings.ToLower(strings.TrimSpace(request.Metadata["backend"]))
	template := map[string]string{
		"{{backend}}":      backend,
		"{{backend-name}}": backend,
		"{{model}}":        strings.TrimSpace(request.Model),
		"{{prompt}}":       strings.TrimSpace(request.Prompt),
		"{{task_id}}":      strings.TrimSpace(request.TaskID),
		"{{repo_root}}":    strings.TrimSpace(request.RepoRoot),
		"{{mode}}":         strings.TrimSpace(string(request.Mode)),
	}
	for _, value := range raw {
		text := strings.TrimSpace(value)
		for placeholder, replacement := range template {
			text = strings.ReplaceAll(text, placeholder, replacement)
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		out = append(out, text)
	}
	return out
}

// newLineWriter replicates existing line-oriented stdout/stderr writers used by built-in adapters.
type lineWriter struct {
	buffer string
	write  func(string)
	target io.Writer
}

func newLineWriter(target io.Writer, write func(string)) *lineWriter {
	return &lineWriter{target: target, write: write}
}

func (w *lineWriter) Write(p []byte) (int, error) {
	for _, b := range p {
		if b == '\n' {
			line := strings.TrimSuffix(w.buffer, "\n")
			if _, err := w.target.Write([]byte(line + "\n")); err != nil {
				return 0, err
			}
			w.write(line)
			w.buffer = ""
			continue
		}
		w.buffer += string(b)
	}
	return len(p), nil
}

func (w *lineWriter) Flush() {
	if strings.TrimSpace(w.buffer) == "" {
		return
	}
	line := strings.TrimSuffix(w.buffer, "\n")
	_, _ = w.target.Write([]byte(line + "\n"))
	w.write(line)
	w.buffer = ""
}
