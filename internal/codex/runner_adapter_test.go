package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

type fakeAppServerProcess struct {
	stdin   io.WriteCloser
	stdout  io.ReadCloser
	stderr  io.ReadCloser
	waitCh  chan error
	killFn  func() error
	waitFn  func() error
	waitErr error
}

func (p *fakeAppServerProcess) Stdin() io.WriteCloser { return p.stdin }
func (p *fakeAppServerProcess) Stdout() io.ReadCloser { return p.stdout }
func (p *fakeAppServerProcess) Stderr() io.ReadCloser { return p.stderr }
func (p *fakeAppServerProcess) Wait() error {
	if p.waitFn != nil {
		return p.waitFn()
	}
	return <-p.waitCh
}
func (p *fakeAppServerProcess) Kill() error {
	if p.killFn != nil {
		return p.killFn()
	}
	return nil
}

func TestNormalizeAppServerNotificationMapsLifecycleItemApprovalAndProgress(t *testing.T) {
	tests := []struct {
		name         string
		message      contracts.JSONRPCMessage
		mode         contracts.RunnerMode
		wantType     contracts.TaskSessionEventType
		wantProgress string
		assert       func(t *testing.T, event contracts.TaskSessionEvent, progress contracts.RunnerProgress)
	}{
		{
			name: "thread started",
			message: contracts.JSONRPCMessage{
				Method: "thread/started",
				Params: map[string]any{
					"threadId": "thread-1",
				},
			},
			mode:         contracts.RunnerModeImplement,
			wantType:     contracts.TaskSessionEventTypeLifecycle,
			wantProgress: string(contracts.EventTypeRunnerProgress),
			assert: func(t *testing.T, event contracts.TaskSessionEvent, progress contracts.RunnerProgress) {
				t.Helper()
				if event.SessionID != "thread-1" {
					t.Fatalf("expected session id thread-1, got %q", event.SessionID)
				}
				if event.Lifecycle == nil || event.Lifecycle.State != contracts.TaskSessionLifecycleReady {
					t.Fatalf("expected ready lifecycle, got %#v", event.Lifecycle)
				}
				if progress.Metadata["thread_id"] != "thread-1" {
					t.Fatalf("expected thread_id metadata, got %#v", progress.Metadata)
				}
			},
		},
		{
			name: "turn started",
			message: contracts.JSONRPCMessage{
				Method: "turn/started",
				Params: map[string]any{
					"threadId": "thread-1",
					"turnId":   "turn-2",
				},
			},
			mode:         contracts.RunnerModeImplement,
			wantType:     contracts.TaskSessionEventTypeLifecycle,
			wantProgress: string(contracts.EventTypeRunnerProgress),
			assert: func(t *testing.T, event contracts.TaskSessionEvent, progress contracts.RunnerProgress) {
				t.Helper()
				if event.Lifecycle == nil || event.Lifecycle.State != contracts.TaskSessionLifecycleRunning {
					t.Fatalf("expected running lifecycle, got %#v", event.Lifecycle)
				}
				if progress.Metadata["turn_id"] != "turn-2" {
					t.Fatalf("expected turn_id metadata, got %#v", progress.Metadata)
				}
			},
		},
		{
			name: "item started",
			message: contracts.JSONRPCMessage{
				Method: "item/started",
				Params: map[string]any{
					"threadId": "thread-1",
					"turnId":   "turn-2",
					"item": map[string]any{
						"id":    "item-3",
						"type":  "command_execution",
						"title": "Run tests",
					},
				},
			},
			mode:         contracts.RunnerModeImplement,
			wantType:     contracts.TaskSessionEventTypeProgress,
			wantProgress: string(contracts.EventTypeRunnerProgress),
			assert: func(t *testing.T, event contracts.TaskSessionEvent, progress contracts.RunnerProgress) {
				t.Helper()
				if event.Progress == nil || event.Progress.Phase != "command_execution" {
					t.Fatalf("expected command_execution phase, got %#v", event.Progress)
				}
				if progress.Metadata["item_id"] != "item-3" || progress.Metadata["item_type"] != "command_execution" {
					t.Fatalf("expected item metadata, got %#v", progress.Metadata)
				}
				if progress.Message != "Run tests" {
					t.Fatalf("expected item title message, got %q", progress.Message)
				}
			},
		},
		{
			name: "approval request",
			message: contracts.JSONRPCMessage{
				Method: "item/commandExecution/requestApproval",
				Params: map[string]any{
					"threadId": "thread-1",
					"turnId":   "turn-2",
					"itemId":   "item-4",
					"id":       "approval-5",
					"title":    "Run pnpm test",
					"reason":   "run tests",
					"command":  []any{"pnpm", "test"},
				},
			},
			mode:         contracts.RunnerModeImplement,
			wantType:     contracts.TaskSessionEventTypeApprovalRequired,
			wantProgress: string(contracts.EventTypeRunnerWarning),
			assert: func(t *testing.T, event contracts.TaskSessionEvent, progress contracts.RunnerProgress) {
				t.Helper()
				if event.Approval == nil {
					t.Fatalf("expected approval event")
				}
				if event.Approval.Request.Kind != contracts.TaskSessionApprovalKindCommand {
					t.Fatalf("expected command approval kind, got %q", event.Approval.Request.Kind)
				}
				if !reflect.DeepEqual(event.Approval.Request.Command, []string{"pnpm", "test"}) {
					t.Fatalf("unexpected approval command %#v", event.Approval.Request.Command)
				}
				if progress.Metadata["approval_id"] != "approval-5" {
					t.Fatalf("expected approval_id metadata, got %#v", progress.Metadata)
				}
			},
		},
		{
			name: "item delta",
			message: contracts.JSONRPCMessage{
				Method: "item/agentMessage/delta",
				Params: map[string]any{
					"threadId": "thread-1",
					"turnId":   "turn-2",
					"itemId":   "item-6",
					"delta":    "all tests passed",
				},
			},
			mode:         contracts.RunnerModeImplement,
			wantType:     contracts.TaskSessionEventTypeOutput,
			wantProgress: string(contracts.EventTypeRunnerOutput),
			assert: func(t *testing.T, event contracts.TaskSessionEvent, progress contracts.RunnerProgress) {
				t.Helper()
				if progress.Message != "all tests passed" {
					t.Fatalf("expected delta message, got %q", progress.Message)
				}
				if progress.Metadata["item_id"] != "item-6" {
					t.Fatalf("expected item_id metadata, got %#v", progress.Metadata)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event, completion, ok := NormalizeAppServerNotification(tt.message, tt.mode)
			if !ok {
				t.Fatalf("expected notification to normalize")
			}
			if completion != nil {
				t.Fatalf("did not expect completion for %s, got %#v", tt.message.Method, completion)
			}
			if event.Type != tt.wantType {
				t.Fatalf("expected event type %q, got %q", tt.wantType, event.Type)
			}
			progress, completion, ok := RunnerProgressFromAppServerNotification(tt.message, tt.mode)
			if !ok {
				t.Fatalf("expected progress to normalize")
			}
			if completion != nil {
				t.Fatalf("did not expect completion in progress path for %s, got %#v", tt.message.Method, completion)
			}
			if progress.Type != tt.wantProgress {
				t.Fatalf("expected progress type %q, got %q", tt.wantProgress, progress.Type)
			}
			tt.assert(t, event, progress)
		})
	}
}

func TestAppServerTaskSessionRuntimeStartInitializesSessionWithoutStartingTurn(t *testing.T) {
	repoRoot := t.TempDir()
	harness := contracts.NewFakeStdioJSONRPCHarness()
	clientWriter, clientReader := harness.ClientIO()
	serverWriter, serverReader := harness.ServerIO()

	waitCh := make(chan error, 1)
	proc := &fakeAppServerProcess{
		stdin:  clientWriter,
		stdout: clientReader,
		stderr: io.NopCloser(strings.NewReader("")),
		waitCh: waitCh,
	}
	proc.killFn = func() error {
		_ = serverWriter.Close()
		_ = serverReader.Close()
		waitCh <- nil
		return harness.Close()
	}

	var gotSpec CommandSpec
	runtime := NewTaskSessionRuntime("codex-bin")
	runtime.starter = appServerStarterFunc(func(_ context.Context, spec CommandSpec) (appServerProcess, error) {
		gotSpec = spec
		return proc, nil
	})

	serverDone := make(chan error, 1)
	noFollowUpChecked := make(chan struct{})
	go func() {
		defer close(serverDone)

		msg, err := harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "initialize" {
			serverDone <- errors.New("expected initialize request")
			return
		}
		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result: map[string]any{
				"protocolVersion": 2,
				"capabilities":    map[string]any{"experimentalApi": true},
			},
		}); err != nil {
			serverDone <- err
			return
		}

		msg, err = harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "initialized" {
			serverDone <- errors.New("expected initialized notification")
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		defer cancel()
		msg, err = harness.ReadMessage(ctx)
		if err == nil {
			serverDone <- errors.New("expected no follow-up request before execute")
			return
		}
		if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, io.EOF) {
			serverDone <- err
			return
		}
		close(noFollowUpChecked)

		msg, err = harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "shutdown" {
			serverDone <- errors.New("expected shutdown request during teardown")
			return
		}
		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  map[string]any{},
		}); err != nil {
			serverDone <- err
			return
		}
		_ = serverWriter.Close()
		_ = serverReader.Close()
		waitCh <- nil
		serverDone <- harness.Close()
	}()

	session, err := runtime.Start(context.Background(), contracts.TaskSessionStartRequest{
		TaskID:   "task-1",
		Backend:  "codex",
		RepoRoot: repoRoot,
	})
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	appSession, ok := session.(*AppServerTaskSession)
	if !ok {
		t.Fatalf("expected AppServerTaskSession, got %T", session)
	}
	if appSession.ID() != "task-1" {
		t.Fatalf("expected task session id task-1, got %q", appSession.ID())
	}
	if err := appSession.WaitReady(context.Background()); err != nil {
		t.Fatalf("wait ready: %v", err)
	}

	if gotSpec.Binary != "codex-bin" {
		t.Fatalf("expected codex-bin binary, got %q", gotSpec.Binary)
	}
	if !reflect.DeepEqual(gotSpec.Args, []string{"app-server"}) {
		t.Fatalf("expected app-server args, got %#v", gotSpec.Args)
	}
	if gotSpec.Dir != repoRoot {
		t.Fatalf("expected repo root dir %q, got %q", repoRoot, gotSpec.Dir)
	}
	select {
	case <-noFollowUpChecked:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting to confirm no pre-execute follow-up")
	}

	if err := appSession.Teardown(context.Background(), contracts.TaskSessionTeardown{Reason: "done"}); err != nil {
		t.Fatalf("teardown session: %v", err)
	}

	if err := <-serverDone; err != nil {
		t.Fatalf("server interaction failed: %v", err)
	}
}

