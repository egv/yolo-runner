package linear

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestEnforceFirstThoughtSLA_UsesDefaultTenSecondsAndEmitsFallbackThought(t *testing.T) {
	event := AgentSessionEvent{
		Action: AgentSessionEventActionCreated,
		AgentSession: AgentSession{
			ID: "session-1",
		},
	}

	var gotTimeout time.Duration
	var gotInput ThoughtActivityInput
	err := EnforceFirstThoughtSLA(context.Background(), event, nil, FirstThoughtSLAConfig{
		EmitThought: func(_ context.Context, input ThoughtActivityInput) (string, error) {
			gotInput = input
			return "activity-1", nil
		},
		After: func(timeout time.Duration) <-chan time.Time {
			gotTimeout = timeout
			ch := make(chan time.Time, 1)
			ch <- time.Now()
			return ch
		},
	})
	if err != nil {
		t.Fatalf("enforce first-thought sla: %v", err)
	}
	if gotTimeout != defaultFirstThoughtSLATimeout {
		t.Fatalf("expected default timeout %s, got %s", defaultFirstThoughtSLATimeout, gotTimeout)
	}
	if gotInput.AgentSessionID != "session-1" {
		t.Fatalf("expected session id to propagate, got %q", gotInput.AgentSessionID)
	}
	if gotInput.Body != defaultFirstThoughtFallbackBody {
		t.Fatalf("expected default fallback body %q, got %q", defaultFirstThoughtFallbackBody, gotInput.Body)
	}
	if gotInput.IdempotencyKey != "session-1:thought:first-sla-fallback" {
		t.Fatalf("expected fallback idempotency key, got %q", gotInput.IdempotencyKey)
	}
}

func TestEnforceFirstThoughtSLA_DoesNothingWhenFirstThoughtAlreadyObserved(t *testing.T) {
	event := AgentSessionEvent{
		Action: AgentSessionEventActionCreated,
		AgentSession: AgentSession{
			ID: "session-1",
		},
	}

	observed := make(chan struct{}, 1)
	observed <- struct{}{}

	emitCalled := false
	err := EnforceFirstThoughtSLA(context.Background(), event, observed, FirstThoughtSLAConfig{
		EmitThought: func(_ context.Context, _ ThoughtActivityInput) (string, error) {
			emitCalled = true
			return "", nil
		},
		After: func(time.Duration) <-chan time.Time {
			return make(chan time.Time)
		},
	})
	if err != nil {
		t.Fatalf("enforce first-thought sla: %v", err)
	}
	if emitCalled {
		t.Fatalf("expected fallback thought not to be emitted when first thought is already observed")
	}
}

func TestEnforceFirstThoughtSLA_RecordsExplicitErrorWhenFallbackEmissionFails(t *testing.T) {
	event := AgentSessionEvent{
		Action: AgentSessionEventActionCreated,
		AgentSession: AgentSession{
			ID: "session-1",
		},
	}

	cause := errors.New("rate limited")
	var recordedMessage string
	err := EnforceFirstThoughtSLA(context.Background(), event, nil, FirstThoughtSLAConfig{
		EmitThought: func(_ context.Context, _ ThoughtActivityInput) (string, error) {
			return "", cause
		},
		RecordSLAError: func(_ context.Context, message string) error {
			recordedMessage = message
			return nil
		},
		After: func(time.Duration) <-chan time.Time {
			ch := make(chan time.Time, 1)
			ch <- time.Now()
			return ch
		},
	})
	if err == nil {
		t.Fatalf("expected SLA enforcement to fail when fallback thought emission fails")
	}
	var slaErr *FirstThoughtSLAError
	if !errors.As(err, &slaErr) {
		t.Fatalf("expected FirstThoughtSLAError, got %T (%v)", err, err)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("expected returned error to wrap fallback cause")
	}
	if recordedMessage == "" {
		t.Fatalf("expected explicit SLA error to be recorded")
	}
	if !strings.Contains(recordedMessage, "first-thought SLA exceeded") {
		t.Fatalf("expected recorded message to include SLA reason, got %q", recordedMessage)
	}
}

func TestEnforceFirstThoughtSLA_SkipsNonCreatedEvents(t *testing.T) {
	event := AgentSessionEvent{
		Action: AgentSessionEventActionPrompted,
		AgentSession: AgentSession{
			ID: "session-1",
		},
	}

	emitCalled := false
	err := EnforceFirstThoughtSLA(context.Background(), event, nil, FirstThoughtSLAConfig{
		EmitThought: func(_ context.Context, _ ThoughtActivityInput) (string, error) {
			emitCalled = true
			return "activity-1", nil
		},
	})
	if err != nil {
		t.Fatalf("enforce first-thought sla: %v", err)
	}
	if emitCalled {
		t.Fatalf("expected non-created events to skip first-thought SLA enforcement")
	}
}
