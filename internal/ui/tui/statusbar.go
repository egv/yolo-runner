package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/anomalyco/yolo-runner/internal/runner"
)

// StatusBar is a Bubble Tea component for rendering TUI status bar at the bottom
// It uses lipgloss for styling and follows the teacup (Bubble Tea component) pattern
type StatusBar struct {
	taskID            string
	phase             string
	model             string
	progressCompleted int
	progressTotal     int
	lastOutputAge     string
	stopping          bool
	spinner           string
	width             int
}

// NewStatusBar creates a new status bar component
func NewStatusBar() StatusBar {
	return StatusBar{
		width: 80,
	}
}

// Init initializes the status bar component
func (s StatusBar) Init() tea.Cmd {
	return nil
}

// UpdateStatusBarMsg is a message to update status bar state
type UpdateStatusBarMsg struct {
	Event         runner.Event
	LastOutputAge string
	Spinner       string
}

// StopStatusBarMsg is a message to set the stopping state
type StopStatusBarMsg struct{}

// Update handles messages and updates the status bar state
func (s StatusBar) Update(msg tea.Msg) (StatusBar, tea.Cmd) {
	switch typed := msg.(type) {
	case tea.WindowSizeMsg:
		s.width = typed.Width
	case UpdateStatusBarMsg:
		s.taskID = typed.Event.IssueID
		s.phase = getPhaseLabel(typed.Event.Type)
		s.model = typed.Event.Model
		s.progressCompleted = typed.Event.ProgressCompleted
		s.progressTotal = typed.Event.ProgressTotal
		s.lastOutputAge = typed.LastOutputAge
		s.spinner = typed.Spinner
	case StopStatusBarMsg:
		s.stopping = true
	}

	return s, nil
}

// View returns the rendered status bar
func (s StatusBar) View() string {
	// Handle stopping status
	if s.stopping {
		style := lipgloss.NewStyle().
			Width(s.width).
			Foreground(lipgloss.Color("#ffffff")).
			Background(lipgloss.Color("#ff0000")).
			Border(lipgloss.NormalBorder()).
			Padding(0, 1)
		return style.Render("Stopping...")
	}

	var parts []string

	// Add spinner
	if s.spinner != "" {
		parts = append(parts, s.spinner)
	}

	// Add progress
	if s.progressTotal > 0 {
		parts = append(parts, fmt.Sprintf("[%d/%d]", s.progressCompleted, s.progressTotal))
	}

	// Add phase
	if s.phase != "" {
		parts = append(parts, s.phase)
	}

	// Add task ID
	if s.taskID != "" {
		parts = append(parts, s.taskID)
	}

	// Add model
	if s.model != "" {
		parts = append(parts, fmt.Sprintf("[%s]", s.model))
	}

	// Add last output age
	if s.lastOutputAge != "" {
		parts = append(parts, fmt.Sprintf("(%s)", s.lastOutputAge))
	}

	statusLine := strings.Join(parts, " ")

	// Style status bar with lipgloss
	style := lipgloss.NewStyle().
		Width(s.width).
		Foreground(lipgloss.Color("#ffffff")).
		Background(lipgloss.Color("#1a1a1a")).
		Border(lipgloss.NormalBorder()).
		Padding(0, 1)

	return style.Render(statusLine)
}

// SetWidth sets the width for status bar rendering
func (s *StatusBar) SetWidth(width int) {
	s.width = width
}

// SetStopping sets the stopping state
func (s *StatusBar) SetStopping(stopping bool) {
	s.stopping = stopping
}
