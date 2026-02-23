package tui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/egv/yolo-runner/internal/runner"
)

// TestModelUsesMarkdownBubbleForThoughts verifies that the model uses MarkdownBubble
// component (teacup pattern) to render agent thoughts, not inline rendering
func TestModelUsesMarkdownBubbleForThoughts(t *testing.T) {
	fixedNow := time.Date(2026, 1, 28, 12, 0, 0, 0, time.UTC)
	m := NewModel(func() time.Time { return fixedNow })

	// Verify that the model has a markdownBubble component
	// This is a structural check to ensure the component exists
	if m.markdownBubble.View() == "" {
		t.Fatal("expected markdownBubble component to be initialized and return non-empty view even without content")
	}

	// Send event with agent thoughts
	updated, _ := m.Update(runner.Event{
		Type:      runner.EventOpenCodeStart,
		IssueID:   "task-1",
		Title:     "Example Task",
		EmittedAt: fixedNow.Add(-5 * time.Second),
		Thought:   "## Analysis\n\nThis is **bold** text.",
	})
	m = updated.(Model)

	// Verify that markdownBubble was updated with the thought content
	view := m.markdownBubble.View()
	if view == "" {
		t.Fatal("expected markdownBubble.View to return content after update")
	}

	// The rendered markdown should contain the expected content
	if !containsString(view, "Analysis") {
		t.Fatalf("expected markdownBubble to render 'Analysis', got: %q", view)
	}

	if !containsString(view, "bold") {
		t.Fatalf("expected markdownBubble to render 'bold', got: %q", view)
	}
}

// TestModelUpdatesMarkdownBubbleWidthOnResize verifies that markdownBubble width
// is updated when window is resized (follows teacup pattern)
func TestModelUpdatesMarkdownBubbleWidthOnResize(t *testing.T) {
	fixedNow := time.Date(2026, 1, 28, 12, 0, 0, 0, time.UTC)
	m := NewModel(func() time.Time { return fixedNow })

	// Initial resize
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = updated.(Model)

	// Set markdown content
	updated, _ = m.Update(runner.Event{
		Type:      runner.EventOpenCodeStart,
		IssueID:   "task-1",
		Title:     "Example Task",
		EmittedAt: fixedNow.Add(-5 * time.Second),
		Thought:   "# Test",
	})
	m = updated.(Model)

	// The markdownBubble should have received the width update
	// We verify this by checking that rendering works with the new width
	view := m.markdownBubble.View()
	if view == "" {
		t.Fatal("expected markdownBubble to render after width update")
	}

	// Resize to different width
	updated, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)

	// MarkdownBubble should still render correctly with new width
	view = m.markdownBubble.View()
	if view == "" {
		t.Fatal("expected markdownBubble to render after second resize")
	}
}

// TestMarkdownBubbleStripsControlSequences verifies that control sequences
// are stripped from agent thoughts before rendering
func TestMarkdownBubbleStripsControlSequences(t *testing.T) {
	mb := NewMarkdownBubble()
	mb.SetWidth(80)

	// Test with various control sequences
	testCases := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "ANSI color codes",
			input:    "\x1b[31mRed text\x1b[0m",
			expected: "Red text", // ANSI codes should be stripped
		},
		{
			name:     "Windows newlines",
			input:    "Line1\r\nLine2",
			expected: "Line1 Line2", // \r\n should be normalized to \n, then markdown treats it as space
		},
		{
			name:     "Carriage return",
			input:    "Line1\rLine2",
			expected: "Line1 Line2", // \r should be normalized to \n, then markdown treats it as space
		},
		{
			name:     "Mixed control sequences",
			input:    "\x1b[31mText\x1b[0m\r\nMore\rText",
			expected: "Text More Text", // All sequences normalized
		},
		{
			name:     "Null characters",
			input:    "Text\x00with\x00nulls",
			expected: "Textwithnulls", // Null characters should be stripped
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			updated, _ := mb.Update(SetMarkdownContentMsg{Content: tc.input})
			view := updated.View()

			// The normalized/rendered content should not contain control sequences
			// We check by verifying expected content is present
			if !containsString(view, tc.expected) {
				t.Fatalf("expected view to contain %q after stripping control sequences, got: %q", tc.expected, view)
			}

			// Verify raw control sequences are not present (they should be stripped/normalized)
			// Note: This is a basic check - some sequences may be transformed rather than completely removed
			if containsString(view, "\x1b[") {
				t.Fatalf("expected ANSI escape sequences to be stripped, got: %q", view)
			}
		})
	}
}

// containsString is a helper to check if a string contains a substring
func containsString(s, substr string) bool {
	return len(s) >= len(substr) && findSubstringInString(s, substr) >= 0
}

// findSubstringInString is a simple substring search
func findSubstringInString(s, substr string) int {
	if len(substr) == 0 {
		return 0
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
