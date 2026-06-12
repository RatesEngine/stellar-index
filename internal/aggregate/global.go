package aggregate

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
)

// PriceAuthority labels which fallback tier produced a global-view
// price. Surfaces verbatim on `/v1/assets/{slug}.price_authority`
// (R-018 Phase 1.4); consumers downgrade trust on the second and
// third tiers vs the first.
type PriceAuthority string

const (
	// AuthorityVWAPNative means the served price came from our own
	// VWAP across exchange-class trades — the gold standard. For a
	// Stellar-issued asset this is the SDEX + Soroban DEX trades;
	// for cross-chain tickers it's any direct-pair venue trade.
	AuthorityVWAPNative PriceAuthority = "vwap_native"

	// AuthorityAggregatorAvg means the served price is a simple
	// average across `Class:Aggregator` sources (CoinGecko + CMC +
	// CryptoCompare). Used when the VWAP tier didn't have enough
	// trades. Aggregator-class sources never contribute to VWAP
	// (they're already aggregated upstream — mixing would
	// double-count), so this tier is a separate fallback layer
	// rather than a VWAP input.
	AuthorityAggregatorAvg PriceAuthority = "aggregator_avg"

	// AuthorityTriangulated means the served price was derived via
	// a bridge currency (`ASSET_USD ≈ ASSET_BTC × BTC_USD`). Last-
	// resort tier; only fires when neither direct VWAP nor
	// aggregator coverage exists for the pair.
	AuthorityTriangulated PriceAuthority = "triangulated"
)

// GlobalPriceResult is the output of [ComputeGlobalPrice]. Carries
// the served price string + the tier that produced it + the
// contributing source names for transparency.
type GlobalPriceResult struct {
	Price     string         // decimal string, exactly as Postgres serialises NUMERIC
	Authority PriceAuthority // which tier won
	Sources   []string       // contributors — venue names (VWAP), aggregator names (avg), bridge-currency-anchor source (triangulated, optional)
	AsOf      time.Time      // observation timestamp the price corresponds to
	// TradeCount carries the trade count from the VWAP tier; zero
	// for the aggregator and triangulated tiers. Useful for
	// consumers that want to render "12 trades from 4 venues"
	// alongside the price.
	TradeCount int64
}

// GlobalPriceReader is the storage seam [ComputeGlobalPrice] reads
// against. Each method maps to one tier of the fallback chain; the
// production wiring in cmd/ratesengine-api/main.go provides a thin
// adapter over *timescale.Store + the existing Redis triangulation
// looker.
type GlobalPriceReader interface {
	// LatestVWAP returns the most-recent closed VWAP bucket for the
	// pair. `ok` is false when no closed bucket exists yet (no
	// trades, retention pruned, or the asset doesn't trade on this
	// pair). The trade count + sources powers the threshold check
	// + transparency surface.
	LatestVWAP(ctx context.Context, base, quote canonical.Asset) (vwap string, asOf time.Time, tradeCount int64, sources []string, ok bool, err error)

	// LatestAggregatorPrices returns the most-recent observation
	// per source across the supplied aggregator-class source list.
	// Callers typically pass `external.AggregatorSources()`.
	LatestAggregatorPrices(ctx context.Context, base, quote canonical.Asset, sources []string) ([]canonical.OracleUpdate, error)

	// LookupTriangulated returns the implied price for (base, quote)
	// computed via a bridge currency (typically BTC or USD).
	// `ok` is false when no triangulation path was published for
	// this pair (caller doesn't need to know the bridge — the
	// triangulation worker tracks the path itself).
	LookupTriangulated(ctx context.Context, base, quote canonical.Asset, window time.Duration) (price string, asOf time.Time, ok bool, err error)
}

