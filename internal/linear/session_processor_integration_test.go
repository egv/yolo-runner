package linear

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestAgentSessionProcessorCreatedEvent_EmitsFirstThoughtWithSLAWatchdogRunning(t *testing.T) {
	event := AgentSessionEvent{
		Action: AgentSessionEventActionCreated,
		AgentSession: AgentSession{
			ID: "session-1",
		},
	}

	watchdogStarted := make(chan struct{})
	watchdogStartedClosed := false
	thoughtCalls := []ThoughtActivityInput{}
	recordedSLAError := ""
	var gotTimeout time.Duration

	processor, err := NewAgentSessionProcessor(AgentSessionProcessorConfig{
		EmitThought: func(_ context.Context, input ThoughtActivityInput) (string, error) {
			thoughtCalls = append(thoughtCalls, input)
			return "activity-thought-1", nil
		},
		RecordSLAError: func(_ context.Context, message string) error {
			recordedSLAError = message
			return nil
		},
		RunCreated: func(ctx context.Context, event AgentSessionEvent, emitThought func(context.Context, ThoughtActivityInput) (string, error)) error {
			select {
			case <-watchdogStarted:
			case <-ctx.Done():
				return ctx.Err()
			}
			_, err := emitThought(ctx, ThoughtActivityInput{
				AgentSessionID: event.AgentSession.ID,
				Body:           "Investigating now.",
				IdempotencyKey: "session-1:thought:run-start",
			})
			return err
		},
		After: func(timeout time.Duration) <-chan time.Time {
			gotTimeout = timeout
			if !watchdogStartedClosed {
				close(watchdogStarted)
				watchdogStartedClosed = true
			}
			return make(chan time.Time)
		},
	})
	if err != nil {
		t.Fatalf("new processor: %v", err)
	}

	err = processor.ProcessEvent(context.Background(), event)
	if err != nil {
		t.Fatalf("process created event: %v", err)
	}
	if gotTimeout != defaultFirstThoughtSLATimeout {
		t.Fatalf("expected default first-thought timeout %s, got %s", defaultFirstThoughtSLATimeout, gotTimeout)
	}
	if len(thoughtCalls) != 1 {
		t.Fatalf("expected exactly one thought emission from created run, got %d", len(thoughtCalls))
	}
	if thoughtCalls[0].IdempotencyKey != "session-1:thought:run-start" {
		t.Fatalf("expected created run thought idempotency key, got %q", thoughtCalls[0].IdempotencyKey)
	}
	if recordedSLAError != "" {
		t.Fatalf("expected no SLA error to be recorded, got %q", recordedSLAError)
	}
}

func TestAgentSessionProcessorCreatedEvent_RecordsExplicitSLAErrorWhenFallbackFails(t *testing.T) {
	event := AgentSessionEvent{
		Action: AgentSessionEventActionCreated,
		AgentSession: AgentSession{
			ID: "session-1",
		},
	}

	watchdogStarted := make(chan struct{})
	watchdogStartedClosed := false
	fallbackErr := errors.New("fallback thought unavailable")
	recordedSLAError := ""
	recordCalls := 0
	var gotTimeout time.Duration

	processor, err := NewAgentSessionProcessor(AgentSessionProcessorConfig{
		EmitThought: func(_ context.Context, _ ThoughtActivityInput) (string, error) {
			return "", fallbackErr
		},
		RecordSLAError: func(_ context.Context, message string) error {
			recordedSLAError = message
			recordCalls++
			return nil
		},
		RunCreated: func(ctx context.Context, _ AgentSessionEvent, _ func(context.Context, ThoughtActivityInput) (string, error)) error {
			select {
			case <-watchdogStarted:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
		After: func(timeout time.Duration) <-chan time.Time {
			gotTimeout = timeout
			if !watchdogStartedClosed {
				close(watchdogStarted)
				watchdogStartedClosed = true
			}
			ch := make(chan time.Time, 1)
			ch <- time.Now()
			return ch
		},
	})
	if err != nil {
		t.Fatalf("new processor: %v", err)
	}

	err = processor.ProcessEvent(context.Background(), event)
	if err == nil {
		t.Fatalf("expected created processing to fail when fallback thought fails")
	}
	var slaErr *FirstThoughtSLAError
	if !errors.As(err, &slaErr) {
		t.Fatalf("expected FirstThoughtSLAError, got %T (%v)", err, err)
	}
	if recordCalls != 1 {
		t.Fatalf("expected one explicit SLA error record call, got %d", recordCalls)
	}
	if gotTimeout != defaultFirstThoughtSLATimeout {
		t.Fatalf("expected default first-thought timeout %s, got %s", defaultFirstThoughtSLATimeout, gotTimeout)
	}
	if recordedSLAError == "" {
		t.Fatalf("expected SLA error message to be persisted")
	}
	if !strings.Contains(recordedSLAError, "first-thought SLA exceeded") {
		t.Fatalf("expected persisted SLA error message, got %q", recordedSLAError)
	}
}
