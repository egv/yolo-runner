package distributed

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/nats-io/nats.go"
)

type fakeNATSBusConnection struct {
	mu           sync.RWMutex
	handler      nats.MsgHandler
	unsubscribed int32
	closeCalls   int32
}

func (c *fakeNATSBusConnection) Subscribe(_ string, handler nats.MsgHandler) (natsBusSubscription, error) {
	c.mu.Lock()
	c.handler = handler
	c.mu.Unlock()
	return &fakeNATSBusSubscription{connection: c}, nil
}

func (c *fakeNATSBusConnection) Publish(_ string, _ []byte) error {
	return nil
}

func (c *fakeNATSBusConnection) Close() error {
	atomic.AddInt32(&c.closeCalls, 1)
	return nil
}

func (c *fakeNATSBusConnection) emit(raw []byte) {
	c.mu.RLock()
	handler := c.handler
	c.mu.RUnlock()
	if handler == nil {
		return
	}
	handler(&nats.Msg{Data: raw})
}

type fakeNATSBusSubscription struct {
	connection *fakeNATSBusConnection
}

func (s *fakeNATSBusSubscription) Unsubscribe() error {
	atomic.AddInt32(&s.connection.unsubscribed, 1)
	return nil
}

func TestNATSBusSubscribeClosesOutputOnUnsubscribe(t *testing.T) {
	conn := &fakeNATSBusConnection{}
	bus := &NATSBus{conn: conn}
	out, unsubscribe, err := bus.Subscribe(context.Background(), "events")
	if err != nil {
		t.Fatalf("subscribe should return channel: %v", err)
	}
	if out == nil {
		t.Fatalf("expected output channel")
	}

	unsubscribe()
	select {
	case _, ok := <-out:
		if ok {
			t.Fatalf("expected output channel to close after unsubscribe")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("expected output channel to close after unsubscribe")
	}

	payload, err := json.Marshal(TaskResultPayload{
		CorrelationID: "corr",
		Result:        contracts.RunnerResult{Status: contracts.RunnerResultCompleted},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	env, err := NewEventEnvelope(EventTypeTaskResult, "executor", "corr", json.RawMessage(payload))
	if err != nil {
		t.Fatalf("build envelope: %v", err)
	}
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}

	defer func() {
		if rec := recover(); rec != nil {
			t.Fatalf("unexpected panic when callback runs after unsubscribe: %v", rec)
		}
	}()
	conn.emit(raw)

	if atomic.LoadInt32(&conn.unsubscribed) != 1 {
		t.Fatalf("expected unsubscribe to be called exactly once, got %d", atomic.LoadInt32(&conn.unsubscribed))
	}
}

func TestNATSBusSubscribeClosesOutputOnContextCancel(t *testing.T) {
	conn := &fakeNATSBusConnection{}
	bus := &NATSBus{conn: conn}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out, _, err := bus.Subscribe(ctx, "events")
	if err != nil {
		t.Fatalf("subscribe should return channel: %v", err)
	}

	cancel()
	select {
	case _, ok := <-out:
		if ok {
			t.Fatalf("expected output channel to close after context cancel")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("expected output channel to close after context cancel")
	}

	if atomic.LoadInt32(&conn.unsubscribed) != 1 {
		t.Fatalf("expected callback unsubscribe on context cancel, got %d", atomic.LoadInt32(&conn.unsubscribed))
	}
}

func TestNATSBusPublishAcceptsNilContext(t *testing.T) {
	conn := &fakeNATSBusConnection{}
	bus := &NATSBus{conn: conn}

	env, err := NewEventEnvelope(EventTypeTaskResult, "executor", "corr", TaskResultPayload{
		CorrelationID: "corr",
		Result:        contracts.RunnerResult{Status: contracts.RunnerResultCompleted},
	})
	if err != nil {
		t.Fatalf("build envelope: %v", err)
	}

	defer func() {
		if rec := recover(); rec != nil {
			t.Fatalf("publish should not panic with nil context: %v", rec)
		}
	}()
	if err := bus.Publish(nil, "events", env); err != nil {
		t.Fatalf("publish should succeed with nil context: %v", err)
	}
}
