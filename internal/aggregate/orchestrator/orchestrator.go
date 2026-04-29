// Package orchestrator drives the aggregation layer's pre-compute
// cycle: on a fixed ticker, for every configured (pair, window)
// combination it fetches the window's trades from Timescale,
// computes VWAP, and writes the result to Redis so API requests
// serve from cache rather than recomputing on every query.
//
// Scope of this v1:
//
//   - Rolling-window VWAP per pair. Three windows baked in
//     (5m, 1h, 24h) covering the RFP's "real-time + historical"
//     shape without committing to per-operator window config yet.
//   - Passthrough single-source aggregation: every trade in the
//     window contributes to VWAP regardless of source class. This
//     matches what the API currently computes on-query; the
//     orchestrator's job here is to move that computation from
//     hot-path query-time to cold-path tick-time.
//
// Deliberately out of scope for v1 (each is a follow-up PR the
// orchestrator is shaped to accept cleanly):
//
//   - Stablecoin → fiat proxy mapping (USDT→USD, USDC→USD …).
//     Will live as a post-fetch pair rewrite before VWAP computes.
//   - Cross-pair triangulation (XLM/USD × USD/EUR = XLM/EUR).
//     Will live as a separate triangulation loop running alongside
//     the direct-pair loop.
//   - Divergence detection (our VWAP vs aggregator-class sources).
//     Will live as a separate worker that runs after the
//     orchestrator's tick and writes to `div:` Redis keys.
//   - Outlier filtering. Will wrap the raw-trade fetch before
//     VWAP sees it; existing internal/aggregate/outliers.go
//     already does the math.
//
// Runtime: one goroutine per window × pair pair-list entry in
// parallel during each tick. Ticks are serialised — if a tick's
// work spans longer than the tick interval, the next tick waits;
// this avoids piling queries on a slow Timescale.
package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/RatesEngine/rates-engine/internal/aggregate"
	"github.com/RatesEngine/rates-engine/internal/aggregate/anomaly"
	"github.com/RatesEngine/rates-engine/internal/cachekeys"
	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/obs"
	"github.com/RatesEngine/rates-engine/internal/sources/external"
)

// Store is the subset of timescale.Store the orchestrator needs.
// Declared as an interface so tests can substitute a mock without
// pulling up a real Timescale container.
type Store interface {
	TradesInRange(ctx context.Context, p canonical.Pair, from, to time.Time, limit int) ([]canonical.Trade, error)
}

// Cache is the subset of redis.UniversalClient we need. Declared
// as an interface for test-time replacement.
type Cache interface {
	Set(ctx context.Context, key string, value any, expiration time.Duration) *redis.StatusCmd
}

// FreezeMarker is the side-effect interface the orchestrator uses
// to record an ActionFreeze decision. Production wiring is
// freeze.Writer from internal/aggregate/freeze; declared here as an
// interface so tests can substitute a recorder without spinning up
// a Redis client.
//
// Mark MUST be idempotent on (asset, quote) — calling it twice for
// the same pair refreshes the marker's TTL, matching the policy
// "freeze stays in effect as long as the underlying anomaly
// persists".
type FreezeMarker interface {
	Mark(ctx context.Context, asset, quote canonical.Asset, decision anomaly.Decision) error
}

