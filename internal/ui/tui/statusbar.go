package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/anomalyco/yolo-runner/internal/runner"
)

// StatusBar is a component for rendering TUI status bar at the bottom
// It uses lipgloss for styling and provides a clean interface for status updates
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

// Update updates the status bar state from a runner.Event
func (s *StatusBar) Update(event runner.Event, lastOutputAge string, spinner string, stopping bool) {
	s.taskID = event.IssueID
	s.phase = getPhaseLabel(event.Type)
	s.model = event.Model
	s.progressCompleted = event.ProgressCompleted
	s.progressTotal = event.ProgressTotal
	s.lastOutputAge = lastOutputAge
	s.spinner = spinner
	s.stopping = stopping
}

// View returns the rendered status bar
func (s StatusBar) View() string {
	// Handle stopping status
	if s.stopping {
		style := lipgloss.NewStyle().
			Width(s.width).
			Foreground(lipgloss.Color("#ffffff")).
			Background(lipgloss.Color("#ff0000")).
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
