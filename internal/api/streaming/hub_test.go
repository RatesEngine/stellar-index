package streaming_test

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RatesEngine/rates-engine/internal/api/streaming"
)

// drainNonblocking returns events from ch until either it has
// accumulated `want` items or `timeout` elapses. Used to assert
// fanout under timing without busy-waiting in test code.
func drainNonblocking(t *testing.T, ch <-chan streaming.Event, want int, timeout time.Duration) []streaming.Event {
	t.Helper()
	out := make([]streaming.Event, 0, want)
	deadline := time.After(timeout)
	for len(out) < want {
		select {
		case ev, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, ev)
		case <-deadline:
			return out
		}
	}
	return out
}

// TestHub_Fanout — two subscribers on the same topic both receive
// every published event in order, with monotonically increasing IDs.
func TestHub_Fanout(t *testing.T) {
	hub := streaming.NewHub(0)
	subA, cancelA := hub.Subscribe([]string{"price:XLM/USD"}, "")
	defer cancelA()
	subB, cancelB := hub.Subscribe([]string{"price:XLM/USD"}, "")
	defer cancelB()

	for _, p := range []string{"0.10", "0.11", "0.12"} {
		hub.Publish("price:XLM/USD", "price_update", []byte(`{"p":"`+p+`"}`))
	}

	gotA := drainNonblocking(t, subA, 3, time.Second)
	gotB := drainNonblocking(t, subB, 3, time.Second)
	if len(gotA) != 3 || len(gotB) != 3 {
		t.Fatalf("fanout = (%d, %d), want (3, 3)", len(gotA), len(gotB))
	}
	for _, set := range [][]streaming.Event{gotA, gotB} {
		for i := 1; i < len(set); i++ {
			if set[i-1].ID >= set[i].ID {
				t.Errorf("IDs not strictly increasing: %q !< %q", set[i-1].ID, set[i].ID)
			}
		}
	}
}

// TestHub_TopicIsolation — a publisher on topic A doesn't reach a
// subscriber on topic B.
func TestHub_TopicIsolation(t *testing.T) {
	hub := streaming.NewHub(0)
	subA, cancelA := hub.Subscribe([]string{"topicA"}, "")
	defer cancelA()
	subB, cancelB := hub.Subscribe([]string{"topicB"}, "")
	defer cancelB()

	hub.Publish("topicA", "x", []byte("hello"))

	gotA := drainNonblocking(t, subA, 1, 500*time.Millisecond)
	gotB := drainNonblocking(t, subB, 1, 200*time.Millisecond)
	if len(gotA) != 1 {
		t.Errorf("topicA expected 1 event, got %d", len(gotA))
	}
	if len(gotB) != 0 {
		t.Errorf("topicB expected 0 events, got %d", len(gotB))
	}
}

// TestHub_MultiTopicSubscription — one Subscribe call covering two
// topics receives events from both.
func TestHub_MultiTopicSubscription(t *testing.T) {
	hub := streaming.NewHub(0)
	sub, cancel := hub.Subscribe([]string{"topicA", "topicB"}, "")
	defer cancel()

	hub.Publish("topicA", "x", []byte("a"))
	hub.Publish("topicB", "x", []byte("b"))

	got := drainNonblocking(t, sub, 2, time.Second)
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2", len(got))
	}
	seen := map[string]bool{}
	for _, ev := range got {
		seen[string(ev.Data)] = true
	}
	if !seen["a"] || !seen["b"] {
		t.Errorf("missing events: %v", seen)
	}
}

// TestHub_LastEventIDReplayFromBuffer — events published before
// Subscribe are replayed when the subscriber's Last-Event-ID is
// older than the most-recent buffered event.
func TestHub_LastEventIDReplayFromBuffer(t *testing.T) {
	hub := streaming.NewHub(0)

	// Publish 3 events into the buffer with no live subscribers.
	id1 := hub.Publish("topic", "x", []byte("first"))
	hub.Publish("topic", "x", []byte("second"))
	hub.Publish("topic", "x", []byte("third"))

	// Subscribe with Last-Event-ID == id1 → should replay the
	// "second" + "third" events that came after.
	sub, cancel := hub.Subscribe([]string{"topic"}, id1)
	defer cancel()

	got := drainNonblocking(t, sub, 2, time.Second)
	if len(got) != 2 {
		t.Fatalf("replay returned %d events, want 2", len(got))
	}
	if string(got[0].Data) != "second" || string(got[1].Data) != "third" {
		t.Errorf("replay order wrong: %q, %q", got[0].Data, got[1].Data)
	}
}

