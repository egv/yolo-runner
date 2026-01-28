package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/anomalyco/yolo-runner/internal/runner"
)

// TestModelViewportHeightIsCalculatedCorrectly verifies that the viewport height
// is calculated as window height - 3 (for title, statusbar, and quit hint)
func TestModelViewportHeightIsCalculatedCorrectly(t *testing.T) {
	fixedNow := time.Date(2026, 1, 19, 12, 0, 10, 0, time.UTC)
	m := NewModel(func() time.Time { return fixedNow })

	// Set window size
	updated, _ := m.Update(tea.WindowSizeMsg{
		Width:  80,
		Height: 24,
	})
	m = updated.(Model)

	// Update with event to trigger viewport rendering
	updated, _ = m.Update(runner.Event{
		Type:      runner.EventSelectTask,
		IssueID:   "task-1",
		Title:     "Example Task",
		EmittedAt: fixedNow.Add(-5 * time.Second),
	})
	m = updated.(Model)

	// Viewport height should be: total height - 3 (title + statusbar + quit hint)
	expectedViewportHeight := 24 - 3 // 24 - 3 = 21
	if m.viewport.Height != expectedViewportHeight {
		t.Fatalf("expected viewport height to be %d (fills available space), got %d", expectedViewportHeight, m.viewport.Height)
	}
}

// TestModelViewportRenderedHeightMatchesExpected verifies that the viewport
// is actually rendered at the expected height in the view output
func TestModelViewportRenderedHeightMatchesExpected(t *testing.T) {
	fixedNow := time.Date(2026, 1, 19, 12, 0, 10, 0, time.UTC)
	m := NewModel(func() time.Time { return fixedNow })

	// Set window size to a small value where we can easily count lines
	updated, _ := m.Update(tea.WindowSizeMsg{
		Width:  80,
		Height: 10,
	})
	m = updated.(Model)

	// Update with event
	updated, _ = m.Update(runner.Event{
		Type:      runner.EventSelectTask,
		IssueID:   "task-1",
		Title:     "Example Task",
		EmittedAt: fixedNow.Add(-5 * time.Second),
	})
	m = updated.(Model)

	// Get the full view
	view := strings.TrimSpace(m.View())
	lines := strings.Split(view, "\n")

	// Expected layout:
	// Line 0: Title (task-1 - Example Task)
	// Line 1-7: Viewport (should be 7 lines = 10 - 3)
	// Line 8: Statusbar
	// Line 9: Quit hint
	expectedTotalLines := 10
	expectedViewportLines := 7 // height - 3

	if len(lines) != expectedTotalLines {
		t.Fatalf("expected view to have %d lines total (height), got %d lines: %q", expectedTotalLines, len(lines), view)
	}

	// Verify viewport lines count
	// Viewport lines should be between title and statusbar
	viewportLineCount := 0
	titleFound := false
	statusbarFound := false
	quitHintFound := false

	for i, line := range lines {
		if strings.Contains(line, "task-1 - Example Task") {
			titleFound = true
		} else if strings.Contains(line, "getting task info") && strings.Contains(line, "task-1") {
			statusbarFound = true
			if i != expectedTotalLines-2 {
				t.Fatalf("expected statusbar at line %d (second to last), found at line %d", expectedTotalLines-2, i)
			}
		} else if strings.Contains(line, "q: stop runner") {
			quitHintFound = true
			if i != expectedTotalLines-1 {
				t.Fatalf("expected quit hint at line %d (last), found at line %d", expectedTotalLines-1, i)
			}
		} else {
			// This should be viewport content
			viewportLineCount++
		}
	}

	if !titleFound {
		t.Fatalf("expected title line not found")
	}
	if !statusbarFound {
		t.Fatalf("expected statusbar line not found")
	}
	if !quitHintFound {
		t.Fatalf("expected quit hint line not found")
	}

	if viewportLineCount != expectedViewportLines {
		t.Fatalf("expected viewport to occupy %d lines in rendered view, got %d lines. View: %q", expectedViewportLines, viewportLineCount, view)
	}
}

// TestModelStatusBarIsExactlyOneLine verifies that the statusbar is exactly 1 line high
func TestModelStatusBarIsExactlyOneLine(t *testing.T) {
	fixedNow := time.Date(2026, 1, 19, 12, 0, 10, 0, time.UTC)
	m := NewModel(func() time.Time { return fixedNow })

	// Set window size
	updated, _ := m.Update(tea.WindowSizeMsg{
		Width:  80,
		Height: 24,
	})
	m = updated.(Model)

	// Update with event to trigger statusbar rendering
	updated, _ = m.Update(runner.Event{
		Type:      runner.EventSelectTask,
		IssueID:   "task-1",
		Title:     "Example Task",
		EmittedAt: fixedNow.Add(-5 * time.Second),
	})
	m = updated.(Model)

	// Get the statusbar view
	statusbarView := m.statusbar.View()

	// Statusbar should be exactly 1 line
	statusbarLines := strings.Split(strings.TrimSpace(statusbarView), "\n")
	if len(statusbarLines) != 1 {
		t.Fatalf("expected statusbar to be exactly 1 line, got %d lines", len(statusbarLines))
	}
}

