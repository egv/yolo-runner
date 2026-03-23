package opencode

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

// acpTestProcess is a fake process with piped stdin/stdout for ACP tests.
type acpTestProcess struct {
	stdinR  *io.PipeReader
	stdinW  *io.PipeWriter
	stdoutR *io.PipeReader
	stdoutW *io.PipeWriter
	waitCh  chan error
}

func newACPTestProcess() *acpTestProcess {
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	return &acpTestProcess{
		stdinR:  stdinR,
		stdinW:  stdinW,
		stdoutR: stdoutR,
		stdoutW: stdoutW,
		waitCh:  make(chan error, 1),
	}
}

func (p *acpTestProcess) Stdin() io.WriteCloser { return p.stdinW }
func (p *acpTestProcess) Stdout() io.ReadCloser { return p.stdoutR }
func (p *acpTestProcess) Wait() error           { return <-p.waitCh }
func (p *acpTestProcess) Kill() error {
	_ = p.stdinR.Close()
	_ = p.stdoutW.Close()
	p.waitCh <- nil
	return nil
}
func (p *acpTestProcess) Stop() error { return p.Kill() }

// TestACPTaskSessionRuntimeStartReturnsSessionWithCorrectID verifies that
// Start() returns a session whose ID matches the TaskID from the request.
func TestACPTaskSessionRuntimeStartReturnsSessionWithCorrectID(t *testing.T) {
	proc := newACPTestProcess()
	proc.waitCh <- nil // process exits cleanly

	runtime := NewACPTaskSessionRuntime(RunnerFunc(func(args []string, env map[string]string, stdoutPath string) (Process, error) {
		return proc, nil
	}))

	session, err := runtime.Start(context.Background(), contracts.TaskSessionStartRequest{
		TaskID:   "task-acp-id",
		RepoRoot: t.TempDir(),
		LogPath:  filepath.Join(t.TempDir(), "runner-logs", "opencode", "task-acp-id.jsonl"),
	})
	if err != nil {
		t.Fatalf("Start() returned error: %v", err)
	}
	if session == nil {
		t.Fatal("Start() returned nil session")
	}
	if got := session.ID(); got != "task-acp-id" {
		t.Fatalf("expected session ID %q, got %q", "task-acp-id", got)
	}
}

// TestACPTaskSessionRuntimeStartPassesACPArgsToRunner verifies that Start()
// builds ACP command args from the request and passes them to the runner.
func TestACPTaskSessionRuntimeStartPassesACPArgsToRunner(t *testing.T) {
	proc := newACPTestProcess()
	proc.waitCh <- nil

	var capturedArgs []string
	runtime := NewACPTaskSessionRuntime(RunnerFunc(func(args []string, env map[string]string, stdoutPath string) (Process, error) {
		capturedArgs = args
		return proc, nil
	}))

	repoRoot := t.TempDir()
	_, err := runtime.Start(context.Background(), contracts.TaskSessionStartRequest{
		TaskID:   "task-acp-args",
		RepoRoot: repoRoot,
		LogPath:  filepath.Join(t.TempDir(), "runner-logs", "opencode", "task-acp-args.jsonl"),
	})
	if err != nil {
		t.Fatalf("Start() returned error: %v", err)
	}

	// Should include "acp" subcommand and the repo root via --cwd.
	found := false
	for _, arg := range capturedArgs {
		if arg == "acp" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected 'acp' in runner args, got %#v", capturedArgs)
	}
}

// TestACPTaskSessionWaitReadyReturnsErrorWhenContextCancelled verifies that
// WaitReady() returns an error when the context is cancelled before the
// Initialize handshake completes.
func TestACPTaskSessionWaitReadyReturnsErrorWhenContextCancelled(t *testing.T) {
	proc := newACPTestProcess()

	runtime := NewACPTaskSessionRuntime(RunnerFunc(func(args []string, env map[string]string, stdoutPath string) (Process, error) {
		return proc, nil
	}))

	session, err := runtime.Start(context.Background(), contracts.TaskSessionStartRequest{
		TaskID:   "task-acp-cancel",
		RepoRoot: t.TempDir(),
		LogPath:  filepath.Join(t.TempDir(), "runner-logs", "opencode", "task-acp-cancel.jsonl"),
	})
	if err != nil {
		t.Fatalf("Start() returned error: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Teardown(context.Background(), contracts.TaskSessionTeardown{Force: true})
		proc.waitCh <- nil
	})

	// Cancel the context immediately — WaitReady should fail without blocking
	// because the ACP Initialize cannot complete without a real ACP server.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := session.WaitReady(ctx); err == nil {
		t.Fatal("expected WaitReady to return error when context is already cancelled")
	}
}