// Config controls the orchestrator's behaviour. Built from config.go
// at startup; the orchestrator itself doesn't know about TOML.
type Config struct {
	// Pairs is the list of pairs the orchestrator pre-computes
	// VWAP for. Empty = orchestrator is a no-op (valid for
	// deployments that want the binary running as a placeholder
	// while operators configure their pair set).
	Pairs []canonical.Pair

	// Windows is the list of rolling windows the orchestrator
	// computes VWAP over. If empty, defaults to [5m, 1h, 24h].
	Windows []time.Duration

	// Interval is the gap between tick-driven refreshes. Defaults
	// to 30 s — matches the Redis `price:` TTL of 60 s with
	// headroom for tick lateness.
	Interval time.Duration

	// MaxTradesPerWindow caps per-query row count to protect
	// Timescale from a runaway scan on an unexpectedly active
	// pair. Defaults to 10_000.
	MaxTradesPerWindow int

	// EnableStablecoinFiatProxy, when true, expands each fiat-
	// denominated target pair into the direct pair plus one
	// stablecoin-backed source pair per known peg and rewrites the
	// fetched trades through aggregate.ProxyPair before VWAP
	// computes. An operator who configures `XLM/fiat:USD` with
	// this enabled gets a VWAP drawn from XLM/fiat:USD (FX-feed
	// origins), XLM/crypto:USDT, XLM/crypto:USDC, XLM/crypto:DAI,
	// XLM/crypto:PYUSD, XLM/crypto:USDP — all collapsed onto the
	// target pair at the aggregator layer.
	//
	// Default (zero value = false): no expansion — the operator's
	// configured Pairs are fetched verbatim. Eager on-by-default
	// is held back because the expansion issues N+1 TradesInRange
	// calls per (pair, window) and many deployments that only
	// care about XLM/USDT want to opt into that extra IO
	// deliberately.
	//
	// See internal/aggregate/stablecoin.go for the pegged-token
	// map and the "aggregator policy, not decoder policy"
	// rationale (late binding keeps depeg signal visible in the
	// raw trade feed).
	EnableStablecoinFiatProxy bool

	// OutlierSigmaThreshold, when > 0, drops trades whose
	// QuoteAmount/BaseAmount price differs from the window's
	// arithmetic-mean price by more than sigma standard deviations
	// before VWAP computes. 0 (zero value) disables the filter —
	// every fetched trade contributes.
	//
	// Applied AFTER class filtering and stablecoin expansion: the
	// fetched-and-rewritten trade set is already homogenised onto
	// the target pair, so the standard-deviation arithmetic is
	// computed over comparable price values rather than across
	// different markets. Windows with fewer than 3 valid prices
	// fall through unchanged (see aggregate.FilterOutliers — too
	// few samples to estimate σ meaningfully).
	//
	// Default value (0) leaves the filter off so a fresh
	// orchestrator behaves identically to its pre-filter
	// predecessor; AggregateConfig in internal/config/config.go
	// stamps a 4.0 default at the binary boundary.
	OutlierSigmaThreshold float64

	// Anomaly, when non-nil, evaluates each fresh VWAP against its
	// previous bucket before publishing. Per ADR-0019:
	//
	//   - ActionAllow → publish normally.
	//   - ActionWarn  → publish; downstream divergence-warning path
	//                   (already handled out-of-band via #205).
	//   - ActionFreeze → DO NOT publish the new bucket; serve the
	//                    previous bucket's last-known-good value
	//                    instead. FreezeWriter writes the marker so
	//                    the API's flags.frozen fires.
	//
	// Nil = anomaly evaluation is off; every fresh VWAP publishes
	// regardless of deviation. Acceptable for early-bring-up
	// deployments where threshold tuning hasn't happened yet;
	// production deployments wire this at the binary boundary.
	Anomaly *anomaly.Checker

	// FreezeWriter, when non-nil and Anomaly is also non-nil, writes
	// a freeze marker to Redis when Anomaly returns ActionFreeze.
	// The API's freeze.Looker (#226) reads the same key to set
	// flags.frozen=true on /v1/price responses for the affected
	// pair.
	//
	// Nil = freeze action is observed (logged + metric incremented)
	// but no Redis marker is written. Acceptable when Anomaly is
	// also nil; loud-but-not-actionable when Anomaly is wired but
	// FreezeWriter isn't.
	FreezeWriter FreezeMarker

	// DisableClassFilter, when true, suppresses the aggregator's
	// default "ClassExchange trades only" filter and lets every row
	// in the fetched window contribute to VWAP regardless of source
	// class.
	//
	// Default (zero value = false): filter is ON. Rationale lives
	// in internal/sources/external/registry.go — aggregator-class
	// sources (coingecko / coinmarketcap / cryptocompare) are
	// derivatives of other venues' data and mixing them into our
	// VWAP double-counts the upstream; oracle-class sources publish
	// already-aggregated derived prices with their own governance.
	// Both belong in the /v1/sources feed for transparency but not
	// in the computed-VWAP numerator.
	//
	// Inverted phrasing (Disable-X rather than Only-X) is
	// deliberate: a Go bool can't distinguish "left unset" from
	// "explicitly false", so the safer default (filter on) is
	// encoded as the zero value and opt-out is an explicit true.
	// Flip this for historical-parity testing against a prior
	// release that hadn't yet introduced class filtering.
	DisableClassFilter bool

	// Logger is the structured logger. If nil, slog.Default() is
	// used.
	Logger *slog.Logger
}

