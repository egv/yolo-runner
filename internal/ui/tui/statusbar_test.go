package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/anomalyco/yolo-runner/internal/runner"
)

// This file contains comprehensive tests for StatusBar formatting behavior.
// Following TDD principles: tests define expected behavior, implementation makes them pass.
// Since StatusBar implementation already exists, these tests verify correct formatting.
// If implementation was missing, these tests would fail (red), prompting implementation.

// TestStatusBarViewEmptyState verifies StatusBar renders correctly with no data
func TestStatusBarViewEmptyState(t *testing.T) {
	sb := NewStatusBar()
	sb.SetWidth(80)

	view := sb.View()

	// Empty status bar should still render (with styling)
	if view == "" {
		t.Fatal("expected non-empty view even with no data")
	}

	// Should not contain any dynamic content
	if strings.Contains(view, "task-") {
		t.Fatalf("did not expect task ID in empty state, got: %q", view)
	}

	if strings.Contains(view, "[0/0]") || strings.Contains(view, "[1/1]") {
		t.Fatalf("did not expect progress in empty state, got: %q", view)
	}
}

// TestStatusBarViewWithSpinner verifies spinner is displayed
func TestStatusBarViewWithSpinner(t *testing.T) {
	sb := NewStatusBar()
	sb.SetWidth(80)

	sb, _ = sb.Update(UpdateStatusBarMsg{
		Event:   runner.Event{},
		Spinner: "⠋",
	})

	view := sb.View()

	// Should contain the spinner character
	if !strings.Contains(view, "⠋") {
		t.Fatalf("expected spinner '⠋' in view, got: %q", view)
	}
}

// TestStatusBarViewWithProgress verifies progress is formatted correctly
func TestStatusBarViewWithProgress(t *testing.T) {
	sb := NewStatusBar()
	sb.SetWidth(80)

	testCases := []struct {
		completed int
		total     int
		expected  string
	}{
		{1, 5, "[1/5]"},
		{0, 10, "[0/10]"},
		{5, 5, "[5/5]"},
		{100, 200, "[100/200]"},
	}

	for _, tc := range testCases {
		t.Run(tc.expected, func(t *testing.T) {
			sb := NewStatusBar()
			sb.SetWidth(80)

			sb, _ = sb.Update(UpdateStatusBarMsg{
				Event: runner.Event{
					ProgressCompleted: tc.completed,
					ProgressTotal:     tc.total,
				},
			})

			view := sb.View()

			if !strings.Contains(view, tc.expected) {
				t.Fatalf("expected progress %q in view, got: %q", tc.expected, view)
			}
		})
	}
}

// TestStatusBarViewNoProgressWhenTotalIsZero verifies progress not shown when total is 0
func TestStatusBarViewNoProgressWhenTotalIsZero(t *testing.T) {
	sb := NewStatusBar()
	sb.SetWidth(80)

	sb, _ = sb.Update(UpdateStatusBarMsg{
		Event: runner.Event{
			ProgressCompleted: 0,
			ProgressTotal:     0,
		},
	})

	view := sb.View()

	// Should not contain progress brackets when total is 0
	if strings.Contains(view, "[0/0]") {
		t.Fatalf("did not expect progress display when total is 0, got: %q", view)
	}
}

// TestStatusBarViewWithPhase verifies phase text is displayed
func TestStatusBarViewWithPhase(t *testing.T) {
	sb := NewStatusBar()
	sb.SetWidth(80)

	testCases := []struct {
		eventType runner.EventType
		phase     string
	}{
		{runner.EventSelectTask, "getting task info"},
		{runner.EventBeadsUpdate, "updating task status"},
		{runner.EventOpenCodeStart, "starting opencode"},
		{runner.EventOpenCodeEnd, "opencode finished"},
		{runner.EventGitAdd, "adding changes"},
		{runner.EventGitStatus, "checking status"},
		{runner.EventGitCommit, "committing changes"},
		{runner.EventBeadsClose, "closing task"},
		{runner.EventBeadsVerify, "verifying closure"},
		{runner.EventBeadsSync, "syncing beads"},
	}

	for _, tc := range testCases {
		t.Run(tc.phase, func(t *testing.T) {
			sb := NewStatusBar()
			sb.SetWidth(80)

			sb, _ = sb.Update(UpdateStatusBarMsg{
				Event: runner.Event{
					Type: tc.eventType,
				},
			})

			view := sb.View()

			if !strings.Contains(view, tc.phase) {
				t.Fatalf("expected phase %q in view, got: %q", tc.phase, view)
			}
		})
	}
}

// TestStatusBarViewWithTaskID verifies task ID is displayed
func TestStatusBarViewWithTaskID(t *testing.T) {
	sb := NewStatusBar()
	sb.SetWidth(80)

	testCases := []string{
		"task-1",
		"task-abc123",
		"bead-xyz",
		"issue-12345",
	}

	for _, taskID := range testCases {
		t.Run(taskID, func(t *testing.T) {
			sb := NewStatusBar()
			sb.SetWidth(80)

			sb, _ = sb.Update(UpdateStatusBarMsg{
				Event: runner.Event{
					IssueID: taskID,
				},
			})

			view := sb.View()

			if !strings.Contains(view, taskID) {
				t.Fatalf("expected task ID %q in view, got: %q", taskID, view)
			}
		})
	}
}