func TestAppServerTaskSessionExecuteStartsEphemeralThreadAndCapturesTurnCompletion(t *testing.T) {
	repoRoot := t.TempDir()
	harness := contracts.NewFakeStdioJSONRPCHarness()
	clientWriter, clientReader := harness.ClientIO()
	serverWriter, serverReader := harness.ServerIO()

	waitCh := make(chan error, 1)
	proc := &fakeAppServerProcess{
		stdin:  clientWriter,
		stdout: clientReader,
		stderr: io.NopCloser(strings.NewReader("")),
		waitCh: waitCh,
	}
	proc.killFn = func() error {
		_ = serverWriter.Close()
		_ = serverReader.Close()
		waitCh <- nil
		return harness.Close()
	}

	runtime := NewTaskSessionRuntime("codex-bin")
	runtime.starter = appServerStarterFunc(func(_ context.Context, spec CommandSpec) (appServerProcess, error) {
		return proc, nil
	})

	serverDone := make(chan error, 1)
	go func() {
		defer close(serverDone)

		msg, err := harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "initialize" {
			serverDone <- errors.New("expected initialize request")
			return
		}
		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  map[string]any{"protocolVersion": 2},
		}); err != nil {
			serverDone <- err
			return
		}

		msg, err = harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "initialized" {
			serverDone <- errors.New("expected initialized notification")
			return
		}

		msg, err = harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "thread/start" {
			serverDone <- errors.New("expected thread/start request")
			return
		}
		if msg.Params["ephemeral"] != true {
			serverDone <- errors.New("expected ephemeral thread")
			return
		}
		if msg.Params["cwd"] != repoRoot {
			serverDone <- errors.New("expected thread/start cwd to match repo root")
			return
		}
		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  map[string]any{"thread": map[string]any{"id": "thread-1"}},
		}); err != nil {
			serverDone <- err
			return
		}
		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			Method:  "thread/started",
			Params:  map[string]any{"threadId": "thread-1"},
		}); err != nil {
			serverDone <- err
			return
		}

		msg, err = harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "turn/start" {
			serverDone <- errors.New("expected turn/start request")
			return
		}
		if msg.Params["threadId"] != "thread-1" {
			serverDone <- errors.New("expected turn/start thread id")
			return
		}
		input, ok := msg.Params["input"].([]any)
		if !ok || len(input) != 1 {
			serverDone <- errors.New("expected one turn/start input item")
			return
		}
		firstInput, ok := input[0].(map[string]any)
		if !ok || firstInput["text"] != "implement the task" {
			serverDone <- errors.New("expected prompt text in turn/start input")
			return
		}
		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  map[string]any{"turn": map[string]any{"id": "turn-1"}},
		}); err != nil {
			serverDone <- err
			return
		}
		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			Method:  "turn/started",
			Params:  map[string]any{"threadId": "thread-1", "turnId": "turn-1"},
		}); err != nil {
			serverDone <- err
			return
		}
		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			Method:  "turn/completed",
			Params: map[string]any{
				"threadId":   "thread-1",
				"turnId":     "turn-1",
				"stopReason": "end_turn",
				"output": map[string]any{
					"text": "task complete",
					"artifacts": []map[string]any{
						{
							"type": "file",
							"path": "artifacts/summary.txt",
						},
					},
				},
			},
		}); err != nil {
			serverDone <- err
			return
		}

		serverDone <- nil
	}()

	session, err := runtime.Start(context.Background(), contracts.TaskSessionStartRequest{
		TaskID:   "task-1",
		Backend:  "codex",
		RepoRoot: repoRoot,
	})
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	appSession, ok := session.(*AppServerTaskSession)
	if !ok {
		t.Fatalf("expected AppServerTaskSession, got %T", session)
	}

	events := []contracts.TaskSessionEvent{}
	err = appSession.Execute(context.Background(), contracts.TaskSessionExecuteRequest{
		Prompt: "implement the task",
		Model:  "openai/gpt-5.3-codex",
		Mode:   contracts.RunnerModeImplement,
		EventSink: contracts.TaskSessionEventSinkFunc(func(_ context.Context, event contracts.TaskSessionEvent) error {
			events = append(events, event)
			return nil
		}),
	})
	if err != nil {
		t.Fatalf("execute session: %v", err)
	}

	if len(events) != 3 {
		t.Fatalf("expected three events, got %#v", events)
	}
	if events[0].Type != contracts.TaskSessionEventTypeLifecycle || events[0].Lifecycle == nil || events[0].Lifecycle.State != contracts.TaskSessionLifecycleReady {
		t.Fatalf("expected thread started lifecycle event, got %#v", events[0])
	}
	if events[1].Type != contracts.TaskSessionEventTypeLifecycle || events[1].Lifecycle == nil || events[1].Lifecycle.State != contracts.TaskSessionLifecycleRunning {
		t.Fatalf("expected turn started lifecycle event, got %#v", events[1])
	}
	if events[2].Type != contracts.TaskSessionEventTypeLifecycle || events[2].Lifecycle == nil || events[2].Lifecycle.State != contracts.TaskSessionLifecycleStopped {
		t.Fatalf("expected stopped lifecycle event, got %#v", events[2])
	}
	if events[2].Metadata["reason"] != "end_turn" {
		t.Fatalf("expected completion reason metadata, got %#v", events[2].Metadata)
	}
	rawCompletion := events[2].Metadata["completion_json"]
	if !strings.Contains(rawCompletion, "\"text\":\"task complete\"") {
		t.Fatalf("expected completion_json to include output text, got %q", rawCompletion)
	}
	if !strings.Contains(rawCompletion, "\"path\":\"artifacts/summary.txt\"") {
		t.Fatalf("expected completion_json to include artifact path, got %q", rawCompletion)
	}
	if err := appSession.Teardown(context.Background(), contracts.TaskSessionTeardown{Reason: "done"}); err != nil {
		t.Fatalf("teardown session: %v", err)
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("server interaction failed: %v", err)
	}
}

func TestAppServerTaskSessionExecuteHonorsCompletionBeforeTurnStartResponse(t *testing.T) {
	repoRoot := t.TempDir()
	harness := contracts.NewFakeStdioJSONRPCHarness()
	clientWriter, clientReader := harness.ClientIO()
	serverWriter, serverReader := harness.ServerIO()

	waitCh := make(chan error, 1)
	proc := &fakeAppServerProcess{
		stdin:  clientWriter,
		stdout: clientReader,
		stderr: io.NopCloser(strings.NewReader("")),
		waitCh: waitCh,
	}
	proc.killFn = func() error {
		_ = serverWriter.Close()
		_ = serverReader.Close()
		waitCh <- nil
		return harness.Close()
	}

	runtime := NewTaskSessionRuntime("codex-bin")
	runtime.starter = appServerStarterFunc(func(_ context.Context, spec CommandSpec) (appServerProcess, error) {
		return proc, nil
	})

	serverDone := make(chan error, 1)
	go func() {
		defer close(serverDone)

		msg, err := harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "initialize" {
			serverDone <- errors.New("expected initialize request")
			return
		}
		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  map[string]any{"protocolVersion": 2},
		}); err != nil {
			serverDone <- err
			return
		}

		msg, err = harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "initialized" {
			serverDone <- errors.New("expected initialized notification")
			return
		}

		msg, err = harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "thread/start" {
			serverDone <- errors.New("expected thread/start request")
			return
		}
		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  map[string]any{"thread": map[string]any{"id": "thread-1"}},
		}); err != nil {
			serverDone <- err
			return
		}

		msg, err = harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "turn/start" {
			serverDone <- errors.New("expected turn/start request")
			return
		}
		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			Method:  "turn/completed",
			Params: map[string]any{
				"threadId":   "thread-1",
				"turnId":     "turn-1",
				"stopReason": "end_turn",
			},
		}); err != nil {
			serverDone <- err
			return
		}
		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  map[string]any{"turn": map[string]any{"id": "turn-1"}},
		}); err != nil {
			serverDone <- err
			return
		}

		serverDone <- nil
	}()

	session, err := runtime.Start(context.Background(), contracts.TaskSessionStartRequest{
		TaskID:   "task-early-complete",
		Backend:  "codex",
		RepoRoot: repoRoot,
	})
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	appSession, ok := session.(*AppServerTaskSession)
	if !ok {
		t.Fatalf("expected AppServerTaskSession, got %T", session)
	}

	events := []contracts.TaskSessionEvent{}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err = appSession.Execute(ctx, contracts.TaskSessionExecuteRequest{
		Prompt: "implement the task",
		Model:  "openai/gpt-5.3-codex",
		Mode:   contracts.RunnerModeImplement,
		EventSink: contracts.TaskSessionEventSinkFunc(func(_ context.Context, event contracts.TaskSessionEvent) error {
			events = append(events, event)
			return nil
		}),
	})
	if err != nil {
		t.Fatalf("execute session: %v", err)
	}

	if len(events) != 1 {
		t.Fatalf("expected one completion event, got %#v", events)
	}
	if events[0].Lifecycle == nil || events[0].Lifecycle.State != contracts.TaskSessionLifecycleStopped {
		t.Fatalf("expected stopped lifecycle event, got %#v", events[0])
	}
	if events[0].Metadata["reason"] != "end_turn" {
		t.Fatalf("expected completion reason metadata, got %#v", events[0].Metadata)
	}
	if err := appSession.Teardown(context.Background(), contracts.TaskSessionTeardown{Reason: "done"}); err != nil {
		t.Fatalf("teardown session: %v", err)
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("server interaction failed: %v", err)
	}
}

func TestAppServerTaskSessionCancelInterruptsRunningTurn(t *testing.T) {
	repoRoot := t.TempDir()
	harness := contracts.NewFakeStdioJSONRPCHarness()
	clientWriter, clientReader := harness.ClientIO()
	serverWriter, serverReader := harness.ServerIO()

	waitCh := make(chan error, 1)
	var killCalls int32
	proc := &fakeAppServerProcess{
		stdin:  clientWriter,
		stdout: clientReader,
		stderr: io.NopCloser(strings.NewReader("")),
		waitCh: waitCh,
	}
	proc.killFn = func() error {
		atomic.AddInt32(&killCalls, 1)
		_ = serverWriter.Close()
		_ = serverReader.Close()
		waitCh <- nil
		return harness.Close()
	}

	runtime := NewTaskSessionRuntime("codex-bin")
	runtime.starter = appServerStarterFunc(func(_ context.Context, spec CommandSpec) (appServerProcess, error) {
		return proc, nil
	})

	executeDone := make(chan error, 1)
	serverDone := make(chan error, 1)
	go func() {
		defer close(serverDone)

		msg, err := harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "initialize" {
			serverDone <- errors.New("expected initialize request")
			return
		}
		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  map[string]any{"protocolVersion": 2},
		}); err != nil {
			serverDone <- err
			return
		}

		msg, err = harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "initialized" {
			serverDone <- errors.New("expected initialized notification")
			return
		}

		msg, err = harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "thread/start" {
			serverDone <- errors.New("expected thread/start request")
			return
		}
		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  map[string]any{"thread": map[string]any{"id": "thread-1"}},
		}); err != nil {
			serverDone <- err
			return
		}

		msg, err = harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "turn/start" {
			serverDone <- errors.New("expected turn/start request")
			return
		}
		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  map[string]any{"turn": map[string]any{"id": "turn-1"}},
		}); err != nil {
			serverDone <- err
			return
		}

		msg, err = harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "turn/interrupt" {
			serverDone <- errors.New("expected turn/interrupt request")
			return
		}
		if msg.Params["threadId"] != "thread-1" || msg.Params["turnId"] != "turn-1" {
			serverDone <- errors.New("expected interrupt request to include active thread and turn ids")
			return
		}

		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			Method:  "turn/completed",
			Params: map[string]any{
				"threadId":   "thread-1",
				"turnId":     "turn-1",
				"stopReason": "interrupted",
			},
		}); err != nil {
			serverDone <- err
			return
		}

		serverDone <- nil
	}()

	session, err := runtime.Start(context.Background(), contracts.TaskSessionStartRequest{
		TaskID:   "task-cancel",
		Backend:  "codex",
		RepoRoot: repoRoot,
	})
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	appSession, ok := session.(*AppServerTaskSession)
	if !ok {
		t.Fatalf("expected AppServerTaskSession, got %T", session)
	}

	go func() {
		executeDone <- appSession.Execute(context.Background(), contracts.TaskSessionExecuteRequest{
			Prompt: "implement the task",
			Model:  "openai/gpt-5.3-codex",
			Mode:   contracts.RunnerModeImplement,
		})
	}()

	select {
	case err := <-executeDone:
		t.Fatalf("execute returned before cancel: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	if err := appSession.Cancel(context.Background(), contracts.TaskSessionCancellation{Reason: "user canceled"}); err != nil {
		t.Fatalf("cancel session: %v", err)
	}
	if err := <-executeDone; err != nil {
		t.Fatalf("execute after cancel: %v", err)
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("server interaction failed: %v", err)
	}
	if atomic.LoadInt32(&killCalls) != 0 {
		t.Fatalf("expected interrupt cancellation to avoid forced kill, got %d", atomic.LoadInt32(&killCalls))
	}
}

