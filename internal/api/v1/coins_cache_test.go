package v1

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/storage/timescale"
)

// fakeCoinsUpstream stubs every CoinsReader method with a per-call
// counter so tests can assert how many times the underlying call
// reached upstream vs. served from cache.
type fakeCoinsUpstream struct {
	listCalls    atomic.Int64
	hist24hCalls atomic.Int64
	hist7dCalls  atomic.Int64
}

func (f *fakeCoinsUpstream) ListCoinsExt(ctx context.Context, opts timescale.ListCoinsOptions) ([]timescale.CoinRow, error) {
	f.listCalls.Add(1)
	return []timescale.CoinRow{{AssetID: "native"}}, nil
}

func (f *fakeCoinsUpstream) GetCoinsPriceHistory24hBatch(ctx context.Context, ids []string) (map[string][]timescale.CoinPricePoint, error) {
	f.hist24hCalls.Add(1)
	return map[string][]timescale.CoinPricePoint{"native": nil}, nil
}

func (f *fakeCoinsUpstream) GetCoinsPriceHistory7dBatch(ctx context.Context, ids []string) (map[string][]timescale.CoinPricePoint, error) {
	f.hist7dCalls.Add(1)
	return map[string][]timescale.CoinPricePoint{"native": nil}, nil
}

func (f *fakeCoinsUpstream) GetCoinsATHBatch(ctx context.Context, ids []string) (map[string]timescale.CoinATH, error) {
	return nil, nil
}

func (f *fakeCoinsUpstream) GetCoinBySlug(ctx context.Context, slug string) (timescale.CoinRow, error) {
	return timescale.CoinRow{AssetID: slug}, nil
}

func (f *fakeCoinsUpstream) GetCoinByAssetID(ctx context.Context, assetID string) (timescale.CoinRow, error) {
	return timescale.CoinRow{AssetID: assetID}, nil
}

func (f *fakeCoinsUpstream) GetNativeCoinRow(ctx context.Context) (timescale.CoinRow, error) {
	return timescale.CoinRow{AssetID: "native"}, nil
}

func (f *fakeCoinsUpstream) GetCoinTopMarkets(ctx context.Context, assetID string, limit int) ([]timescale.CoinTopMarket, error) {
	return nil, nil
}

func (f *fakeCoinsUpstream) GetCoinPriceHistory24h(ctx context.Context, assetID string) ([]timescale.CoinPricePoint, error) {
	return nil, nil
}

func (f *fakeCoinsUpstream) GetCoinPriceHistory7d(ctx context.Context, assetID string) ([]timescale.CoinPricePoint, error) {
	return nil, nil
}

func (f *fakeCoinsUpstream) GetCoinMarketsCount(ctx context.Context, assetID string) (int64, error) {
	return 0, nil
}

func (f *fakeCoinsUpstream) GetCoinATH(ctx context.Context, assetID string) (*timescale.CoinATH, error) {
	return nil, nil
}

func (f *fakeCoinsUpstream) GetCoinTradeCount24h(ctx context.Context, assetID string) (int64, error) {
	return 0, nil
}

