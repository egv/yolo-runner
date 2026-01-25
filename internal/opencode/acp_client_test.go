package opencode

import (
	"context"
	"errors"
	"io"
	"net"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	acp "github.com/ironpark/acp-go"
)

func TestACPHandlerAutoApprovesPermission(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "log.jsonl")
	handler := NewACPHandler("issue-1", logPath, nil)
	decision := handler.HandlePermission(context.Background(), "perm-1", "repo.write")
	if decision != ACPDecisionAllow {
		t.Fatalf("expected allow, got %v", decision)
	}
}

func TestACPClientCancelsQuestionPermission(t *testing.T) {
	var gotKind string
	var gotOutcome string
	handler := NewACPHandler("issue-1", "log", func(_ string, _ string, kind string, outcome string) error {
		gotKind = kind
		gotOutcome = outcome
		return nil
	})
	client := &acpClient{handler: handler}
	questionKind := acp.ToolKind("question")

	response, err := client.RequestPermission(context.Background(), &acp.RequestPermissionRequest{
		ToolCall: acp.ToolCallUpdate{
			ToolCallId: acp.ToolCallId("tool-1"),
			Title:      "Need input",
			Kind:       &questionKind,
		},
		Options: []acp.PermissionOption{
			{
				Kind:     acp.PermissionOptionKindAllowOnce,
				Name:     "Allow",
				OptionId: acp.PermissionOptionId("allow"),
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotKind != "question" {
		t.Fatalf("expected question handler, got %q", gotKind)
	}
	if gotOutcome != "decide yourself" {
		t.Fatalf("expected question outcome, got %q", gotOutcome)
	}

	expected := acp.NewRequestPermissionOutcomeCancelled()
	if !reflect.DeepEqual(response.Outcome, expected) {
		t.Fatalf("expected cancelled outcome, got %#v", response.Outcome)
	}
}

func TestACPClientQuestionQueuesResponse(t *testing.T) {
	client := &acpClient{
		handler:           NewACPHandler("issue-1", "log", nil),
		questionResponses: make(chan string, 1),
	}
	questionKind := acp.ToolKind("question")

	_, err := client.RequestPermission(context.Background(), &acp.RequestPermissionRequest{
		ToolCall: acp.ToolCallUpdate{
			ToolCallId: acp.ToolCallId("tool-1"),
			Title:      "Need input",
			Kind:       &questionKind,
		},
		Options: []acp.PermissionOption{
			{
				Kind:     acp.PermissionOptionKindAllowOnce,
				Name:     "Allow",
				OptionId: acp.PermissionOptionId("allow"),
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	select {
	case got := <-client.questionResponses:
		if got != "decide yourself" {
			t.Fatalf("expected queued response, got %q", got)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("expected queued response")
	}
}

func TestSendQuestionResponsesDrainsQueue(t *testing.T) {
	responses := make(chan string, 2)
	responses <- "first"
	responses <- "second"

	var got []string
	promptFn := func(text string) error {
		got = append(got, text)
		return nil
	}

	if err := sendQuestionResponses(context.Background(), promptFn, responses); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(responses) != 0 {
		t.Fatalf("expected queue to be drained, got %d", len(responses))
	}

	if !reflect.DeepEqual(got, []string{"first", "second"}) {
		t.Fatalf("unexpected prompts: %#v", got)
	}
}

func TestACPClientSessionUpdateCallback(t *testing.T) {
	called := false
	client := &acpClient{
		onUpdate: func(_ *acp.SessionNotification) {
			called = true
		},
	}

	if err := client.SessionUpdate(context.Background(), &acp.SessionNotification{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !called {
		t.Fatalf("expected session update callback to be called")
	}
}

func TestACPClientSelectsAllowOption(t *testing.T) {
	handler := NewACPHandler("issue-1", "log", nil)
	client := &acpClient{handler: handler}

	response, err := client.RequestPermission(context.Background(), &acp.RequestPermissionRequest{
		ToolCall: acp.ToolCallUpdate{
			ToolCallId: acp.ToolCallId("tool-1"),
			Title:      "Update file",
			Kind:       acp.ToolKindPtr(acp.ToolKindEdit),
		},
		Options: []acp.PermissionOption{
			{
				Kind:     acp.PermissionOptionKindRejectOnce,
				Name:     "Reject",
				OptionId: acp.PermissionOptionId("reject"),
			},
			{
				Kind:     acp.PermissionOptionKindAllowOnce,
				Name:     "Allow",
				OptionId: acp.PermissionOptionId("allow"),
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	selected := response.Outcome.GetSelected()
	if selected == nil || selected.OptionId != "allow" {
		t.Fatalf("expected allow option selected, got %#v", response.Outcome)
	}
}

func TestRunACPClientReturnsAfterPrompt(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer listener.Close()

	acceptedConn := make(chan net.Conn, 1)
	serverErr := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		acceptedConn <- conn
		agent := &testACPAgent{}
		agentConn := acp.NewAgentSideConnection(agent, conn, conn)
		serverErr <- agentConn.Start(context.Background())
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	timeout := 500 * time.Millisecond
	runErr := make(chan error, 1)
	go func() {
		runErr <- RunACPClient(ctx, listener.Addr().String(), t.TempDir(), "hi", nil, nil)
	}()

	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("expected RunACPClient to return without error, got %v", err)
		}
	case <-time.After(timeout):
		select {
		case conn := <-acceptedConn:
			_ = conn.Close()
		default:
		}
		cancel()
		t.Fatalf("expected RunACPClient to return within %s", timeout)
	}

	select {
	case err := <-serverErr:
		if err != nil && !errors.Is(err, io.EOF) {
			t.Fatalf("unexpected server error: %v", err)
		}
	case <-time.After(timeout):
		t.Fatalf("expected server connection to close")
	}
}

func TestDialACPRespectsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := dialACP(ctx, "127.0.0.1:0")
	if err == nil {
		t.Fatalf("expected error from canceled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled error, got %v", err)
	}
}

type testACPAgent struct{}

func (a *testACPAgent) Initialize(ctx context.Context, params *acp.InitializeRequest) (*acp.InitializeResponse, error) {
	return &acp.InitializeResponse{
		ProtocolVersion: acp.ProtocolVersion(acp.CurrentProtocolVersion),
		AgentCapabilities: &acp.AgentCapabilities{
			LoadSession: false,
		},
		AuthMethods: []acp.AuthMethod{},
	}, nil
}

func (a *testACPAgent) Authenticate(ctx context.Context, params *acp.AuthenticateRequest) error {
	return nil
}

func (a *testACPAgent) NewSession(ctx context.Context, params *acp.NewSessionRequest) (*acp.NewSessionResponse, error) {
	return &acp.NewSessionResponse{SessionId: acp.SessionId("session-1")}, nil
}

func (a *testACPAgent) LoadSession(ctx context.Context, params *acp.LoadSessionRequest) (*acp.LoadSessionResponse, error) {
	return &acp.LoadSessionResponse{}, nil
}

func (a *testACPAgent) SetSessionMode(ctx context.Context, params *acp.SetSessionModeRequest) error {
	return nil
}

func (a *testACPAgent) Prompt(ctx context.Context, params *acp.PromptRequest) (*acp.PromptResponse, error) {
	return &acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
}

func (a *testACPAgent) Cancel(ctx context.Context, params *acp.CancelNotification) error {
	return nil
}
