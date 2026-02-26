package distributed

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/nats-io/nats.go"
)

type natsBusSubscription interface {
	Unsubscribe() error
}

type natsBusConnection interface {
	Publish(string, []byte) error
	Subscribe(string, nats.MsgHandler) (natsBusSubscription, error)
	Close() error
}

type NATSBus struct {
	conn natsBusConnection
}

func NewNATSBus(address string) (*NATSBus, error) {
	if address == "" {
		address = nats.DefaultURL
	}
	conn, err := nats.Connect(address)
	if err != nil {
		return nil, fmt.Errorf("connect nats: %w", err)
	}
	return &NATSBus{conn: &natsBusConnectionAdapter{conn}}, nil
}

func (b *NATSBus) Publish(ctx context.Context, subject string, event EventEnvelope) error {
	if ctx == nil {
		ctx = context.Background()
	}
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	return b.conn.Publish(subject, raw)
}

func (b *NATSBus) Subscribe(ctx context.Context, subject string) (<-chan EventEnvelope, func(), error) {
	if b == nil || b.conn == nil {
		return nil, nil, fmt.Errorf("nats bus is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	out := make(chan EventEnvelope, 32)
	var stopped int32
	var mu sync.RWMutex
	var unsubscribeOnce sync.Once
	var sub natsBusSubscription

	unsubscribe := func() {
		unsubscribeOnce.Do(func() {
			atomic.StoreInt32(&stopped, 1)
			if sub != nil {
				_ = sub.Unsubscribe()
			}
			mu.Lock()
			defer mu.Unlock()
			close(out)
		})
	}

	sub, err := b.conn.Subscribe(subject, func(msg *nats.Msg) {
		if atomic.LoadInt32(&stopped) == 1 {
			return
		}
		env, err := ParseEventEnvelope(msg.Data)
		if err != nil {
			return
		}
		mu.RLock()
		defer mu.RUnlock()
		if atomic.LoadInt32(&stopped) == 1 {
			return
		}
		select {
		case out <- env:
		default:
		}
	})
	if err != nil {
		return nil, nil, err
	}
	go func() {
		<-ctx.Done()
		unsubscribe()
	}()

	if ctx.Err() != nil {
		unsubscribe()
	}

	return out, unsubscribe, nil
}

func (b *NATSBus) Close() error {
	if b == nil || b.conn == nil {
		return nil
	}
	b.conn.Close()
	return nil
}

type natsBusConnectionAdapter struct {
	*nats.Conn
}

func (a *natsBusConnectionAdapter) Subscribe(subject string, handler nats.MsgHandler) (natsBusSubscription, error) {
	sub, err := a.Conn.Subscribe(subject, handler)
	if err != nil {
		return nil, err
	}
	return natsBusSubscriptionAdapter{sub}, nil
}

type natsBusSubscriptionAdapter struct {
	*nats.Subscription
}

func (a natsBusSubscriptionAdapter) Unsubscribe() error {
	return a.Subscription.Unsubscribe()
}

func (a *natsBusConnectionAdapter) Close() error {
	a.Conn.Close()
	return nil
}
