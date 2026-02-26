package distributed

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	server "github.com/nats-io/nats-server/v2/server"
)

func TestNATSBusQueueConsumerRecoverySharedDurable(t *testing.T) {
	url, shutdown := startNATSServer(t)
	defer shutdown()

	bus, err := NewNATSBus(url, BusBackendOptions{Stream: "TASKS", Group: "workers", Durable: "task-consumers"})
	if err != nil {
		t.Fatalf("new nats bus: %v", err)
	}
	defer bus.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	env, err := NewEventEnvelope(EventTypeTaskDispatch, "client", "corr-nats-integration", map[string]string{"task": "1"})
	if err != nil {
		t.Fatalf("new envelope: %v", err)
	}
	if err := bus.Enqueue(ctx, "queue.tasks", env); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	msgsA, stopA, err := bus.ConsumeQueue(ctx, "queue.tasks", QueueConsumeOptions{Consumer: "worker-a"})
	if err != nil {
		t.Fatalf("consume worker-a: %v", err)
	}
	select {
	case <-msgsA:
	case <-ctx.Done():
		t.Fatalf("worker-a did not receive initial message: %v", ctx.Err())
	}
	stopA()

	msgsB, stopB, err := bus.ConsumeQueue(ctx, "queue.tasks", QueueConsumeOptions{Consumer: "worker-b"})
	if err != nil {
		t.Fatalf("consume worker-b: %v", err)
	}
	defer stopB()

	var recovered QueueMessage
	select {
	case recovered = <-msgsB:
	case <-ctx.Done():
		t.Fatalf("worker-b did not recover message: %v", ctx.Err())
	}
	if recovered.Event.CorrelationID != env.CorrelationID {
		t.Fatalf("expected correlation %q, got %q", env.CorrelationID, recovered.Event.CorrelationID)
	}
	if err := recovered.Ack(ctx); err != nil {
		t.Fatalf("ack recovered: %v", err)
	}
}

func startNATSServer(t *testing.T) (string, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	opts := &server.Options{
		Host:      "127.0.0.1",
		Port:      port,
		JetStream: true,
		StoreDir:  t.TempDir(),
	}
	s, err := server.NewServer(opts)
	if err != nil {
		t.Fatalf("new nats server: %v", err)
	}
	go s.Start()
	if !s.ReadyForConnections(5 * time.Second) {
		t.Fatalf("nats server not ready")
	}

	return fmt.Sprintf("nats://127.0.0.1:%d", port), func() {
		s.Shutdown()
		s.WaitForShutdown()
	}
}
