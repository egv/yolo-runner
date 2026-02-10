package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
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
	demoState := fs.Bool("demo-state", false, "Render seeded demo state for TUI testing")
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
	monitor    *monitor.Model
	viewport   viewport.Model
	width      int
	height     int
	stream     <-chan streamMsg
	errorLine  string
	streamDone bool
	holdOpen   bool
	statusLine string
	helpLine   string
}

func newFullscreenModel(stream <-chan streamMsg, seedEvents []contracts.Event, holdOpen bool) fullscreenModel {
	vp := viewport.New(80, 24)
	seededMonitor := monitor.NewModel(nil)
	for _, event := range seedEvents {
		seededMonitor.Apply(event)
	}
	initial := "Waiting for event stream...\n"
	if len(seedEvents) > 0 {
		initial = strings.TrimSuffix(seededMonitor.View(), "\n")
	}
	vp.SetContent(initial)
	return fullscreenModel{
		monitor:  seededMonitor,
		viewport: vp,
		width:    80,
		height:   24,
		stream:   stream,
		holdOpen: holdOpen,
		helpLine: "Keys: q quit | arrows/jk move | h/l collapse/expand | enter/space toggle | pgup/pgdn scroll",
	}
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
		if m.holdOpen {
			return m, nil
		}
		return m, tea.Quit
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
	if m.width <= 0 {
		m.viewport.Width = 80
	} else {
		m.viewport.Width = m.width
	}
	footerHeight := footerLineCount(m.errorLine, m.streamDone)
	vh := m.height - footerHeight
	if vh < 1 {
		vh = 1
	}
	m.viewport.Height = vh
}

func footerLineCount(errorLine string, streamDone bool) int {
	lines := 2
	if strings.TrimSpace(errorLine) != "" {
		lines++
	}
	if streamDone {
		lines++
	}
	return lines
}

func (m *fullscreenModel) renderBody() string {
	state := m.monitor.UIState()
	if len(state.StatusBar) > 0 {
		parts := append([]string{}, state.StatusBar...)
		sort.Strings(parts)
		m.statusLine = strings.Join(parts, " | ")
	}
	width := m.viewport.Width
	if width <= 0 {
		width = 80
	}
	top := renderTopBanner(width, state.CurrentTask, state.Phase, state.LastOutputAge)
	panes := []string{
		renderPane(width, "Panels", stylePanelLines(state.Panels, width-4), lipgloss.Color("17")),
		renderPane(width, "Status + Run", stylePlainLines(append([]string{"phase=" + state.Phase, "last_output=" + state.LastOutputAge}, append(state.StatusBar, append(state.Performance, state.RunParams...)...)...), width-4), lipgloss.Color("18")),
		renderPane(width, "Workers", stylePlainLines(state.Workers, width-4), lipgloss.Color("19")),
		renderPane(width, "Queue + Triage", stylePlainLines(append(append([]string{}, state.Landing...), state.Triage...), width-4), lipgloss.Color("20")),
		renderPane(width, "History", stylePlainLines(tailLines(state.History, 28), width-4), lipgloss.Color("235")),
	}
	body := renderPaneStack(width, panes)
	return lipgloss.JoinVertical(lipgloss.Left, top, body)
}

func renderPaneStack(width int, panes []string) string {
	if len(panes) == 0 {
		return ""
	}
	separator := lipgloss.NewStyle().Width(width).Foreground(lipgloss.Color("60")).Background(lipgloss.Color("17")).Render(strings.Repeat("─", max(1, width)))
	parts := make([]string, 0, len(panes)*2)
	for i, pane := range panes {
		if i > 0 {
			parts = append(parts, separator)
		}
		parts = append(parts, pane)
	}
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func max(a int, b int) int {
	if a > b {
		return a
	}
	return b
}

type paneRenderLine struct {
	text     string
	tone     string
	selected bool
}

func renderTopBanner(width int, task string, phase string, age string) string {
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("230")).Render("YOLO TUI")
	sub := lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Render("task=" + task + "  phase=" + phase + "  age=" + age)
	banner := lipgloss.JoinVertical(lipgloss.Left, title, sub)
	style := lipgloss.NewStyle().Width(width).Padding(0, 1).Background(lipgloss.Color("24")).Foreground(lipgloss.Color("230"))
	return style.Render(banner)
}