func TestAppServerTaskSessionTeardownGracefullyShutsDownWithoutKill(t *testing.T) {
	repoRoot := t.TempDir()
	harness := contracts.NewFakeStdioJSONRPCHarness()
	clientWriter, clientReader := harness.ClientIO()
	serverWriter, serverReader := harness.ServerIO()

	waitCh := make(chan error, 1)
	var killCalls int32
	proc := &fakeAppServerProcess{
		stdin:  clientWriter,
		stdout: clientReader,
		stderr: io.NopCloser(strings.NewReader("")),
		waitCh: waitCh,
	}
	proc.killFn = func() error {
		atomic.AddInt32(&killCalls, 1)
		_ = serverWriter.Close()
		_ = serverReader.Close()
		waitCh <- nil
		return harness.Close()
	}

	runtime := NewTaskSessionRuntime("codex-bin")
	runtime.starter = appServerStarterFunc(func(_ context.Context, spec CommandSpec) (appServerProcess, error) {
		return proc, nil
	})

	serverDone := make(chan error, 1)
	go func() {
		defer close(serverDone)

		msg, err := harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "initialize" {
			serverDone <- errors.New("expected initialize request")
			return
		}
		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  map[string]any{"protocolVersion": 2},
		}); err != nil {
			serverDone <- err
			return
		}

		msg, err = harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "initialized" {
			serverDone <- errors.New("expected initialized notification")
			return
		}

		msg, err = harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "shutdown" {
			serverDone <- errors.New("expected shutdown request")
			return
		}
		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  map[string]any{},
		}); err != nil {
			serverDone <- err
			return
		}

		_ = serverWriter.Close()
		_ = serverReader.Close()
		waitCh <- nil
		serverDone <- harness.Close()
	}()

	session, err := runtime.Start(context.Background(), contracts.TaskSessionStartRequest{
		TaskID:   "task-teardown",
		Backend:  "codex",
		RepoRoot: repoRoot,
	})
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	appSession, ok := session.(*AppServerTaskSession)
	if !ok {
		t.Fatalf("expected AppServerTaskSession, got %T", session)
	}

	if err := appSession.Teardown(context.Background(), contracts.TaskSessionTeardown{Reason: "done"}); err != nil {
		t.Fatalf("teardown session: %v", err)
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("server interaction failed: %v", err)
	}
	if atomic.LoadInt32(&killCalls) != 0 {
		t.Fatalf("expected graceful teardown to avoid kill, got %d", atomic.LoadInt32(&killCalls))
	}
}

func TestAppServerTaskSessionTeardownForcesKillWhenShutdownStalls(t *testing.T) {
	repoRoot := t.TempDir()
	harness := contracts.NewFakeStdioJSONRPCHarness()
	clientWriter, clientReader := harness.ClientIO()
	serverWriter, serverReader := harness.ServerIO()

	waitCh := make(chan error, 1)
	var killCalls int32
	proc := &fakeAppServerProcess{
		stdin:  clientWriter,
		stdout: clientReader,
		stderr: io.NopCloser(strings.NewReader("")),
		waitCh: waitCh,
	}
	proc.killFn = func() error {
		atomic.AddInt32(&killCalls, 1)
		_ = serverWriter.Close()
		_ = serverReader.Close()
		waitCh <- errors.New("signal: killed")
		return harness.Close()
	}

	runtime := NewTaskSessionRuntime("codex-bin")
	runtime.starter = appServerStarterFunc(func(_ context.Context, spec CommandSpec) (appServerProcess, error) {
		return proc, nil
	})

	serverDone := make(chan error, 1)
	go func() {
		defer close(serverDone)

		msg, err := harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "initialize" {
			serverDone <- errors.New("expected initialize request")
			return
		}
		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  map[string]any{"protocolVersion": 2},
		}); err != nil {
			serverDone <- err
			return
		}

		msg, err = harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "initialized" {
			serverDone <- errors.New("expected initialized notification")
			return
		}

		msg, err = harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "shutdown" {
			serverDone <- errors.New("expected shutdown request")
			return
		}

		serverDone <- nil
	}()

	session, err := runtime.Start(context.Background(), contracts.TaskSessionStartRequest{
		TaskID:   "task-force-teardown",
		Backend:  "codex",
		RepoRoot: repoRoot,
	})
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	appSession, ok := session.(*AppServerTaskSession)
	if !ok {
		t.Fatalf("expected AppServerTaskSession, got %T", session)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := appSession.Teardown(ctx, contracts.TaskSessionTeardown{Reason: "done"}); err != nil {
		t.Fatalf("teardown session: %v", err)
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("server interaction failed: %v", err)
	}
	if atomic.LoadInt32(&killCalls) != 1 {
		t.Fatalf("expected forced kill after shutdown stall, got %d", atomic.LoadInt32(&killCalls))
	}
}

func TestCLIRunnerAdapterAppServerTreatsForcedKillAfterCompletionAsSuccess(t *testing.T) {
	repoRoot := t.TempDir()
	harness := contracts.NewFakeStdioJSONRPCHarness()
	t.Cleanup(func() {
		_ = harness.Close()
	})

	clientWriter, clientReader := harness.ClientIO()
	stderrReader, stderrWriter := io.Pipe()
	waitCh := make(chan error, 1)
	var killCalls int32
	proc := &fakeAppServerProcess{
		stdin:  clientWriter,
		stdout: clientReader,
		stderr: stderrReader,
		waitCh: waitCh,
	}
	proc.killFn = func() error {
		atomic.AddInt32(&killCalls, 1)
		_ = stderrWriter.Close()
		_ = harness.Close()
		waitCh <- errors.New("signal: killed")
		return nil
	}

	adapter := NewCLIRunnerAdapter("codex-bin", nil)
	adapter.starter = appServerStarterFunc(func(_ context.Context, spec CommandSpec) (appServerProcess, error) {
		if !reflect.DeepEqual(spec.Args, []string{"app-server"}) {
			t.Fatalf("expected app-server args, got %#v", spec.Args)
		}
		return proc, nil
	})

	serverDone := make(chan error, 1)
	go func() {
		defer close(serverDone)

		msg, err := harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "initialize" {
			serverDone <- errors.New("expected initialize request")
			return
		}
		if err := harness.SendMessage(contracts.JSONRPCMessage{JSONRPC: "2.0", ID: msg.ID, Result: map[string]any{"protocolVersion": 2}}); err != nil {
			serverDone <- err
			return
		}

		msg, err = harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "initialized" {
			serverDone <- errors.New("expected initialized notification")
			return
		}

		msg, err = harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "thread/start" {
			serverDone <- errors.New("expected thread/start request")
			return
		}
		if err := harness.SendMessage(contracts.JSONRPCMessage{JSONRPC: "2.0", ID: msg.ID, Result: map[string]any{"thread": map[string]any{"id": "thread-1"}}}); err != nil {
			serverDone <- err
			return
		}

		msg, err = harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "turn/start" {
			serverDone <- errors.New("expected turn/start request")
			return
		}
		if err := harness.SendMessage(contracts.JSONRPCMessage{JSONRPC: "2.0", ID: msg.ID, Result: map[string]any{"turn": map[string]any{"id": "turn-1"}}}); err != nil {
			serverDone <- err
			return
		}

		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			Method:  "turn/completed",
			Params: map[string]any{
				"threadId":   "thread-1",
				"turnId":     "turn-1",
				"stopReason": "end_turn",
			},
		}); err != nil {
			serverDone <- err
			return
		}

		msg, err = harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "shutdown" {
			serverDone <- errors.New("expected shutdown request")
			return
		}

		serverDone <- nil
	}()

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		TaskID:   "t-app-server-kill-after-complete",
		RepoRoot: repoRoot,
		Prompt:   "implement",
		Mode:     contracts.RunnerModeImplement,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("app-server interaction failed: %v", err)
	}
	if result.Status != contracts.RunnerResultCompleted {
		t.Fatalf("expected completed result, got %q (%s)", result.Status, result.Reason)
	}
	if atomic.LoadInt32(&killCalls) != 1 {
		t.Fatalf("expected forced kill after shutdown stall, got %d", atomic.LoadInt32(&killCalls))
	}
}

