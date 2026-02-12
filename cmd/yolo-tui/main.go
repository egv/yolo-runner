package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/anomalyco/yolo-runner/internal/contracts"
	"github.com/anomalyco/yolo-runner/internal/ui/monitor"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

func main() {
	os.Exit(RunMain(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func RunMain(args []string, in io.Reader, out io.Writer, errOut io.Writer) int {
	fs := flag.NewFlagSet("yolo-tui", flag.ContinueOnError)
	fs.SetOutput(errOut)
	eventsStdin := fs.Bool("events-stdin", true, "Read NDJSON events from stdin")
	demoState := fs.Bool("demo-state", false, "Render seeded demo state and stay open")
	if err := fs.Parse(args); err != nil {
		return 1
	}

	if *demoState {
		if shouldUseFullscreen(out) {
			if err := runFullscreenDemo(out, errOut); err != nil {
				fmt.Fprintln(errOut, err)
				return 1
			}
			return 0
		}
		if _, err := io.WriteString(out, renderDemoSnapshot(time.Now().UTC())); err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
		return 0
	}

	if !*eventsStdin {
		fmt.Fprintln(errOut, "--events-stdin must be enabled")
		return 1
	}
	if in == nil {
		fmt.Fprintln(errOut, "stdin reader is required")
		return 1
	}

	if shouldUseFullscreen(out) {
		if err := runFullscreenFromReader(in, out, errOut); err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
		return 0
	}

	if err := renderFromReader(in, out, errOut); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	return 0
}

func shouldUseFullscreen(out io.Writer) bool {
	file, ok := out.(*os.File)
	if !ok || file == nil {
		return false
	}
	return term.IsTerminal(int(file.Fd()))
}

type streamMsg interface{}

type eventMsg struct{ event contracts.Event }
type decodeErrorMsg struct{ err error }
type streamDoneMsg struct{}

type fullscreenModel struct {
	monitor           *monitor.Model
	viewport          viewport.Model
	width             int
	height            int
	stream            <-chan streamMsg
	errorLine         string
	streamDone        bool
	holdOpen          bool
	detailsCollapsed  bool
	historyCollapsed  bool
	activityCollapsed bool
	statusLine        string
	keyHint           string
}

type displayLine struct {
	text     string
	tone     string
	selected bool
}

func newFullscreenModel(stream <-chan streamMsg, seed []contracts.Event, holdOpen bool) fullscreenModel {
	m := monitor.NewModel(nil)
	for _, event := range seed {
		m.Apply(event)
	}
	vp := viewport.New(80, 24)
	model := fullscreenModel{
		monitor:           m,
		viewport:          vp,
		width:             80,
		height:            24,
		stream:            stream,
		holdOpen:          holdOpen,
		detailsCollapsed:  true,
		historyCollapsed:  true,
		activityCollapsed: false,
		keyHint:           "🧭 jk/↑↓ move  h/l collapse  enter/space toggle  d details  a activity  H history  q quit",
	}
	model.resizeViewport()
	model.viewport.SetContent(model.renderBody())
	return model
}

func (m fullscreenModel) Init() tea.Cmd {
	return waitForStreamMessage(m.stream)
}

func waitForStreamMessage(stream <-chan streamMsg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-stream
		if !ok {
			return streamDoneMsg{}
		}
		return msg
	}
}

func (m fullscreenModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch typed := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = typed.Width
		m.height = typed.Height
		m.resizeViewport()
		m.viewport.SetContent(m.renderBody())
		return m, nil
	case eventMsg:
		m.monitor.Apply(typed.event)
		m.viewport.SetContent(m.renderBody())
		return m, waitForStreamMessage(m.stream)
	case decodeErrorMsg:
		m.errorLine = strings.TrimSpace(typed.err.Error())
		m.monitor.Apply(contracts.Event{Type: contracts.EventTypeRunnerWarning, Message: "decode_error: " + m.errorLine})
		m.viewport.SetContent(m.renderBody())
		return m, waitForStreamMessage(m.stream)
	case streamDoneMsg:
		m.streamDone = true
		m.viewport.SetContent(m.renderBody())
		return m, nil
	case tea.KeyMsg:
		switch typed.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "pgup":
			m.viewport.HalfViewUp()
			return m, nil
		case "pgdown":
			m.viewport.HalfViewDown()
			return m, nil
		case "d":
			m.detailsCollapsed = !m.detailsCollapsed
			m.viewport.SetContent(m.renderBody())
			return m, nil
		case "a":
			m.activityCollapsed = !m.activityCollapsed
			m.viewport.SetContent(m.renderBody())
			return m, nil
		case "H":
			m.historyCollapsed = !m.historyCollapsed
			m.viewport.SetContent(m.renderBody())
			return m, nil
		case "up", "down", "left", "right", "j", "k", "h", "l", "enter", " ":
			key := typed.String()
			if key == " " {
				key = "space"
			}
			m.monitor.HandleKey(key)
			m.viewport.SetContent(m.renderBody())
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

func (m *fullscreenModel) resizeViewport() {
	w := m.width
	if w <= 0 {
		w = 80
	}
	m.viewport.Width = w
	footer := 2
	if m.errorLine != "" {
		footer++
	}
	if m.streamDone {
		footer++
	}
	vh := m.height - footer
	if vh < 1 {
		vh = 1
	}
	m.viewport.Height = vh
}

func (m *fullscreenModel) renderBody() string {
	state := m.monitor.UIState()
	m.statusLine = state.StatusSummary

	width := m.viewport.Width
	if width <= 0 {
		width = 80
	}

	top := renderTop(width, state)
	panes := []string{renderPane(width, "🌲 Panels", stylePanelLines(state.PanelLines, width-4), lipgloss.Color("17"))}

	if m.detailsCollapsed {
		panes = append(panes, renderCollapsedPane(width, "📦 Details", "press d to expand", lipgloss.Color("18")))
	} else {
		details := []string{}
		details = append(details, "phase="+state.Phase, "last_output="+state.LastOutputAge)
		details = append(details, state.Performance...)
		details = append(details, state.RunParams...)
		panes = append(panes, renderPane(width, "📦 Details", stylePlainLines(details, width-4), lipgloss.Color("18")))
	}

	workerPane := renderPane(width, "👷 Workers", styleWorkerLines(state.WorkerSummaries, width-4), lipgloss.Color("19"))
	panes = append(panes, workerPane)

	if m.activityCollapsed {
		panes = append(panes, renderCollapsedPane(width, "🧪 Activity", "press a to expand", lipgloss.Color("20")))
	} else {
		focused := focusedWorkerSummary(state)
		activity := styleActivityLines(focused, width-4)
		panes = append(panes, renderPane(width, "🧪 Activity", activity, lipgloss.Color("20")))
	}

	showHistory := !m.historyCollapsed && m.height >= 24
	if showHistory {
		panes = append(panes, renderPane(width, "🕘 History", stylePlainLines(tailLines(state.History, 16), width-4), lipgloss.Color("235")))
	} else {
		panes = append(panes, renderCollapsedPane(width, "🕘 History", "press H to expand", lipgloss.Color("235")))
	}

	return lipgloss.JoinVertical(lipgloss.Left, top, renderPaneStack(width, panes))
}

func renderTop(width int, state monitor.UIState) string {
	header := fmt.Sprintf("🚀 %s   🎯 %s   ⏳ %s", state.CurrentTask, state.Phase, state.LastOutputAge)
	style := lipgloss.NewStyle().Width(width).Padding(0, 1).Background(lipgloss.Color("24")).Foreground(lipgloss.Color("230")).Bold(true)
	return style.Render(truncateLine(header, width-2))
}

func stylePanelLines(lines []monitor.UIPanelLine, width int) []displayLine {
	if len(lines) == 0 {
		return []displayLine{{text: "n/a", tone: "muted"}}
	}
	out := make([]displayLine, 0, len(lines))
	for _, line := range lines {
		glyph := "[ ]"
		if !line.Leaf {
			if line.Expanded {
				glyph = "[-]"
			} else {
				glyph = "[+]"
			}
		}
		text := strings.Repeat("  ", maxInt(0, line.Depth)) + glyph + " " + line.Label
		tone := "normal"
		if line.Severity == "warning" {
			tone = "warning"
		}
		if line.Severity == "error" {
			tone = "error"
		}
		out = append(out, displayLine{text: truncateLine(text, width), tone: tone, selected: line.Selected})
	}
	return out
}

func styleWorkerLines(workers []monitor.UIWorkerSummary, width int) []displayLine {
	if len(workers) == 0 {
		return []displayLine{{text: "n/a", tone: "muted"}}
	}
	out := []displayLine{}
	for _, worker := range workers {
		badge := "•"
		tone := "normal"
		switch worker.Severity {
		case "warning":
			badge = "⚠"
			tone = "warning"
		case "error":
			badge = "✖"
			tone = "error"
		case "info":
			badge = "✓"
		}
		line1 := fmt.Sprintf("%s %s  %s  ⏱ %s", badge, worker.WorkerID, worker.Phase, worker.LastActivityAge)
		line2 := "   task: " + worker.Task
		line3 := "   last: " + worker.LastEvent
		if worker.QueuePos > 0 {
			line1 += fmt.Sprintf("  📍%d", worker.QueuePos)
		}
		out = append(out,
			displayLine{text: truncateLine(line1, width), tone: tone},
			displayLine{text: truncateLine(line2, width), tone: "muted"},
			displayLine{text: truncateLine(line3, width), tone: "muted"},
		)
	}
	return out
}

func styleActivityLines(worker monitor.UIWorkerSummary, width int) []displayLine {
	if worker.WorkerID == "" {
		return []displayLine{{text: "n/a", tone: "muted"}}
	}
	lines := []displayLine{{text: truncateLine("focus: "+worker.WorkerID+"  task="+worker.Task, width), tone: "normal"}}
	for _, item := range worker.RecentTaskEvents {
		lines = append(lines, displayLine{text: truncateLine(item, width), tone: toneForEvent(item)})
	}
	return lines
}

func toneForEvent(item string) string {
	lower := strings.ToLower(item)
	if strings.Contains(lower, "error") || strings.Contains(lower, "failed") {
		return "error"
	}
	if strings.Contains(lower, "warning") || strings.Contains(lower, "blocked") {
		return "warning"
	}
	return "muted"
}

func focusedWorkerSummary(state monitor.UIState) monitor.UIWorkerSummary {
	focusedID := ""
	for _, line := range state.PanelLines {
		if !line.Selected {
			continue
		}
		if strings.HasPrefix(line.ID, "worker:") {
			id := strings.TrimPrefix(line.ID, "worker:")
			if idx := strings.Index(id, ":task:"); idx >= 0 {
				id = id[:idx]
			}
			focusedID = id
			break
		}
	}
	if focusedID == "" && len(state.WorkerSummaries) > 0 {
		return state.WorkerSummaries[0]
	}
	for _, worker := range state.WorkerSummaries {
		if worker.WorkerID == focusedID {
			return worker
		}
	}
	return monitor.UIWorkerSummary{}
}

func stylePlainLines(lines []string, width int) []displayLine {
	if len(lines) == 0 {
		return []displayLine{{text: "n/a", tone: "muted"}}
	}
	out := make([]displayLine, 0, len(lines))
	for _, line := range lines {
		trimmed := truncateLine(strings.TrimSpace(line), width)
		tone := "normal"
		lower := strings.ToLower(trimmed)
		if strings.Contains(lower, "warning") || strings.Contains(lower, "blocked") {
			tone = "warning"
		}
		if strings.Contains(lower, "error") || strings.Contains(lower, "failed") {
			tone = "error"
		}
		out = append(out, displayLine{text: trimmed, tone: tone})
	}
	return out
}

func renderPane(width int, title string, lines []displayLine, bg lipgloss.Color) string {
	if width <= 0 {
		width = 80
	}
	inner := width - 2
	if inner < 1 {
		inner = 1
	}
	pad := lipgloss.NewStyle().Width(width).Background(bg)
	head := lipgloss.NewStyle().Width(inner).Background(bg).Foreground(lipgloss.Color("153")).Bold(true)
	body := []string{pad.Render(" " + head.Render(title) + " ")}
	for _, line := range lines {
		style := lipgloss.NewStyle().Width(inner).Background(bg).Foreground(lipgloss.Color("252"))
		switch line.tone {
		case "warning":
			style = style.Foreground(lipgloss.Color("220"))
		case "error":
			style = style.Foreground(lipgloss.Color("203"))
		case "muted":
			style = style.Foreground(lipgloss.Color("246"))
		}
		if line.selected {
			style = style.Background(lipgloss.Color("63")).Foreground(lipgloss.Color("230")).Bold(true)
		}
		body = append(body, pad.Render(" "+style.Render(line.text)+" "))
	}
	return strings.Join(body, "\n")
}

func renderCollapsedPane(width int, title string, hint string, bg lipgloss.Color) string {
	return renderPane(width, title, []displayLine{{text: hint, tone: "muted"}}, bg)
}

func renderPaneStack(width int, panes []string) string {
	if len(panes) == 0 {
		return ""
	}
	sep := lipgloss.NewStyle().Width(width).Background(lipgloss.Color("236")).Foreground(lipgloss.Color("60")).Render(strings.Repeat("─", maxInt(1, width)))
	parts := make([]string, 0, len(panes)*2)
	for i, pane := range panes {
		if i > 0 {
			parts = append(parts, sep)
		}
		parts = append(parts, pane)
	}
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func tailLines(lines []string, count int) []string {
	if count <= 0 || len(lines) <= count {
		return lines
	}
	return append([]string{}, lines[len(lines)-count:]...)
}

func truncateLine(line string, width int) string {
	if width <= 0 {
		return ""
	}
	runes := []rune(line)
	if len(runes) <= width {
		return line
	}
	if width == 1 {
		return "…"
	}
	return string(runes[:width-1]) + "…"
}

func maxInt(a int, b int) int {
	if a > b {
		return a
	}
	return b
}

func (m fullscreenModel) View() string {
	width := m.width
	if width <= 0 {
		width = 80
	}
	foot := lipgloss.NewStyle().Width(width).Foreground(lipgloss.Color("230")).Background(lipgloss.Color("58"))
	if strings.Contains(strings.ToLower(m.statusLine), "❌") {
		foot = foot.Background(lipgloss.Color("52"))
	}
	footer := []string{
		foot.Render(truncateLine(m.statusLine, width)),
		foot.Render(truncateLine(m.keyHint, width)),
	}
	if m.errorLine != "" {
		warn := lipgloss.NewStyle().Width(width).Foreground(lipgloss.Color("230")).Background(lipgloss.Color("94"))
		footer = append(footer, warn.Render(truncateLine("⚠ decode: "+m.errorLine, width)))
	}
	if m.streamDone {
		done := lipgloss.NewStyle().Width(width).Foreground(lipgloss.Color("254")).Background(lipgloss.Color("24"))
		footer = append(footer, done.Render("🧾 stream ended"))
	}
	return lipgloss.JoinVertical(lipgloss.Left, m.viewport.View(), strings.Join(footer, "\n"))
}

func runFullscreenFromReader(reader io.Reader, out io.Writer, errOut io.Writer) error {
	stream := make(chan streamMsg, 64)
	go decodeEvents(reader, stream)
	program := tea.NewProgram(
		newFullscreenModel(stream, nil, false),
		tea.WithOutput(out),
		tea.WithAltScreen(),
	)
	if _, err := program.Run(); err != nil {
		return err
	}
	_ = errOut
	return nil
}

func runFullscreenDemo(out io.Writer, errOut io.Writer) error {
	stream := make(chan streamMsg)
	close(stream)
	program := tea.NewProgram(
		newFullscreenModel(stream, demoEvents(time.Now().UTC()), true),
		tea.WithOutput(out),
		tea.WithAltScreen(),
	)
	if _, err := program.Run(); err != nil {
		return err
	}
	_ = errOut
	return nil
}

func demoEvents(now time.Time) []contracts.Event {
	base := now.Add(-4 * time.Minute)
	return []contracts.Event{
		{Type: contracts.EventTypeRunStarted, TaskID: "yr-s0go", TaskTitle: "E2 Agent backend abstraction and integrations", Metadata: map[string]string{"root_id": "yr-s0go", "concurrency": "2", "model": "openai/gpt-5.3-codex", "runner_timeout": "20m", "stream": "true", "verbose_stream": "false", "stream_output_interval": "150ms", "stream_output_buffer": "64"}, Timestamp: base},
		{Type: contracts.EventTypeTaskStarted, TaskID: "yr-me4i", TaskTitle: "E2-T3 Implement Codex backend MVP", WorkerID: "worker-0", QueuePos: 1, Message: "starting implementation", Timestamp: base.Add(10 * time.Second)},
		{Type: contracts.EventTypeRunnerStarted, TaskID: "yr-me4i", TaskTitle: "E2-T3 Implement Codex backend MVP", WorkerID: "worker-0", Message: "running codex implement", Timestamp: base.Add(20 * time.Second)},
		{Type: contracts.EventTypeRunnerCommandStarted, TaskID: "yr-me4i", WorkerID: "worker-0", Message: "go test ./...", Timestamp: base.Add(25 * time.Second)},
		{Type: contracts.EventTypeRunnerWarning, TaskID: "yr-me4i", WorkerID: "worker-0", Message: "needs backend policy helper", Timestamp: base.Add(40 * time.Second)},
		{Type: contracts.EventTypeTaskStarted, TaskID: "yr-ttw4", TaskTitle: "E2-T5 Implement Kimi backend MVP", WorkerID: "worker-1", QueuePos: 2, Message: "queued", Timestamp: base.Add(50 * time.Second)},
		{Type: contracts.EventTypeRunnerStarted, TaskID: "yr-ttw4", TaskTitle: "E2-T5 Implement Kimi backend MVP", WorkerID: "worker-1", Message: "running kimi implement", Timestamp: base.Add(70 * time.Second)},
		{Type: contracts.EventTypeRunnerOutput, TaskID: "yr-ttw4", WorkerID: "worker-1", Message: "created normalized outcome mapping", Timestamp: base.Add(85 * time.Second)},
		{Type: contracts.EventTypeRunnerFinished, TaskID: "yr-ttw4", WorkerID: "worker-1", Message: "completed", Timestamp: base.Add(95 * time.Second)},
		{Type: contracts.EventTypeTaskFinished, TaskID: "yr-ttw4", TaskTitle: "E2-T5 Implement Kimi backend MVP", WorkerID: "worker-1", Message: "completed", Timestamp: base.Add(100 * time.Second)},
	}
}

func renderDemoSnapshot(now time.Time) string {
	m := monitor.NewModel(func() time.Time { return now })
	for _, event := range demoEvents(now) {
		m.Apply(event)
	}
	return m.View()
}

func decodeEvents(reader io.Reader, out chan<- streamMsg) {
	defer close(out)
	decoder := contracts.NewEventDecoder(reader)
	decodeFailures := 0
	for {
		event, err := decoder.Next()
		if err == io.EOF {
			return
		}
		if err != nil {
			decodeFailures++
			out <- decodeErrorMsg{err: err}
			if decodeFailures >= 3 {
				return
			}
			continue
		}
		decodeFailures = 0
		out <- eventMsg{event: event}
	}
}

func renderFromReader(reader io.Reader, out io.Writer, errOut io.Writer) error {
	decoder := contracts.NewEventDecoder(reader)
	m := monitor.NewModel(nil)
	haveEvents := false
	decodeFailures := 0
	for {
		event, err := decoder.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			decodeFailures++
			haveEvents = true
			m.Apply(contracts.Event{Type: contracts.EventTypeRunnerWarning, Message: "decode_error: " + err.Error()})
			if _, writeErr := io.WriteString(out, m.View()); writeErr != nil {
				return writeErr
			}
			if errOut != nil {
				_, _ = io.WriteString(errOut, "event decode warning: "+err.Error()+"\n")
			}
			if decodeFailures >= 3 {
				return fmt.Errorf("failed to decode event stream after %d errors: %w", decodeFailures, err)
			}
			continue
		}
		decodeFailures = 0
		haveEvents = true
		m.Apply(event)
		if _, writeErr := io.WriteString(out, m.View()); writeErr != nil {
			return writeErr
		}
	}
	if !haveEvents {
		if _, writeErr := io.WriteString(out, m.View()); writeErr != nil {
			return writeErr
		}
	}
	return nil
}
