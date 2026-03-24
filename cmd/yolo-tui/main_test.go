package main

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/distributed"
	"github.com/egv/yolo-runner/v2/internal/ui/monitor"
)

func TestRunMainRendersMonitorViewFromStdin(t *testing.T) {
	content := "{\"type\":\"task_started\",\"task_id\":\"task-1\",\"task_title\":\"Readable task\",\"message\":\"started\",\"ts\":\"2026-02-10T12:00:00Z\"}\n" +
		"{\"type\":\"runner_finished\",\"task_id\":\"task-1\",\"message\":\"done\",\"ts\":\"2026-02-10T12:00:05Z\"}\n"
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	code := RunMain([]string{"--events-stdin"}, strings.NewReader(content), out, errOut)
	if code != 0 {
		t.Fatalf("expected code 0, got %d stderr=%q", code, errOut.String())
	}
	if out.String() == "" {
		t.Fatalf("expected rendered view")
	}
	if !contains(out.String(), "Current Task: task-1 - Readable task") {
		t.Fatalf("expected current task in output, got %q", out.String())
	}
}

func TestRenderBodyShowsTaskDetailsForCurrentTask(t *testing.T) {
	model := newFullscreenModel(make(chan streamMsg), nil, true)
	now := time.Date(2026, 2, 10, 12, 0, 0, 0, time.UTC)
	model.monitor.Apply(contracts.Event{
		Type:      contracts.EventTypeTaskStarted,
		TaskID:    "task-1",
		TaskTitle: "Readable task",
		QueuePos:  2,
		Priority:  7,
		Metadata: map[string]string{
			"parent_id":    "parent-1",
			"dependencies": "dep-1, dep-2",
			"worker_id":    "worker-1",
		},
		Timestamp: now,
	})
	model.monitor.Apply(contracts.Event{
		Type:      contracts.EventTypeRunnerCommandStarted,
		TaskID:    "task-1",
		TaskTitle: "Readable task",
		Message:   "run",
	})
	model.detailsCollapsed = false
	body := model.renderBody()

	if !strings.Contains(body, "task=task-1 - Readable task") {
		t.Fatalf("expected task details to include current task, got %q", body)
	}
	if !strings.Contains(body, "parent=parent-1") {
		t.Fatalf("expected task details to include parent id, got %q", body)
	}
	if !strings.Contains(body, "dependencies=dep-1, dep-2") {
		t.Fatalf("expected task details to include dependencies, got %q", body)
	}
	if !strings.Contains(body, "queue_pos=2 priority=7") {
		t.Fatalf("expected task detail metrics to include queue and priority, got %q", body)
	}
}

func TestRunMainSupportsDistributedBusEventsFromEnvelope(t *testing.T) {
	bus := distributed.NewMemoryBus()
	originalBusFactory := newDistributedBus
	t.Cleanup(func() {
		newDistributedBus = originalBusFactory
	})
	newDistributedBus = func(_ string, _ string, _ distributed.BusBackendOptions) (distributed.Bus, error) {
		return bus, nil
	}

	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	publishErr := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		time.Sleep(20 * time.Millisecond)
		subject := distributed.DefaultEventSubjects("unit").MonitorEvent
		event, err := distributed.NewEventEnvelope(distributed.EventTypeMonitorEvent, "agent", "", distributed.MonitorEventPayload{
			Event: contracts.Event{
				Type:      contracts.EventTypeTaskStarted,
				TaskID:    "task-1",
				TaskTitle: "Bus task",
				Message:   "started",
				Timestamp: time.Date(2026, time.January, 10, 12, 0, 0, 0, time.UTC),
			},
		})
		if err != nil {
			publishErr <- err
			return
		}
		_ = bus.Publish(context.Background(), subject, event)
		time.Sleep(20 * time.Millisecond)
		_ = bus.Close()
		publishErr <- nil
	}()

	code := RunMain([]string{
		"--events-bus",
		"--events-bus-backend", "redis",
		"--events-bus-address", "mem://unit-test",
		"--events-bus-prefix", "unit",
		"--events-bus-source", "agent",
	}, nil, out, errOut)
	if code != 0 {
		t.Fatalf("expected code 0, got %d stderr=%q", code, errOut.String())
	}
	<-done
	if err := <-publishErr; err != nil {
		t.Fatalf("publish monitor envelope: %v", err)
	}
	if !contains(out.String(), "Current Task: task-1 - Bus task") {
		t.Fatalf("expected bus event in output, got %q", out.String())
	}
	if errOut.String() != "" {
		t.Fatalf("unexpected stderr output: %q", errOut.String())
	}
}

