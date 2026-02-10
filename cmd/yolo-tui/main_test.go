package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/anomalyco/yolo-runner/internal/contracts"
	"github.com/anomalyco/yolo-runner/internal/ui/monitor"
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

func TestFooterLineCountIncludesWarningsAndDoneState(t *testing.T) {
	if got := footerLineCount("", false); got != 2 {
		t.Fatalf("expected base footer size 2, got %d", got)
	}
	if got := footerLineCount("decode failure", true); got != 4 {
		t.Fatalf("expected footer size 4 with warning and done, got %d", got)
	}
}

func TestTruncateLineAddsEllipsisWhenTooLong(t *testing.T) {
	got := truncateLine("abcdefghijklmnopqrstuvwxyz", 8)
	if got != "abcdefg…" {
		t.Fatalf("expected truncated line with ellipsis, got %q", got)
	}
}

func TestRunMainDemoStateRendersWithoutStdin(t *testing.T) {
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	code := RunMain([]string{"--demo-state"}, nil, out, errOut)
	if code != 0 {
		t.Fatalf("expected demo mode code 0, got %d stderr=%q", code, errOut.String())
	}
	if !contains(out.String(), "Panels:") {
		t.Fatalf("expected demo snapshot in output, got %q", out.String())
	}
}

func TestDemoEventsContainRunAndWorkerTasks(t *testing.T) {
	events := demoEvents(time.Date(2026, 2, 10, 12, 0, 0, 0, time.UTC))
	if len(events) < 5 {
		t.Fatalf("expected rich demo event set, got %d", len(events))
	}
	if events[0].Type != contracts.EventTypeRunStarted {
		t.Fatalf("expected first event run_started, got %s", events[0].Type)
	}
	hasSecondWorker := false
	for _, event := range events {
		if event.WorkerID == "worker-1" {
			hasSecondWorker = true
			break
		}
	}
	if !hasSecondWorker {
		t.Fatalf("expected demo events for worker-1")
	}
}

func TestFullscreenModelStaysOpenOnStreamDoneInHoldOpenMode(t *testing.T) {
	stream := make(chan streamMsg)
	close(stream)
	m := newFullscreenModel(stream, demoEvents(time.Now().UTC()), true)
	updated, cmd := m.Update(streamDoneMsg{})
	model := updated.(fullscreenModel)
	if !model.streamDone {
		t.Fatalf("expected streamDone=true")
	}
	if cmd != nil {
		t.Fatalf("expected no quit command in hold-open demo mode")
	}
}

func TestMonitorStructuredStateIncludesPanelSelectionDepth(t *testing.T) {
	m := monitor.NewModel(func() time.Time { return time.Date(2026, 2, 10, 12, 0, 0, 0, time.UTC) })
	for _, event := range demoEvents(time.Date(2026, 2, 10, 12, 0, 0, 0, time.UTC)) {
		m.Apply(event)
	}
	m.HandleKey("down")
	state := m.UIState()
	if len(state.Panels) == 0 {
		t.Fatalf("expected panel lines in structured state")
	}
	hasSelected := false
	for _, line := range state.Panels {
		if line.Selected {
			hasSelected = true
			break
		}
	}
	if !hasSelected {
		t.Fatalf("expected one selected panel line")
	}
}

func TestRenderBodyUsesSingleColumnPaneTitles(t *testing.T) {
	stream := make(chan streamMsg)
	close(stream)
	m := newFullscreenModel(stream, demoEvents(time.Now().UTC()), true)
	m.width = 100
	m.height = 32
	m.resizeViewport()
	body := m.renderBody()
	for _, expected := range []string{"Panels", "Status + Run", "Workers", "Queue + Triage", "History"} {
		if !contains(body, expected) {
			t.Fatalf("expected single-column pane title %q in body", expected)
		}
	}
}

func TestStylePanelLinesRemovesSelectionMarkerAndAddsHierarchy(t *testing.T) {
	lines := []monitor.UIPanelLine{
		{Text: "[-] Run severity=warning", Depth: 0, Selected: true, Severity: "warning"},
		{Text: "[-] Workers severity=warning", Depth: 1, Selected: false, Severity: "warning"},
		{Text: "[+] worker-0 severity=warning", Depth: 2, Selected: false, Severity: "warning"},
		{Text: "[ ] yr-me4i - E2-T3", Depth: 3, Selected: false, Severity: "warning"},
	}
	styled := stylePanelLines(lines, 80)
	if len(styled) != 4 {
		t.Fatalf("expected 4 styled lines, got %d", len(styled))
	}
	if contains(styled[0].text, ">") {
		t.Fatalf("expected selected marker removed, got %q", styled[0].text)
	}
	if !styled[0].selected {
		t.Fatalf("expected first line selected")
	}
	if !contains(styled[1].text, "  [-]") || !contains(styled[2].text, "    [+]") || !contains(styled[3].text, "      [ ]") {
		t.Fatalf("expected hierarchical indentation, got %#v", styled)
	}
}

func TestStylePanelLinesSelectedAndUnselectedHaveSameIndent(t *testing.T) {
	selected := stylePanelLines([]monitor.UIPanelLine{{Text: "[-] Workers severity=warning", Depth: 1, Selected: true, Severity: "warning"}}, 80)
	unselected := stylePanelLines([]monitor.UIPanelLine{{Text: "[-] Workers severity=warning", Depth: 1, Selected: false, Severity: "warning"}}, 80)
	if len(selected) != 1 || len(unselected) != 1 {
		t.Fatalf("expected one line each, got %#v %#v", selected, unselected)
	}
	if selected[0].text != unselected[0].text {
		t.Fatalf("expected selected/unselected indentation match, got %q vs %q", selected[0].text, unselected[0].text)
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