// TestCachedCoinsReader_ListCoinsExtHitMissCounter pins both the
// upstream-call dedup behaviour AND the
// ratesengine_api_cache_ops_total{cache="coins",op="list_coins"}
// counter deltas in one test. First call is a miss (+1 miss), the
// repeat call is a hit (+1 hit) and must NOT call upstream a
// second time.
func TestCachedCoinsReader_ListCoinsExtHitMissCounter(t *testing.T) {
	up := &fakeCoinsUpstream{}
	c := NewCachedCoinsReader(up, 60*time.Second)
	opts := timescale.ListCoinsOptions{Limit: 50}

	missBefore := readCacheCounter(t, "coins", "list_coins", "miss")
	hitBefore := readCacheCounter(t, "coins", "list_coins", "hit")

	if _, err := c.ListCoinsExt(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListCoinsExt(context.Background(), opts); err != nil {
		t.Fatal(err)
	}

	if got := up.listCalls.Load(); got != 1 {
		t.Errorf("upstream called %d times for same key; want 1", got)
	}
	if delta := readCacheCounter(t, "coins", "list_coins", "miss") - missBefore; delta != 1 {
		t.Errorf("miss counter delta = %v, want 1", delta)
	}
	if delta := readCacheCounter(t, "coins", "list_coins", "hit") - hitBefore; delta != 1 {
		t.Errorf("hit counter delta = %v, want 1", delta)
	}
}

// TestCachedCoinsReader_HistoryBatchHitMissCounter exercises both
// the 24h and 7d history-batch caches in one go. They share the
// generic fetchHistoryMap helper; this test catches a regression
// where one branch (24h vs 7d) silently drops the metric inc.
func TestCachedCoinsReader_HistoryBatchHitMissCounter(t *testing.T) {
	up := &fakeCoinsUpstream{}
	c := NewCachedCoinsReader(up, 60*time.Second)
	ids := []string{"native"}

	for _, tc := range []struct {
		op   string
		call func() error
	}{
		{"price_history_24h", func() error {
			_, err := c.GetCoinsPriceHistory24hBatch(context.Background(), ids)
			return err
		}},
		{"price_history_7d", func() error {
			_, err := c.GetCoinsPriceHistory7dBatch(context.Background(), ids)
			return err
		}},
	} {
		t.Run(tc.op, func(t *testing.T) {
			missBefore := readCacheCounter(t, "coins", tc.op, "miss")
			hitBefore := readCacheCounter(t, "coins", tc.op, "hit")

			if err := tc.call(); err != nil {
				t.Fatal(err)
			}
			if err := tc.call(); err != nil {
				t.Fatal(err)
			}
			if delta := readCacheCounter(t, "coins", tc.op, "miss") - missBefore; delta != 1 {
				t.Errorf("miss counter delta = %v, want 1", delta)
			}
			if delta := readCacheCounter(t, "coins", tc.op, "hit") - hitBefore; delta != 1 {
				t.Errorf("hit counter delta = %v, want 1", delta)
			}
		})
	}
}

// TestCachedCoinsReader_TTLZeroBypasses pins that ttl=0 disables
// the cache entirely (every call goes through to upstream + no
// metric counted, since the pre-cache code path doesn't hit the
// fetch helper).
func TestCachedCoinsReader_TTLZeroBypasses(t *testing.T) {
	up := &fakeCoinsUpstream{}
	c := NewCachedCoinsReader(up, 0)

	for i := 0; i < 3; i++ {
		if _, err := c.ListCoinsExt(context.Background(), timescale.ListCoinsOptions{Limit: 50}); err != nil {
			t.Fatal(err)
		}
	}
	if got := up.listCalls.Load(); got != 3 {
		t.Errorf("upstream called %d times with ttl=0; want 3 (cache must be bypassed)", got)
	}
}

// swrCoinsUpstream is a configurable ListCoinsExt stub for the
// stale-while-revalidate tests: a per-call counter plus a settable
// delay / return value / failure. Embeds fakeCoinsUpstream so the
// other CoinsReader methods are satisfied.
type swrCoinsUpstream struct {
	*fakeCoinsUpstream
	calls atomic.Int64
	mu    sync.Mutex
	delay time.Duration
	val   string
	fail  bool
}

func (s *swrCoinsUpstream) ListCoinsExt(ctx context.Context, _ timescale.ListCoinsOptions) ([]timescale.CoinRow, error) {
	s.calls.Add(1)
	s.mu.Lock()
	d, v, f := s.delay, s.val, s.fail
	s.mu.Unlock()
	if d > 0 {
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f {
		return nil, errors.New("swr upstream boom")
	}
	return []timescale.CoinRow{{AssetID: v}}, nil
}

func (s *swrCoinsUpstream) set(delay time.Duration, val string, fail bool) {
	s.mu.Lock()
	s.delay, s.val, s.fail = delay, val, fail
	s.mu.Unlock()
}

func swrGet(t *testing.T, c *CachedCoinsReader) (string, error) {
	t.Helper()
	rows, err := c.ListCoinsExt(context.Background(), timescale.ListCoinsOptions{Limit: 7})
	if err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", nil
	}
	return rows[0].AssetID, nil
}

func waitFor(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}

// TestCachedCoinsReader_SWRServesStaleAndRefreshes: an expired entry
// returns the stale value IMMEDIATELY (not blocked on the slow
// upstream refetch — the #22 fix), a single background refresh runs,
// and afterwards the fresh value is served.
func TestCachedCoinsReader_SWRServesStaleAndRefreshes(t *testing.T) {
	up := &swrCoinsUpstream{fakeCoinsUpstream: &fakeCoinsUpstream{}, val: "v1"}
	c := NewCachedCoinsReader(up, 25*time.Millisecond)

	if v, err := swrGet(t, c); err != nil || v != "v1" {
		t.Fatalf("cold fetch: got %q err=%v, want v1", v, err)
	}
	if up.calls.Load() != 1 {
		t.Fatalf("cold calls=%d, want 1", up.calls.Load())
	}

	time.Sleep(50 * time.Millisecond)         // let it expire
	up.set(300*time.Millisecond, "v2", false) // slow refresh, new value

	start := time.Now()
	v, err := swrGet(t, c)
	elapsed := time.Since(start)
	if err != nil || v != "v1" {
		t.Fatalf("SWR must serve stale v1; got %q err=%v", v, err)
	}
	if elapsed > 120*time.Millisecond {
		t.Fatalf("SWR blocked %v on the 300ms refresh; must serve stale ~instantly", elapsed)
	}
	if !waitFor(2*time.Second, func() bool { return up.calls.Load() == 2 }) {
		t.Fatalf("background refresh not started; calls=%d want 2", up.calls.Load())
	}
	if !waitFor(2*time.Second, func() bool { vv, _ := swrGet(t, c); return vv == "v2" }) {
		t.Fatalf("post-refresh did not serve fresh v2")
	}
}

// TestCachedCoinsReader_SWRSingleFlight: many concurrent reads of an
// expired entry all get stale immediately and trigger EXACTLY ONE
// background refresh (1 cold + 1 refresh = 2 upstream calls), never
// a stampede, regardless of concurrency.
func TestCachedCoinsReader_SWRSingleFlight(t *testing.T) {
	up := &swrCoinsUpstream{fakeCoinsUpstream: &fakeCoinsUpstream{}, val: "v1"}
	c := NewCachedCoinsReader(up, 20*time.Millisecond)

	if _, err := swrGet(t, c); err != nil { // cold → calls=1
		t.Fatal(err)
	}
	time.Sleep(40 * time.Millisecond)         // expire
	up.set(250*time.Millisecond, "v2", false) // slow refresh window

	var wg sync.WaitGroup
	for i := 0; i < 25; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if v, err := swrGet(t, c); err != nil || v != "v1" {
				t.Errorf("concurrent SWR got %q err=%v, want stale v1", v, err)
			}
		}()
	}
	wg.Wait()

	if !waitFor(2*time.Second, func() bool { return up.calls.Load() == 2 }) {
		t.Fatalf("want exactly 2 upstream calls (1 cold + 1 single-flighted refresh); got %d", up.calls.Load())
	}
	time.Sleep(350 * time.Millisecond) // let the 250ms refresh finish; no new reads
	if got := up.calls.Load(); got != 2 {
		t.Fatalf("single-flight violated: %d upstream calls for 25 concurrent stale reads", got)
	}
}