func TestCLIRunnerAdapterAppServerUsesStreamedReviewPassVerdictWhenCompletionLacksVerdict(t *testing.T) {
	repoRoot := t.TempDir()
	harness := contracts.NewFakeStdioJSONRPCHarness()
	t.Cleanup(func() {
		_ = harness.Close()
	})

	clientWriter, clientReader := harness.ClientIO()
	stderrReader, stderrWriter := io.Pipe()
	waitCh := make(chan error, 1)
	proc := &fakeAppServerProcess{
		stdin:  clientWriter,
		stdout: clientReader,
		stderr: stderrReader,
		waitCh: waitCh,
	}
	proc.killFn = func() error {
		_ = stderrWriter.Close()
		waitCh <- nil
		return harness.Close()
	}

	adapter := NewCLIRunnerAdapter("codex-bin", nil)
	adapter.starter = appServerStarterFunc(func(_ context.Context, spec CommandSpec) (appServerProcess, error) {
		if !reflect.DeepEqual(spec.Args, []string{"app-server"}) {
			t.Fatalf("expected app-server args, got %#v", spec.Args)
		}
		return proc, nil
	})

	serverDone := make(chan error, 1)
	go func() {
		defer close(serverDone)

		msg, err := harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "initialize" {
			serverDone <- errors.New("expected initialize request")
			return
		}
		if err := harness.SendMessage(contracts.JSONRPCMessage{JSONRPC: "2.0", ID: msg.ID, Result: map[string]any{"protocolVersion": 2}}); err != nil {
			serverDone <- err
			return
		}

		msg, err = harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "initialized" {
			serverDone <- errors.New("expected initialized notification")
			return
		}

		msg, err = harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "thread/start" {
			serverDone <- errors.New("expected thread/start request")
			return
		}
		if err := harness.SendMessage(contracts.JSONRPCMessage{JSONRPC: "2.0", ID: msg.ID, Result: map[string]any{"thread": map[string]any{"id": "thread-1"}}}); err != nil {
			serverDone <- err
			return
		}

		msg, err = harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "turn/start" {
			serverDone <- errors.New("expected turn/start request")
			return
		}
		if err := harness.SendMessage(contracts.JSONRPCMessage{JSONRPC: "2.0", ID: msg.ID, Result: map[string]any{"turn": map[string]any{"id": "turn-1"}}}); err != nil {
			serverDone <- err
			return
		}

		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			Method:  "item/outputDelta",
			Params: map[string]any{
				"threadId": "thread-1",
				"turnId":   "turn-1",
				"itemId":   "item-1",
				"delta":    "REVIEW_VERDICT: pass",
				"item": map[string]any{
					"id":   "item-1",
					"type": "agent_message",
				},
			},
		}); err != nil {
			serverDone <- err
			return
		}

		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			Method:  "turn/completed",
			Params: map[string]any{
				"threadId":   "thread-1",
				"turnId":     "turn-1",
				"stopReason": "end_turn",
			},
		}); err != nil {
			serverDone <- err
			return
		}

		msg, err = harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "shutdown" {
			serverDone <- errors.New("expected shutdown request")
			return
		}
		if err := stderrWriter.Close(); err != nil {
			serverDone <- err
			return
		}
		waitCh <- nil
		serverDone <- nil
	}()

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		TaskID:   "t-app-server-streamed-pass",
		RepoRoot: repoRoot,
		Prompt:   "review",
		Mode:     contracts.RunnerModeReview,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("app-server interaction failed: %v", err)
	}
	if result.Status != contracts.RunnerResultCompleted {
		t.Fatalf("expected completed result, got %q (%s)", result.Status, result.Reason)
	}
	if !result.ReviewReady {
		t.Fatalf("expected ReviewReady=true when streamed output contains pass verdict")
	}
	if verdict := result.Artifacts["review_verdict"]; verdict != "pass" {
		t.Fatalf("expected review_verdict=pass, got %#v", result.Artifacts)
	}
}

func TestNormalizeAppServerNotificationMapsCompletionIntoReviewArtifacts(t *testing.T) {
	message := contracts.JSONRPCMessage{
		Method: "turn/completed",
		Params: map[string]any{
			"threadId":   "thread-review",
			"turnId":     "turn-review",
			"stopReason": "end_turn",
			"output": map[string]any{
				"text": "REVIEW_VERDICT: fail\nREVIEW_FAIL_FEEDBACK: missing retry regression test\n",
			},
		},
	}

	event, completion, ok := NormalizeAppServerNotification(message, contracts.RunnerModeReview)
	if !ok {
		t.Fatalf("expected completion notification to normalize")
	}
	if event.Type != contracts.TaskSessionEventTypeLifecycle {
		t.Fatalf("expected lifecycle completion event, got %q", event.Type)
	}
	if event.Lifecycle == nil || event.Lifecycle.State != contracts.TaskSessionLifecycleStopped {
		t.Fatalf("expected stopped lifecycle, got %#v", event.Lifecycle)
	}
	if completion == nil {
		t.Fatalf("expected completion metadata")
	}
	if completion.Reason != "end_turn" {
		t.Fatalf("expected completion reason end_turn, got %q", completion.Reason)
	}
	if completion.ReviewReady {
		t.Fatalf("expected failing review completion to keep ReviewReady=false")
	}
	if completion.Artifacts["review_verdict"] != "fail" {
		t.Fatalf("expected review_verdict artifact, got %#v", completion.Artifacts)
	}
	if completion.Artifacts["review_fail_feedback"] != "missing retry regression test" {
		t.Fatalf("expected review_fail_feedback artifact, got %#v", completion.Artifacts)
	}

	progress, progressCompletion, ok := RunnerProgressFromAppServerNotification(message, contracts.RunnerModeReview)
	if !ok {
		t.Fatalf("expected completion progress")
	}
	if progress.Type != string(contracts.EventTypeRunnerProgress) {
		t.Fatalf("expected runner_progress type, got %q", progress.Type)
	}
	if progress.Metadata["reason"] != "end_turn" {
		t.Fatalf("expected completion reason metadata, got %#v", progress.Metadata)
	}
	if !reflect.DeepEqual(progressCompletion, completion) {
		t.Fatalf("expected progress completion %#v, got %#v", completion, progressCompletion)
	}

	result := contracts.RunnerResult{Status: contracts.RunnerResultCompleted}
	ApplyAppServerCompletion(&result, progressCompletion)
	if result.ReviewReady {
		t.Fatalf("expected ReviewReady=false after failing review completion")
	}
	if result.Artifacts["review_verdict"] != "fail" {
		t.Fatalf("expected review_verdict artifact after apply, got %#v", result.Artifacts)
	}
	if result.Artifacts["review_fail_feedback"] != "missing retry regression test" {
		t.Fatalf("expected review_fail_feedback after apply, got %#v", result.Artifacts)
	}
}

func TestCLIRunnerAdapterImplementsContract(t *testing.T) {
	var _ contracts.AgentRunner = (*CLIRunnerAdapter)(nil)
}

