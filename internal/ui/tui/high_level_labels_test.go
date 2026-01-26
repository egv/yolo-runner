package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/anomalyco/yolo-runner/internal/runner"
)

func TestTUIOnlyShowsHighLevelActionLabels(t *testing.T) {
	// Create a fixed time function for deterministic testing
	fixedTime := time.Date(2026, 1, 26, 12, 0, 0, 0, time.UTC)

	model := NewModelWithStop(func() time.Time { return fixedTime }, nil)

	// Test cases for different events that should show high-level labels
	testCases := []struct {
		name          string
		event         runner.Event
		expectedLabel string
	}{
		{
			name: "SelectTask shows 'getting task info'",
			event: runner.Event{
				Type:      runner.EventSelectTask,
				IssueID:   "task-1",
				Title:     "Test Task",
				EmittedAt: fixedTime,
			},
			expectedLabel: "getting task info",
		},
		{
			name: "BeadsUpdate shows 'updating task status'",
			event: runner.Event{
				Type:      runner.EventBeadsUpdate,
				IssueID:   "task-1",
				Title:     "Test Task",
				EmittedAt: fixedTime,
			},
			expectedLabel: "updating task status",
		},
		{
			name: "OpenCodeStart shows 'starting opencode'",
			event: runner.Event{
				Type:      runner.EventOpenCodeStart,
				IssueID:   "task-1",
				Title:     "Test Task",
				EmittedAt: fixedTime,
			},
			expectedLabel: "starting opencode",
		},
		{
			name: "OpenCodeEnd shows 'opencode finished'",
			event: runner.Event{
				Type:      runner.EventOpenCodeEnd,
				IssueID:   "task-1",
				Title:     "Test Task",
				EmittedAt: fixedTime,
			},
			expectedLabel: "opencode finished",
		},
		{
			name: "GitAdd shows 'adding changes'",
			event: runner.Event{
				Type:      runner.EventGitAdd,
				IssueID:   "task-1",
				Title:     "Test Task",
				EmittedAt: fixedTime,
			},
			expectedLabel: "adding changes",
		},
		{
			name: "GitStatus shows 'checking status'",
			event: runner.Event{
				Type:      runner.EventGitStatus,
				IssueID:   "task-1",
				Title:     "Test Task",
				EmittedAt: fixedTime,
			},
			expectedLabel: "checking status",
		},
		{
			name: "GitCommit shows 'committing changes'",
			event: runner.Event{
				Type:      runner.EventGitCommit,
				IssueID:   "task-1",
				Title:     "Test Task",
				EmittedAt: fixedTime,
			},
			expectedLabel: "committing changes",
		},
		{
			name: "BeadsClose shows 'closing task'",
			event: runner.Event{
				Type:      runner.EventBeadsClose,
				IssueID:   "task-1",
				Title:     "Test Task",
				EmittedAt: fixedTime,
			},
			expectedLabel: "closing task",
		},
		{
			name: "BeadsVerify shows 'verifying closure'",
			event: runner.Event{
				Type:      runner.EventBeadsVerify,
				IssueID:   "task-1",
				Title:     "Test Task",
				EmittedAt: fixedTime,
			},
			expectedLabel: "verifying closure",
		},
		{
			name: "BeadsSync shows 'syncing beads'",
			event: runner.Event{
				Type:      runner.EventBeadsSync,
				IssueID:   "task-1",
				Title:     "Test Task",
				EmittedAt: fixedTime,
			},
			expectedLabel: "syncing beads",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Update model with the event
			updatedModel, _ := model.Update(tc.event)

			// Get the view output
			view := updatedModel.View()

			// Verify that the expected high-level label appears in the view
			if !strings.Contains(view, tc.expectedLabel) {
				t.Fatalf("expected view to contain '%s', got: %s", tc.expectedLabel, view)
			}

			// Verify that no command echo appears (no '$' or command artifacts)
			if strings.Contains(view, "$") || strings.Contains(view, "bd ") || strings.Contains(view, "git ") {
				t.Fatalf("view should not contain command echo, got: %s", view)
			}

			// Verify that raw event types don't appear in the view
			if strings.Contains(view, string(tc.event.Type)) {
				t.Fatalf("view should not contain raw event type '%s', got: %s", tc.event.Type, view)
			}
		})
	}
}

func TestTUIOnlyShowsHighLevelActionLabels_NoCommandEcho(t *testing.T) {
	// Create a fixed time function for deterministic testing
	fixedTime := time.Date(2026, 1, 26, 12, 0, 0, 0, time.UTC)

	model := NewModelWithStop(func() time.Time { return fixedTime }, nil)

	// Test that command echoes don't appear in the view
	testCases := []string{
		"$ bd ready --parent root --json",
		"$ git add .",
		"$ git commit -m 'message'",
		"bd ready --parent root --json",
		"git add .",
		"git commit -m 'message'",
	}

	for _, commandEcho := range testCases {
		t.Run("No command echo for: "+commandEcho, func(t *testing.T) {
			// Update model with a typical event
			event := runner.Event{
				Type:      runner.EventBeadsUpdate,
				IssueID:   "task-1",
				Title:     "Test Task",
				EmittedAt: fixedTime,
			}

			updatedModel, _ := model.Update(event)
			view := updatedModel.View()

			// Verify that no command echo appears in the view
			if strings.Contains(view, commandEcho) {
				t.Fatalf("view should not contain command echo '%s', got: %s", commandEcho, view)
			}
		})
	}
}
