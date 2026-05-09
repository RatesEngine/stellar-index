package v1

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// CachedMarketsReader wraps a [MarketsReader] with a small per-key
// TTL cache. The five list endpoints it backs (DistinctPairsExt,
// SourceMarkets, AssetMarkets, AllPools,
// GetPairsVolumeHistory24hBatch) all run
// the same expensive 24h-trades-hypertable scan; the explorer hits
// them on every /markets, /pools, and /dexes page load.
//
// Cache key: a stable string derived from the call's args. The
// most-trafficked queries (/v1/pools?source=aquarius&limit=20 etc.)
// hit the same key, sharing one upstream call across many visitors.
//
// Single-flight + error-not-cached match the SourcesStatsReader
// wrapper's semantics. Long-tail (cursor-paginated, oddly-ordered)
// calls also hit upstream once-per-key but are amortised across
// any concurrent callers.
//
// Per-pair lookups (PairMarket) and the sparkline batch are
// pass-through — they're keyed too narrowly to benefit, and the
// underlying queries are already fast.
type CachedMarketsReader struct {
	upstream MarketsReader
	ttl      time.Duration

	mu      sync.Mutex
	entries map[string]*marketsCacheEntry
}

type marketsCacheEntry struct {
	at     time.Time
	flight chan struct{}

	pairs  []Market
	pools  []Pool
	cursor string
}

// NewCachedMarketsReader wraps `upstream` with a TTL cache. ttl=0
// disables the cache. 30s is the production default — these are
// trade-volume aggregates that move slowly, but tighter than
// sources_stats's 60s because /v1/markets is more user-visible
// (the front page).
func NewCachedMarketsReader(upstream MarketsReader, ttl time.Duration) *CachedMarketsReader {
	return &CachedMarketsReader{
		upstream: upstream,
		ttl:      ttl,
		entries:  map[string]*marketsCacheEntry{},
	}
}

// PairMarket and GetPairsVolumeHistory24hBatch are pass-through —
// see type comment for rationale.
func (c *CachedMarketsReader) PairMarket(ctx context.Context, base, quote canonical.Asset) (Market, bool, error) {
	return c.upstream.PairMarket(ctx, base, quote)
}

func (c *CachedMarketsReader) GetPairsVolumeHistory24hBatch(ctx context.Context, pairs [][2]string) (map[string][]timescale.PairVolumePoint, error) {
	return c.upstream.GetPairsVolumeHistory24hBatch(ctx, pairs)
}

// DistinctPairsExt — cached.
func (c *CachedMarketsReader) DistinctPairsExt(ctx context.Context, cursor string, limit int, order timescale.MarketsOrder) ([]Market, string, error) {
	if c.ttl <= 0 {
		return c.upstream.DistinctPairsExt(ctx, cursor, limit, order)
	}
	key := fmt.Sprintf("DistinctPairsExt|%s|%d|%d", cursor, limit, order)
	rows, next, err := c.fetchPairs(ctx, key, func(ctx context.Context) ([]Market, string, error) {
		return c.upstream.DistinctPairsExt(ctx, cursor, limit, order)
	})
	return rows, next, err
}

// SourceMarkets — cached.
func (c *CachedMarketsReader) SourceMarkets(ctx context.Context, source, cursor string, limit int, order timescale.MarketsOrder) ([]Market, string, error) {
	if c.ttl <= 0 {
		return c.upstream.SourceMarkets(ctx, source, cursor, limit, order)
	}
	key := fmt.Sprintf("SourceMarkets|%s|%s|%d|%d", source, cursor, limit, order)
	rows, next, err := c.fetchPairs(ctx, key, func(ctx context.Context) ([]Market, string, error) {
		return c.upstream.SourceMarkets(ctx, source, cursor, limit, order)
	})
	return rows, next, err
}

