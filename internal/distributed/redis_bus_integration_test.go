package distributed

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"testing"
	"time"
)

func TestRedisBusQueueConsumerRecoveryRealServer(t *testing.T) {
	addr := startRedisServer(t)

	bus, err := NewRedisBus("redis://"+addr, BusBackendOptions{Stream: "tasks-stream", Group: "workers"})
	if err != nil {
		t.Fatalf("new redis bus: %v", err)
	}
	defer bus.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	env, err := NewEventEnvelope(EventTypeTaskDispatch, "client", "corr-redis-integration", map[string]string{"task": "1"})
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
		t.Fatalf("worker-a did not receive first message: %v", ctx.Err())
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
		t.Fatalf("worker-b did not recover pending message: %v", ctx.Err())
	}
	if recovered.Event.CorrelationID != env.CorrelationID {
		t.Fatalf("expected correlation %q, got %q", env.CorrelationID, recovered.Event.CorrelationID)
	}
	if err := recovered.Ack(ctx); err != nil {
		t.Fatalf("ack recovered: %v", err)
	}
}

func startRedisServer(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("redis-server"); err != nil {
		t.Skip("redis-server binary is required for Redis integration tests")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen ephemeral port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	dir := t.TempDir()
	cmd := exec.Command("redis-server",
		"--port", fmt.Sprintf("%d", port),
		"--save", "",
		"--appendonly", "no",
		"--dir", dir,
	)
	cmd.Dir = dir
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		t.Fatalf("start redis-server: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 150*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return addr
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("redis-server did not become ready at %s", addr)
	return ""
}

func TestRedisBusQueueNackRedeliveryRealServer(t *testing.T) {
	addr := startRedisServer(t)

	bus, err := NewRedisBus("redis://"+addr, BusBackendOptions{Stream: "tasks-stream", Group: "workers"})
	if err != nil {
		t.Fatalf("new redis bus: %v", err)
	}
	defer bus.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	env, err := NewEventEnvelope(EventTypeTaskDispatch, "client", "corr-redis-nack", map[string]string{"task": "2"})
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
	case <-ctx.Done():
		t.Fatalf("first delivery timeout: %v", ctx.Err())
	}
	if err := first.Nack(ctx); err != nil {
		t.Fatalf("nack: %v", err)
	}

	var redelivered QueueMessage
	select {
	case redelivered = <-msgs:
	case <-ctx.Done():
		t.Fatalf("redelivery timeout: %v", ctx.Err())
	}
	if redelivered.Event.CorrelationID != env.CorrelationID {
		t.Fatalf("expected correlation %q, got %q", env.CorrelationID, redelivered.Event.CorrelationID)
	}
	if err := redelivered.Ack(ctx); err != nil {
		t.Fatalf("ack: %v", err)
	}
}
