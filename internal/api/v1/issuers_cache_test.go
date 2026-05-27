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

// fakeIssuersUpstream is the in-memory stub the cache wrapper sits
// in front of. Counts calls per method so tests can assert hit/miss
// behaviour without poking the Prometheus counter.
type fakeIssuersUpstream struct {
	listCalls   atomic.Int64
	getCalls    atomic.Int64
	assetsCalls atomic.Int64

	listDelay time.Duration
	listErr   error

	rows []timescale.IssuerSummary
}

func (f *fakeIssuersUpstream) GetIssuer(_ context.Context, gStrkey string) (timescale.IssuerRow, error) {
	f.getCalls.Add(1)
	return timescale.IssuerRow{GStrkey: gStrkey}, nil
}

func (f *fakeIssuersUpstream) ListIssuerAssets(_ context.Context, _ string) ([]timescale.IssuerAsset, error) {
	f.assetsCalls.Add(1)
	return nil, nil
}

func (f *fakeIssuersUpstream) ListIssuers(ctx context.Context, limit int) ([]timescale.IssuerSummary, error) {
	f.listCalls.Add(1)
	if f.listDelay > 0 {
		select {
		case <-time.After(f.listDelay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f.listErr != nil {
		return nil, f.listErr
	}
	if f.rows != nil {
		return f.rows, nil
	}
	return []timescale.IssuerSummary{
		{GStrkey: "GA1", HomeDomain: "centre.io", AssetCount: 1, TotalObservationCount: 100},
	}, nil
}

// TestCachedIssuersReader_HitsCachedValue — once warmed the upstream
// must NOT be called again within the TTL window. This is the cache-
// aside path F-0011 was about: the second request must NOT pay the
// 196ms+ HashAggregate scan.
func TestCachedIssuersReader_HitsCachedValue(t *testing.T) {
	up := &fakeIssuersUpstream{}
	c := NewCachedIssuersReader(up, 5*time.Minute)
	for i := 0; i < 5; i++ {
		if _, err := c.ListIssuers(context.Background(), 100); err != nil {
			t.Fatal(err)
		}
	}
	if got := up.listCalls.Load(); got != 1 {
		t.Errorf("upstream ListIssuers called %d times; want 1 (4 cache hits expected)", got)
	}
}

// TestCachedIssuersReader_RefetchesAfterTTL — after the TTL window
// the next call must hit upstream again.
func TestCachedIssuersReader_RefetchesAfterTTL(t *testing.T) {
	up := &fakeIssuersUpstream{}
	c := NewCachedIssuersReader(up, 50*time.Millisecond)
	if _, err := c.ListIssuers(context.Background(), 100); err != nil {
		t.Fatal(err)
	}
	time.Sleep(70 * time.Millisecond)
	if _, err := c.ListIssuers(context.Background(), 100); err != nil {
		t.Fatal(err)
	}
	if got := up.listCalls.Load(); got != 2 {
		t.Errorf("upstream ListIssuers called %d times; want 2 (one before + one after TTL)", got)
	}
}

// TestCachedIssuersReader_SingleFlight — concurrent callers during a
// slow upstream refetch share ONE upstream call. This is the
// thundering-herd protection that matters for /v1/issuers post-cache
// expiry under explorer pageload fanout.
func TestCachedIssuersReader_SingleFlight(t *testing.T) {
	up := &fakeIssuersUpstream{listDelay: 100 * time.Millisecond}
	c := NewCachedIssuersReader(up, 5*time.Minute)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.ListIssuers(context.Background(), 100); err != nil {
				t.Errorf("call: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := up.listCalls.Load(); got != 1 {
		t.Errorf("upstream ListIssuers called %d times under single-flight; want 1", got)
	}
}

// TestCachedIssuersReader_DistinctLimitsCacheSeparately — limit is
// part of the cache key, so two different limits are independent
// slots and each pays one upstream call.
func TestCachedIssuersReader_DistinctLimitsCacheSeparately(t *testing.T) {
	up := &fakeIssuersUpstream{}
	c := NewCachedIssuersReader(up, 5*time.Minute)

	if _, err := c.ListIssuers(context.Background(), 25); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListIssuers(context.Background(), 100); err != nil {
		t.Fatal(err)
	}
	// Same key twice → still cached.
	if _, err := c.ListIssuers(context.Background(), 100); err != nil {
		t.Fatal(err)
	}
	if got := up.listCalls.Load(); got != 2 {
		t.Errorf("upstream called %d times; want 2 (one per distinct limit)", got)
	}
}

// TestCachedIssuersReader_TTLZeroIsBypass — ttl=0 disables the
// cache. Every call hits upstream. Mirrors the
// CachedSourcesStatsReader/CachedCoinsReader knob used in
// integration tests that want the wrapper inert.
func TestCachedIssuersReader_TTLZeroIsBypass(t *testing.T) {
	up := &fakeIssuersUpstream{}
	c := NewCachedIssuersReader(up, 0)
	for i := 0; i < 3; i++ {
		_, _ = c.ListIssuers(context.Background(), 100)
	}
	if got := up.listCalls.Load(); got != 3 {
		t.Errorf("upstream called %d times; want 3 (no caching at ttl=0)", got)
	}
}

// TestCachedIssuersReader_ErrorIsNotCached — if upstream errors, the
// cache MUST forget so the next caller retries. Caching the error
// would freeze us into 500-loops after a transient PG hiccup.
func TestCachedIssuersReader_ErrorIsNotCached(t *testing.T) {
	up := &fakeIssuersUpstream{listErr: errors.New("db is down")}
	c := NewCachedIssuersReader(up, 5*time.Minute)

	if _, err := c.ListIssuers(context.Background(), 100); err == nil {
		t.Fatal("first call: want error, got nil")
	}
	up.listErr = nil
	if _, err := c.ListIssuers(context.Background(), 100); err != nil {
		t.Fatalf("second call: %v", err)
	}
	if got := up.listCalls.Load(); got != 2 {
		t.Errorf("upstream called %d times; want 2 (error not cached → retry)", got)
	}
}

// TestCachedIssuersReader_PassThroughGetIssuer — GetIssuer is
// pass-through (per-row PK lookup is already sub-ms, caching by
// g_strkey gives no shared-callers win). Every call hits upstream.
func TestCachedIssuersReader_PassThroughGetIssuer(t *testing.T) {
	up := &fakeIssuersUpstream{}
	c := NewCachedIssuersReader(up, 5*time.Minute)
	for i := 0; i < 3; i++ {
		if _, err := c.GetIssuer(context.Background(), "GA1"); err != nil {
			t.Fatal(err)
		}
	}
	if got := up.getCalls.Load(); got != 3 {
		t.Errorf("GetIssuer called %d times; want 3 (pass-through)", got)
	}
}

// TestCachedIssuersReader_PassThroughListAssets — ListIssuerAssets
// is pass-through for the same reason as GetIssuer (indexed lookup,
// already cheap).
func TestCachedIssuersReader_PassThroughListAssets(t *testing.T) {
	up := &fakeIssuersUpstream{}
	c := NewCachedIssuersReader(up, 5*time.Minute)
	for i := 0; i < 3; i++ {
		if _, err := c.ListIssuerAssets(context.Background(), "GA1"); err != nil {
			t.Fatal(err)
		}
	}
	if got := up.assetsCalls.Load(); got != 3 {
		t.Errorf("ListIssuerAssets called %d times; want 3 (pass-through)", got)
	}
}

// TestCachedIssuersReader_HitMissCounter pins the
// ratesengine_api_cache_ops_total{cache="issuers"} counter for the
// list_issuers op. Same regression-guard rationale as the markets +
// coins + sources_stats variants — if a future refactor drops the
// .Inc() on either branch the api_cache_miss_rate_high alert
// silently stops firing for /v1/issuers.
func TestCachedIssuersReader_HitMissCounter(t *testing.T) {
	up := &fakeIssuersUpstream{}
	c := NewCachedIssuersReader(up, 5*time.Minute)

	missBefore := readCacheCounter(t, "issuers", "list_issuers", "miss")
	hitBefore := readCacheCounter(t, "issuers", "list_issuers", "hit")

	if _, err := c.ListIssuers(context.Background(), 100); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListIssuers(context.Background(), 100); err != nil {
		t.Fatal(err)
	}

	if delta := readCacheCounter(t, "issuers", "list_issuers", "miss") - missBefore; delta != 1 {
		t.Errorf("miss counter delta = %v, want 1", delta)
	}
	if delta := readCacheCounter(t, "issuers", "list_issuers", "hit") - hitBefore; delta != 1 {
		t.Errorf("hit counter delta = %v, want 1", delta)
	}
}

// BenchmarkCachedIssuersReader_ListIssuers — informal perf guard. A
// cached read should complete in microseconds while the upstream
// stub takes 5ms (a tiny fraction of the real PG 196ms). If a
// future refactor breaks the fast-path the benchmark surfaces it
// loudly. Not gated in CI; run manually via `go test -bench .`.
func BenchmarkCachedIssuersReader_ListIssuers(b *testing.B) {
	up := &fakeIssuersUpstream{listDelay: 5 * time.Millisecond}
	c := NewCachedIssuersReader(up, 5*time.Minute)
	// Prime the cache so the loop measures hits, not the cold miss.
	if _, err := c.ListIssuers(context.Background(), 100); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := c.ListIssuers(context.Background(), 100); err != nil {
			b.Fatal(err)
		}
	}
}
