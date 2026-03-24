package opencode

import (
	"testing"

	acp "github.com/ironpark/acp-go"
)

// TestEventRouter_RoutesToolCallToBubbleStore tests that ACP tool_call events
// are routed to LogBubbleStore's UpsertToolCall method
func TestEventRouter_RoutesToolCallToBubbleStore(t *testing.T) {
	store := NewLogBubbleStore()
	router := NewEventRouter(store)

	// Create a tool call event
	pendingStatus := acp.ToolCallStatusPending
	executeKind := acp.ToolKindExecute

	// Create an ACP session update containing tool call
	update := acp.NewSessionUpdateToolCall(
		"tool-1",
		"Test Tool Call",
		&executeKind,
		&pendingStatus,
		nil,
		nil,
	)

	// Route event
	err := router.RouteACPUpdate(&update)
	if err != nil {
		t.Fatalf("expected no error routing ACP update, got: %v", err)
	}

	// Verify that tool call was added to bubble store
	bubbles := store.GetBubbles()
	if len(bubbles) != 1 {
		t.Fatalf("expected 1 bubble after routing tool call, got %d", len(bubbles))
	}

	if !containsString(bubbles[0], "tool-1") {
		t.Errorf("expected bubble to contain tool call id 'tool-1', got: %s", bubbles[0])
	}
	if !containsString(bubbles[0], "Test Tool Call") {
		t.Errorf("expected bubble to contain tool call title 'Test Tool Call', got: %s", bubbles[0])
	}
}

// TestEventRouter_RoutesToolCallUpdateToBubbleStore tests that ACP tool_call_update events
// are routed to LogBubbleStore's UpsertToolCallUpdate method
func TestEventRouter_RoutesToolCallUpdateToBubbleStore(t *testing.T) {
	store := NewLogBubbleStore()
	router := NewEventRouter(store)

	// Create a tool call update event
	inProgressStatus := acp.ToolCallStatusInProgress

	// Create an ACP session update containing tool call update
	update := acp.NewSessionUpdateToolCallUpdate(
		"tool-1",
		&inProgressStatus,
		nil,
		nil,
	)

	// Route event
	err := router.RouteACPUpdate(&update)
	if err != nil {
		t.Fatalf("expected no error routing ACP update, got: %v", err)
	}

	// Verify that tool call update was added to bubble store
	bubbles := store.GetBubbles()
	if len(bubbles) != 1 {
		t.Fatalf("expected 1 bubble after routing tool call update, got %d", len(bubbles))
	}

	if !containsString(bubbles[0], "tool_call_update") {
		t.Errorf("expected bubble to contain 'tool_call_update', got: %s", bubbles[0])
	}
}

// TestEventRouter_ToolCallUpdatesSameBubble tests that multiple tool call updates
// for the same tool call id update the same bubble in the store
func TestEventRouter_ToolCallUpdatesSameBubble(t *testing.T) {
	store := NewLogBubbleStore()
	router := NewEventRouter(store)

	// Initial tool call
	pendingStatus := acp.ToolCallStatusPending

	update1 := acp.NewSessionUpdateToolCall(
		"tool-1",
		"Test Tool Call",
		nil,
		&pendingStatus,
		nil,
		nil,
	)

	err := router.RouteACPUpdate(&update1)
	if err != nil {
		t.Fatalf("expected no error routing first update, got: %v", err)
	}

	bubbles := store.GetBubbles()
	if len(bubbles) != 1 {
		t.Fatalf("expected 1 bubble after initial tool call, got %d", len(bubbles))
	}

	// Update the same tool call
	inProgressStatus := acp.ToolCallStatusInProgress

	update2 := acp.NewSessionUpdateToolCallUpdate(
		"tool-1",
		&inProgressStatus,
		nil,
		nil,
	)

	err = router.RouteACPUpdate(&update2)
	if err != nil {
		t.Fatalf("expected no error routing update, got: %v", err)
	}

	// Verify we still have only 1 bubble (not 2)
	bubbles = store.GetBubbles()
	if len(bubbles) != 1 {
		t.Fatalf("expected 1 bubble after update (same bubble updated), got %d", len(bubbles))
	}

	// Verify that the bubble was updated with new status
	if !containsString(bubbles[0], "tool_call_update") {
		t.Errorf("expected bubble to be updated to tool_call_update format, got: %s", bubbles[0])
	}
}

