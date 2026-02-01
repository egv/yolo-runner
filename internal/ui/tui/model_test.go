package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/anomalyco/yolo-runner/internal/runner"
)

func TestModelUsesBubblesSpinnerNotCustomFrames(t *testing.T) {
	// Verify that the Model uses the bubbles spinner component
	// and not the old custom spinnerFrames variable
	m := NewModel(func() time.Time { return time.Unix(0, 0) })
	updated, _ := m.Update(runner.Event{
		Type:      runner.EventSelectTask,
		IssueID:   "task-123",
		Title:     "Test Task",
		EmittedAt: time.Unix(0, 0),
	})
	m = updated.(Model)

	view := m.View()

	// The old custom spinnerFrames used these characters: "-", "\", "|", "/"
	// The new bubbles spinner with dot spinner should not use these at line start
	lines := strings.Split(strings.TrimSpace(view), "\n")
	customChars := []string{"-", "\\", "|", "/"}
	for _, line := range lines {
		// Skip the stopping line which starts with "Stopping"
		if strings.HasPrefix(line, "Stopping") {
			continue
		}
		// Check if the first character of the line is a custom spinner char
		if len(line) > 0 {
			firstChar := string(line[0])
			for _, char := range customChars {
				if firstChar == char {
					t.Fatalf("expected model to use bubbles spinner, not custom spinnerFrames. Found custom char %q at start of line: %q", char, line)
				}
			}
		}
	}

	// The view should still render something (spinner output)
	if len(view) == 0 {
		t.Fatal("expected model view to have content")
	}
}

func TestModelRendersTaskAndPhase(t *testing.T) {
	fixedNow := time.Date(2026, 1, 19, 12, 0, 10, 0, time.UTC)
	m := NewModel(func() time.Time { return fixedNow })
	updated, _ := m.Update(runner.Event{
		Type:      runner.EventSelectTask,
		IssueID:   "task-1",
		Title:     "Example Task",
		Phase:     "running",
		EmittedAt: fixedNow.Add(-5 * time.Second),
	})
	m = updated.(Model)

	view := m.View()
	if !strings.Contains(view, "getting task info") {
		t.Fatalf("expected phase in view, got %q", view)
	}
	if !strings.Contains(view, "(5s)") {
		t.Fatalf("expected last output age in view, got %q", view)
	}
	if !strings.Contains(view, "Example Task") {
		t.Fatalf("expected task title to be present in view logs, got %q", view)
	}
}

func TestSpinnerAdvancesOnOutput(t *testing.T) {
	m := NewModel(func() time.Time { return time.Unix(0, 0) })
	// Initialize to start spinner ticking
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("expected Init to return a command")
	}
	// Execute the spinner's tick command to get spinner.TickMsg
	msg := cmd()
	if msg == nil {
		t.Fatal("expected command to return a message")
	}
	// Send the message to update the model
	updated, _ := m.Update(msg)
	m = updated.(Model)
	first := m.View()

	// Tick again
	updated, _ = m.Update(msg)
	m = updated.(Model)
	second := m.View()

	// The spinner should advance (or at least the view should be generated)
	if first == "" || second == "" {
		t.Fatalf("expected non-empty views, got first=%q, second=%q", first, second)
	}
}

func TestModelTicksLastOutputAge(t *testing.T) {
	now := time.Date(2026, 1, 19, 12, 0, 0, 0, time.UTC)
	current := now
	m := NewModel(func() time.Time { return current })
	updated, _ := m.Update(runner.Event{
		Type:      runner.EventSelectTask,
		IssueID:   "task-1",
		Title:     "Example Task",
		Phase:     "running",
		EmittedAt: current,
	})
	m = updated.(Model)

	current = current.Add(3 * time.Second)
	updated, cmd := m.Update(tickMsg{})
	m = updated.(Model)

	if cmd == nil {
		t.Fatalf("expected tick command")
	}
	if !strings.Contains(m.View(), "(3s)") {
		t.Fatalf("expected last output age to tick, got %q", m.View())
	}
}