func TestCLIRunnerAdapterRunsCodexAndStreamsProgress(t *testing.T) {
	repoRoot := t.TempDir()
	var gotSpec CommandSpec
	updates := []contracts.RunnerProgress{}
	adapter := NewCLIRunnerAdapter("codex-bin", commandRunnerFunc(func(_ context.Context, spec CommandSpec) error {
		gotSpec = spec
		_, _ = io.WriteString(spec.Stdout, "working line\n")
		_, _ = io.WriteString(spec.Stderr, "warn line\n")
		return nil
	}))

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		TaskID:   "t-1",
		RepoRoot: repoRoot,
		Prompt:   "implement feature",
		Model:    "openai/gpt-5.3-codex",
		OnProgress: func(progress contracts.RunnerProgress) {
			updates = append(updates, progress)
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != contracts.RunnerResultCompleted {
		t.Fatalf("expected completed status, got %s", result.Status)
	}
	if gotSpec.Binary != "codex-bin" {
		t.Fatalf("expected binary codex-bin, got %q", gotSpec.Binary)
	}
	expectedArgs := []string{"app-server"}
	if !reflect.DeepEqual(gotSpec.Args, expectedArgs) {
		t.Fatalf("unexpected args: %#v", gotSpec.Args)
	}
	if gotSpec.Dir != repoRoot {
		t.Fatalf("expected command dir %q, got %q", repoRoot, gotSpec.Dir)
	}
	expectedLogPath := filepath.Join(repoRoot, "runner-logs", "codex", "t-1.jsonl")
	if result.LogPath != expectedLogPath {
		t.Fatalf("expected log path %q, got %q", expectedLogPath, result.LogPath)
	}
	if result.Artifacts["backend"] != "codex" {
		t.Fatalf("expected backend artifact codex, got %q", result.Artifacts["backend"])
	}
	if len(updates) < 2 {
		t.Fatalf("expected at least 2 progress updates, got %d", len(updates))
	}
	if updates[0].Type != "runner_output" || updates[0].Message != "working line" {
		t.Fatalf("unexpected first update: %#v", updates[0])
	}
	if updates[1].Type != "runner_output" || updates[1].Message != "stderr: warn line" {
		t.Fatalf("unexpected second update: %#v", updates[1])
	}

	stdoutContent, err := os.ReadFile(result.LogPath)
	if err != nil {
		t.Fatalf("read stdout log: %v", err)
	}
	if !strings.Contains(string(stdoutContent), "working line") {
		t.Fatalf("expected stdout log to contain output, got %q", string(stdoutContent))
	}
	stderrPath := strings.TrimSuffix(result.LogPath, ".jsonl") + ".stderr.log"
	stderrContent, err := os.ReadFile(stderrPath)
	if err != nil {
		t.Fatalf("read stderr log: %v", err)
	}
	if !strings.Contains(string(stderrContent), "warn line") {
		t.Fatalf("expected stderr log to contain output, got %q", string(stderrContent))
	}
}

func TestCLIRunnerAdapterBuildsCommandFromConfiguredArgsTemplate(t *testing.T) {
	repoRoot := t.TempDir()
	var gotSpec CommandSpec
	adapter := NewCLIRunnerAdapter("codex-bin", commandRunnerFunc(func(_ context.Context, spec CommandSpec) error {
		gotSpec = spec
		return nil
	}), "--backend={{backend}}", "--model", "{{model}}", "--prompt", "{{prompt}}", "--task-id={{task_id}}", "--repo={{repo_root}}", "--mode={{mode}}")

	_, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		TaskID:   "task-1",
		RepoRoot: repoRoot,
		Prompt:   "implement codex",
		Model:    "openai/gpt-5.3-codex",
		Mode:     contracts.RunnerModeReview,
		Metadata: map[string]string{"backend": "codex"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []string{"--backend=codex", "--model", "openai/gpt-5.3-codex", "--prompt", "implement codex", "--task-id=task-1", "--repo=" + repoRoot, "--mode=review"}
	if !reflect.DeepEqual(gotSpec.Args, expected) {
		t.Fatalf("unexpected templated args: %#v", gotSpec.Args)
	}
}

func TestCLIRunnerAdapterPrefersLegacyRunnerFallbackOverAppServerStarter(t *testing.T) {
	repoRoot := t.TempDir()
	usedRunner := false
	adapter := NewCLIRunnerAdapter("codex-bin", commandRunnerFunc(func(_ context.Context, spec CommandSpec) error {
		usedRunner = true
		_, _ = io.WriteString(spec.Stdout, "working line\n")
		return nil
	}))
	adapter.starter = appServerStarterFunc(func(context.Context, CommandSpec) (appServerProcess, error) {
		return nil, errors.New("app-server starter should not be used when legacy runner fallback is configured")
	})

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		TaskID:   "t-fallback-selection",
		RepoRoot: repoRoot,
		Prompt:   "implement feature",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !usedRunner {
		t.Fatal("expected legacy runner fallback to be used")
	}
	if result.Status != contracts.RunnerResultCompleted {
		t.Fatalf("expected completed result, got %q (%s)", result.Status, result.Reason)
	}
}

func TestCLIRunnerAdapterSetsReviewReadyOnStructuredPassVerdict(t *testing.T) {
	adapter := NewCLIRunnerAdapter("codex-bin", commandRunnerFunc(func(_ context.Context, spec CommandSpec) error {
		_, _ = io.WriteString(spec.Stdout, "REVIEW_VERDICT: pass\n")
		return nil
	}))

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		TaskID:   "t-review",
		RepoRoot: t.TempDir(),
		Prompt:   "review",
		Mode:     contracts.RunnerModeReview,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != contracts.RunnerResultCompleted {
		t.Fatalf("expected completed status, got %s", result.Status)
	}
	if !result.ReviewReady {
		t.Fatalf("expected ReviewReady=true for pass verdict")
	}
}

func TestCLIRunnerAdapterLeavesReviewReadyFalseOnStructuredFailVerdict(t *testing.T) {
	adapter := NewCLIRunnerAdapter("codex-bin", commandRunnerFunc(func(_ context.Context, spec CommandSpec) error {
		_, _ = io.WriteString(spec.Stdout, "REVIEW_VERDICT: failDONE\n")
		return nil
	}))

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		TaskID:   "t-review",
		RepoRoot: t.TempDir(),
		Prompt:   "review",
		Mode:     contracts.RunnerModeReview,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != contracts.RunnerResultCompleted {
		t.Fatalf("expected completed status, got %s", result.Status)
	}
	if result.ReviewReady {
		t.Fatalf("expected ReviewReady=false for fail verdict")
	}
}

func TestCLIRunnerAdapterExtractsStructuredReviewFailFeedback(t *testing.T) {
	adapter := NewCLIRunnerAdapter("codex-bin", commandRunnerFunc(func(_ context.Context, spec CommandSpec) error {
		_, _ = io.WriteString(spec.Stdout, "REVIEW_VERDICT: fail\n")
		_, _ = io.WriteString(spec.Stdout, "REVIEW_FAIL_FEEDBACK: missing e2e assertion for retry path\n")
		return nil
	}))

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		TaskID:   "t-review",
		RepoRoot: t.TempDir(),
		Prompt:   "review",
		Mode:     contracts.RunnerModeReview,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != contracts.RunnerResultCompleted {
		t.Fatalf("expected completed status, got %s", result.Status)
	}
	if result.Artifacts["review_verdict"] != "fail" {
		t.Fatalf("expected review_verdict=fail artifact, got %#v", result.Artifacts)
	}
	if result.Artifacts["review_fail_feedback"] != "missing e2e assertion for retry path" {
		t.Fatalf("expected review_fail_feedback artifact, got %#v", result.Artifacts)
	}
}

func TestCLIRunnerAdapterMapsAppServerNotificationsIntoProgressAndReviewCompletion(t *testing.T) {
	repoRoot := t.TempDir()
	updates := []contracts.RunnerProgress{}
	adapter := NewCLIRunnerAdapter("codex-bin", commandRunnerFunc(func(_ context.Context, spec CommandSpec) error {
		messages := []contracts.JSONRPCMessage{
			{
				Method: "thread/started",
				Params: map[string]any{"threadId": "thread-1"},
			},
			{
				Method: "turn/started",
				Params: map[string]any{"threadId": "thread-1", "turnId": "turn-1"},
			},
			{
				Method: "item/started",
				Params: map[string]any{
					"threadId": "thread-1",
					"turnId":   "turn-1",
					"item": map[string]any{
						"id":    "item-1",
						"type":  "command_execution",
						"title": "Run tests",
					},
				},
			},
			{
				Method: "item/agentMessage/delta",
				Params: map[string]any{
					"threadId": "thread-1",
					"turnId":   "turn-1",
					"itemId":   "item-2",
					"delta":    "running tests",
				},
			},
			{
				Method: "item/commandExecution/requestApproval",
				Params: map[string]any{
					"threadId": "thread-1",
					"turnId":   "turn-1",
					"itemId":   "item-3",
					"id":       "approval-1",
					"title":    "Run go test",
					"reason":   "execute integration test",
					"command":  []any{"go", "test", "./internal/codex"},
				},
			},
			{
				Method: "turn/completed",
				Params: map[string]any{
					"threadId":   "thread-1",
					"turnId":     "turn-1",
					"stopReason": "end_turn",
					"output": map[string]any{
						"text": "REVIEW_VERDICT: fail\nREVIEW_FAIL_FEEDBACK: add app-server progress coverage\n",
					},
				},
			},
		}
		encoder := json.NewEncoder(spec.Stdout)
		for _, message := range messages {
			if err := encoder.Encode(message); err != nil {
				return err
			}
		}
		return nil
	}))

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		TaskID:   "t-review-app-server",
		RepoRoot: repoRoot,
		Prompt:   "review",
		Mode:     contracts.RunnerModeReview,
		OnProgress: func(progress contracts.RunnerProgress) {
			updates = append(updates, progress)
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(updates) != 6 {
		t.Fatalf("expected 6 mapped progress updates, got %d: %#v", len(updates), updates)
	}
	if updates[0].Type != string(contracts.EventTypeRunnerProgress) || updates[0].Metadata["state"] != string(contracts.TaskSessionLifecycleReady) {
		t.Fatalf("expected thread started lifecycle progress, got %#v", updates[0])
	}
	if updates[1].Type != string(contracts.EventTypeRunnerProgress) || updates[1].Metadata["turn_id"] != "turn-1" {
		t.Fatalf("expected turn started progress, got %#v", updates[1])
	}
	if updates[2].Type != string(contracts.EventTypeRunnerProgress) || updates[2].Message != "Run tests" {
		t.Fatalf("expected item progress, got %#v", updates[2])
	}
	if updates[3].Type != string(contracts.EventTypeRunnerOutput) || updates[3].Message != "running tests" {
		t.Fatalf("expected delta output progress, got %#v", updates[3])
	}
	if updates[4].Type != string(contracts.EventTypeRunnerWarning) || updates[4].Metadata["approval_id"] != "approval-1" {
		t.Fatalf("expected approval warning progress, got %#v", updates[4])
	}
	if updates[5].Type != string(contracts.EventTypeRunnerProgress) || updates[5].Metadata["reason"] != "end_turn" {
		t.Fatalf("expected completion progress with reason metadata, got %#v", updates[5])
	}
	if result.Status != contracts.RunnerResultCompleted {
		t.Fatalf("expected completed status, got %s", result.Status)
	}
	if result.ReviewReady {
		t.Fatalf("expected ReviewReady=false for failing review verdict")
	}
	if result.Reason != "end_turn" {
		t.Fatalf("expected result reason end_turn, got %q", result.Reason)
	}
	if result.Artifacts["review_verdict"] != "fail" {
		t.Fatalf("expected review_verdict artifact, got %#v", result.Artifacts)
	}
	if result.Artifacts["review_fail_feedback"] != "add app-server progress coverage" {
		t.Fatalf("expected review_fail_feedback artifact, got %#v", result.Artifacts)
	}
	protocolPath := strings.TrimSuffix(result.LogPath, ".jsonl") + ".protocol.log"
	protocolContent, err := os.ReadFile(protocolPath)
	if err != nil {
		t.Fatalf("read protocol log: %v", err)
	}
	if !strings.Contains(string(protocolContent), "\"method\":\"turn/completed\"") {
		t.Fatalf("expected protocol log to capture app-server notifications, got %q", string(protocolContent))
	}
}

func TestCLIRunnerAdapterUsesAppServerJSONRPCProductionPath(t *testing.T) {
	repoRoot := t.TempDir()
	harness := contracts.NewFakeStdioJSONRPCHarness()
	t.Cleanup(func() {
		_ = harness.Close()
	})

	clientWriter, clientReader := harness.ClientIO()
	stderrReader, stderrWriter := io.Pipe()
	waitCh := make(chan error, 1)
	proc := &fakeAppServerProcess{
		stdin:  clientWriter,
		stdout: clientReader,
		stderr: stderrReader,
		waitCh: waitCh,
	}
	proc.killFn = func() error {
		_ = stderrWriter.Close()
		waitCh <- nil
		return harness.Close()
	}

	var gotSpec CommandSpec
	updates := []contracts.RunnerProgress{}
	adapter := NewCLIRunnerAdapter("codex-bin", nil)
	adapter.starter = appServerStarterFunc(func(_ context.Context, spec CommandSpec) (appServerProcess, error) {
		gotSpec = spec
		return proc, nil
	})

	serverDone := make(chan error, 1)
	go func() {
		defer close(serverDone)

		msg, err := harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "initialize" {
			serverDone <- errors.New("expected initialize request")
			return
		}
		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  map[string]any{"protocolVersion": 2},
		}); err != nil {
			serverDone <- err
			return
		}

		msg, err = harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "initialized" {
			serverDone <- errors.New("expected initialized notification")
			return
		}

		msg, err = harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "thread/start" {
			serverDone <- errors.New("expected thread/start request")
			return
		}
		if msg.Params["cwd"] != repoRoot {
			serverDone <- errors.New("expected thread/start cwd to match repo root")
			return
		}
		if msg.Params["approvalPolicy"] != "never" {
			serverDone <- errors.New("expected thread/start approval policy never")
			return
		}
		if msg.Params["sandbox"] != "danger-full-access" {
			serverDone <- errors.New("expected thread/start sandbox danger-full-access")
			return
		}
		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result: map[string]any{
				"thread":         map[string]any{"id": "thread-1"},
				"approvalPolicy": "never",
				"cwd":            repoRoot,
				"model":          "openai/gpt-5.3-codex",
				"modelProvider":  "openai",
				"sandbox":        "danger-full-access",
			},
		}); err != nil {
			serverDone <- err
			return
		}
		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			Method:  "thread/started",
			Params:  map[string]any{"threadId": "thread-1"},
		}); err != nil {
			serverDone <- err
			return
		}

		msg, err = harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "turn/start" {
			serverDone <- errors.New("expected turn/start request")
			return
		}
		if msg.Params["threadId"] != "thread-1" {
			serverDone <- errors.New("expected turn/start thread id")
			return
		}
		input, ok := msg.Params["input"].([]any)
		if !ok || len(input) != 1 {
			serverDone <- errors.New("expected one turn/start input item")
			return
		}
		firstInput, ok := input[0].(map[string]any)
		if !ok || firstInput["text"] != "review prompt" {
			serverDone <- errors.New("expected prompt text in turn/start input")
			return
		}
		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  map[string]any{"turn": map[string]any{"id": "turn-1"}},
		}); err != nil {
			serverDone <- err
			return
		}

		for _, message := range []contracts.JSONRPCMessage{
			{JSONRPC: "2.0", Method: "turn/started", Params: map[string]any{"threadId": "thread-1", "turnId": "turn-1"}},
			{JSONRPC: "2.0", Method: "item/started", Params: map[string]any{"threadId": "thread-1", "turnId": "turn-1", "item": map[string]any{"id": "item-1", "type": "command_execution", "title": "Run tests"}}},
			{JSONRPC: "2.0", Method: "item/mcpToolCall/progress", Params: map[string]any{"threadId": "thread-1", "turnId": "turn-1", "item": map[string]any{"id": "item-tool", "type": "mcpToolCall", "title": "Search code"}}},
			{JSONRPC: "2.0", Method: "item/agentMessage/delta", Params: map[string]any{"threadId": "thread-1", "turnId": "turn-1", "itemId": "item-2", "delta": "running tests"}},
		} {
			if err := harness.SendMessage(message); err != nil {
				serverDone <- err
				return
			}
		}

		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      json.RawMessage("99"),
			Method:  "item/commandExecution/requestApproval",
			Params: map[string]any{
				"threadId": "thread-1",
				"turnId":   "turn-1",
				"itemId":   "item-3",
				"id":       "approval-1",
				"title":    "Run go test",
				"reason":   "execute integration test",
				"command":  []any{"go", "test", "./internal/codex"},
			},
		}); err != nil {
			serverDone <- err
			return
		}

		msg, err = harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if string(msg.ID) != "99" || msg.Result["decision"] != "accept" {
			serverDone <- errors.New("expected approval response with accept decision")
			return
		}

		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			Method:  "turn/completed",
			Params: map[string]any{
				"threadId":   "thread-1",
				"turnId":     "turn-1",
				"stopReason": "end_turn",
				"output": map[string]any{
					"text": "REVIEW_VERDICT: fail\nREVIEW_FAIL_FEEDBACK: app-server production path coverage missing\n",
				},
			},
		}); err != nil {
			serverDone <- err
			return
		}

		serverDone <- nil
	}()

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		TaskID:   "t-app-server",
		RepoRoot: repoRoot,
		Prompt:   "review prompt",
		Model:    "openai/gpt-5.3-codex",
		Mode:     contracts.RunnerModeReview,
		OnProgress: func(progress contracts.RunnerProgress) {
			updates = append(updates, progress)
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("app-server interaction failed: %v", err)
	}
	if gotSpec.Binary != "codex-bin" {
		t.Fatalf("expected codex-bin binary, got %q", gotSpec.Binary)
	}
	if !reflect.DeepEqual(gotSpec.Args, []string{"app-server"}) {
		t.Fatalf("expected app-server args, got %#v", gotSpec.Args)
	}
	if len(updates) != 7 {
		t.Fatalf("expected 7 progress updates, got %d: %#v", len(updates), updates)
	}
	if updates[2].Message != "Run tests" {
		t.Fatalf("expected item progress message Run tests, got %#v", updates[2])
	}
	if updates[3].Metadata["item_type"] != "mcpToolCall" {
		t.Fatalf("expected tool progress metadata, got %#v", updates[3])
	}
	if updates[4].Type != string(contracts.EventTypeRunnerOutput) || updates[4].Message != "running tests" {
		t.Fatalf("expected delta runner output, got %#v", updates[4])
	}
	if updates[5].Metadata["approval_id"] != "approval-1" {
		t.Fatalf("expected approval progress metadata, got %#v", updates[5])
	}
	if updates[6].Metadata["reason"] != "end_turn" {
		t.Fatalf("expected completion progress metadata, got %#v", updates[6])
	}
	if result.Status != contracts.RunnerResultCompleted {
		t.Fatalf("expected completed result, got %q (%s)", result.Status, result.Reason)
	}
	if result.Reason != "end_turn" {
		t.Fatalf("expected completion reason end_turn, got %q", result.Reason)
	}
	if result.ReviewReady {
		t.Fatalf("expected failing review verdict to keep ReviewReady=false")
	}
	if result.Artifacts["review_verdict"] != "fail" {
		t.Fatalf("expected review verdict artifact, got %#v", result.Artifacts)
	}
	if result.Artifacts["review_fail_feedback"] != "app-server production path coverage missing" {
		t.Fatalf("expected review feedback artifact, got %#v", result.Artifacts)
	}
}

