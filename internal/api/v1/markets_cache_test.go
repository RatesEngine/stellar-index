package v1

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/obs"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

type fakeMarketsReader struct {
	allPoolsCalls atomic.Int64
	delay         time.Duration
	err           error
}

func (f *fakeMarketsReader) DistinctPairsExt(ctx context.Context, cursor string, limit int, order timescale.MarketsOrder) ([]Market, string, error) {
	return nil, "", nil
}

func (f *fakeMarketsReader) SourceMarkets(ctx context.Context, source, cursor string, limit int, order timescale.MarketsOrder) ([]Market, string, error) {
	return nil, "", nil
}

func (f *fakeMarketsReader) AssetMarkets(ctx context.Context, asset, cursor string, limit int, order timescale.MarketsOrder) ([]Market, string, error) {
	return nil, "", nil
}

func (f *fakeMarketsReader) AllPools(ctx context.Context, filter timescale.PoolsFilter, cursor string, limit int, order timescale.MarketsOrder) ([]Pool, string, error) {
	f.allPoolsCalls.Add(1)
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, "", ctx.Err()
		}
	}
	if f.err != nil {
		return nil, "", f.err
	}
	return []Pool{{Source: "aquarius", Base: "native"}}, "", nil
}

func (f *fakeMarketsReader) PairMarket(ctx context.Context, base, quote canonical.Asset) (Market, bool, error) {
	return Market{}, false, nil
}

func (f *fakeMarketsReader) GetPairsVolumeHistory24hBatch(ctx context.Context, pairs [][2]string) (map[string][]timescale.PairVolumePoint, error) {
	return nil, nil
}

func TestCachedMarketsReader_AllPoolsCachesByKey(t *testing.T) {
	up := &fakeMarketsReader{}
	c := NewCachedMarketsReader(up, 60*time.Second)
	filter := timescale.PoolsFilter{Sources: []string{"aquarius"}}

	for i := 0; i < 4; i++ {
		_, _, err := c.AllPools(context.Background(), filter, "", 50, timescale.MarketsOrderVolume24hDesc)
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := up.allPoolsCalls.Load(); got != 1 {
		t.Errorf("upstream called %d times for same key; want 1", got)
	}
}

func TestCachedMarketsReader_AllPoolsDifferentKeys(t *testing.T) {
	up := &fakeMarketsReader{}
	c := NewCachedMarketsReader(up, 60*time.Second)

	_, _, _ = c.AllPools(context.Background(),
		timescale.PoolsFilter{Sources: []string{"aquarius"}}, "", 50, timescale.MarketsOrderVolume24hDesc)
	_, _, _ = c.AllPools(context.Background(),
		timescale.PoolsFilter{Sources: []string{"phoenix"}}, "", 50, timescale.MarketsOrderVolume24hDesc)
	if got := up.allPoolsCalls.Load(); got != 2 {
		t.Errorf("upstream called %d times across 2 distinct keys; want 2", got)
	}
}

func TestCachedMarketsReader_AllPoolsSingleFlight(t *testing.T) {
	up := &fakeMarketsReader{delay: 100 * time.Millisecond}
	c := NewCachedMarketsReader(up, 60*time.Second)
	filter := timescale.PoolsFilter{Sources: []string{"aquarius"}}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _ = c.AllPools(context.Background(), filter, "", 50, timescale.MarketsOrderVolume24hDesc)
		}()
	}
	wg.Wait()

	if got := up.allPoolsCalls.Load(); got != 1 {
		t.Errorf("upstream called %d times under single-flight; want 1", got)
	}
}

func TestCachedMarketsReader_AllPoolsErrorIsNotCached(t *testing.T) {
	up := &fakeMarketsReader{err: errors.New("db down")}
	c := NewCachedMarketsReader(up, 60*time.Second)
	filter := timescale.PoolsFilter{Sources: []string{"aquarius"}}

	_, _, err := c.AllPools(context.Background(), filter, "", 50, timescale.MarketsOrderVolume24hDesc)
	if err == nil {
		t.Fatal("first call: want error")
	}
	up.err = nil
	_, _, err = c.AllPools(context.Background(), filter, "", 50, timescale.MarketsOrderVolume24hDesc)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if got := up.allPoolsCalls.Load(); got != 2 {
		t.Errorf("upstream called %d times; want 2 (error wasn't cached)", got)
	}
}

// readCacheCounter pulls the current ratesengine_api_cache_ops_total
// value for one (cache, op, result) combination. Returns 0 when the
// label set hasn't been incremented yet (Prometheus auto-creates on
// first .Inc()). Lets the metric tests read absolute values without
// depending on test ordering.
func readCacheCounter(t *testing.T, cache, op, result string) float64 {
	t.Helper()
	m := &dto.Metric{}
	if err := obs.APICacheOpsTotal.WithLabelValues(cache, op, result).Write(m); err != nil {
		t.Fatalf("metric write: %v", err)
	}
	return m.Counter.GetValue()
}

