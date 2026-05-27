package v1

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// fakeOracleUpstream is the in-memory stub the cache wrapper sits
// in front of. Counts calls per method so tests can assert hit/miss
// behaviour without scraping Prometheus.
type fakeOracleUpstream struct {
	updatesCalls atomic.Int64
	streamsCalls atomic.Int64

	updatesDelay time.Duration
	updatesErr   error

	updates []canonical.OracleUpdate
}

func (f *fakeOracleUpstream) LatestOracleUpdatesForAsset(ctx context.Context, asset canonical.Asset, sourceFilter string) ([]canonical.OracleUpdate, error) {
	return f.LatestOracleUpdatesForAssets(ctx, []canonical.Asset{asset}, sourceFilter)
}

func (f *fakeOracleUpstream) LatestOracleUpdatesForAssets(ctx context.Context, _ []canonical.Asset, _ string) ([]canonical.OracleUpdate, error) {
	f.updatesCalls.Add(1)
	if f.updatesDelay > 0 {
		select {
		case <-time.After(f.updatesDelay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f.updatesErr != nil {
		return nil, f.updatesErr
	}
	return f.updates, nil
}

func (f *fakeOracleUpstream) LatestOracleStreams(_ context.Context) ([]canonical.OracleUpdate, error) {
	f.streamsCalls.Add(1)
	return nil, nil
}

func nativeAssets(t *testing.T) []canonical.Asset {
	t.Helper()
	out := []canonical.Asset{canonical.NativeAsset()}
	if x, err := canonical.ParseAsset("crypto:XLM"); err == nil {
		out = append(out, x)
	}
	return out
}

func TestCachedOracleReader_HitsCachedValue(t *testing.T) {
	up := &fakeOracleUpstream{}
	c := NewCachedOracleReader(up, 5*time.Second)
	for i := 0; i < 5; i++ {
		if _, err := c.LatestOracleUpdatesForAssets(context.Background(), nativeAssets(t), ""); err != nil {
			t.Fatal(err)
		}
	}
	if got := up.updatesCalls.Load(); got != 1 {
		t.Errorf("upstream called %d times; want 1 (4 hits expected)", got)
	}
}

func TestCachedOracleReader_RefetchesAfterTTL(t *testing.T) {
	up := &fakeOracleUpstream{}
	c := NewCachedOracleReader(up, 30*time.Millisecond)
	if _, err := c.LatestOracleUpdatesForAssets(context.Background(), nativeAssets(t), ""); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if _, err := c.LatestOracleUpdatesForAssets(context.Background(), nativeAssets(t), ""); err != nil {
		t.Fatal(err)
	}
	if got := up.updatesCalls.Load(); got != 2 {
		t.Errorf("upstream called %d times; want 2", got)
	}
}

func TestCachedOracleReader_KeyOrderInvariant(t *testing.T) {
	// Same asset set in different order should hit the same cache
	// slot. The cache key normalises by sorting the asset strkeys.
	up := &fakeOracleUpstream{}
	c := NewCachedOracleReader(up, 5*time.Second)

	native := canonical.NativeAsset()
	crypto, err := canonical.ParseAsset("crypto:XLM")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.LatestOracleUpdatesForAssets(context.Background(), []canonical.Asset{native, crypto}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := c.LatestOracleUpdatesForAssets(context.Background(), []canonical.Asset{crypto, native}, ""); err != nil {
		t.Fatal(err)
	}
	if got := up.updatesCalls.Load(); got != 1 {
		t.Errorf("upstream called %d times for permuted-but-equal keys; want 1", got)
	}
}

func TestCachedOracleReader_SourceFilterDistinguishesSlots(t *testing.T) {
	// Same assets, different source filter → distinct cache slots.
	up := &fakeOracleUpstream{}
	c := NewCachedOracleReader(up, 5*time.Second)

	if _, err := c.LatestOracleUpdatesForAssets(context.Background(), nativeAssets(t), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := c.LatestOracleUpdatesForAssets(context.Background(), nativeAssets(t), "reflector-dex"); err != nil {
		t.Fatal(err)
	}
	if got := up.updatesCalls.Load(); got != 2 {
		t.Errorf("upstream called %d times; want 2 (distinct source-filter keys)", got)
	}
}

func TestCachedOracleReader_TTLZeroBypassesCache(t *testing.T) {
	up := &fakeOracleUpstream{}
	c := NewCachedOracleReader(up, 0) // ttl=0 → disabled

	for i := 0; i < 3; i++ {
		if _, err := c.LatestOracleUpdatesForAssets(context.Background(), nativeAssets(t), ""); err != nil {
			t.Fatal(err)
		}
	}
	if got := up.updatesCalls.Load(); got != 3 {
		t.Errorf("upstream called %d times; want 3 (cache disabled)", got)
	}
}

func TestCachedOracleReader_SingleFlightUnderConcurrentMiss(t *testing.T) {
	// 10 concurrent callers during a slow cold-miss should produce
	// exactly 1 upstream call. Mirrors the F-0011 single-flight
	// guarantee — the cache MUST collapse stampede.
	up := &fakeOracleUpstream{updatesDelay: 80 * time.Millisecond}
	c := NewCachedOracleReader(up, 5*time.Second)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = c.LatestOracleUpdatesForAssets(context.Background(), nativeAssets(t), "")
		}()
	}
	wg.Wait()
	if got := up.updatesCalls.Load(); got != 1 {
		t.Errorf("upstream called %d times under concurrent cold-miss; want 1 (single-flight)", got)
	}
}

func TestCachedOracleReader_DoesNotCacheError(t *testing.T) {
	// Errors must not be cached. Subsequent callers should retry
	// upstream rather than see the stale error.
	upErr := errors.New("oracle: storage transient")
	up := &fakeOracleUpstream{updatesErr: upErr}
	c := NewCachedOracleReader(up, 5*time.Second)

	if _, err := c.LatestOracleUpdatesForAssets(context.Background(), nativeAssets(t), ""); !errors.Is(err, upErr) {
		t.Fatalf("first call err = %v; want %v", err, upErr)
	}
	if _, err := c.LatestOracleUpdatesForAssets(context.Background(), nativeAssets(t), ""); !errors.Is(err, upErr) {
		t.Fatalf("second call err = %v; want %v", err, upErr)
	}
	// Both calls must hit upstream — error wasn't cached.
	if got := up.updatesCalls.Load(); got != 2 {
		t.Errorf("upstream called %d times under repeated error; want 2", got)
	}
}

func TestCachedOracleReader_StreamsIsPassthrough(t *testing.T) {
	up := &fakeOracleUpstream{}
	c := NewCachedOracleReader(up, 5*time.Second)
	for i := 0; i < 4; i++ {
		if _, err := c.LatestOracleStreams(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if got := up.streamsCalls.Load(); got != 4 {
		t.Errorf("upstream LatestOracleStreams called %d times; want 4 (pass-through, no cache)", got)
	}
	if got := up.updatesCalls.Load(); got != 0 {
		t.Errorf("upstream LatestOracleUpdatesForAssets called %d times during streams test; want 0", got)
	}
}
