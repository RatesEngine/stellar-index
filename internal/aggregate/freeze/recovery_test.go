package freeze_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/RatesEngine/rates-engine/internal/aggregate/anomaly"
	"github.com/RatesEngine/rates-engine/internal/aggregate/freeze"
	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// fakeOpenLister returns a fixed list of open pairs.
type fakeOpenLister struct {
	mu    sync.Mutex
	pairs []freeze.OpenFreezePair
	err   error
}

func (l *fakeOpenLister) ListOpen(_ context.Context) ([]freeze.OpenFreezePair, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return nil, l.err
	}
	out := make([]freeze.OpenFreezePair, len(l.pairs))
	copy(out, l.pairs)
	return out, nil
}

// fakeRecoverer captures MarkRecovered calls.
type fakeRecoverer struct {
	mu       sync.Mutex
	calls    []freeze.OpenFreezePair
	failWith error
}

func (r *fakeRecoverer) MarkRecovered(_ context.Context, asset, quote canonical.Asset) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, freeze.OpenFreezePair{Asset: asset, Quote: quote})
	return r.failWith
}

// TestRecovery_ClosesRowsWhenRedisMarkerGone — the canonical happy
// path: postgres has an open row, Redis has no marker (TTL elapsed)
// → MarkRecovered fires.
func TestRecovery_ClosesRowsWhenRedisMarkerGone(t *testing.T) {
	_, rdb := newRedis(t)
	asset, quote := nativeUSD(t)
	lister := &fakeOpenLister{
		pairs: []freeze.OpenFreezePair{{Asset: asset, Quote: quote}},
	}
	closer := &fakeRecoverer{}

	r := freeze.NewRecovery(rdb, lister, closer, freeze.RecoveryOptions{
		Interval: 10 * time.Millisecond,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_ = r.Run(ctx)

	closer.mu.Lock()
	defer closer.mu.Unlock()
	if len(closer.calls) == 0 {
		t.Fatalf("MarkRecovered never called; want at least 1")
	}
	got := closer.calls[0]
	if got.Asset.String() != asset.String() {
		t.Errorf("asset = %s, want %s", got.Asset.String(), asset.String())
	}
	if got.Quote.String() != quote.String() {
		t.Errorf("quote = %s, want %s", got.Quote.String(), quote.String())
	}
}

// TestRecovery_LeavesStillFiringRowsAlone — when the Redis marker is
// still present (orchestrator is refreshing it because the anomaly
// hasn't cleared), MarkRecovered MUST NOT fire.
func TestRecovery_LeavesStillFiringRowsAlone(t *testing.T) {
	_, rdb := newRedis(t)
	asset, quote := nativeUSD(t)

	// Pre-write a freeze marker (so Redis returns the value rather
	// than redis.Nil on the recovery sweep's GET).
	w, err := freeze.NewWriter(rdb, 0)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := w.Mark(context.Background(), asset, quote, "1.000000000000",
		anomaly.Decision{Action: anomaly.ActionFreeze}); err != nil {
		t.Fatalf("Mark: %v", err)
	}

	lister := &fakeOpenLister{
		pairs: []freeze.OpenFreezePair{{Asset: asset, Quote: quote}},
	}
	closer := &fakeRecoverer{}

	r := freeze.NewRecovery(rdb, lister, closer, freeze.RecoveryOptions{
		Interval: 10 * time.Millisecond,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_ = r.Run(ctx)

	closer.mu.Lock()
	defer closer.mu.Unlock()
	if len(closer.calls) != 0 {
		t.Errorf("MarkRecovered called %d times; want 0 (marker still present)",
			len(closer.calls))
	}
}

// TestRecovery_ListErrorIsNonFatal — a lister failure logs + counts
// but doesn't crash the worker (next tick retries).
func TestRecovery_ListErrorIsNonFatal(t *testing.T) {
	_, rdb := newRedis(t)
	lister := &fakeOpenLister{err: errors.New("boom")}
	closer := &fakeRecoverer{}

	r := freeze.NewRecovery(rdb, lister, closer, freeze.RecoveryOptions{
		Interval: 5 * time.Millisecond,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_ = r.Run(ctx)
	// No assertion beyond "didn't panic" — the metric increment is
	// observed via Prometheus in production. Test exists to guard
	// against future code that propagates the error.
}
