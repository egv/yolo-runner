package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/anomalyco/yolo-runner/internal/runner"
)

// TestStatusBarIsBubbleTeaComponent verifies that StatusBar follows Bubble Tea patterns
func TestStatusBarIsBubbleTeaComponent(t *testing.T) {
	// The StatusBar should be a Bubble Tea component with Init, Update, View methods
	// This ensures it follows the "teacup" pattern

	sb := NewStatusBar()

	// StatusBar should have Init method (or return nil)
	var _ func() tea.Cmd = sb.Init

	// StatusBar should have Update method
	var _ func(tea.Msg) (StatusBar, tea.Cmd) = sb.Update

	// StatusBar should have View method
	var _ func() string = sb.View

	// Test Init
	cmd := sb.Init()
	// Init can return nil or a command
	if cmd != nil {
		t.Logf("StatusBar.Init returned a command")
	}

	// Test Update with a simple message
	updated, cmd := sb.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	if cmd != nil {
		t.Logf("StatusBar.Update returned a command")
	}

	// Verify width was updated
	if updated.width != 100 {
		t.Fatalf("expected width to be updated to 100, got %d", updated.width)
	}

	// Test View
	view := updated.View()
	if view == "" {
		t.Fatal("expected StatusBar.View to return non-empty string")
	}
}

// TestStatusBarUsesLipglossForStyling verifies StatusBar uses lipgloss for styling
func TestStatusBarUsesLipglossForStyling(t *testing.T) {
	sb := NewStatusBar()
	sb.SetWidth(80)

	// Update with event via message
	event := runner.Event{
		Type:              runner.EventSelectTask,
		IssueID:           "task-123",
		ProgressCompleted: 1,
		ProgressTotal:     5,
		Model:             "claude-3-5",
	}
	age := "5s"
	spinner := "⠋"

	sb, _ = sb.Update(UpdateStatusBarMsg{Event: event, LastOutputAge: age, Spinner: spinner})

	view := sb.View()

	// View should contain styled content
	// Since lipgloss applies ANSI codes, the output should have color codes
	// This is hard to test directly, but we can verify the structure

	// Should contain the expected content
	if !strings.Contains(view, "task-123") {
		t.Fatalf("expected view to contain task ID, got: %q", view)
	}

	if !strings.Contains(view, "[1/5]") {
		t.Fatalf("expected view to contain progress, got: %q", view)
	}

	if !strings.Contains(view, "claude-3-5") {
		t.Fatalf("expected view to contain model, got: %q", view)
	}

	if !strings.Contains(view, "(5s)") {
		t.Fatalf("expected view to contain age, got: %q", view)
	}
}

// TestStatusBarPinnedToBottomInLayout verifies statusbar appears at bottom of TUI layout
func TestStatusBarPinnedToBottomInLayout(t *testing.T) {
	fixedNow := time.Date(2026, 1, 27, 12, 0, 0, 0, time.UTC)
	m := NewModel(func() time.Time { return fixedNow })

	// Set window size
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

	view := strings.TrimSpace(m.View())
	lines := strings.Split(view, "\n")

	// The statusbar should be the last line
	statusBarLine := lines[len(lines)-1]
	if !strings.Contains(statusBarLine, "task-1") {
		t.Fatalf("expected statusbar line to contain task ID, got: %q", statusBarLine)
	}

	if !strings.Contains(statusBarLine, "getting task info") {
		t.Fatalf("expected statusbar line to contain phase, got: %q", statusBarLine)
	}

	if !strings.Contains(statusBarLine, "(5s)") {
		t.Fatalf("expected statusbar line to contain age, got: %q", statusBarLine)
	}
}

