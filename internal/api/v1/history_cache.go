package v1

import (
	"context"
	"sync"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/obs"
)

// CachedHistoryReader wraps a [HistoryReader], adding a small
// stale-while-revalidate cache to **`LatestTradePerSource` only** —
// the storage primitive behind `/v1/observations` (ADR-0018
// Surface 3). Every other HistoryReader method is pure pass-through
// (they're already range-bounded / CAGG-backed and fast).
//
// Why this one method: `LatestTradePerSource` is a
// `DISTINCT ON (source) … WHERE base=$1 AND quote=$2
// ORDER BY source, ts DESC` over the `trades` hypertable with **no
// time bound**, so TimescaleDB cannot do chunk exclusion and probes
// every chunk — multiple seconds on a busy deployment even when the
// result is empty (e.g. the status page's `asset=native&
// quote=fiat:USD`, which has zero direct trades — fiat:USD is an
// aggregator proxy, never a stored `quote_asset`). The handler caps
// it at 8s and returns 503 on overrun (#29). The real
// query-cheapening fix is the documented-but-missing
// `(base_asset, quote_asset, source, ts DESC)` index — see the note
// on [HistoryReader.LatestTradePerSource] and the durable follow-up.
//
// The status page polls one fixed key (`native|fiat:USD|`) every
// ~2 min, so SWR gives a ~100% hit rate after warm-up with zero
// correctness loss (the exact query result — including a legitimate
// empty slice — is cached).
//
// SWR shape mirrors the proven #22/#23 pattern
// (coins_cache.go / markets_cache.go) with one deliberate change:
// the cold fill runs in a **detached** goroutine on its own budget,
// not the request ctx. The handler's hard 8s ceiling would
// otherwise fail every cold call before it could populate the cache
// — the endpoint would 503 forever and never warm. Decoupling the
// fill lets the first caller(s) 503 (bounded by their own ctx)
// while the fill completes out-of-band and warms the cache for the
// next poll.
type CachedHistoryReader struct {
	HistoryReader // embedded: every method pass-through unless overridden below

	ttl time.Duration

	mu      sync.Mutex
	entries map[string]*historyCacheEntry
}

type historyCacheEntry struct {
	at     time.Time
	flight chan struct{}

	trades []canonical.Trade

	// err is set by the filler before close(flight) on a failing
	// upstream call. Waiters hold a pointer to the SAME entry they
	// joined on, so they can read err even though we delete the
	// entry from the map (errors are not TTL-cached). Mirrors
	// markets_cache.go's marketsCacheEntry.err.
	err error
}

// NewCachedHistoryReader wraps `upstream` so `LatestTradePerSource`
// is stale-while-revalidate cached. ttl=0 disables the cache
// (pure pass-through). Production wires 2m (mirrors the markets /
// coins cache TTL — observations move with trades but the status
// page tolerates 2m staleness on a "latest per source" surface).
func NewCachedHistoryReader(upstream HistoryReader, ttl time.Duration) *CachedHistoryReader {
	return &CachedHistoryReader{
		HistoryReader: upstream,
		ttl:           ttl,
		entries:       map[string]*historyCacheEntry{},
	}
}

// historyRefreshBudget bounds a detached cold-fill or
// stale-while-revalidate background refresh — independent of any
// request ctx (the whole point: outlive the handler's 8s ceiling).
// Matches coins/markets refresh budgets (the proven #22 pattern).
const historyRefreshBudget = 30 * time.Second

