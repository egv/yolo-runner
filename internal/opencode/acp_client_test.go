package opencode

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
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
	handler := NewACPHandler("issue-1", "log", func(_ string, _ string, kind string, outcome string, _ string) error {
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
		handler: NewACPHandler("issue-1", "log", nil),
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

	responses := client.drainQuestionResponses()
	if !reflect.DeepEqual(responses, []string{"decide yourself"}) {
		t.Fatalf("expected queued response, got %#v", responses)
	}
}

func TestACPClientQueuesMultipleQuestionResponses(t *testing.T) {
	client := &acpClient{
		handler: NewACPHandler("issue-1", "log", nil),
	}
	questionKind := acp.ToolKind("question")
	request := &acp.RequestPermissionRequest{
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
	}

	if _, err := client.RequestPermission(context.Background(), request); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := client.RequestPermission(context.Background(), request); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	responses := client.drainQuestionResponses()
	if !reflect.DeepEqual(responses, []string{"decide yourself", "decide yourself"}) {
		t.Fatalf("expected queued responses, got %#v", responses)
	}
}

func TestSendQuestionResponsesDrainsQueue(t *testing.T) {
	responses := []string{"first", "second"}

	var got []string
	promptFn := func(text string) error {
		got = append(got, text)
		return nil
	}

	if err := sendQuestionResponses(context.Background(), promptFn, responses); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !reflect.DeepEqual(got, []string{"first", "second"}) {
		t.Fatalf("unexpected prompts: %#v", got)
	}
}

func TestParseVerificationResponse(t *testing.T) {
	verified, ok := parseVerificationResponse("DONE")
	if !ok || !verified {
		t.Fatalf("expected DONE to verify, got ok=%v verified=%v", ok, verified)
	}

	verified, ok = parseVerificationResponse("NOT DONE")
	if !ok || verified {
		t.Fatalf("expected NOT DONE to fail, got ok=%v verified=%v", ok, verified)
	}

	verified, ok = parseVerificationResponse("All tests pass")
	if !ok || !verified {
		t.Fatalf("expected pass to verify, got ok=%v verified=%v", ok, verified)
	}

	verified, ok = parseVerificationResponse("Task appears complete")
	if !ok || !verified {
		t.Fatalf("expected complete to verify, got ok=%v verified=%v", ok, verified)
	}

	verified, ok = parseVerificationResponse("Tests failed")
	if !ok || verified {
		t.Fatalf("expected failed to be NOT DONE, got ok=%v verified=%v", ok, verified)
	}

	verified, ok = parseVerificationResponse("still working")
	if ok || verified {
		t.Fatalf("expected unknown response to be unrecognized")
	}
}

func TestSendQuestionResponsesSendsInOrder(t *testing.T) {
	responses := []string{"first", "second", "third"}

	var got []string
	promptFn := func(text string) error {
		got = append(got, text)
		return nil
	}

	if err := sendQuestionResponses(context.Background(), promptFn, responses); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !reflect.DeepEqual(got, []string{"first", "second", "third"}) {
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
	clientToAgentReader, clientToAgentWriter := io.Pipe()
	agentToClientReader, agentToClientWriter := io.Pipe()

	serverErr := make(chan error, 1)
	go func() {
		agent := &testACPAgent{}
		agentConn := acp.NewAgentSideConnection(agent, clientToAgentReader, agentToClientWriter)
		agent.client = agentConn.Client()
		err := agentConn.Start(context.Background())
		_ = agentToClientWriter.Close()
		serverErr <- err
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	timeout := 500 * time.Millisecond
	runErr := make(chan error, 1)
	go func() {
		runErr <- RunACPClient(ctx, clientToAgentWriter, agentToClientReader, t.TempDir(), "hi", nil, nil)
	}()

	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("expected RunACPClient to return without error, got %v", err)
		}
	case <-time.After(timeout):
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

func TestRunACPClientDoesNotHangWhenAgentKeepsStdoutOpen(t *testing.T) {
	clientToAgentReader, clientToAgentWriter := io.Pipe()
	agentToClientReader, agentToClientWriter := io.Pipe()

	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		scanner := bufio.NewScanner(clientToAgentReader)
		sessionCounter := 0
		for scanner.Scan() {
			var msg struct {
				Jsonrpc string          `json:"jsonrpc"`
				ID      *int64          `json:"id,omitempty"`
				Method  string          `json:"method,omitempty"`
				Params  json.RawMessage `json:"params,omitempty"`
			}
			if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
				continue
			}
			if msg.Method == "" {
				continue
			}
			// Notifications have no ID; ignore.
			if msg.ID == nil {
				continue
			}

			writeResponse := func(id int64, result any) {
				payload, _ := json.Marshal(result)
				resp := struct {
					Jsonrpc string          `json:"jsonrpc"`
					ID      int64           `json:"id"`
					Result  json.RawMessage `json:"result"`
				}{Jsonrpc: "2.0", ID: id, Result: payload}
				data, _ := json.Marshal(resp)
				data = append(data, '\n')
				_, _ = agentToClientWriter.Write(data)
			}

			switch msg.Method {
			case acp.AgentMethods.Initialize:
				writeResponse(*msg.ID, &acp.InitializeResponse{ProtocolVersion: acp.ProtocolVersion(acp.CurrentProtocolVersion)})
			case acp.AgentMethods.SessionNew:
				sessionCounter++
				writeResponse(*msg.ID, &acp.NewSessionResponse{SessionId: acp.SessionId(fmt.Sprintf("session-%d", sessionCounter))})
			case acp.AgentMethods.SessionSetMode:
				writeResponse(*msg.ID, map[string]any{})
			case acp.AgentMethods.SessionPrompt:
				var req acp.PromptRequest
				_ = json.Unmarshal(msg.Params, &req)
				text := ""
				if len(req.Prompt) > 0 && req.Prompt[0].IsText() {
					text = req.Prompt[0].GetText().Text
				}
				if strings.TrimSpace(text) == verificationPrompt {
					note := &acp.SessionNotification{
						SessionId: req.SessionId,
						Update:    acp.NewSessionUpdateAgentMessageChunk(acp.NewContentBlockText("DONE")),
					}
					params, _ := json.Marshal(note)
					n := struct {
						Jsonrpc string          `json:"jsonrpc"`
						Method  string          `json:"method"`
						Params  json.RawMessage `json:"params"`
					}{Jsonrpc: "2.0", Method: acp.ClientMethods.SessionUpdate, Params: params}
					data, _ := json.Marshal(n)
					data = append(data, '\n')
					_, _ = agentToClientWriter.Write(data)
				}
				writeResponse(*msg.ID, &acp.PromptResponse{StopReason: acp.StopReasonEndTurn})
			default:
				// Return an empty success to keep the client moving.
				writeResponse(*msg.ID, map[string]any{})
			}
		}
		// Intentionally do not close agentToClientWriter to mimic opencode keeping stdout open.
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)

	runErr := make(chan error, 1)
	go func() {
		runErr <- RunACPClient(ctx, clientToAgentWriter, agentToClientReader, t.TempDir(), "do work", nil, nil)
	}()

	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("expected RunACPClient to return without error, got %v", err)
		}
	case <-time.After(750 * time.Millisecond):
		t.Fatalf("expected RunACPClient to return quickly even if stdout stays open")
	}

	select {
	case <-serverDone:
		// ok
	case <-time.After(500 * time.Millisecond):
		// The server goroutine should stop once stdin is closed.
		t.Fatalf("expected server to stop after client shutdown")
	}
}