// TestModelStatusBarPinnedToBottom verifies that the statusbar is always at the bottom
// of the view, just above the quit hint
func TestModelStatusBarPinnedToBottom(t *testing.T) {
	fixedNow := time.Date(2026, 1, 19, 12, 0, 10, 0, time.UTC)
	m := NewModel(func() time.Time { return fixedNow })

	// Set window size
	updated, _ := m.Update(tea.WindowSizeMsg{
		Width:  80,
		Height: 24,
	})
	m = updated.(Model)

	// Update with event to trigger rendering
	updated, _ = m.Update(runner.Event{
		Type:      runner.EventSelectTask,
		IssueID:   "task-1",
		Title:     "Example Task",
		EmittedAt: fixedNow.Add(-5 * time.Second),
	})
	m = updated.(Model)

	// Get the full view
	view := strings.TrimSpace(m.View())
	lines := strings.Split(view, "\n")

	// The last line should be the quit hint
	lastLine := lines[len(lines)-1]
	if !strings.Contains(lastLine, "q: stop runner") {
		t.Fatalf("expected last line to be quit hint, got: %q", lastLine)
	}

	// The line before the last should be the statusbar
	secondToLastLine := lines[len(lines)-2]
	if !strings.Contains(secondToLastLine, "task-1") {
		t.Fatalf("expected line before quit hint to be statusbar (containing task-1), got: %q", secondToLastLine)
	}

	// Verify statusbar is always at position len(lines)-2 regardless of content
	// by checking that the viewport content comes before it
	for i := 0; i < len(lines)-2; i++ {
		line := lines[i]
		// Skip the title line which contains task ID
		if strings.Contains(line, "task-1 - Example Task") {
			continue
		}
		// No other line before position len(lines)-2 should contain statusbar content
		if strings.Contains(line, "getting task info") {
			t.Fatalf("expected statusbar content to only be at line %d, found at line %d: %q", len(lines)-2, i, line)
		}
	}
}

// TestModelViewportPositionedAboveStatusBar verifies that the viewport is positioned above
// the statusbar in the layout
func TestModelViewportPositionedAboveStatusBar(t *testing.T) {
	fixedNow := time.Date(2026, 1, 19, 12, 0, 10, 0, time.UTC)
	m := NewModel(func() time.Time { return fixedNow })

	// Set window size
	updated, _ := m.Update(tea.WindowSizeMsg{
		Width:  80,
		Height: 24,
	})
	m = updated.(Model)

	// Update with event to trigger rendering
	updated, _ = m.Update(runner.Event{
		Type:      runner.EventSelectTask,
		IssueID:   "task-1",
		Title:     "Example Task",
		EmittedAt: fixedNow.Add(-5 * time.Second),
	})
	m = updated.(Model)

	// Add viewport content
	m.viewport.SetContent("Log line 1\nLog line 2\nLog line 3")

	// Get the full view
	view := strings.TrimSpace(m.View())
	lines := strings.Split(view, "\n")

	// Find viewport content lines (should be before statusbar)
	viewportContentFound := false
	statusbarFound := false

	for i, line := range lines {
		// Check for quit hint (last line)
		if strings.Contains(line, "q: stop runner") {
			statusbarFound = true
			// Viewport content should be before this line
			continue
		}

		// Check for statusbar line (contains task ID and phase)
		if strings.Contains(line, "task-1") && strings.Contains(line, "getting task info") {
			// Statusbar should be before quit hint
			if i < len(lines)-1 {
				statusbarFound = true
			}
			continue
		}

		// Check for viewport content
		if strings.Contains(line, "Log line") {
			// Viewport content should be before statusbar
			if !statusbarFound {
				viewportContentFound = true
			}
		}
	}

	if !viewportContentFound {
		t.Fatalf("expected viewport content to be present in view, got: %q", view)
	}

	if !statusbarFound {
		t.Fatalf("expected statusbar to be present in view")
	}
}