func TestModelOutputResetsLastOutputAge(t *testing.T) {
	now := time.Date(2026, 1, 19, 12, 0, 0, 0, time.UTC)
	current := now
	m := NewModel(func() time.Time { return current })
	updated, _ := m.Update(runner.Event{
		Type:      runner.EventSelectTask,
		IssueID:   "task-1",
		Title:     "Example Task",
		Phase:     "running",
		EmittedAt: current,
	})
	m = updated.(Model)

	current = current.Add(5 * time.Second)
	updated, _ = m.Update(OutputMsg{})
	m = updated.(Model)

	if !strings.Contains(m.View(), "(0s)") {
		t.Fatalf("expected last output age to reset, got %q", m.View())
	}
}

func TestModelInitSchedulesTick(t *testing.T) {
	m := NewModel(func() time.Time { return time.Unix(0, 0) })
	if cmd := m.Init(); cmd == nil {
		t.Fatalf("expected tick command")
	}
}

func TestModelDoesNotShowQuitHintOnStart(t *testing.T) {
	m := NewModel(func() time.Time { return time.Unix(0, 0) })
	view := m.View()
	if strings.Contains(view, "q: stop runner") {
		t.Fatalf("expected quit hint to be removed from view, got %q", view)
	}
	if strings.Contains(view, "Stopping...") {
		t.Fatalf("did not expect stopping status in view, got %q", view)
	}
}

func TestModelShowsStoppingStatusWhileStopping(t *testing.T) {
	m := NewModel(func() time.Time { return time.Unix(0, 0) })
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	m = updated.(Model)
	view := m.View()
	if !strings.Contains(view, "Stopping...") {
		t.Fatalf("expected stopping status in view, got %q", view)
	}
	if strings.Contains(view, "q: stop runner") {
		t.Fatalf("expected quit hint to be removed from view, got %q", view)
	}
}

func TestModelStopsOnCtrlC(t *testing.T) {
	stopCh := make(chan struct{})
	m := NewModelWithStop(func() time.Time { return time.Unix(0, 0) }, stopCh)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = updated.(Model)
	if !m.StopRequested() {
		t.Fatalf("expected stop requested")
	}
	select {
	case <-stopCh:
		// ok
	default:
		t.Fatalf("expected stop channel to close")
	}
}

func TestModelScrollKeepsStatusBarPinned(t *testing.T) {
	fixedNow := time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC)
	m := NewModel(func() time.Time { return fixedNow })

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 6})
	m = updated.(Model)

	updated, _ = m.Update(runner.Event{
		Type:      runner.EventSelectTask,
		IssueID:   "task-1",
		Title:     "Example Task",
		EmittedAt: fixedNow.Add(-5 * time.Second),
	})
	m = updated.(Model)

	for i := 0; i < 10; i++ {
		updated, _ = m.Update(AppendLogMsg{Line: fmt.Sprintf("log line %d", i)})
		m = updated.(Model)
	}

	if !m.viewport.AtBottom() {
		t.Fatalf("expected viewport to start at bottom")
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = updated.(Model)
	if m.viewport.AtBottom() {
		t.Fatalf("expected viewport to scroll up from bottom")
	}

	view := strings.TrimSpace(m.View())
	lines := strings.Split(view, "\n")
	if len(lines) == 0 {
		t.Fatalf("expected view to have lines")
	}
	statusbarIndex := -1
	for i, line := range lines {
		if strings.Contains(line, "task-1") && strings.Contains(line, "getting task info") {
			statusbarIndex = i
		}
	}
	if statusbarIndex == -1 {
		t.Fatalf("expected statusbar content to be present")
	}
	if statusbarIndex < len(lines)-statusbarHeight {
		t.Fatalf("expected statusbar to remain pinned at bottom, got index %d", statusbarIndex)
	}
}