// TestStatusBarViewWithModel verifies model is formatted with brackets
func TestStatusBarViewWithModel(t *testing.T) {
	sb := NewStatusBar()
	sb.SetWidth(80)

	testCases := []struct {
		model    string
		expected string
	}{
		{"claude-3-5-sonnet", "[claude-3-5-sonnet]"},
		{"gpt-4", "[gpt-4]"},
		{"gemini-pro", "[gemini-pro]"},
		{"o1-preview", "[o1-preview]"},
	}

	for _, tc := range testCases {
		t.Run(tc.model, func(t *testing.T) {
			sb := NewStatusBar()
			sb.SetWidth(80)

			sb, _ = sb.Update(UpdateStatusBarMsg{
				Event: runner.Event{
					Model: tc.model,
				},
			})

			view := sb.View()

			if !strings.Contains(view, tc.expected) {
				t.Fatalf("expected model %q in view, got: %q", tc.expected, view)
			}
		})
	}
}

// TestStatusBarViewWithLastOutputAge verifies last output age is formatted with parentheses
func TestStatusBarViewWithLastOutputAge(t *testing.T) {
	sb := NewStatusBar()
	sb.SetWidth(80)

	testCases := []struct {
		age      string
		expected string
	}{
		{"5s", "(5s)"},
		{"10s", "(10s)"},
		{"60s", "(60s)"},
		{"0s", "(0s)"},
	}

	for _, tc := range testCases {
		t.Run(tc.age, func(t *testing.T) {
			sb := NewStatusBar()
			sb.SetWidth(80)

			sb, _ = sb.Update(UpdateStatusBarMsg{
				LastOutputAge: tc.age,
			})

			view := sb.View()

			if !strings.Contains(view, tc.expected) {
				t.Fatalf("expected age %q in view, got: %q", tc.expected, view)
			}
		})
	}
}

// TestStatusBarViewWithAllComponents verifies all components are displayed together
func TestStatusBarViewWithAllComponents(t *testing.T) {
	sb := NewStatusBar()
	sb.SetWidth(80)

	sb, _ = sb.Update(UpdateStatusBarMsg{
		Event: runner.Event{
			Type:              runner.EventOpenCodeStart,
			IssueID:           "task-123",
			ProgressCompleted: 2,
			ProgressTotal:     5,
			Model:             "claude-3-5-sonnet",
		},
		LastOutputAge: "10s",
		Spinner:       "⠋",
	})

	view := sb.View()

	// Should contain all components
	expectedComponents := []string{
		"⠋",                   // spinner
		"[2/5]",               // progress
		"starting opencode",   // phase
		"task-123",            // task ID
		"[claude-3-5-sonnet]", // model
		"(10s)",               // age
	}

	for _, comp := range expectedComponents {
		if !strings.Contains(view, comp) {
			t.Fatalf("expected component %q in view, got: %q", comp, view)
		}
	}
}

// TestStatusBarViewOrdering verifies components are ordered correctly
func TestStatusBarViewOrdering(t *testing.T) {
	sb := NewStatusBar()
	sb.SetWidth(120)

	sb, _ = sb.Update(UpdateStatusBarMsg{
		Event: runner.Event{
			Type:              runner.EventOpenCodeStart,
			IssueID:           "task-1",
			ProgressCompleted: 1,
			ProgressTotal:     3,
			Model:             "gpt-4",
		},
		LastOutputAge: "5s",
		Spinner:       "⠋",
	})

	view := sb.View()

	// Verify order: spinner, progress, phase, task ID, model, age
	// We can verify this by checking positions
	spinnerPos := strings.Index(view, "⠋")
	progressPos := strings.Index(view, "[1/3]")
	phasePos := strings.Index(view, "starting opencode")
	taskIDPos := strings.Index(view, "task-1")
	modelPos := strings.Index(view, "[gpt-4]")
	agePos := strings.Index(view, "(5s)")

	// All should be found
	if spinnerPos == -1 || progressPos == -1 || phasePos == -1 ||
		taskIDPos == -1 || modelPos == -1 || agePos == -1 {
		t.Fatal("all components should be present in view")
	}

	// Check ordering
	if spinnerPos > progressPos {
		t.Errorf("spinner should come before progress, got spinner at %d, progress at %d", spinnerPos, progressPos)
	}

	if progressPos > phasePos {
		t.Errorf("progress should come before phase, got progress at %d, phase at %d", progressPos, phasePos)
	}

	if phasePos > taskIDPos {
		t.Errorf("phase should come before task ID, got phase at %d, task ID at %d", phasePos, taskIDPos)
	}

	if taskIDPos > modelPos {
		t.Errorf("task ID should come before model, got task ID at %d, model at %d", taskIDPos, modelPos)
	}

	if modelPos > agePos {
		t.Errorf("model should come before age, got model at %d, age at %d", modelPos, agePos)
	}
}

