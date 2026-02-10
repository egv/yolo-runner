package contracts

import (
	"bytes"
	"io"
	"testing"
	"time"
)

func TestEventStreamRoundTripNDJSON(t *testing.T) {
	buf := &bytes.Buffer{}
	stream := NewEventStream(buf)

	event := Event{
		Type:      EventTypeRunnerStarted,
		TaskID:    "task-1",
		TaskTitle: "Streaming",
		WorkerID:  "worker-1",
		QueuePos:  1,
		Timestamp: time.Date(2026, 2, 10, 2, 0, 0, 0, time.UTC),
	}
	if err := stream.Write(event); err != nil {
		t.Fatalf("write event: %v", err)
	}

	decoder := NewEventDecoder(bytes.NewReader(buf.Bytes()))
	decoded, err := decoder.Next()
	if err != nil {
		t.Fatalf("decode event: %v", err)
	}
	if decoded.TaskID != event.TaskID || decoded.WorkerID != event.WorkerID || decoded.QueuePos != event.QueuePos {
		t.Fatalf("unexpected decoded event: %#v", decoded)
	}
	if _, err := decoder.Next(); err != io.EOF {
		t.Fatalf("expected EOF after one event, got %v", err)
	}
}