// GlobalPriceOptions tunes the fallback-chain policy.
type GlobalPriceOptions struct {
	// VWAPMinTradeCount is the trade-count floor below which the
	// VWAP tier falls through to the aggregator tier. Zero =>
	// any non-empty bucket wins. The default in [DefaultGlobalPriceOptions]
	// matches our existing reduced-redundancy threshold so the
	// global view's "is this real VWAP?" judgement stays aligned
	// with the rest of the API.
	VWAPMinTradeCount int64

	// AggregatorSources is the source list the aggregator tier
	// reads from. Empty disables tier 2 entirely (the global price
	// will skip from VWAP straight to triangulated).
	AggregatorSources []string

	// TriangulationWindow is the time window the triangulation
	// worker's published implied VWAP was computed over. Echoed
	// to the looker so it can pick the matching cache key.
	TriangulationWindow time.Duration

	// MaxAggregatorAge caps how stale an aggregator-tier
	// observation can be before it's ignored. Zero means no
	// freshness check — observations land regardless of age.
	// Defaults to 10 minutes in [DefaultGlobalPriceOptions]
	// (within CG's typical update cadence; observations older
	// than this are stale enough to trust less than the next tier).
	MaxAggregatorAge time.Duration
}

// DefaultGlobalPriceOptions returns the conservative defaults: 5
// trades to clear the VWAP threshold, 5-minute triangulation
// window, 10-minute aggregator-freshness ceiling. Callers can
// override per-deployment via config (Phase 1.4 wires this).
func DefaultGlobalPriceOptions() GlobalPriceOptions {
	return GlobalPriceOptions{
		VWAPMinTradeCount:   5,
		TriangulationWindow: 5 * time.Minute,
		MaxAggregatorAge:    10 * time.Minute,
	}
}

// ErrNoPrice indicates none of the three tiers produced a price.
// Handlers translate to 404 + problem+json.
var ErrNoPrice = errors.New("aggregate: no global price available")

// ComputeGlobalPrice walks the three-tier fallback chain and returns
// the first tier whose data satisfies its threshold:
//
//  1. `vwap_native` — wins when LatestVWAP returns a row with
//     trade_count >= VWAPMinTradeCount.
//  2. `aggregator_avg` — wins when LatestAggregatorPrices returns
//     >= 1 fresh observation (within MaxAggregatorAge).
//  3. `triangulated` — wins when LookupTriangulated returns ok.
//
// Returns ErrNoPrice when every tier comes up empty. Reader errors
// other than "no rows" are propagated — a transient storage failure
// shouldn't silently degrade to a lower tier (operator wants to see
// the failure, and the aggregator tier might mask a broken VWAP
// path). The exception is sql-style "not found", which is the
// expected miss signal and triggers the next tier instead.
func ComputeGlobalPrice(
	ctx context.Context,
	base, quote canonical.Asset,
	reader GlobalPriceReader,
	opts GlobalPriceOptions,
) (GlobalPriceResult, error) {
	if reader == nil {
		return GlobalPriceResult{}, fmt.Errorf("aggregate: nil GlobalPriceReader")
	}

	// Tier 1 — direct VWAP from prices_1m.
	if res, hit, err := tryVWAPTier(ctx, base, quote, reader, opts); err != nil {
		return GlobalPriceResult{}, err
	} else if hit {
		return res, nil
	}

	// Tier 2 — average across aggregator-class sources.
	if res, hit, err := tryAggregatorTier(ctx, base, quote, reader, opts); err != nil {
		return GlobalPriceResult{}, err
	} else if hit {
		return res, nil
	}

	// Tier 3 — triangulated via bridge currency.
	price, asOf, ok, err := reader.LookupTriangulated(ctx, base, quote, opts.TriangulationWindow)
	if err != nil {
		return GlobalPriceResult{}, fmt.Errorf("aggregate: triangulation lookup: %w", err)
	}
	if ok {
		return GlobalPriceResult{
			Price:     price,
			Authority: AuthorityTriangulated,
			AsOf:      asOf,
		}, nil
	}

	return GlobalPriceResult{}, ErrNoPrice
}