func TestRunACPClientSendsVerificationPrompt(t *testing.T) {
	clientToAgentReader, clientToAgentWriter := io.Pipe()
	agentToClientReader, agentToClientWriter := io.Pipe()

	serverErr := make(chan error, 1)
	agent := &testACPAgent{verifyResult: "DONE"}
	go func() {
		agentConn := acp.NewAgentSideConnection(agent, clientToAgentReader, agentToClientWriter)
		agent.client = agentConn.Client()
		err := agentConn.Start(context.Background())
		_ = agentToClientWriter.Close()
		serverErr <- err
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	if err := RunACPClient(ctx, clientToAgentWriter, agentToClientReader, t.TempDir(), "do work", nil, nil); err != nil {
		t.Fatalf("RunACPClient error: %v", err)
	}

	records := agent.getPromptRecords()
	if len(records) < 2 {
		t.Fatalf("expected at least 2 prompts, got %d", len(records))
	}
	if records[1].Text != verificationPrompt {
		t.Fatalf("expected verification prompt, got %q", records[1].Text)
	}
	if records[0].SessionId == records[1].SessionId {
		t.Fatalf("expected verification prompt in a new session")
	}

	select {
	case err := <-serverErr:
		if err != nil && !errors.Is(err, io.EOF) {
			t.Fatalf("unexpected server error: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("expected server connection to close")
	}
}

func TestRunACPClientHandlesDelayedVerification(t *testing.T) {
	clientToAgentReader, clientToAgentWriter := io.Pipe()
	agentToClientReader, agentToClientWriter := io.Pipe()

	serverErr := make(chan error, 1)
	agent := &testACPAgent{verifyResult: "DONE", verifyDelay: 100 * time.Millisecond}
	go func() {
		agentConn := acp.NewAgentSideConnection(agent, clientToAgentReader, agentToClientWriter)
		agent.client = agentConn.Client()
		err := agentConn.Start(context.Background())
		_ = agentToClientWriter.Close()
		serverErr <- err
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	if err := RunACPClient(ctx, clientToAgentWriter, agentToClientReader, t.TempDir(), "do work", nil, nil); err != nil {
		t.Fatalf("RunACPClient error: %v", err)
	}

	select {
	case err := <-serverErr:
		if err != nil && !errors.Is(err, io.EOF) {
			t.Fatalf("unexpected server error: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("expected server connection to close")
	}
}

func TestRunACPClientRetriesAfterNotDone(t *testing.T) {
	clientToAgentReader, clientToAgentWriter := io.Pipe()
	agentToClientReader, agentToClientWriter := io.Pipe()

	serverErr := make(chan error, 1)
	agent := &testACPAgent{verifyResults: []string{"NOT DONE", "DONE"}}
	go func() {
		agentConn := acp.NewAgentSideConnection(agent, clientToAgentReader, agentToClientWriter)
		agent.client = agentConn.Client()
		err := agentConn.Start(context.Background())
		_ = agentToClientWriter.Close()
		serverErr <- err
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	if err := RunACPClient(ctx, clientToAgentWriter, agentToClientReader, t.TempDir(), "do work", nil, nil); err != nil {
		t.Fatalf("RunACPClient error: %v", err)
	}

	records := agent.getPromptRecords()
	if len(records) < 4 {
		t.Fatalf("expected at least 4 prompts, got %d", len(records))
	}
	if records[1].Text != verificationPrompt || records[3].Text != verificationPrompt {
		t.Fatalf("expected verification prompts at steps 2 and 4")
	}
	if records[0].Text == verificationPrompt || records[2].Text == verificationPrompt {
		t.Fatalf("expected task prompts before verification")
	}

	select {
	case err := <-serverErr:
		if err != nil && !errors.Is(err, io.EOF) {
			t.Fatalf("unexpected server error: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("expected server connection to close")
	}
}

type promptRecord struct {
	SessionId acp.SessionId
	Text      string
}

type testACPAgent struct {
	client        acp.Client
	prompts       []promptRecord
	promptCount   int
	verifyResult  string
	verifyResults []string
	verifyDelay   time.Duration
	sessionCount  int
	mu            sync.Mutex
}

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
	a.mu.Lock()
	a.sessionCount++
	count := a.sessionCount
	a.mu.Unlock()
	return &acp.NewSessionResponse{SessionId: acp.SessionId("session-" + fmt.Sprint(count))}, nil
}

func (a *testACPAgent) LoadSession(ctx context.Context, params *acp.LoadSessionRequest) (*acp.LoadSessionResponse, error) {
	return &acp.LoadSessionResponse{}, nil
}

func (a *testACPAgent) SetSessionMode(ctx context.Context, params *acp.SetSessionModeRequest) error {
	return nil
}

func (a *testACPAgent) Prompt(ctx context.Context, params *acp.PromptRequest) (*acp.PromptResponse, error) {
	text := ""
	if len(params.Prompt) > 0 && params.Prompt[0].IsText() {
		text = params.Prompt[0].GetText().Text
	}
	a.mu.Lock()
	a.prompts = append(a.prompts, promptRecord{SessionId: params.SessionId, Text: text})
	a.promptCount++
	a.mu.Unlock()
	if text == verificationPrompt && a.client != nil {
		response := a.verifyResult
		if len(a.verifyResults) > 0 {
			response = a.verifyResults[0]
			if len(a.verifyResults) > 1 {
				a.mu.Lock()
				a.verifyResults = a.verifyResults[1:]
				a.mu.Unlock()
			}
		}
		if response == "" {
			response = "DONE"
		}
		delay := a.verifyDelay
		go func(sessionId acp.SessionId, reply string) {
			if delay > 0 {
				time.Sleep(delay)
			}
			_ = a.client.SessionUpdate(context.Background(), &acp.SessionNotification{
				SessionId: sessionId,
				Update:    acp.NewSessionUpdateAgentMessageChunk(acp.NewContentBlockText(reply)),
			})
		}(params.SessionId, response)
	}
	return &acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
}

func (a *testACPAgent) getPrompts() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	result := make([]string, 0, len(a.prompts))
	for _, prompt := range a.prompts {
		result = append(result, prompt.Text)
	}
	return result
}

func (a *testACPAgent) getPromptRecords() []promptRecord {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]promptRecord{}, a.prompts...)
}

func (a *testACPAgent) Cancel(ctx context.Context, params *acp.CancelNotification) error {
	return nil
}
