package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

const (
	defaultServeBinary              = "opencode"
	defaultServeHostname            = "127.0.0.1"
	defaultServeHealthCheckInterval = 100 * time.Millisecond
	defaultServeStopTimeout         = 2 * time.Second
)

type ServeCommandSpec struct {
	Binary string
	Args   []string
	Env    []string
	Dir    string
	Stdout io.Writer
	Stderr io.Writer
}

type serveProcess interface {
	Wait() error
	Stop() error
	Kill() error
}

type serveProcessStarter interface {
	Start(context.Context, ServeCommandSpec) (serveProcess, error)
}

type serveProcessStarterFunc func(context.Context, ServeCommandSpec) (serveProcess, error)

func (fn serveProcessStarterFunc) Start(ctx context.Context, spec ServeCommandSpec) (serveProcess, error) {
	return fn(ctx, spec)
}

type TaskSessionRuntime struct {
	binary              string
	args                []string
	starter             serveProcessStarter
	httpClient          *http.Client
	hostname            string
	allocatePort        func(hostname string) (int, error)
	healthCheckInterval time.Duration
}

type ServeTaskSession struct {
	id                  string
	taskTitle           string
	proc                serveProcess
	client              *http.Client
	baseURL             string
	healthURL           string
	sessionURL          string
	disposeURL          string
	readyTimeout        time.Duration
	stopTimeout         time.Duration
	healthCheckInterval time.Duration

	readyOnce sync.Once
	readyErr  error

	closeOnce sync.Once
	closeErr  error

	stateMu   sync.Mutex
	sessionID string
	waitErr   error
	waitDone  chan struct{}

	stdoutFile *os.File
	stderrFile *os.File
}

func NewTaskSessionRuntime(binary string, args ...string) *TaskSessionRuntime {
	resolvedBinary := strings.TrimSpace(binary)
	if resolvedBinary == "" {
		resolvedBinary = defaultServeBinary
	}
	return &TaskSessionRuntime{
		binary:              resolvedBinary,
		args:                append([]string(nil), args...),
		starter:             serveProcessStarterFunc(startServeProcess),
		httpClient:          &http.Client{},
		hostname:            defaultServeHostname,
		healthCheckInterval: defaultServeHealthCheckInterval,
	}
}

func (r *TaskSessionRuntime) Start(ctx context.Context, request contracts.TaskSessionStartRequest) (_ contracts.TaskSession, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if r == nil {
		return nil, errors.New("nil opencode task session runtime")
	}

	runtime := *r
	if strings.TrimSpace(runtime.binary) == "" {
		runtime.binary = defaultServeBinary
	}
	if runtime.starter == nil {
		runtime.starter = serveProcessStarterFunc(startServeProcess)
	}
	if runtime.httpClient == nil {
		runtime.httpClient = &http.Client{}
	}
	if strings.TrimSpace(runtime.hostname) == "" {
		runtime.hostname = defaultServeHostname
	}
	if runtime.healthCheckInterval <= 0 {
		runtime.healthCheckInterval = defaultServeHealthCheckInterval
	}

	port, err := AllocateServePort(runtime.hostname, request)
	if runtime.allocatePort != nil {
		port, err = runtime.allocatePort(runtime.hostname)
	}
	if err != nil {
		return nil, err
	}

	logPath := resolveServeSessionLogPath(request)
	stdoutFile, stderrFile, err := openServeLogFiles(logPath)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err == nil {
			return
		}
		if stdoutFile != nil {
			_ = stdoutFile.Close()
		}
		if stderrFile != nil {
			_ = stderrFile.Close()
		}
	}()

	proc, err := runtime.startPreparedServeProcess(ctx, request, stdoutFile, stderrFile, runtime.hostname, port)
	if err != nil {
		return nil, err
	}

	return runtime.newInitialServeTaskSession(request, proc, stdoutFile, stderrFile, runtime.hostname, port), nil
}