// DefaultWindows is the built-in window set — three buckets
// covering hot (5m), warm (1h), and cold (24h) consumer needs.
var DefaultWindows = []time.Duration{
	5 * time.Minute,
	1 * time.Hour,
	24 * time.Hour,
}

// DefaultInterval is the built-in tick cadence. 30s matches the
// Redis price-key TTL of 60s with headroom for missed ticks;
// higher-frequency aggregation is a follow-up once the API's
// consumer pattern stabilises.
const DefaultInterval = 30 * time.Second

// DefaultMaxTradesPerWindow caps per-query scan size. 10,000 at
// ~150 trades/sec network-wide rate is ~67 seconds of activity —
// comfortably wider than any of the default windows.
const DefaultMaxTradesPerWindow = 10_000

// Orchestrator holds the wired dependencies and runs the tick loop.
type Orchestrator struct {
	store  Store
	cache  Cache
	cfg    Config
	logger *slog.Logger

	// prevVWAPs holds the last published VWAP per (pair, window) for
	// the anomaly evaluator's comparison input. Bounded by
	// len(Pairs) × len(Windows) — small. Reset to nil on
	// ActionFreeze (we publish-or-not but don't update the
	// comparator slot during a freeze, so the next bucket compares
	// against the same prev).
	//
	// Tick is serialised (the ticker drops events that arrive while
	// a previous Tick is still running), and refreshPairWindow runs
	// sequentially within Tick — so this map needs no separate lock.
	prevVWAPs map[string]*big.Rat

	// Stats exposed for metrics / test assertions. Zero-copy.
	mu             sync.Mutex
	lastTickAt     time.Time
	ticksTotal     int64
	vwapWrites     int64
	emptyWindows   int64
	errors         int64
	freezesEngaged int64
}

// New constructs an Orchestrator with defaults applied.
func New(store Store, cache Cache, cfg Config) *Orchestrator {
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultInterval
	}
	if len(cfg.Windows) == 0 {
		cfg.Windows = DefaultWindows
	}
	if cfg.MaxTradesPerWindow <= 0 {
		cfg.MaxTradesPerWindow = DefaultMaxTradesPerWindow
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Orchestrator{
		store:     store,
		cache:     cache,
		cfg:       cfg,
		logger:    logger,
		prevVWAPs: make(map[string]*big.Rat, len(cfg.Pairs)*max(len(cfg.Windows), 1)),
	}
}

// Run blocks until ctx is cancelled, invoking [Tick] on
// [Config.Interval] cadence. First tick fires immediately on
// startup so a freshly-launched aggregator has warm Redis keys
// before the API's first query.
func (o *Orchestrator) Run(ctx context.Context) error {
	if len(o.cfg.Pairs) == 0 {
		o.logger.Warn("orchestrator: no pairs configured — running as no-op")
	}

	// Kick off an immediate first tick.
	if err := o.Tick(ctx); err != nil && !errors.Is(err, context.Canceled) {
		o.logger.Warn("initial tick failed", "err", err)
	}

	t := time.NewTicker(o.cfg.Interval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			if err := o.Tick(ctx); err != nil && !errors.Is(err, context.Canceled) {
				o.logger.Warn("tick failed", "err", err)
			}
		}
	}
}

