package tui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type Model struct {
	taskID       string
	taskTitle    string
	phase        string
	lastOutputAt time.Time
	now          func() time.Time
	spinnerIndex int
}

type StatusMsg struct {
	TaskID       string
	TaskTitle    string
	Phase        string
	LastOutputAt time.Time
}

type OutputMsg struct{}

func NewModel(now func() time.Time) Model {
	if now == nil {
		now = time.Now
	}
	return Model{now: now}
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch typed := msg.(type) {
	case StatusMsg:
		m.taskID = typed.TaskID
		m.taskTitle = typed.TaskTitle
		m.phase = typed.Phase
		m.lastOutputAt = typed.LastOutputAt
	case OutputMsg:
		m.spinnerIndex = (m.spinnerIndex + 1) % len(spinnerFrames)
		if m.lastOutputAt.IsZero() {
			m.lastOutputAt = m.now()
		}
	}
	return m, nil
}

func (m Model) View() string {
	spinner := spinnerFrames[m.spinnerIndex%len(spinnerFrames)]
	age := m.lastOutputAge()
	return fmt.Sprintf("%s %s - %s\nphase: %s\nlast output %s\n", spinner, m.taskID, m.taskTitle, m.phase, age)
}

func (m Model) lastOutputAge() string {
	if m.lastOutputAt.IsZero() {
		return "n/a"
	}
	age := m.now().Sub(m.lastOutputAt).Round(time.Second)
	return fmt.Sprintf("%ds", int(age.Seconds()))
}

var spinnerFrames = []string{"-", "\\", "|", "/"}
