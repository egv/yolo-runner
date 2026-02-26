package distributed

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"
)

type natsBusSubscription interface {
	Unsubscribe() error
}

type natsBusConnection interface {
	Publish(string, []byte) error
	Subscribe(string, nats.MsgHandler) (natsBusSubscription, error)
	JetStream(opts ...nats.JSOpt) (natsJetStream, error)
	Close() error
}

type natsPullSubscription interface {
	Fetch(batch int, opts ...nats.PullOpt) ([]*nats.Msg, error)
	Unsubscribe() error
}

type natsJetStream interface {
	Publish(subj string, data []byte, opts ...nats.PubOpt) (*nats.PubAck, error)
	StreamInfo(stream string, opts ...nats.JSOpt) (*nats.StreamInfo, error)
	AddStream(cfg *nats.StreamConfig, opts ...nats.JSOpt) (*nats.StreamInfo, error)
	AddConsumer(stream string, cfg *nats.ConsumerConfig, opts ...nats.JSOpt) (*nats.ConsumerInfo, error)
	PullSubscribe(subj, durable string, opts ...nats.SubOpt) (natsPullSubscription, error)
}

type NATSBus struct {
	conn    natsBusConnection
	js      natsJetStream
	options BusBackendOptions
}

func NewNATSBus(address string, opts ...BusBackendOptions) (*NATSBus, error) {
	if address == "" {
		address = nats.DefaultURL
	}
	conn, err := nats.Connect(address)
	if err != nil {
		return nil, fmt.Errorf("connect nats: %w", err)
	}
	adapter := &natsBusConnectionAdapter{conn}
	js, err := adapter.JetStream()
	if err != nil {
		adapter.Close()
		return nil, fmt.Errorf("connect nats jetstream: %w", err)
	}
	bus := &NATSBus{conn: adapter, js: js}
	if len(opts) > 0 {
		bus.options = opts[0]
	}
	return bus, nil
}

func (b *NATSBus) Publish(ctx context.Context, subject string, event EventEnvelope) error {
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

func (b *NATSBus) Enqueue(ctx context.Context, queue string, event EventEnvelope) error {
	if b == nil || b.js == nil {
		return fmt.Errorf("nats jetstream is nil")
	}
	if err := b.ensureJetStreamQueue(queue); err != nil {
		return err
	}
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = b.js.Publish(queue, raw)
	return err
}

func (b *NATSBus) ConsumeQueue(ctx context.Context, queue string, opts QueueConsumeOptions) (<-chan QueueMessage, func(), error) {
	if b == nil || b.js == nil {
		return nil, nil, fmt.Errorf("nats jetstream is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := b.ensureJetStreamQueue(queue); err != nil {
		return nil, nil, err
	}
	group := strings.TrimSpace(opts.Group)
	if group == "" {
		group = strings.TrimSpace(b.options.Group)
	}
	if group == "" {
		group = "workers"
	}
	durable := strings.TrimSpace(b.options.Durable)
	if durable == "" {
		durable = strings.ReplaceAll(group, ".", "_")
	}
	if durable == "" {
		durable = "durable"
	}
	_, err := b.js.AddConsumer(b.streamForQueue(queue), &nats.ConsumerConfig{
		Durable:       durable,
		DeliverPolicy: nats.DeliverAllPolicy,
		AckPolicy:     nats.AckExplicitPolicy,
		AckWait:       1 * time.Second,
	})
	if err != nil &&
		!strings.Contains(err.Error(), "consumer name already in use") &&
		!strings.Contains(err.Error(), "consumer already exists") {
		return nil, nil, err
	}

	sub, err := b.js.PullSubscribe(queue, durable, nats.Bind(b.streamForQueue(queue), durable))
	if err != nil {
		return nil, nil, err
	}
	out := make(chan QueueMessage, 32)
	stop := make(chan struct{})
	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			_ = sub.Unsubscribe()
			close(stop)
		})
	}

	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			default:
			}
			msgs, err := sub.Fetch(1, nats.MaxWait(500*time.Millisecond))
			if err != nil {
				if strings.Contains(err.Error(), "timeout") {
					continue
				}
				continue
			}
			for _, msg := range msgs {
				env, err := ParseEventEnvelope(msg.Data)
				if err != nil {
					_ = msg.Ack()
					continue
				}
				natsMsg := msg
				queueMsg := QueueMessage{
					ID:    msg.Reply,
					Event: env,
					ackFn: func(context.Context) error {
						return natsMsg.Ack()
					},
					nackFn: func(context.Context) error {
						return natsMsg.Nak()
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
	return out, unsubscribe, nil
}

func (b *NATSBus) Request(ctx context.Context, subject string, request EventEnvelope, timeout time.Duration) (EventEnvelope, error) {
	if b == nil {
		return EventEnvelope{}, fmt.Errorf("nats bus is nil")
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	replySubject := subject + ".reply." + request.IdempotencyKey
	request.ReplyTo = replySubject
	respCh, unsubscribe, err := b.Subscribe(ctx, replySubject)
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
		case resp, ok := <-respCh:
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

func (b *NATSBus) Respond(ctx context.Context, subject string, handler RequestHandler) (func(), error) {
	if b == nil {
		return nil, fmt.Errorf("nats bus is nil")
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

func (b *NATSBus) ensureJetStreamQueue(queue string) error {
	stream := b.streamForQueue(queue)
	_, err := b.js.StreamInfo(stream)
	if err == nil {
		return nil
	}
	_, err = b.js.AddStream(&nats.StreamConfig{
		Name:      stream,
		Subjects:  []string{queue},
		Retention: nats.WorkQueuePolicy,
		Storage:   nats.FileStorage,
	})
	if err != nil && !strings.Contains(err.Error(), "stream name already in use") {
		return err
	}
	return nil
}

func (b *NATSBus) streamForQueue(queue string) string {
	if b != nil && strings.TrimSpace(b.options.Stream) != "" {
		return strings.TrimSpace(b.options.Stream)
	}
	return strings.ReplaceAll(queue, ".", "_")
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

func (a *natsBusConnectionAdapter) JetStream(opts ...nats.JSOpt) (natsJetStream, error) {
	js, err := a.Conn.JetStream(opts...)
	if err != nil {
		return nil, err
	}
	return natsJetStreamAdapter{JetStreamContext: js}, nil
}

type natsBusSubscriptionAdapter struct {
	*nats.Subscription
}

func (a natsBusSubscriptionAdapter) Unsubscribe() error {
	return a.Subscription.Unsubscribe()
}

type natsPullSubscriptionAdapter struct {
	*nats.Subscription
}

func (s natsPullSubscriptionAdapter) Fetch(batch int, opts ...nats.PullOpt) ([]*nats.Msg, error) {
	return s.Subscription.Fetch(batch, opts...)
}

type natsJetStreamAdapter struct {
	nats.JetStreamContext
}

func (a natsJetStreamAdapter) PullSubscribe(subj, durable string, opts ...nats.SubOpt) (natsPullSubscription, error) {
	sub, err := a.JetStreamContext.PullSubscribe(subj, durable, opts...)
	if err != nil {
		return nil, err
	}
	return natsPullSubscriptionAdapter{Subscription: sub}, nil
}

func (a *natsBusConnectionAdapter) Close() error {
	a.Conn.Close()
	return nil
}
