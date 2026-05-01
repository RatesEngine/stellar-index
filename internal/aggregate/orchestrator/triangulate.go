package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/RatesEngine/rates-engine/internal/aggregate"
	"github.com/RatesEngine/rates-engine/internal/cachekeys"
	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/obs"
	"github.com/RatesEngine/rates-engine/internal/sources/external"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// TriangulationChain is one chain pricing entry. Target is the
// implied pair (e.g. XLM/EUR); Legs is the ordered chain whose
// product yields the target price (e.g. [XLM/USD, USD/EUR]).
//
// Validation: at least 2 legs; Legs[0].Base must equal Target.Base;
// Legs[N-1].Quote must equal Target.Quote; adjacent legs must share
// their pivot asset (Legs[i].Quote == Legs[i+1].Base). Caller-side
// validation lives in [ValidateTriangulationChain].
type TriangulationChain struct {
	Target canonical.Pair
	Legs   []canonical.Pair
}

// ValidateTriangulationChain returns nil when the chain is
// structurally consistent (chainable legs, Target endpoints match
// the chain endpoints). Returns an error naming the specific
// violation otherwise. Cheap; runs once per chain at startup.
func ValidateTriangulationChain(chain TriangulationChain) error {
	if len(chain.Legs) < 2 {
		return fmt.Errorf("triangulation: chain for %s has %d legs, want at least 2",
			chain.Target.String(), len(chain.Legs))
	}
	first := chain.Legs[0]
	last := chain.Legs[len(chain.Legs)-1]
	if !first.Base.Equal(chain.Target.Base) {
		return fmt.Errorf("triangulation: chain for %s — first leg base %s != target base %s",
			chain.Target.String(), first.Base.String(), chain.Target.Base.String())
	}
	if !last.Quote.Equal(chain.Target.Quote) {
		return fmt.Errorf("triangulation: chain for %s — last leg quote %s != target quote %s",
			chain.Target.String(), last.Quote.String(), chain.Target.Quote.String())
	}
	for i := 0; i < len(chain.Legs)-1; i++ {
		if !chain.Legs[i].Quote.Equal(chain.Legs[i+1].Base) {
			return fmt.Errorf("triangulation: chain for %s — leg[%d].Quote=%s does not match leg[%d].Base=%s",
				chain.Target.String(),
				i, chain.Legs[i].Quote.String(),
				i+1, chain.Legs[i+1].Base.String())
		}
	}
	return nil
}

// triangulateAll runs the post-refresh triangulation pass for every
// configured chain × window combination. Per-chain failures (a leg
// missing from cache, a parse error) are logged + counted but never
// abort the tick — a single bad chain shouldn't stall the rest of
// the worker.
//
// `bucketEnd` for the X2.5 forex-snap rule is computed once per
// (chain, window) as `now.Truncate(window)` — the most recent UTC-
// aligned boundary at-or-before now. Every region computes the same
// boundary for the same wall-clock now, which is what makes the
// chained-fiat output across-region deterministic per ADR-0018.
func (o *Orchestrator) triangulateAll(ctx context.Context) {
	if len(o.cfg.Triangulations) == 0 {
		return
	}
	now := time.Now().UTC()
	for _, chain := range o.cfg.Triangulations {
		for _, window := range o.cfg.Windows {
			if err := ctx.Err(); err != nil {
				return
			}
			bucketEnd := now.Truncate(window)
			outcome := o.triangulateOne(ctx, chain, window, bucketEnd)
			obs.AggregatorTriangulationsTotal.WithLabelValues(outcome).Inc()
		}
	}
}

// isFXLeg reports whether a leg should use the X2.5 forex-snap rule.
// Per the design note (approach A): a leg is FX iff its Base AND
// Quote are both [canonical.AssetFiat]. This is a structural test —
// crypto-vs-fiat legs (XLM/USD) stay on the cached-VWAP path because
// CEX/DEX trades dominate that pair; fiat-vs-fiat legs (USD/EUR) are
// the chained-fiat factor that ADR-0018 mandates be snapped.
func isFXLeg(leg canonical.Pair) bool {
	return leg.Base.Type == canonical.AssetFiat && leg.Quote.Type == canonical.AssetFiat
}