// TestModelLayoutWithDifferentWindowSizes verifies that the layout works correctly
// for different window sizes, with viewport always filling available space
func TestModelLayoutWithDifferentWindowSizes(t *testing.T) {
	fixedNow := time.Date(2026, 1, 19, 12, 0, 10, 0, time.UTC)

	testCases := []struct {
		width  int
		height int
	}{
		{80, 20},
		{100, 30},
		{120, 40},
		{80, 10},
		{200, 50},
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%dx%d", tc.width, tc.height), func(t *testing.T) {
			m := NewModel(func() time.Time { return fixedNow })

			// Set window size
			updated, _ := m.Update(tea.WindowSizeMsg{
				Width:  tc.width,
				Height: tc.height,
			})
			m = updated.(Model)

			// Update with event
			updated, _ = m.Update(runner.Event{
				Type:      runner.EventSelectTask,
				IssueID:   "task-1",
				Title:     "Example Task",
				EmittedAt: fixedNow.Add(-5 * time.Second),
			})
			m = updated.(Model)

			// Viewport height should be: total height - 3
			expectedViewportHeight := tc.height - 3
			if m.viewport.Height != expectedViewportHeight {
				t.Fatalf("expected viewport height to be %d for window size %dx%d, got %d",
					expectedViewportHeight, tc.width, tc.height, m.viewport.Height)
			}

			// Get the view and verify layout
			view := strings.TrimSpace(m.View())
			lines := strings.Split(view, "\n")

			// Last line should be quit hint
			lastLine := lines[len(lines)-1]
			if !strings.Contains(lastLine, "q: stop runner") {
				t.Fatalf("expected last line to be quit hint for size %dx%d, got: %q", tc.width, tc.height, lastLine)
			}

			// Line before last should be statusbar
			if len(lines) < 2 {
				t.Fatalf("expected at least 2 lines in view for size %dx%d", tc.width, tc.height)
			}

			secondToLastLine := lines[len(lines)-2]
			if !strings.Contains(secondToLastLine, "task-1") {
				t.Fatalf("expected line before quit hint to be statusbar for size %dx%d, got: %q", tc.width, tc.height, secondToLastLine)
			}
		})
	}
}

// TestModelViewportHeightConsistency verifies that viewport height is consistent
// across updates and doesn't change unexpectedly
func TestModelViewportHeightConsistency(t *testing.T) {
	fixedNow := time.Date(2026, 1, 19, 12, 0, 10, 0, time.UTC)
	m := NewModel(func() time.Time { return fixedNow })

	// Set initial window size
	updated, _ := m.Update(tea.WindowSizeMsg{
		Width:  80,
		Height: 24,
	})
	m = updated.(Model)

	// Update with event
	updated, _ = m.Update(runner.Event{
		Type:      runner.EventSelectTask,
		IssueID:   "task-1",
		Title:     "Example Task",
		EmittedAt: fixedNow.Add(-5 * time.Second),
	})
	m = updated.(Model)

	// Store viewport height
	initialViewportHeight := m.viewport.Height

	// Update with another event
	updated, _ = m.Update(runner.Event{
		Type:              runner.EventBeadsUpdate,
		IssueID:           "task-1",
		Title:             "Example Task",
		ProgressCompleted: 1,
		ProgressTotal:     5,
		EmittedAt:         fixedNow.Add(-5 * time.Second),
	})
	m = updated.(Model)

	// Viewport height should remain the same
	if m.viewport.Height != initialViewportHeight {
		t.Fatalf("expected viewport height to remain %d after event update, got %d", initialViewportHeight, m.viewport.Height)
	}
}

// TestModelLayoutWithSmallWindowSize verifies that the layout works correctly
// even with very small window sizes
func TestModelLayoutWithSmallWindowSize(t *testing.T) {
	fixedNow := time.Date(2026, 1, 19, 12, 0, 10, 0, time.UTC)
	m := NewModel(func() time.Time { return fixedNow })

	// Set a very small window size (minimum viable)
	updated, _ := m.Update(tea.WindowSizeMsg{
		Width:  40,
		Height: 5,
	})
	m = updated.(Model)

	// Update with event
	updated, _ = m.Update(runner.Event{
		Type:      runner.EventSelectTask,
		IssueID:   "task-1",
		Title:     "Example Task",
		EmittedAt: fixedNow.Add(-5 * time.Second),
	})
	m = updated.(Model)

	// Viewport height should be: 5 - 3 = 2 lines
	expectedViewportHeight := 2
	if m.viewport.Height != expectedViewportHeight {
		t.Fatalf("expected viewport height to be %d for small window, got %d", expectedViewportHeight, m.viewport.Height)
	}

	// Get view and verify layout
	view := strings.TrimSpace(m.View())
	lines := strings.Split(view, "\n")

	// Should have exactly 5 lines (the window height)
	if len(lines) != 5 {
		t.Fatalf("expected view to have %d lines for window height 5, got %d lines", 5, len(lines))
	}

	// Last line should be quit hint
	lastLine := lines[len(lines)-1]
	if !strings.Contains(lastLine, "q: stop runner") {
		t.Fatalf("expected last line to be quit hint for small window, got: %q", lastLine)
	}

	// Line before last should be statusbar
	secondToLastLine := lines[len(lines)-2]
	if !strings.Contains(secondToLastLine, "task-1") {
		t.Fatalf("expected line before quit hint to be statusbar for small window, got: %q", secondToLastLine)
	}
}