func TestRunMainSupportsStdinByDefault(t *testing.T) {
	content := "{\"type\":\"task_started\",\"task_id\":\"task-1\",\"task_title\":\"Readable task\",\"message\":\"started\",\"ts\":\"2026-02-10T12:00:00Z\"}\n"
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	code := RunMain(nil, strings.NewReader(content), out, errOut)
	if code != 0 {
		t.Fatalf("expected code 0, got %d stderr=%q", code, errOut.String())
	}
}

func TestRunMainRejectsFileFlag(t *testing.T) {
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	code := RunMain([]string{"--events", "runner-logs/agent.events.jsonl"}, strings.NewReader(""), out, errOut)
	if code != 1 {
		t.Fatalf("expected code 1 for unsupported --events flag, got %d", code)
	}
}

func TestParseEventIncludesParallelContext(t *testing.T) {
	line := []byte(`{"type":"runner_started","task_id":"task-1","task_title":"Readable task","worker_id":"worker-1","clone_path":"/tmp/clones/task-1","queue_pos":2,"message":"implement","ts":"2026-02-10T12:00:05Z"}`)

	event, err := contracts.ParseEventJSONLLine(line)
	if err != nil {
		t.Fatalf("parse event failed: %v", err)
	}
	if event.WorkerID != "worker-1" {
		t.Fatalf("expected worker id, got %q", event.WorkerID)
	}
	if event.ClonePath != "/tmp/clones/task-1" {
		t.Fatalf("expected clone path, got %q", event.ClonePath)
	}
	if event.QueuePos != 2 {
		t.Fatalf("expected queue pos 2, got %d", event.QueuePos)
	}
}

func TestRenderFromReaderParsesIncrementalEvents(t *testing.T) {
	reader, writer := io.Pipe()
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}

	done := make(chan error, 1)
	go func() {
		done <- renderFromReader(reader, out, errOut)
	}()

	_, _ = writer.Write([]byte("{\"type\":\"task_started\",\"task_id\":\"task-1\",\"task_title\":\"Readable task\",\"ts\":\"2026-02-10T12:00:00Z\"}\n"))
	_, _ = writer.Write([]byte("{\"type\":\"runner_started\",\"task_id\":\"task-1\",\"worker_id\":\"worker-1\",\"queue_pos\":1,\"ts\":\"2026-02-10T12:00:01Z\"}\n"))
	_ = writer.Close()

	if err := <-done; err != nil {
		t.Fatalf("render from reader failed: %v", err)
	}
	if !contains(out.String(), "Current Task: task-1 - Readable task") {
		t.Fatalf("expected current task in output, got %q", out.String())
	}
}

func TestRenderFromReaderWritesOnEachEventForLiveUpdates(t *testing.T) {
	input := strings.NewReader("{\"type\":\"task_started\",\"task_id\":\"task-1\",\"task_title\":\"Readable task\",\"ts\":\"2026-02-10T12:00:00Z\"}\n" +
		"{\"type\":\"runner_output\",\"task_id\":\"task-1\",\"message\":\"line\",\"ts\":\"2026-02-10T12:00:01Z\"}\n")
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}

	if err := renderFromReader(input, out, errOut); err != nil {
		t.Fatalf("render failed: %v", err)
	}
	if strings.Count(out.String(), "Current Task:") < 2 {
		t.Fatalf("expected at least two incremental renders, got %q", out.String())
	}
}

func TestRenderFromReaderContinuesAfterMalformedEventWithFallbackWarning(t *testing.T) {
	input := strings.NewReader("{\"type\":\"task_started\",\"task_id\":\"task-1\",\"task_title\":\"Readable task\",\"ts\":\"2026-02-10T12:00:00Z\"}\n" +
		"{\"type\":\"runner_output\",\"task_id\":\"task-1\",\"message\":\"unterminated\"\n" +
		"{\"type\":\"runner_finished\",\"task_id\":\"task-1\",\"message\":\"completed\",\"ts\":\"2026-02-10T12:00:02Z\"}\n")
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}

	if err := renderFromReader(input, out, errOut); err != nil {
		t.Fatalf("render failed: %v", err)
	}
	if !contains(out.String(), "decode_error") {
		t.Fatalf("expected decode fallback warning in output, got %q", out.String())
	}
	if !contains(out.String(), "runner_finished") {
		t.Fatalf("expected stream to continue after malformed event, got %q", out.String())
	}
}

