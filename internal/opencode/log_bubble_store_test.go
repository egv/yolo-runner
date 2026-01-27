package opencode

import (
	"testing"

	acp "github.com/ironpark/acp-go"
)

// TestLogBubbleStore_CreateAndUpsertToolCall tests that tool call bubbles
// can be created and updated by id
func TestLogBubbleStore_CreateAndUpsertToolCall(t *testing.T) {
	store := NewLogBubbleStore()

	// Create a new tool call bubble
	pendingStatus := acp.ToolCallStatusPending
	executeKind := acp.ToolKindExecute
	toolCall1 := &acp.ToolCall{
		ToolCallId: "tool-1",
		Title:      "First Tool Call",
		Kind:       &executeKind,
		Status:     &pendingStatus,
	}

	store.UpsertToolCall(toolCall1)

	bubbles := store.GetBubbles()
	if len(bubbles) != 1 {
		t.Fatalf("expected 1 bubble after create, got %d", len(bubbles))
	}

	// Verify the bubble content contains the tool call details
	if !containsString(bubbles[0], "tool-1") {
		t.Errorf("expected bubble to contain tool call id 'tool-1', got: %s", bubbles[0])
	}
	if !containsString(bubbles[0], "First Tool Call") {
		t.Errorf("expected bubble to contain tool call title 'First Tool Call', got: %s", bubbles[0])
	}
	if !containsString(bubbles[0], "pending") {
		t.Errorf("expected bubble to contain status 'pending', got: %s", bubbles[0])
	}

	// Update the same tool call bubble with new status
	inProgressStatus := acp.ToolCallStatusInProgress
	toolCall1Update := &acp.ToolCall{
		ToolCallId: "tool-1",
		Title:      "First Tool Call",
		Kind:       &executeKind,
		Status:     &inProgressStatus,
	}

	store.UpsertToolCall(toolCall1Update)

	bubbles = store.GetBubbles()
	if len(bubbles) != 1 {
		t.Fatalf("expected 1 bubble after update, got %d", len(bubbles))
	}

	// Verify the bubble was updated
	if !containsString(bubbles[0], "in_progress") {
		t.Errorf("expected bubble to contain updated status 'in_progress', got: %s", bubbles[0])
	}
	if !containsString(bubbles[0], "tool-1") {
		t.Errorf("expected bubble to still contain tool call id 'tool-1', got: %s", bubbles[0])
	}
}

// TestLogBubbleStore_OrderingStability tests that updating a tool call
// bubble maintains its position in the ordering
func TestLogBubbleStore_OrderingStability(t *testing.T) {
	store := NewLogBubbleStore()

	// Create first tool call
	pendingStatus := acp.ToolCallStatusPending
	toolCall1 := &acp.ToolCall{
		ToolCallId: "tool-1",
		Title:      "First Tool Call",
		Status:     &pendingStatus,
	}

	store.UpsertToolCall(toolCall1)

	// Create second tool call
	toolCall2 := &acp.ToolCall{
		ToolCallId: "tool-2",
		Title:      "Second Tool Call",
		Status:     &pendingStatus,
	}

	store.UpsertToolCall(toolCall2)

	// Create third tool call
	toolCall3 := &acp.ToolCall{
		ToolCallId: "tool-3",
		Title:      "Third Tool Call",
		Status:     &pendingStatus,
	}

	store.UpsertToolCall(toolCall3)

	bubbles := store.GetBubbles()
	if len(bubbles) != 3 {
		t.Fatalf("expected 3 bubbles, got %d", len(bubbles))
	}

	// Verify initial ordering
	if !containsString(bubbles[0], "tool-1") {
		t.Errorf("expected first bubble to be tool-1, got: %s", bubbles[0])
	}
	if !containsString(bubbles[1], "tool-2") {
		t.Errorf("expected second bubble to be tool-2, got: %s", bubbles[1])
	}
	if !containsString(bubbles[2], "tool-3") {
		t.Errorf("expected third bubble to be tool-3, got: %s", bubbles[2])
	}

	// Update the second tool call - it should stay in position
	inProgressStatus := acp.ToolCallStatusInProgress
	toolCall2Update := &acp.ToolCall{
		ToolCallId: "tool-2",
		Title:      "Second Tool Call Updated",
		Status:     &inProgressStatus,
	}

	store.UpsertToolCall(toolCall2Update)

	bubbles = store.GetBubbles()
	if len(bubbles) != 3 {
		t.Fatalf("expected 3 bubbles after update, got %d", len(bubbles))
	}

	// Verify ordering is stable (tool-2 still in middle)
	if !containsString(bubbles[0], "tool-1") {
		t.Errorf("expected first bubble to still be tool-1, got: %s", bubbles[0])
	}
	if !containsString(bubbles[1], "tool-2") {
		t.Errorf("expected second bubble to still be tool-2, got: %s", bubbles[1])
	}
	if !containsString(bubbles[1], "Second Tool Call Updated") {
		t.Errorf("expected second bubble to be updated with new title, got: %s", bubbles[1])
	}
	if !containsString(bubbles[1], "in_progress") {
		t.Errorf("expected second bubble to have updated status, got: %s", bubbles[1])
	}
	if !containsString(bubbles[2], "tool-3") {
		t.Errorf("expected third bubble to still be tool-3, got: %s", bubbles[2])
	}
}