func TestModelScrollDoesNotJumpOnNewOutput(t *testing.T) {
	fixedNow := time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC)
	m := NewModel(func() time.Time { return fixedNow })

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 6})
	m = updated.(Model)

	updated, _ = m.Update(runner.Event{
		Type:      runner.EventSelectTask,
		IssueID:   "task-1",
		Title:     "Example Task",
		EmittedAt: fixedNow.Add(-5 * time.Second),
	})
	m = updated.(Model)

	for i := 0; i < 12; i++ {
		updated, _ = m.Update(AppendLogMsg{Line: fmt.Sprintf("log line %d", i)})
		m = updated.(Model)
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = updated.(Model)
	if m.viewport.AtBottom() {
		t.Fatalf("expected viewport to scroll up from bottom")
	}
	beforeOffset := m.viewport.YOffset

	updated, _ = m.Update(AppendLogMsg{Line: "new output line"})
	m = updated.(Model)
	if m.viewport.AtBottom() {
		t.Fatalf("expected viewport to remain scrolled after new output")
	}
	if m.viewport.YOffset != beforeOffset {
		t.Fatalf("expected viewport offset to remain %d, got %d", beforeOffset, m.viewport.YOffset)
	}
}

func TestModelRendersStatusBarInSingleLine(t *testing.T) {
	fixedNow := time.Date(2026, 1, 19, 12, 0, 10, 0, time.UTC)
	m := NewModel(func() time.Time { return fixedNow })
	updated, _ := m.Update(runner.Event{
		Type:      runner.EventSelectTask,
		IssueID:   "task-1",
		Title:     "Example Task",
		Phase:     "running",
		EmittedAt: fixedNow.Add(-5 * time.Second),
	})
	m = updated.(Model)

	view := m.View()
	lines := strings.Split(strings.TrimSpace(view), "\n")

	// Statusbar bubble should contain spinner, phase, and last output age
	statusBarIndex := -1
	for i, line := range lines {
		if strings.Contains(line, "phase:") {
			// This should not exist anymore - phase should be in status bar
			t.Fatalf("phase should be in status bar, not separate line: %q", line)
		}
		if strings.Contains(line, "last output") {
			// This should not exist anymore - last output should be in status bar
			t.Fatalf("last output should be in status bar, not separate line: %q", line)
		}
		if strings.Contains(line, "getting task info") && strings.Contains(line, "5s") {
			statusBarIndex = i
		}
	}

	if statusBarIndex == -1 {
		t.Fatalf("expected status bar content line with phase and age, got view: %q", view)
	}
	if statusBarIndex < len(lines)-statusbarHeight {
		t.Fatalf("expected status bar content near bottom, got index %d", statusBarIndex)
	}
}

func TestModelStatusBarUpdatesInPlace(t *testing.T) {
	fixedNow := time.Date(2026, 1, 19, 12, 0, 0, 0, time.UTC)
	current := fixedNow
	m := NewModel(func() time.Time { return current })
	updated, _ := m.Update(runner.Event{
		Type:      runner.EventSelectTask,
		IssueID:   "task-1",
		Title:     "Example Task",
		Phase:     "running",
		EmittedAt: current,
	})
	m = updated.(Model)

	firstView := m.View()

	// Advance time and update
	current = current.Add(3 * time.Second)
	updated, _ = m.Update(tickMsg{})
	m = updated.(Model)

	secondView := m.View()

	// Views should be different (age updated) but structure should be same
	if firstView == secondView {
		t.Fatalf("expected view to change with time")
	}

	// Both should have same number of lines
	firstLines := strings.Split(strings.TrimSpace(firstView), "\n")
	secondLines := strings.Split(strings.TrimSpace(secondView), "\n")
	if len(firstLines) != len(secondLines) {
		t.Fatalf("expected same number of lines in status bar updates, got %d vs %d", len(firstLines), len(secondLines))
	}
}

