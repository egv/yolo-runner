package distributed

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type InboxTransport struct {
	bus      Bus
	subjects EventSubjects
}

func NewInboxTransport(bus Bus, subjects EventSubjects) *InboxTransport {
	if subjects.Register == "" {
		subjects = DefaultEventSubjects("yolo")
	}
	return &InboxTransport{bus: bus, subjects: subjects}
}

func (t *InboxTransport) PublishTaskGraphSnapshot(ctx context.Context, payload TaskGraphSnapshotPayload) error {
	if t == nil || t.bus == nil {
		return fmt.Errorf("inbox transport bus is required")
	}
	graphs, err := payload.NormalizeGraphs()
	if err != nil {
		return err
	}
	if payload.SchemaVersion == "" {
		payload.SchemaVersion = InboxSchemaVersionV1
	}
	if len(payload.Graphs) == 0 {
		payload.Graphs = graphs
	}
	env, err := NewEventEnvelope(EventTypeTaskGraphSnapshot, "inbox", "", payload)
	if err != nil {
		return err
	}
	return t.bus.Publish(ctx, t.subjects.TaskGraphSnapshot, env)
}

func (t *InboxTransport) PublishTaskGraphDiff(ctx context.Context, payload TaskGraphDiffPayload) error {
	if t == nil || t.bus == nil {
		return fmt.Errorf("inbox transport bus is required")
	}
	if _, err := normalizeInboxSchemaVersion(payload.SchemaVersion); err != nil {
		return err
	}
	if payload.SchemaVersion == "" {
		payload.SchemaVersion = InboxSchemaVersionV1
	}
	env, err := NewEventEnvelope(EventTypeTaskGraphDiff, "inbox", "", payload)
	if err != nil {
		return err
	}
	return t.bus.Publish(ctx, t.subjects.TaskGraphDiff, env)
}

func (t *InboxTransport) PublishTaskStatusUpdateCommand(ctx context.Context, command TaskStatusUpdateCommandPayload) (string, error) {
	if t == nil || t.bus == nil {
		return "", fmt.Errorf("inbox transport bus is required")
	}
	command.CommandID = strings.TrimSpace(command.CommandID)
	if command.CommandID == "" {
		command.CommandID = nextEventID()
	}
	env, err := NewEventEnvelope(EventTypeTaskStatusCommand, "inbox", command.CommandID, command)
	if err != nil {
		return "", err
	}
	env.CorrelationID = command.CommandID
	return command.CommandID, t.bus.Publish(ctx, t.subjects.TaskStatusUpdate, env)
}

func (t *InboxTransport) PublishTaskStatusUpdateCommandWithRetry(ctx context.Context, command TaskStatusUpdateCommandPayload, maxAttempts int, retryDelay time.Duration) (string, error) {
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	commandID := strings.TrimSpace(command.CommandID)
	var err error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		command.CommandID = commandID
		commandID, err = t.PublishTaskStatusUpdateCommand(ctx, command)
		if err == nil {
			return commandID, nil
		}
		if attempt < maxAttempts && retryDelay > 0 {
			select {
			case <-ctx.Done():
				return commandID, ctx.Err()
			case <-time.After(retryDelay):
			}
		}
	}
	return commandID, err
}

func (t *InboxTransport) SubscribeTaskStatusUpdateAcks(ctx context.Context) (<-chan TaskStatusUpdateAckPayload, func(), error) {
	if t == nil || t.bus == nil {
		return nil, nil, fmt.Errorf("inbox transport bus is required")
	}
	ackCh, unsubAck, err := t.bus.Subscribe(ctx, t.subjects.TaskStatusUpdateAck)
	if err != nil {
		return nil, nil, err
	}
	rejectCh, unsubReject, err := t.bus.Subscribe(ctx, t.subjects.TaskStatusUpdateReject)
	if err != nil {
		unsubAck()
		return nil, nil, err
	}
	out := make(chan TaskStatusUpdateAckPayload, 32)
	stop := make(chan struct{})
	unsubscribe := func() {
		select {
		case <-stop:
			return
		default:
			close(stop)
		}
		unsubAck()
		unsubReject()
	}
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case raw, ok := <-ackCh:
				if !ok {
					ackCh = nil
					if rejectCh == nil {
						return
					}
					continue
				}
				if raw.Type != EventTypeTaskStatusAck || len(raw.Payload) == 0 {
					continue
				}
				var ack TaskStatusUpdateAckPayload
				if err := json.Unmarshal(raw.Payload, &ack); err != nil {
					continue
				}
				select {
				case out <- ack:
				default:
				}
			case raw, ok := <-rejectCh:
				if !ok {
					rejectCh = nil
					if ackCh == nil {
						return
					}
					continue
				}
				if raw.Type != EventTypeTaskStatusReject || len(raw.Payload) == 0 {
					continue
				}
				var reject TaskStatusUpdateRejectPayload
				if err := json.Unmarshal(raw.Payload, &reject); err != nil {
					continue
				}
				select {
				case out <- TaskStatusUpdateAckPayload{TaskStatusUpdateResultPayload: TaskStatusUpdateResultPayload{
					CommandID: reject.CommandID,
					TaskID:    reject.TaskID,
					Status:    reject.Status,
					Backends:  reject.Backends,
					Versions:  reject.Versions,
					Result:    "error",
					Message:   reject.Reason,
					Success:   false,
					Reason:    reject.Reason,
				}}:
				default:
				}
			}
		}
	}()
	return out, unsubscribe, nil
}
