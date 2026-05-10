package v1

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

type fakeUpstream struct {
	statsCalls atomic.Int64
	histCalls  atomic.Int64
	statsDelay time.Duration
	statsErr   error
}

func (f *fakeUpstream) GetSourceStats(ctx context.Context) ([]timescale.SourceStats, error) {
	f.statsCalls.Add(1)
	if f.statsDelay > 0 {
		select {
		case <-time.After(f.statsDelay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f.statsErr != nil {
		return nil, f.statsErr
	}
	return []timescale.SourceStats{{Source: "binance", TradeCount24h: 42}}, nil
}

func (f *fakeUpstream) GetSourceVolumeHistory24h(ctx context.Context) ([]timescale.SourceVolumeBucket, error) {
	f.histCalls.Add(1)
	return []timescale.SourceVolumeBucket{{Source: "binance", Hour: time.Now()}}, nil
}

// TestCachedSourcesStatsReader_HitsCachedValue — once warmed, the
// upstream must NOT be called again within the TTL window.
func TestCachedSourcesStatsReader_HitsCachedValue(t *testing.T) {
	up := &fakeUpstream{}
	c := NewCachedSourcesStatsReader(up, 60*time.Second)
	for i := 0; i < 5; i++ {
		_, err := c.GetSourceStats(context.Background())
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := up.statsCalls.Load(); got != 1 {
		t.Errorf("upstream called %d times; want 1 (4 cache hits expected)", got)
	}
}

// TestCachedSourcesStatsReader_RefetchesAfterTTL — after the TTL
// window the next call must hit upstream again.
func TestCachedSourcesStatsReader_RefetchesAfterTTL(t *testing.T) {
	up := &fakeUpstream{}
	c := NewCachedSourcesStatsReader(up, 50*time.Millisecond)
	if _, err := c.GetSourceStats(context.Background()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(70 * time.Millisecond)
	if _, err := c.GetSourceStats(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := up.statsCalls.Load(); got != 2 {
		t.Errorf("upstream called %d times; want 2 (one before + one after TTL)", got)
	}
}

// TestCachedSourcesStatsReader_SingleFlight — concurrent calls
// during a slow upstream refetch must share ONE upstream call.
// This is the property that protects the DB during a thundering-
// herd page load.
func TestCachedSourcesStatsReader_SingleFlight(t *testing.T) {
	up := &fakeUpstream{statsDelay: 100 * time.Millisecond}
	c := NewCachedSourcesStatsReader(up, 60*time.Second)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.GetSourceStats(context.Background()); err != nil {
				t.Errorf("call: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := up.statsCalls.Load(); got != 1 {
		t.Errorf("upstream called %d times under single-flight; want 1", got)
	}
}

// TestCachedSourcesStatsReader_TTLZeroIsBypass — ttl=0 disables
// the cache entirely. Every call hits upstream. Useful for tests
// that want the wrapper inert.
func TestCachedSourcesStatsReader_TTLZeroIsBypass(t *testing.T) {
	up := &fakeUpstream{}
	c := NewCachedSourcesStatsReader(up, 0)
	for i := 0; i < 3; i++ {
		_, _ = c.GetSourceStats(context.Background())
	}
	if got := up.statsCalls.Load(); got != 3 {
		t.Errorf("upstream called %d times; want 3 (no caching at ttl=0)", got)
	}
}

// TestCachedSourcesStatsReader_ErrorIsNotCached — if upstream
// errors, the cache should NOT remember the error. Next caller
// retries.
func TestCachedSourcesStatsReader_ErrorIsNotCached(t *testing.T) {
	up := &fakeUpstream{statsErr: errors.New("db is down")}
	c := NewCachedSourcesStatsReader(up, 60*time.Second)

	if _, err := c.GetSourceStats(context.Background()); err == nil {
		t.Fatal("first call: want error, got nil")
	}
	up.statsErr = nil
	if _, err := c.GetSourceStats(context.Background()); err != nil {
		t.Fatalf("second call: %v", err)
	}
	// Second call must have hit upstream (error wasn't cached).
	if got := up.statsCalls.Load(); got != 2 {
		t.Errorf("upstream called %d times; want 2", got)
	}
}

// TestCachedSourcesStatsReader_HitMissCounter pins the
// ratesengine_api_cache_ops_total{cache="sources_stats"} counter
// for both ops on the wrapper. Same regression-guard rationale as
// the markets + coins variants — if a future refactor drops the
// .Inc() on either branch the alert from #1197 silently stops
// firing for these surfaces.
func TestCachedSourcesStatsReader_HitMissCounter(t *testing.T) {
	for _, tc := range []struct {
		op   string
		call func(c *CachedSourcesStatsReader) error
	}{
		{"source_stats", func(c *CachedSourcesStatsReader) error {
			_, err := c.GetSourceStats(context.Background())
			return err
		}},
		{"volume_history_24h", func(c *CachedSourcesStatsReader) error {
			_, err := c.GetSourceVolumeHistory24h(context.Background())
			return err
		}},
	} {
		t.Run(tc.op, func(t *testing.T) {
			up := &fakeUpstream{}
			c := NewCachedSourcesStatsReader(up, 60*time.Second)

			missBefore := readCacheCounter(t, "sources_stats", tc.op, "miss")
			hitBefore := readCacheCounter(t, "sources_stats", tc.op, "hit")

			if err := tc.call(c); err != nil {
				t.Fatal(err)
			}
			if err := tc.call(c); err != nil {
				t.Fatal(err)
			}

			if delta := readCacheCounter(t, "sources_stats", tc.op, "miss") - missBefore; delta != 1 {
				t.Errorf("miss counter delta = %v, want 1", delta)
			}
			if delta := readCacheCounter(t, "sources_stats", tc.op, "hit") - hitBefore; delta != 1 {
				t.Errorf("hit counter delta = %v, want 1", delta)
			}
		})
	}
}
