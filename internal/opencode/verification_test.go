package opencode

import (
	"testing"

	acp "github.com/ironpark/acp-go"
)

// TestAggregateAgentMessageChunks_Verification - Verifies all acceptance criteria for task yolo-runner-127.4.14
// AC: Buffer ACP message/thought chunks and emit a single line per completed message
// AC: Newline (\n) is the delimiter for flushing a message
// AC: Applies to agent_message, agent_thought, and user_message chunk streams
// AC: No per-token spam in console/TUI output
// AC: Add aggregation tests covering multi-chunk + newline flush
// AC: go test ./... passes
func TestAggregateAgentMessageChunks_Verification(t *testing.T) {
	t.Run("agent_message: multi-chunk with newline flush", func(t *testing.T) {
		aggregator := NewAgentMessageAggregator()

		// Multiple chunks before newline - should not emit
		chunk1 := acp.NewSessionUpdateAgentMessageChunk(acp.NewContentBlockText("First"))
		result1 := aggregator.ProcessUpdate(&chunk1)
		if result1 != "" {
			t.Errorf("Expected no output for incomplete chunk, got: %q", result1)
		}

		chunk2 := acp.NewSessionUpdateAgentMessageChunk(acp.NewContentBlockText(" Second"))
		result2 := aggregator.ProcessUpdate(&chunk2)
		if result2 != "" {
			t.Errorf("Expected no output for incomplete chunk, got: %q", result2)
		}

		// Chunk with newline - should emit single aggregated line
		chunk3 := acp.NewSessionUpdateAgentMessageChunk(acp.NewContentBlockText(" Third\n"))
		result3 := aggregator.ProcessUpdate(&chunk3)
		expected := "agent_message \"First Second Third\\n\""
		if result3 != expected {
			t.Errorf("Expected aggregated message %q, got %q", expected, result3)
		}
	})

	t.Run("agent_thought: multi-chunk with newline flush", func(t *testing.T) {
		aggregator := NewAgentMessageAggregator()

		// Multiple chunks before newline - should not emit
		chunk1 := acp.NewSessionUpdateAgentThoughtChunk(acp.NewContentBlockText("Thinking"))
		result1 := aggregator.ProcessUpdate(&chunk1)
		if result1 != "" {
			t.Errorf("Expected no output for incomplete chunk, got: %q", result1)
		}

		chunk2 := acp.NewSessionUpdateAgentThoughtChunk(acp.NewContentBlockText(" about"))
		result2 := aggregator.ProcessUpdate(&chunk2)
		if result2 != "" {
			t.Errorf("Expected no output for incomplete chunk, got: %q", result2)
		}

		// Chunk with newline - should emit single aggregated line
		chunk3 := acp.NewSessionUpdateAgentThoughtChunk(acp.NewContentBlockText(" this\n"))
		result3 := aggregator.ProcessUpdate(&chunk3)
		expected := "agent_thought \"Thinking about this \""
		if result3 != expected {
			t.Errorf("Expected aggregated message %q, got %q", expected, result3)
		}
	})

	t.Run("user_message: multi-chunk with newline flush", func(t *testing.T) {
		aggregator := NewAgentMessageAggregator()

		// Multiple chunks before newline - should not emit
		chunk1 := acp.NewSessionUpdateUserMessageChunk(acp.NewContentBlockText("User"))
		result1 := aggregator.ProcessUpdate(&chunk1)
		if result1 != "" {
			t.Errorf("Expected no output for incomplete chunk, got: %q", result1)
		}

		chunk2 := acp.NewSessionUpdateUserMessageChunk(acp.NewContentBlockText(" input"))
		result2 := aggregator.ProcessUpdate(&chunk2)
		if result2 != "" {
			t.Errorf("Expected no output for incomplete chunk, got: %q", result2)
		}

		// Chunk with newline - should emit single aggregated line
		chunk3 := acp.NewSessionUpdateUserMessageChunk(acp.NewContentBlockText(" now\n"))
		result3 := aggregator.ProcessUpdate(&chunk3)
		expected := "user_message \"User input now\\n\""
		if result3 != expected {
			t.Errorf("Expected aggregated message %q, got %q", expected, result3)
		}
	})

	t.Run("no per-token spam: multiple newlines in single chunk", func(t *testing.T) {
		aggregator := NewAgentMessageAggregator()

		// Single chunk with multiple newlines - should emit all complete lines at once
		chunk := acp.NewSessionUpdateAgentMessageChunk(acp.NewContentBlockText("Line1\nLine2\nLine3\n"))
		result := aggregator.ProcessUpdate(&chunk)
		expected := "agent_message \"Line1\\nLine2\\nLine3\\n\""
		if result != expected {
			t.Errorf("Expected all lines in one output %q, got %q", expected, result)
		}
	})

	t.Run("trailing content preserved: buffer holds content without newline", func(t *testing.T) {
		aggregator := NewAgentMessageAggregator()

		// First complete message
		chunk1 := acp.NewSessionUpdateAgentMessageChunk(acp.NewContentBlockText("Complete\n"))
		result1 := aggregator.ProcessUpdate(&chunk1)
		expected1 := "agent_message \"Complete\\n\""
		if result1 != expected1 {
			t.Errorf("Expected first message %q, got %q", expected1, result1)
		}

		// Incomplete trailing message - should not emit
		chunk2 := acp.NewSessionUpdateAgentMessageChunk(acp.NewContentBlockText("Trailing"))
		result2 := aggregator.ProcessUpdate(&chunk2)
		if result2 != "" {
			t.Errorf("Expected no output for trailing content, got: %q", result2)
		}

		// Complete trailing message
		chunk3 := acp.NewSessionUpdateAgentMessageChunk(acp.NewContentBlockText(" content\n"))
		result3 := aggregator.ProcessUpdate(&chunk3)
		expected3 := "agent_message \"Trailing content\\n\""
		if result3 != expected3 {
			t.Errorf("Expected trailing message %q, got %q", expected3, result3)
		}
	})
}