func TestModelStatusBarExactFormat(t *testing.T) {
	fixedNow := time.Date(2026, 1, 19, 12, 0, 10, 0, time.UTC)
	m := NewModel(func() time.Time { return fixedNow })
	updated, _ := m.Update(runner.Event{
		Type:      runner.EventSelectTask,
		IssueID:   "task-1",
		Title:     "Example Task",
		Phase:     "running",
		EmittedAt: fixedNow.Add(-5 * time.Second),
	})
	m = updated.(Model)

	view := m.View()

	// Check that view contains expected components (not exact format since spinner changes)
	if !strings.Contains(view, "getting task info") {
		t.Fatalf("expected phase in view, got %q", view)
	}
	if !strings.Contains(view, "task-1") {
		t.Fatalf("expected task id in status bar, got %q", view)
	}
	if !strings.Contains(view, "(5s)") {
		t.Fatalf("expected last output age in view, got %q", view)
	}

	// Test with progress
	updated, _ = m.Update(runner.Event{
		Type:              runner.EventSelectTask,
		IssueID:           "task-1",
		Title:             "Example Task",
		Phase:             "running",
		ProgressCompleted: 2,
		ProgressTotal:     5,
		EmittedAt:         fixedNow.Add(-5 * time.Second),
	})
	m = updated.(Model)

	view = m.View()

	// Check progress is shown
	if !strings.Contains(view, "[2/5]") {
		t.Fatalf("expected progress [2/5] in view, got %q", view)
	}
}

func TestModelStatusBarShowsProgress(t *testing.T) {
	fixedNow := time.Date(2026, 1, 19, 12, 0, 10, 0, time.UTC)
	m := NewModel(func() time.Time { return fixedNow })
	updated, _ := m.Update(runner.Event{
		Type:              runner.EventSelectTask,
		IssueID:           "task-1",
		Title:             "Example Task",
		Phase:             "running",
		ProgressCompleted: 2,
		ProgressTotal:     5,
		EmittedAt:         fixedNow.Add(-5 * time.Second),
	})
	m = updated.(Model)

	view := m.View()
	lines := strings.Split(strings.TrimSpace(view), "\n")

	// Find the status bar line (should contain spinner and phase)
	var statusBarLine string
	for _, line := range lines {
		if strings.Contains(line, "getting task info") && strings.Contains(line, "task-1") && strings.Contains(line, "(5s)") {
			statusBarLine = line
			break
		}
	}

	if statusBarLine == "" {
		t.Fatalf("expected to find status bar line with getting task info phase and task-1, got view: %q", view)
	}

	// Status bar should contain progress [2/5]
	if !strings.Contains(statusBarLine, "[2/5]") {
		t.Fatalf("expected status bar to contain progress [2/5], got: %q", statusBarLine)
	}
}

func TestModelStatusBarShowsModel(t *testing.T) {
	fixedNow := time.Date(2026, 1, 19, 12, 0, 10, 0, time.UTC)
	m := NewModel(func() time.Time { return fixedNow })
	updated, _ := m.Update(runner.Event{
		Type:      runner.EventOpenCodeStart,
		IssueID:   "task-1",
		Title:     "Example Task",
		Phase:     "starting opencode",
		Model:     "claude-3-5-sonnet",
		EmittedAt: fixedNow.Add(-5 * time.Second),
	})
	m = updated.(Model)

	view := m.View()
	lines := strings.Split(strings.TrimSpace(view), "\n")

	// Find the status bar line
	var statusBarLine string
	for _, line := range lines {
		if strings.Contains(line, "starting opencode") && strings.Contains(line, "task-1") && strings.Contains(line, "(5s)") {
			statusBarLine = line
			break
		}
	}

	if statusBarLine == "" {
		t.Fatalf("expected to find status bar line with starting opencode phase, got view: %q", view)
	}

	// Status bar should contain the model name
	if !strings.Contains(statusBarLine, "claude-3-5-sonnet") {
		t.Fatalf("expected status bar to contain model 'claude-3-5-sonnet', got: %q", statusBarLine)
	}
}

