package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/StellarAtlas/stellar-atlas/internal/aggregate/baseline"
	"github.com/StellarAtlas/stellar-atlas/internal/aggregate/confidence"
	"github.com/StellarAtlas/stellar-atlas/internal/cachekeys"
	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
	"github.com/StellarAtlas/stellar-atlas/internal/divergence"
	"github.com/StellarAtlas/stellar-atlas/internal/obs"
	"github.com/StellarAtlas/stellar-atlas/internal/sources/external"
)

// divergenceMinSources is the floor on a cached divergence result's
// SuccessCount before its DivergencePct is trusted as a confidence
// input. Below this we pass the "no cross-oracle data" sentinel —
// safer to neutralise the factor than to score a single reference's
// hiccup as a multi-source signal.
//
// Matches the divergence Service's default minSources gate.
const divergenceMinSources = 2

// BaselineSource is the read-side interface the confidence step
// uses to look up a per-pair MultiBaseline. Production wiring
// adapts `*timescale.Store.LatestBaseline`. Nil = confidence step
// runs with z-factor in bootstrap (no baseline available).
type BaselineSource interface {
	// LatestBaseline returns the current per-pair baseline plus the
	// wall-clock timestamp it was computed at. Implementations
	// return ([baseline.MultiBaseline]{}, zero time, error) when
	// the pair has no baseline yet.
	LatestBaseline(ctx context.Context, pair canonical.Pair) (baseline.MultiBaseline, time.Time, error)
}

// confidenceCacheTTL — the TTL is identical to VWAP so a stale
// confidence record can't outlive the price it scored.
func confidenceCacheTTL(window time.Duration) time.Duration {
	return cachekeys.ConfidenceTTL(window)
}

// confidenceComputation bundles the score with the z-score that
// produced it. Returned by [Orchestrator.computeConfidence] so the
// Phase 2 freeze check can read both without recomputing.
type confidenceComputation struct {
	Score  confidence.Score
	ZScore float64
}

// computeConfidence runs the multi-factor confidence math for the
// freshly-computed (pair, window) bucket. Returns (_, false) when
// the inputs aren't ready yet — first tick, no baseline, baseline
// in full bootstrap.
//
// The split between compute and cache exists so the Phase 2 freeze
// check (ADR-0019) can read the score before deciding whether to
// publish — frozen buckets must NOT cache confidence either, or
// the next API read would surface a stale score from a refused
// bucket.
func (o *Orchestrator) computeConfidence(
	ctx context.Context,
	pair canonical.Pair,
	vwap *big.Rat,
	prevVWAP *big.Rat,
	trades []canonicalTrade,
) (confidenceComputation, bool) {
	if o.cfg.Baselines == nil || prevVWAP == nil {
		obs.AggregatorConfidenceComputeTotal.WithLabelValues("skipped").Inc()
		return confidenceComputation{}, false
	}

	multi, computedAt, err := o.cfg.Baselines.LatestBaseline(ctx, pair)
	if err != nil {
		obs.AggregatorConfidenceComputeTotal.WithLabelValues("baseline_missing").Inc()
		return confidenceComputation{}, false
	}

	currF, _ := vwap.Float64()
	prevF, _ := prevVWAP.Float64()
	if prevF == 0 {
		obs.AggregatorConfidenceComputeTotal.WithLabelValues("skipped").Inc()
		return confidenceComputation{}, false
	}
	returnPct := (currF - prevF) / prevF

	z, _, valid := multi.MaxZScore(returnPct)
	if !valid {
		obs.AggregatorConfidenceComputeTotal.WithLabelValues("baseline_missing").Inc()
		return confidenceComputation{}, false
	}

	score := confidence.Compute(confidence.Inputs{
		ZScore:                   z,
		SourceCount:              distinctSourceCount(trades),
		SourceClassCount:         distinctSourceClassCount(trades),
		LiquidityUSD:             approxUSDVolume(trades, pair),
		CrossOracleDivergencePct: o.lookupDivergencePct(ctx, pair),
		BaselineAgeDays:          baselineAgeDays(multi, computedAt),
	}, confidence.DefaultWeights())

	return confidenceComputation{Score: score, ZScore: z}, true
}

// cacheConfidence writes a previously-computed [confidence.Score]
// to Redis. Called only when the bucket is being published (i.e.
// neither Phase 1 nor Phase 2 has refused). Failures are logged +
// counted but don't propagate — confidence is enrichment.
func (o *Orchestrator) cacheConfidence(
	ctx context.Context,
	pair canonical.Pair,
	window time.Duration,
	score confidence.Score,
) {
	body, err := json.Marshal(score)
	if err != nil {
		obs.AggregatorConfidenceComputeTotal.WithLabelValues("marshal_error").Inc()
		return
	}
	key := cachekeys.Confidence(pair.Base, pair.Quote, window)
	if err := o.cache.Set(ctx, key, body, confidenceCacheTTL(window)).Err(); err != nil {
		obs.AggregatorConfidenceComputeTotal.WithLabelValues("write_error").Inc()
		o.logger.Warn("confidence cache write failed",
			"pair", pair.String(), "window", window.String(), "err", err)
		return
	}
	obs.AggregatorConfidenceComputeTotal.WithLabelValues("ok").Inc()
}

// canonicalTrade is a local alias to avoid an import cycle while
// the orchestrator's Trade-handling helpers live next to the
// canonical package import. Same shape; same semantics.
type canonicalTrade = canonical.Trade

