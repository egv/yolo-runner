package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

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
	if err := fs.Parse(args); err != nil {
		return 1
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
}

func newFullscreenModel(stream <-chan streamMsg) fullscreenModel {
	vp := viewport.New(80, 24)
	vp.SetContent("Waiting for event stream...\n")
	return fullscreenModel{
		monitor:  monitor.NewModel(nil),
		viewport: vp,
		width:    80,
		height:   24,
		stream:   stream,
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
		m.viewport.Width = typed.Width
		m.viewport.Height = typed.Height
		m.viewport.SetContent(m.renderContent())
		return m, nil
	case eventMsg:
		m.monitor.Apply(typed.event)
		m.viewport.SetContent(m.renderContent())
		return m, waitForStreamMessage(m.stream)
	case decodeErrorMsg:
		m.errorLine = strings.TrimSpace(typed.err.Error())
		m.monitor.Apply(contracts.Event{Type: contracts.EventTypeRunnerWarning, Message: "decode_error: " + m.errorLine})
		m.viewport.SetContent(m.renderContent())
		return m, waitForStreamMessage(m.stream)
	case streamDoneMsg:
		m.streamDone = true
		m.viewport.SetContent(m.renderContent())
		return m, tea.Quit
	case tea.KeyMsg:
		switch typed.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "up", "down", "left", "right", "j", "k", "h", "l", "enter", " ":
			key := typed.String()
			if key == " " {
				key = "space"
			}
			m.monitor.HandleKey(key)
			m.viewport.SetContent(m.renderContent())
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

func (m fullscreenModel) renderContent() string {
	body := strings.TrimSuffix(m.monitor.View(), "\n")
	lines := []string{body}
	if m.errorLine != "" {
		lines = append(lines, "", "Last decode warning: "+m.errorLine)
	}
	if m.streamDone {
		lines = append(lines, "", "Stream ended.")
	}
	content := strings.Join(lines, "\n")
	if m.width > 0 && m.height > 0 {
		return lipgloss.NewStyle().Width(m.width).Height(m.height).Render(content)
	}
	return content
}

func (m fullscreenModel) View() string {
	return m.viewport.View()
}

func runFullscreenFromReader(reader io.Reader, out io.Writer, errOut io.Writer) error {
	stream := make(chan streamMsg, 64)
	go decodeEvents(reader, stream)

	program := tea.NewProgram(
		newFullscreenModel(stream),
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