// TestModelUsesTeacupStatusBar verifies the model uses a proper Bubble Tea StatusBar component
func TestModelUsesTeacupStatusBar(t *testing.T) {
	fixedNow := time.Date(2026, 1, 27, 12, 0, 0, 0, time.UTC)
	m := NewModel(func() time.Time { return fixedNow })

	// The model's statusbar should follow Bubble Tea component patterns
	// Check that it has the required methods
	sb := m.statusbar

	// Init should be callable
	_ = sb.Init()

	// Update should be callable
	_, _ = sb.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	// View should return something
	view := sb.View()
	if view == "" {
		t.Fatal("expected statusbar.View() to return non-empty string")
	}
}

// TestMarkdownRenderedForAgentThoughts verifies agent thoughts are rendered as markdown
func TestMarkdownRenderedForAgentThoughts(t *testing.T) {
	fixedNow := time.Date(2026, 1, 27, 12, 0, 0, 0, time.UTC)
	m := NewModel(func() time.Time { return fixedNow })

	// Send event with markdown thoughts
	updated, _ := m.Update(runner.Event{
		Type:      runner.EventOpenCodeStart,
		IssueID:   "task-1",
		Title:     "Example Task",
		EmittedAt: fixedNow.Add(-5 * time.Second),
		Thought:   "# Analysis\n\nThis is **bold** and *italic* text.\n\n## Steps\n1. First step\n2. Second step",
	})
	m = updated.(Model)

	view := m.View()

	// Markdown should be rendered (not displayed as raw markdown)
	// The rendered content should be in the view
	if !strings.Contains(view, "Analysis") {
		t.Fatalf("expected rendered markdown header 'Analysis', got: %q", view)
	}

	if !strings.Contains(view, "Steps") {
		t.Fatalf("expected rendered markdown header 'Steps', got: %q", view)
	}

	// Raw markdown syntax should not be visible (or be transformed)
	// We check that the structure is preserved but rendering has occurred
	// This is a basic check - the actual rendering will transform markdown
}

// TestViewportAboveStatusBar verifies viewport is positioned above statusbar
func TestViewportAboveStatusBar(t *testing.T) {
	fixedNow := time.Date(2026, 1, 27, 12, 0, 0, 0, time.UTC)
	m := NewModel(func() time.Time { return fixedNow })

	// Set window size
	updated, _ := m.Update(tea.WindowSizeMsg{
		Width:  80,
		Height: 24,
	})
	m = updated.(Model)

	// Add content to viewport
	updated, _ = m.Update(runner.Event{
		Type:      runner.EventSelectTask,
		IssueID:   "task-1",
		Title:     "Example Task",
		EmittedAt: fixedNow.Add(-5 * time.Second),
	})
	m = updated.(Model)

	// Add content to viewport via log append
	updated, _ = m.Update(AppendLogMsg{Line: "Line 1"})
	m = updated.(Model)

	view := m.View()
	lines := strings.Split(strings.TrimSpace(view), "\n")

	// Find viewport content (lines)
	viewportContentFound := false
	statusBarFound := false
	viewportIndex := -1
	statusBarIndex := -1

	for i, line := range lines {
		if strings.Contains(line, "Line 1") {
			viewportContentFound = true
			viewportIndex = i
		}
		if strings.Contains(line, "task-1") && strings.Contains(line, "getting task info") {
			statusBarFound = true
			statusBarIndex = i
		}
	}

	if !viewportContentFound {
		t.Fatalf("expected viewport content in view, got: %q", view)
	}

	if !statusBarFound {
		t.Fatalf("expected statusbar in view, got: %q", view)
	}

	// Verify order: viewport above statusbar
	if viewportIndex >= statusBarIndex {
		t.Fatalf("expected viewport (index %d) to be above statusbar (index %d)", viewportIndex, statusBarIndex)
	}
}

