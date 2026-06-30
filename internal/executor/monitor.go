package executor

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

type MonitorEventContext struct {
	TaskID    string
	TaskTitle string
	WorkerID  string
	ClonePath string
	QueuePos  int
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

	request.OnProgress = func(progress contracts.RunnerProgress) {
		eventTime := progress.Timestamp
		if eventTime.IsZero() {
			eventTime = time.Now().UTC()
		}
		progressMu.Lock()
		lastOutputAt = eventTime
		warned = false
		progressMu.Unlock()
		event := contracts.Event{
			Type:      eventTypeForRunnerProgress(progress.Type),
			TaskID:    eventContext.TaskID,
			TaskTitle: eventContext.TaskTitle,
			WorkerID:  eventContext.WorkerID,
			ClonePath: eventContext.ClonePath,
			QueuePos:  eventContext.QueuePos,
			Message:   progress.Message,
			Metadata:  progress.Metadata,
			Timestamp: eventTime,
		}
		if event.Type == contracts.EventTypeAgentBlocked {
			event.Reason = contracts.BlockReason(strings.TrimSpace(progress.Metadata["reason"]))
			event.Detail = strings.TrimSpace(progress.Metadata["detail"])
		}
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

				_ = emitMonitorEvent(ctx, events, contracts.Event{
					Type:      contracts.EventTypeRunnerHeartbeat,
					TaskID:    eventContext.TaskID,
					TaskTitle: eventContext.TaskTitle,
					WorkerID:  eventContext.WorkerID,
					ClonePath: eventContext.ClonePath,
					QueuePos:  eventContext.QueuePos,
					Message:   "alive",
					Metadata:  map[string]string{"last_output_age": elapsed.Round(time.Second).String()},
					Timestamp: now.UTC(),
				})

				if elapsed >= warningAfter && !alreadyWarned {
					_ = emitMonitorEvent(ctx, events, contracts.Event{
						Type:      contracts.EventTypeRunnerWarning,
						TaskID:    eventContext.TaskID,
						TaskTitle: eventContext.TaskTitle,
						WorkerID:  eventContext.WorkerID,
						ClonePath: eventContext.ClonePath,
						QueuePos:  eventContext.QueuePos,
						Message:   "no output threshold exceeded",
						Metadata:  map[string]string{"last_output_age": elapsed.Round(time.Second).String()},
						Timestamp: now.UTC(),
					})
				}
			}
		}
	}()

	result, err := runner.Run(ctx, request)
	cancel()
	return result, err
}

func emitMonitorEvent(ctx context.Context, events contracts.EventSink, event contracts.Event) error {
	if events == nil {
		return nil
	}
	return events.Emit(ctx, event)
}

func eventTypeForRunnerProgress(progressType string) contracts.EventType {
	switch strings.TrimSpace(progressType) {
	case "runner_cmd_started":
		return contracts.EventTypeRunnerCommandStarted
	case "runner_cmd_finished":
		return contracts.EventTypeRunnerCommandFinished
	case "runner_output":
		return contracts.EventTypeRunnerOutput
	case "runner_warning":
		return contracts.EventTypeRunnerWarning
	case "agent_text":
		return contracts.EventTypeAgentText
	case "agent_progress":
		return contracts.EventTypeAgentProgress
	case "agent_blocked":
		return contracts.EventTypeAgentBlocked
	case "command_run":
		return contracts.EventTypeCommandRun
	case "tool_invoked":
		return contracts.EventTypeToolInvoked
	case "token_usage":
		return contracts.EventTypeTokenUsage
	default:
		return contracts.EventTypeRunnerProgress
	}
}
