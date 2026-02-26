package distributed

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

type Bus interface {
	Publish(ctx context.Context, subject string, event EventEnvelope) error
	Subscribe(ctx context.Context, subject string) (<-chan EventEnvelope, func(), error)
	Enqueue(ctx context.Context, queue string, event EventEnvelope) error
	ConsumeQueue(ctx context.Context, queue string, opts QueueConsumeOptions) (<-chan QueueMessage, func(), error)
	Request(ctx context.Context, subject string, request EventEnvelope, timeout time.Duration) (EventEnvelope, error)
	Respond(ctx context.Context, subject string, handler RequestHandler) (func(), error)
	Close() error
}

type BusBackendOptions struct {
	Stream  string
	Group   string
	Durable string
}

type QueueConsumeOptions struct {
	Consumer string
	Group    string
}

type RequestHandler func(context.Context, EventEnvelope) (EventEnvelope, error)

type QueueMessage struct {
	ID    string
	Event EventEnvelope

	ackFn  func(context.Context) error
	nackFn func(context.Context) error
}

func (m QueueMessage) Ack(ctx context.Context) error {
	if m.ackFn == nil {
		return nil
	}
	return m.ackFn(ctx)
}

func (m QueueMessage) Nack(ctx context.Context) error {
	if m.nackFn == nil {
		return nil
	}
	return m.nackFn(ctx)
}

type MemoryBus struct {
	mu        sync.RWMutex
	channels  map[string][]chan EventEnvelope
	queues    map[string][]memoryQueueItem
	inflight  map[string]memoryQueueItem
	seq       uint64
	closed    bool
	closeOnce sync.Once
}

type memoryQueueItem struct {
	id    string
	queue string
	event EventEnvelope
}

func NewMemoryBus() *MemoryBus {
	return &MemoryBus{
		channels: make(map[string][]chan EventEnvelope),
		queues:   make(map[string][]memoryQueueItem),
		inflight: make(map[string]memoryQueueItem),
	}
}

func (b *MemoryBus) Publish(_ context.Context, subject string, event EventEnvelope) error {
	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return fmt.Errorf("bus closed")
	}
	consumers := append([]chan EventEnvelope{}, b.channels[subject]...)
	b.mu.RUnlock()

	for _, ch := range consumers {
		select {
		case ch <- event:
		default:
		}
	}
	return nil
}

func (b *MemoryBus) Subscribe(_ context.Context, subject string) (<-chan EventEnvelope, func(), error) {
	if b == nil {
		return nil, nil, fmt.Errorf("bus is nil")
	}
	ch := make(chan EventEnvelope, 32)
	b.mu.Lock()
	if b.closed {
		close(ch)
		b.mu.Unlock()
		return nil, nil, fmt.Errorf("bus closed")
	}
	b.channels[subject] = append(b.channels[subject], ch)
	b.mu.Unlock()

	unsub := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		subscribers := b.channels[subject]
		for i, candidate := range subscribers {
			if candidate == ch {
				b.channels[subject] = append(subscribers[:i], subscribers[i+1:]...)
				close(ch)
				return
			}
		}
	}
	return ch, unsub, nil
}

func (b *MemoryBus) Enqueue(_ context.Context, queue string, event EventEnvelope) error {
	if b == nil {
		return fmt.Errorf("bus is nil")
	}
	queue = strings.TrimSpace(queue)
	if queue == "" {
		return fmt.Errorf("queue is required")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return fmt.Errorf("bus closed")
	}
	b.seq++
	id := fmt.Sprintf("%s-%d", queue, b.seq)
	b.queues[queue] = append(b.queues[queue], memoryQueueItem{
		id:    id,
		queue: queue,
		event: event,
	})
	return nil
}

func (b *MemoryBus) ConsumeQueue(ctx context.Context, queue string, _ QueueConsumeOptions) (<-chan QueueMessage, func(), error) {
	if b == nil {
		return nil, nil, fmt.Errorf("bus is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	queue = strings.TrimSpace(queue)
	if queue == "" {
		return nil, nil, fmt.Errorf("queue is required")
	}
	out := make(chan QueueMessage, 32)
	stop := make(chan struct{})
	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			close(stop)
		})
	}

	go func() {
		defer close(out)
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case <-ticker.C:
				item, ok := b.takeQueueItem(queue)
				if !ok {
					continue
				}
				msg := QueueMessage{
					ID:    item.id,
					Event: item.event,
					ackFn: func(context.Context) error {
						b.ackQueueItem(item.id)
						return nil
					},
					nackFn: func(context.Context) error {
						b.nackQueueItem(item.id)
						return nil
					},
				}
				select {
				case out <- msg:
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

func (b *MemoryBus) Request(ctx context.Context, subject string, request EventEnvelope, timeout time.Duration) (EventEnvelope, error) {
	if b == nil {
		return EventEnvelope{}, fmt.Errorf("bus is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	replySubject := fmt.Sprintf("%s.reply.%s", strings.TrimSpace(subject), request.IdempotencyKey)
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
		case event, ok := <-replyCh:
			if !ok {
				return EventEnvelope{}, fmt.Errorf("request response channel closed")
			}
			if event.CorrelationID != request.CorrelationID {
				continue
			}
			return event, nil
		}
	}
}

func (b *MemoryBus) Respond(ctx context.Context, subject string, handler RequestHandler) (func(), error) {
	if b == nil {
		return nil, fmt.Errorf("bus is nil")
	}
	if handler == nil {
		return nil, fmt.Errorf("request handler is required")
	}
	sub, unsubscribe, err := b.Subscribe(ctx, subject)
	if err != nil {
		return nil, err
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case request, ok := <-sub:
				if !ok {
					return
				}
				replyTo := strings.TrimSpace(request.ReplyTo)
				if replyTo == "" {
					continue
				}
				response, err := handler(ctx, request)
				if err != nil {
					continue
				}
				response.CorrelationID = request.CorrelationID
				response.IdempotencyKey = request.IdempotencyKey
				_ = b.Publish(ctx, replyTo, response)
			}
		}
	}()
	return unsubscribe, nil
}

func (b *MemoryBus) takeQueueItem(queue string) (memoryQueueItem, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return memoryQueueItem{}, false
	}
	items := b.queues[queue]
	if len(items) == 0 {
		return memoryQueueItem{}, false
	}
	item := items[0]
	b.queues[queue] = items[1:]
	b.inflight[item.id] = item
	return item, true
}

func (b *MemoryBus) ackQueueItem(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.inflight, id)
}

func (b *MemoryBus) nackQueueItem(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	item, ok := b.inflight[id]
	if !ok {
		return
	}
	delete(b.inflight, id)
	b.queues[item.queue] = append(b.queues[item.queue], item)
}

func (b *MemoryBus) Close() error {
	if b == nil {
		return nil
	}
	b.closeOnce.Do(func() {
		b.mu.Lock()
		b.closed = true
		for subject, subscribers := range b.channels {
			for _, ch := range subscribers {
				close(ch)
			}
			delete(b.channels, subject)
		}
		b.queues = map[string][]memoryQueueItem{}
		b.inflight = map[string]memoryQueueItem{}
		b.mu.Unlock()
	})
	return nil
}

func MustMarshal(raw json.RawMessage, fallback map[string]any) ([]byte, error) {
	if len(raw) > 0 {
		return raw, nil
	}
	return json.Marshal(fallback)
}