// TestHub_EmptyLastEventIDSkipsReplay — an empty resume cursor
// means "I want only future events" and should NOT replay anything
// from the buffer.
func TestHub_EmptyLastEventIDSkipsReplay(t *testing.T) {
	hub := streaming.NewHub(0)
	hub.Publish("topic", "x", []byte("buffered-1"))
	hub.Publish("topic", "x", []byte("buffered-2"))

	sub, cancel := hub.Subscribe([]string{"topic"}, "")
	defer cancel()

	// Nothing should arrive from replay — only the live event below.
	got := drainNonblocking(t, sub, 1, 200*time.Millisecond)
	if len(got) != 0 {
		t.Errorf("empty cursor replayed %d events, want 0", len(got))
	}

	hub.Publish("topic", "x", []byte("live"))
	got = drainNonblocking(t, sub, 1, time.Second)
	if len(got) != 1 || string(got[0].Data) != "live" {
		t.Errorf("live event missed: %v", got)
	}
}

// TestHub_BufferEvictsOldest — when the buffer fills, the oldest
// event is dropped. Resuming from an evicted ID returns whatever
// remains (the client sees a forward jump and can detect the loss).
func TestHub_BufferEvictsOldest(t *testing.T) {
	hub := streaming.NewHub(2) // tiny buffer
	id1 := hub.Publish("topic", "x", []byte("e1"))
	hub.Publish("topic", "x", []byte("e2"))
	hub.Publish("topic", "x", []byte("e3")) // evicts e1

	sub, cancel := hub.Subscribe([]string{"topic"}, id1)
	defer cancel()

	got := drainNonblocking(t, sub, 2, time.Second)
	// e1 was evicted; e2 + e3 should both replay because both
	// have IDs > id1.
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2 (e2, e3)", len(got))
	}
	for _, ev := range got {
		if string(ev.Data) == "e1" {
			t.Errorf("evicted e1 was replayed: %v", got)
		}
	}
}

// TestHub_SlowSubscriberDropped — a subscriber that never reads is
// evicted once its queue fills, freeing the publish path. Other
// subscribers continue to receive events.
func TestHub_SlowSubscriberDropped(t *testing.T) {
	hub := streaming.NewHub(0)
	// Slow sub: never reads. Its 32-deep queue will fill.
	slow, cancelSlow := hub.Subscribe([]string{"topic"}, "")
	defer cancelSlow()

	fast, cancelFast := hub.Subscribe([]string{"topic"}, "")
	defer cancelFast()

	// Publish 64 events — twice the per-subscriber queue depth.
	for i := 0; i < 64; i++ {
		hub.Publish("topic", "x", []byte("payload"))
	}

	// Slow sub's channel should now be closed. Signal completion
	// over a channel rather than a shared bool — the race detector
	// flags the latter (the drain goroutine writes while the main
	// goroutine reads).
	slowDrained := make(chan struct{})
	go func() {
		for range slow {
		}
		close(slowDrained)
	}()

	gotFast := drainNonblocking(t, fast, 64, 2*time.Second)
	if len(gotFast) < 32 {
		t.Errorf("fast subscriber starved: got %d events", len(gotFast))
	}

	select {
	case <-slowDrained:
		// channel closed → drain goroutine exited → drop confirmed.
	case <-time.After(time.Second):
		t.Error("slow subscriber's channel was not closed after overflow")
	}
}

// TestHub_PublishRaceFreeIDs — concurrent Publish calls produce
// strictly distinct IDs. Sanity check on the ID generator's CAS
// loop.
func TestHub_PublishRaceFreeIDs(t *testing.T) {
	hub := streaming.NewHub(0)
	const goroutines = 8
	const perG = 100

	var wg sync.WaitGroup
	ids := make(chan string, goroutines*perG)
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				ids <- hub.Publish("topic", "x", []byte("p"))
			}
		}()
	}
	wg.Wait()
	close(ids)

	seen := make(map[string]struct{}, goroutines*perG)
	for id := range ids {
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate ID under concurrency: %q", id)
		}
		seen[id] = struct{}{}
		if len(id) != 16 {
			t.Errorf("ID length = %d, want 16", len(id))
		}
		if strings.ContainsAny(id, "ghijklmnopqrstuvwxyz") {
			t.Errorf("ID contains non-hex chars: %q", id)
		}
	}
	if len(seen) != goroutines*perG {
		t.Errorf("unique IDs = %d, want %d", len(seen), goroutines*perG)
	}
}
