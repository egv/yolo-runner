package contracts

import (
	"context"
	"errors"
	"io"
	"strconv"
	"sync"
	"time"
)

type StreamEventSink struct {
	stream         *EventStream
	mu             sync.Mutex
	verboseOutput  bool
	outputInterval time.Duration
	maxPending     int
	lastOutputAt   time.Time
	pendingOutput  *Event
	pendingCount   int
	droppedCount   int
}

func NewStreamEventSink(writer io.Writer) *StreamEventSink {
	return NewStreamEventSinkWithOptions(writer, StreamEventSinkOptions{})
}

type StreamEventSinkOptions struct {
	VerboseOutput  bool
	OutputInterval time.Duration
	MaxPending     int
}

func NewStreamEventSinkWithOptions(writer io.Writer, options StreamEventSinkOptions) *StreamEventSink {
	interval := options.OutputInterval
	if interval <= 0 {
		interval = 150 * time.Millisecond
	}
	maxPending := options.MaxPending
	if maxPending <= 0 {
		maxPending = 64
	}
	return &StreamEventSink{
		stream:         NewEventStream(writer),
		verboseOutput:  options.VerboseOutput,
		outputInterval: interval,
		maxPending:     maxPending,
	}
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

	if s.verboseOutput || event.Type != EventTypeRunnerOutput {
		if err := s.flushPendingRunnerOutputLocked(); err != nil {
			return err
		}
		if event.Type == EventTypeRunnerOutput {
			s.lastOutputAt = event.Timestamp
		}
		return s.stream.Write(event)
	}

	now := event.Timestamp
	if s.lastOutputAt.IsZero() || now.Sub(s.lastOutputAt) >= s.outputInterval {
		if err := s.flushPendingRunnerOutputLocked(); err != nil {
			return err
		}
		s.lastOutputAt = now
		return s.stream.Write(event)
	}

	s.queueRunnerOutputLocked(event)
	return nil
}

func (s *StreamEventSink) queueRunnerOutputLocked(event Event) {
	eventCopy := event
	s.pendingOutput = &eventCopy
	if s.pendingCount < s.maxPending {
		s.pendingCount++
		return
	}
	s.droppedCount++
}

func (s *StreamEventSink) flushPendingRunnerOutputLocked() error {
	if s.pendingOutput == nil {
		return nil
	}
	event := *s.pendingOutput
	if event.Metadata == nil {
		event.Metadata = map[string]string{}
	}
	coalesced := s.pendingCount - 1
	if coalesced > 0 {
		event.Metadata["coalesced_outputs"] = strconv.Itoa(coalesced)
	}
	if s.droppedCount > 0 {
		event.Metadata["dropped_outputs"] = strconv.Itoa(s.droppedCount)
	}
	s.pendingOutput = nil
	s.pendingCount = 0
	s.droppedCount = 0
	s.lastOutputAt = event.Timestamp
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
