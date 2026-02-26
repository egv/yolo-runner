package distributed

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

type redisPubSub interface {
	Channel(...redis.ChannelOption) <-chan *redis.Message
	Close() error
}

type redisClient interface {
	Publish(ctx context.Context, subject string, value interface{}) *redis.IntCmd
	Subscribe(ctx context.Context, channels ...string) redisPubSub
	XAdd(ctx context.Context, args *redis.XAddArgs) *redis.StringCmd
	XGroupCreateMkStream(ctx context.Context, stream, group, start string) *redis.StatusCmd
	XReadGroup(ctx context.Context, args *redis.XReadGroupArgs) *redis.XStreamSliceCmd
	XAutoClaim(ctx context.Context, args *redis.XAutoClaimArgs) *redis.XAutoClaimCmd
	XPendingExt(ctx context.Context, args *redis.XPendingExtArgs) *redis.XPendingExtCmd
	XClaim(ctx context.Context, args *redis.XClaimArgs) *redis.XMessageSliceCmd
	XAck(ctx context.Context, stream, group string, ids ...string) *redis.IntCmd
	Close() error
}

type RedisBus struct {
	client  redisClient
	options BusBackendOptions
}

func NewRedisBus(address string, opts ...BusBackendOptions) (*RedisBus, error) {
	if address == "" {
		address = "redis://127.0.0.1:6379"
	}
	options, err := redis.ParseURL(address)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	client := redis.NewClient(options)
	bus := &RedisBus{client: &redisClientAdapter{Client: client}}
	if len(opts) > 0 {
		bus.options = opts[0]
	}
	return bus, nil
}

func (b *RedisBus) Publish(ctx context.Context, subject string, event EventEnvelope) error {
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	return b.client.Publish(ctx, subject, raw).Err()
}

func (b *RedisBus) Subscribe(ctx context.Context, subject string) (<-chan EventEnvelope, func(), error) {
	if b == nil || b.client == nil {
		return nil, nil, fmt.Errorf("redis bus is nil")
	}
	pubSub := b.client.Subscribe(ctx, subject)
	if pubSub == nil {
		return nil, nil, fmt.Errorf("subscribe failed")
	}
	rawCh := pubSub.Channel()
	out := make(chan EventEnvelope, 32)
	unsubscribeOnce := sync.Once{}
	stop := make(chan struct{})
	unsubscribe := func() {
		unsubscribeOnce.Do(func() {
			_ = pubSub.Close()
			close(stop)
		})
	}
	go func() {
		defer close(out)
		defer unsubscribe()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case rawMsg, ok := <-rawCh:
				if !ok {
					return
				}
				env, err := ParseEventEnvelope([]byte(rawMsg.Payload))
				if err != nil {
					continue
				}
				select {
				case out <- env:
				default:
				}
			}
		}
	}()
	return out, unsubscribe, nil
}

func (b *RedisBus) Close() error {
	if b == nil || b.client == nil {
		return nil
	}
	return b.client.Close()
}

func (b *RedisBus) Enqueue(ctx context.Context, queue string, event EventEnvelope) error {
	if b == nil || b.client == nil {
		return fmt.Errorf("redis bus is nil")
	}
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	stream := b.streamForQueue(queue)
	return b.client.XAdd(ctx, &redis.XAddArgs{
		Stream: stream,
		Values: map[string]any{
			"event": raw,
		},
	}).Err()
}

