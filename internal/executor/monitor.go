package executor

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

type MonitorEventContext struct {
	TaskID      string
	TaskTitle   string
	WorkerID    string
	ClonePath   string
	QueuePos    int
	Attempt     int
	RetryCount  int
	MaxAttempts int
}

type MonitorOptions struct {
	HeartbeatInterval    time.Duration
	NoOutputWarningAfter time.Duration
}

func RunWithMonitoring(ctx context.Context, runner contracts.AgentRunner, events contracts.EventSink, request contracts.RunnerRequest, eventContext MonitorEventContext, options MonitorOptions) (contracts.RunnerResult, error) {
	heartbeatInterval := options.HeartbeatInterval
	if heartbeatInterval <= 0 {
		heartbeatInterval = 5 * time.Second
	}
	warningAfter := options.NoOutputWarningAfter
	if warningAfter <= 0 {
		warningAfter = 30 * time.Second
	}

	lastOutputAt := time.Now().UTC()
	warned := false
	var progressMu sync.Mutex
	eventContext = eventContext.withRequestMetadata(request.Metadata)

	request.OnProgress = func(progress contracts.RunnerProgress) {
		eventTime := progress.Timestamp
		if eventTime.IsZero() {
			eventTime = time.Now().UTC()
		}
		progressMu.Lock()
		lastOutputAt = eventTime
		warned = false
		progressMu.Unlock()
		eventType := eventTypeForRunnerProgress(progress.Type)
		if eventType == "" {
			return
		}
		event := buildAgentEvent(
			eventType,
			eventContext.TaskID,
			eventContext.TaskTitle,
			eventContext.WorkerID,
			eventContext.ClonePath,
			eventContext.QueuePos,
			progress.Message,
			progress.Metadata,
			eventTime,
		)
		applyMonitorAttemptFields(&event, eventContext)
		_ = emitMonitorEvent(ctx, events, event)
	}

	monitorCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		ticker := time.NewTicker(heartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-monitorCtx.Done():
				return
			case now := <-ticker.C:
				progressMu.Lock()
				elapsed := now.Sub(lastOutputAt)
				alreadyWarned := warned
				if elapsed >= warningAfter {
					warned = true
				}
				progressMu.Unlock()

				event := buildAgentEvent(
					contracts.EventTypeAgentHeartbeat,
					eventContext.TaskID,
					eventContext.TaskTitle,
					eventContext.WorkerID,
					eventContext.ClonePath,
					eventContext.QueuePos,
					"alive",
					map[string]string{"last_output_age": elapsed.Round(time.Second).String()},
					now.UTC(),
				)
				applyMonitorAttemptFields(&event, eventContext)
				_ = emitMonitorEvent(ctx, events, event)

				if elapsed >= warningAfter && !alreadyWarned {
					event := buildAgentEvent(
						contracts.EventTypeAgentBlocked,
						eventContext.TaskID,
						eventContext.TaskTitle,
						eventContext.WorkerID,
						eventContext.ClonePath,
						eventContext.QueuePos,
						"no output threshold exceeded",
						map[string]string{"last_output_age": elapsed.Round(time.Second).String(), "reason": "no output threshold exceeded"},
						now.UTC(),
					)
					applyMonitorAttemptFields(&event, eventContext)
					_ = emitMonitorEvent(ctx, events, event)
				}
			}
		}
	}()

	result, err := runner.Run(ctx, request)
	cancel()
	return result, err
}

func (c MonitorEventContext) withRequestMetadata(metadata map[string]string) MonitorEventContext {
	if c.Attempt == 0 {
		c.Attempt = metadataInt(metadata, "attempt", "review_attempt", "landing_attempt")
	}
	if c.RetryCount == 0 {
		c.RetryCount = metadataInt(metadata, "retry_count", "review_retry_count", "completion_retry_count")
	}
	if c.MaxAttempts == 0 {
		c.MaxAttempts = metadataInt(metadata, "max_attempts")
	}
	return c
}

func applyMonitorAttemptFields(event *contracts.Event, eventContext MonitorEventContext) {
	if event == nil {
		return
	}
	if event.Attempt == 0 {
		event.Attempt = positiveOrZero(eventContext.Attempt)
	}
	if event.RetryCount == 0 {
		event.RetryCount = positiveOrZero(eventContext.RetryCount)
	}
	if event.MaxAttempts == 0 {
		event.MaxAttempts = positiveOrZero(eventContext.MaxAttempts)
	}
}

func emitMonitorEvent(ctx context.Context, events contracts.EventSink, event contracts.Event) error {
	if events == nil {
		return nil
	}
	return events.Emit(ctx, event)
}

func eventTypeForRunnerProgress(progressType string) contracts.EventType {
	switch contracts.EventType(strings.TrimSpace(progressType)) {
	case contracts.EventTypeAgentText:
		return contracts.EventTypeAgentText
	case contracts.EventTypeAgentProgress:
		return contracts.EventTypeAgentProgress
	case contracts.EventTypeAgentBlocked:
		return contracts.EventTypeAgentBlocked
	case contracts.EventTypeCommandRun:
		return contracts.EventTypeCommandRun
	case contracts.EventTypeToolInvoked:
		return contracts.EventTypeToolInvoked
	case contracts.EventTypeTokenUsage:
		return contracts.EventTypeTokenUsage
	case contracts.EventTypeAgentFinished:
		return contracts.EventTypeAgentFinished
	case contracts.EventTypeAgentHeartbeat:
		return contracts.EventTypeAgentHeartbeat
	default:
		return contracts.EventTypeAgentProgress
	}
}