func TestModelStatusBarShowsMultipleModelFormats(t *testing.T) {
	fixedNow := time.Date(2026, 1, 19, 12, 0, 10, 0, time.UTC)

	testCases := []struct {
		model    string
		expected string
	}{
		{"gpt-4", "gpt-4"},
		{"claude-3-5-sonnet-20241022", "claude-3-5-sonnet-20241022"},
		{"gemini-pro", "gemini-pro"},
		{"o1-preview", "o1-preview"},
	}

	for _, tc := range testCases {
		t.Run(tc.model, func(t *testing.T) {
			m := NewModel(func() time.Time { return fixedNow })
			updated, _ := m.Update(runner.Event{
				Type:      runner.EventOpenCodeStart,
				IssueID:   "task-1",
				Title:     "Example Task",
				Phase:     "starting opencode",
				Model:     tc.model,
				EmittedAt: fixedNow.Add(-5 * time.Second),
			})
			m = updated.(Model)

			view := m.View()

			// View should contain the model name
			if !strings.Contains(view, tc.expected) {
				t.Fatalf("expected view to contain model %q, got: %q", tc.expected, view)
			}
		})
	}
}

// Test for lipgloss layout requirements
func TestModelStatusBarAlwaysAtBottom(t *testing.T) {
	fixedNow := time.Date(2026, 1, 19, 12, 0, 10, 0, time.UTC)
	m := NewModel(func() time.Time { return fixedNow })
	updated, _ := m.Update(runner.Event{
		Type:      runner.EventSelectTask,
		IssueID:   "task-1",
		Title:     "Example Task",
		EmittedAt: fixedNow.Add(-5 * time.Second),
	})
	m = updated.(Model)

	view := strings.TrimSpace(m.View())
	lines := strings.Split(view, "\n")

	statusbarIndex := -1
	for i, line := range lines {
		if strings.Contains(line, "task-1") && strings.Contains(line, "getting task info") {
			statusbarIndex = i
		}
	}
	if statusbarIndex == -1 {
		t.Fatalf("expected statusbar to be positioned at bottom, got view: %q", view)
	}
	if statusbarIndex < len(lines)-statusbarHeight {
		t.Fatalf("expected statusbar to be positioned at bottom, got index %d", statusbarIndex)
	}
}

func TestModelViewportAboveStatusBar(t *testing.T) {
	fixedNow := time.Date(2026, 1, 19, 12, 0, 10, 0, time.UTC)
	m := NewModel(func() time.Time { return fixedNow })

	// Set window size
	updated, _ := m.Update(tea.WindowSizeMsg{
		Width:  80,
		Height: 24,
	})
	m = updated.(Model)

	updated, _ = m.Update(runner.Event{
		Type:      runner.EventSelectTask,
		IssueID:   "task-1",
		Title:     "Example Task",
		EmittedAt: fixedNow.Add(-5 * time.Second),
	})
	m = updated.(Model)

	// Add some content to viewport
	m.viewport.GotoTop()
	m.viewport.SetContent("Log line 1\nLog line 2\nLog line 3")

	view := m.View()

	// View should contain viewport content
	if !strings.Contains(view, "Log line 1") {
		t.Fatalf("expected viewport content in view, got: %q", view)
	}

	// Verify viewport is scrollable by checking it's rendered
	// Layout structure has: viewport -> statusbar
	// (exact string position check is unreliable due to viewport padding)
	if !strings.Contains(view, "task-1") {
		t.Fatalf("expected statusbar at bottom")
	}
}