// TestCachedCoinsReader_SWRKeepsStaleOnError: when the background
// refresh fails, the user keeps getting the stale value (never an
// error, never a block) and the refresh is retried on the next
// expired request.
func TestCachedCoinsReader_SWRKeepsStaleOnError(t *testing.T) {
	up := &swrCoinsUpstream{fakeCoinsUpstream: &fakeCoinsUpstream{}, val: "v1"}
	c := NewCachedCoinsReader(up, 20*time.Millisecond)

	if _, err := swrGet(t, c); err != nil { // cold v1, calls=1
		t.Fatal(err)
	}
	time.Sleep(40 * time.Millisecond) // expire
	up.set(0, "v2", true)             // refresh will error (fast)

	v, err := swrGet(t, c)
	if err != nil || v != "v1" {
		t.Fatalf("stale-with-failing-refresh must serve stale v1, no error; got %q err=%v", v, err)
	}
	if !waitFor(2*time.Second, func() bool { return up.calls.Load() == 2 }) {
		t.Fatalf("refresh not attempted; calls=%d want 2", up.calls.Load())
	}

	time.Sleep(40 * time.Millisecond) // re-expire
	v2, err2 := swrGet(t, c)
	if err2 != nil || v2 != "v1" {
		t.Fatalf("after a failed refresh, still serve stale v1 no error; got %q err=%v", v2, err2)
	}
	if !waitFor(2*time.Second, func() bool { return up.calls.Load() >= 3 }) {
		t.Fatalf("failed refresh was not retried on the next request; calls=%d", up.calls.Load())
	}
}

// swrCoinByIDUpstream is a race-safe configurable GetCoinByAssetID
// stub for the generic swr[T] tests (#24): atomic call counter,
// fixed construction-time delay, atomic "fail on call >= 2" toggle
// (deterministic by call number → no mid-test field mutation, so
// `go test -race` is clean under the concurrent background
// refresh). Embeds *fakeCoinsUpstream for the other methods.
type swrCoinByIDUpstream struct {
	*fakeCoinsUpstream
	calls   atomic.Int64
	delay   time.Duration
	failGE2 atomic.Bool
}

