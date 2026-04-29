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
func (o *Orchestrator) triangulateAll(ctx context.Context) {
	if len(o.cfg.Triangulations) == 0 {
		return
	}
	for _, chain := range o.cfg.Triangulations {
		for _, window := range o.cfg.Windows {
			if err := ctx.Err(); err != nil {
				return
			}
			outcome := o.triangulateOne(ctx, chain, window)
			obs.AggregatorTriangulationsTotal.WithLabelValues(outcome).Inc()
		}
	}
}

// triangulateOne computes one (chain, window) entry. Returns the
// outcome label for the metric: "ok" on successful publish,
// "missing_leg" when at least one leg's VWAP is absent from cache
// (the most common — the leg's refresh just produced an empty
// window), "parse_error" on a malformed cached value (rare,
// indicates upstream regression), "redis_error" on a Get failure
// (Redis blip).
func (o *Orchestrator) triangulateOne(ctx context.Context, chain TriangulationChain, window time.Duration) string {
	legPrices := make([]*big.Rat, 0, len(chain.Legs))
	for _, leg := range chain.Legs {
		key := cachekeys.VWAP(leg.Base, leg.Quote, window)
		raw, err := o.cache.Get(ctx, key).Result()
		switch {
		case errors.Is(err, redis.Nil):
			// Leg's window was empty this tick — skip the chain.
			// The next tick re-evaluates with whatever's freshly
			// cached.
			return "missing_leg"
		case err != nil:
			o.logger.Warn("triangulation: cache get failed",
				"chain", chain.Target.String(),
				"leg", leg.String(),
				"err", err)
			return "redis_error"
		}
		price, ok := new(big.Rat).SetString(raw)
		if !ok {
			o.logger.Warn("triangulation: parse leg VWAP",
				"chain", chain.Target.String(),
				"leg", leg.String(),
				"raw", raw)
			return "parse_error"
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
	return "ok"
}
