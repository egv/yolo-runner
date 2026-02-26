package distributed

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

type fakeRedisPubSub struct {
	messages   chan *redis.Message
	closeCalls int32
}

func (p *fakeRedisPubSub) Channel(...redis.ChannelOption) <-chan *redis.Message {
	return p.messages
}

func (p *fakeRedisPubSub) Close() error {
	if atomic.CompareAndSwapInt32(&p.closeCalls, 0, 1) {
		close(p.messages)
	}
	return nil
}

type fakeRedisClient struct {
	pubSub   redisPubSub
	mu       sync.Mutex
	seq      int
	queue    []redis.XMessage
	inflight map[string]redis.XMessage
	owner    map[string]string
}

func (c *fakeRedisClient) Publish(_ context.Context, _ string, _ interface{}) *redis.IntCmd {
	return redis.NewIntResult(1, nil)
}

func (c *fakeRedisClient) Subscribe(_ context.Context, _ ...string) redisPubSub {
	if c.pubSub == nil {
		c.pubSub = &fakeRedisPubSub{messages: make(chan *redis.Message)}
	}
	return c.pubSub
}

func (c *fakeRedisClient) XAdd(_ context.Context, args *redis.XAddArgs) *redis.StringCmd {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.inflight == nil {
		c.inflight = map[string]redis.XMessage{}
	}
	if c.owner == nil {
		c.owner = map[string]string{}
	}
	c.seq++
	id := fmt.Sprintf("%d-0", c.seq)
	var raw any
	switch values := args.Values.(type) {
	case map[string]any:
		raw = values["event"]
	case []any:
		for idx := 0; idx+1 < len(values); idx += 2 {
			key, _ := values[idx].(string)
			if key == "event" {
				raw = values[idx+1]
				break
			}
		}
	}
	rawString, _ := raw.(string)
	if rawString == "" {
		rawBytes, _ := raw.([]byte)
		rawString = string(rawBytes)
	}
	if rawString == "" {
		fallback, _ := json.Marshal(EventEnvelope{Type: EventTypeTaskDispatch, CorrelationID: id})
		rawString = string(fallback)
	}
	c.queue = append(c.queue, redis.XMessage{
		ID: id,
		Values: map[string]any{
			"event": rawString,
		},
	})
	return redis.NewStringResult(id, nil)
}

func (c *fakeRedisClient) XGroupCreateMkStream(_ context.Context, _, _, _ string) *redis.StatusCmd {
	return redis.NewStatusResult("OK", nil)
}

func (c *fakeRedisClient) XReadGroup(_ context.Context, args *redis.XReadGroupArgs) *redis.XStreamSliceCmd {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.inflight == nil {
		c.inflight = map[string]redis.XMessage{}
	}
	if c.owner == nil {
		c.owner = map[string]string{}
	}
	if len(c.queue) > 0 {
		msg := c.queue[0]
		c.queue = c.queue[1:]
		c.inflight[msg.ID] = msg
		c.owner[msg.ID] = args.Consumer
		return redis.NewXStreamSliceCmdResult([]redis.XStream{{Stream: args.Streams[0], Messages: []redis.XMessage{msg}}}, nil)
	}
	for id, msg := range c.inflight {
		if c.owner[id] != args.Consumer {
			c.owner[id] = args.Consumer
			return redis.NewXStreamSliceCmdResult([]redis.XStream{{Stream: args.Streams[0], Messages: []redis.XMessage{msg}}}, nil)
		}
	}
	return redis.NewXStreamSliceCmdResult(nil, redis.Nil)
}

func (c *fakeRedisClient) XAutoClaim(_ context.Context, args *redis.XAutoClaimArgs) *redis.XAutoClaimCmd {
	c.mu.Lock()
	defer c.mu.Unlock()
	cmd := redis.NewXAutoClaimCmd(context.Background())
	if c.inflight == nil {
		c.inflight = map[string]redis.XMessage{}
	}
	if c.owner == nil {
		c.owner = map[string]string{}
	}
	for id, msg := range c.inflight {
		if c.owner[id] != args.Consumer {
			c.owner[id] = args.Consumer
			cmd.SetVal([]redis.XMessage{msg}, "0-0")
			return cmd
		}
	}
	cmd.SetVal(nil, "0-0")
	cmd.SetErr(redis.Nil)
	return cmd
}

