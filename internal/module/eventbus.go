package module

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// Each subscriber owns a bounded FIFO and one worker. A blocking native
// handler cannot create an unbounded goroutine for every event.
type EventBus struct {
	mu              sync.RWMutex
	subscribers     map[string]*eventSubscriber
	closed          bool
	logger          *slog.Logger
	deliveryTimeout time.Duration
}
type EventStats struct {
	Queued    int    `json:"queued"`
	Delivered uint64 `json:"delivered"`
	Failed    uint64 `json:"failed"`
	Dropped   uint64 `json:"dropped"`
}
type eventSubscriber struct {
	mu      sync.Mutex
	topics  map[string]bool
	handler EventHandler
	queue   chan Event
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}
	seen    map[string]bool
	order   []string
	stats   EventStats
}

func NewEventBus(logger *slog.Logger) *EventBus {
	if logger == nil {
		logger = slog.Default()
	}
	return &EventBus{subscribers: map[string]*eventSubscriber{}, logger: logger, deliveryTimeout: 5 * time.Second}
}
func (b *EventBus) Subscribe(topic string, handler EventHandler) func() {
	return b.SubscribeNamed(rand.Text(), []string{topic}, handler)
}
func (b *EventBus) SubscribeNamed(id string, topics []string, handler EventHandler) func() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || handler == nil {
		return func() {}
	}
	if old := b.subscribers[id]; old != nil {
		old.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &eventSubscriber{topics: map[string]bool{}, handler: handler, queue: make(chan Event, 64), ctx: ctx, cancel: cancel, done: make(chan struct{}), seen: map[string]bool{}}
	for _, topic := range topics {
		s.topics[topic] = true
	}
	b.subscribers[id] = s
	go b.run(id, s)
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if b.subscribers[id] == s {
			delete(b.subscribers, id)
		}
		cancel()
	}
}
func (b *EventBus) Emit(ctx context.Context, topic, eventID string, payload any) bool {
	data, err := json.Marshal(payload)
	if err != nil {
		return false
	}
	return b.Publish(context.WithoutCancel(ctx), topic, eventID, string(data))
}

// Publish never waits on consumers. False signals cancellation, shutdown,
// invalid JSON, or a full subscriber queue. It is not a durable disk outbox.
func (b *EventBus) Publish(ctx context.Context, topic, eventID, payloadJSON string) bool {
	if ctx.Err() != nil || len(payloadJSON) > 32*1024 || !json.Valid([]byte(payloadJSON)) {
		return false
	}
	if eventID == "" {
		eventID = rand.Text()
	}
	event := Event{Topic: topic, EventID: eventID, Timestamp: time.Now().UTC(), PayloadJSON: payloadJSON}
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.closed {
		return false
	}
	accepted := true
	for _, s := range b.subscribers {
		if !s.topics[topic] && !s.topics["*"] {
			continue
		}
		s.mu.Lock()
		key := topic + ":" + eventID
		if !s.seen[key] {
			select {
			case s.queue <- event:
				s.seen[key] = true
				s.order = append(s.order, key)
				if len(s.order) > 1024 {
					delete(s.seen, s.order[0])
					s.order = s.order[1:]
				}
			default:
				s.stats.Dropped++
				accepted = false
			}
		}
		s.mu.Unlock()
	}
	return accepted
}
func (b *EventBus) Stats(id string) EventStats {
	b.mu.RLock()
	s := b.subscribers[id]
	b.mu.RUnlock()
	if s == nil {
		return EventStats{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	stats := s.stats
	stats.Queued = len(s.queue)
	return stats
}
func (b *EventBus) run(id string, s *eventSubscriber) {
	defer close(s.done)
	for {
		select {
		case <-s.ctx.Done():
			return
		case event := <-s.queue:
			if s.ctx.Err() != nil {
				return
			}
			var err error
			for attempt := 0; attempt < 3; attempt++ {
				ctx, cancel := context.WithTimeout(s.ctx, b.deliveryTimeout)
				err = callHandler(ctx, s.handler, event)
				cancel()
				if err == nil || s.ctx.Err() != nil {
					break
				}
				if attempt < 2 {
					timer := time.NewTimer(time.Duration(attempt+1) * 100 * time.Millisecond)
					select {
					case <-s.ctx.Done():
						timer.Stop()
						return
					case <-timer.C:
					}
				}
			}
			if s.ctx.Err() != nil {
				return
			}
			s.mu.Lock()
			if err == nil {
				s.stats.Delivered++
			} else {
				s.stats.Failed++
			}
			s.mu.Unlock()
			if err != nil {
				b.logger.Warn("模块事件投递失败", "subscriber", id, "topic", event.Topic, "eventId", event.EventID)
			}
		}
	}
}
func callHandler(ctx context.Context, h EventHandler, event Event) (err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("event handler panicked")
		}
	}()
	err = h(ctx, event)
	if err == nil {
		err = ctx.Err()
	}
	return err
}

// Close cancels queued/in-flight deliveries. Native handlers must honor context;
// callers can bound waiting for handlers that violate that rule.
func (b *EventBus) Close(ctx context.Context) error {
	b.mu.Lock()
	b.closed = true
	subs := make([]*eventSubscriber, 0, len(b.subscribers))
	for _, s := range b.subscribers {
		s.cancel()
		subs = append(subs, s)
	}
	b.mu.Unlock()
	for _, s := range subs {
		select {
		case <-s.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
