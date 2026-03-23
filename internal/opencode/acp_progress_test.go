package opencode

import (
	"strings"
	"testing"

	"github.com/egv/yolo-runner/v2/internal/contracts"
	acp "github.com/ironpark/acp-go"
)

func TestNormalizeACPProgressNotificationNilReturnsNotOk(t *testing.T) {
	_, ok := NormalizeACPProgressNotification(nil)
	if ok {
		t.Fatalf("expected ok=false for nil notification")
	}
}

func TestNormalizeACPProgressNotificationToolCallPendingIsCommandStarted(t *testing.T) {
	notification := &acp.SessionNotification{
		SessionId: "sess-1",
		Update: acp.NewSessionUpdateToolCall(
			acp.ToolCallId("tool-1"),
			"Read file",
			acp.ToolKindPtr(acp.ToolKindRead),
			acp.ToolCallStatusPtr(acp.ToolCallStatusPending),
			nil, nil,
		),
	}

	progress, ok := NormalizeACPProgressNotification(notification)
	if !ok {
		t.Fatalf("expected ok=true for tool call")
	}
	if progress.Type != string(contracts.EventTypeRunnerCommandStarted) {
		t.Fatalf("expected %q, got %q", contracts.EventTypeRunnerCommandStarted, progress.Type)
	}
	if progress.Message == "" {
		t.Fatalf("expected non-empty message")
	}
}

func TestNormalizeACPProgressNotificationToolCallInProgressIsCommandStarted(t *testing.T) {
	notification := &acp.SessionNotification{
		SessionId: "sess-1",
		Update: acp.NewSessionUpdateToolCall(
			acp.ToolCallId("tool-2"),
			"Write file",
			acp.ToolKindPtr(acp.ToolKindEdit),
			acp.ToolCallStatusPtr(acp.ToolCallStatusInProgress),
			nil, nil,
		),
	}

	progress, ok := NormalizeACPProgressNotification(notification)
	if !ok {
		t.Fatalf("expected ok=true for in-progress tool call")
	}
	if progress.Type != string(contracts.EventTypeRunnerCommandStarted) {
		t.Fatalf("expected %q, got %q", contracts.EventTypeRunnerCommandStarted, progress.Type)
	}
}

func TestNormalizeACPProgressNotificationToolCallCompletedIsCommandFinished(t *testing.T) {
	notification := &acp.SessionNotification{
		SessionId: "sess-1",
		Update: acp.NewSessionUpdateToolCall(
			acp.ToolCallId("tool-3"),
			"Execute command",
			acp.ToolKindPtr(acp.ToolKindExecute),
			acp.ToolCallStatusPtr(acp.ToolCallStatusCompleted),
			nil, nil,
		),
	}

	progress, ok := NormalizeACPProgressNotification(notification)
	if !ok {
		t.Fatalf("expected ok=true for completed tool call")
	}
	if progress.Type != string(contracts.EventTypeRunnerCommandFinished) {
		t.Fatalf("expected %q, got %q", contracts.EventTypeRunnerCommandFinished, progress.Type)
	}
}

func TestNormalizeACPProgressNotificationToolCallFailedIsCommandFinished(t *testing.T) {
	notification := &acp.SessionNotification{
		SessionId: "sess-1",
		Update: acp.NewSessionUpdateToolCall(
			acp.ToolCallId("tool-4"),
			"Search files",
			acp.ToolKindPtr(acp.ToolKindSearch),
			acp.ToolCallStatusPtr(acp.ToolCallStatusFailed),
			nil, nil,
		),
	}

	progress, ok := NormalizeACPProgressNotification(notification)
	if !ok {
		t.Fatalf("expected ok=true for failed tool call")
	}
	if progress.Type != string(contracts.EventTypeRunnerCommandFinished) {
		t.Fatalf("expected %q, got %q", contracts.EventTypeRunnerCommandFinished, progress.Type)
	}
}

func TestNormalizeACPProgressNotificationToolCallNilStatusIsCommandStarted(t *testing.T) {
	notification := &acp.SessionNotification{
		SessionId: "sess-1",
		Update: acp.NewSessionUpdateToolCall(
			acp.ToolCallId("tool-5"),
			"Fetch URL",
			acp.ToolKindPtr(acp.ToolKindFetch),
			nil,
			nil, nil,
		),
	}

	progress, ok := NormalizeACPProgressNotification(notification)
	if !ok {
		t.Fatalf("expected ok=true for nil-status tool call")
	}
	if progress.Type != string(contracts.EventTypeRunnerCommandStarted) {
		t.Fatalf("expected %q, got %q", contracts.EventTypeRunnerCommandStarted, progress.Type)
	}
}

func TestNormalizeACPProgressNotificationToolCallIncludesMetadata(t *testing.T) {
	notification := &acp.SessionNotification{
		SessionId: "sess-42",
		Update: acp.NewSessionUpdateToolCall(
			acp.ToolCallId("tc-99"),
			"Read file",
			acp.ToolKindPtr(acp.ToolKindRead),
			acp.ToolCallStatusPtr(acp.ToolCallStatusPending),
			nil, nil,
		),
	}

	progress, ok := NormalizeACPProgressNotification(notification)
	if !ok {
		t.Fatalf("expected ok=true")
	}
	if progress.Metadata["tool_call_id"] != "tc-99" {
		t.Fatalf("expected tool_call_id=tc-99, got %q", progress.Metadata["tool_call_id"])
	}
	if progress.Metadata["kind"] != "read" {
		t.Fatalf("expected kind=read, got %q", progress.Metadata["kind"])
	}
	if progress.Metadata["status"] != "pending" {
		t.Fatalf("expected status=pending, got %q", progress.Metadata["status"])
	}
	if progress.Metadata["session_id"] != "sess-42" {
		t.Fatalf("expected session_id=sess-42, got %q", progress.Metadata["session_id"])
	}
}