func (b *RedisBus) ConsumeQueue(ctx context.Context, queue string, opts QueueConsumeOptions) (<-chan QueueMessage, func(), error) {
	if b == nil || b.client == nil {
		return nil, nil, fmt.Errorf("redis bus is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	stream := b.streamForQueue(queue)
	group := opts.Group
	if group == "" {
		group = b.groupForQueue()
	}
	if group == "" {
		group = "workers"
	}
	consumer := opts.Consumer
	if consumer == "" {
		consumer = "consumer"
	}
	err := b.client.XGroupCreateMkStream(ctx, stream, group, "0").Err()
	if err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		return nil, nil, err
	}

	out := make(chan QueueMessage, 32)
	stop := make(chan struct{})
	var once sync.Once
	cancel := func() {
		once.Do(func() { close(stop) })
	}

	go func() {
		defer close(out)
		claimStart := "0-0"
		for {
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			default:
			}
			messages, nextClaimStart := b.claimPendingMessages(ctx, stream, group, consumer, claimStart)
			claimStart = nextClaimStart
			if len(messages) == 0 {
				streams, err := b.client.XReadGroup(ctx, &redis.XReadGroupArgs{
					Group:    group,
					Consumer: consumer,
					Streams:  []string{stream, ">"},
					Count:    1,
					Block:    500 * time.Millisecond,
				}).Result()
				if err == redis.Nil {
					continue
				}
				if err != nil {
					continue
				}
				for _, s := range streams {
					messages = append(messages, s.Messages...)
				}
			}
			for _, msg := range messages {
				env, ok := parseRedisQueueEnvelope(msg)
				if !ok {
					_ = b.client.XAck(ctx, stream, group, msg.ID).Err()
					continue
				}
				id := msg.ID
				queueMsg := QueueMessage{
					ID:    id,
					Event: env,
					ackFn: func(ctx context.Context) error {
						return b.client.XAck(ctx, stream, group, id).Err()
					},
					nackFn: func(ctx context.Context) error {
						if err := b.client.XAck(ctx, stream, group, id).Err(); err != nil {
							return err
						}
						return b.Enqueue(ctx, queue, env)
					},
				}
				select {
				case out <- queueMsg:
				case <-ctx.Done():
					return
				case <-stop:
					return
				}
			}
		}
	}()
	return out, cancel, nil
}

func (b *RedisBus) claimPendingMessages(ctx context.Context, stream, group, consumer, start string) ([]redis.XMessage, string) {
	if strings.TrimSpace(start) == "" {
		start = "0-0"
	}
	const minIdle = 200 * time.Millisecond
	msgs, next, err := b.client.XAutoClaim(ctx, &redis.XAutoClaimArgs{
		Stream:   stream,
		Group:    group,
		Consumer: consumer,
		MinIdle:  minIdle,
		Start:    start,
		Count:    1,
	}).Result()
	if err == nil {
		return msgs, next
	}
	pending, pendingErr := b.client.XPendingExt(ctx, &redis.XPendingExtArgs{
		Stream: stream,
		Group:  group,
		Idle:   minIdle,
		Start:  "-",
		End:    "+",
		Count:  1,
	}).Result()
	if pendingErr != nil || len(pending) == 0 {
		return nil, "0-0"
	}
	ids := make([]string, 0, len(pending))
	for _, item := range pending {
		if item.Consumer == consumer {
			continue
		}
		ids = append(ids, item.ID)
	}
	if len(ids) == 0 {
		return nil, "0-0"
	}
	claimed, claimErr := b.client.XClaim(ctx, &redis.XClaimArgs{
		Stream:   stream,
		Group:    group,
		Consumer: consumer,
		MinIdle:  minIdle,
		Messages: ids,
	}).Result()
	if claimErr != nil {
		return nil, "0-0"
	}
	return claimed, "0-0"
}

