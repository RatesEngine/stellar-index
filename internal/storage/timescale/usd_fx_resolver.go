package timescale

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/lib/pq"

	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// VWAPUSDFXResolver implements [USDVolumeFXResolver] against the
// `prices_1m` continuous-aggregate. For a given on-chain quote
// asset + timestamp, it returns the asset's most-recent VWAP
// against any of the operator-declared USD-pegged classics
// (typically Circle USDC, Stellar USDT, AnchorUSD) — treating the
// peg as exactly $1.
//
// L2.2 Phase 2 / F-1268 (audit-2026-05-12). Pre-Phase-2:
// on-chain trades whose quote asset wasn't already in the
// operator's USD-pegged list contributed 0 to volume_24h_usd.
// This resolver closes the gap by looking up `<quote>/<USD-peg>`
// at the trade's timestamp; if a recent VWAP exists, the trade
// inherits the USD value through that chain.
//
// Cache: per (asset, 1-minute bucket) → resolved rate string,
// with a TTL (default 5 minutes). The trade-insert hot path can
// stamp hundreds of trades per second; without a cache we'd
// hammer prices_1m with one query per insert. The minute-bucket
// key matches the CAGG's resolution — finer-grained caching adds
// no precision but multiplies misses.
type VWAPUSDFXResolver struct {
	store *Store

	// usdPegs is the operator-declared classic USD-peg list (e.g.
	// USDC-GA5Z…, USDT-GCQT…). The resolver queries prices_1m for
	// `<asset>/<peg>` for each peg until one returns a row.
	usdPegs []string

	// freshness is the maximum allowable (now - VWAP timestamp).
	// Entries older than this return ok=false rather than letting
	// a stale rate land in a fresh trade's usd_volume.
	freshness time.Duration

	// cacheTTL caps how long a cached rate is valid before
	// re-querying. Default 5 min.
	cacheTTL time.Duration

	clock func() time.Time

	mu    sync.RWMutex
	cache map[fxCacheKey]fxCacheEntry
}

// fxCacheKey is (asset.String(), 1-minute floor of `at`). Two
// trades within the same minute against the same asset share a
// resolved rate — same as the CAGG's natural granularity.
type fxCacheKey struct {
	asset    string
	bucketMs int64
}

type fxCacheEntry struct {
	rate     string // empty when no rate available
	cachedAt time.Time
}

// VWAPUSDFXResolverOptions tunes the resolver.
type VWAPUSDFXResolverOptions struct {
	// USDPegs is the operator's classic USD-peg list (the
	// same value the [USDVolumeQuoteSpec] consumes; pass through
	// the canonical "CODE-ISSUER" wire form). Resolver queries
	// pegs in order, first match wins. Empty list = resolver is
	// a no-op (every USDPriceAt returns ok=false).
	USDPegs []string

	// Freshness — max staleness for a returned rate. Default 1h.
	// Set to 0 to disable the freshness check entirely (use only
	// when the underlying source's per-minute cadence guarantees
	// near-zero lag; production should always set this).
	Freshness time.Duration

	// CacheTTL bounds the in-memory cache. Default 5 min.
	CacheTTL time.Duration

	// Clock is the time source. Override in tests.
	Clock func() time.Time
}

// Compile-time conformance check.
var _ USDVolumeFXResolver = (*VWAPUSDFXResolver)(nil)

// NewVWAPUSDFXResolver constructs the resolver.
func NewVWAPUSDFXResolver(store *Store, opts VWAPUSDFXResolverOptions) (*VWAPUSDFXResolver, error) {
	if store == nil {
		return nil, errors.New("timescale: VWAPUSDFXResolver: store is required")
	}
	if opts.Freshness == 0 {
		opts.Freshness = time.Hour
	}
	if opts.CacheTTL == 0 {
		opts.CacheTTL = 5 * time.Minute
	}
	if opts.Clock == nil {
		opts.Clock = time.Now
	}
	// Defensive copy — caller may mutate their slice after.
	pegs := make([]string, len(opts.USDPegs))
	copy(pegs, opts.USDPegs)
	return &VWAPUSDFXResolver{
		store:     store,
		usdPegs:   pegs,
		freshness: opts.Freshness,
		cacheTTL:  opts.CacheTTL,
		clock:     opts.Clock,
		cache:     make(map[fxCacheKey]fxCacheEntry),
	}, nil
}

