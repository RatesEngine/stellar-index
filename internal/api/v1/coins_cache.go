package v1

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/obs"
	"github.com/StellarAtlas/stellar-atlas/internal/storage/timescale"
)

// CachedCoinsReader wraps a [CoinsReader] with a small per-key TTL
// cache for the methods that back high-traffic listing endpoints —
// ListCoinsExt and the batched price-history calls used by
// /v1/assets?limit=200&include=sparkline. The legacy /v1/coins
// route was removed in rc.48; /v1/assets now sources the same data
// through this seam. The unified listing fires that exact request
// on every page load and the underlying SQL takes ~1.1s; without
// this cache the explorer's time-to-interactive is gated on it.
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

	mu sync.Mutex
	// entries backs the legacy method-field caches (ListCoinsExt /
	// history batches via fetchRows / fetchHistoryMap).
	entries map[string]*coinsCacheEntry
	// swrEntries backs the generic single-value SWR path (swr[T] —
	// the per-asset single-row methods). Distinct map, SAME mu.
	swrEntries map[string]*swrEntry
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
		upstream:   upstream,
		ttl:        ttl,
		entries:    map[string]*coinsCacheEntry{},
		swrEntries: map[string]*swrEntry{},
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

// Per-asset single-value reads — all stale-while-revalidate cached
// via the generic swr[T] helper. These were pass-through on the
// assumption they were "<50ms low-volume single-row" lookups; that
// became false post-backfill: /v1/assets/{id}'s coin-extension
// fans out ~9 of these per request and GetCoinByAssetID /
// GetNativeCoinRow run the whole-asset-universe listCoinsBaseSelect
// query (~13s under load), with GetCoinTradeCount24h /
// GetCoinMarketsCount adding multi-second trades-OR scans (#24).
// SWR moves all of that off the request path with zero correctness
// loss (serve stale instantly, single-flighted background refresh).

func (c *CachedCoinsReader) GetCoinBySlug(ctx context.Context, slug string) (timescale.CoinRow, error) {
	if c.ttl <= 0 {
		return c.upstream.GetCoinBySlug(ctx, slug)
	}
	return swr(ctx, c, "coin_by_slug", "GetCoinBySlug|"+slug,
		func(ctx context.Context) (timescale.CoinRow, error) {
			return c.upstream.GetCoinBySlug(ctx, slug)
		})
}

func (c *CachedCoinsReader) GetCoinByAssetID(ctx context.Context, assetID string) (timescale.CoinRow, error) {
	if c.ttl <= 0 {
		return c.upstream.GetCoinByAssetID(ctx, assetID)
	}
	return swr(ctx, c, "coin_by_asset", "GetCoinByAssetID|"+assetID,
		func(ctx context.Context) (timescale.CoinRow, error) {
			return c.upstream.GetCoinByAssetID(ctx, assetID)
		})
}

func (c *CachedCoinsReader) GetNativeCoinRow(ctx context.Context) (timescale.CoinRow, error) {
	if c.ttl <= 0 {
		return c.upstream.GetNativeCoinRow(ctx)
	}
	return swr(ctx, c, "native_coin", "GetNativeCoinRow",
		func(ctx context.Context) (timescale.CoinRow, error) {
			return c.upstream.GetNativeCoinRow(ctx)
		})
}

func (c *CachedCoinsReader) GetCoinTopMarkets(ctx context.Context, assetID string, limit int) ([]timescale.CoinTopMarket, error) {
	if c.ttl <= 0 {
		return c.upstream.GetCoinTopMarkets(ctx, assetID, limit)
	}
	return swr(ctx, c, "coin_top_markets", fmt.Sprintf("GetCoinTopMarkets|%s|%d", assetID, limit),
		func(ctx context.Context) ([]timescale.CoinTopMarket, error) {
			return c.upstream.GetCoinTopMarkets(ctx, assetID, limit)
		})
}

func (c *CachedCoinsReader) GetCoinPriceHistory24h(ctx context.Context, assetID string) ([]timescale.CoinPricePoint, error) {
	if c.ttl <= 0 {
		return c.upstream.GetCoinPriceHistory24h(ctx, assetID)
	}
	return swr(ctx, c, "coin_hist_24h", "GetCoinPriceHistory24h|"+assetID,
		func(ctx context.Context) ([]timescale.CoinPricePoint, error) {
			return c.upstream.GetCoinPriceHistory24h(ctx, assetID)
		})
}

func (c *CachedCoinsReader) GetCoinPriceHistory7d(ctx context.Context, assetID string) ([]timescale.CoinPricePoint, error) {
	if c.ttl <= 0 {
		return c.upstream.GetCoinPriceHistory7d(ctx, assetID)
	}
	return swr(ctx, c, "coin_hist_7d", "GetCoinPriceHistory7d|"+assetID,
		func(ctx context.Context) ([]timescale.CoinPricePoint, error) {
			return c.upstream.GetCoinPriceHistory7d(ctx, assetID)
		})
}