// TestACPTaskSessionRuntimeImplementsTaskSessionRuntime confirms that
// ACPTaskSessionRuntime satisfies the contracts.TaskSessionRuntime interface.
func TestACPTaskSessionRuntimeImplementsTaskSessionRuntime(t *testing.T) {
	var _ contracts.TaskSessionRuntime = (*ACPTaskSessionRuntime)(nil)
}

// acpTeardownProcess is a fake process for testing ACP session teardown.
// It tracks Kill calls and supports simulating both forced and natural exit.
type acpTeardownProcess struct {
	stdinR  *io.PipeReader
	stdinW  *io.PipeWriter
	stdoutR *io.PipeReader
	stdoutW *io.PipeWriter
	waitCh  chan error

	mu        sync.Mutex
	killCalls int
	exitOnce  sync.Once
}

func newACPTeardownProcess() *acpTeardownProcess {
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	return &acpTeardownProcess{
		stdinR:  stdinR,
		stdinW:  stdinW,
		stdoutR: stdoutR,
		stdoutW: stdoutW,
		waitCh:  make(chan error, 1),
	}
}

func (p *acpTeardownProcess) Stdin() io.WriteCloser { return p.stdinW }
func (p *acpTeardownProcess) Stdout() io.ReadCloser { return p.stdoutR }
func (p *acpTeardownProcess) Wait() error           { return <-p.waitCh }
func (p *acpTeardownProcess) Kill() error {
	p.mu.Lock()
	p.killCalls++
	p.mu.Unlock()
	p.exitOnce.Do(func() {
		_ = p.stdinR.Close()
		_ = p.stdoutW.Close()
		p.waitCh <- nil
	})
	return nil
}

// exitNaturally simulates the process exiting on its own without Kill.
func (p *acpTeardownProcess) exitNaturally() {
	p.exitOnce.Do(func() {
		p.waitCh <- nil
	})
}

func (p *acpTeardownProcess) kills() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.killCalls
}