// USDPriceAt implements [USDVolumeFXResolver]. Returns the
// resolved USD price for `asset` at-or-before `at`, treating each
// configured peg as exactly $1.
//
// Returns ("", false, nil) when:
//   - no peg query returned a row (asset isn't traded against any
//     covered peg in the lookup window)
//   - the most-recent matching row is older than Freshness
//   - the resolver has no pegs configured
//
// Real DB errors propagate so the caller can surface them via
// metrics; the calling trade still inserts, just with usd_volume
// NULL.
func (r *VWAPUSDFXResolver) USDPriceAt(ctx context.Context, asset canonical.Asset, at time.Time) (string, bool, error) {
	if len(r.usdPegs) == 0 {
		return "", false, nil
	}
	// Floor `at` to the minute for cache-key stability — matches
	// the prices_1m CAGG's natural resolution.
	bucket := at.UTC().Truncate(time.Minute)
	key := fxCacheKey{asset: asset.String(), bucketMs: bucket.UnixMilli()}

	if rate, ok := r.lookupCache(key); ok {
		if rate == "" {
			return "", false, nil
		}
		return rate, true, nil
	}

	rate, observedAt, err := r.queryDB(ctx, asset, at)
	if err != nil {
		return "", false, err
	}
	if rate == "" {
		r.storeCache(key, fxCacheEntry{rate: "", cachedAt: r.clock()})
		return "", false, nil
	}
	if r.freshness > 0 && r.clock().Sub(observedAt) > r.freshness {
		// Stale: don't return the rate, but cache the negative
		// result for the TTL window so we don't re-query on every
		// trade.
		r.storeCache(key, fxCacheEntry{rate: "", cachedAt: r.clock()})
		return "", false, nil
	}
	r.storeCache(key, fxCacheEntry{rate: rate, cachedAt: r.clock()})
	return rate, true, nil
}

// lookupCache returns (rate, true) when the cache has a fresh
// entry, otherwise ("", false). Empty rate means "previously
// resolved as no-rate-available" — caller still treats that as
// ok=false at the boundary.
func (r *VWAPUSDFXResolver) lookupCache(key fxCacheKey) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.cache[key]
	if !ok {
		return "", false
	}
	if r.clock().Sub(entry.cachedAt) > r.cacheTTL {
		return "", false
	}
	return entry.rate, true
}

func (r *VWAPUSDFXResolver) storeCache(key fxCacheKey, entry fxCacheEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cache[key] = entry
}

// queryDB does one prices_1m read for `<asset>/<peg>` for any peg
// in the configured list, at-or-before `at`. Returns the VWAP
// string + the row's bucket timestamp on hit, or ("", zero, nil)
// on miss.
//
// Implementation: single round-trip with `quote_asset = ANY(...)`
// so the DB picks the highest-bucket row across all pegs in one
// pass.
func (r *VWAPUSDFXResolver) queryDB(ctx context.Context, asset canonical.Asset, at time.Time) (string, time.Time, error) {
	const q = `
		SELECT bucket, vwap::text
		  FROM prices_1m
		 WHERE base_asset  = $1
		   AND quote_asset = ANY($2)
		   AND bucket     <= $3
		 ORDER BY bucket DESC
		 LIMIT 1
	`
	row := r.store.db.QueryRowContext(ctx, q,
		asset.String(),
		pq.Array(r.usdPegs),
		at.UTC(),
	)
	var (
		bucket time.Time
		vwap   string
	)
	if err := row.Scan(&bucket, &vwap); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", time.Time{}, nil
		}
		return "", time.Time{}, fmt.Errorf("timescale: VWAPUSDFXResolver query: %w", err)
	}
	return vwap, bucket, nil
}
