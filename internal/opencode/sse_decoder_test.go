package opencode

import (
	"bufio"
	"strings"
	"testing"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

func TestDecodeNextSSEFrameReturnsEventWithEventAndData(t *testing.T) {
	input := "event: message\ndata: {\"type\":\"token\",\"text\":\"hello\"}\n\n"
	scanner := bufio.NewScanner(strings.NewReader(input))

	event, ok, err := decodeNextSSEFrame(scanner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected a decoded SSE frame")
	}
	if event.Event != "message" {
		t.Fatalf("expected event name 'message', got %q", event.Event)
	}
	if event.Data != `{"type":"token","text":"hello"}` {
		t.Fatalf("expected token data, got %q", event.Data)
	}
}

func TestDecodeNextSSEFrameReturnsEventWithIDField(t *testing.T) {
	input := "id: 42\nevent: update\ndata: {\"type\":\"delta\"}\n\n"
	scanner := bufio.NewScanner(strings.NewReader(input))

	event, ok, err := decodeNextSSEFrame(scanner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected a decoded SSE frame")
	}
	if event.ID != "42" {
		t.Fatalf("expected id '42', got %q", event.ID)
	}
	if event.Event != "update" {
		t.Fatalf("expected event name 'update', got %q", event.Event)
	}
}

func TestDecodeNextSSEFrameJoinsMultiLineData(t *testing.T) {
	input := "event: message\ndata: line1\ndata: line2\n\n"
	scanner := bufio.NewScanner(strings.NewReader(input))

	event, ok, err := decodeNextSSEFrame(scanner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected a decoded SSE frame")
	}
	if event.Data != "line1\nline2" {
		t.Fatalf("expected joined multi-line data, got %q", event.Data)
	}
}

func TestDecodeNextSSEFrameSkipsEmptyLeadingBlankLines(t *testing.T) {
	input := "\n\nevent: ping\ndata: {}\n\n"
	scanner := bufio.NewScanner(strings.NewReader(input))

	event, ok, err := decodeNextSSEFrame(scanner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected a decoded SSE frame")
	}
	if event.Event != "ping" {
		t.Fatalf("expected 'ping' event, got %q", event.Event)
	}
}

func TestDecodeNextSSEFrameReturnsFalseAtEOF(t *testing.T) {
	input := ""
	scanner := bufio.NewScanner(strings.NewReader(input))

	_, ok, err := decodeNextSSEFrame(scanner)
	if err != nil {
		t.Fatalf("unexpected error at EOF: %v", err)
	}
	if ok {
		t.Fatal("expected false at EOF")
	}
}

func TestDecodeNextSSEFrameReturnsFalseForDataOnlyEventAtEOF(t *testing.T) {
	// An incomplete frame (no trailing blank line) is discarded at EOF.
	input := "event: message\ndata: hello"
	scanner := bufio.NewScanner(strings.NewReader(input))

	_, ok, err := decodeNextSSEFrame(scanner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected false for incomplete frame at EOF")
	}
}

func TestDecodeNextSSEFrameDecodesConsecutiveFrames(t *testing.T) {
	input := "event: a\ndata: first\n\nevent: b\ndata: second\n\n"
	scanner := bufio.NewScanner(strings.NewReader(input))

	first, ok, err := decodeNextSSEFrame(scanner)
	if err != nil || !ok {
		t.Fatalf("expected first frame: err=%v ok=%v", err, ok)
	}
	second, ok, err := decodeNextSSEFrame(scanner)
	if err != nil || !ok {
		t.Fatalf("expected second frame: err=%v ok=%v", err, ok)
	}
	third, ok, err := decodeNextSSEFrame(scanner)
	if err != nil {
		t.Fatalf("unexpected error on third call: %v", err)
	}
	if ok {
		t.Fatalf("expected EOF on third call, got %#v", third)
	}

	if first.Event != "a" || first.Data != "first" {
		t.Fatalf("unexpected first frame: %#v", first)
	}
	if second.Event != "b" || second.Data != "second" {
		t.Fatalf("unexpected second frame: %#v", second)
	}
}

func TestDecodeNextSSEFrameIgnoresCommentLines(t *testing.T) {
	input := ": this is a comment\nevent: msg\ndata: body\n\n"
	scanner := bufio.NewScanner(strings.NewReader(input))

	event, ok, err := decodeNextSSEFrame(scanner)
	if err != nil || !ok {
		t.Fatalf("expected decoded frame: err=%v ok=%v", err, ok)
	}
	if event.Event != "msg" || event.Data != "body" {
		t.Fatalf("unexpected event: %#v", event)
	}
}

func TestDecodeNextSSEFrameReturnsOpenCodeTokenEvent(t *testing.T) {
	// Verify typical opencode serve SSE event is decoded correctly.
	input := "event: event\ndata: {\"type\":\"message.part.added\",\"properties\":{\"part\":{\"type\":\"text\",\"text\":\"Hello world\"}}}\n\n"
	scanner := bufio.NewScanner(strings.NewReader(input))

	event, ok, err := decodeNextSSEFrame(scanner)
	if err != nil || !ok {
		t.Fatalf("expected decoded opencode event: err=%v ok=%v", err, ok)
	}
	if event.Event != "event" {
		t.Fatalf("expected event name 'event', got %q", event.Event)
	}
	if !strings.Contains(event.Data, "message.part.added") {
		t.Fatalf("expected opencode event type in data, got %q", event.Data)
	}
}

func TestDecodeNextSSEFrameDecodesDataFieldWithoutLeadingSpace(t *testing.T) {
	// SSE spec allows "data:value" (no space after colon) as well as "data: value".
	input := "event: msg\ndata:hello-no-space\n\n"
	scanner := bufio.NewScanner(strings.NewReader(input))

	event, ok, err := decodeNextSSEFrame(scanner)
	if err != nil || !ok {
		t.Fatalf("expected decoded frame: err=%v ok=%v", err, ok)
	}
	if event.Data != "hello-no-space" {
		t.Fatalf("expected data without leading space, got %q", event.Data)
	}
}

func TestDecodeNextSSEFrameDecodesFrameWithOnlyEventField(t *testing.T) {
	// A frame with only an event field (no data or id) should be dispatched.
	input := "event: ping\n\n"
	scanner := bufio.NewScanner(strings.NewReader(input))

	event, ok, err := decodeNextSSEFrame(scanner)
	if err != nil || !ok {
		t.Fatalf("expected decoded frame: err=%v ok=%v", err, ok)
	}
	if event.Event != "ping" {
		t.Fatalf("expected event 'ping', got %q", event.Event)
	}
	if event.Data != "" {
		t.Fatalf("expected empty data, got %q", event.Data)
	}
}

// Verify the decoder result satisfies the contracts.SSEEvent type shape used
// elsewhere in the codebase.
var _ contracts.SSEEvent = contracts.SSEEvent{}