// Tick runs one aggregation cycle — fetch trades, compute VWAP,
// write Redis for every (pair, window) combination in Config.
// Exported so tests can drive deterministic cycles without waiting
// on the ticker.
func (o *Orchestrator) Tick(ctx context.Context) error {
	now := time.Now().UTC()
	o.mu.Lock()
	o.lastTickAt = now
	o.ticksTotal++
	o.mu.Unlock()

	tickHadError := false
	for _, pair := range o.cfg.Pairs {
		for _, window := range o.cfg.Windows {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := o.refreshPairWindow(ctx, pair, window, now); err != nil {
				tickHadError = true
				o.mu.Lock()
				o.errors++
				o.mu.Unlock()
				o.logger.Warn("refresh failed",
					"pair", pair.String(),
					"window", window,
					"err", err)
				continue
			}
		}
	}
	outcome := "ok"
	if tickHadError {
		outcome = "error"
	}
	obs.AggregatorTicksTotal.WithLabelValues(outcome).Inc()
	return nil
}

// refreshPairWindow computes VWAP for one (pair, window) and
// writes it to Redis. ErrNoTrades is a normal-path outcome (the
// window was empty for this pair) and not propagated as an error.
func (o *Orchestrator) refreshPairWindow(
	ctx context.Context,
	pair canonical.Pair,
	window time.Duration,
	now time.Time,
) error {
	from := now.Add(-window)
	trades, err := o.fetchForTarget(ctx, pair, from, now)
	if err != nil {
		return fmt.Errorf("fetch %s %v: %w", pair.String(), window, err)
	}
	preFilter := len(trades)
	if !o.cfg.DisableClassFilter {
		trades = filterForVWAP(trades)
		if dropped := preFilter - len(trades); dropped > 0 {
			obs.AggregatorDroppedTradesTotal.WithLabelValues("class").Add(float64(dropped))
		}
	}
	if o.cfg.OutlierSigmaThreshold > 0 {
		preOutlier := len(trades)
		trades = aggregate.FilterOutliers(trades, o.cfg.OutlierSigmaThreshold)
		if dropped := preOutlier - len(trades); dropped > 0 {
			obs.AggregatorDroppedTradesTotal.WithLabelValues("outlier").Add(float64(dropped))
		}
	}
	if len(trades) == 0 {
		o.mu.Lock()
		o.emptyWindows++
		o.mu.Unlock()
		obs.AggregatorEmptyWindowsTotal.Inc()
		return nil
	}

	vwap, err := aggregate.VWAP(trades)
	if err != nil {
		if errors.Is(err, aggregate.ErrNoTrades) {
			o.mu.Lock()
			o.emptyWindows++
			o.mu.Unlock()
			obs.AggregatorEmptyWindowsTotal.Inc()
			return nil
		}
		return fmt.Errorf("vwap %s %v: %w", pair.String(), window, err)
	}

	// Anomaly evaluation BEFORE cache write — when ActionFreeze
	// fires we keep the previous bucket's value in cache (don't
	// overwrite) so the API serves the LKG, and emit a freeze
	// marker so flags.frozen=true on the next /v1/price read.
	stateKey := pair.String() + ":" + window.String()
	if action, ok := o.evaluateAndMaybeFreeze(ctx, pair, window, vwap, trades, stateKey); !ok {
		// ActionFreeze — bail out before touching the cache. The
		// previous bucket's VWAP stays valid; the marker has been
		// written by FreezeWriter.
		_ = action
		return nil
	}

	// Serialise to a decimal-string representation. Aggregator
	// writers stay in big.Rat / big.Int land; API readers parse
	// the string back to a decimal. Float encoding is prohibited
	// on this path per ADR-0003.
	value := formatRatFixed(vwap, 12)
	key := cachekeys.VWAP(pair.Base, pair.Quote, window)
	ttl := cachekeys.VWAPTTL(window)
	if err := o.cache.Set(ctx, key, value, ttl).Err(); err != nil {
		return fmt.Errorf("redis set %s: %w", key, err)
	}

	// Update the prev-VWAP comparator slot ONLY on successful
	// publish — frozen buckets keep the prior slot intact so the
	// next tick compares against the same baseline rather than
	// drifting forward.
	o.prevVWAPs[stateKey] = vwap

	o.mu.Lock()
	o.vwapWrites++
	o.mu.Unlock()
	obs.AggregatorVWAPWritesTotal.Inc()
	return nil
}