func TestParseMonitorEnvelopeSkipsWrongSourceFilter(t *testing.T) {
	event, err := distributed.NewEventEnvelope(distributed.EventTypeMonitorEvent, "worker", "", distributed.MonitorEventPayload{
		Event: contracts.Event{
			Type: contracts.EventTypeTaskStarted,
		},
	})
	if err != nil {
		t.Fatalf("create monitor envelope: %v", err)
	}
	_, ok, err := parseMonitorEnvelope(event, "master")
	if err != nil {
		t.Fatalf("parse monitor envelope failed: %v", err)
	}
	if ok {
		t.Fatalf("expected event to be filtered out")
	}
}

func TestParseMonitorEnvelopeReturnsMonitorEvent(t *testing.T) {
	event, err := distributed.NewEventEnvelope(distributed.EventTypeMonitorEvent, "master", "", distributed.MonitorEventPayload{
		Event: contracts.Event{
			Type:      contracts.EventTypeTaskStarted,
			TaskID:    "task-1",
			TaskTitle: "Filtered task",
		},
	})
	if err != nil {
		t.Fatalf("create monitor envelope: %v", err)
	}
	parsed, ok, err := parseMonitorEnvelope(event, "")
	if err != nil {
		t.Fatalf("parse monitor envelope failed: %v", err)
	}
	if !ok {
		t.Fatalf("expected envelope to be accepted")
	}
	if parsed.TaskID != "task-1" || parsed.TaskTitle != "Filtered task" {
		t.Fatalf("unexpected parsed event %#v", parsed)
	}
}

func TestRenderFromReaderIgnoresRawACPStderrLines(t *testing.T) {
	input := strings.NewReader("acp: reconnecting to stream\n" +
		"2026-02-12T11:00:00Z WARN transport timeout\n" +
		"raw stderr line 3\n" +
		"raw stderr line 4\n" +
		"raw stderr line 5\n" +
		"{\"type\":\"runner_finished\",\"task_id\":\"task-1\",\"message\":\"completed\",\"ts\":\"2026-02-10T12:00:02Z\"}\n")
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}

	if err := renderFromReader(input, out, errOut); err != nil {
		t.Fatalf("expected raw stderr lines to be ignored, got error: %v", err)
	}
	if !contains(out.String(), "runner_finished") {
		t.Fatalf("expected valid event after raw stderr lines, got %q", out.String())
	}
	if contains(out.String(), "decode_error") {
		t.Fatalf("expected raw stderr lines to be ignored without decode warnings, got %q", out.String())
	}
}

func TestRenderFromReaderIsDeterministicForSameInput(t *testing.T) {
	content := "{\"type\":\"run_started\",\"metadata\":{\"root_id\":\"yr-2y0b\"},\"ts\":\"2026-02-10T12:00:00Z\"}\n" +
		"{\"type\":\"runner_finished\",\"task_id\":\"task-1\",\"message\":\"failed\",\"ts\":\"2026-02-10T12:00:01Z\"}\n"

	outA := &bytes.Buffer{}
	outB := &bytes.Buffer{}
	errA := &bytes.Buffer{}
	errB := &bytes.Buffer{}

	if err := renderFromReader(strings.NewReader(content), outA, errA); err != nil {
		t.Fatalf("first render failed: %v", err)
	}
	if err := renderFromReader(strings.NewReader(content), outB, errB); err != nil {
		t.Fatalf("second render failed: %v", err)
	}
	if outA.String() != outB.String() {
		t.Fatalf("expected deterministic render output for same input")
	}
}

func TestShouldUseFullscreenIsFalseForBufferOutput(t *testing.T) {
	if shouldUseFullscreen(&bytes.Buffer{}) {
		t.Fatalf("expected fullscreen mode to be disabled for non-terminal output")
	}
}

func TestDecodeEventsContinuesAfterMalformedLine(t *testing.T) {
	input := strings.NewReader("{\"type\":\"task_started\",\"task_id\":\"task-1\",\"ts\":\"2026-02-10T12:00:00Z\"}\n" +
		"{\"type\":\"runner_output\",\"task_id\":\"task-1\",\"message\":\"unterminated\"\n" +
		"{\"type\":\"runner_finished\",\"task_id\":\"task-1\",\"message\":\"completed\",\"ts\":\"2026-02-10T12:00:02Z\"}\n")

	ch := make(chan streamMsg, 8)
	go decodeEvents(input, ch)

	hadDecodeError := false
	hadRunnerFinished := false
	for msg := range ch {
		switch typed := msg.(type) {
		case decodeErrorMsg:
			if typed.err == nil {
				t.Fatalf("expected decode error payload")
			}
			hadDecodeError = true
		case eventMsg:
			if typed.event.Type == contracts.EventTypeRunnerFinished {
				hadRunnerFinished = true
			}
		}
	}

	if !hadDecodeError {
		t.Fatalf("expected decode error message in stream")
	}
	if !hadRunnerFinished {
		t.Fatalf("expected stream to continue and emit runner_finished")
	}
}

