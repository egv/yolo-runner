package linear

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	defaultFirstThoughtSLADeadline     = 10 * time.Second
	defaultFirstThoughtFallbackMessage = "Processing started."
)

var ErrFirstThoughtSLAWatchdogConfig = errors.New("first-thought SLA watchdog config invalid")

type FirstThoughtSLAWatchdogOptions struct {
	Deadline     time.Duration
	FallbackBody string
	After        func(time.Duration) <-chan time.Time
	Now          func() time.Time
}

type FirstThoughtEmitter interface {
	EmitThought(ctx context.Context, input ThoughtActivityInput) (string, error)
}

type FirstThoughtSLAErrorRecorder interface {
	RecordFirstThoughtSLAError(ctx context.Context, input FirstThoughtSLAError) error
}

type FirstThoughtSLAError struct {
	AgentSessionID string
	Deadline       time.Duration
	OccurredAt     time.Time
	Cause          error
}

func (e FirstThoughtSLAError) Error() string {
	sessionID := strings.TrimSpace(e.AgentSessionID)
	if sessionID == "" {
		sessionID = "<unknown>"
	}
	base := fmt.Sprintf("first thought SLA violated for session %s after %s", sessionID, e.Deadline)
	if e.Cause != nil {
		return base + ": " + e.Cause.Error()
	}
	return base
}

func (e FirstThoughtSLAError) Unwrap() error {
	return e.Cause
}

type FirstThoughtSLAWatchdog struct {
	event        AgentSessionEvent
	emitter      FirstThoughtEmitter
	errorRecorder FirstThoughtSLAErrorRecorder

	deadline     time.Duration
	fallbackBody string
	after        func(time.Duration) <-chan time.Time
	now          func() time.Time

	enforce bool

	startOnce sync.Once
	markOnce  sync.Once

	thoughtEmittedCh chan struct{}
	doneCh           chan struct{}

	mu  sync.Mutex
	err error
}

func NewFirstThoughtSLAWatchdog(event AgentSessionEvent, emitter FirstThoughtEmitter, errorRecorder FirstThoughtSLAErrorRecorder, opts FirstThoughtSLAWatchdogOptions) (*FirstThoughtSLAWatchdog, error) {
	deadline := opts.Deadline
	if deadline <= 0 {
		deadline = defaultFirstThoughtSLADeadline
	}
	fallbackBody := strings.TrimSpace(opts.FallbackBody)
	if fallbackBody == "" {
		fallbackBody = defaultFirstThoughtFallbackMessage
	}
	after := opts.After
	if after == nil {
		after = time.After
	}
	nowFn := opts.Now
	if nowFn == nil {
		nowFn = func() time.Time { return time.Now().UTC() }
	}

	enforce := event.Action == AgentSessionEventActionCreated
	if enforce {
		if strings.TrimSpace(event.AgentSession.ID) == "" {
			return nil, fmt.Errorf("%w: created event agent session id is required", ErrFirstThoughtSLAWatchdogConfig)
		}
		if emitter == nil {
			return nil, fmt.Errorf("%w: created event requires thought emitter", ErrFirstThoughtSLAWatchdogConfig)
		}
	}

	return &FirstThoughtSLAWatchdog{
		event:           event,
		emitter:         emitter,
		errorRecorder:   errorRecorder,
		deadline:        deadline,
		fallbackBody:    fallbackBody,
		after:           after,
		now:             nowFn,
		enforce:         enforce,
		thoughtEmittedCh: make(chan struct{}),
		doneCh:          make(chan struct{}),
	}, nil
}

func (w *FirstThoughtSLAWatchdog) Start(ctx context.Context) {
	if w == nil {
		return
	}

	w.startOnce.Do(func() {
		if !w.enforce {
			close(w.doneCh)
			return
		}

		timeoutCh := w.after(w.deadline)
		go func() {
			defer close(w.doneCh)

			select {
			case <-ctx.Done():
				return
			case <-w.thoughtEmittedCh:
				return
			case <-timeoutCh:
			}

			// Re-check thought state in case timeout and thought emission raced.
			select {
			case <-w.thoughtEmittedCh:
				return
			default:
			}

			if _, err := w.emitter.EmitThought(context.Background(), ThoughtActivityInput{
				AgentSessionID: w.event.AgentSession.ID,
				Body:           w.fallbackBody,
				IdempotencyKey: firstThoughtFallbackIdempotencyKey(w.event.AgentSession.ID),
			}); err != nil {
				slaErr := FirstThoughtSLAError{
					AgentSessionID: w.event.AgentSession.ID,
					Deadline:       w.deadline,
					OccurredAt:     w.now(),
					Cause:          err,
				}

				if w.errorRecorder != nil {
					if recordErr := w.errorRecorder.RecordFirstThoughtSLAError(context.Background(), slaErr); recordErr != nil {
						w.setErr(fmt.Errorf("%w: record explicit SLA error: %v", slaErr, recordErr))
						return
					}
				}
				w.setErr(slaErr)
				return
			}

			w.MarkThoughtEmitted()
		}()
	})
}

func (w *FirstThoughtSLAWatchdog) MarkThoughtEmitted() {
	if w == nil {
		return
	}
	w.markOnce.Do(func() {
		close(w.thoughtEmittedCh)
	})
}

func (w *FirstThoughtSLAWatchdog) Wait(ctx context.Context) error {
	if w == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-w.doneCh:
		return w.getErr()
	}
}

func firstThoughtFallbackIdempotencyKey(agentSessionID string) string {
	return strings.TrimSpace(agentSessionID) + ":first-thought:fallback"
}

func (w *FirstThoughtSLAWatchdog) setErr(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.err = err
}

func (w *FirstThoughtSLAWatchdog) getErr() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.err
}
