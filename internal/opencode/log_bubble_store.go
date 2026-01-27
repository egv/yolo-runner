package opencode

import (
	"sync"

	acp "github.com/ironpark/acp-go"
)

// LogBubbleStore manages a list of log bubbles that can be displayed in the TUI.
// Tool call bubbles are tracked by their ToolCallId and can be updated (upserted)
// without losing their position in the ordering.
type LogBubbleStore struct {
	mu      sync.RWMutex
	bubbles []logBubble
}

type logBubble struct {
	id       string        // ToolCallId for tool calls, empty string for regular log entries
	content  string        // Formatted string representation
	isTool   bool          // True if this is a tool call bubble
	toolCall *acp.ToolCall // Full tool call data for updates
}

// NewLogBubbleStore creates a new empty log bubble store.
func NewLogBubbleStore() *LogBubbleStore {
	return &LogBubbleStore{
		bubbles: make([]logBubble, 0),
	}
}

// UpsertToolCall creates or updates a tool call bubble.
// If a tool call bubble with the same ToolCallId already exists,
// it is updated in place to maintain ordering stability.
// If not, a new bubble is appended to the end.
func (s *LogBubbleStore) UpsertToolCall(toolCall *acp.ToolCall) {
	if toolCall == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Format the tool call for display
	content := formatToolCall("tool_call", toolCall.ToolCallId, toolCall.Title, toolCall.Kind, toolCall.Status)

	// Check if a bubble with this ToolCallId already exists
	id := string(toolCall.ToolCallId)
	for i := range s.bubbles {
		if s.bubbles[i].isTool && s.bubbles[i].id == id {
			// Update existing bubble in place
			s.bubbles[i].content = content
			s.bubbles[i].toolCall = toolCall
			return
		}
	}

	// Add new bubble
	s.bubbles = append(s.bubbles, logBubble{
		id:       id,
		content:  content,
		isTool:   true,
		toolCall: toolCall,
	})
}

// UpsertToolCallUpdate creates or updates a tool call bubble from a ToolCallUpdate.
// If a tool call bubble with the same ToolCallId already exists,
// it is updated in place to maintain ordering stability.
func (s *LogBubbleStore) UpsertToolCallUpdate(toolUpdate *acp.ToolCallUpdate) {
	if toolUpdate == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Format the tool call update for display
	content := formatToolCall("tool_call_update", toolUpdate.ToolCallId, toolUpdate.Title, toolUpdate.Kind, toolUpdate.Status)

	// Check if a bubble with this ToolCallId already exists
	id := string(toolUpdate.ToolCallId)
	for i := range s.bubbles {
		if s.bubbles[i].isTool && s.bubbles[i].id == id {
			// Update existing bubble in place
			s.bubbles[i].content = content
			// Note: ToolCallUpdate doesn't have the full ToolCall data,
			// so we keep the original toolCall reference if available
			return
		}
	}

	// Add new bubble (toolUpdate doesn't have full ToolCall, so toolCall is nil)
	s.bubbles = append(s.bubbles, logBubble{
		id:       id,
		content:  content,
		isTool:   true,
		toolCall: nil,
	})
}

// AddLogEntry adds a regular (non-tool call) log entry to the store.
// These are always appended to the end and cannot be updated.
func (s *LogBubbleStore) AddLogEntry(entry string) {
	if entry == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.bubbles = append(s.bubbles, logBubble{
		id:      "",
		content: entry,
		isTool:  false,
	})
}

// GetBubbles returns a snapshot of all bubbles in order.
// The returned slice is a copy and safe to use without locking.
func (s *LogBubbleStore) GetBubbles() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]string, len(s.bubbles))
	for i, bubble := range s.bubbles {
		result[i] = bubble.content
	}
	return result
}
