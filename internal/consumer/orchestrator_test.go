package consumer_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/consumer"
)

// fakeSource implements consumer.Source. Emits N events then sleeps
// until ctx cancels; optionally returns a configurable error.
type fakeSource struct {
	name       string
	emit       []consumer.Event
	streamErr  error
	streamDone atomic.Int32 // increments every time StreamLive returns
	started    atomic.Int32 // increments every time StreamLive starts
	// lastLedger, when non-zero, is reported via Health.LastLedger —
	// the orchestrator's cursor persister uses this to advance the
	// checkpoint. Zero means "source hasn't observed any events yet"
	// and the persister declines to regress the cursor.
	lastLedger uint32
	// streamPanic, when non-nil, is the value StreamLive panics with
	// on first invocation. The orchestrator should recover and restart
	// via the backoff loop, not crash the process.
	streamPanic any
	panicked    atomic.Bool
}

func (f *fakeSource) Name() string { return f.name }

func (f *fakeSource) BackfillRange(ctx context.Context, from, to uint32, out chan<- consumer.Event) error {
	return nil
}

func (f *fakeSource) StreamLive(ctx context.Context, out chan<- consumer.Event) error {
	f.started.Add(1)
	defer f.streamDone.Add(1)

	// Panic only once so the restart loop eventually converges on
	// the normal error path. This mirrors real behaviour — a panic
	// is from a transient bad event, not a repeating condition.
	if f.streamPanic != nil && !f.panicked.Load() {
		f.panicked.Store(true)
		panic(f.streamPanic)
	}

	for _, ev := range f.emit {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case out <- ev:
		}
	}
	if f.streamErr != nil {
		return f.streamErr
	}
	<-ctx.Done()
	return ctx.Err()
}

func (f *fakeSource) Health() consumer.HealthStatus {
	return consumer.HealthStatus{Connected: true, LastLedger: f.lastLedger}
}

// testEvent is a trivial consumer.Event for the orchestrator tests.
type testEvent struct{ kind, source string }

func (t testEvent) EventKind() string { return t.kind }
func (t testEvent) Source() string    { return t.source }

// inmemCursors is a minimal CursorStore.
type inmemCursors struct {
	mu sync.Mutex
	m  map[string]consumer.Cursor
}

func newInmemCursors() *inmemCursors {
	return &inmemCursors{m: map[string]consumer.Cursor{}}
}

func (s *inmemCursors) GetCursor(ctx context.Context, source, sub string) (consumer.Cursor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.m[source+":"+sub]
	if !ok {
		return consumer.Cursor{}, consumer.ErrNoCursor
	}
	return c, nil
}

func (s *inmemCursors) UpsertCursor(ctx context.Context, source, sub string, lastLedger uint32) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[source+":"+sub] = consumer.Cursor{
		Source: source, Sub: sub, LastLedger: lastLedger, UpdatedAt: time.Now(),
	}
	return nil
}

// ─── Tests ────────────────────────────────────────────────────────