// TestACPTaskSessionTeardownGracefulDoesNotKillWhenProcessExitsCleanly verifies
// that a graceful (non-force) teardown does not call Kill when the process exits
// on its own within the stop timeout.
func TestACPTaskSessionTeardownGracefulDoesNotKillWhenProcessExitsCleanly(t *testing.T) {
	proc := newACPTeardownProcess()
	rt := NewACPTaskSessionRuntime(RunnerFunc(func(args []string, env map[string]string, stdoutPath string) (Process, error) {
		return proc, nil
	}))

	session, err := rt.Start(context.Background(), contracts.TaskSessionStartRequest{
		TaskID:      "task-graceful",
		RepoRoot:    t.TempDir(),
		LogPath:     filepath.Join(t.TempDir(), "acp.jsonl"),
		StopTimeout: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Simulate process exiting naturally before the stop timeout expires.
	proc.exitNaturally()

	if err := session.Teardown(context.Background(), contracts.TaskSessionTeardown{}); err != nil {
		t.Fatalf("Teardown: %v", err)
	}

	if got := proc.kills(); got != 0 {
		t.Fatalf("expected Kill not called for graceful exit, got %d calls", got)
	}
}

// TestACPTaskSessionTeardownGracefulFallsBackToKillWhenProcessStalls verifies
// that a graceful teardown falls back to Kill when the process does not exit
// within the stop timeout.
func TestACPTaskSessionTeardownGracefulFallsBackToKillWhenProcessStalls(t *testing.T) {
	proc := newACPTeardownProcess()
	rt := NewACPTaskSessionRuntime(RunnerFunc(func(args []string, env map[string]string, stdoutPath string) (Process, error) {
		return proc, nil
	}))

	session, err := rt.Start(context.Background(), contracts.TaskSessionStartRequest{
		TaskID:      "task-stall",
		RepoRoot:    t.TempDir(),
		LogPath:     filepath.Join(t.TempDir(), "acp.jsonl"),
		StopTimeout: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Process never exits naturally — teardown must fall back to Kill.
	_ = session.Teardown(context.Background(), contracts.TaskSessionTeardown{})

	if got := proc.kills(); got != 1 {
		t.Fatalf("expected Kill called once as fallback, got %d calls", got)
	}
}

// TestACPTaskSessionTeardownForceKillsProcess verifies that a forced teardown
// immediately calls Kill and waits for the process to exit.
func TestACPTaskSessionTeardownForceKillsProcess(t *testing.T) {
	proc := newACPTeardownProcess()
	rt := NewACPTaskSessionRuntime(RunnerFunc(func(args []string, env map[string]string, stdoutPath string) (Process, error) {
		return proc, nil
	}))

	session, err := rt.Start(context.Background(), contracts.TaskSessionStartRequest{
		TaskID:   "task-force",
		RepoRoot: t.TempDir(),
		LogPath:  filepath.Join(t.TempDir(), "acp.jsonl"),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := session.Teardown(context.Background(), contracts.TaskSessionTeardown{Force: true}); err != nil {
		t.Fatalf("Teardown: %v", err)
	}

	if got := proc.kills(); got != 1 {
		t.Fatalf("expected Kill called once, got %d calls", got)
	}
}

// TestACPTaskSessionCancelNonForceDoesNotKillWhenProcessExitsCleanly verifies
// that a non-force Cancel does not call Kill immediately when the process exits
// on its own within the stop timeout (mirrors graceful Teardown behaviour).
func TestACPTaskSessionCancelNonForceDoesNotKillWhenProcessExitsCleanly(t *testing.T) {
	proc := newACPTeardownProcess()
	rt := NewACPTaskSessionRuntime(RunnerFunc(func(args []string, env map[string]string, stdoutPath string) (Process, error) {
		return proc, nil
	}))

	session, err := rt.Start(context.Background(), contracts.TaskSessionStartRequest{
		TaskID:      "task-cancel-graceful",
		RepoRoot:    t.TempDir(),
		LogPath:     filepath.Join(t.TempDir(), "acp.jsonl"),
		StopTimeout: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Simulate process exiting naturally before the stop timeout expires.
	proc.exitNaturally()

	if err := session.Cancel(context.Background(), contracts.TaskSessionCancellation{Force: false}); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	if got := proc.kills(); got != 0 {
		t.Fatalf("expected Kill not called for non-force cancel with natural exit, got %d calls", got)
	}
}

// TestACPTaskSessionCancelNonForceFallsBackToKillWhenProcessStalls verifies
// that a non-force Cancel falls back to Kill when the process does not exit
// within the stop timeout (mirrors graceful Teardown behaviour).
func TestACPTaskSessionCancelNonForceFallsBackToKillWhenProcessStalls(t *testing.T) {
	proc := newACPTeardownProcess()
	rt := NewACPTaskSessionRuntime(RunnerFunc(func(args []string, env map[string]string, stdoutPath string) (Process, error) {
		return proc, nil
	}))

	session, err := rt.Start(context.Background(), contracts.TaskSessionStartRequest{
		TaskID:      "task-cancel-stall",
		RepoRoot:    t.TempDir(),
		LogPath:     filepath.Join(t.TempDir(), "acp.jsonl"),
		StopTimeout: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Process never exits naturally — Cancel must fall back to Kill.
	_ = session.Cancel(context.Background(), contracts.TaskSessionCancellation{Force: false})

	if got := proc.kills(); got != 1 {
		t.Fatalf("expected Kill called once as fallback, got %d calls", got)
	}
}

// TestACPTaskSessionTeardownPropagatesContextCancellation verifies that when
// the context passed to Teardown is already cancelled, the close propagates
// that cancellation and kills the process instead of waiting for stopTimeout.
func TestACPTaskSessionTeardownPropagatesContextCancellation(t *testing.T) {
	proc := newACPTeardownProcess()
	rt := NewACPTaskSessionRuntime(RunnerFunc(func(args []string, env map[string]string, stdoutPath string) (Process, error) {
		return proc, nil
	}))

	// Use a very long StopTimeout so the test would stall if context is ignored.
	session, err := rt.Start(context.Background(), contracts.TaskSessionStartRequest{
		TaskID:      "task-ctx-cancel",
		RepoRoot:    t.TempDir(),
		LogPath:     filepath.Join(t.TempDir(), "acp.jsonl"),
		StopTimeout: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel so Teardown sees a cancelled context

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = session.Teardown(ctx, contracts.TaskSessionTeardown{})
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Teardown blocked instead of respecting cancelled context")
	}

	if got := proc.kills(); got != 1 {
		t.Fatalf("expected Kill called once when context cancelled, got %d calls", got)
	}
}

// TestACPTaskSessionExecuteEmitsLogEventToSink verifies that Execute() emits a
// TaskSessionLogEvent containing the session log path to the EventSink even
// when execution fails early (e.g. cancelled context → WaitReady error).
func TestACPTaskSessionExecuteEmitsLogEventToSink(t *testing.T) {
	proc := newACPTeardownProcess()
	logPath := filepath.Join(t.TempDir(), "runner-logs", "opencode", "test-log-event.jsonl")

	rt := NewACPTaskSessionRuntime(RunnerFunc(func(args []string, env map[string]string, stdoutPath string) (Process, error) {
		return proc, nil
	}))

	session, err := rt.Start(context.Background(), contracts.TaskSessionStartRequest{
		TaskID:   "test-log-event",
		RepoRoot: t.TempDir(),
		LogPath:  logPath,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Teardown(context.Background(), contracts.TaskSessionTeardown{Force: true})
	})

	var mu sync.Mutex
	var receivedEvents []contracts.TaskSessionEvent
	sink := contracts.TaskSessionEventSinkFunc(func(_ context.Context, event contracts.TaskSessionEvent) error {
		mu.Lock()
		receivedEvents = append(receivedEvents, event)
		mu.Unlock()
		return nil
	})

	// Cancel context immediately so WaitReady fails fast.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_ = session.Execute(ctx, contracts.TaskSessionExecuteRequest{
		Prompt:    "do something",
		EventSink: sink,
	})

	mu.Lock()
	defer mu.Unlock()
	found := false
	for _, ev := range receivedEvents {
		if ev.Type == contracts.TaskSessionEventTypeLog && ev.Log != nil && ev.Log.Path == logPath {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected TaskSessionLogEvent with path %q to be emitted via EventSink, got %d events: %#v", logPath, len(receivedEvents), receivedEvents)
	}
}

// TestACPTaskSessionExecuteEmitsArtifactEventToSink verifies that Execute()
// emits a TaskSessionArtifactEvent for the log artifact via the EventSink.
func TestACPTaskSessionExecuteEmitsArtifactEventToSink(t *testing.T) {
	proc := newACPTeardownProcess()
	logPath := filepath.Join(t.TempDir(), "runner-logs", "opencode", "test-artifact.jsonl")

	rt := NewACPTaskSessionRuntime(RunnerFunc(func(args []string, env map[string]string, stdoutPath string) (Process, error) {
		return proc, nil
	}))

	session, err := rt.Start(context.Background(), contracts.TaskSessionStartRequest{
		TaskID:   "test-artifact",
		RepoRoot: t.TempDir(),
		LogPath:  logPath,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Teardown(context.Background(), contracts.TaskSessionTeardown{Force: true})
	})

	var mu sync.Mutex
	var receivedEvents []contracts.TaskSessionEvent
	sink := contracts.TaskSessionEventSinkFunc(func(_ context.Context, event contracts.TaskSessionEvent) error {
		mu.Lock()
		receivedEvents = append(receivedEvents, event)
		mu.Unlock()
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_ = session.Execute(ctx, contracts.TaskSessionExecuteRequest{
		Prompt:    "do something",
		EventSink: sink,
	})

	mu.Lock()
	defer mu.Unlock()
	found := false
	for _, ev := range receivedEvents {
		if ev.Type == contracts.TaskSessionEventTypeArtifact && ev.Artifact != nil && ev.Artifact.Path == logPath {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected TaskSessionArtifactEvent with path %q to be emitted via EventSink, got %d events: %#v", logPath, len(receivedEvents), receivedEvents)
	}
}

// TestACPTaskSessionExecuteWiresEventSinkToACPClient verifies that Execute()
// sets the EventSink from the request onto the internal acpClient so that
// permission events are routed to the caller's sink.
func TestACPTaskSessionExecuteWiresEventSinkToACPClient(t *testing.T) {
	cli := &acpClient{}
	sink := contracts.TaskSessionEventSinkFunc(func(_ context.Context, _ contracts.TaskSessionEvent) error {
		return nil
	})

	session := &ACPTaskSession{
		id:       "wire-test",
		logPath:  "",
		waitDone: make(chan struct{}),
		acpCli:   cli,
	}
	// Mark readyOnce as already done with no error so WaitReady returns immediately.
	session.readyOnce.Do(func() {})

	// Execute will fail (nil connection) but must wire the sink before that.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = session.Execute(ctx, contracts.TaskSessionExecuteRequest{
		Prompt:    "test",
		EventSink: sink,
	})

	if cli.getEventSink() == nil {
		t.Fatal("expected acpClient.eventSink to be wired from TaskSessionExecuteRequest, got nil")
	}
}

// noStdioProcess is a fake process that implements Process but NOT stdioProcess.
// It is used to test the startup fallback path where the process has no pipes.
type noStdioProcess struct {
	mu        sync.Mutex
	killCalls int
	waitCh    chan error
}

func newNoStdioProcess() *noStdioProcess {
	return &noStdioProcess{waitCh: make(chan error, 1)}
}

func (p *noStdioProcess) Wait() error { return <-p.waitCh }
func (p *noStdioProcess) Kill() error {
	p.mu.Lock()
	p.killCalls++
	p.mu.Unlock()
	p.waitCh <- nil
	return nil
}
func (p *noStdioProcess) kills() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.killCalls
}

// TestACPTaskSessionStartKillsProcessWhenNoStdioPipes verifies the startup
// fallback: when the runner returns a process that does not expose stdin/stdout
// pipes, Start() kills the process and returns an error.
func TestACPTaskSessionStartKillsProcessWhenNoStdioPipes(t *testing.T) {
	proc := newNoStdioProcess()
	rt := NewACPTaskSessionRuntime(RunnerFunc(func(args []string, env map[string]string, stdoutPath string) (Process, error) {
		return proc, nil
	}))

	_, err := rt.Start(context.Background(), contracts.TaskSessionStartRequest{
		TaskID:   "task-no-stdio",
		RepoRoot: t.TempDir(),
		LogPath:  t.TempDir() + "/acp.jsonl",
	})
	if err == nil {
		t.Fatal("expected Start to return error when process has no stdio pipes")
	}
	if got := proc.kills(); got != 1 {
		t.Fatalf("expected Kill called once as fallback, got %d calls", got)
	}
}

// TestACPTaskSessionStartPropagatesRunnerError verifies that when the runner
// itself fails, Start() propagates the error to the caller.
func TestACPTaskSessionStartPropagatesRunnerError(t *testing.T) {
	runnerErr := errors.New("runner failed")
	rt := NewACPTaskSessionRuntime(RunnerFunc(func(args []string, env map[string]string, stdoutPath string) (Process, error) {
		return nil, runnerErr
	}))

	_, err := rt.Start(context.Background(), contracts.TaskSessionStartRequest{
		TaskID:   "task-runner-err",
		RepoRoot: t.TempDir(),
		LogPath:  t.TempDir() + "/acp.jsonl",
	})
	if err == nil {
		t.Fatal("expected Start to return error when runner fails")
	}
	if !errors.Is(err, runnerErr) {
		t.Fatalf("expected runner error to be propagated, got: %v", err)
	}
}

// TestACPTaskSessionCancelForceKillsProcess verifies that a forced Cancel
// immediately calls Kill and waits for the process to exit (mirrors the
// equivalent Teardown test for the Cancel path).
func TestACPTaskSessionCancelForceKillsProcess(t *testing.T) {
	proc := newACPTeardownProcess()
	rt := NewACPTaskSessionRuntime(RunnerFunc(func(args []string, env map[string]string, stdoutPath string) (Process, error) {
		return proc, nil
	}))

	session, err := rt.Start(context.Background(), contracts.TaskSessionStartRequest{
		TaskID:   "task-cancel-force",
		RepoRoot: t.TempDir(),
		LogPath:  filepath.Join(t.TempDir(), "acp.jsonl"),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := session.Cancel(context.Background(), contracts.TaskSessionCancellation{Force: true}); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	if got := proc.kills(); got != 1 {
		t.Fatalf("expected Kill called once, got %d calls", got)
	}
}

// TestACPTaskSessionCancelIsIdempotent verifies that calling Cancel more than
// once does not kill the process multiple times (mirrors the equivalent
// Teardown test for the Cancel path).
func TestACPTaskSessionCancelIsIdempotent(t *testing.T) {
	proc := newACPTeardownProcess()
	rt := NewACPTaskSessionRuntime(RunnerFunc(func(args []string, env map[string]string, stdoutPath string) (Process, error) {
		return proc, nil
	}))

	session, err := rt.Start(context.Background(), contracts.TaskSessionStartRequest{
		TaskID:   "task-cancel-idempotent",
		RepoRoot: t.TempDir(),
		LogPath:  filepath.Join(t.TempDir(), "acp.jsonl"),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := session.Cancel(context.Background(), contracts.TaskSessionCancellation{Force: true}); err != nil {
		t.Fatalf("first Cancel: %v", err)
	}
	if err := session.Cancel(context.Background(), contracts.TaskSessionCancellation{Force: true}); err != nil {
		t.Fatalf("second Cancel: %v", err)
	}

	if got := proc.kills(); got != 1 {
		t.Fatalf("expected Kill called once (idempotent via sync.Once), got %d calls", got)
	}
}

// TestACPTaskSessionCancelPropagatesContextCancellation verifies that when
// the context passed to Cancel is already cancelled, the close propagates
// that cancellation and kills the process instead of waiting for stopTimeout
// (mirrors the equivalent Teardown test for the Cancel path).
func TestACPTaskSessionCancelPropagatesContextCancellation(t *testing.T) {
	proc := newACPTeardownProcess()
	rt := NewACPTaskSessionRuntime(RunnerFunc(func(args []string, env map[string]string, stdoutPath string) (Process, error) {
		return proc, nil
	}))

	// Use a very long StopTimeout so the test would stall if context is ignored.
	session, err := rt.Start(context.Background(), contracts.TaskSessionStartRequest{
		TaskID:      "task-cancel-ctx-cancel",
		RepoRoot:    t.TempDir(),
		LogPath:     filepath.Join(t.TempDir(), "acp.jsonl"),
		StopTimeout: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel so Cancel sees a cancelled context

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = session.Cancel(ctx, contracts.TaskSessionCancellation{})
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Cancel blocked instead of respecting cancelled context")
	}

	if got := proc.kills(); got != 1 {
		t.Fatalf("expected Kill called once when context cancelled, got %d calls", got)
	}
}

// TestACPTaskSessionStopTimeoutValueDefaultFallback verifies that when
// StopTimeout is not configured (zero), stopTimeoutValue returns
// defaultACPStopTimeout.
func TestACPTaskSessionStopTimeoutValueDefaultFallback(t *testing.T) {
	session := &ACPTaskSession{stopTimeout: 0}
	if got := session.stopTimeoutValue(); got != defaultACPStopTimeout {
		t.Fatalf("expected default stop timeout %v, got %v", defaultACPStopTimeout, got)
	}
}

// TestACPTaskSessionStopTimeoutValueUsesConfiguredTimeout verifies that when
// StopTimeout is explicitly set, stopTimeoutValue returns that value.
func TestACPTaskSessionStopTimeoutValueUsesConfiguredTimeout(t *testing.T) {
	const configured = 42 * time.Millisecond
	session := &ACPTaskSession{stopTimeout: configured}
	if got := session.stopTimeoutValue(); got != configured {
		t.Fatalf("expected configured timeout %v, got %v", configured, got)
	}
}

// TestACPTaskSessionTeardownIsIdempotent verifies that calling Teardown more
// than once does not kill the process multiple times.
func TestACPTaskSessionTeardownIsIdempotent(t *testing.T) {
	proc := newACPTeardownProcess()
	rt := NewACPTaskSessionRuntime(RunnerFunc(func(args []string, env map[string]string, stdoutPath string) (Process, error) {
		return proc, nil
	}))

	session, err := rt.Start(context.Background(), contracts.TaskSessionStartRequest{
		TaskID:   "task-idempotent",
		RepoRoot: t.TempDir(),
		LogPath:  filepath.Join(t.TempDir(), "acp.jsonl"),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := session.Teardown(context.Background(), contracts.TaskSessionTeardown{Force: true}); err != nil {
		t.Fatalf("first Teardown: %v", err)
	}
	if err := session.Teardown(context.Background(), contracts.TaskSessionTeardown{Force: true}); err != nil {
		t.Fatalf("second Teardown: %v", err)
	}

	if got := proc.kills(); got != 1 {
		t.Fatalf("expected Kill called once (idempotent via sync.Once), got %d calls", got)
	}
}
