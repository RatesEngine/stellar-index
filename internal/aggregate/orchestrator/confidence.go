package orchestrator

import (
	"context"
	"encoding/json"
	"math/big"
	"time"

	"github.com/RatesEngine/rates-engine/internal/aggregate/baseline"
	"github.com/RatesEngine/rates-engine/internal/aggregate/confidence"
	"github.com/RatesEngine/rates-engine/internal/cachekeys"
	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/obs"
)

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

// computeAndCacheConfidence runs the multi-factor confidence score
// for one freshly-published (pair, window) bucket and writes the
// JSON-encoded [confidence.Score] to Redis.
//
// Skipped entirely when:
//   - Baselines is nil (operator hasn't wired the source);
//   - prevVWAP is nil (this is the first tick — no return to score
//     against). The next tick will have a baseline-comparable return
//     and confidence will start flowing.
//
// Per-pair failures are logged + counted but never propagated:
// confidence is enrichment, not a publish-blocking signal. The
// VWAP itself is already cached by the time we run.
func (o *Orchestrator) computeAndCacheConfidence(
	ctx context.Context,
	pair canonical.Pair,
	window time.Duration,
	vwap *big.Rat,
	prevVWAP *big.Rat,
	trades []canonicalTrade,
) {
	if o.cfg.Baselines == nil || prevVWAP == nil {
		// First-tick / unwired-baseline → no z-score input. Emit a
		// metric so the operator can see how often this happens
		// without parsing logs.
		obs.AggregatorConfidenceComputeTotal.WithLabelValues("skipped").Inc()
		return
	}

	multi, computedAt, err := o.cfg.Baselines.LatestBaseline(ctx, pair)
	if err != nil {
		// Baseline-read errors include "not yet computed for this
		// pair" — treat as bootstrap, not an aggregator-side failure.
		obs.AggregatorConfidenceComputeTotal.WithLabelValues("baseline_missing").Inc()
		return
	}

	// Convert big.Rat → float64 for the factor math. Loss of
	// precision here doesn't matter — confidence is a [0, 1]
	// statistic, not a money figure.
	currF, _ := vwap.Float64()
	prevF, _ := prevVWAP.Float64()
	if prevF == 0 {
		obs.AggregatorConfidenceComputeTotal.WithLabelValues("skipped").Inc()
		return
	}
	returnPct := (currF - prevF) / prevF

	z, _, valid := multi.MaxZScore(returnPct)
	if !valid {
		// MultiBaseline is fully bootstrapped — no usable window.
		obs.AggregatorConfidenceComputeTotal.WithLabelValues("baseline_missing").Inc()
		return
	}

	classCount := distinctSourceClassCount(trades)
	usdVolume := approxUSDVolume(trades, pair)
	ageDays := baselineAgeDays(multi, computedAt)

	score := confidence.Compute(confidence.Inputs{
		ZScore:           z,
		SourceCount:      distinctSourceCount(trades),
		SourceClassCount: classCount,
		LiquidityUSD:     usdVolume,
		// Negative = "no cross-oracle data" sentinel; the next L2.6
		// slice wires this from the `div:<asset>` Redis key written
		// by `internal/divergence` (the cached Result carries
		// DivergencePct + SuccessCount; we use the pct when count >= 2).
		CrossOracleDivergencePct: -1,
		BaselineAgeDays:          ageDays,
	}, confidence.DefaultWeights())

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

// distinctSourceClassCount returns the count of distinct
// source CLASSES (as understood by external.Lookup) in the trade
// slice. Sources without a class registration count as their
// own bucket — keeps on-chain DEXes (which have no external
// registry entry) from being silently dropped.
func distinctSourceClassCount(trades []canonicalTrade) int {
	if len(trades) == 0 {
		return 0
	}
	seen := make(map[string]struct{}, len(trades))
	for i := range trades {
		// The orchestrator already imports external; using its class
		// registry here would create a cycle through the test path.
		// Cheap proxy: distinct source name = distinct class for the
		// confidence-score's purpose (CEX_a + CEX_b read as 2; same
		// source twice reads as 1). Refines once L2.10's Reference
		// type gives us a stable class lookup.
		seen[trades[i].Source] = struct{}{}
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