func (r *TaskSessionRuntime) startPreparedServeProcess(ctx context.Context, request contracts.TaskSessionStartRequest, stdout io.Writer, stderr io.Writer, hostname string, port int) (serveProcess, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if r == nil {
		return nil, errors.New("nil opencode task session runtime")
	}

	starter := r.starter
	if starter == nil {
		starter = serveProcessStarterFunc(startServeProcess)
	}

	binary, args := r.buildCommand(request, hostname, port)
	spec := ServeCommandSpec{
		Binary: binary,
		Args:   args,
		Env:    flattenServeEnv(request.Env),
		Dir:    strings.TrimSpace(request.RepoRoot),
		Stdout: stdout,
		Stderr: stderr,
	}
	proc, err := starter.Start(ctx, spec)
	if err != nil {
		return nil, err
	}
	if proc == nil {
		return nil, errors.New("opencode serve starter returned nil process")
	}
	return proc, nil
}

func (r *TaskSessionRuntime) newInitialServeTaskSession(request contracts.TaskSessionStartRequest, proc serveProcess, stdoutFile *os.File, stderrFile *os.File, hostname string, port int) *ServeTaskSession {
	baseURL := resolveServeBaseURL(hostname, port)
	session := &ServeTaskSession{
		id:                  resolveServeTaskSessionID(request),
		taskTitle:           resolveServeTaskSessionTitle(request),
		proc:                proc,
		client:              r.httpClient,
		baseURL:             baseURL,
		healthURL:           resolveServeHealthURL(baseURL),
		sessionURL:          baseURL + "/session",
		disposeURL:          baseURL + "/instance/dispose",
		readyTimeout:        request.ReadyTimeout,
		stopTimeout:         request.StopTimeout,
		healthCheckInterval: r.healthCheckInterval,
		waitDone:            make(chan struct{}),
		stdoutFile:          stdoutFile,
		stderrFile:          stderrFile,
	}
	go func() {
		session.waitErr = proc.Wait()
		close(session.waitDone)
	}()
	return session
}

func (r *TaskSessionRuntime) buildCommand(request contracts.TaskSessionStartRequest, hostname string, port int) (string, []string) {
	var raw []string
	if len(request.Command) > 0 {
		raw = resolveServeArgs(request.Command, request, hostname, port)
	} else if len(r.args) > 0 {
		raw = resolveServeArgs(r.args, request, hostname, port)
	}
	binary, args := resolveServeCommand(r.binary, raw)
	if !containsServeHostnameFlag(args) {
		args = append(args, "--hostname", hostname)
	}
	if !containsServePortFlag(args) {
		args = append(args, "--port", strconv.Itoa(port))
	}
	return binary, args
}

func containsServeHostnameFlag(args []string) bool {
	for i := 0; i < len(args); i++ {
		if strings.TrimSpace(args[i]) == "--hostname" {
			return true
		}
	}
	return false
}

func containsServePortFlag(args []string) bool {
	for i := 0; i < len(args); i++ {
		if strings.TrimSpace(args[i]) == "--port" {
			return true
		}
	}
	return false
}

func resolveServeArgs(raw []string, request contracts.TaskSessionStartRequest, hostname string, port int) []string {
	replacements := map[string]string{
		"{{backend}}":   "opencode",
		"{{task_id}}":   strings.TrimSpace(request.TaskID),
		"{{repo_root}}": strings.TrimSpace(request.RepoRoot),
		"{{hostname}}":  strings.TrimSpace(hostname),
		"{{port}}":      strconv.Itoa(port),
	}

	out := make([]string, 0, len(raw))
	for _, value := range raw {
		text := strings.TrimSpace(value)
		for placeholder, replacement := range replacements {
			text = strings.ReplaceAll(text, placeholder, replacement)
		}
		if text == "" {
			continue
		}
		out = append(out, text)
	}
	return out
}

func resolveServeTaskSessionID(request contracts.TaskSessionStartRequest) string {
	if id := strings.TrimSpace(request.TaskID); id != "" {
		return id
	}
	return "opencode-serve"
}