func TestCLIRunnerAdapterAppServerHandlesUserInputRequestsAndTeardown(t *testing.T) {
	repoRoot := t.TempDir()
	harness := contracts.NewFakeStdioJSONRPCHarness()
	t.Cleanup(func() {
		_ = harness.Close()
	})

	clientWriter, clientReader := harness.ClientIO()
	stderrReader, stderrWriter := io.Pipe()
	waitCh := make(chan error, 1)
	var killCalls int32
	var waitCalls int32
	proc := &fakeAppServerProcess{
		stdin:  clientWriter,
		stdout: clientReader,
		stderr: stderrReader,
		waitCh: waitCh,
	}
	proc.killFn = func() error {
		atomic.AddInt32(&killCalls, 1)
		_ = stderrWriter.Close()
		waitCh <- nil
		return harness.Close()
	}
	proc.waitFn = func() error {
		atomic.AddInt32(&waitCalls, 1)
		return <-waitCh
	}

	adapter := NewCLIRunnerAdapter("codex-bin", nil)
	adapter.starter = appServerStarterFunc(func(_ context.Context, spec CommandSpec) (appServerProcess, error) {
		if !reflect.DeepEqual(spec.Args, []string{"app-server"}) {
			t.Fatalf("expected app-server args, got %#v", spec.Args)
		}
		return proc, nil
	})

	updates := []contracts.RunnerProgress{}
	serverDone := make(chan error, 1)
	go func() {
		defer close(serverDone)

		msg, err := harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "initialize" {
			serverDone <- errors.New("expected initialize request")
			return
		}
		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  map[string]any{"protocolVersion": 2},
		}); err != nil {
			serverDone <- err
			return
		}

		msg, err = harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "initialized" {
			serverDone <- errors.New("expected initialized notification")
			return
		}

		msg, err = harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "thread/start" {
			serverDone <- errors.New("expected thread/start request")
			return
		}
		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  map[string]any{"thread": map[string]any{"id": "thread-1"}},
		}); err != nil {
			serverDone <- err
			return
		}

		msg, err = harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "turn/start" {
			serverDone <- errors.New("expected turn/start request")
			return
		}
		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  map[string]any{"turn": map[string]any{"id": "turn-1"}},
		}); err != nil {
			serverDone <- err
			return
		}

		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      json.RawMessage("7"),
			Method:  "item/tool/requestUserInput",
			Params: map[string]any{
				"threadId": "thread-1",
				"turnId":   "turn-1",
				"itemId":   "item-1",
				"title":    "Choose a path",
				"questions": []any{
					map[string]any{
						"id":       "selection",
						"question": "Choose a path",
						"options": []any{
							map[string]any{"label": "Proceed"},
							map[string]any{"label": "Abort"},
						},
					},
				},
			},
		}); err != nil {
			serverDone <- err
			return
		}

		msg, err = harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if string(msg.ID) != "7" {
			serverDone <- errors.New("expected user-input response id 7")
			return
		}
		answers, ok := msg.Result["answers"].(map[string]any)
		if !ok {
			serverDone <- errors.New("expected answers map in response")
			return
		}
		selection, ok := answers["selection"].(map[string]any)
		if !ok {
			serverDone <- errors.New("expected selection answer entry")
			return
		}
		picked, ok := selection["answers"].([]any)
		if !ok || len(picked) != 1 || picked[0] != "Proceed" {
			serverDone <- errors.New("expected first option to be selected")
			return
		}

		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			Method:  "turn/completed",
			Params: map[string]any{
				"threadId":   "thread-1",
				"turnId":     "turn-1",
				"stopReason": "end_turn",
			},
		}); err != nil {
			serverDone <- err
			return
		}

		msg, err = harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "shutdown" {
			serverDone <- errors.New("expected shutdown request during graceful teardown")
			return
		}
		if err := stderrWriter.Close(); err != nil {
			serverDone <- err
			return
		}
		waitCh <- nil

		serverDone <- nil
	}()

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		TaskID:   "t-user-input",
		RepoRoot: repoRoot,
		Prompt:   "implement prompt",
		Mode:     contracts.RunnerModeImplement,
		OnProgress: func(progress contracts.RunnerProgress) {
			updates = append(updates, progress)
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("app-server interaction failed: %v", err)
	}
	if result.Status != contracts.RunnerResultCompleted {
		t.Fatalf("expected completed result, got %q (%s)", result.Status, result.Reason)
	}
	if len(updates) != 2 {
		t.Fatalf("expected user-input warning plus completion progress, got %d: %#v", len(updates), updates)
	}
	if updates[0].Type != string(contracts.EventTypeRunnerWarning) || updates[0].Message != "Choose a path" {
		t.Fatalf("expected user-input warning progress, got %#v", updates[0])
	}
	if updates[1].Metadata["reason"] != "end_turn" {
		t.Fatalf("expected completion reason metadata, got %#v", updates[1])
	}
	if atomic.LoadInt32(&killCalls) != 0 {
		t.Fatalf("expected graceful teardown to avoid kill, got %d", atomic.LoadInt32(&killCalls))
	}
	if atomic.LoadInt32(&waitCalls) != 1 {
		t.Fatalf("expected one teardown wait, got %d", atomic.LoadInt32(&waitCalls))
	}
}

func TestCLIRunnerAdapterAppServerFailsHandshakeWithoutThreadID(t *testing.T) {
	repoRoot := t.TempDir()
	harness := contracts.NewFakeStdioJSONRPCHarness()
	t.Cleanup(func() {
		_ = harness.Close()
	})

	clientWriter, clientReader := harness.ClientIO()
	stderrReader, stderrWriter := io.Pipe()
	waitCh := make(chan error, 1)
	proc := &fakeAppServerProcess{
		stdin:  clientWriter,
		stdout: clientReader,
		stderr: stderrReader,
		waitCh: waitCh,
	}
	proc.killFn = func() error {
		_ = stderrWriter.Close()
		waitCh <- nil
		return harness.Close()
	}

	adapter := NewCLIRunnerAdapter("codex-bin", nil)
	adapter.starter = appServerStarterFunc(func(_ context.Context, spec CommandSpec) (appServerProcess, error) {
		return proc, nil
	})

	serverDone := make(chan error, 1)
	go func() {
		defer close(serverDone)

		msg, err := harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  map[string]any{"protocolVersion": 2},
		}); err != nil {
			serverDone <- err
			return
		}

		msg, err = harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "initialized" {
			serverDone <- errors.New("expected initialized notification")
			return
		}

		msg, err = harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "thread/start" {
			serverDone <- errors.New("expected thread/start request")
			return
		}
		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  map[string]any{"thread": map[string]any{}},
		}); err != nil {
			serverDone <- err
			return
		}

		serverDone <- nil
	}()

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		TaskID:   "t-handshake",
		RepoRoot: repoRoot,
		Prompt:   "implement prompt",
		Mode:     contracts.RunnerModeImplement,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("app-server interaction failed: %v", err)
	}
	if result.Status != contracts.RunnerResultFailed {
		t.Fatalf("expected failed result, got %q (%s)", result.Status, result.Reason)
	}
	if !strings.Contains(result.Reason, "missing thread id") {
		t.Fatalf("expected missing thread id failure, got %q", result.Reason)
	}
}

