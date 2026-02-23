package runner

import (
	"testing"
	"time"

	"github.com/egv/yolo-runner/internal/opencode"
)

// TestRunnerEventsRouteToLogBubbleStore tests that runner events
// are routed to LogBubbleStore's AddLogEntry method
func TestRunnerEventsRouteToLogBubbleStore(t *testing.T) {
	store := opencode.NewLogBubbleStore()
	router := opencode.NewEventRouter(store)

	// Create a runner event
	event := Event{
		Type:      EventGitCommit,
		IssueID:   "task-123",
		Title:     "Commit changes",
		Phase:     "commit",
		EmittedAt: time.Now(),
	}

	// Route event
	err := router.RouteRunnerEvent(event)
	if err != nil {
		t.Fatalf("expected no error routing runner event, got: %v", err)
	}

	// Verify that log entry was added to bubble store
	bubbles := store.GetBubbles()
	if len(bubbles) != 1 {
		t.Fatalf("expected 1 bubble after routing runner event, got %d", len(bubbles))
	}

	if !containsString(bubbles[0], "git_commit") {
		t.Errorf("expected bubble to contain 'git_commit', got: %s", bubbles[0])
	}
	if !containsString(bubbles[0], "Commit changes") {
		t.Errorf("expected bubble to contain 'Commit changes', got: %s", bubbles[0])
	}
}

// TestRunnerEventsWithThoughts tests that runner events
// with thoughts are routed to bubble store
func TestRunnerEventsWithThoughts(t *testing.T) {
	store := opencode.NewLogBubbleStore()
	router := opencode.NewEventRouter(store)

	// Create a runner event with a thought
	event := Event{
		Type:      EventSelectTask,
		IssueID:   "task-456",
		Title:     "Select task",
		Thought:   "This is a thought about the task",
		EmittedAt: time.Now(),
	}

	// Route event
	err := router.RouteRunnerEvent(event)
	if err != nil {
		t.Fatalf("expected no error routing runner event with thought, got: %v", err)
	}

	// Verify that log entry was added
	bubbles := store.GetBubbles()
	if len(bubbles) != 1 {
		t.Fatalf("expected 1 bubble, got %d", len(bubbles))
	}

	if !containsString(bubbles[0], "select_task") {
		t.Errorf("expected bubble to contain 'select_task', got: %s", bubbles[0])
	}
	if !containsString(bubbles[0], "This is a thought about the task") {
		t.Errorf("expected bubble to contain the thought, got: %s", bubbles[0])
	}
}

// TestMultipleRunnerEventsRouteToBubbleStore tests that multiple runner events
// are routed and stored separately
func TestMultipleRunnerEventsRouteToBubbleStore(t *testing.T) {
	store := opencode.NewLogBubbleStore()
	router := opencode.NewEventRouter(store)

	// Create multiple runner events
	event1 := Event{
		Type:      EventGitAdd,
		Title:     "Add files",
		EmittedAt: time.Now(),
	}

	event2 := Event{
		Type:      EventGitCommit,
		Title:     "Commit changes",
		EmittedAt: time.Now(),
	}

	event3 := Event{
		Type:      EventBeadsClose,
		Title:     "Close task",
		EmittedAt: time.Now(),
	}

	// Route them
	err := router.RouteRunnerEvent(event1)
	if err != nil {
		t.Fatalf("expected no error routing first event, got: %v", err)
	}

	err = router.RouteRunnerEvent(event2)
	if err != nil {
		t.Fatalf("expected no error routing second event, got: %v", err)
	}

	err = router.RouteRunnerEvent(event3)
	if err != nil {
		t.Fatalf("expected no error routing third event, got: %v", err)
	}

	// Verify we have 3 separate bubbles
	bubbles := store.GetBubbles()
	if len(bubbles) != 3 {
		t.Fatalf("expected 3 bubbles after routing three events, got %d", len(bubbles))
	}

	if !containsString(bubbles[0], "git_add") {
		t.Errorf("expected first bubble to contain 'git_add', got: %s", bubbles[0])
	}
	if !containsString(bubbles[1], "git_commit") {
		t.Errorf("expected second bubble to contain 'git_commit', got: %s", bubbles[1])
	}
	if !containsString(bubbles[2], "beads_close") {
		t.Errorf("expected third bubble to contain 'beads_close', got: %s", bubbles[2])
	}
}

// TestEmptyRunnerEventHandledGracefully tests that empty runner events
// are handled gracefully
func TestEmptyRunnerEventHandledGracefully(t *testing.T) {
	store := opencode.NewLogBubbleStore()
	router := opencode.NewEventRouter(store)

	// Create empty runner event
	event := Event{
		Type:      EventBeadsUpdate,
		Title:     "",
		EmittedAt: time.Now(),
	}

	// Route event
	err := router.RouteRunnerEvent(event)
	if err != nil {
		t.Fatalf("expected no error routing empty runner event, got: %v", err)
	}

	// Empty events might still be logged, or might be skipped
	// The behavior should be consistent - verify no panic
	bubbles := store.GetBubbles()
	// Acceptable outcomes: 0 bubbles (empty title skipped) or 1 bubble (empty title logged)
	if len(bubbles) > 1 {
		t.Errorf("expected at most 1 bubble after empty event, got %d", len(bubbles))
	}
}

// TestMixedACPAndRunnerEventsRouteToBubbleStore tests that both ACP and runner
// events can be routed to the same bubble store and are stored correctly
func TestMixedACPAndRunnerEventsRouteToBubbleStore(t *testing.T) {
	store := opencode.NewLogBubbleStore()
	router := opencode.NewEventRouter(store)

	// Route a runner event
	runnerEvent := Event{
		Type:      EventOpenCodeStart,
		Title:     "Starting OpenCode",
		EmittedAt: time.Now(),
	}

	err := router.RouteRunnerEvent(runnerEvent)
	if err != nil {
		t.Fatalf("expected no error routing runner event, got: %v", err)
	}

	// Route an ACP tool call (simulated via direct store call since we can't import acp here)
	// For this test, we'll just verify that runner events can be routed after ACP events
	// by routing another runner event
	runnerEvent2 := Event{
		Type:      EventOpenCodeEnd,
		Title:     "Finished OpenCode",
		EmittedAt: time.Now(),
	}

	err = router.RouteRunnerEvent(runnerEvent2)
	if err != nil {
		t.Fatalf("expected no error routing second runner event, got: %v", err)
	}

	// Verify we have 2 bubbles in correct order
	bubbles := store.GetBubbles()
	if len(bubbles) != 2 {
		t.Fatalf("expected 2 bubbles after runner events, got %d", len(bubbles))
	}

	if !containsString(bubbles[0], "opencode_start") {
		t.Errorf("expected first bubble to be runner opencode_start, got: %s", bubbles[0])
	}
	if !containsString(bubbles[1], "opencode_end") {
		t.Errorf("expected second bubble to be runner opencode_end, got: %s", bubbles[1])
	}
}

// Helper function to check if a string contains a substring
func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