// lookupDivergencePct reads the cached divergence result for the
// specific `pair` and returns its DivergencePct when the
// SuccessCount meets the trust floor (`divergenceMinSources`).
// Otherwise (no key, decode error, transient cache failure,
// single-source success) returns -1 — the
// [confidence.CrossOracleFactor] "no cross-oracle data" sentinel.
//
// F-1344 (G16-03): reads the per-PAIR key (`div:<base>/<quote>`),
// not a per-base key. The confidence score for XLM/USDT must use
// XLM/USDT's own divergence, not whatever pair last wrote the base
// key — the pre-fix per-base key meant the score consumed a
// divergence computed against a different quote.
//
// Best-effort: divergence is enrichment, not a publish-blocker.
// Read failures don't propagate; the confidence step continues with
// the neutral sentinel.
func (o *Orchestrator) lookupDivergencePct(ctx context.Context, pair canonical.Pair) float64 {
	raw, err := o.cache.Get(ctx, cachekeys.Divergence(pair)).Bytes()
	if errors.Is(err, redis.Nil) {
		return -1 // no cache entry; treat as "no data"
	}
	if err != nil {
		// Transient Redis read failure — log debug-level (not warn)
		// because we don't want a Redis blip to flood logs every tick;
		// the metric label captures this cleanly enough.
		obs.AggregatorConfidenceComputeTotal.WithLabelValues("divergence_read_error").Inc()
		return -1
	}
	var cached divergence.CachedResult
	if err := json.Unmarshal(raw, &cached); err != nil {
		obs.AggregatorConfidenceComputeTotal.WithLabelValues("divergence_decode_error").Inc()
		return -1
	}
	if cached.SuccessCount < divergenceMinSources {
		// Single-reference signal: don't trust as a multi-source
		// divergence input. Pass "no data" sentinel.
		return -1
	}
	return cached.DivergencePct
}

// distinctSourceClassCount returns the count of distinct
// (Class, Subclass) buckets represented in the trade slice. Used
// by the confidence diversity factor (ADR-0019): sources of the
// same economic kind agree less informatively than sources from
// different kinds.
//
// Bucket key is `Class:Subclass`. This means:
//   - two CEXes (binance + coinbase) → both `exchange:cex` → 1
//   - CEX + DEX (binance + soroswap) → `exchange:cex` + `exchange:dex` → 2
//   - CEX + Oracle (binance + reflector-dex) → 2
//   - DEX + FX (soroswap + polygon-forex) → 2
//
// Sources outside ClassExchange typically have empty Subclass —
// their parent Class already captures the economic distinction
// (oracles, aggregators, authority anchors don't sub-partition).
//
// Sources missing from the registry fall into the [external.Lookup]
// fallback (`exchange:`, no subclass) — an unknown source name
// doesn't get its own bucket.
func distinctSourceClassCount(trades []canonicalTrade) int {
	if len(trades) == 0 {
		return 0
	}
	seen := make(map[string]struct{}, 4)
	for i := range trades {
		md := external.Lookup(trades[i].Source)
		key := string(md.Class) + ":" + string(md.Subclass)
		seen[key] = struct{}{}
	}
	return len(seen)
}

// approxUSDVolume returns an approximation of bucket USD volume.
// Best when the pair quotes in fiat:USD or a USD-pegged stablecoin
// — uses sum(QuoteAmount) directly. For non-USD-quoted pairs
// returns 0 (the LiquidityFactor then reads as 0 — the right
// signal: "we can't see USD liquidity for this pair").
//
// Refines once L2.2 (`usd_volume` column populated per trade)
// ships and the trade carries an authoritative USD figure.
func approxUSDVolume(trades []canonicalTrade, pair canonical.Pair) float64 {
	if !isUSDQuoted(pair) {
		return 0
	}
	sum := new(big.Int)
	for i := range trades {
		sum.Add(sum, trades[i].QuoteAmount.BigInt())
	}
	// Quote amounts are integer stroops at 7 decimals for XLM-side
	// pairs; for stablecoin quotes the convention is also 7. We
	// divide by 1e7 to get a USD-magnitude figure. Approximate is
	// fine — the LiquidityFactor's log-saturating shape doesn't
	// care about a 10x error.
	usd, _ := new(big.Rat).SetFrac(sum, big.NewInt(10_000_000)).Float64()
	return usd
}

// isUSDQuoted reports whether the pair's quote is fiat:USD or a
// canonical USD-pegged stablecoin. Used to gate USD-magnitude
// approximations in [approxUSDVolume].
func isUSDQuoted(pair canonical.Pair) bool {
	switch pair.Quote.String() {
	case "fiat:USD",
		"crypto:USDT",
		"crypto:USDC",
		"crypto:DAI",
		"crypto:PYUSD",
		"crypto:USDP":
		return true
	}
	return false
}

// baselineAgeDays returns how much real history backs the 30d
// baseline, in days. Uses Day30.N (count of returns that fed the
// MAD computation) divided by 1440 (1m buckets per 24h). Returns
// -1 (the [confidence.BaselineQualityFactor] sentinel) when the
// 30d window is in bootstrap.
func baselineAgeDays(multi baseline.MultiBaseline, computedAt time.Time) float64 {
	if multi.Day30 == nil {
		return -1
	}
	// 1440 = minutes per day. Day30.N is the number of bucket-to-
	// bucket returns in the window (one per 1m bucket pair).
	days := float64(multi.Day30.N) / 1440.0
	// computedAt is the wall-clock when the refresher last wrote
	// the baseline — useful sanity check, not used in the math.
	_ = computedAt
	return days
}