func TestCLIRunnerAdapterAppServerCancellationStopsBlockedRead(t *testing.T) {
	repoRoot := t.TempDir()
	harness := contracts.NewFakeStdioJSONRPCHarness()
	t.Cleanup(func() {
		_ = harness.Close()
	})

	clientWriter, clientReader := harness.ClientIO()
	stderrReader, stderrWriter := io.Pipe()
	waitCh := make(chan error, 1)
	var killCalls int32
	var waitCalls int32
	proc := &fakeAppServerProcess{
		stdin:  clientWriter,
		stdout: clientReader,
		stderr: stderrReader,
		waitCh: waitCh,
	}
	proc.killFn = func() error {
		atomic.AddInt32(&killCalls, 1)
		_ = stderrWriter.Close()
		waitCh <- nil
		return harness.Close()
	}
	proc.waitFn = func() error {
		atomic.AddInt32(&waitCalls, 1)
		return <-waitCh
	}

	adapter := NewCLIRunnerAdapter("codex-bin", nil)
	adapter.starter = appServerStarterFunc(func(_ context.Context, spec CommandSpec) (appServerProcess, error) {
		return proc, nil
	})

	handshakeReady := make(chan struct{})
	serverDone := make(chan error, 1)
	go func() {
		defer close(serverDone)

		msg, err := harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "initialize" {
			serverDone <- errors.New("expected initialize request")
			return
		}
		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  map[string]any{"protocolVersion": 2},
		}); err != nil {
			serverDone <- err
			return
		}

		msg, err = harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "initialized" {
			serverDone <- errors.New("expected initialized notification")
			return
		}

		msg, err = harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "thread/start" {
			serverDone <- errors.New("expected thread/start request")
			return
		}
		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  map[string]any{"thread": map[string]any{"id": "thread-1"}},
		}); err != nil {
			serverDone <- err
			return
		}

		msg, err = harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "turn/start" {
			serverDone <- errors.New("expected turn/start request")
			return
		}
		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  map[string]any{"turn": map[string]any{"id": "turn-1"}},
		}); err != nil {
			serverDone <- err
			return
		}

		close(handshakeReady)
		serverDone <- nil
	}()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan contracts.RunnerResult, 1)
	go func() {
		result, err := adapter.Run(ctx, contracts.RunnerRequest{
			TaskID:   "t-cancel",
			RepoRoot: repoRoot,
			Prompt:   "implement prompt",
			Mode:     contracts.RunnerModeImplement,
		})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
			return
		}
		done <- result
	}()

	select {
	case <-handshakeReady:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for handshake")
	}

	cancel()

	select {
	case result := <-done:
		if result.Status != contracts.RunnerResultFailed {
			t.Fatalf("expected canceled run to fail, got %q (%s)", result.Status, result.Reason)
		}
		if !strings.Contains(strings.ToLower(result.Reason), "canceled") {
			t.Fatalf("expected canceled reason, got %q", result.Reason)
		}
	case <-time.After(250 * time.Millisecond):
		_ = harness.Close()
		t.Fatal("run did not stop after context cancellation")
	}

	if atomic.LoadInt32(&killCalls) != 1 {
		t.Fatalf("expected one teardown kill after cancellation, got %d", atomic.LoadInt32(&killCalls))
	}
	if atomic.LoadInt32(&waitCalls) != 1 {
		t.Fatalf("expected one teardown wait after cancellation, got %d", atomic.LoadInt32(&waitCalls))
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("app-server interaction failed: %v", err)
	}
}

func TestAppServerTaskSessionForceCancelKillsProcessWithoutInterrupt(t *testing.T) {
	repoRoot := t.TempDir()
	harness := contracts.NewFakeStdioJSONRPCHarness()
	clientWriter, clientReader := harness.ClientIO()
	serverWriter, serverReader := harness.ServerIO()

	waitCh := make(chan error, 1)
	var killCalls int32
	proc := &fakeAppServerProcess{
		stdin:  clientWriter,
		stdout: clientReader,
		stderr: io.NopCloser(strings.NewReader("")),
		waitCh: waitCh,
	}
	proc.killFn = func() error {
		atomic.AddInt32(&killCalls, 1)
		_ = serverWriter.Close()
		_ = serverReader.Close()
		waitCh <- nil
		return harness.Close()
	}

	runtime := NewTaskSessionRuntime("codex-bin")
	runtime.starter = appServerStarterFunc(func(_ context.Context, spec CommandSpec) (appServerProcess, error) {
		return proc, nil
	})

	turnStarted := make(chan struct{})
	serverDone := make(chan error, 1)
	go func() {
		defer close(serverDone)

		msg, err := harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "initialize" {
			serverDone <- errors.New("expected initialize request")
			return
		}
		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  map[string]any{"protocolVersion": 2},
		}); err != nil {
			serverDone <- err
			return
		}

		msg, err = harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "initialized" {
			serverDone <- errors.New("expected initialized notification")
			return
		}

		msg, err = harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "thread/start" {
			serverDone <- errors.New("expected thread/start request")
			return
		}
		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  map[string]any{"thread": map[string]any{"id": "thread-1"}},
		}); err != nil {
			serverDone <- err
			return
		}

		msg, err = harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "turn/start" {
			serverDone <- errors.New("expected turn/start request")
			return
		}
		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  map[string]any{"turn": map[string]any{"id": "turn-1"}},
		}); err != nil {
			serverDone <- err
			return
		}

		close(turnStarted)
		// Wait for kill to close the connection - do not expect interrupt/shutdown.
		_, _ = harness.ReadMessage(context.Background())
		serverDone <- nil
	}()

	session, err := runtime.Start(context.Background(), contracts.TaskSessionStartRequest{
		TaskID:   "task-force-cancel",
		Backend:  "codex",
		RepoRoot: repoRoot,
	})
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	appSession, ok := session.(*AppServerTaskSession)
	if !ok {
		t.Fatalf("expected AppServerTaskSession, got %T", session)
	}

	executeDone := make(chan error, 1)
	go func() {
		executeDone <- appSession.Execute(context.Background(), contracts.TaskSessionExecuteRequest{
			Prompt: "implement the task",
			Model:  "openai/gpt-5.3-codex",
			Mode:   contracts.RunnerModeImplement,
		})
	}()

	select {
	case <-turnStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for turn to start")
	}

	if err := appSession.Cancel(context.Background(), contracts.TaskSessionCancellation{Force: true}); err != nil {
		t.Fatalf("force cancel: %v", err)
	}
	if err := <-executeDone; err == nil {
		t.Fatal("expected execute to fail after force cancel")
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("server interaction failed: %v", err)
	}
	if atomic.LoadInt32(&killCalls) != 1 {
		t.Fatalf("expected exactly one kill call, got %d", atomic.LoadInt32(&killCalls))
	}
}

func TestAppServerTaskSessionTeardownAfterUnexpectedProcessExit(t *testing.T) {
	repoRoot := t.TempDir()
	harness := contracts.NewFakeStdioJSONRPCHarness()
	t.Cleanup(func() { _ = harness.Close() })
	clientWriter, clientReader := harness.ClientIO()
	serverWriter, serverReader := harness.ServerIO()

	waitCh := make(chan error, 1)
	proc := &fakeAppServerProcess{
		stdin:  clientWriter,
		stdout: clientReader,
		stderr: io.NopCloser(strings.NewReader("")),
		waitCh: waitCh,
	}
	proc.killFn = func() error {
		_ = serverWriter.Close()
		_ = serverReader.Close()
		waitCh <- nil
		return harness.Close()
	}

	runtime := NewTaskSessionRuntime("codex-bin")
	runtime.starter = appServerStarterFunc(func(_ context.Context, spec CommandSpec) (appServerProcess, error) {
		return proc, nil
	})

	serverDone := make(chan error, 1)
	go func() {
		defer close(serverDone)

		msg, err := harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "initialize" {
			serverDone <- errors.New("expected initialize request")
			return
		}
		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  map[string]any{"protocolVersion": 2},
		}); err != nil {
			serverDone <- err
			return
		}

		msg, err = harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "initialized" {
			serverDone <- errors.New("expected initialized notification")
			return
		}

		msg, err = harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "thread/start" {
			serverDone <- errors.New("expected thread/start request")
			return
		}
		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  map[string]any{"thread": map[string]any{"id": "thread-1"}},
		}); err != nil {
			serverDone <- err
			return
		}

		msg, err = harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "turn/start" {
			serverDone <- errors.New("expected turn/start request")
			return
		}
		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  map[string]any{"turn": map[string]any{"id": "turn-1"}},
		}); err != nil {
			serverDone <- err
			return
		}

		// Simulate unexpected process exit: close stdout and signal clean exit.
		_ = serverWriter.Close()
		waitCh <- nil
		serverDone <- nil
	}()

	session, err := runtime.Start(context.Background(), contracts.TaskSessionStartRequest{
		TaskID:   "task-crash",
		Backend:  "codex",
		RepoRoot: repoRoot,
	})
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	appSession, ok := session.(*AppServerTaskSession)
	if !ok {
		t.Fatalf("expected AppServerTaskSession, got %T", session)
	}

	err = appSession.Execute(context.Background(), contracts.TaskSessionExecuteRequest{
		Prompt: "implement the task",
		Model:  "openai/gpt-5.3-codex",
		Mode:   contracts.RunnerModeImplement,
	})
	if err == nil {
		t.Fatal("expected execute to fail when process exits unexpectedly")
	}

	if teardownErr := appSession.Teardown(context.Background(), contracts.TaskSessionTeardown{Reason: "cleanup after crash"}); teardownErr != nil {
		t.Fatalf("teardown after process exit should succeed, got: %v", teardownErr)
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("server interaction failed: %v", err)
	}
}

func TestCLIRunnerAdapterPreservesLogDerivedReviewReadyWhenCompletionHasNoVerdict(t *testing.T) {
	repoRoot := t.TempDir()
	adapter := NewCLIRunnerAdapter("codex-bin", commandRunnerFunc(func(_ context.Context, spec CommandSpec) error {
		_, _ = io.WriteString(spec.Stdout, "REVIEW_VERDICT: pass\n")
		return json.NewEncoder(spec.Stdout).Encode(contracts.JSONRPCMessage{
			Method: "turn/completed",
			Params: map[string]any{
				"threadId":   "thread-1",
				"turnId":     "turn-1",
				"stopReason": "end_turn",
				"output": map[string]any{
					"text": "review finished without explicit completion verdict",
				},
			},
		})
	}))

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		TaskID:   "t-review-mixed",
		RepoRoot: repoRoot,
		Prompt:   "review",
		Mode:     contracts.RunnerModeReview,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != contracts.RunnerResultCompleted {
		t.Fatalf("expected completed status, got %s", result.Status)
	}
	if !result.ReviewReady {
		t.Fatalf("expected ReviewReady=true from stdout verdict when completion contains no verdict")
	}
	if verdict := result.Artifacts["review_verdict"]; verdict != "pass" {
		t.Fatalf("expected review_verdict=pass artifact, got %#v", result.Artifacts)
	}
}

func TestCLIRunnerAdapterMapsTimeoutToBlocked(t *testing.T) {
	adapter := NewCLIRunnerAdapter("codex-bin", commandRunnerFunc(func(_ context.Context, spec CommandSpec) error {
		_, _ = io.WriteString(spec.Stdout, "still working\n")
		return context.DeadlineExceeded
	}))

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		TaskID:   "t-timeout",
		RepoRoot: t.TempDir(),
		Prompt:   "implement",
		Timeout:  10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != contracts.RunnerResultBlocked {
		t.Fatalf("expected blocked status, got %s", result.Status)
	}
	if !strings.Contains(result.Reason, "timeout") {
		t.Fatalf("expected timeout reason, got %q", result.Reason)
	}
}

func TestCLIRunnerAdapterMapsContextTimeoutToBlockedEvenWhenRunnerReturnsNil(t *testing.T) {
	adapter := NewCLIRunnerAdapter("codex-bin", commandRunnerFunc(func(_ context.Context, spec CommandSpec) error {
		_, _ = io.WriteString(spec.Stdout, "still working\n")
		time.Sleep(30 * time.Millisecond)
		return nil
	}))

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		TaskID:   "t-timeout",
		RepoRoot: t.TempDir(),
		Prompt:   "implement",
		Timeout:  5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != contracts.RunnerResultBlocked {
		t.Fatalf("expected blocked status, got %s", result.Status)
	}
	if !strings.Contains(result.Reason, "timeout") {
		t.Fatalf("expected timeout reason, got %q", result.Reason)
	}
}

func TestCLIRunnerAdapterMapsGenericErrorToFailed(t *testing.T) {
	adapter := NewCLIRunnerAdapter("codex-bin", commandRunnerFunc(func(_ context.Context, spec CommandSpec) error {
		_, _ = io.WriteString(spec.Stderr, "boom\n")
		return errors.New("codex failed")
	}))

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		TaskID:   "t-fail",
		RepoRoot: t.TempDir(),
		Prompt:   "implement",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != contracts.RunnerResultFailed {
		t.Fatalf("expected failed status, got %s", result.Status)
	}
	if !strings.Contains(result.Reason, "codex failed") {
		t.Fatalf("expected failure reason to contain codex failed, got %q", result.Reason)
	}
}

