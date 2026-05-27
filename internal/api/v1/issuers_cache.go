package v1

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/RatesEngine/rates-engine/internal/obs"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// CachedIssuersReader wraps an [IssuersReader] with a per-process
// TTL cache + single-flight refetch. F-0011 audit (2026-05-26)
// measured `/v1/issuers` at p95 ~404ms — over the 200ms SLO. The
// underlying SQL is a 5-table-equivalent aggregate:
//
//	SELECT ... FROM issuers i JOIN classic_assets c USING(g_strkey)
//	 GROUP BY i.g_strkey ... ORDER BY total_obs DESC LIMIT $1
//
// EXPLAIN ANALYZE on r1 (2026-05-26) showed two seq scans
// (issuers ~58k rows + classic_assets ~190k rows) feeding a
// HashAggregate over 57k groups and a top-N heapsort. No single
// index helps because the GROUP BY + sum(observation_count)
// requires the full hashagg regardless of access path. The data
// shape (one row per (g_strkey, asset)) is small enough that the
// scan itself is the right plan — Postgres just has nothing else
// it can do. Query alone is ~196ms; the rest of the p95 budget is
// JSON marshalling + HTTP overhead.
//
// The catalogue moves on the human timescale of "new issuer
// observed on SDEX" (minutes-to-hours) — the underlying ranking by
// 24h+ observation totals isn't materially stale at 5 min. Same
// freshness rationale as CachedSourcesStatsReader / CachedMarketsReader.
//
// GetIssuer + ListIssuerAssets are pass-through — they're keyed
// too narrowly (one G-strkey) to share across callers and the
// underlying queries already hit `issuers_pkey` /
// `classic_assets_issuer_idx`, so they're sub-millisecond at the
// DB layer. Caching them would just add LRU bookkeeping for no
// throughput win.
//
// Single-flight: concurrent callers during a refetch share one
// upstream call. Same write-on-success / delete-on-error /
// waiter-err-pointer pattern as CachedMarketsReader (the proven
// race-clean shape; markets had a panic-on-error-waiter bug
// before that pattern).
type CachedIssuersReader struct {
	upstream IssuersReader
	ttl      time.Duration

	mu      sync.Mutex
	entries map[string]*issuersCacheEntry
}

type issuersCacheEntry struct {
	at     time.Time
	flight chan struct{}

	list []timescale.IssuerSummary

	// err is set by the leader before close(flight) on a failing
	// upstream call. Waiters hold a pointer to the SAME entry they
	// joined the flight on — so even if the leader removes the
	// entry from the map (we don't TTL-cache errors), waiters can
	// still read entry.err here and return it instead of nil-
	// derefing the missing entry. Mirrors CachedMarketsReader's
	// fix.
	err error
}

// NewCachedIssuersReader wraps `upstream` with a TTL cache. ttl=0
// disables caching (every call passes through). 5 min is the
// production default — the verified-issuer catalogue's top-N
// ranking is stable on the timescale of new SDEX activity. Pin
// shorter via configs/example.toml's [api] issuers_cache_ttl if
// the deployment needs fresher data.
func NewCachedIssuersReader(upstream IssuersReader, ttl time.Duration) *CachedIssuersReader {
	return &CachedIssuersReader{
		upstream: upstream,
		ttl:      ttl,
		entries:  map[string]*issuersCacheEntry{},
	}
}

// GetIssuer — pass-through. Single-row PK lookup on `issuers_pkey`
// is sub-ms; caching by g_strkey would scatter the working set
// across thousands of LRU slots with no shared-callers win.
func (c *CachedIssuersReader) GetIssuer(ctx context.Context, gStrkey string) (timescale.IssuerRow, error) {
	return c.upstream.GetIssuer(ctx, gStrkey)
}

// ListIssuerAssets — pass-through. Indexed scan via
// `classic_assets_issuer_idx`; bounded by per-issuer asset count
// (typically <20 rows).
func (c *CachedIssuersReader) ListIssuerAssets(ctx context.Context, gStrkey string) ([]timescale.IssuerAsset, error) {
	return c.upstream.ListIssuerAssets(ctx, gStrkey)
}

// ListIssuers — cached. Key is just the limit (the only argument);
// the handler clamps limit to [1, 500] before this call, so we
// have a bounded key space. Most traffic hits limit=100 (the
// default) and limit=25 (explorer's home strip).
func (c *CachedIssuersReader) ListIssuers(ctx context.Context, limit int) ([]timescale.IssuerSummary, error) {
	if c.ttl <= 0 {
		return c.upstream.ListIssuers(ctx, limit)
	}
	key := fmt.Sprintf("ListIssuers|%d", limit)
	return c.fetchList(ctx, "list_issuers", key, func(ctx context.Context) ([]timescale.IssuerSummary, error) {
		return c.upstream.ListIssuers(ctx, limit)
	})
}

// fetchList is the TTL + single-flight loop. Mirrors
// CachedMarketsReader.fetchPairs (delete-on-error,
// waiter-err-pointer panic safety). No SWR — the underlying query
// is ~200ms not multi-second, so a cold-leader inline fetch on
// expiry is acceptable; we don't need to add the goroutine
// complexity that coins_cache.go / markets_cache.go ship for
// their multi-second worst cases. (If r1 measurements show
// post-cache p95 still spiking on miss, the swr[T] helper from
// coins_cache.go drops in.)
func (c *CachedIssuersReader) fetchList(
	ctx context.Context,
	op, key string,
	upstream func(context.Context) ([]timescale.IssuerSummary, error),
) ([]timescale.IssuerSummary, error) {
	c.mu.Lock()
	e, ok := c.entries[key]

	// (A) Fresh hit.
	if ok && e.flight == nil && time.Since(e.at) < c.ttl {
		out := e.list
		c.mu.Unlock()
		obs.APICacheOpsTotal.WithLabelValues("issuers", op, "hit").Inc()
		return out, nil
	}

	// (B) Refresh already in flight (cold or stale-leader-running) —
	// join it rather than stampede upstream. Capture the entry
	// pointer so we read the leader's result/err off the SAME
	// struct we joined on; survives the leader's delete-on-error.
	if ok && e.flight != nil {
		entry := e
		ch := e.flight
		c.mu.Unlock()
		select {
		case <-ch:
			if entry.err != nil {
				obs.APICacheOpsTotal.WithLabelValues("issuers", op, "miss").Inc()
				return nil, entry.err
			}
			obs.APICacheOpsTotal.WithLabelValues("issuers", op, "hit").Inc()
			return entry.list, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	// (C) Leader: no entry, or a stale entry with no flight in
	// progress. Take the slot, run the upstream call inline.
	done := make(chan struct{})
	entry := &issuersCacheEntry{flight: done}
	c.entries[key] = entry
	c.mu.Unlock()
	obs.APICacheOpsTotal.WithLabelValues("issuers", op, "miss").Inc()

	rows, err := upstream(ctx)

	c.mu.Lock()
	if err == nil {
		entry.at = time.Now()
		entry.list = rows
		entry.flight = nil
	} else {
		entry.err = err
		delete(c.entries, key) // don't cache the error for new callers
	}
	c.mu.Unlock()
	close(done)
	return rows, err
}