// TestEventRouter_RoutesMultipleToolCalls tests that multiple different tool calls
// are routed and stored separately
func TestEventRouter_RoutesMultipleToolCalls(t *testing.T) {
	store := NewLogBubbleStore()
	router := NewEventRouter(store)

	// Create first tool call
	pendingStatus := acp.ToolCallStatusPending

	update1 := acp.NewSessionUpdateToolCall(
		"tool-1",
		"First Tool",
		nil,
		&pendingStatus,
		nil,
		nil,
	)

	err := router.RouteACPUpdate(&update1)
	if err != nil {
		t.Fatalf("expected no error routing first tool call, got: %v", err)
	}

	// Create second tool call
	update2 := acp.NewSessionUpdateToolCall(
		"tool-2",
		"Second Tool",
		nil,
		&pendingStatus,
		nil,
		nil,
	)

	err = router.RouteACPUpdate(&update2)
	if err != nil {
		t.Fatalf("expected no error routing second tool call, got: %v", err)
	}

	// Verify we have 2 separate bubbles
	bubbles := store.GetBubbles()
	if len(bubbles) != 2 {
		t.Fatalf("expected 2 bubbles after routing two tool calls, got %d", len(bubbles))
	}

	if !containsString(bubbles[0], "tool-1") {
		t.Errorf("expected first bubble to be tool-1, got: %s", bubbles[0])
	}
	if !containsString(bubbles[1], "tool-2") {
		t.Errorf("expected second bubble to be tool-2, got: %s", bubbles[1])
	}
}

// TestEventRouter_HandlesNilACPUpdate tests that nil ACP updates are handled gracefully
func TestEventRouter_HandlesNilACPUpdate(t *testing.T) {
	store := NewLogBubbleStore()
	router := NewEventRouter(store)

	// Route nil update
	err := router.RouteACPUpdate(nil)
	if err != nil {
		t.Fatalf("expected no error routing nil ACP update, got: %v", err)
	}

	// Verify no bubbles were added
	bubbles := store.GetBubbles()
	if len(bubbles) != 0 {
		t.Errorf("expected 0 bubbles after nil update, got %d", len(bubbles))
	}
}

// TestEventRouter_HandlesEmptyACPUpdate tests that empty ACP updates (no tool call data)
// are handled gracefully
func TestEventRouter_HandlesEmptyACPUpdate(t *testing.T) {
	store := NewLogBubbleStore()
	router := NewEventRouter(store)

	// Route empty update - create a plan update which has no tool call data
	update := acp.NewSessionUpdatePlan(nil)

	err := router.RouteACPUpdate(&update)
	if err != nil {
		t.Fatalf("expected no error routing empty ACP update, got: %v", err)
	}

	// Verify no bubbles were added
	bubbles := store.GetBubbles()
	if len(bubbles) != 0 {
		t.Errorf("expected 0 bubbles after empty update, got %d", len(bubbles))
	}
}

// TestEventRouter_RoutesAgentThoughts tests that agent thoughts from ACP updates
// are routed to bubble store as log entries
func TestEventRouter_RoutesAgentThoughts(t *testing.T) {
	store := NewLogBubbleStore()
	router := NewEventRouter(store)

	// Create an ACP update with agent thought
	content := acp.NewContentBlockText("## Analysis\n\nThis is a thought.")
	update := acp.NewSessionUpdateAgentThoughtChunk(content)

	// Route update
	err := router.RouteACPUpdate(&update)
	if err != nil {
		t.Fatalf("expected no error routing agent thought, got: %v", err)
	}

	// Verify that agent thought was added to bubble store
	bubbles := store.GetBubbles()
	if len(bubbles) != 1 {
		t.Fatalf("expected 1 bubble after routing agent thought, got %d", len(bubbles))
	}

	if !containsString(bubbles[0], "agent_thought") {
		t.Errorf("expected bubble to contain 'agent_thought', got: %s", bubbles[0])
	}
}

// fakeRunnerEvent is a minimal RunnerEvent implementation for testing.
type fakeRunnerEvent struct {
	eventType string
	title     string
	thought   string
	message   string
}

func (e *fakeRunnerEvent) RunnerEventType() string    { return e.eventType }
func (e *fakeRunnerEvent) RunnerEventTitle() string   { return e.title }
func (e *fakeRunnerEvent) RunnerEventThought() string { return e.thought }
func (e *fakeRunnerEvent) RunnerEventMessage() string { return e.message }

// TestEventRouter_RunnerCmdStartedAppendsNewEntry verifies that runner_cmd_started
// events append a new tool entry to the bubble store.
func TestEventRouter_RunnerCmdStartedAppendsNewEntry(t *testing.T) {
	store := NewLogBubbleStore()
	router := NewEventRouter(store)

	event := &fakeRunnerEvent{eventType: "runner_cmd_started", message: "⏳ Running Read"}
	if err := router.RouteRunnerEvent(event); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	bubbles := store.GetBubbles()
	if len(bubbles) != 1 {
		t.Fatalf("expected 1 bubble after runner_cmd_started, got %d", len(bubbles))
	}
	if !containsString(bubbles[0], "⏳ Running Read") {
		t.Errorf("expected bubble to contain message, got: %s", bubbles[0])
	}
}

