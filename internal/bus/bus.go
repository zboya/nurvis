// Package bus provides an in-process event bus for loosely coupled communication between components.
package bus

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// Envelope is the unified envelope delivered by the event bus.
type Envelope struct {
	Topic string
	Data  any
	Ts    time.Time
}

// Handler is the event handler function type.
type Handler func(ctx context.Context, e Envelope)

// Bus defines the event bus interface.
type Bus interface {
	// Publish publishes an event to the given topic (non-blocking; drops and warns if queue is full).
	Publish(topic string, data any)
	// Subscribe subscribes to one or more topics (supports trailing "*" wildcard, e.g. "agent.*").
	// Returns a channel and an unsubscribe function.
	Subscribe(topics ...string) (<-chan Envelope, func())
	// Start launches the background dispatch goroutine; must be called before Publish/Subscribe.
	Start(ctx context.Context)
	// Drain waits for the queue to be processed and then closes (for graceful shutdown).
	Drain(timeout time.Duration) error
}

type subscription struct {
	topics []string
	ch     chan Envelope
}

type impl struct {
	queue  chan Envelope
	mu     sync.RWMutex
	subs   []*subscription
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New creates a new Bus. queueSize is the internal buffer queue size, workers is the number of concurrent dispatch goroutines.
func New(queueSize, workers int) Bus {
	if queueSize <= 0 {
		queueSize = 2048
	}
	if workers <= 0 {
		workers = 4
	}
	b := &impl{
		queue: make(chan Envelope, queueSize),
	}
	return b
}

func (b *impl) Start(ctx context.Context) {
	b.ctx, b.cancel = context.WithCancel(ctx)
	// Single dispatch goroutine preserves ordering; adjust if concurrency is needed.
	b.wg.Add(1)
	go b.dispatch()
}

func (b *impl) Publish(topic string, data any) {
	e := Envelope{Topic: topic, Data: data, Ts: time.Now()}
	select {
	case b.queue <- e:
	default:
		slog.Warn("bus: queue full, dropping event", "topic", topic)
	}
}

func (b *impl) Subscribe(topics ...string) (<-chan Envelope, func()) {
	sub := &subscription{
		topics: topics,
		ch:     make(chan Envelope, 128),
	}
	b.mu.Lock()
	b.subs = append(b.subs, sub)
	b.mu.Unlock()

	cancel := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		for i, s := range b.subs {
			if s == sub {
				b.subs = append(b.subs[:i], b.subs[i+1:]...)
				close(sub.ch)
				return
			}
		}
	}
	return sub.ch, cancel
}

func (b *impl) Drain(timeout time.Duration) error {
	b.cancel()
	done := make(chan struct{})
	go func() {
		b.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("bus: drain timeout after %v", timeout)
	}
}

func (b *impl) dispatch() {
	defer b.wg.Done()
	for {
		select {
		case e, ok := <-b.queue:
			if !ok {
				return
			}
			b.fan(e)
		case <-b.ctx.Done():
			// Drain remaining queued events
			for {
				select {
				case e := <-b.queue:
					b.fan(e)
				default:
					return
				}
			}
		}
	}
}

func (b *impl) fan(e Envelope) {
	b.mu.RLock()
	subs := make([]*subscription, len(b.subs))
	copy(subs, b.subs)
	b.mu.RUnlock()

	for _, sub := range subs {
		if !matchTopics(sub.topics, e.Topic) {
			continue
		}
		safeDeliver(sub.ch, e)
	}
}

func safeDeliver(ch chan Envelope, e Envelope) {
	defer func() {
		if r := recover(); r != nil {
			slog.Warn("bus: deliver to closed channel", "topic", e.Topic)
		}
	}()
	select {
	case ch <- e:
	default:
		slog.Warn("bus: subscriber slow, dropping event", "topic", e.Topic)
	}
}

// matchTopics checks whether an event topic matches any pattern in the subscription list.
// Patterns support a trailing "*" wildcard, e.g. "agent.*" matches all topics with the "agent." prefix.
func matchTopics(patterns []string, topic string) bool {
	for _, p := range patterns {
		if p == "*" || p == topic {
			return true
		}
		if strings.HasSuffix(p, "*") {
			prefix := p[:len(p)-1]
			if strings.HasPrefix(topic, prefix) {
				return true
			}
		}
	}
	return false
}
