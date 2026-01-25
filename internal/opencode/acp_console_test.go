package opencode

import (
	"testing"

	acp "github.com/ironpark/acp-go"
)

func TestFormatSessionUpdateToolCall(t *testing.T) {
	update := acp.NewSessionUpdateToolCall(
		acp.ToolCallId("tool-1"),
		"Read file",
		acp.ToolKindPtr(acp.ToolKindRead),
		acp.ToolCallStatusPtr(acp.ToolCallStatusPending),
		nil,
		nil,
	)

	got := formatSessionUpdate(&update)
	expected := "tool_call id=tool-1 title=\"Read file\" kind=read status=pending"
	if got != expected {
		t.Fatalf("unexpected output: %q", got)
	}
}

func TestFormatSessionUpdateAgentMessage(t *testing.T) {
	update := acp.NewSessionUpdateAgentMessageChunk(acp.NewContentBlockText("Hello there"))

	got := formatSessionUpdate(&update)
	expected := "agent_message \"Hello there\""
	if got != expected {
		t.Fatalf("unexpected output: %q", got)
	}
}

func TestFormatACPRequest(t *testing.T) {
	got := formatACPRequest("permission", "allow")
	expected := "request permission allow"
	if got != expected {
		t.Fatalf("unexpected output: %q", got)
	}
}
