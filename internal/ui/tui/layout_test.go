package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/anomalyco/yolo-runner/internal/runner"
)

// TestModelViewportHeightIsCalculatedCorrectly verifies that the viewport height
// is calculated as window height - 1 (statusbar) - 2 (log bubble border)
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

	// Viewport height should be: total height - 1 (statusbar) - 2 (log bubble border)
	expectedViewportHeight := 24 - 1 - 2 // 24 - 3 = 21
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
	// Line 0-8: Log bubble (should be 9 lines = 10 - 1)
	// Line 9: Statusbar
	expectedTotalLines := 10
	expectedViewportLines := 9 // height - 1 (log bubble height)

	if len(lines) != expectedTotalLines {
		t.Fatalf("expected view to have %d lines total (height), got %d lines: %q", expectedTotalLines, len(lines), view)
	}

	// Verify viewport lines count
	// Viewport lines should be above statusbar
	viewportLineCount := 0
	statusbarFound := false

	for i, line := range lines {
		if strings.Contains(line, "getting task info") && strings.Contains(line, "task-1") {
			statusbarFound = true
			if i != expectedTotalLines-1 {
				t.Fatalf("expected statusbar at line %d (last), found at line %d", expectedTotalLines-1, i)
			}
		} else {
			// This should be viewport content
			viewportLineCount++
		}
	}

	if !statusbarFound {
		t.Fatalf("expected statusbar line not found")
	}

	if viewportLineCount != expectedViewportLines {
		t.Fatalf("expected viewport to occupy %d lines in rendered view, got %d lines. View: %q", expectedViewportLines, viewportLineCount, view)
	}
	if strings.Contains(view, "q: stop runner") {
		t.Fatalf("expected quit hint to be removed from view, got: %q", view)
	}
}

func TestModelLogBubbleUsesBorder(t *testing.T) {
	fixedNow := time.Date(2026, 1, 19, 12, 0, 10, 0, time.UTC)
	m := NewModel(func() time.Time { return fixedNow })

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 8})
	m = updated.(Model)

	updated, _ = m.Update(runner.Event{
		Type:      runner.EventSelectTask,
		IssueID:   "task-1",
		Title:     "Example Task",
		EmittedAt: fixedNow.Add(-5 * time.Second),
	})
	m = updated.(Model)

	updated, _ = m.Update(AppendLogMsg{Line: "Log line"})
	m = updated.(Model)

	view := strings.TrimRight(m.View(), "\n")
	lines := strings.Split(view, "\n")
	if len(lines) < 2 {
		t.Fatalf("expected view to have multiple lines, got %q", view)
	}

	border := lipgloss.NormalBorder()
	if !strings.HasPrefix(lines[0], border.TopLeft) {
		t.Fatalf("expected log bubble to start with border top-left, got %q", lines[0])
	}

	logBottomLine := lines[len(lines)-2]
	if !strings.HasPrefix(logBottomLine, border.BottomLeft) {
		t.Fatalf("expected log bubble bottom border before statusbar, got %q", logBottomLine)
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
// of the view
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

	// The last line should be the statusbar
	lastLine := lines[len(lines)-1]
	if !strings.Contains(lastLine, "task-1") {
		t.Fatalf("expected last line to be statusbar (containing task-1), got: %q", lastLine)
	}

	// Verify statusbar is always at position len(lines)-1 regardless of content
	// by checking that the viewport content comes before it
	for i := 0; i < len(lines)-1; i++ {
		line := lines[i]
		// No other line before position len(lines)-1 should contain statusbar content
		if strings.Contains(line, "getting task info") {
			t.Fatalf("expected statusbar content to only be at line %d, found at line %d: %q", len(lines)-1, i, line)
		}
	}
}

// TestModelViewportPositionedAboveStatusBar verifies that the log bubble is positioned above
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

	// Find log bubble content lines (should be before statusbar)
	viewportContentFound := false
	statusbarFound := false

	for i, line := range lines {
		// Check for statusbar line (contains task ID and phase)
		if strings.Contains(line, "task-1") && strings.Contains(line, "getting task info") {
			// Statusbar should be the last line
			if i == len(lines)-1 {
				statusbarFound = true
			}
			continue
		}

		// Check for log bubble content
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
// for different window sizes, with log bubble always filling available space
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

			// Viewport height should be: total height - 1 (statusbar) - 2 (log bubble border)
			expectedViewportHeight := tc.height - 1 - 2
			if m.viewport.Height != expectedViewportHeight {
				t.Fatalf("expected viewport height to be %d for window size %dx%d, got %d",
					expectedViewportHeight, tc.width, tc.height, m.viewport.Height)
			}

			// Get the view and verify layout
			view := strings.TrimSpace(m.View())
			lines := strings.Split(view, "\n")

			// Last line should be statusbar
			lastLine := lines[len(lines)-1]
			if !strings.Contains(lastLine, "task-1") {
				t.Fatalf("expected last line to be statusbar for size %dx%d, got: %q", tc.width, tc.height, lastLine)
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

	// Viewport height should be: 5 - 1 (statusbar) - 2 (log bubble border) = 2 lines
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

	// Last line should be statusbar
	lastLine := lines[len(lines)-1]
	if !strings.Contains(lastLine, "task-1") {
		t.Fatalf("expected last line to be statusbar for small window, got: %q", lastLine)
	}
}