func stylePanelLines(lines []monitor.UIPanelLine, width int) []paneRenderLine {
	if len(lines) == 0 {
		return []paneRenderLine{{text: "n/a", tone: "muted"}}
	}
	styled := make([]paneRenderLine, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line.Text)
		text := renderPanelTreeLine(trimmed, line.Depth)
		text = truncateLine(text, width)
		switch {
		case line.Severity == "error" || strings.Contains(trimmed, "severity=error"):
			styled = append(styled, paneRenderLine{text: text, tone: "error", selected: line.Selected})
		case line.Severity == "warning" || strings.Contains(trimmed, "severity=warning"):
			styled = append(styled, paneRenderLine{text: text, tone: "warning", selected: line.Selected})
		default:
			styled = append(styled, paneRenderLine{text: text, tone: "normal", selected: line.Selected})
		}
	}
	return styled
}

func renderPanelTreeLine(line string, depth int) string {
	if depth < 0 {
		depth = 0
	}
	if depth == 0 {
		return line
	}
	indent := strings.Repeat("  ", depth)
	return indent + line
}

func stylePlainLines(lines []string, width int) []paneRenderLine {
	if len(lines) == 0 {
		return []paneRenderLine{{text: "n/a", tone: "muted"}}
	}
	styled := make([]paneRenderLine, 0, len(lines))
	for _, line := range lines {
		line = truncateLine(strings.TrimSpace(line), width)
		if strings.Contains(line, "error") || strings.Contains(line, "failed") {
			styled = append(styled, paneRenderLine{text: line, tone: "error"})
			continue
		}
		if strings.Contains(line, "warning") || strings.Contains(line, "blocked") {
			styled = append(styled, paneRenderLine{text: line, tone: "warning"})
			continue
		}
		styled = append(styled, paneRenderLine{text: line, tone: "normal"})
	}
	return styled
}