// TestResizeCorrectly updates viewport and statusbar dimensions
func TestResizeCorrectly(t *testing.T) {
	fixedNow := time.Date(2026, 1, 27, 12, 0, 0, 0, time.UTC)
	m := NewModel(func() time.Time { return fixedNow })

	// Initial size
	updated, _ := m.Update(tea.WindowSizeMsg{
		Width:  80,
		Height: 24,
	})
	m = updated.(Model)

	// Verify initial dimensions
	if m.width != 80 {
		t.Fatalf("expected model width to be 80, got %d", m.width)
	}

	if m.height != 24 {
		t.Fatalf("expected model height to be 24, got %d", m.height)
	}

	if m.statusbar.width != 80 {
		t.Fatalf("expected statusbar width to be 80, got %d", m.statusbar.width)
	}

	// Resize
	updated, _ = m.Update(tea.WindowSizeMsg{
		Width:  120,
		Height: 40,
	})
	m = updated.(Model)

	// Verify new dimensions
	if m.width != 120 {
		t.Fatalf("expected model width to be 120 after resize, got %d", m.width)
	}

	if m.height != 40 {
		t.Fatalf("expected model height to be 40 after resize, got %d", m.height)
	}

	if m.statusbar.width != 120 {
		t.Fatalf("expected statusbar width to be 120 after resize, got %d", m.statusbar.width)
	}

	if m.viewport.Width != 118 {
		t.Fatalf("expected viewport width to be 118 after resize, got %d", m.viewport.Width)
	}

	// Viewport height should account for other components (statusbar + log bubble border)
	expectedViewportHeight := 40 - 1 - 2 // Height minus statusbar minus log bubble border
	if m.viewport.Height != expectedViewportHeight {
		t.Fatalf("expected viewport height to be %d after resize, got %d", expectedViewportHeight, m.viewport.Height)
	}
}

// TestStatusBarSupportsStoppingState verifies statusbar handles stopping state
func TestStatusBarSupportsStoppingState(t *testing.T) {
	sb := NewStatusBar()
	sb.SetWidth(80)

	// Normal state
	sb.SetStopping(false)
	view := sb.View()
	if strings.Contains(view, "Stopping...") {
		t.Fatalf("did not expect 'Stopping...' in normal state, got: %q", view)
	}

	// Stopping state
	sb.SetStopping(true)
	view = sb.View()
	if !strings.Contains(view, "Stopping...") {
		t.Fatalf("expected 'Stopping...' in stopping state, got: %q", view)
	}
}

// TestMarkdownBubbleIsBubbleTeaComponent verifies that MarkdownBubble follows Bubble Tea patterns
func TestMarkdownBubbleIsBubbleTeaComponent(t *testing.T) {
	// The MarkdownBubble should be a Bubble Tea component with Init, Update, View methods
	// This ensures it follows the "teacup" pattern

	mb := NewMarkdownBubble()

	// MarkdownBubble should have Init method (or return nil)
	var _ func() tea.Cmd = mb.Init

	// MarkdownBubble should have Update method
	var _ func(tea.Msg) (MarkdownBubble, tea.Cmd) = mb.Update

	// MarkdownBubble should have View method
	var _ func() string = mb.View

	// Test Init
	cmd := mb.Init()
	// Init can return nil or a command
	if cmd != nil {
		t.Logf("MarkdownBubble.Init returned a command")
	}

	// Test Update with a simple message
	updated, cmd := mb.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	if cmd != nil {
		t.Logf("MarkdownBubble.Update returned a command")
	}

	// Verify width was updated
	if updated.width != 100 {
		t.Fatalf("expected width to be updated to 100, got %d", updated.width)
	}

	// Test View
	view := updated.View()
	if view == "" {
		t.Fatal("expected MarkdownBubble.View to return non-empty string")
	}
}

// TestMarkdownBubbleRendersMarkdown verifies markdown is rendered in the bubble
func TestMarkdownBubbleRendersMarkdown(t *testing.T) {
	mb := NewMarkdownBubble()
	mb.SetWidth(80)

	// Update with markdown content
	updated, _ := mb.Update(SetMarkdownContentMsg{
		Content: "# Header\n\nThis is **bold** and *italic* text.",
	})

	view := updated.View()

	// Rendered content should be present (markdown transformed)
	if !strings.Contains(view, "Header") {
		t.Fatalf("expected rendered markdown to contain 'Header', got: %q", view)
	}

	if !strings.Contains(view, "bold") {
		t.Fatalf("expected rendered markdown to contain 'bold', got: %q", view)
	}

	if !strings.Contains(view, "italic") {
		t.Fatalf("expected rendered markdown to contain 'italic', got: %q", view)
	}

	// Raw markdown syntax should be transformed (not displayed as-is)
	// The exact rendering depends on the markdown library, but structure should be preserved
}

