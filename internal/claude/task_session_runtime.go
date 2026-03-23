package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

// StdinProcessSpec describes how to launch the claude subprocess.
type StdinProcessSpec struct {
	Binary string
	Args   []string
	Env    []string
	Dir    string
	Stderr io.Writer
}

// startStdinProcess launches the claude binary with piped stdin/stdout.
func startStdinProcess(ctx context.Context, spec StdinProcessSpec) (*osStdinProcess, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(spec.Binary) == "" {
		return nil, errors.New("claude binary is required")
	}

	cmd := exec.CommandContext(ctx, spec.Binary, spec.Args...)
	if strings.TrimSpace(spec.Dir) != "" {
		cmd.Dir = spec.Dir
	}
	if len(spec.Env) > 0 {
		cmd.Env = append(os.Environ(), spec.Env...)
	}
	if spec.Stderr != nil {
		cmd.Stderr = spec.Stderr
	}

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("claude stdin pipe: %w", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("claude stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("claude start: %w", err)
	}

	return &osStdinProcess{
		cmd:      cmd,
		stdin:    stdinPipe,
		stdout:   stdoutPipe,
		waitDone: make(chan struct{}),
	}, nil
}

// stdinProcess abstracts a running claude subprocess with bidirectional stdio.
type stdinProcess interface {
	Stdin() io.WriteCloser
	Stdout() io.ReadCloser
	Wait() error
	Stop() error
	Kill() error
}

// osStdinProcess wraps an exec.Cmd whose stdin and stdout are piped.
type osStdinProcess struct {
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	stdout   io.ReadCloser
	waitOnce sync.Once
	waitErr  error
	waitDone chan struct{}
}

// Stdin returns the write end of the pipe connected to claude's stdin.
func (p *osStdinProcess) Stdin() io.WriteCloser {
	if p == nil {
		return nil
	}
	return p.stdin
}

// Stdout returns the read end of the pipe connected to claude's stdout.
func (p *osStdinProcess) Stdout() io.ReadCloser {
	if p == nil {
		return nil
	}
	return p.stdout
}

// Wait blocks until the process exits and is idempotent.
func (p *osStdinProcess) Wait() error {
	if p == nil || p.cmd == nil {
		return nil
	}
	p.waitOnce.Do(func() {
		p.waitErr = p.cmd.Wait()
		close(p.waitDone)
	})
	<-p.waitDone
	return p.waitErr
}

// Stop sends SIGINT to the process for graceful shutdown.
func (p *osStdinProcess) Stop() error {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return nil
	}
	if p.cmd.ProcessState != nil && p.cmd.ProcessState.Exited() {
		return nil
	}
	err := p.cmd.Process.Signal(os.Interrupt)
	if err == nil || errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}

// Kill forcibly terminates the process.
func (p *osStdinProcess) Kill() error {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return nil
	}
	if p.cmd.ProcessState != nil && p.cmd.ProcessState.Exited() {
		return nil
	}
	err := p.cmd.Process.Kill()
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}

// ---- TaskSessionRuntime ----

const defaultStdinBinary = "claude"
const defaultStdinStopTimeout = 10 * time.Second

// stdinProcessStarter is a factory for osStdinProcess instances.
type stdinProcessStarter interface {
	Start(context.Context, StdinProcessSpec) (*osStdinProcess, error)
}

type stdinProcessStarterFunc func(context.Context, StdinProcessSpec) (*osStdinProcess, error)

func (fn stdinProcessStarterFunc) Start(ctx context.Context, spec StdinProcessSpec) (*osStdinProcess, error) {
	return fn(ctx, spec)
}

// TaskSessionRuntime implements contracts.TaskSessionRuntime for the claude CLI
// using stdin/stdout streaming with --output-format stream-json.
type TaskSessionRuntime struct {
	binary  string
	args    []string
	starter stdinProcessStarter
}

// NewTaskSessionRuntime returns a runtime that spawns the given binary (defaults
// to "claude") with the given extra args prepended to each invocation.
func NewTaskSessionRuntime(binary string, args ...string) *TaskSessionRuntime {
	b := strings.TrimSpace(binary)
	if b == "" {
		b = defaultStdinBinary
	}
	return &TaskSessionRuntime{
		binary:  b,
		args:    append([]string(nil), args...),
		starter: stdinProcessStarterFunc(startStdinProcess),
	}
}

