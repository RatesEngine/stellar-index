package pipeline

import (
	"context"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/consumer"
)

// fakeEvent is a consumer.Event that hits the sink's default
// (unhandled) case so the test exercises the buffered-drain logic
// without needing a real postgres store.
type fakeEvent struct {
	id int
}

func (fakeEvent) EventKind() string { return "test.fake" }
func (fakeEvent) Source() string    { return "test-fake-source" }

var _ consumer.Event = fakeEvent{}

// TestPersistEvents_DrainsBufferedEventsOnShutdown — the load-bearing
// safety property: when the parent ctx is cancelled mid-stream,
// PersistEvents must still consume every event already in the
// channel buffer before returning. Without this, the indexer's
// per-ledger cursor advance (which happens AFTER the producer
// enqueues events to `in`, BEFORE the sink writes them) would
// silently lose up to cap(in) events on every SIGTERM.
func TestPersistEvents_DrainsBufferedEventsOnShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	in := make(chan consumer.Event, 10)

	// Pre-fill the buffer so we can prove the drain reads them
	// after ctx is cancelled.
	const buffered = 10
	for i := 0; i < buffered; i++ {
		in <- fakeEvent{id: i}
	}

	// Cancel the ctx FIRST so PersistEvents enters the drain path
	// on its first iteration. (If we cancelled mid-iteration, the
	// race between `case <-ctx.Done()` and `case ev, ok := <-in`
	// would be Go-runtime-dependent and the test would be flaky.)
	cancel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	done := make(chan struct{})
	go func() {
		defer close(done)
		// Pass nil store — the test events hit the default
		// (unhandled) switch case which only logs + increments a
		// counter, never touching the store. If a future
		// handleOneEvent change makes the default case dereference
		// the store, this test surfaces it as a panic — which is
		// the correct signal.
		PersistEvents(ctx, logger, nil, in, SinkModeAll)
	}()

	// Close the channel so drain can exit cleanly without hitting
	// the 30-second drainTimeout fallback.
	close(in)

	select {
	case <-done:
		// PersistEvents returned — verify it drained everything by
		// checking the channel is empty (an undrained channel would
		// still have events).
		if got := len(in); got != 0 {
			t.Errorf("after shutdown drain, channel still has %d events; want 0", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("PersistEvents didn't return within 5s after ctx cancel + channel close")
	}
}

// TestPersistEvents_DrainTimeoutBoundsHang — paranoid safety net:
// if the drain path's `handleOneEvent` ever blocks (e.g. a future
// store call hangs), the 30-second [drainTimeout] still bounds the
// shutdown. We can't easily simulate a hang in unit-time, so we
// just sanity-check the timeout constant is non-zero and within an
// operationally-sane bound.
func TestPersistEvents_DrainTimeoutBoundsHang(t *testing.T) {
	if drainTimeout <= 0 {
		t.Errorf("drainTimeout = %v; must be positive", drainTimeout)
	}
	if drainTimeout > 5*time.Minute {
		t.Errorf("drainTimeout = %v; > 5min defeats the bounded-shutdown invariant", drainTimeout)
	}
}

// TestPersistEvents_NormalCloseStillWorks — natural completion
// (channel closed without ctx cancel) is the common case for a
// bounded backfill. Make sure the new drain path didn't break it.
func TestPersistEvents_NormalCloseStillWorks(t *testing.T) {
	ctx := context.Background()
	in := make(chan consumer.Event, 5)

	// Buffer some events, then close. PersistEvents should consume
	// all of them and return without needing ctx cancellation.
	for i := 0; i < 5; i++ {
		in <- fakeEvent{id: i}
	}
	close(in)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	done := make(chan struct{})
	go func() {
		defer close(done)
		PersistEvents(ctx, logger, nil, in, SinkModeAll)
	}()

	select {
	case <-done:
		// Counter sanity: every event reached handleOneEvent.
		// We don't have a direct hook, so we re-use the channel
		// length: drained == empty.
		if got := len(in); got != 0 {
			t.Errorf("len(in)=%d after PersistEvents return; want 0", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("PersistEvents didn't return after channel close within 5s")
	}
}

// processedCount lets future tests verify drain counts without
// scraping prometheus globals; not used by the current test set
// but kept here as a hook for the next test that needs it.
var processedCount atomic.Int64

func init() { processedCount.Store(0) }
