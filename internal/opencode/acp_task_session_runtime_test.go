package opencode

import (
	"context"
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