func (b *RedisBus) Request(ctx context.Context, subject string, request EventEnvelope, timeout time.Duration) (EventEnvelope, error) {
	if b == nil {
		return EventEnvelope{}, fmt.Errorf("redis bus is nil")
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	replySubject := subject + ".reply." + request.IdempotencyKey
	request.ReplyTo = replySubject
	replyCh, unsubscribe, err := b.Subscribe(ctx, replySubject)
	if err != nil {
		return EventEnvelope{}, err
	}
	defer unsubscribe()
	if err := b.Publish(ctx, subject, request); err != nil {
		return EventEnvelope{}, err
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		select {
		case <-waitCtx.Done():
			return EventEnvelope{}, waitCtx.Err()
		case resp, ok := <-replyCh:
			if !ok {
				return EventEnvelope{}, fmt.Errorf("request response channel closed")
			}
			if resp.CorrelationID != request.CorrelationID {
				continue
			}
			return resp, nil
		}
	}
}

func (b *RedisBus) Respond(ctx context.Context, subject string, handler RequestHandler) (func(), error) {
	if b == nil {
		return nil, fmt.Errorf("redis bus is nil")
	}
	if handler == nil {
		return nil, fmt.Errorf("request handler is required")
	}
	reqCh, unsubscribe, err := b.Subscribe(ctx, subject)
	if err != nil {
		return nil, err
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case req, ok := <-reqCh:
				if !ok {
					return
				}
				if req.ReplyTo == "" {
					continue
				}
				resp, err := handler(ctx, req)
				if err != nil {
					continue
				}
				resp.CorrelationID = req.CorrelationID
				resp.IdempotencyKey = req.IdempotencyKey
				_ = b.Publish(ctx, req.ReplyTo, resp)
			}
		}
	}()
	return unsubscribe, nil
}

func (b *RedisBus) streamForQueue(queue string) string {
	if b != nil && b.options.Stream != "" {
		return b.options.Stream
	}
	return queue
}

func (b *RedisBus) groupForQueue() string {
	if b == nil {
		return ""
	}
	return b.options.Group
}

func parseRedisQueueEnvelope(message redis.XMessage) (EventEnvelope, bool) {
	value, ok := message.Values["event"]
	if !ok {
		return EventEnvelope{}, false
	}
	switch typed := value.(type) {
	case string:
		env, err := ParseEventEnvelope([]byte(typed))
		return env, err == nil
	case []byte:
		env, err := ParseEventEnvelope(typed)
		return env, err == nil
	default:
		return EventEnvelope{}, false
	}
}

type redisClientAdapter struct {
	*redis.Client
}

func (r *redisClientAdapter) Subscribe(ctx context.Context, channels ...string) redisPubSub {
	return r.Client.Subscribe(ctx, channels...)
}

func (r *redisClientAdapter) Publish(ctx context.Context, subject string, value interface{}) *redis.IntCmd {
	return r.Client.Publish(ctx, subject, value)
}

func (r *redisClientAdapter) XAdd(ctx context.Context, args *redis.XAddArgs) *redis.StringCmd {
	return r.Client.XAdd(ctx, args)
}

func (r *redisClientAdapter) XGroupCreateMkStream(ctx context.Context, stream, group, start string) *redis.StatusCmd {
	return r.Client.XGroupCreateMkStream(ctx, stream, group, start)
}

func (r *redisClientAdapter) XReadGroup(ctx context.Context, args *redis.XReadGroupArgs) *redis.XStreamSliceCmd {
	return r.Client.XReadGroup(ctx, args)
}

func (r *redisClientAdapter) XAutoClaim(ctx context.Context, args *redis.XAutoClaimArgs) *redis.XAutoClaimCmd {
	return r.Client.XAutoClaim(ctx, args)
}

func (r *redisClientAdapter) XPendingExt(ctx context.Context, args *redis.XPendingExtArgs) *redis.XPendingExtCmd {
	return r.Client.XPendingExt(ctx, args)
}

func (r *redisClientAdapter) XClaim(ctx context.Context, args *redis.XClaimArgs) *redis.XMessageSliceCmd {
	return r.Client.XClaim(ctx, args)
}

func (r *redisClientAdapter) XAck(ctx context.Context, stream, group string, ids ...string) *redis.IntCmd {
	return r.Client.XAck(ctx, stream, group, ids...)
}

func (r *redisClientAdapter) Close() error {
	if r == nil || r.Client == nil {
		return nil
	}
	return r.Client.Close()
}
