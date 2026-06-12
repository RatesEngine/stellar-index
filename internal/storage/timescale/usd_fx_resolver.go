package timescale

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/lib/pq"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
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

// fxCacheSweepThreshold bounds the resolver's in-memory cache. The
// key space is (asset, 1-minute bucket) including negative results,
// and nothing evicted entries before audit-2026-06-11 G11-05, so a
// long-running backfill (every historical minute × every traded
// asset) grew the map without bound on the trade-insert hot path.
// When the map exceeds this many entries, storeCache opportunistically
// sweeps everything past its TTL before inserting. The TTL (default
// 5 min) already makes stale entries dead weight, so the sweep only
// drops rows lookupCache would have ignored anyway — correctness is
// unchanged, only resident size is bounded. The threshold is high
// enough that steady-state live ingest (a handful of assets × a few
// minutes) never triggers a sweep.
const fxCacheSweepThreshold = 8192

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

	// Freshness — max staleness for a returned rate. Set to a
	// negative value (e.g. -1) to DISABLE the freshness check
	// entirely (used by tests + by deployments where the
	// source's per-minute cadence guarantees near-zero lag). Set
	// to 0 (the zero value) to inherit the default 1h. Set to a
	// positive duration to override the default.
	//
	// F-1251 (codex audit-2026-05-12): pre-fix the docstring
	// said "Set to 0 to disable" but the constructor's
	// `if opts.Freshness == 0 { opts.Freshness = time.Hour }`
	// silently turned a 0 into the 1h default, so callers who
	// thought they'd disabled freshness were still enforcing it.
	// The negative-disable convention removes the ambiguity.
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
	// F-1251: 0 → default 1h; negative → disabled (sentinel 0
	// inside the resolver so the runtime check below can stay
	// `freshness > 0`); positive → use as-is.
	switch {
	case opts.Freshness < 0:
		opts.Freshness = 0
	case opts.Freshness == 0:
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
	// F-1251 (codex audit-2026-05-12): Postgres NUMERIC::text
	// preserves the column's full scale, so a VWAP that's
	// arithmetically `1.085` arrives here as
	// `1.085000000000000000000`. Trim the trailing zeros (and
	// the lone trailing decimal point) so consumers (the
	// indexer, integration tests, the API JSON envelope) see
	// the canonical decimal form. Mathematically equivalent;
	// just easier to compare + display.
	rate = trimNumericText(rate)
	if r.freshness > 0 && at.Sub(observedAt) > r.freshness {
		// F-1251 (codex audit-2026-05-12): staleness is measured
		// against the TRADE timestamp `at`, not wall-clock. Pre-
		// fix the comparison used `r.clock().Sub(observedAt)`,
		// which rejected every historical / backfill trade older
		// than the 1h window even when a contemporaneous FX
		// anchor existed (the trade ran at T, the anchor was at
		// T-30m, both an hour ago — fine in trade-time but the
		// old check saw it as "anchor is 1h30m stale by my
		// wall-clock"). Now: at-time freshness, so historical
		// replay and backfill correctly inherit a peer-aligned
		// USD rate.
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
	// Bounded eviction (audit-2026-06-11 G11-05): before the map can
	// grow unbounded, opportunistically drop every entry past its TTL.
	// These are entries lookupCache already treats as misses, so the
	// sweep is correctness-neutral. Only runs when the map is large,
	// so the O(n) scan is amortised away under steady-state ingest.
	if len(r.cache) >= fxCacheSweepThreshold {
		now := r.clock()
		for k, e := range r.cache {
			if now.Sub(e.cachedAt) > r.cacheTTL {
				delete(r.cache, k)
			}
		}
	}
	r.cache[key] = entry
}

// trimNumericText strips trailing zeros from a Postgres NUMERIC
// text representation. `1.085000` → `1.085`; `1.000000` → `1`;
// `42` (no decimal) → `42`; `0.000` → `0`. Caller-friendly
// canonical form so downstream consumers don't need to be
// scale-aware. F-1251 (codex audit-2026-05-12).
func trimNumericText(s string) string {
	if !strings.ContainsRune(s, '.') {
		return s
	}
	// Strip trailing zeros, then strip a lone trailing dot.
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	if s == "" || s == "-" || s == "-0" {
		return "0"
	}
	return s
}

// queryDB does one prices_1m read for `<asset>/<peg>` for any peg
// in the configured list, at-or-before `at`. Returns the VWAP
// string + the row's bucket timestamp on hit, or ("", zero, nil)
// on miss.
//
// Implementation: single round-trip with `quote_asset = ANY(...)`
// so the DB picks the highest-bucket row across all pegs in one
// pass.
//
// Lower bucket bound (audit-2026-06-11 G11-06): when freshness is
// enforced (>0), USDPriceAt rejects any row whose bucket is older
// than `at - freshness`, so a miss within the window is the only
// useful result. Without a lower bound the index scan walks
// prices_1m chunks back to genesis on a miss before returning a row
// the caller would discard. The `bucket >= at - freshness` floor is
// behaviour-preserving — anything below it is rejected anyway — and
// lets TimescaleDB prune to the freshness window's chunks. When
// freshness is disabled (0) we keep the unbounded scan.
func (r *VWAPUSDFXResolver) queryDB(ctx context.Context, asset canonical.Asset, at time.Time) (string, time.Time, error) {
	q := `
		SELECT bucket, vwap::text
		  FROM prices_1m
		 WHERE base_asset  = $1
		   AND quote_asset = ANY($2)
		   AND bucket     <= $3`
	args := []any{
		asset.String(),
		pq.Array(r.usdPegs),
		at.UTC(),
	}
	if r.freshness > 0 {
		q += `
		   AND bucket     >= $4`
		args = append(args, at.UTC().Add(-r.freshness))
	}
	q += `
		 ORDER BY bucket DESC
		 LIMIT 1
	`
	row := r.store.db.QueryRowContext(ctx, q, args...)
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
