package v1

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
)

// fakeHist is a HistoryReader whose only real method is
// LatestTradePerSource. The embedded interface is nil — every other
// method would panic, but these tests only exercise the cached one.
// Call accounting is atomic + the failure toggle is keyed on call
// number (no mid-test field mutation) so `go test -race` stays
// clean under the concurrent detached fill.
type fakeHist struct {
	HistoryReader
	calls   atomic.Int64
	delay   time.Duration
	failGE2 atomic.Bool
}

func (f *fakeHist) LatestTradePerSource(
	ctx context.Context, _ canonical.Pair, _ string,
) ([]canonical.Trade, error) {
	n := f.calls.Add(1)
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if n >= 2 && f.failGE2.Load() {
		return nil, errors.New("swr history boom")
	}
	return []canonical.Trade{{Source: "sdex"}}, nil
}

func histTestPair(t *testing.T) canonical.Pair {
	t.Helper()
	base, err := canonical.ParseAsset("native")
	if err != nil {
		t.Fatalf("parse base: %v", err)
	}
	quote, err := canonical.ParseAsset("fiat:USD")
	if err != nil {
		t.Fatalf("parse quote: %v", err)
	}
	p, err := canonical.NewPair(base, quote)
	if err != nil {
		t.Fatalf("new pair: %v", err)
	}
	return p
}

// TestCachedHistoryReader_ColdThenFreshHit: a cold call returns the
// upstream result (one call); an immediate repeat is a fresh hit
// (still one call). ttl=0 disables caching entirely (pass-through).
func TestCachedHistoryReader_ColdThenFreshHit(t *testing.T) {
	up := &fakeHist{}
	c := NewCachedHistoryReader(up, time.Minute)
	p := histTestPair(t)

	rows, err := c.LatestTradePerSource(context.Background(), p, "")
	if err != nil || len(rows) != 1 {
		t.Fatalf("cold: rows=%d err=%v", len(rows), err)
	}
	rows, err = c.LatestTradePerSource(context.Background(), p, "")
	if err != nil || len(rows) != 1 {
		t.Fatalf("fresh hit: rows=%d err=%v", len(rows), err)
	}
	if up.calls.Load() != 1 {
		t.Fatalf("want 1 upstream call (cold + fresh hit); got %d", up.calls.Load())
	}

	pt := NewCachedHistoryReader(&fakeHist{}, 0) // ttl=0 → pass-through
	if _, err := pt.LatestTradePerSource(context.Background(), p, ""); err != nil {
		t.Fatalf("pass-through err: %v", err)
	}
}

// TestCachedHistoryReader_DetachedColdFillWarms is the #29 core: a
// cold caller whose ctx deadline is shorter than the upstream query
// gets ctx.Err() promptly (the handler 503s) — but the detached
// fill keeps running on its own budget, so the NEXT poll is a fast
// fresh hit, and the slow caller did NOT spawn a second upstream
// call.
func TestCachedHistoryReader_DetachedColdFillWarms(t *testing.T) {
	up := &fakeHist{delay: 200 * time.Millisecond}
	c := NewCachedHistoryReader(up, time.Minute)
	p := histTestPair(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	st := time.Now()
	_, err := c.LatestTradePerSource(ctx, p, "")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cold caller must get DeadlineExceeded (→503); got %v", err)
	}
	if d := time.Since(st); d > 120*time.Millisecond {
		t.Fatalf("cold caller blocked %v; must return at its own ~30ms ctx", d)
	}

	// Detached fill (200ms) keeps going and warms the cache.
	if !waitFor(2*time.Second, func() bool {
		rows, e := c.LatestTradePerSource(context.Background(), p, "")
		return e == nil && len(rows) == 1
	}) {
		t.Fatal("detached fill never warmed the cache")
	}
	if got := up.calls.Load(); got != 1 {
		t.Fatalf("timed-out caller must not spawn a 2nd fill; want 1 upstream call, got %d", got)
	}
}

// TestCachedHistoryReader_SWRServesStaleSingleFlight: an expired
// entry serves stale immediately under heavy concurrency, with
// exactly one single-flighted detached refresh.
func TestCachedHistoryReader_SWRServesStaleSingleFlight(t *testing.T) {
	up := &fakeHist{delay: 250 * time.Millisecond}
	c := NewCachedHistoryReader(up, 25*time.Millisecond)
	p := histTestPair(t)

	if _, err := c.LatestTradePerSource(context.Background(), p, ""); err != nil {
		t.Fatalf("cold: %v", err) // calls=1
	}
	if up.calls.Load() != 1 {
		t.Fatalf("cold calls=%d want 1", up.calls.Load())
	}
	time.Sleep(50 * time.Millisecond) // expire

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st := time.Now()
			rows, err := c.LatestTradePerSource(context.Background(), p, "")
			if err != nil || len(rows) != 1 {
				t.Errorf("SWR must serve stale; rows=%d err=%v", len(rows), err)
			}
			if d := time.Since(st); d > 120*time.Millisecond {
				t.Errorf("SWR blocked %v; must serve stale ~instantly", d)
			}
		}()
	}
	wg.Wait()

	if !waitFor(2*time.Second, func() bool { return up.calls.Load() == 2 }) {
		t.Fatalf("want 2 calls (1 cold + 1 single-flighted refresh); got %d", up.calls.Load())
	}
	time.Sleep(350 * time.Millisecond) // let the 250ms refresh finish; no new reads
	if got := up.calls.Load(); got != 2 {
		t.Fatalf("single-flight violated: %d calls for 20 concurrent stale reads", got)
	}
}

// TestCachedHistoryReader_SWRKeepsStaleOnError: a failing background
// refresh keeps serving stale (never errors, never blocks) and is
// retried on the next expired request.
func TestCachedHistoryReader_SWRKeepsStaleOnError(t *testing.T) {
	up := &fakeHist{}
	up.failGE2.Store(true) // call 1 (cold) OK; call >=2 (refresh) errors
	c := NewCachedHistoryReader(up, 20*time.Millisecond)
	p := histTestPair(t)

	if _, err := c.LatestTradePerSource(context.Background(), p, ""); err != nil {
		t.Fatalf("cold: %v", err) // calls=1
	}
	time.Sleep(40 * time.Millisecond) // expire

	rows, err := c.LatestTradePerSource(context.Background(), p, "")
	if err != nil || len(rows) != 1 {
		t.Fatalf("stale-with-failing-refresh must serve stale; rows=%d err=%v", len(rows), err)
	}
	if !waitFor(2*time.Second, func() bool { return up.calls.Load() == 2 }) {
		t.Fatalf("refresh not attempted; calls=%d want 2", up.calls.Load())
	}

	time.Sleep(40 * time.Millisecond) // re-expire → retry
	rows2, err2 := c.LatestTradePerSource(context.Background(), p, "")
	if err2 != nil || len(rows2) != 1 {
		t.Fatalf("must still serve stale after a failed refresh; rows=%d err=%v", len(rows2), err2)
	}
	if !waitFor(2*time.Second, func() bool { return up.calls.Load() == 3 }) {
		t.Fatalf("refresh not retried after re-expire; calls=%d want 3", up.calls.Load())
	}
}
