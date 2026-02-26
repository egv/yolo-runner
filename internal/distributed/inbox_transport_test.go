package distributed

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

type flakyPublishBus struct {
	Bus
	mu    sync.Mutex
	fails int
}

func (b *flakyPublishBus) Publish(ctx context.Context, subject string, event EventEnvelope) error {
	b.mu.Lock()
	if b.fails > 0 {
		b.fails--
		b.mu.Unlock()
		return errors.New("temporary publish failure")
	}
	b.mu.Unlock()
	return b.Bus.Publish(ctx, subject, event)
}

func TestInboxTransportPublishTaskStatusUpdateCommandWithRetry(t *testing.T) {
	base := NewMemoryBus()
	bus := &flakyPublishBus{Bus: base, fails: 1}
	transport := NewInboxTransport(bus, DefaultEventSubjects("unit"))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	commandCh, unsubscribe, err := base.Subscribe(ctx, DefaultEventSubjects("unit").TaskStatusUpdate)
	if err != nil {
		t.Fatalf("subscribe command subject: %v", err)
	}
	defer unsubscribe()

	commandID, err := transport.PublishTaskStatusUpdateCommandWithRetry(ctx, TaskStatusUpdateCommandPayload{
		CommandID: "cmd-retry-1",
		TaskID:    "task-1",
		Status:    contracts.TaskStatusClosed,
	}, 2, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("publish with retry: %v", err)
	}
	if commandID != "cmd-retry-1" {
		t.Fatalf("expected command id to be stable, got %q", commandID)
	}

	select {
	case evt := <-commandCh:
		if evt.CorrelationID != "cmd-retry-1" {
			t.Fatalf("expected correlation id cmd-retry-1, got %q", evt.CorrelationID)
		}
	case <-ctx.Done():
		t.Fatalf("timed out waiting for retried publish: %v", ctx.Err())
	}
}

func TestInboxTransportSubscribeTaskStatusUpdateAcksRoutesAckAndReject(t *testing.T) {
	bus := NewMemoryBus()
	subjects := DefaultEventSubjects("unit")
	transport := NewInboxTransport(bus, subjects)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	ackStream, unsubscribe, err := transport.SubscribeTaskStatusUpdateAcks(ctx)
	if err != nil {
		t.Fatalf("subscribe status acks: %v", err)
	}
	defer unsubscribe()

	if err := bus.Publish(ctx, subjects.TaskStatusUpdateAck, mustEnvelope(t, EventTypeTaskStatusAck, TaskStatusUpdateAckPayload{
		TaskStatusUpdateResultPayload: TaskStatusUpdateResultPayload{
			CommandID: "cmd-ok",
			TaskID:    "task-1",
			Status:    contracts.TaskStatusClosed,
			Success:   true,
			Result:    "ok",
		},
	})); err != nil {
		t.Fatalf("publish ack: %v", err)
	}

	if err := bus.Publish(ctx, subjects.TaskStatusUpdateReject, mustEnvelope(t, EventTypeTaskStatusReject, TaskStatusUpdateRejectPayload{
		TaskStatusUpdateResultPayload: TaskStatusUpdateResultPayload{
			CommandID: "cmd-fail",
			TaskID:    "task-2",
			Status:    contracts.TaskStatusOpen,
		},
		Reason: "version conflict",
	})); err != nil {
		t.Fatalf("publish reject: %v", err)
	}

	seen := map[string]TaskStatusUpdateAckPayload{}
	for len(seen) < 2 {
		select {
		case ack := <-ackStream:
			seen[ack.CommandID] = ack
		case <-ctx.Done():
			t.Fatalf("timed out waiting for routed acks: %v", ctx.Err())
		}
	}
	if !seen["cmd-ok"].Success {
		t.Fatalf("expected cmd-ok to be success ack")
	}
	if seen["cmd-fail"].Success {
		t.Fatalf("expected cmd-fail to be normalized as failure ack")
	}
	if seen["cmd-fail"].Reason == "" {
		t.Fatalf("expected cmd-fail to include reason")
	}
}

func TestInboxTransportPublishTaskGraphSnapshotNormalizesLegacyPayload(t *testing.T) {
	bus := NewMemoryBus()
	subjects := DefaultEventSubjects("unit")
	transport := NewInboxTransport(bus, subjects)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	stream, unsubscribe, err := bus.Subscribe(ctx, subjects.TaskGraphSnapshot)
	if err != nil {
		t.Fatalf("subscribe snapshots: %v", err)
	}
	defer unsubscribe()

	err = transport.PublishTaskGraphSnapshot(ctx, TaskGraphSnapshotPayload{
		Backend: "tk",
		RootID:  "root-1",
		TaskTree: contracts.TaskTree{
			Root: contracts.Task{ID: "root-1", Status: contracts.TaskStatusOpen},
			Tasks: map[string]contracts.Task{
				"root-1": {ID: "root-1", Status: contracts.TaskStatusOpen},
			},
		},
	})
	if err != nil {
		t.Fatalf("publish legacy snapshot: %v", err)
	}

	select {
	case evt := <-stream:
		payload := TaskGraphSnapshotPayload{}
		if err := json.Unmarshal(evt.Payload, &payload); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		if payload.SchemaVersion != InboxSchemaVersionV1 {
			t.Fatalf("expected inbox schema v1, got %q", payload.SchemaVersion)
		}
		if len(payload.Graphs) != 1 {
			t.Fatalf("expected one normalized graph, got %d", len(payload.Graphs))
		}
	case <-ctx.Done():
		t.Fatalf("timed out waiting for snapshot: %v", ctx.Err())
	}
}

func mustEnvelope(t *testing.T, typ EventType, payload any) EventEnvelope {
	t.Helper()
	env, err := NewEventEnvelope(typ, "test", "", payload)
	if err != nil {
		t.Fatalf("new envelope: %v", err)
	}
	return env
}
