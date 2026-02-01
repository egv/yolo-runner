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

	_, expectedViewportHeight := logViewportSize(80, 24)
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
	// Log bubble fills height minus statusbar bubble
	// Statusbar bubble occupies the last lines
	expectedTotalLines := 10
	expectedViewportLines := 10 - statusbarHeight

	if len(lines) != expectedTotalLines {
		t.Fatalf("expected view to have %d lines total (height), got %d lines: %q", expectedTotalLines, len(lines), view)
	}

	// Verify viewport lines count
	viewportLineCount := len(lines) - statusbarHeight
	if viewportLineCount != expectedViewportLines {
		t.Fatalf("expected viewport to occupy %d lines in rendered view, got %d lines. View: %q", expectedViewportLines, viewportLineCount, view)
	}

	statusbarIndex := -1
	for i, line := range lines {
		if strings.Contains(line, "getting task info") && strings.Contains(line, "task-1") {
			statusbarIndex = i
		}
	}
	if statusbarIndex == -1 {
		t.Fatalf("expected statusbar line not found")
	}
	if statusbarIndex != expectedTotalLines-2 {
		t.Fatalf("expected statusbar content at line %d, found at line %d", expectedTotalLines-2, statusbarIndex)
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

	logBottomLine := lines[len(lines)-statusbarHeight-1]
	if !strings.HasPrefix(logBottomLine, border.BottomLeft) {
		t.Fatalf("expected log bubble bottom border before statusbar, got %q", logBottomLine)
	}
}

// TestModelStatusBarIsExactlyOneLine verifies that the statusbar renders with its bubble height
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

	// Statusbar bubble should render with the configured height
	statusbarLines := strings.Split(strings.TrimRight(statusbarView, "\n"), "\n")
	if len(statusbarLines) != statusbarHeight {
		t.Fatalf("expected statusbar to be %d lines, got %d lines", statusbarHeight, len(statusbarLines))
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

	// Statusbar content should be within the last statusbarHeight lines
	statusbarIndex := -1
	for i, line := range lines {
		if strings.Contains(line, "task-1") && strings.Contains(line, "getting task info") {
			statusbarIndex = i
		}
	}
	if statusbarIndex == -1 {
		t.Fatalf("expected statusbar content line to be present")
	}
	if statusbarIndex < len(lines)-statusbarHeight {
		t.Fatalf("expected statusbar content near bottom, found at line %d", statusbarIndex)
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
			if i >= len(lines)-statusbarHeight {
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

			_, expectedViewportHeight := logViewportSize(tc.width, tc.height)
			if m.viewport.Height != expectedViewportHeight {
				t.Fatalf("expected viewport height to be %d for window size %dx%d, got %d",
					expectedViewportHeight, tc.width, tc.height, m.viewport.Height)
			}

			// Get the view and verify layout
			view := strings.TrimSpace(m.View())
			lines := strings.Split(view, "\n")

			// Statusbar content should be within the last statusbarHeight lines
			statusbarIndex := -1
			for i, line := range lines {
				if strings.Contains(line, "task-1") && strings.Contains(line, "getting task info") {
					statusbarIndex = i
				}
			}
			if statusbarIndex == -1 {
				t.Fatalf("expected statusbar content for size %dx%d, got: %q", tc.width, tc.height, view)
			}
			if statusbarIndex < len(lines)-statusbarHeight {
				t.Fatalf("expected statusbar near bottom for size %dx%d, got index %d", tc.width, tc.height, statusbarIndex)
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

	_, expectedViewportHeight := logViewportSize(40, 5)
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

	// Statusbar content should be within the last statusbarHeight lines
	statusbarIndex := -1
	for i, line := range lines {
		if strings.Contains(line, "task-1") && strings.Contains(line, "getting task info") {
			statusbarIndex = i
		}
	}
	if statusbarIndex == -1 {
		t.Fatalf("expected statusbar content for small window, got: %q", view)
	}
	if statusbarIndex < len(lines)-statusbarHeight {
		t.Fatalf("expected statusbar near bottom for small window, got index %d", statusbarIndex)
	}
}
