package claude

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

// T1: compile-time conformance — stdinProcess interface satisfied by *osStdinProcess
var _ stdinProcess = (*osStdinProcess)(nil)

func TestOsStdinProcess_Compile(t *testing.T) {
	// If the file compiles, the interface is satisfied.
	t.Log("osStdinProcess satisfies stdinProcess interface")
}

// T9: Execute returns nil on success result, error on error result.
func TestStdinTaskSession_Execute_SuccessResult(t *testing.T) {
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	go io.Copy(io.Discard, stdinR) //nolint:errcheck
	sess := newTestSession(stdinW, stdoutR)
	go func() {
		_, _ = fmt.Fprintln(stdoutW, `{"type":"system","subtype":"init"}`)
		_, _ = fmt.Fprintln(stdoutW, `{"type":"result","subtype":"success","result":"done"}`)
		_ = stdoutW.Close()
	}()
	if err := sess.Execute(t.Context(), contracts.TaskSessionExecuteRequest{Prompt: "p"}); err != nil {
		t.Fatalf("Execute() = %v; want nil", err)
	}
}

func TestStdinTaskSession_Execute_ErrorResult(t *testing.T) {
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	go io.Copy(io.Discard, stdinR) //nolint:errcheck
	sess := newTestSession(stdinW, stdoutR)
	go func() {
		_, _ = fmt.Fprintln(stdoutW, `{"type":"system","subtype":"init"}`)
		_, _ = fmt.Fprintln(stdoutW, `{"type":"result","subtype":"error_during_execution","error":"boom"}`)
		_ = stdoutW.Close()
	}()
	err := sess.Execute(t.Context(), contracts.TaskSessionExecuteRequest{Prompt: "p"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error %q does not contain 'boom'", err.Error())
	}
}

// T10: Execute emits output events to EventSink on assistant messages.
func TestStdinTaskSession_Execute_EmitsOutputEvent(t *testing.T) {
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	go io.Copy(io.Discard, stdinR) //nolint:errcheck
	sess := newTestSession(stdinW, stdoutR)
	go func() {
		_, _ = fmt.Fprintln(stdoutW, `{"type":"system","subtype":"init"}`)
		_, _ = fmt.Fprintln(stdoutW, `{"type":"assistant","message":{"content":[{"type":"text","text":"hello"}]}}`)
		_, _ = fmt.Fprintln(stdoutW, `{"type":"result","subtype":"success"}`)
		_ = stdoutW.Close()
	}()

	var events []contracts.TaskSessionEvent
	sink := contracts.TaskSessionEventSinkFunc(func(_ context.Context, e contracts.TaskSessionEvent) error {
		events = append(events, e)
		return nil
	})
	if err := sess.Execute(t.Context(), contracts.TaskSessionExecuteRequest{Prompt: "p", EventSink: sink}); err != nil {
		t.Fatalf("Execute() = %v; want nil", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events; want 1", len(events))
	}
	if events[0].Type != contracts.TaskSessionEventTypeOutput {
		t.Errorf("event type = %q; want output", events[0].Type)
	}
	if events[0].Message != "hello" {
		t.Errorf("event message = %q; want 'hello'", events[0].Message)
	}
}

// T11: Execute emits approval event on tool_use (no stdin write — permissions bypassed).
func TestStdinTaskSession_Execute_ApprovesToolUse(t *testing.T) {
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	go io.Copy(io.Discard, stdinR) //nolint:errcheck
	sess := newTestSession(stdinW, stdoutR)

	go func() {
		_, _ = fmt.Fprintln(stdoutW, `{"type":"system","subtype":"init"}`)
		_, _ = fmt.Fprintln(stdoutW, `{"type":"tool_use","id":"t1","name":"bash"}`)
		_, _ = fmt.Fprintln(stdoutW, `{"type":"result","subtype":"success"}`)
		_ = stdoutW.Close()
	}()

	var events []contracts.TaskSessionEvent
	sink := contracts.TaskSessionEventSinkFunc(func(_ context.Context, e contracts.TaskSessionEvent) error {
		events = append(events, e)
		return nil
	})
	if err := sess.Execute(t.Context(), contracts.TaskSessionExecuteRequest{Prompt: "p", EventSink: sink}); err != nil {
		t.Fatalf("Execute() = %v; want nil", err)
	}

	var approvalEvents []contracts.TaskSessionEvent
	for _, e := range events {
		if e.Type == contracts.TaskSessionEventTypeApprovalRequired {
			approvalEvents = append(approvalEvents, e)
		}
	}
	if len(approvalEvents) != 1 {
		t.Fatalf("got %d approval events; want 1", len(approvalEvents))
	}
}

// T12: Cancel sends Stop; Teardown force kills.
func TestStdinTaskSession_Cancel_StopsProcess(t *testing.T) {
	cmd := exec.Command("cat") // blocks waiting for stdin
	if err := cmd.Start(); err != nil {
		t.Fatalf("cmd.Start: %v", err)
	}
	waitDone := make(chan struct{})
	proc := &osStdinProcess{cmd: cmd, waitDone: waitDone}
	sess := &StdinTaskSession{
		id:       "test",
		proc:     proc,
		waitDone: waitDone,
	}
	go proc.Wait() //nolint:errcheck
	if err := sess.Cancel(t.Context(), contracts.TaskSessionCancellation{Force: false}); err != nil {
		t.Errorf("Cancel() = %v; want nil", err)
	}
}

func TestStdinTaskSession_Teardown_Force(t *testing.T) {
	_, stdinW := io.Pipe()
	stdoutR, _ := io.Pipe()
	cmd := exec.Command("cat")
	if err := cmd.Start(); err != nil {
		t.Fatalf("cmd.Start: %v", err)
	}
	waitDone := make(chan struct{})
	proc := &osStdinProcess{cmd: cmd, stdin: stdinW, stdout: stdoutR, waitDone: waitDone}
	sess := &StdinTaskSession{id: "test", proc: proc, waitDone: waitDone}
	go proc.Wait() //nolint:errcheck
	if err := sess.Teardown(t.Context(), contracts.TaskSessionTeardown{Force: true}); err != nil {
		t.Errorf("Teardown(force=true) = %v; want nil", err)
	}
}

// T13: resolveStdinLogPath returns correct paths for all cases.
func TestResolveStdinLogPath(t *testing.T) {
	tests := []struct {
		name     string
		request  contracts.TaskSessionStartRequest
		wantSuff string
	}{
		{
			name:     "explicit log path",
			request:  contracts.TaskSessionStartRequest{LogPath: "/tmp/custom.jsonl"},
			wantSuff: "/tmp/custom.jsonl",
		},
		{
			name:     "taskID and repoRoot",
			request:  contracts.TaskSessionStartRequest{TaskID: "t1", RepoRoot: "/repo"},
			wantSuff: "/repo/runner-logs/claude/t1.jsonl",
		},
		{
			name:     "taskID only",
			request:  contracts.TaskSessionStartRequest{TaskID: "t2"},
			wantSuff: "runner-logs/claude/t2.jsonl",
		},
		{
			name:     "empty",
			request:  contracts.TaskSessionStartRequest{},
			wantSuff: "runner-logs/claude/claude-stdin.jsonl",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveStdinLogPath(tc.request)
			if got != tc.wantSuff {
				t.Errorf("resolveStdinLogPath() = %q; want %q", got, tc.wantSuff)
			}
		})
	}
}

func newTestSession(stdinW io.WriteCloser, stdoutR io.ReadCloser) *StdinTaskSession {
	waitDone := make(chan struct{})
	proc := &osStdinProcess{
		stdin:    stdinW,
		stdout:   stdoutR,
		waitDone: waitDone,
	}
	return &StdinTaskSession{
		id:       "test",
		proc:     proc,
		waitDone: waitDone,
	}
}

// T7: WaitReady returns a useful error when the process exits before init.
func TestStdinTaskSession_WaitReady_ProcessExitsEarly(t *testing.T) {
	pr, pw := io.Pipe()
	waitDone := make(chan struct{})
	sess := &StdinTaskSession{
		id:       "test",
		waitDone: waitDone,
		proc: &osStdinProcess{
			stdout:   pr,
			waitDone: make(chan struct{}),
		},
	}
	// Close write end immediately and signal process done.
	_ = pw.Close()
	close(waitDone)

	err := sess.WaitReady(t.Context())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "exited before ready") {
		t.Errorf("error %q does not contain 'exited before ready'", err.Error())
	}
}

