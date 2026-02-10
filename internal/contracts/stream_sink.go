package contracts

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"
)

type StreamEventSink struct {
	stream *EventStream
	mu     sync.Mutex
}

func NewStreamEventSink(writer io.Writer) *StreamEventSink {
	return &StreamEventSink{stream: NewEventStream(writer)}
}

func (s *StreamEventSink) Emit(_ context.Context, event Event) error {
	if s == nil || s.stream == nil {
		return nil
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stream.Write(event)
}

type FanoutEventSink struct {
	sinks []EventSink
}

func NewFanoutEventSink(sinks ...EventSink) *FanoutEventSink {
	filtered := make([]EventSink, 0, len(sinks))
	for _, sink := range sinks {
		if sink != nil {
			filtered = append(filtered, sink)
		}
	}
	return &FanoutEventSink{sinks: filtered}
}

func (f *FanoutEventSink) Emit(ctx context.Context, event Event) error {
	if f == nil {
		return nil
	}
	var err error
	for _, sink := range f.sinks {
		err = errors.Join(err, sink.Emit(ctx, event))
	}
	return err
}