func TestRunMainDemoStateRendersSnapshotWithoutTerminal(t *testing.T) {
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	code := RunMain([]string{"--demo-state"}, nil, out, errOut)
	if code != 0 {
		t.Fatalf("expected code 0, got %d stderr=%q", code, errOut.String())
	}
	if !contains(out.String(), "Panels:") {
		t.Fatalf("expected monitor snapshot output, got %q", out.String())
	}
}

func TestFullscreenModelDefaultsToCollapsedDetails(t *testing.T) {
	stream := make(chan streamMsg)
	close(stream)
	m := newFullscreenModel(stream, demoEvents(time.Now().UTC()), true)
	body := m.renderBody()
	if !contains(body, "press d to expand") {
		t.Fatalf("expected details pane collapsed by default, got %q", body)
	}
}

func TestFullscreenModelToggleDetailsWithDKey(t *testing.T) {
	stream := make(chan streamMsg)
	close(stream)
	m := newFullscreenModel(stream, demoEvents(time.Now().UTC()), true)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	next := updated.(fullscreenModel)
	body := next.renderBody()
	if contains(body, "press d to expand") {
		t.Fatalf("expected details pane expanded after d toggle")
	}
	if !contains(body, "root_id=yr-s0go") {
		t.Fatalf("expected expanded details to include run params")
	}
}

func TestFullscreenModelStaysOpenAfterStreamDoneUntilQuitKey(t *testing.T) {
	stream := make(chan streamMsg)
	close(stream)
	m := newFullscreenModel(stream, nil, false)

	updated, cmd := m.Update(streamDoneMsg{})
	if cmd != nil {
		t.Fatalf("expected no quit command when stream ends")
	}

	next := updated.(fullscreenModel)
	if !next.streamDone {
		t.Fatalf("expected streamDone to be true")
	}

	updated, cmd = next.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil {
		t.Fatalf("expected quit command on q after stream ends")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("expected tea.QuitMsg, got %T", cmd())
	}
	_ = updated
}

func TestFullscreenModelSupportsAdditionalQuitShortcuts(t *testing.T) {
	stream := make(chan streamMsg)
	close(stream)
	m := newFullscreenModel(stream, demoEvents(time.Now().UTC()), true)
	m.detailsCollapsed = false
	m.activityCollapsed = false
	m.historyCollapsed = false
	streamDoneModel, _ := m.Update(streamDoneMsg{})
	m = streamDoneModel.(fullscreenModel)

	for _, tc := range []struct {
		name string
		msg  tea.KeyMsg
	}{
		{name: "q", msg: tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}}},
		{name: "Q", msg: tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'Q'}}},
		{name: "ctrl+q", msg: tea.KeyMsg{Type: tea.KeyCtrlQ}},
		{name: "ctrl+c", msg: tea.KeyMsg{Type: tea.KeyCtrlC}},
		{name: "esc", msg: tea.KeyMsg{Type: tea.KeyEsc}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			updated, cmd := m.Update(tc.msg)
			if cmd == nil {
				t.Fatalf("expected quit command for %s", tc.name)
			}
			if _, ok := cmd().(tea.QuitMsg); !ok {
				t.Fatalf("expected tea.QuitMsg for %s, got %T", tc.name, cmd())
			}
			next := updated.(fullscreenModel)
			if next.detailsCollapsed != false {
				t.Fatalf("expected detailsCollapsed false after %s", tc.name)
			}
			if next.activityCollapsed != false {
				t.Fatalf("expected activityCollapsed false after %s", tc.name)
			}
			if next.historyCollapsed != false {
				t.Fatalf("expected historyCollapsed false after %s", tc.name)
			}
		})
	}
}

