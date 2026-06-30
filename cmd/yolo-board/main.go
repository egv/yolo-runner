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

type boardSnapshot struct {
	items           []workitem.Item
	runners         []workqueue.RunnerRow
	currentByRunner map[string]*workitem.Item
	sources         []workqueue.SourceRow
	stateCounts     map[string]int
}

func (s boardSnapshot) itemCount() int {
	return len(s.items)
}

func (s boardSnapshot) sourceCount() int {
	seen := map[string]struct{}{}
	for _, source := range s.sources {
		seen[source.Source] = struct{}{}
	}
	return len(seen)
}

type streamMsg interface{}

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
		m.snapshot = typed.snapshot
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
		}
	}
	return m, nil
}

func (m boardModel) View() string {
	if m.waitingForDB && m.snapshot.itemCount() == 0 {
		return "waiting for queue DB\n"
	}
	line := fmt.Sprintf("polling %d items across %d sources", m.snapshot.itemCount(), m.snapshot.sourceCount())
	if m.errLine != "" {
		line += "\n" + m.errLine
	}
	return line + "\n\n" + renderRunnersTab(m.snapshot, time.Now().UTC())
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
