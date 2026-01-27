package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

// MarkdownBubble is a Bubble Tea component for rendering markdown content
// It uses glamour for markdown rendering and follows the teacup pattern
type MarkdownBubble struct {
	content string
	width   int
}

// NewMarkdownBubble creates a new markdown bubble component
func NewMarkdownBubble() MarkdownBubble {
	return MarkdownBubble{
		width: 80,
	}
}

// Init initializes markdown bubble component
func (m MarkdownBubble) Init() tea.Cmd {
	return nil
}

// SetMarkdownContentMsg is a message to set markdown content in bubble
type SetMarkdownContentMsg struct {
	Content string
}

// Update handles messages and updates the markdown bubble state
func (m MarkdownBubble) Update(msg tea.Msg) (MarkdownBubble, tea.Cmd) {
	switch typed := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = typed.Width
	case SetMarkdownContentMsg:
		m.content = typed.Content
	}

	return m, nil
}

// View returns the rendered markdown
func (m MarkdownBubble) View() string {
	if m.content == "" {
		// Return styled empty line to maintain component visibility (follows teacup pattern)
		style := lipgloss.NewStyle().Width(m.width)
		return style.Render("")
	}

	// Normalize newlines before rendering
	normalizedContent := normalizeMarkdownNewlines(m.content)

	// Create a glamour renderer for terminal markdown rendering
	renderer, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(m.width),
	)
	if err != nil {
		// Fallback to plain text if glamour fails
		return normalizedContent
	}

	rendered, err := renderer.Render(normalizedContent)
	if err != nil {
		return normalizedContent
	}

	return rendered
}

// SetWidth sets the width for markdown rendering
func (m *MarkdownBubble) SetWidth(width int) {
	m.width = width
}

// normalizeMarkdownNewlines normalizes newlines in markdown content
// It converts all newline variants (\r\n, \n, \r) to standard Unix newlines (\n)
func normalizeMarkdownNewlines(text string) string {
	// Replace Windows line endings (\r\n) with Unix newlines (\n)
	text = strings.ReplaceAll(text, "\r\n", "\n")
	// Replace Mac line endings (\r) with Unix newlines (\n)
	text = strings.ReplaceAll(text, "\r", "\n")
	return text
}