func resolveServeTaskSessionTitle(request contracts.TaskSessionStartRequest) string {
	if title := strings.TrimSpace(request.TaskID); title != "" {
		return title
	}
	return "yolo-runner task"
}

func resolveServeSessionLogPath(request contracts.TaskSessionStartRequest) string {
	if path := strings.TrimSpace(request.LogPath); path != "" {
		return path
	}
	if strings.TrimSpace(request.RepoRoot) != "" && strings.TrimSpace(request.TaskID) != "" {
		return filepath.Join(request.RepoRoot, "runner-logs", "opencode", request.TaskID+".jsonl")
	}
	if strings.TrimSpace(request.TaskID) != "" {
		return filepath.Join("runner-logs", "opencode", request.TaskID+".jsonl")
	}
	return filepath.Join("runner-logs", "opencode", "opencode-serve.jsonl")
}

func openServeLogFiles(logPath string) (*os.File, *os.File, error) {
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return nil, nil, err
	}

	stdoutFile, err := os.Create(logPath)
	if err != nil {
		return nil, nil, err
	}
	stderrPath := contracts.BackendLogSidecarPath(logPath, contracts.BackendLogStderr)
	stderrFile, err := os.Create(stderrPath)
	if err != nil {
		_ = stdoutFile.Close()
		return nil, nil, err
	}
	return stdoutFile, stderrFile, nil
}

func flattenServeEnv(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for key := range env {
		trimmed := strings.TrimSpace(key)
		if trimmed == "" {
			continue
		}
		keys = append(keys, trimmed)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+env[key])
	}
	return out
}

func (s *ServeTaskSession) ID() string {
	if s == nil {
		return ""
	}
	return s.id
}

func (s *ServeTaskSession) WaitReady(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil {
		return errors.New("nil opencode serve task session")
	}

	s.readyOnce.Do(func() {
		readyCtx, cancel := withOptionalTimeout(ctx, s.readyTimeout)
		defer cancel()

		if err := s.waitForHealth(readyCtx); err != nil {
			s.readyErr = err
			return
		}
		createdID, err := s.createSession(readyCtx)
		if err != nil {
			s.readyErr = err
			return
		}
		s.setSessionID(createdID)
	})
	return s.readyErr
}

func (s *ServeTaskSession) waitForHealth(ctx context.Context) error {
	interval := s.healthCheckInterval
	if interval <= 0 {
		interval = defaultServeHealthCheckInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var lastHealthErr error

	for {
		if err := s.checkHealth(ctx); err == nil {
			return nil
		} else if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			lastHealthErr = err
		}

		select {
		case <-ctx.Done():
			if procErr, ok := s.processWaitErr(); ok {
				return s.serveExitedBeforeReadinessError(procErr)
			}
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return s.serveReadinessTimeoutError(ctx.Err(), lastHealthErr)
			}
			return ctx.Err()
		case <-s.waitDone:
			return s.serveExitedBeforeReadinessError(s.waitErr)
		case <-ticker.C:
		}
	}
}