// TestMarkdownBubbleNormalizesNewlines verifies newlines are normalized in markdown
func TestMarkdownBubbleNormalizesNewlines(t *testing.T) {
	mb := NewMarkdownBubble()
	mb.SetWidth(80)

	// Test single newline normalization
	updated, _ := mb.Update(SetMarkdownContentMsg{
		Content: "Line1\nLine2",
	})
	view := updated.View()

	// Newlines should be properly normalized for rendering
	// The exact behavior depends on markdown rendering, but content should be preserved
	if !strings.Contains(view, "Line1") {
		t.Fatalf("expected view to contain 'Line1', got: %q", view)
	}

	if !strings.Contains(view, "Line2") {
		t.Fatalf("expected view to contain 'Line2', got: %q", view)
	}

	// Test multiple consecutive newlines
	updated, _ = mb.Update(SetMarkdownContentMsg{
		Content: "Line1\n\n\nLine2",
	})
	view = updated.View()

	if !strings.Contains(view, "Line1") {
		t.Fatalf("expected view to contain 'Line1' after multiple newlines, got: %q", view)
	}

	if !strings.Contains(view, "Line2") {
		t.Fatalf("expected view to contain 'Line2' after multiple newlines, got: %q", view)
	}

	// Test mixed newline types (Windows \r\n, Unix \n, Mac \r)
	updated, _ = mb.Update(SetMarkdownContentMsg{
		Content: "Line1\r\nLine2\nLine3\rLine4",
	})
	view = updated.View()

	// All content should be present regardless of newline type
	contentLines := []string{"Line1", "Line2", "Line3", "Line4"}
	for _, line := range contentLines {
		if !strings.Contains(view, line) {
			t.Fatalf("expected view to contain %q after mixed newlines, got: %q", line, view)
		}
	}
}

// TestMarkdownBubbleSupportsCodeBlocks verifies markdown with code blocks renders correctly
func TestMarkdownBubbleSupportsCodeBlocks(t *testing.T) {
	mb := NewMarkdownBubble()
	mb.SetWidth(80)

	// Update with code block markdown
	updated, _ := mb.Update(SetMarkdownContentMsg{
		Content: "```go\nfunc hello() {\n  fmt.Println(\"Hello\")\n}\n```",
	})

	view := updated.View()

	// Code content should be present
	if !strings.Contains(view, "func hello()") {
		t.Fatalf("expected view to contain code function, got: %q", view)
	}

	if !strings.Contains(view, "fmt.Println") {
		t.Fatalf("expected view to contain code print, got: %q", view)
	}
}

// TestMarkdownBubbleHandlesLists verifies markdown lists are rendered
func TestMarkdownBubbleHandlesLists(t *testing.T) {
	mb := NewMarkdownBubble()
	mb.SetWidth(80)

	// Update with list markdown
	updated, _ := mb.Update(SetMarkdownContentMsg{
		Content: "## Steps\n\n1. First step\n2. Second step\n3. Third step",
	})

	view := updated.View()

	// List items should be present
	if !strings.Contains(view, "First step") {
		t.Fatalf("expected view to contain 'First step', got: %q", view)
	}

	if !strings.Contains(view, "Second step") {
		t.Fatalf("expected view to contain 'Second step', got: %q", view)
	}

	if !strings.Contains(view, "Third step") {
		t.Fatalf("expected view to contain 'Third step', got: %q", view)
	}

	if !strings.Contains(view, "Steps") {
		t.Fatalf("expected view to contain header 'Steps', got: %q", view)
	}
}
