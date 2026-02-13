package linear

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFirstThoughtSLAWatchdogDefaultDeadlineIsTenSeconds(t *testing.T) {
	event := AgentSessionEvent{
		Action: AgentSessionEventActionCreated,
		AgentSession: AgentSession{
			ID: "session-1",
		},
	}
	emitter := &recordThoughtEmitter{}
	recorder := &recordSLAErrorRecorder{}

	var (
		mu              sync.Mutex
		afterDurations  []time.Duration
		timeoutSignalCh = make(chan time.Time, 1)
	)
	watchdog, err := NewFirstThoughtSLAWatchdog(event, emitter, recorder, FirstThoughtSLAWatchdogOptions{
		After: func(d time.Duration) <-chan time.Time {
			mu.Lock()
			afterDurations = append(afterDurations, d)
			mu.Unlock()
			return timeoutSignalCh
		},
	})
	if err != nil {
		t.Fatalf("new watchdog: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	watchdog.Start(ctx)
	watchdog.MarkThoughtEmitted()
	if err := watchdog.Wait(ctx); err != nil {
		t.Fatalf("wait watchdog: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(afterDurations) != 1 {
		t.Fatalf("expected one deadline registration, got %d", len(afterDurations))
	}
	if afterDurations[0] != 10*time.Second {
		t.Fatalf("expected default deadline 10s, got %s", afterDurations[0])
	}
}

func TestFirstThoughtSLAWatchdogSkipsFallbackWhenThoughtArrivesBeforeDeadline(t *testing.T) {
	event := AgentSessionEvent{
		Action: AgentSessionEventActionCreated,
		AgentSession: AgentSession{
			ID: "session-1",
		},
	}
	emitter := &recordThoughtEmitter{}
	recorder := &recordSLAErrorRecorder{}
	timeoutSignalCh := make(chan time.Time, 1)

	watchdog, err := NewFirstThoughtSLAWatchdog(event, emitter, recorder, FirstThoughtSLAWatchdogOptions{
		After: func(time.Duration) <-chan time.Time {
			return timeoutSignalCh
		},
	})
	if err != nil {
		t.Fatalf("new watchdog: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	watchdog.Start(ctx)
	watchdog.MarkThoughtEmitted()
	timeoutSignalCh <- time.Now()
	if err := watchdog.Wait(ctx); err != nil {
		t.Fatalf("wait watchdog: %v", err)
	}

	if len(emitter.calls()) != 0 {
		t.Fatalf("expected no fallback thought, got %d calls", len(emitter.calls()))
	}
	if len(recorder.calls()) != 0 {
		t.Fatalf("expected no SLA error records, got %d", len(recorder.calls()))
	}
}

func TestFirstThoughtSLAWatchdogEmitsFallbackThoughtWhenDeadlineExpires(t *testing.T) {
	event := AgentSessionEvent{
		Action: AgentSessionEventActionCreated,
		AgentSession: AgentSession{
			ID: "session-1",
		},
	}
	emitter := &recordThoughtEmitter{}
	recorder := &recordSLAErrorRecorder{}
	timeoutSignalCh := make(chan time.Time, 1)

	watchdog, err := NewFirstThoughtSLAWatchdog(event, emitter, recorder, FirstThoughtSLAWatchdogOptions{
		Deadline:     5 * time.Millisecond,
		FallbackBody: "Bootstrapping run context.",
		After: func(time.Duration) <-chan time.Time {
			return timeoutSignalCh
		},
	})
	if err != nil {
		t.Fatalf("new watchdog: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	watchdog.Start(ctx)
	timeoutSignalCh <- time.Now()
	if err := watchdog.Wait(ctx); err != nil {
		t.Fatalf("wait watchdog: %v", err)
	}

	calls := emitter.calls()
	if len(calls) != 1 {
		t.Fatalf("expected one fallback thought call, got %d", len(calls))
	}
	if calls[0].AgentSessionID != "session-1" {
		t.Fatalf("expected fallback session id session-1, got %q", calls[0].AgentSessionID)
	}
	if calls[0].Body != "Bootstrapping run context." {
		t.Fatalf("expected fallback body to round-trip, got %q", calls[0].Body)
	}
	if calls[0].IdempotencyKey != "session-1:first-thought:fallback" {
		t.Fatalf("expected fallback idempotency key, got %q", calls[0].IdempotencyKey)
	}
	if len(recorder.calls()) != 0 {
		t.Fatalf("expected no SLA error records, got %d", len(recorder.calls()))
	}
}

func TestFirstThoughtSLAWatchdogRecordsExplicitErrorWhenFallbackFails(t *testing.T) {
	event := AgentSessionEvent{
		Action: AgentSessionEventActionCreated,
		AgentSession: AgentSession{
			ID: "session-1",
		},
	}
	emitter := &recordThoughtEmitter{err: errors.New("linear write failed")}
	recorder := &recordSLAErrorRecorder{}
	timeoutSignalCh := make(chan time.Time, 1)

	watchdog, err := NewFirstThoughtSLAWatchdog(event, emitter, recorder, FirstThoughtSLAWatchdogOptions{
		Deadline: 5 * time.Millisecond,
		After: func(time.Duration) <-chan time.Time {
			return timeoutSignalCh
		},
	})
	if err != nil {
		t.Fatalf("new watchdog: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	watchdog.Start(ctx)
	timeoutSignalCh <- time.Now()
	err = watchdog.Wait(ctx)
	if err == nil {
		t.Fatalf("expected watchdog error for fallback failure")
	}
	if !strings.Contains(err.Error(), "first thought SLA") {
		t.Fatalf("expected SLA context in error, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "linear write failed") {
		t.Fatalf("expected fallback failure details in error, got %q", err.Error())
	}

	records := recorder.calls()
	if len(records) != 1 {
		t.Fatalf("expected one SLA error record, got %d", len(records))
	}
	if records[0].AgentSessionID != "session-1" {
		t.Fatalf("expected SLA error session id session-1, got %q", records[0].AgentSessionID)
	}
	if !strings.Contains(records[0].Error(), "linear write failed") {
		t.Fatalf("expected recorded error to include fallback cause, got %q", records[0].Error())
	}
}

func TestFirstThoughtSLAWatchdogIgnoresNonCreatedEvents(t *testing.T) {
	event := AgentSessionEvent{
		Action: AgentSessionEventActionPrompted,
		AgentSession: AgentSession{
			ID: "session-1",
		},
	}
	emitter := &recordThoughtEmitter{}
	recorder := &recordSLAErrorRecorder{}
	timeoutSignalCh := make(chan time.Time, 1)

	watchdog, err := NewFirstThoughtSLAWatchdog(event, emitter, recorder, FirstThoughtSLAWatchdogOptions{
		After: func(time.Duration) <-chan time.Time {
			return timeoutSignalCh
		},
	})
	if err != nil {
		t.Fatalf("new watchdog: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	watchdog.Start(ctx)
	timeoutSignalCh <- time.Now()
	if err := watchdog.Wait(ctx); err != nil {
		t.Fatalf("wait watchdog: %v", err)
	}

	if len(emitter.calls()) != 0 {
		t.Fatalf("expected no fallback thought calls, got %d", len(emitter.calls()))
	}
	if len(recorder.calls()) != 0 {
		t.Fatalf("expected no SLA error records, got %d", len(recorder.calls()))
	}
}

type recordThoughtEmitter struct {
	mu    sync.Mutex
	input []ThoughtActivityInput
	err   error
}

func (r *recordThoughtEmitter) EmitThought(_ context.Context, input ThoughtActivityInput) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.input = append(r.input, input)
	if r.err != nil {
		return "", r.err
	}
	return "activity-1", nil
}

func (r *recordThoughtEmitter) calls() []ThoughtActivityInput {
	r.mu.Lock()
	defer r.mu.Unlock()
	copied := make([]ThoughtActivityInput, len(r.input))
	copy(copied, r.input)
	return copied
}

type recordSLAErrorRecorder struct {
	mu     sync.Mutex
	inputs []FirstThoughtSLAError
	err    error
}

func (r *recordSLAErrorRecorder) RecordFirstThoughtSLAError(_ context.Context, input FirstThoughtSLAError) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.inputs = append(r.inputs, input)
	return r.err
}

func (r *recordSLAErrorRecorder) calls() []FirstThoughtSLAError {
	r.mu.Lock()
	defer r.mu.Unlock()
	copied := make([]FirstThoughtSLAError, len(r.inputs))
	copy(copied, r.inputs)
	return copied
}