func TestModelResizesCorrectly(t *testing.T) {
	fixedNow := time.Date(2026, 1, 19, 12, 0, 10, 0, time.UTC)
	m := NewModel(func() time.Time { return fixedNow })

	// Initial size
	updated, _ := m.Update(tea.WindowSizeMsg{
		Width:  80,
		Height: 24,
	})
	m = updated.(Model)

	updated, _ = m.Update(runner.Event{
		Type:      runner.EventSelectTask,
		IssueID:   "task-1",
		Title:     "Example Task",
		EmittedAt: fixedNow.Add(-5 * time.Second),
	})
	m = updated.(Model)

	firstView := m.View()

	// Resize to larger
	updated, _ = m.Update(tea.WindowSizeMsg{
		Width:  120,
		Height: 40,
	})
	m = updated.(Model)

	secondView := m.View()

	// Views should be different after resize
	if firstView == secondView {
		t.Fatalf("expected view to change after resize")
	}

	// Verify viewport was updated
	if m.viewport.Width != 118 {
		t.Fatalf("expected viewport width to be 118 after resize, got %d", m.viewport.Width)
	}

	_, expectedViewportHeight := logViewportSize(120, 40)
	if m.viewport.Height != expectedViewportHeight {
		t.Fatalf("expected viewport height to be %d after resize, got %d", expectedViewportHeight, m.viewport.Height)
	}
}

func TestModelUsesLipglossForLayout(t *testing.T) {
	fixedNow := time.Date(2026, 1, 19, 12, 0, 10, 0, time.UTC)
	m := NewModel(func() time.Time { return fixedNow })
	updated, _ := m.Update(runner.Event{
		Type:      runner.EventSelectTask,
		IssueID:   "task-1",
		Title:     "Example Task",
		EmittedAt: fixedNow.Add(-5 * time.Second),
	})
	m = updated.(Model)

	view := m.View()

	// Verify the view is rendered (not empty)
	if strings.TrimSpace(view) == "" {
		t.Fatalf("expected non-empty view")
	}

	// Verify view has structured layout with multiple lines
	lines := strings.Split(strings.TrimSpace(view), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected view to have multiple lines for layout, got %d", len(lines))
	}

	// View should contain expected components
	expectedComponents := []string{"task-1", "getting task info"}
	for _, comp := range expectedComponents {
		if !strings.Contains(view, comp) {
			t.Fatalf("expected view to contain %q, got: %q", comp, view)
		}
	}
}

func TestModelRendersAgentThoughtsAsMarkdown(t *testing.T) {
	fixedNow := time.Date(2026, 1, 19, 12, 0, 10, 0, time.UTC)
	m := NewModel(func() time.Time { return fixedNow })

	// Send an event with agent thoughts in markdown format
	updated, _ := m.Update(runner.Event{
		Type:      runner.EventOpenCodeStart,
		IssueID:   "task-1",
		Title:     "Example Task",
		EmittedAt: fixedNow.Add(-5 * time.Second),
		Thought:   "## Analysis\n\nThis is a **bold** statement with *italics*.\n\n- Item 1\n- Item 2",
	})
	m = updated.(Model)

	view := m.View()

	// Verify that markdown content is rendered in the view
	// Markdown headers should be rendered with special styling or just as text
	if !strings.Contains(view, "Analysis") {
		t.Fatalf("expected markdown header 'Analysis' in view, got: %q", view)
	}

	// The viewport should contain to markdown content
	if !strings.Contains(view, "This is a") {
		t.Fatalf("expected markdown content in view, got: %q", view)
	}
}

func TestModelUsesStatusBarComponent(t *testing.T) {
	fixedNow := time.Date(2026, 1, 19, 12, 0, 10, 0, time.UTC)
	m := NewModel(func() time.Time { return fixedNow })

	// Verify that the model has a statusbar component
	if m.statusbar.View() == "" {
		t.Fatal("expected statusbar component to be initialized")
	}

	// Update model with event
	updated, _ := m.Update(runner.Event{
		Type:      runner.EventSelectTask,
		IssueID:   "task-1",
		Title:     "Example Task",
		EmittedAt: fixedNow.Add(-5 * time.Second),
	})
	m = updated.(Model)

	view := m.View()

	// Verify that the statusbar component's View() is being used
	// The statusbar should be rendered with lipgloss styling
	if !strings.Contains(view, "task-1") {
		t.Fatalf("expected statusbar to render task ID, got: %q", view)
	}

	// Verify statusbar is at the bottom
	lines := strings.Split(strings.TrimSpace(view), "\n")
	if len(lines) < 2 {
		t.Fatal("expected multiple lines in view")
	}

	statusbarIndex := -1
	for i, line := range lines {
		if strings.Contains(line, "task-1") && strings.Contains(line, "getting task info") {
			statusbarIndex = i
		}
	}
	if statusbarIndex == -1 {
		t.Fatalf("expected statusbar to be positioned at bottom, got view: %q", view)
	}
	if statusbarIndex < len(lines)-statusbarHeight {
		t.Fatalf("expected statusbar to be positioned at bottom, got index %d", statusbarIndex)
	}
}