// TestLogBubbleStore_UpsertToolCallUpdate tests that tool_call_update
// events also upsert by id
func TestLogBubbleStore_UpsertToolCallUpdate(t *testing.T) {
	store := NewLogBubbleStore()

	// Create a tool call bubble
	pendingStatus := acp.ToolCallStatusPending
	toolCall1 := &acp.ToolCall{
		ToolCallId: "tool-1",
		Title:      "First Tool Call",
		Status:     &pendingStatus,
	}

	store.UpsertToolCall(toolCall1)

	bubbles := store.GetBubbles()
	if len(bubbles) != 1 {
		t.Fatalf("expected 1 bubble after create, got %d", len(bubbles))
	}

	// Update using ToolCallUpdate
	inProgressStatus := acp.ToolCallStatusInProgress
	toolCall1Update := &acp.ToolCallUpdate{
		ToolCallId: "tool-1",
		Title:      "First Tool Call",
		Status:     &inProgressStatus,
	}

	store.UpsertToolCallUpdate(toolCall1Update)

	bubbles = store.GetBubbles()
	if len(bubbles) != 1 {
		t.Fatalf("expected 1 bubble after update, got %d", len(bubbles))
	}

	// Verify the bubble was updated to tool_call_update format
	// Note: tool_call_update uses simplified format (emoji + label + title, no id/status)
	if !containsString(bubbles[0], "tool_call_update") {
		t.Errorf("expected bubble to contain 'tool_call_update', got: %s", bubbles[0])
	}
	// Should contain the rotating emoji for in_progress status (🔄)
	if !containsString(bubbles[0], "🔄") {
		t.Errorf("expected bubble to contain in_progress emoji, got: %s", bubbles[0])
	}
	if !containsString(bubbles[0], "First Tool Call") {
		t.Errorf("expected bubble to still contain tool call title, got: %s", bubbles[0])
	}
}

// TestLogBubbleStore_AddLogEntry tests that regular log entries
// can be added to the store
func TestLogBubbleStore_AddLogEntry(t *testing.T) {
	store := NewLogBubbleStore()

	// Add a regular log entry
	store.AddLogEntry("Starting task...")

	bubbles := store.GetBubbles()
	if len(bubbles) != 1 {
		t.Fatalf("expected 1 bubble after adding log entry, got %d", len(bubbles))
	}

	if bubbles[0] != "Starting task..." {
		t.Errorf("expected bubble to be 'Starting task...', got: %s", bubbles[0])
	}
}

// Helper function to check if a string contains a substring
func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