func (s *ServeTaskSession) checkHealth(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.healthURL, http.NoBody)
	if err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("health endpoint returned %d", resp.StatusCode)
	}

	var payload struct {
		Healthy *bool `json:"healthy"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if payload.Healthy != nil && !*payload.Healthy {
		return errors.New("opencode global health reported unhealthy")
	}
	return nil
}

func (s *ServeTaskSession) createSession(ctx context.Context) (string, error) {
	body, err := json.Marshal(map[string]string{"title": s.taskTitle})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.sessionURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("create session returned %d", resp.StatusCode)
	}

	var payload struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	if strings.TrimSpace(payload.ID) == "" {
		return "", errors.New("opencode create session response missing id")
	}
	return strings.TrimSpace(payload.ID), nil
}

func (s *ServeTaskSession) Execute(ctx context.Context, request contracts.TaskSessionExecuteRequest) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil {
		return errors.New("nil opencode serve task session")
	}
	if err := s.WaitReady(ctx); err != nil {
		return err
	}

	sessionID := s.currentSessionID()
	if sessionID == "" {
		createdSessionID, err := s.createSession(ctx)
		if err != nil {
			return err
		}
		s.setSessionID(createdSessionID)
		sessionID = createdSessionID
	}

	return s.submitPromptMessage(ctx, sessionID, strings.TrimSpace(request.Prompt))
}

func (s *ServeTaskSession) submitPromptMessage(ctx context.Context, sessionID string, prompt string) error {
	body, err := json.Marshal(struct {
		Parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"parts"`
	}{
		Parts: []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{
			{
				Type: "text",
				Text: prompt,
			},
		},
	})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.sessionURL+"/"+strings.TrimSpace(sessionID)+"/message", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		if len(respBody) > 0 {
			return fmt.Errorf("submit session message returned %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
		}
		return fmt.Errorf("submit session message returned %d", resp.StatusCode)
	}
	return nil
}

func (s *ServeTaskSession) Cancel(ctx context.Context, request contracts.TaskSessionCancellation) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil {
		return errors.New("nil opencode serve task session")
	}
	if request.Force {
		return s.close(ctx, true)
	}

	sessionID := s.currentSessionID()
	if sessionID == "" {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.sessionURL+"/"+sessionID+"/abort", http.NoBody)
	if err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("abort session returned %d", resp.StatusCode)
	}
	return nil
}

func (s *ServeTaskSession) Teardown(ctx context.Context, request contracts.TaskSessionTeardown) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil {
		return errors.New("nil opencode serve task session")
	}
	return s.close(ctx, request.Force)
}

func (s *ServeTaskSession) close(ctx context.Context, force bool) error {
	s.closeOnce.Do(func() {
		closeCtx, cancel := withOptionalTimeout(ctx, s.stopTimeoutValue())
		defer cancel()

		var shutdownErr error
		if sessionID := s.currentSessionID(); sessionID != "" {
			shutdownErr = errors.Join(shutdownErr, s.deleteSession(closeCtx, sessionID))
		}
		shutdownErr = errors.Join(shutdownErr, s.disposeInstance(closeCtx))

		if force {
			shutdownErr = errors.Join(shutdownErr, ignoreServeProcessDone(s.proc.Kill()))
			shutdownErr = errors.Join(shutdownErr, ignoreServeProcessDone(s.wait()))
			s.closeLogs()
			s.closeErr = shutdownErr
			return
		}

		stopErr := ignoreServeProcessDone(s.proc.Stop())
		waitErr := s.waitWithContext(closeCtx)
		if errors.Is(waitErr, context.DeadlineExceeded) || errors.Is(waitErr, context.Canceled) {
			waitErr = errors.Join(waitErr, ignoreServeProcessDone(s.proc.Kill()), ignoreServeProcessDone(s.wait()))
		}
		s.closeLogs()
		s.closeErr = errors.Join(shutdownErr, stopErr, ignoreServeProcessDone(waitErr))
	})
	return s.closeErr
}

func (s *ServeTaskSession) deleteSession(ctx context.Context, sessionID string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, s.sessionURL+"/"+sessionID, http.NoBody)
	if err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("delete session returned %d", resp.StatusCode)
	}
	return nil
}

func (s *ServeTaskSession) disposeInstance(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.disposeURL, http.NoBody)
	if err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("dispose instance returned %d", resp.StatusCode)
	}
	return nil
}

func (s *ServeTaskSession) stopTimeoutValue() time.Duration {
	if s.stopTimeout > 0 {
		return s.stopTimeout
	}
	return defaultServeStopTimeout
}

func (s *ServeTaskSession) currentSessionID() string {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return strings.TrimSpace(s.sessionID)
}