// StdinTaskSession is a running claude session backed by a single OS process.
type StdinTaskSession struct {
	id           string
	proc         *osStdinProcess
	logFile      *os.File
	stderrFile   *os.File
	readyTimeout time.Duration
	stopTimeout  time.Duration

	readyOnce sync.Once
	readyErr  error

	closeOnce sync.Once
	closeErr  error

	waitDone chan struct{}
	waitErr  error
}

// Start spawns the claude process and returns a StdinTaskSession.
func (r *TaskSessionRuntime) Start(ctx context.Context, request contracts.TaskSessionStartRequest) (contracts.TaskSession, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if r == nil {
		return nil, errors.New("nil claude task session runtime")
	}

	rt := *r
	if strings.TrimSpace(rt.binary) == "" {
		rt.binary = defaultStdinBinary
	}
	if rt.starter == nil {
		rt.starter = stdinProcessStarterFunc(startStdinProcess)
	}

	logPath := resolveStdinLogPath(request)
	logFile, stderrFile, err := openStdinLogFiles(logPath)
	if err != nil {
		return nil, err
	}

	binary, args := rt.buildCommand(request)
	proc, err := rt.starter.Start(ctx, StdinProcessSpec{
		Binary: binary,
		Args:   args,
		Env:    flattenStdinEnv(request.Env),
		Dir:    strings.TrimSpace(request.RepoRoot),
		Stderr: stderrFile,
	})
	if err != nil {
		_ = logFile.Close()
		_ = stderrFile.Close()
		return nil, err
	}

	sess := &StdinTaskSession{
		id:           resolveStdinTaskSessionID(request),
		proc:         proc,
		logFile:      logFile,
		stderrFile:   stderrFile,
		readyTimeout: request.ReadyTimeout,
		stopTimeout:  request.StopTimeout,
		waitDone:     make(chan struct{}),
	}
	go func() {
		sess.waitErr = proc.Wait()
		close(sess.waitDone)
	}()
	return sess, nil
}

func (r *TaskSessionRuntime) buildCommand(request contracts.TaskSessionStartRequest) (string, []string) {
	args := append([]string(nil), r.args...)
	if len(request.Command) > 0 {
		args = append([]string(nil), request.Command...)
	}
	if len(args) == 0 {
		args = defaultStdinArgs(request)
	}
	return r.binary, args
}

func defaultStdinArgs(request contracts.TaskSessionStartRequest) []string {
	args := []string{"--print", "--output-format", "stream-json", "--verbose", "--dangerously-skip-permissions"}
	if model := strings.TrimSpace(request.Metadata["model"]); model != "" {
		args = append(args, "--model", model)
	}
	return args
}

func resolveStdinTaskSessionID(request contracts.TaskSessionStartRequest) string {
	if id := strings.TrimSpace(request.TaskID); id != "" {
		return id
	}
	return "claude-stdin"
}

func resolveStdinLogPath(request contracts.TaskSessionStartRequest) string {
	if path := strings.TrimSpace(request.LogPath); path != "" {
		return path
	}
	if strings.TrimSpace(request.RepoRoot) != "" && strings.TrimSpace(request.TaskID) != "" {
		return filepath.Join(request.RepoRoot, "runner-logs", "claude", request.TaskID+".jsonl")
	}
	if strings.TrimSpace(request.TaskID) != "" {
		return filepath.Join("runner-logs", "claude", request.TaskID+".jsonl")
	}
	return filepath.Join("runner-logs", "claude", "claude-stdin.jsonl")
}

func openStdinLogFiles(logPath string) (*os.File, *os.File, error) {
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return nil, nil, err
	}
	logFile, err := os.Create(logPath)
	if err != nil {
		return nil, nil, err
	}
	stderrPath := contracts.BackendLogSidecarPath(logPath, contracts.BackendLogStderr)
	stderrFile, err := os.Create(stderrPath)
	if err != nil {
		_ = logFile.Close()
		return nil, nil, err
	}
	return logFile, stderrFile, nil
}

func flattenStdinEnv(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	out := make([]string, 0, len(env))
	for k, v := range env {
		if strings.TrimSpace(k) != "" {
			out = append(out, k+"="+v)
		}
	}
	return out
}

// LogPath returns the path of the stdout log file for this session.
func (s *StdinTaskSession) LogPath() string {
	if s == nil || s.logFile == nil {
		return ""
	}
	return s.logFile.Name()
}

func (s *StdinTaskSession) ID() string {
	if s == nil {
		return ""
	}
	return s.id
}

