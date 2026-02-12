package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/anomalyco/yolo-runner/internal/contracts"
	"github.com/anomalyco/yolo-runner/internal/ui/monitor"
	tea "github.com/charmbracelet/bubbletea"
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

func contains(text string, sub string) bool {
	for i := 0; i+len(sub) <= len(text); i++ {
		if text[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