func tryVWAPTier(
	ctx context.Context,
	base, quote canonical.Asset,
	reader GlobalPriceReader,
	opts GlobalPriceOptions,
) (GlobalPriceResult, bool, error) {
	// F-1340 (G14-04): loop the base's canonical aliases, mirroring
	// the API's readPriceWithAliases. XLM has two canonical forms —
	// `native` (SDEX-emitted) and `crypto:XLM` (CEX-emitted). The
	// aggregator VWAPs under whichever form the configured pair set
	// names; without the alias loop the global view (/v1/assets/{slug})
	// degrades from vwap_native to aggregator_avg when LatestVWAP is
	// queried under the form that has no trades. The FIRST alias whose
	// VWAP clears the trade-count floor wins.
	for _, b := range assetAliases(base) {
		vwap, asOf, tradeCount, sources, ok, err := reader.LatestVWAP(ctx, b, quote)
		if err != nil {
			return GlobalPriceResult{}, false, fmt.Errorf("aggregate: VWAP lookup: %w", err)
		}
		if !ok {
			continue
		}
		if tradeCount < opts.VWAPMinTradeCount {
			continue
		}
		return GlobalPriceResult{
			Price:      vwap,
			Authority:  AuthorityVWAPNative,
			Sources:    sources,
			AsOf:       asOf,
			TradeCount: tradeCount,
		}, true, nil
	}
	return GlobalPriceResult{}, false, nil
}

// assetAliases returns the canonical-asset forms equivalent to
// `asset`, in priority order (the literal input first, then known
// aliases). XLM is the only asset with two canonical forms today:
// `native` (the per-network strkey-less form SDEX writes) and
// `crypto:XLM` (the cross-network ticker form every CEX writes).
// Both appear in production trade rows depending on the emitting
// source, and the aggregator VWAPs under whichever form the
// configured pair set names — so a read keyed by one form must also
// try the other or it misses the VWAP published under the alias.
//
// This intentionally duplicates the small switch in
// internal/api/v1.assetAliases rather than importing it (v1 imports
// aggregate, not the reverse — pulling the helper down here would
// invert the dependency). Keep the two in lock-step: a new asset
// with a second canonical form needs a case in BOTH (and a test).
func assetAliases(asset canonical.Asset) []canonical.Asset {
	out := []canonical.Asset{asset}
	switch asset.String() {
	case "native":
		if alt, err := canonical.ParseAsset("crypto:XLM"); err == nil {
			out = append(out, alt)
		}
	case "crypto:XLM":
		if alt, err := canonical.ParseAsset("native"); err == nil {
			out = append(out, alt)
		}
	}
	return out
}

func tryAggregatorTier(
	ctx context.Context,
	base, quote canonical.Asset,
	reader GlobalPriceReader,
	opts GlobalPriceOptions,
) (GlobalPriceResult, bool, error) {
	if len(opts.AggregatorSources) == 0 {
		return GlobalPriceResult{}, false, nil
	}
	rows, err := reader.LatestAggregatorPrices(ctx, base, quote, opts.AggregatorSources)
	if err != nil {
		return GlobalPriceResult{}, false, fmt.Errorf("aggregate: aggregator-prices lookup: %w", err)
	}
	if len(rows) == 0 {
		return GlobalPriceResult{}, false, nil
	}
	fresh := filterFreshAggregatorRows(rows, opts.MaxAggregatorAge)
	if len(fresh) == 0 {
		return GlobalPriceResult{}, false, nil
	}
	avgPrice, latest, ok := averageAggregatorPrices(fresh)
	if !ok {
		return GlobalPriceResult{}, false, nil
	}
	sources := make([]string, 0, len(fresh))
	for _, u := range fresh {
		sources = append(sources, u.Source)
	}
	return GlobalPriceResult{
		Price:     avgPrice,
		Authority: AuthorityAggregatorAvg,
		Sources:   sources,
		AsOf:      latest,
	}, true, nil
}