// TestEventRouter_RunnerCmdFinishedMutatesExistingEntry verifies that runner_cmd_finished
// updates the last runner_cmd_started entry in place (same position, same count).
func TestEventRouter_RunnerCmdFinishedMutatesExistingEntry(t *testing.T) {
	store := NewLogBubbleStore()
	router := NewEventRouter(store)

	startEvent := &fakeRunnerEvent{eventType: "runner_cmd_started", message: "⏳ Running Read"}
	if err := router.RouteRunnerEvent(startEvent); err != nil {
		t.Fatalf("unexpected error routing started: %v", err)
	}

	finishEvent := &fakeRunnerEvent{eventType: "runner_cmd_finished", message: "✅ Running Read"}
	if err := router.RouteRunnerEvent(finishEvent); err != nil {
		t.Fatalf("unexpected error routing finished: %v", err)
	}

	bubbles := store.GetBubbles()
	if len(bubbles) != 1 {
		t.Fatalf("expected 1 bubble after start+finish (mutated in place), got %d", len(bubbles))
	}
	if !containsString(bubbles[0], "✅ Running Read") {
		t.Errorf("expected bubble to contain finish message, got: %s", bubbles[0])
	}
}

// TestEventRouter_RunnerCmdOrderStabilityWithOtherBubbles verifies that a finished
// event mutates the cmd entry in its original position among other bubbles.
func TestEventRouter_RunnerCmdOrderStabilityWithOtherBubbles(t *testing.T) {
	store := NewLogBubbleStore()
	router := NewEventRouter(store)

	// Add a regular log entry before the cmd
	store.AddLogEntry("task started")

	startEvent := &fakeRunnerEvent{eventType: "runner_cmd_started", message: "⏳ Reading"}
	router.RouteRunnerEvent(startEvent)

	store.AddLogEntry("other output")

	finishEvent := &fakeRunnerEvent{eventType: "runner_cmd_finished", message: "✅ Reading"}
	router.RouteRunnerEvent(finishEvent)

	bubbles := store.GetBubbles()
	if len(bubbles) != 3 {
		t.Fatalf("expected 3 bubbles (log, cmd, log), got %d", len(bubbles))
	}
	if bubbles[0] != "task started" {
		t.Errorf("expected first bubble unchanged, got: %s", bubbles[0])
	}
	if !containsString(bubbles[1], "✅ Reading") {
		t.Errorf("expected cmd bubble mutated in position 1, got: %s", bubbles[1])
	}
	if bubbles[2] != "other output" {
		t.Errorf("expected third bubble unchanged, got: %s", bubbles[2])
	}
}

// TestEventRouter_MaintainsOrderingAcrossUpdates tests that bubble store maintains
// correct ordering when multiple tool calls are updated
func TestEventRouter_MaintainsOrderingAcrossUpdates(t *testing.T) {
	store := NewLogBubbleStore()
	router := NewEventRouter(store)

	// Create three tool calls
	pendingStatus := acp.ToolCallStatusPending

	update1 := acp.NewSessionUpdateToolCall(
		"tool-1",
		"First Tool",
		nil,
		&pendingStatus,
		nil,
		nil,
	)

	update2 := acp.NewSessionUpdateToolCall(
		"tool-2",
		"Second Tool",
		nil,
		&pendingStatus,
		nil,
		nil,
	)

	update3 := acp.NewSessionUpdateToolCall(
		"tool-3",
		"Third Tool",
		nil,
		&pendingStatus,
		nil,
		nil,
	)

	// Route them in order
	router.RouteACPUpdate(&update1)
	router.RouteACPUpdate(&update2)
	router.RouteACPUpdate(&update3)

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

	// Update tool-2 - it should stay in position
	inProgressStatus := acp.ToolCallStatusInProgress

	update4 := acp.NewSessionUpdateToolCallUpdate(
		"tool-2",
		&inProgressStatus,
		nil,
		nil,
	)

	router.RouteACPUpdate(&update4)

	bubbles = store.GetBubbles()
	if len(bubbles) != 3 {
		t.Fatalf("expected 3 bubbles after update, got %d", len(bubbles))
	}

	// Verify ordering is stable (tool-2 still in middle)
	if !containsString(bubbles[0], "tool-1") {
		t.Errorf("expected first bubble to still be tool-1, got: %s", bubbles[0])
	}
	if !containsString(bubbles[1], "Second Tool") {
		t.Errorf("expected second bubble to still refer to Second Tool, got: %s", bubbles[1])
	}
	if !containsString(bubbles[2], "tool-3") {
		t.Errorf("expected third bubble to still be tool-3, got: %s", bubbles[2])
	}
}