// WaitReady blocks until claude emits {"type":"system","subtype":"init"} on stdout
// or the readyTimeout elapses or the process exits early.
func (s *StdinTaskSession) WaitReady(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil {
		return errors.New("nil claude stdin task session")
	}
	s.readyOnce.Do(func() {
		readyCtx, cancel := withStdinOptionalTimeout(ctx, s.readyTimeout)
		defer cancel()
		s.readyErr = s.waitForInit(readyCtx)
	})
	return s.readyErr
}

func (s *StdinTaskSession) waitForInit(ctx context.Context) error {
	initCh := make(chan error, 1)
	go func() {
		// The scanner goroutine is unblocked when the process exits and closes
		// the pipe (bounded by process lifetime).
		initCh <- s.scanForInit()
	}()
	select {
	case err := <-initCh:
		return err
	case <-s.waitDone:
		return s.stdinExitedBeforeReadyError(s.waitErr)
	case <-ctx.Done():
		if _, done := s.processWaitErr(); done {
			return s.stdinExitedBeforeReadyError(s.waitErr)
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return s.stdinReadinessTimeoutError(ctx.Err())
		}
		return ctx.Err()
	}
}

func (s *StdinTaskSession) scanForInit() error {
	stdout := s.proc.Stdout()
	if stdout == nil {
		return errors.New("claude stdout pipe is nil")
	}
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		var msg struct {
			Type    string `json:"type"`
			Subtype string `json:"subtype"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}
		if msg.Type == "system" && msg.Subtype == "init" {
			return nil
		}
	}
	return scanner.Err()
}

func (s *StdinTaskSession) processWaitErr() (error, bool) {
	select {
	case <-s.waitDone:
		return s.waitErr, true
	default:
		return nil, false
	}
}

func (s *StdinTaskSession) stdinExitedBeforeReadyError(waitErr error) error {
	msg := "claude exited before ready"
	if summary := s.stderrSummary(); summary != "" {
		msg += "; stderr: " + summary
	}
	if waitErr == nil {
		return errors.New(msg)
	}
	return fmt.Errorf("%s: %w", msg, waitErr)
}

func (s *StdinTaskSession) stdinReadinessTimeoutError(timeoutErr error) error {
	msg := "timed out waiting for claude readiness"
	if summary := s.stderrSummary(); summary != "" {
		msg += "; stderr: " + summary
	}
	return fmt.Errorf("%s: %w", msg, timeoutErr)
}

func (s *StdinTaskSession) stderrSummary() string {
	if s == nil || s.stderrFile == nil {
		return ""
	}
	content, err := os.ReadFile(s.stderrFile.Name())
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	var last []string
	for i := len(lines) - 1; i >= 0 && len(last) < 3; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			last = append(last, l)
		}
	}
	for l, r := 0, len(last)-1; l < r; l, r = l+1, r-1 {
		last[l], last[r] = last[r], last[l]
	}
	return strings.Join(last, " | ")
}

func withStdinOptionalTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if d <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, d)
}

func (s *StdinTaskSession) Execute(ctx context.Context, req contracts.TaskSessionExecuteRequest) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.WaitReady(ctx); err != nil {
		return fmt.Errorf("claude not ready: %w", err)
	}
	if _, err := fmt.Fprintln(s.proc.Stdin(), req.Prompt); err != nil {
		return fmt.Errorf("claude stdin write: %w", err)
	}
	// Close stdin to signal EOF so claude starts processing the prompt.
	_ = s.proc.Stdin().Close()
	return s.readUntilResult(ctx, req)
}

func (s *StdinTaskSession) readUntilResult(ctx context.Context, req contracts.TaskSessionExecuteRequest) error {
	resultCh := make(chan error, 1)
	go func() {
		// The scanner goroutine holds stdout until a result event or EOF.
		// On ctx cancel or early process exit, it is unblocked when the
		// process exits and closes the pipe (bounded by process lifetime).
		resultCh <- s.scanForResult(ctx, req)
	}()
	select {
	case err := <-resultCh:
		return err
	case <-s.waitDone:
		return fmt.Errorf("claude exited before result")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *StdinTaskSession) scanForResult(ctx context.Context, req contracts.TaskSessionExecuteRequest) error {
	stdout := s.proc.Stdout()
	if stdout == nil {
		return errors.New("claude stdout pipe is nil")
	}
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Bytes()
		if s.logFile != nil {
			_, _ = fmt.Fprintf(s.logFile, "%s\n", line)
		}
		var msg struct {
			Type    string `json:"type"`
			Subtype string `json:"subtype"`
			Error   string `json:"error"`
			ID      string `json:"id"`
			Name    string `json:"name"`
			Message struct {
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		switch msg.Type {
		case "result":
			if msg.Subtype == "success" {
				return nil
			}
			if msg.Subtype == "error_during_execution" {
				if msg.Error != "" {
					return fmt.Errorf("claude execution error: %s", msg.Error)
				}
				return errors.New("claude execution error")
			}
		case "assistant":
			if req.EventSink != nil {
				text := stdinExtractAssistantText(msg.Message.Content)
				_ = req.EventSink.HandleEvent(ctx, contracts.TaskSessionEvent{
					Type:      contracts.TaskSessionEventTypeOutput,
					SessionID: s.id,
					Message:   text,
					Timestamp: time.Now().UTC(),
				})
			}
		case "tool_use":
			if err := s.handleToolUse(ctx, msg.ID, msg.Name, req); err != nil {
				return err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("claude stdout scan: %w", err)
	}
	return errors.New("claude stdout closed without result")
}

func stdinExtractAssistantText(content []struct {
	Type string `json:"type"`
	Text string `json:"text"`
}) string {
	var parts []string
	for _, c := range content {
		if c.Type == "text" && c.Text != "" {
			parts = append(parts, c.Text)
		}
	}
	return strings.Join(parts, "")
}

// handleToolUse always approves tool_use requests by writing "y\n" to stdin.
// ApprovalHandler is called for observation only — its decision is not honoured
// because this runtime auto-approves all tool calls per its contract.
func (s *StdinTaskSession) handleToolUse(ctx context.Context, id, name string, req contracts.TaskSessionExecuteRequest) error {
	approvalReq := contracts.TaskSessionApprovalRequest{
		ID:    id,
		Kind:  contracts.TaskSessionApprovalKindToolCall,
		Title: name,
	}
	decision := contracts.TaskSessionApprovalDecision{Outcome: contracts.TaskSessionApprovalApproved}
	if req.ApprovalHandler != nil {
		if d, err := req.ApprovalHandler.HandleApproval(ctx, approvalReq); err == nil {
			decision = d
		}
	}
	// In --print --dangerously-skip-permissions mode, tool_use events are
	// informational only — permissions are already bypassed, no stdin write needed.
	if req.EventSink != nil {
		_ = req.EventSink.HandleEvent(ctx, contracts.TaskSessionEvent{
			Type:      contracts.TaskSessionEventTypeApprovalRequired,
			SessionID: s.id,
			Timestamp: time.Now().UTC(),
			Approval: &contracts.TaskSessionApprovalEvent{
				Request:  approvalReq,
				Decision: &decision,
			},
		})
	}
	return nil
}
func (s *StdinTaskSession) Cancel(ctx context.Context, req contracts.TaskSessionCancellation) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil {
		return errors.New("nil claude stdin task session")
	}
	if req.Force {
		return s.close(ctx, true)
	}
	return ignoreStdinProcessDone(s.proc.Stop())
}

func (s *StdinTaskSession) Teardown(ctx context.Context, req contracts.TaskSessionTeardown) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil {
		return errors.New("nil claude stdin task session")
	}
	return s.close(ctx, req.Force)
}

func (s *StdinTaskSession) close(ctx context.Context, force bool) error {
	s.closeOnce.Do(func() {
		closeCtx, cancel := withStdinOptionalTimeout(ctx, s.stopTimeoutValue())
		defer cancel()

		_ = s.proc.Stdin().Close()

		if force {
			s.closeErr = ignoreStdinProcessDone(s.proc.Kill())
			s.closeLogs()
			return
		}

		stopErr := ignoreStdinProcessDone(s.proc.Stop())
		waitErr := s.waitWithContext(closeCtx)
		if errors.Is(waitErr, context.DeadlineExceeded) || errors.Is(waitErr, context.Canceled) {
			waitErr = errors.Join(waitErr, ignoreStdinProcessDone(s.proc.Kill()))
		}
		s.closeLogs()
		s.closeErr = errors.Join(stopErr, ignoreStdinProcessDone(waitErr))
	})
	return s.closeErr
}

func (s *StdinTaskSession) waitWithContext(ctx context.Context) error {
	select {
	case <-s.waitDone:
		return s.waitErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *StdinTaskSession) closeLogs() {
	if s.logFile != nil {
		_ = s.logFile.Close()
		s.logFile = nil
	}
	if s.stderrFile != nil {
		_ = s.stderrFile.Close()
		s.stderrFile = nil
	}
}

func (s *StdinTaskSession) stopTimeoutValue() time.Duration {
	if s.stopTimeout > 0 {
		return s.stopTimeout
	}
	return defaultStdinStopTimeout
}

func ignoreStdinProcessDone(err error) error {
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}
