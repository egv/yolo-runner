package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/anomalyco/yolo-runner/internal/runner"
)

type Model struct {
	taskID            string
	taskTitle         string
	phase             string
	model             string
	progressCompleted int
	progressTotal     int
	lastOutputAt      time.Time
	now               func() time.Time
	spinner           Spinner
	stopRequested     bool
	stopping          bool
	stopCh            chan struct{}
	stopNotified      bool
	viewport          viewport.Model
	logs              []string
	thought           string         // Agent thoughts in markdown format
	statusbar         StatusBar      // Teacup-style status bar component
	markdownBubble    MarkdownBubble // Teacup-style markdown bubble component for rendering agent messages
	width             int
	height            int
}

const (
	defaultWidth          = 80
	defaultHeight         = 24
	statusbarHeight       = 3
	logBubbleBorderWidth  = 2
	logBubbleBorderHeight = 2
)

type OutputMsg struct{}

type AppendLogMsg struct {
	Line string
}

type stopTickMsg struct{}

func NewModel(now func() time.Time) Model {
	return NewModelWithStop(now, nil)
}

func NewModelWithStop(now func() time.Time, stopCh chan struct{}) Model {
	if now == nil {
		now = time.Now
	}
	logViewportWidth, logViewportHeight := logViewportSize(defaultWidth, defaultHeight)
	vp := viewport.New(logViewportWidth, logViewportHeight)
	vp.SetContent("")
	return Model{
		viewport:       vp,
		logs:           []string{},
		width:          defaultWidth,
		height:         defaultHeight,
		now:            now,
		stopCh:         stopCh,
		spinner:        NewSpinner(),
		statusbar:      NewStatusBar(),
		markdownBubble: NewMarkdownBubble(),
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Init(), m.markdownBubble.Init(), tickCmd())
}

type tickMsg struct{}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg {
		return tickMsg{}
	})
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.spinner, cmd = m.spinner.Update(msg)
	var statusbarCmd tea.Cmd
	m.statusbar, statusbarCmd = m.statusbar.Update(msg)
	var markdownBubbleCmd tea.Cmd
	m.markdownBubble, markdownBubbleCmd = m.markdownBubble.Update(msg)
	var viewportCmd tea.Cmd

	switch typed := msg.(type) {
	case runner.Event:
		m.appendLogLines(formatRunnerEventLine(typed))
		m.taskID = typed.IssueID
		m.taskTitle = typed.Title
		m.phase = getPhaseLabel(typed.Type)
		m.model = typed.Model
		m.progressCompleted = typed.ProgressCompleted
		m.progressTotal = typed.ProgressTotal
		m.thought = typed.Thought
		m.lastOutputAt = typed.EmittedAt
		if typed.Type == runner.EventOpenCodeEnd {
			m.lastOutputAt = m.now()
		}
		// Append markdown bubble for thoughts into the log view
		if typed.Thought != "" {
			m.markdownBubble, _ = m.markdownBubble.Update(SetMarkdownContentMsg{Content: typed.Thought})
			m.appendLogLines(m.markdownBubble.View())
		}
		// Update statusbar synchronously with current state
		age := m.lastOutputAge()
		spinner := m.spinner.View()
		updateMsg := UpdateStatusBarMsg{Event: typed, LastOutputAge: age, Spinner: spinner}
		m.statusbar, _ = m.statusbar.Update(updateMsg)
		return m, tea.Batch(cmd, statusbarCmd)
	case OutputMsg:
		m.lastOutputAt = m.now()
	case AppendLogMsg:
		m.lastOutputAt = m.now()
		m.appendLogLines(typed.Line)
	case tea.WindowSizeMsg:
		m.width = typed.Width
		m.height = typed.Height
		logViewportWidth, logViewportHeight := logViewportSize(typed.Width, typed.Height)
		m.viewport.Width = logViewportWidth
		m.viewport.Height = logViewportHeight
	case tickMsg:
		// On tick, update statusbar with new age if we have task info
		if m.taskID != "" {
			age := m.lastOutputAge()
			spinner := m.spinner.View()
			// Reconstruct event for statusbar update
			event := runner.Event{
				Type:              getEventTypeForPhase(m.phase),
				IssueID:           m.taskID,
				Title:             m.taskTitle,
				Model:             m.model,
				ProgressCompleted: m.progressCompleted,
				ProgressTotal:     m.progressTotal,
			}
			updateMsg := UpdateStatusBarMsg{Event: event, LastOutputAge: age, Spinner: spinner}
			m.statusbar, _ = m.statusbar.Update(updateMsg)
		}
		return m, tea.Batch(cmd, statusbarCmd, markdownBubbleCmd, tickCmd())
	case tea.KeyMsg:
		if typed.Type == tea.KeyCtrlC || (typed.Type == tea.KeyRunes && len(typed.Runes) == 1 && typed.Runes[0] == 'q') {
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
			// Update statusbar with stopping state
			m.statusbar, _ = m.statusbar.Update(StopStatusBarMsg{})
			return m, tea.Batch(cmd, statusbarCmd, func() tea.Msg { return stopTickMsg{} })
		}
		m.viewport, viewportCmd = m.viewport.Update(msg)
	case tea.MouseMsg:
		m.viewport, viewportCmd = m.viewport.Update(msg)
	case stopTickMsg:
		m.stopRequested = true
		m.stopping = true
		// Update statusbar with stopping state
		m.statusbar, _ = m.statusbar.Update(StopStatusBarMsg{})
	}
	return m, tea.Batch(cmd, statusbarCmd, viewportCmd)
}

