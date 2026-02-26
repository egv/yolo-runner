package distributed

import (
	"context"
	"encoding/json"
	"fmt"
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
	js           natsJetStream
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

func (c *fakeNATSBusConnection) JetStream(_ ...nats.JSOpt) (natsJetStream, error) {
	if c.js == nil {
		c.js = fakeNATSJetStream{}
	}
	return c.js, nil
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

type fakeNATSJetStream struct{}

func (fakeNATSJetStream) Publish(_ string, _ []byte, _ ...nats.PubOpt) (*nats.PubAck, error) {
	return &nats.PubAck{}, nil
}

func (fakeNATSJetStream) StreamInfo(_ string, _ ...nats.JSOpt) (*nats.StreamInfo, error) {
	return &nats.StreamInfo{}, nil
}

func (fakeNATSJetStream) AddStream(_ *nats.StreamConfig, _ ...nats.JSOpt) (*nats.StreamInfo, error) {
	return &nats.StreamInfo{}, nil
}

func (fakeNATSJetStream) AddConsumer(_ string, _ *nats.ConsumerConfig, _ ...nats.JSOpt) (*nats.ConsumerInfo, error) {
	return &nats.ConsumerInfo{}, nil
}

func (fakeNATSJetStream) PullSubscribe(_ string, _ string, _ ...nats.SubOpt) (natsPullSubscription, error) {
	return fakeNATSPullSubscription{}, nil
}

type fakeNATSPullSubscription struct{}

func (fakeNATSPullSubscription) Fetch(_ int, _ ...nats.PullOpt) ([]*nats.Msg, error) {
	return nil, nats.ErrTimeout
}

func (fakeNATSPullSubscription) Unsubscribe() error { return nil }

type ackErrorJetStream struct {
	msg *nats.Msg
}

func (j *ackErrorJetStream) Publish(_ string, _ []byte, _ ...nats.PubOpt) (*nats.PubAck, error) {
	return &nats.PubAck{}, nil
}

func (j *ackErrorJetStream) StreamInfo(_ string, _ ...nats.JSOpt) (*nats.StreamInfo, error) {
	return nil, fmt.Errorf("not found")
}

func (j *ackErrorJetStream) AddStream(_ *nats.StreamConfig, _ ...nats.JSOpt) (*nats.StreamInfo, error) {
	return &nats.StreamInfo{}, nil
}

func (j *ackErrorJetStream) AddConsumer(_ string, _ *nats.ConsumerConfig, _ ...nats.JSOpt) (*nats.ConsumerInfo, error) {
	return &nats.ConsumerInfo{}, nil
}

func (j *ackErrorJetStream) PullSubscribe(_ string, _ string, _ ...nats.SubOpt) (natsPullSubscription, error) {
	return &ackErrorPullSub{msg: j.msg}, nil
}

type ackErrorPullSub struct {
	msg  *nats.Msg
	sent bool
}

func (s *ackErrorPullSub) Fetch(_ int, _ ...nats.PullOpt) ([]*nats.Msg, error) {
	if s.sent {
		return nil, nats.ErrTimeout
	}
	s.sent = true
	return []*nats.Msg{s.msg}, nil
}

func (s *ackErrorPullSub) Unsubscribe() error { return nil }

type fakeQueueJetStream struct {
	mu       sync.Mutex
	seq      int
	queue    []*nats.Msg
	inflight map[string]*nats.Msg
	owner    map[string]string
}

func (j *fakeQueueJetStream) Publish(_ string, data []byte, _ ...nats.PubOpt) (*nats.PubAck, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.seq++
	id := fmt.Sprintf("id-%d", j.seq)
	j.queue = append(j.queue, &nats.Msg{Data: data, Reply: id})
	return &nats.PubAck{}, nil
}

func (j *fakeQueueJetStream) StreamInfo(_ string, _ ...nats.JSOpt) (*nats.StreamInfo, error) {
	return nil, fmt.Errorf("not found")
}

func (j *fakeQueueJetStream) AddStream(_ *nats.StreamConfig, _ ...nats.JSOpt) (*nats.StreamInfo, error) {
	return &nats.StreamInfo{}, nil
}

func (j *fakeQueueJetStream) AddConsumer(_ string, _ *nats.ConsumerConfig, _ ...nats.JSOpt) (*nats.ConsumerInfo, error) {
	return &nats.ConsumerInfo{}, nil
}

func (j *fakeQueueJetStream) PullSubscribe(_ string, durable string, _ ...nats.SubOpt) (natsPullSubscription, error) {
	return &fakeQueuePullSub{js: j, durable: durable}, nil
}

type fakeQueuePullSub struct {
	js      *fakeQueueJetStream
	durable string
}

func (s *fakeQueuePullSub) Fetch(_ int, _ ...nats.PullOpt) ([]*nats.Msg, error) {
	s.js.mu.Lock()
	defer s.js.mu.Unlock()
	if s.js.inflight == nil {
		s.js.inflight = map[string]*nats.Msg{}
	}
	if s.js.owner == nil {
		s.js.owner = map[string]string{}
	}
	if len(s.js.queue) > 0 {
		msg := s.js.queue[0]
		s.js.queue = s.js.queue[1:]
		s.js.inflight[msg.Reply] = msg
		s.js.owner[msg.Reply] = s.durable
		return []*nats.Msg{msg}, nil
	}
	for id, msg := range s.js.inflight {
		if s.js.owner[id] != s.durable {
			s.js.owner[id] = s.durable
			return []*nats.Msg{msg}, nil
		}
	}
	return nil, nats.ErrTimeout
}

func (s *fakeQueuePullSub) Unsubscribe() error { return nil }

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

func TestNATSBusQueueAckNackErrorsPropagate(t *testing.T) {
	env, err := NewEventEnvelope(EventTypeTaskDispatch, "client", "corr-nats", map[string]string{"task": "1"})
	if err != nil {
		t.Fatalf("new envelope: %v", err)
	}
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	js := &ackErrorJetStream{msg: &nats.Msg{Data: raw}}
	bus := &NATSBus{
		conn:    &fakeNATSBusConnection{},
		js:      js,
		options: BusBackendOptions{Stream: "tasks", Group: "workers", Durable: "worker"},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	msgsA, stopA, err := bus.ConsumeQueue(ctx, "queue.tasks", QueueConsumeOptions{Consumer: "worker-a"})
	if err != nil {
		t.Fatalf("consume worker-a: %v", err)
	}
	var first QueueMessage
	select {
	case first = <-msgsA:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for queue message")
	}
	if err := first.Nack(ctx); err == nil {
		t.Fatalf("expected nack error for unbound fake nats message")
	}
	if err := first.Ack(ctx); err == nil {
		t.Fatalf("expected ack error for unbound fake nats message")
	}
	stopA()
}