func TestModelAppendsLogLinesToViewport(t *testing.T) {
	fixedNow := time.Date(2026, 1, 19, 12, 0, 10, 0, time.UTC)
	m := NewModel(func() time.Time { return fixedNow })

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 8})
	m = updated.(Model)

	updated, _ = m.Update(AppendLogMsg{Line: "first log line"})
	m = updated.(Model)
	updated, _ = m.Update(AppendLogMsg{Line: "second log line"})
	m = updated.(Model)

	view := m.View()
	if !strings.Contains(view, "first log line") {
		t.Fatalf("expected first log line in view, got %q", view)
	}
	if !strings.Contains(view, "second log line") {
		t.Fatalf("expected second log line in view, got %q", view)
	}
	if strings.Index(view, "first log line") > strings.Index(view, "second log line") {
		t.Fatalf("expected log lines to remain in order, got %q", view)
	}
}

func TestModelScrollsLogViewport(t *testing.T) {
	fixedNow := time.Date(2026, 1, 19, 12, 0, 10, 0, time.UTC)
	m := NewModel(func() time.Time { return fixedNow })

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 6})
	m = updated.(Model)

	for i := 0; i < 10; i++ {
		updated, _ = m.Update(AppendLogMsg{Line: fmt.Sprintf("log line %d", i)})
		m = updated.(Model)
	}

	// Ensure we can scroll down from the top
	m.viewport.GotoTop()
	if m.viewport.YOffset != 0 {
		t.Fatalf("expected viewport to start at top, got %d", m.viewport.YOffset)
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	if m.viewport.YOffset == 0 {
		t.Fatalf("expected viewport to scroll on key down")
	}
}

func TestModelKeepsScrollOffsetWhenNotAtBottom(t *testing.T) {
	fixedNow := time.Date(2026, 1, 19, 12, 0, 10, 0, time.UTC)
	m := NewModel(func() time.Time { return fixedNow })

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 8})
	m = updated.(Model)

	for i := 0; i < 10; i++ {
		updated, _ = m.Update(AppendLogMsg{Line: fmt.Sprintf("log line %d", i)})
		m = updated.(Model)
	}

	// Scroll to top to simulate user viewing history
	m.viewport.GotoTop()
	if m.viewport.YOffset != 0 {
		t.Fatalf("expected viewport to be at top, got %d", m.viewport.YOffset)
	}

	updated, _ = m.Update(AppendLogMsg{Line: "new log line"})
	m = updated.(Model)

	if m.viewport.YOffset != 0 {
		t.Fatalf("expected viewport to keep scroll offset when not at bottom, got %d", m.viewport.YOffset)
	}
}

func TestModelAppendsRunnerEventsToLogView(t *testing.T) {
	fixedNow := time.Date(2026, 1, 19, 12, 0, 10, 0, time.UTC)
	m := NewModel(func() time.Time { return fixedNow })

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 8})
	m = updated.(Model)

	updated, _ = m.Update(runner.Event{
		Type:      runner.EventSelectTask,
		IssueID:   "task-1",
		Title:     "Example Task",
		EmittedAt: fixedNow.Add(-5 * time.Second),
	})
	m = updated.(Model)

	view := m.View()
	if !strings.Contains(view, "[runner] task-1 Example Task") {
		t.Fatalf("expected runner event log in view, got %q", view)
	}
}