func (c *CachedCoinsReader) GetCoinMarketsCount(ctx context.Context, assetID string) (int64, error) {
	if c.ttl <= 0 {
		return c.upstream.GetCoinMarketsCount(ctx, assetID)
	}
	return swr(ctx, c, "coin_markets_count", "GetCoinMarketsCount|"+assetID,
		func(ctx context.Context) (int64, error) {
			return c.upstream.GetCoinMarketsCount(ctx, assetID)
		})
}

func (c *CachedCoinsReader) GetCoinATH(ctx context.Context, assetID string) (*timescale.CoinATH, error) {
	if c.ttl <= 0 {
		return c.upstream.GetCoinATH(ctx, assetID)
	}
	return swr(ctx, c, "coin_ath", "GetCoinATH|"+assetID,
		func(ctx context.Context) (*timescale.CoinATH, error) {
			return c.upstream.GetCoinATH(ctx, assetID)
		})
}

func (c *CachedCoinsReader) GetCoinTradeCount24h(ctx context.Context, assetID string) (int64, error) {
	if c.ttl <= 0 {
		return c.upstream.GetCoinTradeCount24h(ctx, assetID)
	}
	return swr(ctx, c, "coin_trade_count_24h", "GetCoinTradeCount24h|"+assetID,
		func(ctx context.Context) (int64, error) {
			return c.upstream.GetCoinTradeCount24h(ctx, assetID)
		})
}

// swrEntry backs the generic single-value SWR path. Separate from
// coinsCacheEntry's method-field shape — `val` holds an opaque T;
// its dynamic type is invariant per key because keys are
// method-namespaced (e.g. "GetCoinByAssetID|<id>"), so the
// e.val.(T) assertions never mix types. Guarded by
// CachedCoinsReader.mu (shared with `entries`; distinct map).
type swrEntry struct {
	at     time.Time
	flight chan struct{}
	val    any
	err    error
}

// swr is the generic single-value stale-while-revalidate fetch: the
// proven, race-clean coins fetchRows/refreshRows logic (#22), made
// type-parametric so every per-asset single-value coin method
// shares ONE implementation. Free function — Go methods can't have
// type parameters.
//
//	(A)  fresh hit → return cached
//	(A') expired with a prior success → serve stale IMMEDIATELY +
//	     one single-flighted background refresh (refreshSWR); never
//	     blocks a request on the slow upstream
//	(B)  cold fetch already in flight → join it
//	(C)  cold leader → block inline; delete-on-error with the
//	     waiter-err-pointer panic-safety
func swr[T any](ctx context.Context, c *CachedCoinsReader, op, key string, upstream func(context.Context) (T, error)) (T, error) {
	var zero T
	c.mu.Lock()
	e, ok := c.swrEntries[key]

	if ok && e.flight == nil && time.Since(e.at) < c.ttl {
		v := e.val
		c.mu.Unlock()
		obs.APICacheOpsTotal.WithLabelValues("coins", op, "hit").Inc()
		return v.(T), nil
	}
	if ok && !e.at.IsZero() {
		v := e.val
		if e.flight == nil {
			done := make(chan struct{})
			e.flight = done
			entry := e
			c.mu.Unlock()
			obs.APICacheOpsTotal.WithLabelValues("coins", op, "stale").Inc()
			//nolint:gosec,contextcheck // G118 / contextcheck:
			// intentional. The SWR background refresh MUST use a
			// fresh context (refreshSWR -> context.Background), NOT
			// the request ctx, which is cancelled the instant the
			// stale response is written; reusing it would abort
			// every refresh — defeating the entire point of SWR.
			go refreshSWR(c, op, entry, done, upstream)
			return v.(T), nil
		}
		c.mu.Unlock()
		obs.APICacheOpsTotal.WithLabelValues("coins", op, "stale").Inc()
		return v.(T), nil
	}
	if ok && e.flight != nil {
		entry := e
		ch := e.flight
		c.mu.Unlock()
		select {
		case <-ch:
			if entry.err != nil {
				obs.APICacheOpsTotal.WithLabelValues("coins", op, "miss").Inc()
				return zero, entry.err
			}
			obs.APICacheOpsTotal.WithLabelValues("coins", op, "hit").Inc()
			return entry.val.(T), nil
		case <-ctx.Done():
			return zero, ctx.Err()
		}
	}

	done := make(chan struct{})
	entry := &swrEntry{flight: done}
	c.swrEntries[key] = entry
	c.mu.Unlock()
	obs.APICacheOpsTotal.WithLabelValues("coins", op, "miss").Inc()

	v, err := upstream(ctx)

	c.mu.Lock()
	if err == nil {
		entry.at = time.Now()
		entry.val = v
		entry.flight = nil
	} else {
		entry.err = err
		delete(c.swrEntries, key) // don't cache the error
	}
	c.mu.Unlock()
	close(done)
	return v, err
}