// WaitReady returns nil when the process is alive (non-blocking alive check).
func TestStdinTaskSession_WaitReady_ProcessAlive(t *testing.T) {
	sess := &StdinTaskSession{
		id:       "test",
		waitDone: make(chan struct{}), // never closed → process alive
		proc:     &osStdinProcess{waitDone: make(chan struct{})},
	}
	if err := sess.WaitReady(t.Context()); err != nil {
		t.Fatalf("WaitReady() = %v; want nil for alive process", err)
	}
}

// T5: NewTaskSessionRuntime defaults binary to "claude"; Start returns non-nil session.
func TestTaskSessionRuntime_DefaultBinary(t *testing.T) {
	rt := NewTaskSessionRuntime("")
	if rt.binary != "claude" {
		t.Errorf("binary = %q; want %q", rt.binary, "claude")
	}
}

var _ contracts.TaskSessionRuntime = (*TaskSessionRuntime)(nil)

// T4: startStdinProcess rejects empty binary and spawns a real process.
func TestStartStdinProcess_BinaryRequired(t *testing.T) {
	_, err := startStdinProcess(t.Context(), StdinProcessSpec{Binary: ""})
	if err == nil {
		t.Fatal("expected error for empty binary, got nil")
	}
}

func TestStartStdinProcess_SpawnsProcess(t *testing.T) {
	proc, err := startStdinProcess(t.Context(), StdinProcessSpec{Binary: "cat"})
	if err != nil {
		t.Fatalf("startStdinProcess: %v", err)
	}
	if proc.Stdin() == nil {
		t.Error("Stdin() is nil")
	}
	if proc.Stdout() == nil {
		t.Error("Stdout() is nil")
	}
	_ = proc.Kill()
	_ = proc.Wait()
}