func (s *swrCoinByIDUpstream) GetCoinByAssetID(ctx context.Context, assetID string) (timescale.CoinRow, error) {
	n := s.calls.Add(1)
	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			return timescale.CoinRow{}, ctx.Err()
		}
	}
	if n >= 2 && s.failGE2.Load() {
		return timescale.CoinRow{}, errors.New("swr coin boom")
	}
	return timescale.CoinRow{AssetID: assetID}, nil
}

// TestCachedCoinsReader_GenericSWRServesStaleSingleFlight pins the
// generic swr[T] via the now-cached GetCoinByAssetID: an expired
// entry serves stale IMMEDIATELY under heavy concurrency and
// triggers EXACTLY ONE single-flighted background refresh — the
// #24 fix for /v1/assets/{id}.
func TestCachedCoinsReader_GenericSWRServesStaleSingleFlight(t *testing.T) {
	up := &swrCoinByIDUpstream{fakeCoinsUpstream: &fakeCoinsUpstream{}, delay: 300 * time.Millisecond}
	c := NewCachedCoinsReader(up, 25*time.Millisecond)

	if r, err := c.GetCoinByAssetID(context.Background(), "X"); err != nil || r.AssetID != "X" {
		t.Fatalf("cold fetch: got %q err=%v, want X", r.AssetID, err) // blocks ~300ms, calls=1
	}
	if up.calls.Load() != 1 {
		t.Fatalf("cold calls=%d, want 1", up.calls.Load())
	}
	time.Sleep(50 * time.Millisecond) // expire

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st := time.Now()
			r, err := c.GetCoinByAssetID(context.Background(), "X")
			if err != nil || r.AssetID != "X" {
				t.Errorf("SWR must serve stale X no err; got %q err=%v", r.AssetID, err)
			}
			if d := time.Since(st); d > 120*time.Millisecond {
				t.Errorf("SWR blocked %v; must serve stale ~instantly (refresh is 300ms)", d)
			}
		}()
	}
	wg.Wait()

	if !waitFor(2*time.Second, func() bool { return up.calls.Load() == 2 }) {
		t.Fatalf("want exactly 2 upstream calls (1 cold + 1 single-flighted refresh); got %d", up.calls.Load())
	}
	time.Sleep(400 * time.Millisecond) // let the 300ms refresh finish; no new reads
	if got := up.calls.Load(); got != 2 {
		t.Fatalf("single-flight violated: %d upstream calls for 20 concurrent stale reads", got)
	}
}

// TestCachedCoinsReader_GenericSWRKeepsStaleOnError: a failing
// background refresh keeps serving stale (never an error, never a
// block) and is retried on the next expired request.
func TestCachedCoinsReader_GenericSWRKeepsStaleOnError(t *testing.T) {
	up := &swrCoinByIDUpstream{fakeCoinsUpstream: &fakeCoinsUpstream{}}
	up.failGE2.Store(true) // call 1 (cold) OK; call >=2 (refresh) errors
	c := NewCachedCoinsReader(up, 20*time.Millisecond)

	if r, err := c.GetCoinByAssetID(context.Background(), "X"); err != nil || r.AssetID != "X" {
		t.Fatalf("cold: got %q err=%v, want X", r.AssetID, err) // calls=1
	}
	time.Sleep(40 * time.Millisecond) // expire

	r, err := c.GetCoinByAssetID(context.Background(), "X")
	if err != nil || r.AssetID != "X" {
		t.Fatalf("stale-with-failing-refresh must serve stale X no err; got %q err=%v", r.AssetID, err)
	}
	if !waitFor(2*time.Second, func() bool { return up.calls.Load() == 2 }) {
		t.Fatalf("refresh not attempted; calls=%d want 2", up.calls.Load())
	}

	time.Sleep(40 * time.Millisecond) // re-expire
	r2, err2 := c.GetCoinByAssetID(context.Background(), "X")
	if err2 != nil || r2.AssetID != "X" {
		t.Fatalf("after a failed refresh, still serve stale no err; got %q err=%v", r2.AssetID, err2)
	}
	if !waitFor(2*time.Second, func() bool { return up.calls.Load() >= 3 }) {
		t.Fatalf("failed refresh was not retried; calls=%d", up.calls.Load())
	}
}