func renderPane(width int, title string, lines []paneRenderLine, bg lipgloss.Color) string {
	if width <= 0 {
		width = 80
	}
	innerWidth := width - 2
	if innerWidth < 1 {
		innerWidth = 1
	}
	padStyle := lipgloss.NewStyle().Width(width).Background(bg)
	headStyle := lipgloss.NewStyle().Width(innerWidth).Foreground(lipgloss.Color("153")).Bold(true).Background(bg)
	body := make([]string, 0, len(lines)+1)
	body = append(body, padStyle.Render(" "+headStyle.Render(title)+" "))
	for _, line := range lines {
		lineStyle := lipgloss.NewStyle().Width(innerWidth).Background(bg).Foreground(lipgloss.Color("251"))
		switch line.tone {
		case "error":
			lineStyle = lineStyle.Foreground(lipgloss.Color("203"))
		case "warning":
			lineStyle = lineStyle.Foreground(lipgloss.Color("220"))
		case "muted":
			lineStyle = lineStyle.Foreground(lipgloss.Color("246"))
		}
		if line.selected {
			lineStyle = lineStyle.Background(lipgloss.Color("63")).Foreground(lipgloss.Color("230")).Bold(true)
		}
		body = append(body, padStyle.Render(" "+lineStyle.Render(line.text)+" "))
	}
	return strings.Join(body, "\n")
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

func (m fullscreenModel) View() string {
	width := m.width
	if width <= 0 {
		width = 80
	}
	footStyle := lipgloss.NewStyle().Width(width).Foreground(lipgloss.Color("252")).Background(lipgloss.Color("236"))
	status := strings.TrimSpace(m.statusLine)
	if status == "" {
		status = "Waiting for status metrics..."
	}
	if strings.Contains(status, "run:error") || strings.Contains(status, "workers:error") || strings.Contains(status, "tasks:error") {
		footStyle = footStyle.Background(lipgloss.Color("52")).Foreground(lipgloss.Color("230"))
	} else if strings.Contains(status, "warning") {
		footStyle = footStyle.Background(lipgloss.Color("58")).Foreground(lipgloss.Color("230"))
	}
	footer := []string{
		footStyle.Render(truncateLine(status, width)),
		footStyle.Render(truncateLine(m.helpLine, width)),
	}
	if m.errorLine != "" {
		warnStyle := lipgloss.NewStyle().Width(width).Foreground(lipgloss.Color("230")).Background(lipgloss.Color("94"))
		footer = append(footer, warnStyle.Render(truncateLine("Last decode warning: "+m.errorLine, width)))
	}
	if m.streamDone {
		doneStyle := lipgloss.NewStyle().Width(width).Foreground(lipgloss.Color("254")).Background(lipgloss.Color("24"))
		footer = append(footer, doneStyle.Render("Stream ended."))
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
		tea.WithMouseCellMotion(),
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
	seed := demoEvents(time.Now().UTC())
	program := tea.NewProgram(
		newFullscreenModel(stream, seed, true),
		tea.WithOutput(out),
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
	if _, err := program.Run(); err != nil {
		return err
	}
	_ = errOut
	return nil
}

func renderDemoSnapshot(now time.Time) string {
	model := monitor.NewModel(func() time.Time { return now })
	for _, event := range demoEvents(now) {
		model.Apply(event)
	}
	return model.View()
}

func demoEvents(now time.Time) []contracts.Event {
	base := now.Add(-3 * time.Minute)
	return []contracts.Event{
		{
			Type:      contracts.EventTypeRunStarted,
			TaskID:    "yr-s0go",
			TaskTitle: "E2 Agent backend abstraction and integrations",
			Metadata: map[string]string{
				"root_id":                "yr-s0go",
				"concurrency":            "2",
				"model":                  "openai/gpt-5.3-codex",
				"runner_timeout":         "20m",
				"stream":                 "true",
				"verbose_stream":         "false",
				"stream_output_interval": "150ms",
				"stream_output_buffer":   "64",
			},
			Timestamp: base,
		},
		{Type: contracts.EventTypeTaskStarted, TaskID: "yr-me4i", TaskTitle: "E2-T3 Implement Codex backend MVP", WorkerID: "worker-0", QueuePos: 1, Message: "starting implementation", Timestamp: base.Add(10 * time.Second)},
		{Type: contracts.EventTypeRunnerStarted, TaskID: "yr-me4i", TaskTitle: "E2-T3 Implement Codex backend MVP", WorkerID: "worker-0", Message: "running codex implement", Timestamp: base.Add(15 * time.Second)},
		{Type: contracts.EventTypeRunnerOutput, TaskID: "yr-me4i", WorkerID: "worker-0", Message: "wrote adapter scaffolding and failing tests", Timestamp: base.Add(20 * time.Second)},
		{Type: contracts.EventTypeRunnerWarning, TaskID: "yr-me4i", WorkerID: "worker-0", Message: "needs backend policy helper", Timestamp: base.Add(25 * time.Second)},
		{Type: contracts.EventTypeTaskStarted, TaskID: "yr-ttw4", TaskTitle: "E2-T5 Implement Kimi backend MVP", WorkerID: "worker-1", QueuePos: 2, Message: "queued", Timestamp: base.Add(30 * time.Second)},
		{Type: contracts.EventTypeRunnerStarted, TaskID: "yr-ttw4", TaskTitle: "E2-T5 Implement Kimi backend MVP", WorkerID: "worker-1", Message: "running kimi implement", Timestamp: base.Add(35 * time.Second)},
		{Type: contracts.EventTypeRunnerOutput, TaskID: "yr-ttw4", WorkerID: "worker-1", Message: "created normalized outcome mapping", Timestamp: base.Add(40 * time.Second)},
		{Type: contracts.EventTypeRunnerFinished, TaskID: "yr-ttw4", WorkerID: "worker-1", Message: "completed", Timestamp: base.Add(45 * time.Second)},
		{Type: contracts.EventTypeTaskFinished, TaskID: "yr-ttw4", TaskTitle: "E2-T5 Implement Kimi backend MVP", WorkerID: "worker-1", Message: "completed", Timestamp: base.Add(50 * time.Second)},
	}
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
	model := monitor.NewModel(nil)
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
			model.Apply(contracts.Event{Type: contracts.EventTypeRunnerWarning, Message: "decode_error: " + err.Error()})
			if _, writeErr := io.WriteString(out, model.View()); writeErr != nil {
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
		model.Apply(event)
		if _, writeErr := io.WriteString(out, model.View()); writeErr != nil {
			return writeErr
		}
	}
	if !haveEvents {
		if _, writeErr := io.WriteString(out, model.View()); writeErr != nil {
			return writeErr
		}
	}
	return nil
}
