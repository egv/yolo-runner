package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/distributed"
	"github.com/egv/yolo-runner/v2/internal/ui/monitor"
	"github.com/egv/yolo-runner/v2/internal/version"
	"golang.org/x/term"
)

const (
	distributedBusRedis        = "redis"
	distributedBusNATS         = "nats"
	runDefaultEventsBusBackend = "redis"
	runDefaultEventsBusPrefix  = "yolo"
	runDefaultMonitorSourceEnv = "YOLO_MONITOR_SOURCE_ID"
	runDefaultBusBackendEnv    = "YOLO_DISTRIBUTED_BUS_BACKEND"
	runDefaultBusAddressEnv    = "YOLO_DISTRIBUTED_BUS_ADDRESS"
	runDefaultBusPrefixEnv     = "YOLO_DISTRIBUTED_BUS_PREFIX"
)

var newDistributedBus = func(backend string, address string, opts distributed.BusBackendOptions) (distributed.Bus, error) {
	switch strings.TrimSpace(backend) {
	case distributedBusRedis:
		return distributed.NewRedisBus(address, opts)
	case distributedBusNATS:
		return distributed.NewNATSBus(address, opts)
	default:
		return nil, fmt.Errorf("unsupported distributed bus backend %q", backend)
	}
}

func main() {
	os.Exit(RunMain(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func RunMain(args []string, in io.Reader, out io.Writer, errOut io.Writer) int {
	if version.IsVersionRequest(args) {
		version.Print(out, "yolo-tui")
		return 0
	}

	fs := flag.NewFlagSet("yolo-tui", flag.ContinueOnError)
	fs.SetOutput(errOut)
	repoRoot := fs.String("repo", ".", "Repository root")
	eventsStdin := fs.Bool("events-stdin", false, "Read NDJSON events from stdin")
	eventsBus := fs.Bool("events-bus", false, "Read monitor events from distributed bus")
	busBackend := fs.String("events-bus-backend", "", "Distributed bus backend (redis, nats)")
	busAddress := fs.String("events-bus-address", "", "Distributed bus address")
	busPrefix := fs.String("events-bus-prefix", "", "Distributed bus subject prefix")
	busSource := fs.String("events-bus-source", "", "Monitor source filter")
	demoState := fs.Bool("demo-state", false, "Render seeded demo state and stay open")
	if err := fs.Parse(args); err != nil {
		return 1
	}

	if *eventsBus && *eventsStdin {
		fmt.Fprintln(errOut, "set exactly one event input mode: --events-stdin or --events-bus")
		return 1
	}
	setFlags := map[string]struct{}{}
	fs.Visit(func(f *flag.Flag) {
		setFlags[f.Name] = struct{}{}
	})
	if _, eventsStdinSet := setFlags["events-stdin"]; !eventsStdinSet && !*eventsBus {
		*eventsStdin = true
	}
	if _, eventsBusSet := setFlags["events-bus"]; !eventsBusSet && !*eventsStdin {
		// default remains stdin when bus flag is not explicitly set
		*eventsStdin = true
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

	if !*eventsBus && !*eventsStdin {
		fmt.Fprintln(errOut, "--events-stdin must be enabled")
		return 1
	}
	if in == nil {
		if !*eventsBus {
			fmt.Fprintln(errOut, "stdin reader is required")
			return 1
		}
	}

	if !*eventsBus {
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

	selectedBusConfig, err := resolveTUIDistributedBusConfig(
		*repoRoot,
		*busBackend,
		*busAddress,
		*busPrefix,
		*busSource,
		os.Getenv,
	)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}

	if shouldUseFullscreen(out) {
		if err := runFullscreenFromBus(
			selectedBusConfig.Backend,
			selectedBusConfig.Address,
			selectedBusConfig.Prefix,
			selectedBusConfig.Source,
			selectedBusConfig.BackendOptions(),
			out,
			errOut,
		); err != nil {
			fmt.Fprintln(errOut, err)
			return 1
		}
		return 0
	}

	if err := renderFromBus(
		selectedBusConfig.Backend,
		selectedBusConfig.Address,
		selectedBusConfig.Prefix,
		selectedBusConfig.Source,
		selectedBusConfig.BackendOptions(),
		out,
		errOut,
	); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	return 0
}

func normalizeDistributedBusBackend(raw string) (string, error) {
	switch strings.TrimSpace(raw) {
	case "", distributedBusRedis:
		return distributedBusRedis, nil
	case distributedBusNATS:
		return distributedBusNATS, nil
	default:
		return "", fmt.Errorf("unsupported distributed bus backend %q (supported: %s, %s)", raw, distributedBusRedis, distributedBusNATS)
	}
}

func resolveTUIDistributedBusConfig(
	repoRoot string,
	flagBackend string,
	flagAddress string,
	flagPrefix string,
	flagSource string,
	getenv func(string) string,
) (distributed.DistributedBusConfig, error) {
	configBus, err := distributed.LoadDistributedBusConfig(repoRoot)
	if err != nil {
		return distributed.DistributedBusConfig{}, err
	}
	configBus = configBus.ApplyDefaults(runDefaultEventsBusBackend, runDefaultEventsBusPrefix)
	if getenv == nil {
		getenv = os.Getenv
	}

	selectedBackend := strings.TrimSpace(flagBackend)
	if selectedBackend == "" {
		selectedBackend = strings.TrimSpace(getenv(runDefaultBusBackendEnv))
	}
	if selectedBackend == "" {
		selectedBackend = configBus.Backend
	}
	selectedBackend, err = normalizeDistributedBusBackend(selectedBackend)
	if err != nil {
		return distributed.DistributedBusConfig{}, err
	}

	selectedAddress := strings.TrimSpace(flagAddress)
	if selectedAddress == "" {
		selectedAddress = strings.TrimSpace(getenv(runDefaultBusAddressEnv))
	}
	if selectedAddress == "" {
		selectedAddress = configBus.Address
	}
	if selectedAddress == "" {
		return distributed.DistributedBusConfig{}, fmt.Errorf("--events-bus-address is required")
	}

	selectedPrefix := strings.TrimSpace(flagPrefix)
	if selectedPrefix == "" {
		selectedPrefix = strings.TrimSpace(getenv(runDefaultBusPrefixEnv))
	}
	if selectedPrefix == "" {
		selectedPrefix = configBus.Prefix
	}
	if selectedPrefix == "" {
		selectedPrefix = runDefaultEventsBusPrefix
	}

	selectedSource := strings.TrimSpace(flagSource)
	if selectedSource == "" {
		selectedSource = strings.TrimSpace(getenv(runDefaultMonitorSourceEnv))
	}
	if selectedSource == "" {
		selectedSource = configBus.Source
	}

	configBus.Backend = selectedBackend
	configBus.Address = selectedAddress
	configBus.Prefix = selectedPrefix
	configBus.Source = selectedSource
	return configBus, nil
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
	stopping          bool
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
		keyHint:           "🧭 jk/↑↓ move  h/l collapse  enter/space toggle  f queue filter  d details  a activity  H history  q quit",
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
		if m.stopping && isTerminalEvent(typed.event.Type) {
			m.stopping = false
		}
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
		rawKey := typed.String()
		normalizedKey := strings.ToLower(strings.TrimSpace(rawKey))
		switch rawKey {
		case "ctrl+c", "q", "Q", "ctrl+q":
			if m.streamDone {
				return m, tea.Quit
			}
			m.stopping = true
			m.viewport.SetContent(m.renderBody())
			return m, nil
		case "esc", "escape":
			return m, tea.Quit
		case "pgup", "pageup":
			m.viewport.HalfViewUp()
			return m, nil
		case "pgdown", "pagedown":
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
		case "f":
			m.monitor.CycleQueueFilter()
			m.viewport.SetContent(m.renderBody())
			return m, nil
		case " ":
			normalizedKey = "space"
		}
		switch normalizedKey {
		case "up", "down", "left", "right", "j", "k", "h", "l", "enter", "space":
			m.monitor.HandleKey(normalizedKey)
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
		details := []string{"phase=" + state.Phase, "last_output=" + state.LastOutputAge}
		details = append(details, state.Performance...)
		details = append(details, state.RunParams...)
		details = append(details, "", "task_details:")
		details = append(details, state.TaskDetails...)
		panes = append(panes, renderPane(width, "📦 Details", stylePlainLines(details, width-4), lipgloss.Color("18")))
	}

	queueTitle := fmt.Sprintf("🗂 Queue (priority, %s)", state.QueueFilter)
	panes = append(panes, renderPane(width, queueTitle, stylePlainLines(state.Queue, width-4), lipgloss.Color("20")))
	panes = append(panes, renderPane(width, "🌳 Task Graph", stylePlainLines(state.TaskGraph, width-4), lipgloss.Color("21")))
	panes = append(panes, renderPane(width, "🧰 Executor Dashboard", stylePlainLines(state.ExecutorDashboard, width-4), lipgloss.Color("22")))
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
	header := fmt.Sprintf("🚀 %s   🎯 %s   ⏳ %s   %d / %d tasks", state.CurrentTask, state.Phase, state.LastOutputAge, state.CompletedCount, state.TotalCount)
	style := lipgloss.NewStyle().Width(width).Padding(0, 1).Background(lipgloss.Color("24")).Foreground(lipgloss.Color("230")).Bold(true)
	return style.Render(truncateDisplayWidth(header, width-2))
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
		label := line.Label
		if line.Completed && !strings.HasPrefix(strings.TrimSpace(label), "✅") {
			label = "✅ " + label
		} else if !line.Completed && strings.HasPrefix(line.ID, "task:") {
			label = monitor.StageIcon(line.Stage) + " " + label
		}
		if !line.Expanded && !line.Leaf && line.OutputSnippet != "" {
			label = label + " | " + line.OutputSnippet
		}
		text := strings.Repeat("  ", maxInt(0, line.Depth)) + glyph + " " + label
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

func truncateDisplayWidth(line string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(line) <= width {
		return line
	}
	if width == 1 {
		return "…"
	}
	limit := width - 1
	truncated := ""
	for _, r := range line {
		next := truncated + string(r)
		if lipgloss.Width(next) > limit {
			break
		}
		truncated = next
	}
	return truncated + "…"
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
	if m.stopping {
		stop := lipgloss.NewStyle().Width(width).Foreground(lipgloss.Color("230")).Background(lipgloss.Color("52")).Bold(true)
		footer = append(footer, stop.Render("Stopping..."))
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

func isTerminalEvent(eventType contracts.EventType) bool {
	switch eventType {
	case contracts.EventTypeRunnerFinished, contracts.EventTypeTaskFinished:
		return true
	default:
		return false
	}
}

func runFullscreenFromReader(reader io.Reader, out io.Writer, errOut io.Writer) error {
	stream := make(chan streamMsg, 64)
	go decodeEvents(reader, stream)
	return runFullscreenFromStream(stream, out, errOut)
}

func runFullscreenFromBus(busBackend, busAddress, busPrefix, busSource string, opts distributed.BusBackendOptions, out io.Writer, errOut io.Writer) error {
	stream, stop, err := startMonitorEventStream(busBackend, busAddress, busPrefix, busSource, opts)
	if err != nil {
		return err
	}
	defer stop()
	return runFullscreenFromStream(stream, out, errOut)
}

func runFullscreenFromStream(stream <-chan streamMsg, out io.Writer, errOut io.Writer) error {
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

func renderFromBus(busBackend, busAddress, busPrefix, busSource string, opts distributed.BusBackendOptions, out io.Writer, errOut io.Writer) error {
	stream, stop, err := startMonitorEventStream(busBackend, busAddress, busPrefix, busSource, opts)
	if err != nil {
		return err
	}
	defer stop()
	return renderFromStream(stream, out, errOut)
}

func renderFromReader(reader io.Reader, out io.Writer, errOut io.Writer) error {
	stream := make(chan streamMsg, 64)
	go decodeEvents(reader, stream)
	return renderFromStream(stream, out, errOut)
}

func renderFromStream(stream <-chan streamMsg, out io.Writer, errOut io.Writer) error {
	m := monitor.NewModel(nil)
	haveEvents := false
	decodeFailures := 0
	for {
		msg, ok := <-stream
		if !ok {
			break
		}
		switch typed := msg.(type) {
		case eventMsg:
			decodeFailures = 0
			haveEvents = true
			m.Apply(typed.event)
		case decodeErrorMsg:
			decodeFailures++
			haveEvents = true
			m.Apply(contracts.Event{
				Type:    contracts.EventTypeRunnerWarning,
				Message: "decode_error: " + typed.err.Error(),
			})
			if _, writeErr := io.WriteString(out, m.View()); writeErr != nil {
				return writeErr
			}
			if errOut != nil {
				_, _ = io.WriteString(errOut, "event decode warning: "+typed.err.Error()+"\n")
			}
			if decodeFailures >= 3 {
				return fmt.Errorf("failed to decode event stream after %d errors: %w", decodeFailures, typed.err)
			}
			continue
		default:
			continue
		}
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

func startMonitorEventStream(busBackend string, busAddress string, busPrefix string, busSource string, opts distributed.BusBackendOptions) (<-chan streamMsg, func(), error) {
	bus, err := newDistributedBus(busBackend, busAddress, opts)
	if err != nil {
		return nil, nil, err
	}
	subject := distributed.DefaultEventSubjects(busPrefix).MonitorEvent
	ctx, cancel := context.WithCancel(context.Background())
	out := make(chan streamMsg, 64)

	rawEvents, unsubscribe, err := bus.Subscribe(ctx, subject)
	if err != nil {
		_ = bus.Close()
		cancel()
		close(out)
		return nil, nil, err
	}

	stop := func() {
		cancel()
		if unsubscribe != nil {
			unsubscribe()
		}
		_ = bus.Close()
	}

	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case env, ok := <-rawEvents:
				if !ok {
					return
				}
				event, shouldUse, err := parseMonitorEnvelope(env, busSource)
				if err != nil {
					out <- decodeErrorMsg{err: err}
					continue
				}
				if !shouldUse {
					continue
				}
				out <- eventMsg{event: event}
			}
		}
	}()
	return out, stop, nil
}

func parseMonitorEnvelope(envelope distributed.EventEnvelope, sourceFilter string) (contracts.Event, bool, error) {
	if envelope.Type != distributed.EventTypeMonitorEvent {
		return contracts.Event{}, false, nil
	}
	if sourceFilter != "" && strings.TrimSpace(envelope.Source) != strings.TrimSpace(sourceFilter) {
		return contracts.Event{}, false, nil
	}
	payload := distributed.MonitorEventPayload{}
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return contracts.Event{}, true, err
	}
	if payload.Event.Type == "" && payload.Event.TaskID == "" && payload.Event.WorkerID == "" && payload.Event.TaskTitle == "" && payload.Event.Message == "" {
		return contracts.Event{}, true, nil
	}
	return payload.Event, true, nil
}
