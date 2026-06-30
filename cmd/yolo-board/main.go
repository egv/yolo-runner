package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/version"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

var errQueueDBMissing = errors.New("queue DB missing")

func main() {
	os.Exit(RunMain(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func RunMain(args []string, in io.Reader, out io.Writer, errOut io.Writer) int {
	if version.IsVersionRequest(args) {
		version.Print(out, "yolo-board")
		return 0
	}

	fs := flag.NewFlagSet("yolo-board", flag.ContinueOnError)
	fs.SetOutput(errOut)
	queuePath := fs.String("queue", "", "Path to queue DB")
	eventsStdin := fs.Bool("events-stdin", true, "Read NDJSON events from stdin")
	pollInterval := fs.Duration("poll-interval", time.Second, "Queue DB poll interval")
	_ = fs.Bool("demo-state", false, "Render seeded demo state")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if strings.TrimSpace(*queuePath) == "" {
		fmt.Fprintln(errOut, "--queue is required")
		return 1
	}
	if *pollInterval <= 0 {
		fmt.Fprintln(errOut, "--poll-interval must be positive")
		return 1
	}

	var stream <-chan streamMsg
	if *eventsStdin {
		ch := make(chan streamMsg, 32)
		stream = ch
		go func() {
			readEventsFromStdin(in, ch)
			close(ch)
		}()
	}

	model := newBoardModel(boardConfig{
		queuePath:    *queuePath,
		pollInterval: *pollInterval,
	}, openReadOnlyBoardStore, stream)
	input := in
	if *eventsStdin {
		input = nil
	}
	program := tea.NewProgram(model, tea.WithInput(input), tea.WithOutput(out))
	if _, err := program.Run(); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	return 0
}

type boardConfig struct {
	queuePath    string
	pollInterval time.Duration
}

type boardStore interface {
	ListItems(workqueue.ListItemsFilter) ([]workitem.Item, error)
	ListRunners() ([]workqueue.RunnerRow, error)
	CurrentItemForRunner(string) (*workitem.Item, error)
	ListSources() ([]workqueue.SourceRow, error)
	ItemStateCounts() (map[string]int, error)
}

type boardStoreOpener func(string) (boardStore, error)

type streamMsg interface{}

type boardTab int

const (
	boardTabCollectors boardTab = iota
	boardTabQueue
	boardTabRunners
	boardTabCount
)

type pollTickMsg struct{}
type pollMsg struct {
	snapshot boardSnapshot
}
type eventMsg struct {
	event contracts.Event
}
type pollErrorMsg struct {
	err error
}
type decodeErrorMsg struct {
	err error
}
type streamDoneMsg struct{}

type boardModel struct {
	config       boardConfig
	openStore    boardStoreOpener
	store        boardStore
	stream       <-chan streamMsg
	snapshot     boardSnapshot
	events       []contracts.Event
	activeTab    boardTab
	runnerDetail string
	collectorCur int
	queueCur     int
	runnerCur    int
	waitingForDB bool
	errLine      string
	streamDone   bool
}

func newBoardModel(config boardConfig, opener boardStoreOpener, stream <-chan streamMsg) boardModel {
	if config.pollInterval <= 0 {
		config.pollInterval = time.Second
	}
	if opener == nil {
		opener = openReadOnlyBoardStore
	}
	return boardModel{
		config:       config,
		openStore:    opener,
		stream:       stream,
		waitingForDB: true,
	}
}

func (m boardModel) Init() tea.Cmd {
	cmds := []tea.Cmd{nextPollCmd(m.config.pollInterval)}
	if m.stream != nil {
		cmds = append(cmds, waitForStreamMessage(m.stream))
	}
	return tea.Batch(cmds...)
}

func (m boardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch typed := msg.(type) {
	case pollTickMsg:
		if m.store == nil {
			store, err := m.openStore(m.config.queuePath)
			if err != nil {
				if errors.Is(err, errQueueDBMissing) {
					m.waitingForDB = true
					m.errLine = ""
				} else {
					m.errLine = err.Error()
				}
				return m, nextPollCmd(m.config.pollInterval)
			}
			m.store = store
		}
		return m, pollCmd(m.store)
	case pollMsg:
		m.snapshot.applyPoll(typed.snapshot)
		m.waitingForDB = false
		m.errLine = ""
		return m, nextPollCmd(m.config.pollInterval)
	case pollErrorMsg:
		m.errLine = typed.err.Error()
		return m, nextPollCmd(m.config.pollInterval)
	case eventMsg:
		m.events = append(m.events, typed.event)
		if len(m.events) > 100 {
			m.events = m.events[len(m.events)-100:]
		}
		m.snapshot.applyEvent(typed.event)
		if m.stream != nil && !m.streamDone {
			return m, waitForStreamMessage(m.stream)
		}
		return m, nil
	case decodeErrorMsg:
		m.errLine = typed.err.Error()
		if m.stream != nil && !m.streamDone {
			return m, waitForStreamMessage(m.stream)
		}
		return m, nil
	case streamDoneMsg:
		m.streamDone = true
		return m, nil
	case tea.KeyMsg:
		switch typed.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "1":
			if m.runnerDetail == "" {
				m.activeTab = boardTabCollectors
			}
		case "2":
			if m.runnerDetail == "" {
				m.activeTab = boardTabQueue
			}
		case "3":
			if m.runnerDetail == "" {
				m.activeTab = boardTabRunners
			}
		case "tab":
			if m.runnerDetail == "" {
				m.activeTab = (m.activeTab + 1) % boardTabCount
			}
		case "down", "j":
			m.moveActiveCursor(1)
		case "up", "k":
			m.moveActiveCursor(-1)
		case "enter":
			if m.activeTab != boardTabQueue && m.runnerDetail == "" && len(m.snapshot.runners) > 0 {
				m.runnerDetail = m.snapshot.runners[0].ID
			}
		case "esc":
			m.runnerDetail = ""
		}
	}
	return m, nil
}

func (m boardModel) View() string {
	if m.waitingForDB && m.snapshot.itemCount() == 0 && len(collectorRows(m.snapshot, m.events)) == 0 {
		return "waiting for queue DB\n"
	}
	line := fmt.Sprintf("polling %d items across %d sources", m.snapshot.itemCount(), m.snapshot.sourceCount())
	if m.errLine != "" {
		line += "\n" + m.errLine
	}
	if m.runnerDetail != "" {
		return line + "\n\n" + renderRunnerDetail(m.snapshot, m.events, m.runnerDetail, time.Now().UTC())
	}
	switch m.activeTab {
	case boardTabQueue:
		return line + "\n\n" + renderQueueTab(m.snapshot, time.Now().UTC(), m.queueCur)
	case boardTabRunners:
		return line + "\n\n" + renderRunnersTab(m.snapshot, time.Now().UTC(), m.runnerCur)
	default:
		return line + "\n\n" + renderCollectorsTab(m.snapshot, m.events, time.Now().UTC(), m.collectorCur)
	}
}

func (m *boardModel) moveActiveCursor(delta int) {
	switch m.activeTab {
	case boardTabCollectors:
		m.collectorCur = moveCursor(m.collectorCur, len(collectorRows(m.snapshot, m.events)), delta)
	case boardTabQueue:
		m.queueCur = moveCursor(m.queueCur, len(m.snapshot.items), delta)
	case boardTabRunners:
		m.runnerCur = moveCursor(m.runnerCur, len(m.snapshot.runners), delta)
	}
}

func moveCursor(current int, count int, delta int) int {
	if count <= 0 {
		return 0
	}
	next := current + delta
	if next < 0 {
		return 0
	}
	if next >= count {
		return count - 1
	}
	return next
}

func selectedCursor(cursorArg []int, count int) int {
	cursor := 0
	if len(cursorArg) > 0 {
		cursor = cursorArg[0]
	}
	if cursor < 0 {
		return 0
	}
	if cursor >= count && count > 0 {
		return count - 1
	}
	return cursor
}

func nextPollCmd(interval time.Duration) tea.Cmd {
	return tea.Tick(interval, func(time.Time) tea.Msg {
		return pollTickMsg{}
	})
}

func pollCmd(store boardStore) tea.Cmd {
	return func() tea.Msg {
		return pollBoardStore(context.Background(), store)
	}
}

func pollBoardStore(ctx context.Context, store boardStore) tea.Msg {
	if err := ctx.Err(); err != nil {
		return pollErrorMsg{err: err}
	}
	items, err := store.ListItems(workqueue.ListItemsFilter{})
	if err != nil {
		return pollErrorMsg{err: err}
	}
	runners, err := store.ListRunners()
	if err != nil {
		return pollErrorMsg{err: err}
	}
	currentByRunner := make(map[string]*workitem.Item, len(runners))
	for _, runner := range runners {
		current, err := store.CurrentItemForRunner(runner.ID)
		if err != nil {
			return pollErrorMsg{err: err}
		}
		if current != nil {
			currentByRunner[runner.ID] = current
		}
	}
	sources, err := store.ListSources()
	if err != nil {
		return pollErrorMsg{err: err}
	}
	stateCounts, err := store.ItemStateCounts()
	if err != nil {
		return pollErrorMsg{err: err}
	}
	return pollMsg{
		snapshot: boardSnapshot{
			items:           items,
			runners:         runners,
			currentByRunner: currentByRunner,
			sources:         sources,
			stateCounts:     stateCounts,
		},
	}
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

func readEventsFromStdin(reader io.Reader, out chan<- streamMsg) {
	decoder := contracts.NewEventDecoder(reader)
	for {
		event, err := decoder.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			out <- decodeErrorMsg{err: err}
			continue
		}
		out <- eventMsg{event: event}
	}
}

func openReadOnlyBoardStore(path string) (boardStore, error) {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errQueueDBMissing
		}
		return nil, err
	}
	store, err := workqueue.OpenReadOnly(path)
	if err != nil {
		return nil, err
	}
	return store, nil
}