func TestStylePanelLinesUsesDepthWithoutMarkers(t *testing.T) {
	lines := []monitor.UIPanelLine{
		{ID: "run", Depth: 0, Label: "Run", Selected: false, Expanded: true, Leaf: false},
		{ID: "workers", Depth: 1, Label: "Workers", Selected: true, Expanded: true, Leaf: false},
	}
	styled := stylePanelLines(lines, 80)
	if len(styled) != 2 {
		t.Fatalf("expected two lines, got %#v", styled)
	}
	if contains(styled[1].text, ">") {
		t.Fatalf("expected selection without marker, got %q", styled[1].text)
	}
	if !contains(styled[1].text, "  [-] Workers") {
		t.Fatalf("expected indentation from depth, got %q", styled[1].text)
	}
}

func TestStylePanelLinesAddsCompletedMarkerFromUIState(t *testing.T) {
	lines := []monitor.UIPanelLine{
		{ID: "task:task-1", Depth: 2, Label: "task-1 - First", Completed: true, Leaf: true},
	}
	styled := stylePanelLines(lines, 80)
	if len(styled) != 1 {
		t.Fatalf("expected one line, got %#v", styled)
	}
	if !contains(styled[0].text, "✅ task-1 - First") {
		t.Fatalf("expected completed marker to be added from state, got %q", styled[0].text)
	}
}

func TestStylePanelLinesPrependsStageIconForTaskRows(t *testing.T) {
	lines := []monitor.UIPanelLine{
		{ID: "task:task-1", Depth: 2, Label: "task-1 - First", Leaf: true, Stage: contracts.TaskStageRunning},
	}
	styled := stylePanelLines(lines, 80)
	if len(styled) != 1 {
		t.Fatalf("expected one line, got %#v", styled)
	}
	if !contains(styled[0].text, "▶ task-1 - First") {
		t.Fatalf("expected stage icon prepended, got %q", styled[0].text)
	}
}

func TestStylePanelLinesDoesNotPrependStageIconForNonTaskRows(t *testing.T) {
	lines := []monitor.UIPanelLine{
		{ID: "tasks", Depth: 0, Label: "Tasks", Leaf: false, Expanded: true, Stage: contracts.TaskStageRunning},
	}
	styled := stylePanelLines(lines, 80)
	if len(styled) != 1 {
		t.Fatalf("expected one line, got %#v", styled)
	}
	if contains(styled[0].text, "▶") {
		t.Fatalf("expected no stage icon for non-task row, got %q", styled[0].text)
	}
}

func TestStylePanelLinesAppendsOutputSnippetWhenCollapsed(t *testing.T) {
	lines := []monitor.UIPanelLine{
		{ID: "task:task-1", Depth: 1, Label: "task-1", Leaf: false, Expanded: false, OutputSnippet: "building output"},
	}
	styled := stylePanelLines(lines, 80)
	if len(styled) != 1 {
		t.Fatalf("expected one line, got %#v", styled)
	}
	if !contains(styled[0].text, "building output") {
		t.Fatalf("expected output snippet in collapsed line, got %q", styled[0].text)
	}
}

func TestStylePanelLinesDoesNotAppendOutputSnippetWhenExpanded(t *testing.T) {
	lines := []monitor.UIPanelLine{
		{ID: "task:task-1", Depth: 1, Label: "task-1", Leaf: false, Expanded: true, OutputSnippet: "building output"},
	}
	styled := stylePanelLines(lines, 80)
	if len(styled) != 1 {
		t.Fatalf("expected one line, got %#v", styled)
	}
	if contains(styled[0].text, "building output") {
		t.Fatalf("expected no output snippet for expanded line, got %q", styled[0].text)
	}
}

func TestRenderTopIncludesTaskProgressCounter(t *testing.T) {
	state := monitor.UIState{
		CurrentTask:    "task-1 - My task",
		Phase:          "running",
		LastOutputAge:  "5s",
		CompletedCount: 3,
		TotalCount:     7,
	}
	rendered := renderTop(80, state)
	if !strings.Contains(rendered, "3 / 7 tasks") {
		t.Fatalf("expected progress counter '3 / 7 tasks' in header, got %q", rendered)
	}
}

func TestRenderTopKeepsHeaderSingleLineAtTerminalWidth(t *testing.T) {
	state := monitor.UIState{
		CurrentTask:   "yr-very-long-task-id-with-a-title-that-keeps-going",
		Phase:         "running",
		LastOutputAge: "12s",
	}
	rendered := renderTop(24, state)
	if strings.Count(rendered, "\n") != 0 {
		t.Fatalf("expected top bar to remain single-line, got %q", rendered)
	}
}

func contains(text string, sub string) bool {
	for i := 0; i+len(sub) <= len(text); i++ {
		if text[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