// TestCachedMarketsReader_HitMissCounter pins the contract:
// AllPools' miss-on-first-call + hit-on-repeat-call increments the
// ratesengine_api_cache_ops_total counter on the right label set.
// Detection target: a future refactor that drops the metric inc on
// either branch. Three earlier session bugs (#1185 / #1194 / #1195)
// were prewarm-key drifts; this test guards the OBSERVABILITY of
// future drifts by ensuring the counter actually moves.
func TestCachedMarketsReader_HitMissCounter(t *testing.T) {
	up := &fakeMarketsReader{}
	c := NewCachedMarketsReader(up, 60*time.Second)
	filter := timescale.PoolsFilter{Sources: []string{"aquarius"}}

	// Counters are process-global; capture the baseline so we can
	// assert deltas instead of absolute values (other tests + parallel
	// runs increment the same counter).
	missBefore := readCacheCounter(t, "markets", "all_pools", "miss")
	hitBefore := readCacheCounter(t, "markets", "all_pools", "hit")

	// First call → miss (+1 miss, +0 hit).
	if _, _, err := c.AllPools(context.Background(), filter, "", 50, timescale.MarketsOrderVolume24hDesc); err != nil {
		t.Fatal(err)
	}
	// Second call → hit (+0 miss, +1 hit).
	if _, _, err := c.AllPools(context.Background(), filter, "", 50, timescale.MarketsOrderVolume24hDesc); err != nil {
		t.Fatal(err)
	}

	missDelta := readCacheCounter(t, "markets", "all_pools", "miss") - missBefore
	hitDelta := readCacheCounter(t, "markets", "all_pools", "hit") - hitBefore

	if missDelta != 1 {
		t.Errorf("miss counter delta = %v, want 1", missDelta)
	}
	if hitDelta != 1 {
		t.Errorf("hit counter delta = %v, want 1", hitDelta)
	}
}

// TestCachedMarketsReader_AllPoolsLeaderFailsWaitersDontPanic pins the
// regression for a runtime panic observed on r1 production
// (2026-05-10 15:36:20 UTC, GET /v1/markets):
//
//	panic: runtime error: invalid memory address or nil pointer
//	dereference
//	  …markets_cache.go: out := c.entries[key]
//	  …                  return out.pairs, out.cursor, nil
//
// Root cause: under single-flight, the leader's failing upstream call
// removed the entry from the map (we don't TTL-cache errors) BEFORE
// closing the flight chan. Waiters then woke and re-read
// c.entries[key], got nil, and derefed `out.pairs`.
//
// Fix: waiters hold a pointer to the SAME entry they joined on and
// read entry.err / entry.pairs there, surviving the leader's delete.
func TestCachedMarketsReader_AllPoolsLeaderFailsWaitersDontPanic(t *testing.T) {
	up := &fakeMarketsReader{
		delay: 100 * time.Millisecond,
		err:   errors.New("simulated db down"),
	}
	c := NewCachedMarketsReader(up, 60*time.Second)
	filter := timescale.PoolsFilter{Sources: []string{"aquarius"}}

	// Fire the leader plus 9 waiters concurrently. With the bug
	// present, at least one waiter would panic on out.pairs deref.
	var wg sync.WaitGroup
	results := make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					results <- errors.New("panic: " + toString(r))
				}
			}()
			_, _, err := c.AllPools(context.Background(), filter, "", 50, timescale.MarketsOrderVolume24hDesc)
			results <- err
		}()
	}
	wg.Wait()
	close(results)

	gotErrs := 0
	for err := range results {
		if err == nil {
			t.Errorf("want error from every caller, got nil")
			continue
		}
		if err.Error() == "simulated db down" || err.Error() == `panic: not allowed` {
			gotErrs++
			continue
		}
		// Anything else (especially "panic: ...") is a regression.
		if len(err.Error()) >= 6 && err.Error()[:6] == "panic:" {
			t.Errorf("waiter panicked: %v", err)
			continue
		}
		// Wrapped errors are fine as long as they aren't panics.
		gotErrs++
	}
	if gotErrs == 0 {
		t.Fatal("no callers returned an error; want all 10")
	}
	if got := up.allPoolsCalls.Load(); got != 1 {
		t.Errorf("upstream called %d times under single-flight; want 1", got)
	}
}

// toString renders a recovered panic value for the regression test
// above. Avoids a fmt import in the production path.
func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if e, ok := v.(error); ok {
		return e.Error()
	}
	return "unknown"
}
