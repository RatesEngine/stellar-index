package streaming

import (
	"sync"
	"time"
)

// DefaultBufferSize is the per-topic ring-buffer capacity used when
// no explicit override is supplied. 256 events is the sweet spot:
//
//   - At the 1m closed-bucket cadence (/v1/price/stream): ~4 hours of
//     replay window — generous for the typical 30s reconnect blip
//     and enough for a multi-minute network outage.
//   - At the 5s tip cadence (/v1/price/tip/stream): ~21 minutes,
//     comfortable for short reconnect storms.
//
// At 4 KiB per Event the buffer is ~1 MiB per topic — fine even with
// hundreds of topics.
const DefaultBufferSize = 256

// subscriberQueueDepth is the per-subscriber channel capacity. Once
// the channel fills up the subscription is dropped (see Hub.Publish).
// 32 events is enough for short bursts but small enough that a stuck
// subscriber gets evicted quickly rather than ballooning memory.
const subscriberQueueDepth = 32

// Hub is the pub/sub primitive backing the SSE stream handlers.
// One instance is shared across all stream endpoints (each endpoint
// uses different topic names — typically pair-keyed, e.g.
// "tip:native/fiat:USD").
//
// Hub is goroutine-safe.
type Hub struct {
	gen        Generator
	bufferSize int

	mu     sync.RWMutex
	topics map[string]*topicState
}

// topicState is the per-topic ring buffer + subscriber list.
//
// Held via a per-topic pointer so Hub.Publish can lock the topic
// alone, not the whole Hub — avoids lock-contention spikes when many
// topics publish concurrently.
type topicState struct {
	mu     sync.Mutex
	buffer *ring
	subs   map[*subscription]struct{}
}

// NewHub returns a Hub with [DefaultBufferSize] per topic. Pass 0 to
// take the default; positive values override per-topic capacity.
func NewHub(bufferSize int) *Hub {
	if bufferSize <= 0 {
		bufferSize = DefaultBufferSize
	}
	return &Hub{
		bufferSize: bufferSize,
		topics:     make(map[string]*topicState),
	}
}

// Publish broadcasts a fresh Event on the given topic. The Event's
// ID and Timestamp are populated by Hub — callers MUST leave both
// zero. Returns the assigned ID.
//
// Slow subscribers are dropped: if a subscriber's queue is full,
// its channel is closed and the subscription removed. The dropped
// client sees the connection close and reconnects with
// Last-Event-ID for buffered replay.
func (h *Hub) Publish(topic, eventType string, data []byte) string {
	ev := Event{
		ID:        h.gen.Next(),
		Type:      eventType,
		Data:      data,
		Timestamp: time.Now(),
	}

	t := h.getOrCreateTopic(topic)

	t.mu.Lock()
	t.buffer.push(ev)
	// Snapshot subscribers so we can release the topic lock before
	// sending — keeps a slow sub from blocking publishers (sends
	// below are non-blocking anyway, but the snapshot lets us drop
	// them off-lock).
	subs := make([]*subscription, 0, len(t.subs))
	for s := range t.subs {
		subs = append(subs, s)
	}
	t.mu.Unlock()

	for _, s := range subs {
		// trySend is guarded by the subscription's own mutex, so it can
		// never race with a concurrent close() (CS-012): a send on a
		// closed channel panics and would crash the whole process, and
		// the send here is deliberately off the topic lock, so a
		// cancel()/drop on another goroutine (or another topic, for a
		// multi-topic sub) could otherwise close sub.ch mid-send.
		if full := s.trySend(ev); full {
			h.dropSubscriber(topic, s)
		}
	}
	return ev.ID
}

// Subscribe registers a subscriber across one or more topics, with
// an optional Last-Event-ID resume cursor. The returned chan
// receives buffered-replay events first (in ID order), then live
// events. Unsubscribe by calling cancel().
//
// If lastEventID is empty, no replay happens — the client gets only
// events published after Subscribe returns. If lastEventID is older
// than the buffered window, replay starts at the buffer's oldest
// event (the client sees an ID jump, which is the documented signal
// that some events were lost).
func (h *Hub) Subscribe(topics []string, lastEventID string) (<-chan Event, func()) {
	sub := &subscription{
		ch:     make(chan Event, subscriberQueueDepth),
		topics: append([]string(nil), topics...),
	}

	// Replay buffered events FIRST, before registering as a live
	// listener. Otherwise a live event published mid-replay could
	// be sent before the older buffered ones.
	for _, topic := range topics {
		t := h.getOrCreateTopic(topic)
		t.mu.Lock()
		replay := t.buffer.snapshotAfter(lastEventID)
		t.mu.Unlock()
		for _, ev := range replay {
			select {
			case sub.ch <- ev:
			default:
				// Replay overflowed the subscriber queue. Close +
				// signal — the client sees an immediate drop and
				// can reconnect with a more recent Last-Event-ID.
				sub.close()
				return sub.ch, func() {}
			}
		}
	}

	// Now register for live events on every topic.
	for _, topic := range topics {
		t := h.getOrCreateTopic(topic)
		t.mu.Lock()
		t.subs[sub] = struct{}{}
		t.mu.Unlock()
	}

	cancel := func() {
		for _, topic := range topics {
			h.dropSubscriber(topic, sub)
		}
	}
	return sub.ch, cancel
}

// dropSubscriber removes sub from the named topic's subscriber set
// and closes its channel exactly once. Called when a subscription
// is cancelled or its queue fills up.
func (h *Hub) dropSubscriber(topic string, sub *subscription) {
	h.mu.RLock()
	t, ok := h.topics[topic]
	h.mu.RUnlock()
	if !ok {
		return
	}
	t.mu.Lock()
	_, present := t.subs[sub]
	if present {
		delete(t.subs, sub)
	}
	t.mu.Unlock()
	// Close outside the topic lock; sub.close() is idempotent and
	// mutually exclusive with trySend via the subscription's own mutex.
	if present {
		sub.close()
	}
}

// getOrCreateTopic returns the topicState for `name`, creating it
// on first use. Held outside any topic lock so two concurrent
// publishers on different topics don't serialise.
func (h *Hub) getOrCreateTopic(name string) *topicState {
	h.mu.RLock()
	t, ok := h.topics[name]
	h.mu.RUnlock()
	if ok {
		return t
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	// Double-check under write lock — another goroutine may have
	// won the race.
	if t, ok = h.topics[name]; ok {
		return t
	}
	t = &topicState{
		buffer: newRing(h.bufferSize),
		subs:   make(map[*subscription]struct{}),
	}
	h.topics[name] = t
	return t
}

// subscription is one active stream's per-Hub state.
//
// mu serializes trySend against close so a publisher can never send on a
// channel a concurrent cancel/drop has closed (CS-012 — send-on-closed
// panics and crashes the process). It is held only for a non-blocking
// select, so contention is negligible.
type subscription struct {
	ch     chan Event
	topics []string

	mu     sync.Mutex
	closed bool
}

// trySend delivers ev to the subscriber without blocking. It returns
// full=true when the subscriber's queue is full (caller should drop it).
// A closed subscription is a no-op (returns full=false) — never a panic.
func (s *subscription) trySend(ev Event) (full bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	select {
	case s.ch <- ev:
		return false
	default:
		return true
	}
}

// close closes the subscriber channel exactly once. Idempotent and
// mutually exclusive with trySend.
func (s *subscription) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		close(s.ch)
	}
}