// LatestTradePerSource is the one cached method. See type doc.
func (c *CachedHistoryReader) LatestTradePerSource(
	ctx context.Context, pair canonical.Pair, sourceFilter string,
) ([]canonical.Trade, error) {
	if c.ttl <= 0 {
		return c.HistoryReader.LatestTradePerSource(ctx, pair, sourceFilter)
	}
	key := pair.Base.String() + "|" + pair.Quote.String() + "|" + sourceFilter
	upstream := func(uctx context.Context) ([]canonical.Trade, error) {
		return c.HistoryReader.LatestTradePerSource(uctx, pair, sourceFilter)
	}

	c.mu.Lock()
	e, ok := c.entries[key]

	// (A) Fresh hit.
	if ok && e.flight == nil && time.Since(e.at) < c.ttl {
		out := e.trades
		c.mu.Unlock()
		obs.APICacheOpsTotal.WithLabelValues("observations", "latest_per_source", "hit").Inc()
		return out, nil
	}

	// (A') Stale-while-revalidate: have a value, serve it now, kick a
	// single-flighted detached refresh.
	if ok && !e.at.IsZero() {
		out := e.trades
		if e.flight == nil {
			done := make(chan struct{})
			e.flight = done
			entry := e
			c.mu.Unlock()
			obs.APICacheOpsTotal.WithLabelValues("observations", "latest_per_source", "stale").Inc()
			//nolint:gosec,contextcheck // G118 / contextcheck:
			// intentional — the refresh MUST outlive the stale
			// response's request ctx (same as markets_cache.go
			// fetchPools (A')).
			go c.fill(key, entry, done, upstream)
			return out, nil
		}
		c.mu.Unlock()
		obs.APICacheOpsTotal.WithLabelValues("observations", "latest_per_source", "stale").Inc()
		return out, nil
	}

	// (B)/(C) Cold: no usable value. Ensure a detached fill is in
	// flight, then wait on it OR the request ctx — whichever first.
	// The caller is bounded by its own ctx (handler 503s at 8s); the
	// fill always runs to completion on its own budget and warms the
	// cache for the next poll.
	var entry *historyCacheEntry
	if ok && e.flight != nil {
		entry = e
	} else {
		done := make(chan struct{})
		entry = &historyCacheEntry{flight: done}
		c.entries[key] = entry
		//nolint:gosec,contextcheck // G118 / contextcheck:
		// intentional detached fill — see type doc.
		go c.fill(key, entry, done, upstream)
	}
	flight := entry.flight
	c.mu.Unlock()
	obs.APICacheOpsTotal.WithLabelValues("observations", "latest_per_source", "miss").Inc()

	select {
	case <-flight:
		if entry.err != nil {
			return nil, entry.err
		}
		return entry.trades, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// fill runs the upstream query on a detached budget and updates the
// entry. On success it caches (and clears flight so the entry is
// reusable as a fresh/stale value). On error it does NOT cache —
// the entry is removed from the map; waiters still read entry.err
// via their retained pointer. Mirrors markets_cache.go refreshPools
// + the cold-leader error path, unified.
func (c *CachedHistoryReader) fill(
	key string,
	entry *historyCacheEntry,
	done chan struct{},
	upstream func(context.Context) ([]canonical.Trade, error),
) {
	defer close(done)
	ctx, cancel := context.WithTimeout(context.Background(), historyRefreshBudget)
	defer cancel()

	rows, err := upstream(ctx)

	c.mu.Lock()
	switch {
	case err == nil:
		entry.at = time.Now()
		entry.trades = rows
		entry.flight = nil
	case !entry.at.IsZero():
		// Stale-while-revalidate refresh failed: KEEP serving the
		// existing stale value. Don't set entry.err, don't delete —
		// just clear flight so the next expiry retries. (Mirrors
		// markets_cache.go refreshPools.)
		entry.flight = nil
	default:
		// Cold fill failed (no prior value): propagate the error to
		// waiters via their retained entry pointer and don't
		// TTL-cache it. Only delete if still the mapped entry (a
		// concurrent fresh fill may have replaced it).
		entry.err = err
		if c.entries[key] == entry {
			delete(c.entries, key)
		}
	}
	c.mu.Unlock()

	if err != nil {
		obs.APICacheOpsTotal.WithLabelValues("observations", "latest_per_source", "refresh_error").Inc()
	}
}
