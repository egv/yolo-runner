package tui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"yolo-runner/internal/runner"
)

type Model struct {
	taskID       string
	taskTitle    string
	phase        string
	lastOutputAt time.Time
	now          func() time.Time
	spinnerIndex int
}

type OutputMsg struct{}

func NewModel(now func() time.Time) Model {
	if now == nil {
		now = time.Now
	}
	return Model{now: now}
}

func (m Model) Init() tea.Cmd {
	return tickCmd()
}

type tickMsg struct{}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg {
		return tickMsg{}
	})
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch typed := msg.(type) {
	case runner.Event:
		m.taskID = typed.IssueID
		m.taskTitle = typed.Title
		m.phase = typed.Phase
		m.lastOutputAt = typed.EmittedAt
	case OutputMsg:
		m.spinnerIndex = (m.spinnerIndex + 1) % len(spinnerFrames)
		m.lastOutputAt = m.now()
	case tickMsg:
		return m, tickCmd()
	}
	return m, nil
}

func (m Model) View() string {
	spinner := spinnerFrames[m.spinnerIndex%len(spinnerFrames)]
	age := m.lastOutputAge()
	return fmt.Sprintf("%s %s - %s\nphase: %s\nlast runner event %s\n", spinner, m.taskID, m.taskTitle, m.phase, age)
}

func (m Model) lastOutputAge() string {
	if m.lastOutputAt.IsZero() {
		return "n/a"
	}
	age := m.now().Sub(m.lastOutputAt).Round(time.Second)
	return fmt.Sprintf("%ds", int(age.Seconds()))
}

var spinnerFrames = []string{"-", "\\", "|", "/"}
