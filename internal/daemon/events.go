package daemon

import (
	"sync"
	"sync/atomic"
	"time"
)

const subscriberBuffer = 256

// Event is one bus message.
type Event struct {
	Kind      string `json:"kind"`
	Seq       uint64 `json:"seq"`
	Dropped   uint64 `json:"dropped,omitempty"`
	CreatedAt string `json:"created_at"`
	Payload   any    `json:"payload,omitempty"`
}

// Subscriber receives events until it is closed.
type Subscriber struct {
	bus     *Bus
	ch      chan Event
	kinds   map[string]bool
	dropped atomic.Uint64
	once    sync.Once
}

// Events returns the delivery channel.
func (s *Subscriber) Events() <-chan Event { return s.ch }

// Close unsubscribes and closes the channel exactly once.
func (s *Subscriber) Close() {
	s.once.Do(func() {
		s.bus.remove(s)
		close(s.ch)
	})
}

func (s *Subscriber) wants(kind string) bool {
	if len(s.kinds) == 0 {
		return true
	}
	return s.kinds[kind]
}

func (s *Subscriber) publish(event Event) {
	deliver := event
	deliver.Dropped = s.dropped.Swap(0)
	select {
	case s.ch <- deliver:
		return
	default:
	}
	select {
	case <-s.ch:
		s.dropped.Add(1)
	default:
	}
	select {
	case s.ch <- deliver:
	default:
		s.dropped.Add(1)
	}
}

// Bus fans out events without blocking publishers.
type Bus struct {
	mu     sync.Mutex
	subs   map[*Subscriber]bool
	seq    atomic.Uint64
	closed atomic.Bool
}

// NewBus creates an empty bus.
func NewBus() *Bus {
	return &Bus{subs: map[*Subscriber]bool{}}
}

// Subscribe registers a subscriber; empty kinds receive every event.
func (b *Bus) Subscribe(kinds ...string) *Subscriber {
	sub := &Subscriber{bus: b, ch: make(chan Event, subscriberBuffer), kinds: map[string]bool{}}
	for _, kind := range kinds {
		sub.kinds[kind] = true
	}
	b.mu.Lock()
	if b.closed.Load() {
		b.mu.Unlock()
		close(sub.ch)
		return sub
	}
	b.subs[sub] = true
	b.mu.Unlock()
	return sub
}

func (b *Bus) remove(sub *Subscriber) {
	b.mu.Lock()
	delete(b.subs, sub)
	b.mu.Unlock()
}

// Publish delivers one event to matching subscribers. Publishing after Close
// or with no subscribers is a no-op.
func (b *Bus) Publish(kind string, payload any) {
	if b.closed.Load() {
		return
	}
	event := Event{Kind: kind, Seq: b.seq.Add(1), CreatedAt: time.Now().UTC().Format(time.RFC3339), Payload: payload}
	b.mu.Lock()
	subs := make([]*Subscriber, 0, len(b.subs))
	for sub := range b.subs {
		subs = append(subs, sub)
	}
	b.mu.Unlock()
	for _, sub := range subs {
		if sub.wants(kind) {
			sub.publish(event)
		}
	}
}

// Close closes every subscriber and ignores later publishes.
func (b *Bus) Close() {
	if b.closed.Swap(true) {
		return
	}
	b.mu.Lock()
	subs := make([]*Subscriber, 0, len(b.subs))
	for sub := range b.subs {
		subs = append(subs, sub)
	}
	b.mu.Unlock()
	for _, sub := range subs {
		sub.Close()
	}
}
