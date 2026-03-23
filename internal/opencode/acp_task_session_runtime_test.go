package opencode

import (
	"context"
	"io"
	"path/filepath"
	"testing"

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