// T3: Stop/Kill do not error on an already-exited process.
func TestOsStdinProcess_Stop_NoErrorWhenAlreadyDone(t *testing.T) {
	cmd := exec.Command("true")
	if err := cmd.Start(); err != nil {
		t.Fatalf("cmd.Start: %v", err)
	}
	p := &osStdinProcess{cmd: cmd, waitDone: make(chan struct{})}
	_ = p.Wait() // let it exit
	if err := p.Stop(); err != nil {
		t.Errorf("Stop() = %v; want nil", err)
	}
	if err := p.Kill(); err != nil {
		t.Errorf("Kill() = %v; want nil", err)
	}
}

// buildClaudeArgs must include required flags and the prompt as the last arg.
// Passing the prompt as a CLI argument avoids the stdin-before-init-event
// deadlock that occurs in --print mode when reading from stdin.
func TestBuildClaudeArgs_RequiredFlagsAndPrompt(t *testing.T) {
	prompt := "do the thing"
	args := buildClaudeArgs("claude-test-model", prompt)
	for _, required := range []string{"--print", "--output-format", "stream-json", "--dangerously-skip-permissions"} {
		if !slices.Contains(args, required) {
			t.Errorf("buildClaudeArgs missing %q; got %v", required, args)
		}
	}
	idx := slices.Index(args, "--model")
	if idx == -1 || idx+1 >= len(args) || args[idx+1] != "claude-test-model" {
		t.Errorf("expected --model claude-test-model in args; got %v", args)
	}
	if args[len(args)-1] != prompt {
		t.Errorf("prompt not last arg; got %v", args)
	}
}

func TestBuildClaudeArgs_NoModelFlag(t *testing.T) {
	args := buildClaudeArgs("", "hello")
	if slices.Contains(args, "--model") {
		t.Errorf("expected no --model flag for empty model; got %v", args)
	}
	if args[len(args)-1] != "hello" {
		t.Errorf("prompt not last arg; got %v", args)
	}
}