// evaluateAndMaybeFreeze runs the anomaly check on a fresh VWAP
// and writes a freeze marker when the decision says so. Returns
// (decision, ok=true) for Allow / Warn — caller proceeds to the
// cache write — and (decision, ok=false) for Freeze — caller skips
// the cache write so the previous bucket's value continues to
// serve.
//
// When o.cfg.Anomaly is nil, the evaluator is off — every fresh
// VWAP returns Allow without computing a decision. Acceptable for
// early bring-up; production deployments wire Anomaly + FreezeWriter
// at the binary boundary.
func (o *Orchestrator) evaluateAndMaybeFreeze(
	ctx context.Context,
	pair canonical.Pair,
	window time.Duration,
	currVWAP *big.Rat,
	trades []canonical.Trade,
	stateKey string,
) (anomaly.Action, bool) {
	if o.cfg.Anomaly == nil {
		return anomaly.ActionAllow, true
	}
	prev := o.prevVWAPs[stateKey]
	decision := o.cfg.Anomaly.Evaluate(anomaly.Observation{
		Pair:        pair,
		PrevVWAP:    prev,
		CurrVWAP:    currVWAP,
		SourceCount: distinctSourceCount(trades),
	})
	if !decision.IsFrozen() {
		return decision.Action, true
	}

	o.mu.Lock()
	o.freezesEngaged++
	o.mu.Unlock()
	obs.AnomalyFreezeEngagedTotal.WithLabelValues(string(decision.Class)).Inc()

	o.logger.Warn("anomaly freeze engaged",
		"pair", pair.String(),
		"window", window,
		"class", decision.Class,
		"deviation_pct", decision.DeviationPct,
		"reason", decision.Reason)

	if o.cfg.FreezeWriter != nil {
		if err := o.cfg.FreezeWriter.Mark(ctx, pair.Base, pair.Quote, decision); err != nil {
			o.logger.Warn("freeze writer mark failed",
				"pair", pair.String(),
				"err", err)
			// Soft-fail: anomaly was detected, marker write failed,
			// API won't see flags.frozen. Operators alert on
			// AnomalyFreezeEngagedTotal vs the API-side flag rate;
			// a sustained gap = the writer is broken. Don't 5xx the
			// tick over it.
		}
	}
	return decision.Action, false
}

// distinctSourceCount returns how many distinct trade.Source values
// contributed to the supplied trades. Zero on empty input — the
// caller short-circuits before calling Evaluate, but the guard is
// cheap enough to keep here too.
func distinctSourceCount(trades []canonical.Trade) int {
	if len(trades) == 0 {
		return 0
	}
	seen := make(map[string]struct{}, 8)
	for i := range trades {
		seen[trades[i].Source] = struct{}{}
	}
	return len(seen)
}