// TestStatusBarViewStoppingState verifies stopping state formatting
func TestStatusBarViewStoppingState(t *testing.T) {
	sb := NewStatusBar()
	sb.SetWidth(80)

	// Set stopping state
	sb.SetStopping(true)

	view := sb.View()

	// Should show "Stopping..." with red background
	if !strings.Contains(view, "Stopping...") {
		t.Fatalf("expected 'Stopping...' in view, got: %q", view)
	}

	// Should not show any other content when stopping
	if strings.Contains(view, "task-") || strings.Contains(view, "[") {
		t.Fatalf("did not expect other content in stopping state, got: %q", view)
	}
}

// TestStatusBarViewStoppingOverridesNormalContent verifies stopping state hides normal content
func TestStatusBarViewStoppingOverridesNormalContent(t *testing.T) {
	sb := NewStatusBar()
	sb.SetWidth(80)

	// First set normal content
	sb, _ = sb.Update(UpdateStatusBarMsg{
		Event: runner.Event{
			Type:              runner.EventOpenCodeStart,
			IssueID:           "task-123",
			ProgressCompleted: 2,
			ProgressTotal:     5,
			Model:             "claude-3-5",
		},
		LastOutputAge: "10s",
		Spinner:       "⠋",
	})

	normalView := sb.View()

	// Verify normal content is present
	if !strings.Contains(normalView, "task-123") {
		t.Fatal("expected task ID in normal view")
	}

	// Now set stopping state
	sb.SetStopping(true)
	stoppingView := sb.View()

	// Should show "Stopping..."
	if !strings.Contains(stoppingView, "Stopping...") {
		t.Fatalf("expected 'Stopping...' in stopping view, got: %q", stoppingView)
	}

	// Should not show normal content
	if strings.Contains(stoppingView, "task-123") {
		t.Fatalf("did not expect task ID in stopping view, got: %q", stoppingView)
	}

	// Clear stopping state
	sb.SetStopping(false)
	clearedView := sb.View()

	// Normal content should be visible again
	if !strings.Contains(clearedView, "task-123") {
		t.Fatal("expected task ID to reappear after clearing stopping state")
	}
}

// TestStatusBarViewWidth verifies status bar respects width setting
func TestStatusBarViewWidth(t *testing.T) {
	sb := NewStatusBar()

	sb.SetWidth(50)
	sb, _ = sb.Update(UpdateStatusBarMsg{
		Event: runner.Event{
			Type:    runner.EventOpenCodeStart,
			IssueID: "task-1",
		},
	})

	view := sb.View()

	// View should be generated (content may be wrapped by lipgloss)
	if view == "" {
		t.Fatal("expected non-empty view")
	}

	// Width should be updated
	if sb.width != 50 {
		t.Errorf("expected width to be 50, got %d", sb.width)
	}
}

// TestStatusBarUpdateWithWindowSizeMsg verifies width updates with window size
func TestStatusBarUpdateWithWindowSizeMsg(t *testing.T) {
	sb := NewStatusBar()

	updated, _ := sb.Update(tea.WindowSizeMsg{
		Width:  100,
		Height: 24,
	})

	if updated.width != 100 {
		t.Fatalf("expected width to be 100 after window size update, got %d", updated.width)
	}
}

// TestStatusBarPartialContent verifies status bar handles partial data
func TestStatusBarPartialContent(t *testing.T) {
	sb := NewStatusBar()
	sb.SetWidth(80)

	// Only spinner and phase
	sb, _ = sb.Update(UpdateStatusBarMsg{
		Event: runner.Event{
			Type: runner.EventOpenCodeStart,
		},
		Spinner: "⠋",
	})

	view := sb.View()

	// Should have spinner and phase
	if !strings.Contains(view, "⠋") {
		t.Fatal("expected spinner in view")
	}

	if !strings.Contains(view, "starting opencode") {
		t.Fatal("expected phase in view")
	}

	// Should not have progress, task ID, or model
	if strings.Contains(view, "[") {
		t.Fatal("did not expect progress in view")
	}

	if strings.Contains(view, "task-") {
		t.Fatal("did not expect task ID in view")
	}

	if strings.Contains(view, "[") && strings.Contains(view, "]") && strings.Index(view, "[") < strings.Index(view, "]") {
		// Check if there's a model in brackets
		bracketContent := view[strings.Index(view, "[")+1 : strings.Index(view, "]")]
		if len(bracketContent) > 0 && bracketContent[0] >= 'a' && bracketContent[0] <= 'z' {
			t.Fatal("did not expect model in brackets in view")
		}
	}
}

// TestStatusBarNAAge verifies n/a age is displayed when no last output time
func TestStatusBarNAAge(t *testing.T) {
	sb := NewStatusBar()
	sb.SetWidth(80)

	sb, _ = sb.Update(UpdateStatusBarMsg{
		LastOutputAge: "n/a",
	})

	view := sb.View()

	if !strings.Contains(view, "(n/a)") {
		t.Fatalf("expected '(n/a)' in view, got: %q", view)
	}
}
