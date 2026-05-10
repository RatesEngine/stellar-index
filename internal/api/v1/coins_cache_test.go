package v1

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
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