func TestOrchestrator_EventsFlow(t *testing.T) {
	src := &fakeSource{
		name: "fake",
		emit: []consumer.Event{
			testEvent{kind: "t1"},
			testEvent{kind: "t2"},
			testEvent{kind: "t3"},
		},
	}
	o := consumer.New(newInmemCursors(), []consumer.Source{src}, consumer.Config{
		MinBackoff: 50 * time.Millisecond, MaxBackoff: 100 * time.Millisecond,
		CursorPersistEvery: 50 * time.Millisecond,
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = o.Run(ctx) }()

	var got []string
	timeout := time.After(2 * time.Second)
	for len(got) < 3 {
		select {
		case ev, ok := <-o.Events():
			if !ok {
				t.Fatal("events channel closed early")
			}
			got = append(got, ev.EventKind())
		case <-timeout:
			t.Fatalf("only got %d events: %v", len(got), got)
		}
	}
	if got[0] != "t1" || got[1] != "t2" || got[2] != "t3" {
		t.Errorf("wrong order: %v", got)
	}
}

func TestOrchestrator_RestartsOnError(t *testing.T) {
	// Source returns an error after emitting one event → orchestrator
	// should sleep-backoff + restart + emit the next iteration's
	// event.
	src := &fakeSource{
		name:      "flaky",
		emit:      []consumer.Event{testEvent{kind: "once"}},
		streamErr: errors.New("boom"),
	}
	o := consumer.New(newInmemCursors(), []consumer.Source{src}, consumer.Config{
		MinBackoff: 10 * time.Millisecond, MaxBackoff: 50 * time.Millisecond,
		CursorPersistEvery: 20 * time.Millisecond,
	}, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	// Drain events in background so the orchestrator doesn't block
	// on the event channel.
	var drained atomic.Int32
	go func() {
		for range o.Events() {
			drained.Add(1)
		}
	}()

	_ = o.Run(ctx)

	// Should have restarted the source multiple times (bounded by
	// how many "emit once → err → backoff" cycles fit in 1s with
	// 10-50ms backoff).
	started := src.started.Load()
	if started < 2 {
		t.Errorf("expected at least 2 StreamLive starts (restart loop), got %d", started)
	}
	if drained.Load() == 0 {
		t.Error("expected at least one event drained")
	}
}

func TestOrchestrator_HonoursContextCancel(t *testing.T) {
	src := &fakeSource{
		name: "long",
		emit: []consumer.Event{testEvent{kind: "x"}},
	}
	o := consumer.New(newInmemCursors(), []consumer.Source{src}, consumer.Config{
		MinBackoff: 10 * time.Millisecond,
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		// Drain while running.
		for range o.Events() {
		}
	}()

	done := make(chan error, 1)
	go func() { done <- o.Run(ctx) }()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancel")
	}
}

func TestOrchestrator_RejectsEmptySourceList(t *testing.T) {
	o := consumer.New(newInmemCursors(), nil, consumer.Config{}, nil)
	err := o.Run(context.Background())
	if err == nil {
		t.Fatal("expected error for empty source list")
	}
}

func TestOrchestrator_CursorPersisted(t *testing.T) {
	cursors := newInmemCursors()
	src := &fakeSource{
		name:       "cursor-test",
		emit:       []consumer.Event{testEvent{kind: "one"}},
		lastLedger: 42_000_000, // source reports progress
	}
	o := consumer.New(cursors, []consumer.Source{src}, consumer.Config{
		MinBackoff:         10 * time.Millisecond,
		CursorPersistEvery: 20 * time.Millisecond,
	}, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	go func() {
		for range o.Events() {
		}
	}()
	_ = o.Run(ctx)

	// After the ticker fires at least once, the cursor should be upserted
	// with the fakeSource's reported LastLedger.
	got, err := cursors.GetCursor(context.Background(), "cursor-test", "")
	if err != nil {
		t.Fatalf("cursor should have been persisted at least once, got: %v", err)
	}
	if got.Source != "cursor-test" {
		t.Errorf("wrong cursor stored: %+v", got)
	}
	if got.LastLedger != 42_000_000 {
		t.Errorf("cursor LastLedger = %d, want 42000000 (from Health.LastLedger)", got.LastLedger)
	}
}

func TestOrchestrator_RecoversFromSourcePanic(t *testing.T) {
	// A source that panics on its first StreamLive invocation must
	// NOT take down the orchestrator. The restart loop converts the
	// panic into an error, backs off, and tries again.
	cursors := newInmemCursors()
	src := &fakeSource{
		name:        "panicky",
		streamPanic: "nil pointer deref in decoder",
	}
	o := consumer.New(cursors, []consumer.Source{src}, consumer.Config{
		MinBackoff:         10 * time.Millisecond,
		CursorPersistEvery: 20 * time.Millisecond,
	}, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		for range o.Events() {
		}
	}()
	go func() {
		done <- o.Run(ctx)
	}()

	select {
	case err := <-done:
		// Run should return ctx.Err(), not re-throw the panic.
		if err != nil && !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("Run returned unexpected error: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Run didn't return — panic may have crashed the goroutine")
	}

	// Proof the restart loop actually re-entered StreamLive after
	// the panic: `started` > 1 means it tried at least twice.
	if started := src.started.Load(); started < 2 {
		t.Errorf("expected at least 2 StreamLive starts (panic + restart), got %d", started)
	}
}

func TestOrchestrator_ShutdownFlushesCursor(t *testing.T) {
	// Persister ticker is long (5s); we cancel ctx in 100ms.
	// Without the shutdown flush, the cursor would never advance
	// because no tick fires. With the flush, final health.LastLedger
	// lands in the store before Run returns.
	cursors := newInmemCursors()
	src := &fakeSource{
		name:       "shutdown-flush",
		emit:       []consumer.Event{testEvent{kind: "e"}},
		lastLedger: 42_000_000,
	}
	o := consumer.New(cursors, []consumer.Source{src}, consumer.Config{
		MinBackoff:         10 * time.Millisecond,
		CursorPersistEvery: 5 * time.Second, // way past the test window
	}, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	go func() {
		for range o.Events() {
		}
	}()
	_ = o.Run(ctx)

	got, err := cursors.GetCursor(context.Background(), "shutdown-flush", "")
	if err != nil {
		t.Fatalf("cursor should have been flushed on shutdown: %v", err)
	}
	if got.LastLedger != 42_000_000 {
		t.Errorf("shutdown flush LastLedger = %d, want 42000000", got.LastLedger)
	}
}

func TestOrchestrator_CursorDoesNotRegressWhenSourceReportsZero(t *testing.T) {
	// A source that hasn't observed any events this session returns
	// LastLedger=0. The persister must NOT overwrite a prior
	// non-zero cursor with zero.
	cursors := newInmemCursors()

	// Seed a cursor at ledger 100.
	if err := cursors.UpsertCursor(context.Background(), "never-advance", "", 100); err != nil {
		t.Fatal(err)
	}

	src := &fakeSource{
		name:       "never-advance",
		lastLedger: 0, // never advances
	}
	o := consumer.New(cursors, []consumer.Source{src}, consumer.Config{
		MinBackoff:         10 * time.Millisecond,
		CursorPersistEvery: 20 * time.Millisecond,
	}, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	go func() {
		for range o.Events() {
		}
	}()
	_ = o.Run(ctx)

	got, err := cursors.GetCursor(context.Background(), "never-advance", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.LastLedger != 100 {
		t.Errorf("cursor regressed: got %d, want 100", got.LastLedger)
	}
}