func (c *fakeRedisClient) XPendingExt(_ context.Context, _ *redis.XPendingExtArgs) *redis.XPendingExtCmd {
	cmd := redis.NewXPendingExtCmd(context.Background())
	cmd.SetVal(nil)
	cmd.SetErr(redis.Nil)
	return cmd
}

func (c *fakeRedisClient) XClaim(_ context.Context, _ *redis.XClaimArgs) *redis.XMessageSliceCmd {
	return redis.NewXMessageSliceCmdResult(nil, redis.Nil)
}

func (c *fakeRedisClient) XAck(_ context.Context, _, _ string, ids ...string) *redis.IntCmd {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, id := range ids {
		delete(c.inflight, id)
		delete(c.owner, id)
	}
	return redis.NewIntResult(1, nil)
}

func (c *fakeRedisClient) Close() error {
	if c == nil || c.pubSub == nil {
		return nil
	}
	return c.pubSub.Close()
}

func TestRedisBusSubscribeReturnsUnsubscribeThatClosesUnderlyingSubscription(t *testing.T) {
	defer func() {
		if err := recover(); err != nil {
			t.Fatalf("unexpected panic: %v", err)
		}
	}()

	fakePubSub := &fakeRedisPubSub{messages: make(chan *redis.Message)}
	bus := &RedisBus{client: &fakeRedisClient{pubSub: fakePubSub}}

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
			t.Fatalf("expected unsubscribe to close output channel")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("expected output channel to close after unsubscribe")
	}

	if atomic.LoadInt32(&fakePubSub.closeCalls) != 1 {
		t.Fatalf("expected close called exactly once, got %d", atomic.LoadInt32(&fakePubSub.closeCalls))
	}

	unsubscribe()
	if atomic.LoadInt32(&fakePubSub.closeCalls) != 1 {
		t.Fatalf("expected close to be idempotent")
	}
}

func TestRedisBusSubscribeClosesUnderlyingSubscriptionOnContextCancel(t *testing.T) {
	fakePubSub := &fakeRedisPubSub{messages: make(chan *redis.Message)}
	bus := &RedisBus{client: &fakeRedisClient{pubSub: fakePubSub}}
	ctx, cancel := context.WithCancel(context.Background())

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
		t.Fatalf("expected output channel to close after cancel")
	}

	if atomic.LoadInt32(&fakePubSub.closeCalls) != 1 {
		t.Fatalf("expected pubsub close on context cancel, got %d", atomic.LoadInt32(&fakePubSub.closeCalls))
	}
}

func TestRedisBusQueueAckNack(t *testing.T) {
	client := &fakeRedisClient{}
	bus := &RedisBus{client: client, options: BusBackendOptions{Stream: "stream-jobs", Group: "workers"}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	env, err := NewEventEnvelope(EventTypeTaskDispatch, "client", "corr-redis", map[string]string{"task": "1"})
	if err != nil {
		t.Fatalf("new envelope: %v", err)
	}
	if err := bus.Enqueue(ctx, "queue.jobs", env); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	msgsA, stopA, err := bus.ConsumeQueue(ctx, "queue.jobs", QueueConsumeOptions{Consumer: "worker-a"})
	if err != nil {
		t.Fatalf("consume worker-a: %v", err)
	}
	first := <-msgsA
	if err := first.Nack(ctx); err != nil {
		t.Fatalf("nack: %v", err)
	}
	stopA()

	msgsB, stopB, err := bus.ConsumeQueue(ctx, "queue.jobs", QueueConsumeOptions{Consumer: "worker-b"})
	if err != nil {
		t.Fatalf("consume worker-b: %v", err)
	}
	defer stopB()
	redelivered := <-msgsB
	if redelivered.Event.CorrelationID != "corr-redis" {
		t.Fatalf("expected correlation corr-redis, got %q", redelivered.Event.CorrelationID)
	}
	if err := redelivered.Ack(ctx); err != nil {
		t.Fatalf("ack: %v", err)
	}
}