func (s *ServeTaskSession) setSessionID(sessionID string) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.sessionID = strings.TrimSpace(sessionID)
}

func (s *ServeTaskSession) processWaitErr() (error, bool) {
	select {
	case <-s.waitDone:
		return s.waitErr, true
	default:
		return nil, false
	}
}

func (s *ServeTaskSession) waitWithContext(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.waitDone:
		return s.waitErr
	}
}

func (s *ServeTaskSession) wait() error {
	<-s.waitDone
	return s.waitErr
}

func (s *ServeTaskSession) closeLogs() {
	if s.stdoutFile != nil {
		_ = s.stdoutFile.Close()
		s.stdoutFile = nil
	}
	if s.stderrFile != nil {
		_ = s.stderrFile.Close()
		s.stderrFile = nil
	}
}

func (s *ServeTaskSession) serveReadinessTimeoutError(timeoutErr error, lastHealthErr error) error {
	parts := []string{
		fmt.Sprintf("timed out waiting for opencode serve readiness at %s", s.healthURL),
	}
	if lastHealthErr != nil {
		parts = append(parts, "last health error: "+strings.TrimSpace(lastHealthErr.Error()))
	}
	if stderrPath := s.stderrLogPath(); stderrPath != "" {
		parts = append(parts, "stderr log: "+stderrPath)
	}
	if stderrSummary := s.stderrLogSummary(); stderrSummary != "" {
		parts = append(parts, "stderr: "+stderrSummary)
	}
	return fmt.Errorf("%s: %w", strings.Join(parts, "; "), timeoutErr)
}

func (s *ServeTaskSession) serveExitedBeforeReadinessError(waitErr error) error {
	parts := []string{"opencode serve exited before readiness"}
	if stderrPath := s.stderrLogPath(); stderrPath != "" {
		parts = append(parts, "stderr log: "+stderrPath)
	}
	if stderrSummary := s.stderrLogSummary(); stderrSummary != "" {
		parts = append(parts, "stderr: "+stderrSummary)
	}
	message := strings.Join(parts, "; ")
	if waitErr == nil {
		return errors.New(message)
	}
	return fmt.Errorf("%s: %w", message, waitErr)
}

func (s *ServeTaskSession) stderrLogPath() string {
	if s == nil || s.stderrFile == nil {
		return ""
	}
	return strings.TrimSpace(s.stderrFile.Name())
}

func (s *ServeTaskSession) stderrLogSummary() string {
	path := s.stderrLogPath()
	if path == "" {
		return ""
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(content), "\n")
	summary := make([]string, 0, 3)
	for i := len(lines) - 1; i >= 0 && len(summary) < 3; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		summary = append(summary, line)
	}
	for left, right := 0, len(summary)-1; left < right; left, right = left+1, right-1 {
		summary[left], summary[right] = summary[right], summary[left]
	}
	return strings.Join(summary, " | ")
}

func withOptionalTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

func allocateLoopbackPort(hostname string) (int, error) {
	if strings.TrimSpace(hostname) == "" {
		hostname = defaultServeHostname
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(hostname, "0"))
	if err != nil {
		return 0, err
	}
	defer func() {
		_ = listener.Close()
	}()

	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("unexpected loopback listener address %T", listener.Addr())
	}
	return addr.Port, nil
}

type osServeProcess struct {
	cmd      *exec.Cmd
	waitOnce sync.Once
	waitErr  error
	waitDone chan struct{}
}

func startServeProcess(ctx context.Context, spec ServeCommandSpec) (serveProcess, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(spec.Binary) == "" {
		return nil, errors.New("opencode binary is required")
	}

	cmd := exec.Command(spec.Binary, spec.Args...)
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

	return &osServeProcess{
		cmd:      cmd,
		waitDone: make(chan struct{}),
	}, nil
}

func (p *osServeProcess) Wait() error {
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

func (p *osServeProcess) Stop() error {
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

func (p *osServeProcess) Kill() error {
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

func ignoreServeProcessDone(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}