// triangulateOne computes one (chain, window) entry. Returns the
// outcome label for the metric: "ok" on successful publish,
// "missing_leg" when at least one leg's VWAP is absent from cache
// (the most common — the leg's refresh just produced an empty
// window), "parse_error" on a malformed cached value (rare,
// indicates upstream regression), "redis_error" on a Get failure
// (Redis blip).
//
// For FX legs (Base AND Quote both AssetFiat), [Config.FXStore] is
// queried for the most recent FX-source quote at-or-before bucketEnd
// (the X2.5 forex-snap rule). Misses fall back to the cached-VWAP
// path and increment [obs.AggregatorFXSnapFallbackTotal] — the chain
// still publishes (degraded but functional), and the alert in
// deploy/monitoring/rules/aggregator.yml fires when the fallback rate
// dominates.
func (o *Orchestrator) triangulateOne(ctx context.Context, chain TriangulationChain, window time.Duration, bucketEnd time.Time) string {
	legPrices := make([]*big.Rat, 0, len(chain.Legs))
	for _, leg := range chain.Legs {
		price, outcome := o.legPrice(ctx, chain, leg, window, bucketEnd)
		if outcome != "" {
			return outcome
		}
		legPrices = append(legPrices, price)
	}

	implied, err := aggregate.TriangulateChain(legPrices...)
	if err != nil {
		// Zero or negative leg price — already filtered upstream
		// (VWAP rejects empty windows) but defensive.
		o.logger.Warn("triangulation: chain compute failed",
			"chain", chain.Target.String(),
			"err", err)
		return "parse_error"
	}

	value := formatRatFixed(implied, 12)
	key := cachekeys.VWAP(chain.Target.Base, chain.Target.Quote, window)
	ttl := cachekeys.VWAPTTL(window)
	if err := o.cache.Set(ctx, key, value, ttl).Err(); err != nil {
		o.logger.Warn("triangulation: cache set failed",
			"chain", chain.Target.String(),
			"err", err)
		return "redis_error"
	}

	// Provenance marker. Lets the API set flags.triangulated=true
	// when serving this pair via the Redis-fallback path. Per-pair
	// direct refresh does NOT write this key — absence == direct.
	// A failure here is logged but does not roll back the value
	// write: the value is correct either way, and the flag has a
	// safe default of false.
	provKey := cachekeys.VWAPProvenance(chain.Target.Base, chain.Target.Quote, window)
	if err := o.cache.Set(ctx, provKey, cachekeys.VWAPProvenanceTriangulated, ttl).Err(); err != nil {
		o.logger.Warn("triangulation: provenance marker set failed",
			"chain", chain.Target.String(),
			"err", err)
		// Don't return — value write succeeded; flag would just
		// stay at default false this cycle.
	}
	return "ok"
}

// legPrice returns the price for one leg of a triangulation chain.
// On success, returns (price, ""); on a recoverable miss/error,
// returns (nil, outcomeLabel) where outcomeLabel is the metric label
// the caller bubbles up via [obs.AggregatorTriangulationsTotal].
//
// FX legs (both sides fiat) attempt the X2.5 snap path via
// [Config.FXStore]. Snap misses (no FX quote at-or-before bucketEnd)
// fall back to the cached-VWAP path and increment
// [obs.AggregatorFXSnapFallbackTotal]; this keeps the chain publishing
// during fresh deploys / FX-source outages instead of black-holing.
// Snap DB errors propagate up as "redis_error"-class outcomes — the
// FX-store error means we can't trust ANY chained-fiat output this
// tick, so the chain skips publish.
//
// Non-FX legs (and FX legs when FXStore is nil) read the cached VWAP
// the per-pair refresh wrote earlier this tick.
func (o *Orchestrator) legPrice(
	ctx context.Context,
	chain TriangulationChain,
	leg canonical.Pair,
	window time.Duration,
	bucketEnd time.Time,
) (*big.Rat, string) {
	if isFXLeg(leg) && o.cfg.FXStore != nil {
		price, _, _, err := o.cfg.FXStore.FXQuoteAtOrBefore(ctx, leg, bucketEnd, external.FXSources())
		switch {
		case err == nil:
			return price, ""
		case errors.Is(err, timescale.ErrNoFXQuote):
			// Soft fallback to cached VWAP — degraded but the chain
			// still publishes. Counter drives the dashboard /
			// alert (>50% sustained = FX ingestion is sick).
			obs.AggregatorFXSnapFallbackTotal.WithLabelValues(leg.String()).Inc()
			// fallthrough to cached-VWAP read below
		default:
			o.logger.Warn("triangulation: FX snap query failed",
				"chain", chain.Target.String(),
				"leg", leg.String(),
				"err", err)
			return nil, "redis_error"
		}
	}
	return o.legPriceFromCache(ctx, chain, leg, window)
}

// legPriceFromCache reads a leg's freshly-cached VWAP. Used for non-
// FX legs and for FX legs when the snap path produced ErrNoFXQuote.
func (o *Orchestrator) legPriceFromCache(
	ctx context.Context,
	chain TriangulationChain,
	leg canonical.Pair,
	window time.Duration,
) (*big.Rat, string) {
	key := cachekeys.VWAP(leg.Base, leg.Quote, window)
	raw, err := o.cache.Get(ctx, key).Result()
	switch {
	case errors.Is(err, redis.Nil):
		return nil, "missing_leg"
	case err != nil:
		o.logger.Warn("triangulation: cache get failed",
			"chain", chain.Target.String(),
			"leg", leg.String(),
			"err", err)
		return nil, "redis_error"
	}
	price, ok := new(big.Rat).SetString(raw)
	if !ok {
		o.logger.Warn("triangulation: parse leg VWAP",
			"chain", chain.Target.String(),
			"leg", leg.String(),
			"raw", raw)
		return nil, "parse_error"
	}
	return price, ""
}