func getPhaseLabel(eventType runner.EventType) string {
	switch eventType {
	case runner.EventSelectTask:
		return "getting task info"
	case runner.EventBeadsUpdate:
		return "updating task status"
	case runner.EventOpenCodeStart:
		return "starting opencode"
	case runner.EventOpenCodeEnd:
		return "opencode finished"
	case runner.EventGitAdd:
		return "adding changes"
	case runner.EventGitStatus:
		return "checking status"
	case runner.EventGitCommit:
		return "committing changes"
	case runner.EventBeadsClose:
		return "closing task"
	case runner.EventBeadsVerify:
		return "verifying closure"
	case runner.EventBeadsSync:
		return "syncing beads"
	default:
		return string(eventType)
	}
}

func getEventTypeForPhase(phase string) runner.EventType {
	switch phase {
	case "getting task info":
		return runner.EventSelectTask
	case "updating task status":
		return runner.EventBeadsUpdate
	case "starting opencode":
		return runner.EventOpenCodeStart
	case "opencode finished":
		return runner.EventOpenCodeEnd
	case "adding changes":
		return runner.EventGitAdd
	case "checking status":
		return runner.EventGitStatus
	case "committing changes":
		return runner.EventGitCommit
	case "closing task":
		return runner.EventBeadsClose
	case "verifying closure":
		return runner.EventBeadsVerify
	case "syncing beads":
		return runner.EventBeadsSync
	default:
		return runner.EventType(phase)
	}
}

func (m Model) View() string {
	logBubbleHeight := m.height - statusbarHeight
	if logBubbleHeight < 0 {
		logBubbleHeight = 0
	}

	statusbarView := m.statusbar.View()
	if logBubbleHeight == 0 {
		return statusbarView + "\n"
	}

	logBubbleView := ""
	if logBubbleHeight <= logBubbleBorderHeight {
		logBubbleView = lipgloss.NewStyle().
			Width(m.width).
			Height(logBubbleHeight).
			Render(m.viewport.View())
	} else {
		logBubbleStyle := lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			Width(m.viewport.Width).
			Height(m.viewport.Height)
		logBubbleView = logBubbleStyle.Render(m.viewport.View())
	}

	content := lipgloss.JoinVertical(lipgloss.Top, logBubbleView, statusbarView)

	return content + "\n"
}

func logViewportSize(width int, height int) (int, int) {
	logBubbleHeight := height - statusbarHeight
	if logBubbleHeight < 0 {
		logBubbleHeight = 0
	}

	logViewportWidth := width
	logViewportHeight := logBubbleHeight

	if logBubbleHeight > logBubbleBorderHeight {
		logViewportWidth = width - logBubbleBorderWidth
		logViewportHeight = logBubbleHeight - logBubbleBorderHeight
	}

	if logViewportWidth < 0 {
		logViewportWidth = 0
	}
	if logViewportHeight < 0 {
		logViewportHeight = 0
	}

	return logViewportWidth, logViewportHeight
}

func (m *Model) appendLogLines(text string) {
	if text == "" {
		return
	}
	wasAtBottom := m.viewport.AtBottom()
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		m.logs = append(m.logs, line)
	}
	m.viewport.SetContent(strings.Join(m.logs, "\n"))
	if wasAtBottom {
		m.viewport.GotoBottom()
	}
}

func formatRunnerEventLine(event runner.Event) string {
	parts := []string{"[runner]"}
	if event.IssueID != "" {
		parts = append(parts, event.IssueID)
	}
	if event.Title != "" {
		parts = append(parts, event.Title)
	}
	if len(parts) == 1 {
		parts = append(parts, "event update")
	}
	return strings.Join(parts, " ")
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