// refreshSWR runs the upstream call OFF the request path for swr's
// (A') branch — fresh background context (the request ctx dies when
// the stale response is written); on success swaps val+at under the
// lock; on failure keeps the stale value and only clears the
// in-flight marker (retry next request); single-flighted via
// done/entry.flight. Mirrors coins_cache.go refreshRows.
func refreshSWR[T any](c *CachedCoinsReader, op string, entry *swrEntry, done chan struct{}, upstream func(context.Context) (T, error)) {
	defer close(done)
	ctx, cancel := context.WithTimeout(context.Background(), coinsRefreshBudget)
	defer cancel()

	v, err := upstream(ctx)

	c.mu.Lock()
	if err == nil {
		entry.at = time.Now()
		entry.val = v
	}
	entry.flight = nil
	c.mu.Unlock()

	if err != nil {
		obs.APICacheOpsTotal.WithLabelValues("coins", op, "refresh_error").Inc()
	}
}

// coinsRefreshBudget bounds a stale-while-revalidate background
// refresh. It runs OFF the request path so a generous budget costs
// users nothing (they're already served the stale value); it just
// has to comfortably exceed the listing aggregate's worst case
// (~seconds, contended) so the refresh actually completes and the
// cache moves forward instead of perpetually re-spawning.
const coinsRefreshBudget = 30 * time.Second

func (c *CachedCoinsReader) fetchRows(
	ctx context.Context,
	op, key string,
	upstream func(context.Context) ([]timescale.CoinRow, error),
) ([]timescale.CoinRow, error) {
	c.mu.Lock()
	e, ok := c.entries[key]

	// (A) Fresh hit.
	if ok && e.flight == nil && time.Since(e.at) < c.ttl {
		out := e.rows
		c.mu.Unlock()
		obs.APICacheOpsTotal.WithLabelValues("coins", op, "hit").Inc()
		return out, nil
	}

	// (A') Stale-while-revalidate. A prior SUCCESSFUL fetch exists
	// (e.at non-zero — failed cold fetches delete the entry, so a
	// present entry with non-zero at always has servable rows) but
	// it's expired. Serve the stale rows IMMEDIATELY and, if no
	// refresh is already running, kick exactly one in the
	// background. Concurrent callers during the refresh also get
	// stale — nobody ever waits on the upstream call. This is the
	// entire fix for #22: the expiry refetch (~seconds on the
	// listing aggregate) must never land on a user request.
	if ok && !e.at.IsZero() {
		stale := e.rows
		if e.flight == nil {
			done := make(chan struct{})
			e.flight = done
			entry := e
			c.mu.Unlock()
			obs.APICacheOpsTotal.WithLabelValues("coins", op, "stale").Inc()
			//nolint:gosec,contextcheck // G118 / contextcheck:
			// intentional. The SWR background refresh MUST use a
			// fresh context (refreshRows -> context.Background), NOT
			// the request ctx: the request ctx is cancelled the
			// instant the stale response is written, so reusing it
			// would abort every refresh — defeating the entire point
			// of serving stale while revalidating.
			go c.refreshRows(op, entry, done, upstream)
			return stale, nil
		}
		c.mu.Unlock()
		obs.APICacheOpsTotal.WithLabelValues("coins", op, "stale").Inc()
		return stale, nil
	}

	// (B) Cold fetch already in flight (no prior success to serve) —
	// join it rather than stampede upstream.
	if ok && e.flight != nil {
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

	// (C) Cold leader: no entry (or a prior failed cold fetch left
	// none). Block inline — there is nothing stale to serve.
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

// refreshRows runs the upstream call OFF the request path for the
// stale-while-revalidate (A') branch of fetchRows.
//
//   - It uses a fresh background context, NOT the triggering
//     request's ctx: that ctx is cancelled the instant the stale
//     response is written, which would abort every refresh.
//   - On success it swaps the entry's rows + timestamp under the
//     lock → subsequent callers get a fresh hit.
//   - On failure it KEEPS the existing stale value (does not delete,
//     does not touch e.at) so we keep serving stale and simply
//     retry on the next request; only the in-flight marker clears.
//   - Single-flighted via `done`/`entry.flight`: while it runs,
//     fetchRows' (A') sees e.flight != nil and serves stale without
//     spawning a second refresh. Nothing waits on `done` (the SWR
//     path never blocks), but closing it is harmless and keeps the
//     channel lifecycle symmetric with the cold path.
func (c *CachedCoinsReader) refreshRows(
	op string,
	entry *coinsCacheEntry,
	done chan struct{},
	upstream func(context.Context) ([]timescale.CoinRow, error),
) {
	defer close(done)
	ctx, cancel := context.WithTimeout(context.Background(), coinsRefreshBudget)
	defer cancel()

	rows, err := upstream(ctx)

	c.mu.Lock()
	if err == nil {
		entry.at = time.Now()
		entry.rows = rows
	}
	entry.flight = nil
	c.mu.Unlock()

	if err != nil {
		obs.APICacheOpsTotal.WithLabelValues("coins", op, "refresh_error").Inc()
	}
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