// filterFreshAggregatorRows drops observations older than
// `maxAge`. Zero `maxAge` is "no filter" — every observation
// passes through.
func filterFreshAggregatorRows(rows []canonical.OracleUpdate, maxAge time.Duration) []canonical.OracleUpdate {
	if maxAge <= 0 {
		return rows
	}
	cutoff := time.Now().Add(-maxAge)
	out := rows[:0]
	for _, u := range rows {
		if u.Timestamp.After(cutoff) {
			out = append(out, u)
		}
	}
	// Reslice into a fresh backing array so callers don't alias the
	// input slice's storage in surprising ways.
	cp := make([]canonical.OracleUpdate, len(out))
	copy(cp, out)
	return cp
}

// averageAggregatorPrices computes the simple arithmetic mean
// across a slice of OracleUpdate. Each price is scaled to a common
// 14-decimal representation before averaging — matches the
// decimal precision the existing /v1/oracle/lastprice surface
// already uses, and exceeds the per-source `Decimals` for every
// aggregator we wire today (CG=8, CMC=8). Returns a decimal string
// + the latest observation timestamp + ok=true on success.
//
// ok=false when the input has zero rows or every row's price scales
// to zero/negative (defensive — InsertOracleUpdate validates the
// CHECK constraint already, but we don't want a future schema
// relaxation to crash this path).
func averageAggregatorPrices(rows []canonical.OracleUpdate) (string, time.Time, bool) {
	if len(rows) == 0 {
		return "", time.Time{}, false
	}
	const commonDecimals = 14
	target := new(big.Int).Exp(big.NewInt(10), big.NewInt(commonDecimals), nil)

	sum := new(big.Int)
	var latest time.Time
	contributed := 0
	for i := range rows {
		u := &rows[i]
		if u.Price.BigInt() == nil || u.Price.BigInt().Sign() <= 0 {
			continue
		}
		// Scale u.Price (at u.Decimals) up to commonDecimals.
		// scaled = price × 10^(commonDecimals - decimals) when
		// commonDecimals > decimals (the typical case).
		// When decimals > commonDecimals, we'd truncate — reject by
		// not scaling down (returns precision-equivalent result
		// after careful rounding, which we don't need today since
		// no aggregator publishes at > 14dp).
		diff := int(commonDecimals) - int(u.Decimals)
		scaled := new(big.Int).Set(u.Price.BigInt())
		if diff > 0 {
			factor := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(diff)), nil)
			scaled.Mul(scaled, factor)
		} else if diff < 0 {
			factor := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(-diff)), nil)
			scaled.Quo(scaled, factor)
		}
		sum.Add(sum, scaled)
		contributed++
		if u.Timestamp.After(latest) {
			latest = u.Timestamp
		}
	}
	if contributed == 0 {
		return "", time.Time{}, false
	}

	// Average over the contributors. We use integer arithmetic to
	// preserve precision: avg_scaled = sum / contributed. Then
	// render as a decimal string at `commonDecimals` precision.
	avgScaled := new(big.Int).Quo(sum, big.NewInt(int64(contributed)))
	return formatScaledDecimal(avgScaled, target, commonDecimals), latest, true
}

// formatScaledDecimal renders avg (scaled by `pow10`) as a fixed-
// fractional decimal string with `decimals` fractional digits.
// e.g. avg = 100_010_000_000_000, pow10 = 10^14, decimals = 14
// → "1.00010000000000".
func formatScaledDecimal(avg, pow10 *big.Int, decimals int) string {
	// Integer and fractional parts via big.Int division.
	intPart := new(big.Int).Quo(avg, pow10)
	frac := new(big.Int).Rem(avg, pow10)
	if frac.Sign() < 0 {
		frac.Neg(frac)
	}

	fracStr := frac.String()
	// Pad with leading zeros to `decimals` digits.
	for len(fracStr) < decimals {
		fracStr = "0" + fracStr
	}
	return intPart.String() + "." + fracStr
}
