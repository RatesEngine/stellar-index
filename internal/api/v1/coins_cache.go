package v1

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/RatesEngine/rates-engine/internal/obs"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// CachedCoinsReader wraps a [CoinsReader] with a small per-key TTL
// cache for the methods that back high-traffic listing endpoints —
// ListCoinsExt and the batched price-history calls used by
// /v1/coins?limit=200&include=sparkline. The unified-currencies
// listing fires that exact request on every page load and the
// underlying SQL takes ~1.1s; without this cache the explorer's
// time-to-interactive is gated on it.
//
// All other CoinsReader methods (single-coin lookups, ATH, market
// counts) pass through unchanged — they're keyed too narrowly to
// benefit and most are already fast.
//
// Single-flight: concurrent callers during a refetch share one
// upstream call, mirroring the SourcesStats / Markets caches.
type CachedCoinsReader struct {
	upstream CoinsReader
	ttl      time.Duration

	mu      sync.Mutex
	entries map[string]*coinsCacheEntry
}

type coinsCacheEntry struct {
	at     time.Time
	flight chan struct{}

	// One field per method we cache. Only one is populated per entry.
	rows           []timescale.CoinRow
	historyByAsset map[string][]timescale.CoinPricePoint

	// err is set by the leader before close(flight) on a failing
	// upstream call. Waiters hold a pointer to the SAME entry they
	// joined the flight on so they can read entry.err here even
	// after the leader removes the entry from the map (we don't
	// TTL-cache errors). Without this, a waiter that wakes after
	// the leader's delete derefs `c.entries[key].rows` on nil and
	// panics — same root cause as the markets_cache fix.
	err error
}

// NewCachedCoinsReader wraps `upstream` with a TTL cache. ttl=0
// disables the cache (every call passes through). 30s is the
// production default — listings are activity-ranked aggregates
// that don't move materially in 30s, and the explorer's existing
// react-query layer caches client-side anyway.
func NewCachedCoinsReader(upstream CoinsReader, ttl time.Duration) *CachedCoinsReader {
	return &CachedCoinsReader{
		upstream: upstream,
		ttl:      ttl,
		entries:  map[string]*coinsCacheEntry{},
	}
}

// ListCoinsExt — cached on a key derived from the options struct.
func (c *CachedCoinsReader) ListCoinsExt(ctx context.Context, opts timescale.ListCoinsOptions) ([]timescale.CoinRow, error) {
	if c.ttl <= 0 {
		return c.upstream.ListCoinsExt(ctx, opts)
	}
	key := fmt.Sprintf("ListCoinsExt|%d|%s|%s|%s|%d",
		opts.Limit, opts.Issuer, opts.Cursor, opts.Q, opts.Order)
	return c.fetchRows(ctx, "list_coins", key, func(ctx context.Context) ([]timescale.CoinRow, error) {
		return c.upstream.ListCoinsExt(ctx, opts)
	})
}

// GetCoinsPriceHistory24hBatch — cached on the asset-id slice.
// Slice order matters for keying; callers normally pass a stable
// order (the listing iterates rows in returned order).
func (c *CachedCoinsReader) GetCoinsPriceHistory24hBatch(ctx context.Context, assetIDs []string) (map[string][]timescale.CoinPricePoint, error) {
	if c.ttl <= 0 {
		return c.upstream.GetCoinsPriceHistory24hBatch(ctx, assetIDs)
	}
	key := fmt.Sprintf("Price24hBatch|%v", assetIDs)
	return c.fetchHistoryMap(ctx, "price_history_24h", key, func(ctx context.Context) (map[string][]timescale.CoinPricePoint, error) {
		return c.upstream.GetCoinsPriceHistory24hBatch(ctx, assetIDs)
	})
}

// GetCoinsPriceHistory7dBatch — cached. Same keying caveat as the
// 24h batch.
func (c *CachedCoinsReader) GetCoinsPriceHistory7dBatch(ctx context.Context, assetIDs []string) (map[string][]timescale.CoinPricePoint, error) {
	if c.ttl <= 0 {
		return c.upstream.GetCoinsPriceHistory7dBatch(ctx, assetIDs)
	}
	key := fmt.Sprintf("Price7dBatch|%v", assetIDs)
	return c.fetchHistoryMap(ctx, "price_history_7d", key, func(ctx context.Context) (map[string][]timescale.CoinPricePoint, error) {
		return c.upstream.GetCoinsPriceHistory7dBatch(ctx, assetIDs)
	})
}

// GetCoinsATHBatch — cached. ATH can change on every new high but
// per-request cost is the same as the histories so 30s freshness
// is plenty.
func (c *CachedCoinsReader) GetCoinsATHBatch(ctx context.Context, assetIDs []string) (map[string]timescale.CoinATH, error) {
	if c.ttl <= 0 {
		return c.upstream.GetCoinsATHBatch(ctx, assetIDs)
	}
	// Pass-through for now — the upstream is fast enough that the
	// cost / complexity tradeoff doesn't yet justify a third
	// fetcher type. Plumbed here so an operator can flip it on
	// without changing the interface.
	return c.upstream.GetCoinsATHBatch(ctx, assetIDs)
}

