package opencode

import (
	"testing"

	acp "github.com/ironpark/acp-go"
)

func TestAgentMessageAggregator_BasicAggregation(t *testing.T) {
	aggregator := NewAgentMessageAggregator()

	// Send first chunk without newline
	update1 := acp.NewSessionUpdateAgentMessageChunk(acp.NewContentBlockText("Hello"))
	got1 := aggregator.ProcessUpdate(&update1)
	if got1 != "" {
		t.Fatalf("expected empty output for incomplete chunk, got: %q", got1)
	}

	// Send second chunk with newline - should trigger output
	update2 := acp.NewSessionUpdateAgentMessageChunk(acp.NewContentBlockText(" world\n"))
	got2 := aggregator.ProcessUpdate(&update2)
	expected := "agent_message \"Hello world\\n\""
	if got2 != expected {
		t.Fatalf("expected aggregated message: %q, got: %q", expected, got2)
	}
}

func TestAgentMessageAggregator_MultipleChunksBeforeNewline(t *testing.T) {
	aggregator := NewAgentMessageAggregator()

	// Send multiple chunks before newline
	update1 := acp.NewSessionUpdateAgentMessageChunk(acp.NewContentBlockText("Chunk1"))
	got1 := aggregator.ProcessUpdate(&update1)
	if got1 != "" {
		t.Fatalf("expected empty output for first chunk, got: %q", got1)
	}

	update2 := acp.NewSessionUpdateAgentMessageChunk(acp.NewContentBlockText(" Chunk2"))
	got2 := aggregator.ProcessUpdate(&update2)
	if got2 != "" {
		t.Fatalf("expected empty output for second chunk, got: %q", got2)
	}

	update3 := acp.NewSessionUpdateAgentMessageChunk(acp.NewContentBlockText(" Chunk3\n"))
	got3 := aggregator.ProcessUpdate(&update3)
	expected := "agent_message \"Chunk1 Chunk2 Chunk3\\n\""
	if got3 != expected {
		t.Fatalf("expected aggregated message: %q, got: %q", expected, got3)
	}
}

func TestAgentMessageAggregator_MultipleMessages(t *testing.T) {
	aggregator := NewAgentMessageAggregator()

	// First complete message
	update1 := acp.NewSessionUpdateAgentMessageChunk(acp.NewContentBlockText("First message\n"))
	got1 := aggregator.ProcessUpdate(&update1)
	expected1 := "agent_message \"First message\\n\""
	if got1 != expected1 {
		t.Fatalf("expected first message: %q, got: %q", expected1, got1)
	}

	// Second complete message
	update2 := acp.NewSessionUpdateAgentMessageChunk(acp.NewContentBlockText("Second message\n"))
	got2 := aggregator.ProcessUpdate(&update2)
	expected2 := "agent_message \"Second message\\n\""
	if got2 != expected2 {
		t.Fatalf("expected second message: %q, got: %q", expected2, got2)
	}
}

func TestAgentMessageAggregator_PreservesExactText(t *testing.T) {
	aggregator := NewAgentMessageAggregator()

	// Test with special characters and formatting
	update := acp.NewSessionUpdateAgentMessageChunk(acp.NewContentBlockText("Line 1\nLine 2\nLine 3\n"))
	got := aggregator.ProcessUpdate(&update)
	expected := "agent_message \"Line 1\\nLine 2\\nLine 3\\n\""
	if got != expected {
		t.Fatalf("expected exact text preservation: %q, got: %q", expected, got)
	}
}

func TestAgentMessageAggregator_NonAgentMessageChunks(t *testing.T) {
	aggregator := NewAgentMessageAggregator()

	// Test that non-agent-message chunks are not affected
	toolUpdate := acp.NewSessionUpdateToolCall(
		acp.ToolCallId("tool-1"),
		"Read file",
		acp.ToolKindPtr(acp.ToolKindRead),
		acp.ToolCallStatusPtr(acp.ToolCallStatusPending),
		nil,
		nil,
	)

	got := aggregator.ProcessUpdate(&toolUpdate)
	expected := "⏳ \x1b[33mtool_call\x1b[0m id=tool-1 title=\"Read file\" kind=read status=pending"
	if got != expected {
		t.Fatalf("expected tool call to have status badge: %q, got: %q", expected, got)
	}
}

func TestAgentMessageAggregator_AggregatesUserMessageChunks(t *testing.T) {
	aggregator := NewAgentMessageAggregator()

	// Send first user message chunk without newline
	update1 := acp.NewSessionUpdateUserMessageChunk(acp.NewContentBlockText("User input"))
	got1 := aggregator.ProcessUpdate(&update1)
	if got1 != "" {
		t.Fatalf("expected empty output for incomplete user message chunk, got: %q", got1)
	}

	// Send second user message chunk with newline - should trigger output
	update2 := acp.NewSessionUpdateUserMessageChunk(acp.NewContentBlockText(" complete\n"))
	got2 := aggregator.ProcessUpdate(&update2)
	expected := "user_message \"User input complete\\n\""
	if got2 != expected {
		t.Fatalf("expected aggregated user message: %q, got: %q", expected, got2)
	}
}

