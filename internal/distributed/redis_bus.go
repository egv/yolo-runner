package distributed

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/redis/go-redis/v9"
)

type redisPubSub interface {
	Channel(...redis.ChannelOption) <-chan *redis.Message
	Close() error
}

type redisClient interface {
	Publish(ctx context.Context, subject string, value interface{}) *redis.IntCmd
	Subscribe(ctx context.Context, channels ...string) redisPubSub
	Close() error
}

type RedisBus struct {
	client redisClient
}

func NewRedisBus(address string) (*RedisBus, error) {
	if address == "" {
		address = "redis://127.0.0.1:6379"
	}
	options, err := redis.ParseURL(address)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	client := redis.NewClient(options)
	return &RedisBus{client: &redisClientAdapter{Client: client}}, nil
}

func (b *RedisBus) Publish(ctx context.Context, subject string, event EventEnvelope) error {
	if ctx == nil {
		ctx = context.Background()
	}
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

type redisClientAdapter struct {
	*redis.Client
}

func (r *redisClientAdapter) Subscribe(ctx context.Context, channels ...string) redisPubSub {
	return r.Client.Subscribe(ctx, channels...)
}

func (r *redisClientAdapter) Publish(ctx context.Context, subject string, value interface{}) *redis.IntCmd {
	return r.Client.Publish(ctx, subject, value)
}

func (r *redisClientAdapter) Close() error {
	if r == nil || r.Client == nil {
		return nil
	}
	return r.Client.Close()
}
