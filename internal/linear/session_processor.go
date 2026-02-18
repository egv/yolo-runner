package linear

import (
	"context"
	"fmt"
	"time"
)

type AgentSessionProcessorConfig struct {
	EmitThought          func(context.Context, ThoughtActivityInput) (string, error)
	RecordSLAError       func(context.Context, string) error
	RunCreated           func(context.Context, AgentSessionEvent, func(context.Context, ThoughtActivityInput) (string, error)) error
	RunPrompted          func(context.Context, AgentSessionEvent) error
	FirstThoughtTimeout  time.Duration
	FirstThoughtFallback string
	After                func(time.Duration) <-chan time.Time
}

type AgentSessionProcessor struct {
	emitThought          func(context.Context, ThoughtActivityInput) (string, error)
	recordSLAError       func(context.Context, string) error
	runCreated           func(context.Context, AgentSessionEvent, func(context.Context, ThoughtActivityInput) (string, error)) error
	runPrompted          func(context.Context, AgentSessionEvent) error
	firstThoughtTimeout  time.Duration
	firstThoughtFallback string
	after                func(time.Duration) <-chan time.Time
}

func NewAgentSessionProcessor(config AgentSessionProcessorConfig) (*AgentSessionProcessor, error) {
	if config.EmitThought == nil {
		return nil, fmt.Errorf("emit thought callback is required")
	}
	if config.RecordSLAError == nil {
		return nil, fmt.Errorf("record SLA error callback is required")
	}
	if config.RunCreated == nil {
		return nil, fmt.Errorf("created event handler is required")
	}

	return &AgentSessionProcessor{
		emitThought:          config.EmitThought,
		recordSLAError:       config.RecordSLAError,
		runCreated:           config.RunCreated,
		runPrompted:          config.RunPrompted,
		firstThoughtTimeout:  config.FirstThoughtTimeout,
		firstThoughtFallback: config.FirstThoughtFallback,
		after:                config.After,
	}, nil
}

func (p *AgentSessionProcessor) ProcessEvent(ctx context.Context, event AgentSessionEvent) error {
	if p == nil {
		return fmt.Errorf("agent session processor is nil")
	}

	switch event.Action {
	case AgentSessionEventActionCreated:
		return p.processCreatedEvent(ctx, event)
	case AgentSessionEventActionPrompted:
		if p.runPrompted == nil {
			return nil
		}
		return p.runPrompted(ctx, event)
	default:
		return fmt.Errorf("%w: %q", ErrUnknownAgentSessionEventAction, event.Action)
	}
}

func (p *AgentSessionProcessor) processCreatedEvent(ctx context.Context, event AgentSessionEvent) error {
	firstThoughtObserved := make(chan struct{}, 1)
	observeFirstThought := func() {
		select {
		case firstThoughtObserved <- struct{}{}:
		default:
		}
	}

	emitThought := func(thoughtCtx context.Context, input ThoughtActivityInput) (string, error) {
		id, err := p.emitThought(thoughtCtx, input)
		if err == nil {
			observeFirstThought()
		}
		return id, err
	}

	slaErrCh := make(chan error, 1)
	go func() {
		slaErrCh <- EnforceFirstThoughtSLA(ctx, event, firstThoughtObserved, FirstThoughtSLAConfig{
			Timeout:        p.firstThoughtTimeout,
			FallbackBody:   p.firstThoughtFallback,
			EmitThought:    emitThought,
			RecordSLAError: p.recordSLAError,
			After:          p.after,
		})
	}()

	runErr := p.runCreated(ctx, event, emitThought)
	slaErr := <-slaErrCh
	if runErr != nil && slaErr != nil {
		return fmt.Errorf("created event run failed: %w; first-thought SLA failed: %v", runErr, slaErr)
	}
	if runErr != nil {
		return runErr
	}
	return slaErr
}