func TestNormalizeACPProgressNotificationAgentMessageIsRunnerOutput(t *testing.T) {
	notification := &acp.SessionNotification{
		SessionId: "sess-1",
		Update:    acp.NewSessionUpdateAgentMessageChunk(acp.NewContentBlockText("Hello world")),
	}

	progress, ok := NormalizeACPProgressNotification(notification)
	if !ok {
		t.Fatalf("expected ok=true for agent message chunk")
	}
	if progress.Type != string(contracts.EventTypeRunnerOutput) {
		t.Fatalf("expected %q, got %q", contracts.EventTypeRunnerOutput, progress.Type)
	}
	if progress.Message != "Hello world" {
		t.Fatalf("expected message 'Hello world', got %q", progress.Message)
	}
}

func TestNormalizeACPProgressNotificationAgentThoughtIsRunnerOutput(t *testing.T) {
	notification := &acp.SessionNotification{
		SessionId: "sess-1",
		Update:    acp.NewSessionUpdateAgentThoughtChunk(acp.NewContentBlockText("Thinking...")),
	}

	progress, ok := NormalizeACPProgressNotification(notification)
	if !ok {
		t.Fatalf("expected ok=true for agent thought chunk")
	}
	if progress.Type != string(contracts.EventTypeRunnerOutput) {
		t.Fatalf("expected %q, got %q", contracts.EventTypeRunnerOutput, progress.Type)
	}
	if progress.Message != "Thinking..." {
		t.Fatalf("expected 'Thinking...', got %q", progress.Message)
	}
}

func TestNormalizeACPProgressNotificationEmptyAgentMessageIsNotOk(t *testing.T) {
	notification := &acp.SessionNotification{
		SessionId: "sess-1",
		Update:    acp.NewSessionUpdateAgentMessageChunk(acp.NewContentBlockText("")),
	}

	_, ok := NormalizeACPProgressNotification(notification)
	if ok {
		t.Fatalf("expected ok=false for empty agent message")
	}
}

func TestNormalizeACPProgressNotificationToolCallUpdateCompletedIsCommandFinished(t *testing.T) {
	notification := &acp.SessionNotification{
		SessionId: "sess-1",
		Update: acp.NewSessionUpdateToolCallUpdate(
			acp.ToolCallId("tool-1"),
			acp.ToolCallStatusPtr(acp.ToolCallStatusCompleted),
			nil, nil,
		),
	}
	notification.Update.GetToolcallupdate().Title = "Read file"

	progress, ok := NormalizeACPProgressNotification(notification)
	if !ok {
		t.Fatalf("expected ok=true for tool call update")
	}
	if progress.Type != string(contracts.EventTypeRunnerCommandFinished) {
		t.Fatalf("expected %q, got %q", contracts.EventTypeRunnerCommandFinished, progress.Type)
	}
}

func TestNormalizeACPProgressNotificationToolCallUpdateInProgressIsCommandStarted(t *testing.T) {
	notification := &acp.SessionNotification{
		SessionId: "sess-1",
		Update: acp.NewSessionUpdateToolCallUpdate(
			acp.ToolCallId("tool-1"),
			acp.ToolCallStatusPtr(acp.ToolCallStatusInProgress),
			nil, nil,
		),
	}

	progress, ok := NormalizeACPProgressNotification(notification)
	if !ok {
		t.Fatalf("expected ok=true for in-progress tool call update")
	}
	if progress.Type != string(contracts.EventTypeRunnerCommandStarted) {
		t.Fatalf("expected %q, got %q", contracts.EventTypeRunnerCommandStarted, progress.Type)
	}
}

func TestNormalizeACPProgressNotificationPlanIsRunnerProgress(t *testing.T) {
	notification := &acp.SessionNotification{
		SessionId: "sess-1",
		Update:    acp.NewSessionUpdatePlan(nil),
	}

	progress, ok := NormalizeACPProgressNotification(notification)
	if !ok {
		t.Fatalf("expected ok=true for plan update")
	}
	if progress.Type != string(contracts.EventTypeRunnerProgress) {
		t.Fatalf("expected %q, got %q", contracts.EventTypeRunnerProgress, progress.Type)
	}
}

func TestNormalizeACPProgressNotificationTimestampIsSet(t *testing.T) {
	notification := &acp.SessionNotification{
		SessionId: "sess-1",
		Update:    acp.NewSessionUpdateAgentMessageChunk(acp.NewContentBlockText("hi")),
	}

	progress, ok := NormalizeACPProgressNotification(notification)
	if !ok {
		t.Fatalf("expected ok=true")
	}
	if progress.Timestamp.IsZero() {
		t.Fatalf("expected non-zero timestamp")
	}
}

func TestNormalizeACPProgressNotificationToolCallMessageContainsTitle(t *testing.T) {
	notification := &acp.SessionNotification{
		SessionId: "sess-1",
		Update: acp.NewSessionUpdateToolCall(
			acp.ToolCallId("tool-1"),
			"Read /etc/hosts",
			acp.ToolKindPtr(acp.ToolKindRead),
			acp.ToolCallStatusPtr(acp.ToolCallStatusPending),
			nil, nil,
		),
	}

	progress, ok := NormalizeACPProgressNotification(notification)
	if !ok {
		t.Fatalf("expected ok=true")
	}
	if !strings.Contains(progress.Message, "Read /etc/hosts") {
		t.Fatalf("expected message to contain title, got %q", progress.Message)
	}
}
