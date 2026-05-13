package orchestrator

import (
	"context"
	"strconv"
	"time"

	"github.com/RatesEngine/rates-engine/internal/cachekeys"
	"github.com/RatesEngine/rates-engine/internal/obs"
)

// refreshDivergenceAll iterates over every configured pair and asks
// the [DivergenceRefresher] to update its `div:<asset>` cache entry
// for the asset, using the shortest-window VWAP this Tick just
// wrote as the "our price" input.
//
// Best-effort: per-pair errors are counted via
// `obs.DivergenceRefreshTotal{outcome=…}` and logged at WARN; the
// Tick's overall outcome label is unaffected. The cache TTL
// (cachekeys.DivergenceTTL = 5 min) is the safety net — even if a
// few ticks fail, the API hot-path still serves stale-but-valid
// data while the worker recovers.
//
// Outcome labels:
//   - `ok`            — refresh succeeded; cache entry written.
//   - `no_vwap`       — VWAP cache miss for this pair (frozen,
//     empty window, transient cache error). Skip.
//   - `parse_error`   — cached value couldn't be parsed as float.
//     Indicates a writer regression.
//   - `refresh_error` — the refresher returned an error (network
//     failure to all references, marshal failure,
//     cache write failure). The cache entry is
//     NOT updated; the previous entry's TTL keeps
//     counting down.
//
// Skipped silently when DivergenceRefresher or Windows is nil/empty
// (operator config / launch order).
func (o *Orchestrator) refreshDivergenceAll(ctx context.Context, now time.Time) {
	if o.cfg.DivergenceRefresher == nil || len(o.cfg.Windows) == 0 {
		return
	}
	// Use the shortest configured window — gives the freshest VWAP
	// as the divergence input. Windows are operator-supplied in
	// increasing order (the default DefaultWindows = [5m, 1h, 24h]
	// satisfies this; operators who reorder get whatever comes
	// first — Windows is a slice not a map, no enforced sort).
	shortest := o.cfg.Windows[0]

	for _, pair := range o.cfg.Pairs {
		if err := ctx.Err(); err != nil {
			return
		}
		// Time the full per-pair refresh attempt — including the
		// VWAP cache lookup + parse + HTTP fan-out to every
		// configured reference. Recorded against the outcome
		// label so operators chart `ok` p95/p99 separately from
		// `refresh_error` (often the fast-fail path) and the
		// near-zero `no_vwap` / `parse_error` paths.
		start := time.Now()
		key := cachekeys.VWAP(pair.Base, pair.Quote, shortest)
		raw, err := o.cache.Get(ctx, key).Result()
		if err != nil {
			// Cache miss is normal-path on the first tick or after
			// a freeze; log at debug so an operator looking at INFO
			// doesn't see false noise.
			obs.DivergenceRefreshTotal.WithLabelValues("no_vwap").Inc()
			obs.DivergenceRefreshDurationSeconds.WithLabelValues("no_vwap").Observe(time.Since(start).Seconds())
			o.logger.Debug("divergence refresh: no vwap in cache",
				"pair", pair.String(), "window", shortest, "err", err)
			continue
		}
		ourPrice, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			obs.DivergenceRefreshTotal.WithLabelValues("parse_error").Inc()
			obs.DivergenceRefreshDurationSeconds.WithLabelValues("parse_error").Observe(time.Since(start).Seconds())
			o.logger.Warn("divergence refresh: vwap parse failed",
				"pair", pair.String(), "raw", raw, "err", err)
			continue
		}
		if err := o.cfg.DivergenceRefresher.RefreshPair(ctx, pair, ourPrice, now); err != nil {
			obs.DivergenceRefreshTotal.WithLabelValues("refresh_error").Inc()
			obs.DivergenceRefreshDurationSeconds.WithLabelValues("refresh_error").Observe(time.Since(start).Seconds())
			o.logger.Warn("divergence refresh failed",
				"pair", pair.String(), "err", err)
			continue
		}
		obs.DivergenceRefreshTotal.WithLabelValues("ok").Inc()
		obs.DivergenceRefreshDurationSeconds.WithLabelValues("ok").Observe(time.Since(start).Seconds())
	}
}
