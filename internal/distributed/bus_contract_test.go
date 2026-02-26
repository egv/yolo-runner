package distributed

import (
	"context"
	"testing"
	"time"
)

func TestEventEnvelopeIncludesSchemaCorrelationAndIdempotency(t *testing.T) {
	env, err := NewEventEnvelope(EventTypeTaskDispatch, "client", "", map[string]string{"task": "1"})
	if err != nil {
		t.Fatalf("new envelope: %v", err)
	}
	if env.SchemaVersion != EventSchemaVersionV1 {
		t.Fatalf("expected schema %q, got %q", EventSchemaVersionV1, env.SchemaVersion)
	}
	if env.CorrelationID == "" {
		t.Fatalf("expected non-empty correlation id")
	}
	if env.IdempotencyKey == "" {
		t.Fatalf("expected non-empty idempotency key")
	}
}

func TestMemoryBusQueueNackRedeliversAndAckCompletes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bus := NewMemoryBus()
	defer bus.Close()

	env, err := NewEventEnvelope(EventTypeTaskDispatch, "client", "corr-1", map[string]string{"task": "1"})
	if err != nil {
		t.Fatalf("new envelope: %v", err)
	}
	if err := bus.Enqueue(ctx, "queue.tasks", env); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	msgs, stop, err := bus.ConsumeQueue(ctx, "queue.tasks", QueueConsumeOptions{Consumer: "worker-a"})
	if err != nil {
		t.Fatalf("consume queue: %v", err)
	}
	defer stop()

	var first QueueMessage
	select {
	case first = <-msgs:
	case <-time.After(time.Second):
		t.Fatalf("timeout waiting first delivery")
	}
	if err := first.Nack(ctx); err != nil {
		t.Fatalf("nack: %v", err)
	}

	var second QueueMessage
	select {
	case second = <-msgs:
	case <-time.After(time.Second):
		t.Fatalf("timeout waiting redelivery")
	}
	if second.Event.CorrelationID != "corr-1" {
		t.Fatalf("expected same correlation id, got %q", second.Event.CorrelationID)
	}
	if err := second.Ack(ctx); err != nil {
		t.Fatalf("ack: %v", err)
	}
}

func TestMemoryBusRequestReply(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bus := NewMemoryBus()
	defer bus.Close()

	stop, err := bus.Respond(ctx, "service.echo", func(_ context.Context, req EventEnvelope) (EventEnvelope, error) {
		return NewEventEnvelope(EventTypeServiceResponse, "server", req.CorrelationID, map[string]string{"ok": "true"})
	})
	if err != nil {
		t.Fatalf("respond: %v", err)
	}
	defer stop()

	req, err := NewEventEnvelope(EventTypeServiceRequest, "client", "corr-2", map[string]string{"ping": "pong"})
	if err != nil {
		t.Fatalf("request envelope: %v", err)
	}
	resp, err := bus.Request(ctx, "service.echo", req, time.Second)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.CorrelationID != "corr-2" {
		t.Fatalf("expected correlation id to round-trip, got %q", resp.CorrelationID)
	}
}