// AssetMarkets — cached.
func (c *CachedMarketsReader) AssetMarkets(ctx context.Context, asset, cursor string, limit int, order timescale.MarketsOrder) ([]Market, string, error) {
	if c.ttl <= 0 {
		return c.upstream.AssetMarkets(ctx, asset, cursor, limit, order)
	}
	key := fmt.Sprintf("AssetMarkets|%s|%s|%d|%d", asset, cursor, limit, order)
	rows, next, err := c.fetchPairs(ctx, key, func(ctx context.Context) ([]Market, string, error) {
		return c.upstream.AssetMarkets(ctx, asset, cursor, limit, order)
	})
	return rows, next, err
}

// AllPools — cached. Filter struct stringified via fmt for the key.
func (c *CachedMarketsReader) AllPools(ctx context.Context, filter timescale.PoolsFilter, cursor string, limit int, order timescale.MarketsOrder) ([]Pool, string, error) {
	if c.ttl <= 0 {
		return c.upstream.AllPools(ctx, filter, cursor, limit, order)
	}
	// Sources is a slice — fmt %v gives a stable repr for
	// equal-length slices with the same element order. Handlers
	// upstream sort sources from a registry so order is stable.
	key := fmt.Sprintf("AllPools|%v|%s|%s|%s|%s|%d|%d",
		filter.Sources, filter.Base, filter.Quote, filter.Asset, cursor, limit, order)
	rows, next, err := c.fetchPools(ctx, key, func(ctx context.Context) ([]Pool, string, error) {
		return c.upstream.AllPools(ctx, filter, cursor, limit, order)
	})
	return rows, next, err
}

// fetchPairs is the shared TTL+single-flight loop for the two
// pair-returning methods.
func (c *CachedMarketsReader) fetchPairs(
	ctx context.Context,
	key string,
	upstream func(context.Context) ([]Market, string, error),
) ([]Market, string, error) {
	c.mu.Lock()
	if e, ok := c.entries[key]; ok && e.flight == nil && time.Since(e.at) < c.ttl {
		out, next := e.pairs, e.cursor
		c.mu.Unlock()
		return out, next, nil
	}
	if e, ok := c.entries[key]; ok && e.flight != nil {
		ch := e.flight
		c.mu.Unlock()
		select {
		case <-ch:
			c.mu.Lock()
			out := c.entries[key]
			c.mu.Unlock()
			return out.pairs, out.cursor, nil
		case <-ctx.Done():
			return nil, "", ctx.Err()
		}
	}
	done := make(chan struct{})
	c.entries[key] = &marketsCacheEntry{flight: done}
	c.mu.Unlock()

	rows, cursor, err := upstream(ctx)

	c.mu.Lock()
	if err == nil {
		c.entries[key] = &marketsCacheEntry{
			at:     time.Now(),
			pairs:  rows,
			cursor: cursor,
		}
	} else {
		delete(c.entries, key) // don't cache the error
	}
	c.mu.Unlock()
	close(done)
	return rows, cursor, err
}

// fetchPools mirrors fetchPairs for AllPools' return type.
func (c *CachedMarketsReader) fetchPools(
	ctx context.Context,
	key string,
	upstream func(context.Context) ([]Pool, string, error),
) ([]Pool, string, error) {
	c.mu.Lock()
	if e, ok := c.entries[key]; ok && e.flight == nil && time.Since(e.at) < c.ttl {
		out, next := e.pools, e.cursor
		c.mu.Unlock()
		return out, next, nil
	}
	if e, ok := c.entries[key]; ok && e.flight != nil {
		ch := e.flight
		c.mu.Unlock()
		select {
		case <-ch:
			c.mu.Lock()
			out := c.entries[key]
			c.mu.Unlock()
			return out.pools, out.cursor, nil
		case <-ctx.Done():
			return nil, "", ctx.Err()
		}
	}
	done := make(chan struct{})
	c.entries[key] = &marketsCacheEntry{flight: done}
	c.mu.Unlock()

	rows, cursor, err := upstream(ctx)

	c.mu.Lock()
	if err == nil {
		c.entries[key] = &marketsCacheEntry{
			at:     time.Now(),
			pools:  rows,
			cursor: cursor,
		}
	} else {
		delete(c.entries, key)
	}
	c.mu.Unlock()
	close(done)
	return rows, cursor, err
}