// Execute must close stdin so claude does not block waiting for more input.
func TestStdinTaskSession_Execute_CloseStdinAfterPrompt(t *testing.T) {
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	sess := newTestSession(stdinW, stdoutR)

	stdinClosed := make(chan struct{})
	go func() {
		defer close(stdinClosed)
		io.Copy(io.Discard, stdinR) //nolint:errcheck
	}()

	go func() {
		_, _ = fmt.Fprintln(stdoutW, `{"type":"result","subtype":"success"}`)
		_ = stdoutW.Close()
	}()

	if err := sess.Execute(t.Context(), contracts.TaskSessionExecuteRequest{Prompt: "p"}); err != nil {
		t.Fatalf("Execute() = %v; want nil", err)
	}

	// stdinClosed should be closed because Execute() closed the write end.
	select {
	case <-stdinClosed:
		// good — EOF was signalled
	case <-time.After(time.Second):
		t.Error("stdin was not closed after Execute(); claude would block waiting for more input")
	}
}

// TestStdinTaskSession_Execute_HandlesLineLargerThanDefaultBuffer is a regression
// test for "bufio.Scanner: token too long": an assistant output line larger than
// the default scanner buffer (64 KiB) must not cause Execute to fail.
func TestStdinTaskSession_Execute_HandlesLineLargerThanDefaultBuffer(t *testing.T) {
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	go io.Copy(io.Discard, stdinR) //nolint:errcheck
	sess := newTestSession(stdinW, stdoutR)

	go func() {
		// Build an assistant message line well over 64 KiB.
		longText := strings.Repeat("a", 100*1024)
		bigLine := fmt.Sprintf(`{"type":"assistant","message":{"content":[{"type":"text","text":"%s"}]}}`, longText)
		_, _ = fmt.Fprintln(stdoutW, bigLine)
		_, _ = fmt.Fprintln(stdoutW, `{"type":"result","subtype":"success"}`)
		_ = stdoutW.Close()
	}()

	if err := sess.Execute(t.Context(), contracts.TaskSessionExecuteRequest{Prompt: "p"}); err != nil {
		t.Fatalf("Execute() = %v; want nil (scanner buffer may be too small for large lines)", err)
	}
}

// TestStdinTaskSession_Execute_HandlesLargeToolUsePermissionEvent is a regression
// test for "bufio.Scanner: token too long": a tool_use (permission) event whose
// JSON line exceeds 64 KiB must be handled without error.
func TestStdinTaskSession_Execute_HandlesLargeToolUsePermissionEvent(t *testing.T) {
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	go io.Copy(io.Discard, stdinR) //nolint:errcheck
	sess := newTestSession(stdinW, stdoutR)

	go func() {
		// Build a tool_use line well over 64 KiB.
		longName := strings.Repeat("b", 100*1024)
		bigLine := fmt.Sprintf(`{"type":"tool_use","id":"t1","name":"%s"}`, longName)
		_, _ = fmt.Fprintln(stdoutW, bigLine)
		_, _ = fmt.Fprintln(stdoutW, `{"type":"result","subtype":"success"}`)
		_ = stdoutW.Close()
	}()

	if err := sess.Execute(t.Context(), contracts.TaskSessionExecuteRequest{Prompt: "p"}); err != nil {
		t.Fatalf("Execute() = %v; want nil (large tool_use/permission event must not break scanner)", err)
	}
}

// T2: Wait() is idempotent and returns the correct exit error.
func TestOsStdinProcess_Wait_ReturnsOnExit(t *testing.T) {
	cmd := exec.Command("true") // exits immediately with code 0
	cmd.Stdin = nil
	if err := cmd.Start(); err != nil {
		t.Fatalf("cmd.Start: %v", err)
	}
	p := &osStdinProcess{cmd: cmd, waitDone: make(chan struct{})}
	if err := p.Wait(); err != nil {
		t.Fatalf("Wait() = %v; want nil", err)
	}
	// idempotent
	if err := p.Wait(); err != nil {
		t.Fatalf("Wait() second call = %v; want nil", err)
	}
}