// Pass-throughs for narrow methods that don't share keys across
// callers. The single-coin lookups are typically <50ms each and
// are already low-volume traffic.
func (c *CachedCoinsReader) GetCoinBySlug(ctx context.Context, slug string) (timescale.CoinRow, error) {
	return c.upstream.GetCoinBySlug(ctx, slug)
}

func (c *CachedCoinsReader) GetNativeCoinRow(ctx context.Context) (timescale.CoinRow, error) {
	return c.upstream.GetNativeCoinRow(ctx)
}

func (c *CachedCoinsReader) GetCoinTopMarkets(ctx context.Context, assetID string, limit int) ([]timescale.CoinTopMarket, error) {
	return c.upstream.GetCoinTopMarkets(ctx, assetID, limit)
}

func (c *CachedCoinsReader) GetCoinPriceHistory24h(ctx context.Context, assetID string) ([]timescale.CoinPricePoint, error) {
	return c.upstream.GetCoinPriceHistory24h(ctx, assetID)
}

func (c *CachedCoinsReader) GetCoinPriceHistory7d(ctx context.Context, assetID string) ([]timescale.CoinPricePoint, error) {
	return c.upstream.GetCoinPriceHistory7d(ctx, assetID)
}

func (c *CachedCoinsReader) GetCoinMarketsCount(ctx context.Context, assetID string) (int64, error) {
	return c.upstream.GetCoinMarketsCount(ctx, assetID)
}

func (c *CachedCoinsReader) GetCoinATH(ctx context.Context, assetID string) (*timescale.CoinATH, error) {
	return c.upstream.GetCoinATH(ctx, assetID)
}

func (c *CachedCoinsReader) GetCoinTradeCount24h(ctx context.Context, assetID string) (int64, error) {
	return c.upstream.GetCoinTradeCount24h(ctx, assetID)
}

func (c *CachedCoinsReader) fetchRows(
	ctx context.Context,
	op, key string,
	upstream func(context.Context) ([]timescale.CoinRow, error),
) ([]timescale.CoinRow, error) {
	c.mu.Lock()
	if e, ok := c.entries[key]; ok && e.flight == nil && time.Since(e.at) < c.ttl {
		out := e.rows
		c.mu.Unlock()
		obs.APICacheOpsTotal.WithLabelValues("coins", op, "hit").Inc()
		return out, nil
	}
	if e, ok := c.entries[key]; ok && e.flight != nil {
		entry := e
		ch := e.flight
		c.mu.Unlock()
		select {
		case <-ch:
			if entry.err != nil {
				obs.APICacheOpsTotal.WithLabelValues("coins", op, "miss").Inc()
				return nil, entry.err
			}
			obs.APICacheOpsTotal.WithLabelValues("coins", op, "hit").Inc()
			return entry.rows, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	done := make(chan struct{})
	entry := &coinsCacheEntry{flight: done}
	c.entries[key] = entry
	c.mu.Unlock()
	obs.APICacheOpsTotal.WithLabelValues("coins", op, "miss").Inc()

	rows, err := upstream(ctx)

	c.mu.Lock()
	if err == nil {
		entry.at = time.Now()
		entry.rows = rows
		entry.flight = nil
	} else {
		entry.err = err
		delete(c.entries, key) // don't cache the error for new callers
	}
	c.mu.Unlock()
	close(done)
	return rows, err
}

func (c *CachedCoinsReader) fetchHistoryMap(
	ctx context.Context,
	op, key string,
	upstream func(context.Context) (map[string][]timescale.CoinPricePoint, error),
) (map[string][]timescale.CoinPricePoint, error) {
	c.mu.Lock()
	if e, ok := c.entries[key]; ok && e.flight == nil && time.Since(e.at) < c.ttl {
		out := e.historyByAsset
		c.mu.Unlock()
		obs.APICacheOpsTotal.WithLabelValues("coins", op, "hit").Inc()
		return out, nil
	}
	if e, ok := c.entries[key]; ok && e.flight != nil {
		entry := e
		ch := e.flight
		c.mu.Unlock()
		select {
		case <-ch:
			if entry.err != nil {
				obs.APICacheOpsTotal.WithLabelValues("coins", op, "miss").Inc()
				return nil, entry.err
			}
			obs.APICacheOpsTotal.WithLabelValues("coins", op, "hit").Inc()
			return entry.historyByAsset, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	done := make(chan struct{})
	entry := &coinsCacheEntry{flight: done}
	c.entries[key] = entry
	c.mu.Unlock()
	obs.APICacheOpsTotal.WithLabelValues("coins", op, "miss").Inc()

	hist, err := upstream(ctx)

	c.mu.Lock()
	if err == nil {
		entry.at = time.Now()
		entry.historyByAsset = hist
		entry.flight = nil
	} else {
		entry.err = err
		delete(c.entries, key)
	}
	c.mu.Unlock()
	close(done)
	return hist, err
}
