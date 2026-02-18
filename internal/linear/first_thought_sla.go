package linear

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const (
	defaultFirstThoughtSLATimeout   = 10 * time.Second
	defaultFirstThoughtFallbackBody = "Starting work on your request now."
	firstThoughtFallbackIDSuffix    = "thought:first-sla-fallback"
)

type FirstThoughtSLAConfig struct {
	Timeout        time.Duration
	FallbackBody   string
	EmitThought    func(context.Context, ThoughtActivityInput) (string, error)
	RecordSLAError func(context.Context, string) error
	After          func(time.Duration) <-chan time.Time
}

type FirstThoughtSLAError struct {
	SessionID string
	Timeout   time.Duration
	Cause     error
}

func (err *FirstThoughtSLAError) Error() string {
	if err == nil {
		return "first-thought SLA exceeded"
	}
	message := fmt.Sprintf(
		"first-thought SLA exceeded session=%s timeout=%s",
		strings.TrimSpace(err.SessionID),
		err.Timeout,
	)
	if err.Cause != nil {
		message += ": fallback thought failed: " + err.Cause.Error()
	}
	return message
}

func (err *FirstThoughtSLAError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Cause
}

func EnforceFirstThoughtSLA(ctx context.Context, event AgentSessionEvent, firstThoughtObserved <-chan struct{}, config FirstThoughtSLAConfig) error {
	if event.Action != AgentSessionEventActionCreated {
		return nil
	}

	sessionID := strings.TrimSpace(event.AgentSession.ID)
	if sessionID == "" {
		return fmt.Errorf("agent session id is required")
	}
	if config.EmitThought == nil {
		return fmt.Errorf("emit thought callback is required")
	}

	timeout := config.Timeout
	if timeout <= 0 {
		timeout = defaultFirstThoughtSLATimeout
	}
	after := config.After
	if after == nil {
		after = time.After
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-firstThoughtObserved:
		return nil
	case <-after(timeout):
	}

	fallbackBody := strings.TrimSpace(config.FallbackBody)
	if fallbackBody == "" {
		fallbackBody = defaultFirstThoughtFallbackBody
	}

	_, err := config.EmitThought(ctx, ThoughtActivityInput{
		AgentSessionID: sessionID,
		Body:           fallbackBody,
		IdempotencyKey: firstThoughtFallbackID(sessionID),
	})
	if err == nil {
		return nil
	}

	slaErr := &FirstThoughtSLAError{
		SessionID: sessionID,
		Timeout:   timeout,
		Cause:     err,
	}
	if config.RecordSLAError != nil {
		if recordErr := config.RecordSLAError(ctx, slaErr.Error()); recordErr != nil {
			return fmt.Errorf("%w; record sla error: %v", slaErr, recordErr)
		}
	}
	return slaErr
}

func firstThoughtFallbackID(sessionID string) string {
	session := strings.TrimSpace(sessionID)
	if session == "" {
		return firstThoughtFallbackIDSuffix
	}
	return session + ":" + firstThoughtFallbackIDSuffix
}