func TestAgentMessageAggregator_MultipleUserMessageChunksBeforeNewline(t *testing.T) {
	aggregator := NewAgentMessageAggregator()

	// Send multiple user message chunks before newline
	update1 := acp.NewSessionUpdateUserMessageChunk(acp.NewContentBlockText("Part1"))
	got1 := aggregator.ProcessUpdate(&update1)
	if got1 != "" {
		t.Fatalf("expected empty output for first user message chunk, got: %q", got1)
	}

	update2 := acp.NewSessionUpdateUserMessageChunk(acp.NewContentBlockText(" Part2"))
	got2 := aggregator.ProcessUpdate(&update2)
	if got2 != "" {
		t.Fatalf("expected empty output for second user message chunk, got: %q", got2)
	}

	update3 := acp.NewSessionUpdateUserMessageChunk(acp.NewContentBlockText(" Part3\n"))
	got3 := aggregator.ProcessUpdate(&update3)
	expected := "user_message \"Part1 Part2 Part3\\n\""
	if got3 != expected {
		t.Fatalf("expected aggregated user message: %q, got: %q", expected, got3)
	}
}

func TestAgentMessageAggregator_AggregatesAgentThoughtChunks(t *testing.T) {
	aggregator := NewAgentMessageAggregator()

	// Send first agent thought chunk without newline
	update1 := acp.NewSessionUpdateAgentThoughtChunk(acp.NewContentBlockText("Thinking"))
	got1 := aggregator.ProcessUpdate(&update1)
	if got1 != "" {
		t.Fatalf("expected empty output for incomplete agent thought chunk, got: %q", got1)
	}

	// Send second agent thought chunk with newline - should trigger output
	update2 := acp.NewSessionUpdateAgentThoughtChunk(acp.NewContentBlockText(" done\n"))
	got2 := aggregator.ProcessUpdate(&update2)
	expected := "agent_thought \"Thinking done\\n\""
	if got2 != expected {
		t.Fatalf("expected aggregated agent thought: %q, got: %q", expected, got2)
	}
}

func TestAgentMessageAggregator_MultipleAgentThoughtChunksBeforeNewline(t *testing.T) {
	aggregator := NewAgentMessageAggregator()

	// Send multiple agent thought chunks before newline
	update1 := acp.NewSessionUpdateAgentThoughtChunk(acp.NewContentBlockText("Step1"))
	got1 := aggregator.ProcessUpdate(&update1)
	if got1 != "" {
		t.Fatalf("expected empty output for first agent thought chunk, got: %q", got1)
	}

	update2 := acp.NewSessionUpdateAgentThoughtChunk(acp.NewContentBlockText(" Step2"))
	got2 := aggregator.ProcessUpdate(&update2)
	if got2 != "" {
		t.Fatalf("expected empty output for second agent thought chunk, got: %q", got2)
	}

	update3 := acp.NewSessionUpdateAgentThoughtChunk(acp.NewContentBlockText(" Step3\n"))
	got3 := aggregator.ProcessUpdate(&update3)
	expected := "agent_thought \"Step1 Step2 Step3\\n\""
	if got3 != expected {
		t.Fatalf("expected aggregated agent thought: %q, got: %q", expected, got3)
	}
}

func TestAgentMessageAggregator_SeparateBuffersForEachMessageType(t *testing.T) {
	aggregator := NewAgentMessageAggregator()

	// Send agent message chunk
	agentMsg1 := acp.NewSessionUpdateAgentMessageChunk(acp.NewContentBlockText("Agent"))
	got1 := aggregator.ProcessUpdate(&agentMsg1)
	if got1 != "" {
		t.Fatalf("expected empty output for agent message chunk, got: %q", got1)
	}

	// Send user message chunk
	userMsg1 := acp.NewSessionUpdateUserMessageChunk(acp.NewContentBlockText("User"))
	got2 := aggregator.ProcessUpdate(&userMsg1)
	if got2 != "" {
		t.Fatalf("expected empty output for user message chunk, got: %q", got2)
	}

	// Send agent thought chunk
	thought1 := acp.NewSessionUpdateAgentThoughtChunk(acp.NewContentBlockText("Thought"))
	got3 := aggregator.ProcessUpdate(&thought1)
	if got3 != "" {
		t.Fatalf("expected empty output for agent thought chunk, got: %q", got3)
	}

	// Complete agent message with newline
	agentMsg2 := acp.NewSessionUpdateAgentMessageChunk(acp.NewContentBlockText(" message\n"))
	got4 := aggregator.ProcessUpdate(&agentMsg2)
	expected4 := "agent_message \"Agent message\\n\""
	if got4 != expected4 {
		t.Fatalf("expected aggregated agent message: %q, got: %q", expected4, got4)
	}

	// Complete user message with newline
	userMsg2 := acp.NewSessionUpdateUserMessageChunk(acp.NewContentBlockText(" input\n"))
	got5 := aggregator.ProcessUpdate(&userMsg2)
	expected5 := "user_message \"User input\\n\""
	if got5 != expected5 {
		t.Fatalf("expected aggregated user message: %q, got: %q", expected5, got5)
	}

	// Complete agent thought with newline
	thought2 := acp.NewSessionUpdateAgentThoughtChunk(acp.NewContentBlockText(" complete\n"))
	got6 := aggregator.ProcessUpdate(&thought2)
	expected6 := "agent_thought \"Thought complete\\n\""
	if got6 != expected6 {
		t.Fatalf("expected aggregated agent thought: %q, got: %q", expected6, got6)
	}
}