// fetchForTarget pulls trades from the store for a single target
// pair and window. When EnableStablecoinFiatProxy is off this is a
// single TradesInRange call for pair itself; when on, the pair is
// expanded via aggregate.ExpandTargetPair into a direct pair plus
// one backer pair per peg, each backer pair is fetched and its
// trades are rewritten onto the target pair.
//
// Per-backer fetch errors are logged and skipped rather than
// aborting the whole window — a single connector misbehaving at
// the Timescale layer shouldn't black out an otherwise-healthy
// aggregation target.
func (o *Orchestrator) fetchForTarget(
	ctx context.Context,
	target canonical.Pair,
	from, to time.Time,
) ([]canonical.Trade, error) {
	if !o.cfg.EnableStablecoinFiatProxy {
		return o.store.TradesInRange(ctx, target, from, to, o.cfg.MaxTradesPerWindow)
	}

	sources, err := aggregate.ExpandTargetPair(target)
	if err != nil {
		return nil, fmt.Errorf("expand target %s: %w", target.String(), err)
	}

	// Collect trades from each source pair. Rewriting non-target
	// source trades through ProxyPair happens here — by the time
	// the merged slice leaves this function every trade carries
	// the target pair and the downstream VWAP math treats them
	// as homogenous.
	var merged []canonical.Trade
	for _, src := range sources {
		batch, ferr := o.store.TradesInRange(ctx, src, from, to, o.cfg.MaxTradesPerWindow)
		if ferr != nil {
			o.logger.Warn("stablecoin-expansion fetch failed",
				"target", target.String(),
				"source_pair", src.String(),
				"err", ferr,
			)
			continue
		}
		if src.Equal(target) {
			merged = append(merged, batch...)
			continue
		}
		for i := range batch {
			batch[i].Pair = target
			merged = append(merged, batch[i])
		}
	}
	return merged, nil
}

// filterForVWAP drops trades whose source is not registered as a
// Class=Exchange + IncludeInVWAP=true venue. This is the
// aggregator-policy layer that implements the "only genuine
// exchange trades contribute to the average" rule.
//
// Unknown sources (not in external.Registry) are dropped — the
// registry's fail-closed default (ClassExchange, IncludeInVWAP=
// false) already handles that: they're VISIBLE in /v1/sources but
// don't vote in VWAP unless an operator explicitly registers them.
//
// Preserves input order so VWAP's weighted-mean semantics stay
// deterministic under the same input set.
func filterForVWAP(trades []canonical.Trade) []canonical.Trade {
	out := trades[:0]
	for _, t := range trades {
		md := external.Lookup(t.Source)
		if md.Class == external.ClassExchange && md.IncludeInVWAP {
			out = append(out, t)
		}
	}
	return out
}

// formatRatFixed returns a fixed-precision decimal string
// representation of r. 12 decimal places covers every sensible
// crypto/fiat price range without float-precision loss.
//
// We don't use (*big.Rat).FloatString because Go's default
// rounding is banker's round-half-to-even — fine for accounting
// but not the "truncate toward zero" convention the API spec
// mandates. Rolling a tiny fixed-precision formatter keeps the
// rounding behaviour explicit.
func formatRatFixed(r *big.Rat, decimals int) string {
	// Multiply numerator by 10^decimals, divide by denominator,
	// then insert the decimal point.
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	num := new(big.Int).Mul(r.Num(), scale)
	q, _ := new(big.Int).QuoRem(num, r.Denom(), new(big.Int))

	// Build the string. q is the integer part at 10^decimals scale
	// → split into int and fractional halves.
	negative := q.Sign() < 0
	if negative {
		q.Neg(q)
	}
	digits := q.String()
	if len(digits) <= decimals {
		// Left-pad fractional part.
		pad := decimals - len(digits) + 1
		digits = zeroes(pad) + digits
	}
	cut := len(digits) - decimals
	out := digits[:cut] + "." + digits[cut:]
	if negative {
		out = "-" + out
	}
	return out
}

func zeroes(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = '0'
	}
	return string(b)
}

// Stats is a snapshot of the orchestrator's runtime counters.
// All fields are value types; returning by value gives the
// caller an independent copy that won't change under their feet
// while the orchestrator keeps ticking.
type Stats struct {
	LastTickAt   time.Time
	TicksTotal   int64
	VWAPWrites   int64
	EmptyWindows int64
	Errors       int64
}

// Stats returns a snapshot of the counters.
func (o *Orchestrator) Stats() Stats {
	o.mu.Lock()
	defer o.mu.Unlock()
	return Stats{
		LastTickAt:   o.lastTickAt,
		TicksTotal:   o.ticksTotal,
		VWAPWrites:   o.vwapWrites,
		EmptyWindows: o.emptyWindows,
		Errors:       o.errors,
	}
}