func TestAppServerRunnerAdapterRunHappyPath(t *testing.T) {
	repoRoot := t.TempDir()
	harness := contracts.NewFakeStdioJSONRPCHarness()
	clientWriter, clientReader := harness.ClientIO()
	serverWriter, serverReader := harness.ServerIO()

	waitCh := make(chan error, 1)
	proc := &fakeAppServerProcess{
		stdin:  clientWriter,
		stdout: clientReader,
		stderr: io.NopCloser(strings.NewReader("")),
		waitCh: waitCh,
	}
	proc.killFn = func() error {
		_ = serverWriter.Close()
		_ = serverReader.Close()
		waitCh <- nil
		return harness.Close()
	}

	runtime := NewTaskSessionRuntime("codex-bin")
	runtime.starter = appServerStarterFunc(func(_ context.Context, _ CommandSpec) (appServerProcess, error) {
		return proc, nil
	})
	adapter := &AppServerRunnerAdapter{runtime: runtime, now: time.Now}

	serverDone := make(chan error, 1)
	go func() {
		defer close(serverDone)

		msg, err := harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "initialize" {
			serverDone <- errors.New("expected initialize request")
			return
		}
		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  map[string]any{"protocolVersion": 2},
		}); err != nil {
			serverDone <- err
			return
		}

		msg, err = harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "initialized" {
			serverDone <- errors.New("expected initialized notification")
			return
		}

		msg, err = harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "thread/start" {
			serverDone <- errors.New("expected thread/start request")
			return
		}
		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  map[string]any{"thread": map[string]any{"id": "thread-1"}},
		}); err != nil {
			serverDone <- err
			return
		}
		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			Method:  "thread/started",
			Params:  map[string]any{"threadId": "thread-1"},
		}); err != nil {
			serverDone <- err
			return
		}

		msg, err = harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "turn/start" {
			serverDone <- errors.New("expected turn/start request")
			return
		}
		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  map[string]any{"turn": map[string]any{"id": "turn-1"}},
		}); err != nil {
			serverDone <- err
			return
		}
		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			Method:  "turn/started",
			Params:  map[string]any{"threadId": "thread-1", "turnId": "turn-1"},
		}); err != nil {
			serverDone <- err
			return
		}
		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			Method:  "item/agentMessage/delta",
			Params:  map[string]any{"threadId": "thread-1", "turnId": "turn-1", "itemId": "item-1", "delta": "working on it"},
		}); err != nil {
			serverDone <- err
			return
		}
		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			Method:  "turn/completed",
			Params: map[string]any{
				"threadId":   "thread-1",
				"turnId":     "turn-1",
				"stopReason": "end_turn",
			},
		}); err != nil {
			serverDone <- err
			return
		}

		msg, err = harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "shutdown" {
			serverDone <- errors.New("expected shutdown request")
			return
		}
		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  map[string]any{},
		}); err != nil {
			serverDone <- err
			return
		}
		_ = serverWriter.Close()
		_ = serverReader.Close()
		waitCh <- nil
		serverDone <- harness.Close()
	}()

	var progressUpdates []contracts.RunnerProgress
	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		TaskID:   "task-e2e",
		RepoRoot: repoRoot,
		Prompt:   "implement the feature",
		Model:    "openai/gpt-5.3-codex",
		Mode:     contracts.RunnerModeImplement,
		OnProgress: func(p contracts.RunnerProgress) {
			progressUpdates = append(progressUpdates, p)
		},
	})
	if err != nil {
		t.Fatalf("Run() returned unexpected error: %v", err)
	}

	if result.Status != contracts.RunnerResultCompleted {
		t.Fatalf("expected completed status, got %s", result.Status)
	}
	if result.LogPath == "" {
		t.Fatal("expected non-empty log path")
	}
	if result.Artifacts["backend"] != "codex" {
		t.Fatalf("expected backend artifact 'codex', got %q", result.Artifacts["backend"])
	}
	if len(progressUpdates) == 0 {
		t.Fatal("expected at least one progress update")
	}

	if err := <-serverDone; err != nil {
		t.Fatalf("server interaction failed: %v", err)
	}
}

func TestAppServerRunnerAdapterRunCancellationUsesGracefulTeardown(t *testing.T) {
	repoRoot := t.TempDir()
	harness := contracts.NewFakeStdioJSONRPCHarness()
	clientWriter, clientReader := harness.ClientIO()
	serverWriter, serverReader := harness.ServerIO()

	waitCh := make(chan error, 1)
	var killCalls int32
	proc := &fakeAppServerProcess{
		stdin:  clientWriter,
		stdout: clientReader,
		stderr: io.NopCloser(strings.NewReader("")),
		waitCh: waitCh,
	}
	proc.killFn = func() error {
		atomic.AddInt32(&killCalls, 1)
		_ = serverWriter.Close()
		_ = serverReader.Close()
		waitCh <- nil
		return harness.Close()
	}

	runtime := NewTaskSessionRuntime("codex-bin")
	runtime.starter = appServerStarterFunc(func(_ context.Context, _ CommandSpec) (appServerProcess, error) {
		return proc, nil
	})
	adapter := &AppServerRunnerAdapter{runtime: runtime, now: time.Now}

	executionStarted := make(chan struct{})
	serverDone := make(chan error, 1)
	go func() {
		defer close(serverDone)

		msg, err := harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "initialize" {
			serverDone <- errors.New("expected initialize request")
			return
		}
		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  map[string]any{"protocolVersion": 2},
		}); err != nil {
			serverDone <- err
			return
		}

		msg, err = harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "initialized" {
			serverDone <- errors.New("expected initialized notification")
			return
		}

		msg, err = harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "thread/start" {
			serverDone <- errors.New("expected thread/start request")
			return
		}
		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  map[string]any{"thread": map[string]any{"id": "thread-1"}},
		}); err != nil {
			serverDone <- err
			return
		}

		msg, err = harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "turn/start" {
			serverDone <- errors.New("expected turn/start request")
			return
		}
		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  map[string]any{"turn": map[string]any{"id": "turn-1"}},
		}); err != nil {
			serverDone <- err
			return
		}

		close(executionStarted)

		msg, err = harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "shutdown" {
			serverDone <- fmt.Errorf("expected shutdown request after cancellation, got %q", msg.Method)
			return
		}
		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  map[string]any{},
		}); err != nil {
			serverDone <- err
			return
		}

		_ = serverWriter.Close()
		_ = serverReader.Close()
		waitCh <- nil
		serverDone <- harness.Close()
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runDone := make(chan contracts.RunnerResult, 1)
	go func() {
		result, _ := adapter.Run(ctx, contracts.RunnerRequest{
			TaskID:   "task-cancel-e2e",
			RepoRoot: repoRoot,
			Prompt:   "implement",
			Model:    "openai/gpt-5.3-codex",
			Mode:     contracts.RunnerModeImplement,
		})
		runDone <- result
	}()

	<-executionStarted
	cancel()

	result := <-runDone
	if result.Status != contracts.RunnerResultFailed {
		t.Fatalf("expected failed status after cancellation, got %s", result.Status)
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("server interaction failed: %v", err)
	}
	if atomic.LoadInt32(&killCalls) != 0 {
		t.Fatalf("expected graceful teardown after cancellation (no kill), got %d kill calls", atomic.LoadInt32(&killCalls))
	}
}

func TestAppServerRunnerAdapterRunExecuteFailureForcesKillTeardown(t *testing.T) {
	repoRoot := t.TempDir()
	harness := contracts.NewFakeStdioJSONRPCHarness()
	clientWriter, clientReader := harness.ClientIO()
	serverWriter, serverReader := harness.ServerIO()

	waitCh := make(chan error, 1)
	var killCalls int32
	proc := &fakeAppServerProcess{
		stdin:  clientWriter,
		stdout: clientReader,
		stderr: io.NopCloser(strings.NewReader("")),
		waitCh: waitCh,
	}
	proc.killFn = func() error {
		atomic.AddInt32(&killCalls, 1)
		return nil
	}

	runtime := NewTaskSessionRuntime("codex-bin")
	runtime.starter = appServerStarterFunc(func(_ context.Context, _ CommandSpec) (appServerProcess, error) {
		return proc, nil
	})
	adapter := &AppServerRunnerAdapter{runtime: runtime, now: time.Now}

	serverDone := make(chan error, 1)
	go func() {
		defer close(serverDone)

		msg, err := harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "initialize" {
			serverDone <- errors.New("expected initialize request")
			return
		}
		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  map[string]any{"protocolVersion": 2},
		}); err != nil {
			serverDone <- err
			return
		}

		msg, err = harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "initialized" {
			serverDone <- errors.New("expected initialized notification")
			return
		}

		msg, err = harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "thread/start" {
			serverDone <- errors.New("expected thread/start request")
			return
		}
		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  map[string]any{"thread": map[string]any{"id": "thread-1"}},
		}); err != nil {
			serverDone <- err
			return
		}

		msg, err = harness.ReadMessage(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "turn/start" {
			serverDone <- errors.New("expected turn/start request")
			return
		}
		if err := harness.SendMessage(contracts.JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  map[string]any{"turn": map[string]any{"id": "turn-1"}},
		}); err != nil {
			serverDone <- err
			return
		}

		// Simulate unexpected process death: close pipes and signal process exit with error.
		_ = serverWriter.Close()
		_ = serverReader.Close()
		waitCh <- errors.New("exit status 1")
		serverDone <- harness.Close()
	}()

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		TaskID:   "task-failure-e2e",
		RepoRoot: repoRoot,
		Prompt:   "implement",
		Model:    "openai/gpt-5.3-codex",
		Mode:     contracts.RunnerModeImplement,
	})
	if err != nil {
		t.Fatalf("Run() returned unexpected error: %v", err)
	}
	if result.Status != contracts.RunnerResultFailed {
		t.Fatalf("expected failed status after execute failure, got %s", result.Status)
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("server interaction failed: %v", err)
	}
	if atomic.LoadInt32(&killCalls) != 1 {
		t.Fatalf("expected forced kill after execute failure, got %d kill calls", atomic.LoadInt32(&killCalls))
	}
}

func TestAppServerRunnerAdapterRunStartFailureReturnsFailedResult(t *testing.T) {
	repoRoot := t.TempDir()
	runtime := NewTaskSessionRuntime("codex-bin")
	runtime.starter = appServerStarterFunc(func(_ context.Context, _ CommandSpec) (appServerProcess, error) {
		return nil, errors.New("process start failed")
	})
	adapter := &AppServerRunnerAdapter{runtime: runtime, now: time.Now}

	result, err := adapter.Run(context.Background(), contracts.RunnerRequest{
		TaskID:   "task-start-fail",
		RepoRoot: repoRoot,
		Prompt:   "implement",
		Model:    "openai/gpt-5.3-codex",
		Mode:     contracts.RunnerModeImplement,
	})
	if err != nil {
		t.Fatalf("Run() returned unexpected error: %v", err)
	}
	if result.Status != contracts.RunnerResultFailed {
		t.Fatalf("expected failed status when start fails, got %s", result.Status)
	}
}
