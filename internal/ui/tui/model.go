package tui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/anomalyco/yolo-runner/internal/runner"
)

type Model struct {
	taskID            string
	taskTitle         string
	phase             string
	progressCompleted int
	progressTotal     int
	lastOutputAt      time.Time
	now               func() time.Time
	spinnerIndex      int
	stopRequested     bool
	stopping          bool
	stopCh            chan struct{}
	stopNotified      bool
}

type OutputMsg struct{}

type stopTickMsg struct{}

func NewModel(now func() time.Time) Model {
	return NewModelWithStop(now, nil)
}

func NewModelWithStop(now func() time.Time, stopCh chan struct{}) Model {
	if now == nil {
		now = time.Now
	}
	return Model{now: now, stopCh: stopCh}
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
		m.progressCompleted = typed.ProgressCompleted
		m.progressTotal = typed.ProgressTotal
		m.lastOutputAt = typed.EmittedAt
		if typed.Type == runner.EventOpenCodeEnd {
			m.lastOutputAt = m.now()
		}
	case OutputMsg:
		m.spinnerIndex = (m.spinnerIndex + 1) % len(spinnerFrames)
		m.lastOutputAt = m.now()
	case tickMsg:
		return m, tickCmd()
	case tea.KeyMsg:
		if typed.Type == tea.KeyRunes && len(typed.Runes) == 1 && typed.Runes[0] == 'q' {
			m.stopRequested = true
			m.stopping = true
			if m.stopCh != nil && !m.stopNotified {
				m.stopNotified = true
				select {
				case <-m.stopCh:
					// already closed
				default:
					close(m.stopCh)
				}
			}
			return m, func() tea.Msg { return stopTickMsg{} }
		}
	case stopTickMsg:
		m.stopRequested = true
		m.stopping = true
	}
	return m, nil
}

func (m Model) View() string {
	spinner := spinnerFrames[m.spinnerIndex%len(spinnerFrames)]
	age := m.lastOutputAge()
	status := ""
	if m.stopping {
		status = "Stopping...\n"
	}
	progress := ""
	if m.progressTotal > 0 {
		progress = fmt.Sprintf("[%d/%d] ", m.progressCompleted, m.progressTotal)
	}
	return fmt.Sprintf("%s %s%s - %s\nphase: %s\nlast output %s\n%sq: stop runner\n", spinner, progress, m.taskID, m.taskTitle, m.phase, age, status)
}

func (m Model) lastOutputAge() string {
	if m.lastOutputAt.IsZero() {
		return "n/a"
	}
	age := m.now().Sub(m.lastOutputAt).Round(time.Second)
	return fmt.Sprintf("%ds", int(age.Seconds()))
}

func (m Model) StopRequested() bool {
	return m.stopRequested
}

func (m Model) StopChannel() chan struct{} {
	return m.stopCh
}

var spinnerFrames = []string{"-", "\\", "|", "/"}
