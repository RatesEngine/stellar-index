package orchestrator

import (
	"context"
	"math/big"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/obs"
)

// RewrittenSnapshotSink is the optional durable-mirror seam for
// stablecoin-fiat-proxy rewritten VWAPs (ADR-0025). Called once
// per (pair, window) at every successful VWAP compute whose target
// quote is fiat — the rewriting that already happens in
// [Orchestrator.fetchForTarget] produces a value that lives only
// in the Redis `vwap:` cache today; this sink mirrors it to a
// hypertable so historical surfaces (`/v1/vwap`, `/v1/twap`,
// `/v1/changes`, `/v1/history`) can answer fiat-target queries.
//
// Implementations live at the binary boundary so the orchestrator
// package doesn't take a storage import. See
// `cmd/ratesengine-aggregator/main.go::rewrittenSnapshotSink`.
type RewrittenSnapshotSink interface {
	InsertRewrittenVWAPSnapshot(ctx context.Context, snap RewrittenSnapshot) error
}

// RewrittenSnapshot is the per-tick payload the orchestrator hands
// to a [RewrittenSnapshotSink]. Mirrors what the Redis vwap: key
// already publishes (one record per pair × window per tick) plus
// the volume / trade-count / sources fields the historical reader
// needs.
//
// VWAP / Volume / VolumeUSD are decimal strings (NUMERIC at the
// postgres layer); ADR-0003 mandates string-based handling at the
// boundary so a float round-trip never loses precision.
type RewrittenSnapshot struct {
	ObservedAt    time.Time
	Pair          canonical.Pair
	WindowSeconds int
	VWAP          string
	Volume        string
	VolumeUSD     string // empty when no contributing trade had usd_volume
	TradeCount    int64
	Sources       []string
}

// publishRewrittenSnapshot mirrors a freshly-computed rewritten-pair
// VWAP to the configured [RewrittenSnapshotSink]. Best-effort: never
// returns an error to refreshPairWindow — sink failures log +
// increment the per-outcome counter, but the Redis VWAP write
// upstream is the source of truth for the live cache.
//
// Only invoked when (a) a sink is wired, and (b) the target's quote
// is fiat-typed — non-fiat targets aren't part of the proxy
// rewriting at all (no ProxyPair lookup runs for them) so storing
// them in this table would be misleading.
func (o *Orchestrator) publishRewrittenSnapshot(
	ctx context.Context,
	pair canonical.Pair,
	window time.Duration,
	value string,
	trades []canonical.Trade,
	observedAt time.Time,
) {
	if o.cfg.RewrittenSnapshotSink == nil {
		return
	}
	if pair.Quote.Type != canonical.AssetFiat {
		return
	}
	volumeBase, volumeUSD := windowVolumes(trades, pair)
	snap := RewrittenSnapshot{
		ObservedAt:    observedAt.UTC(),
		Pair:          pair,
		WindowSeconds: int(window.Seconds()),
		VWAP:          value,
		Volume:        volumeBase,
		VolumeUSD:     volumeUSD,
		TradeCount:    int64(len(trades)),
		Sources:       distinctSourceNames(trades),
	}
	if err := o.cfg.RewrittenSnapshotSink.InsertRewrittenVWAPSnapshot(ctx, snap); err != nil {
		obs.AggregatorRewrittenSnapshotTotal.WithLabelValues("error").Inc()
		o.logger.Warn("rewritten snapshot write failed",
			"pair", pair.String(), "window", window, "err", err)
		return
	}
	obs.AggregatorRewrittenSnapshotTotal.WithLabelValues("ok").Inc()
}

// windowVolumes sums the contributing trades' base volume and,
// for fiat:USD-quoted targets, derives a USD volume estimate via the
// same scale convention `windowUSDVolume` uses (uniform 10^8 quote
// decimals). Returns ("0", "") when no trades; the empty USD string
// passes through to NULLIF in the storage layer so postgres stores
// NULL (matching prices_1m.volume_usd's "no usd-volume context"
// shape).
//
// USD-volume is non-empty only when target.Quote is fiat:USD —
// that gate matches `minUSDVolumeApplies` since the aggregator's
// uniform-1e8 scale assumption only holds for the off-chain or
// stablecoin-rewritten USD path (cf. orchestrator.go::windowUSDVolume).
// EUR / GBP fiat targets get base volume only; rate-engine v1 has
// no FX-anchor multiplication for non-USD on-chain quotes (L2.2
// phase 2, post-launch).
func windowVolumes(trades []canonical.Trade, target canonical.Pair) (base, usd string) {
	if len(trades) == 0 {
		return "0", ""
	}
	baseSum := new(big.Int)
	for i := range trades {
		amt := trades[i].BaseAmount.BigInt()
		if amt == nil {
			continue
		}
		baseSum.Add(baseSum, amt)
	}
	usdStr := ""
	if target.Quote.Type == canonical.AssetFiat && target.Quote.Code == "USD" && minUSDVolumeApplies(target) {
		// Same `Σ quote_amount / 10^8 → USD` convention windowUSDVolume
		// uses, but rendered as a decimal string instead of float64
		// so the postgres NUMERIC keeps full precision.
		quoteSum := new(big.Int)
		for i := range trades {
			amt := trades[i].QuoteAmount.BigInt()
			if amt == nil {
				continue
			}
			quoteSum.Add(quoteSum, amt)
		}
		if quoteSum.Sign() > 0 {
			rat := new(big.Rat).SetFrac(quoteSum, big.NewInt(100_000_000))
			usdStr = rat.FloatString(8)
		}
	}
	return baseSum.String(), usdStr
}

// distinctSourceNames returns the unique trade.Source values across
// the supplied trades. Order is not stable; callers that compare
// must sort. Used to populate the snapshot row's `sources` field —
// matches the prices_1m CAGG's array_agg(DISTINCT source) shape.
func distinctSourceNames(trades []canonical.Trade) []string {
	if len(trades) == 0 {
		return []string{}
	}
	seen := make(map[string]struct{}, 8)
	out := make([]string, 0, 8)
	for i := range trades {
		s := trades[i].Source
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
