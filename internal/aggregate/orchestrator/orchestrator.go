// Package orchestrator drives the aggregation layer's pre-compute
// cycle: on a fixed ticker, for every configured (pair, window)
// combination it fetches the window's trades from Timescale,
// computes VWAP, and writes the result to Redis so API requests
// serve from cache rather than recomputing on every query.
//
// Scope:
//
//   - Rolling-window VWAP per pair. Three windows are the built-in
//     default (5m, 1h, 24h via [DefaultWindows]); operators
//     override via `[aggregate].windows` in TOML.
//   - Class-filtered single-tier aggregation by default
//     (ClassExchange-only); operators flip
//     `[aggregate].disable_class_filter` to opt out and pull
//     aggregator + oracle classes too.
//   - Stablecoin → fiat proxy mapping (USDT/USDC/PYUSD → USD,
//     EUROC/EUROB → EUR, MXNe → MXN) when
//     `[aggregate].enable_stablecoin_fiat_proxy` is set; the
//     mapping lives in [internal/aggregate/stablecoin] and is
//     applied as a post-fetch pair rewrite before VWAP computes.
//   - Cross-pair triangulation (XLM/USD × USD/EUR = XLM/EUR) via
//     the `Triangulations` field; X2.5 forex-snap rule for
//     chained-fiat per [internal/aggregate/triangulate].
//   - Outlier filtering at fetch time via `OutlierSigmaThreshold`;
//     the math lives in [internal/aggregate/outliers].
//   - Divergence-cache refresh from each Tick via
//     `DivergenceRefresher` (the API's
//     `flags.divergence_warning` reads from the resulting
//     `div:<asset>` Redis keys).
//   - Multi-factor confidence scoring + ADR-0019 anomaly response
//     (Phase 1 + 2 — z-score / confidence / source-count freeze
//     thresholds via the `Anomaly` + `FreezeWriter` fields; the
//     API binary's `freeze.Looker` reads the markers this
//     publishes).
//
// Out of scope: CAGG refresh stays Timescale-driven (background
// job in migration 0002's `add_continuous_aggregate_policy`
// calls); the orchestrator deliberately does not refresh CAGGs
// itself.
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

// FXStore is the subset of timescale.Store the X2.5 forex-snap path
// needs. Optional — wired into [Config.FXStore] only when an operator
// runs chained-fiat triangulation. Nil keeps the orchestrator on the
// pre-snap cached-VWAP path for FX legs (the safe default for
// deployments without FX ingestion).
//
// Returns ([timescale.ErrNoFXQuote]) when no FX quote exists at-or-
// before cutoff — caller falls back to cached VWAP and increments
// [obs.AggregatorFXSnapFallbackTotal].
type FXStore interface {
	FXQuoteAtOrBefore(ctx context.Context, pair canonical.Pair, cutoff time.Time, fxSources []string) (*big.Rat, time.Time, string, error)
}

// Cache is the subset of redis.UniversalClient we need. Declared
// as an interface for test-time replacement.
//
// Get is used by the triangulation worker to read freshly-written
// leg VWAPs. Returns redis.Nil for absent keys (a leg's refresh
// produced an empty window); the triangulation pass treats absence
// as "skip this chain this tick" rather than fail.
type Cache interface {
	Set(ctx context.Context, key string, value any, expiration time.Duration) *redis.StatusCmd
	Get(ctx context.Context, key string) *redis.StringCmd
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

	// USDPeggedClassicAssets is the operator's parsed list of
	// classic credit assets (e.g. Circle's
	// `USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN`)
	// they declare as USD-pegged. Exists alongside the abstract-
	// stablecoin map in internal/aggregate/stablecoin.go: that map
	// keys on `crypto:CODE` (USDT/USDC/…) which is the layer most
	// CEX feeds report; classic credits carry full issuer identity
	// and are intentionally NOT in the abstract map. On Stellar
	// mainnet today the dominant USD-denominated DEX pairs are
	// quoted in classic credits, so without this list every
	// XLM/fiat:USD VWAP would be empty even with
	// EnableStablecoinFiatProxy on.
	//
	// Wired by the binary from cfg.Trades.USDPeggedClassicAssets so
	// the operator declares the allow-list in one place and both the
	// indexer (for trades.usd_volume population) and the aggregator
	// (for VWAP source expansion) pick it up. Empty = no classic
	// expansion, abstract-stablecoin map only.
	//
	// Only consulted when EnableStablecoinFiatProxy is true and the
	// target pair's quote is fiat:USD.
	USDPeggedClassicAssets []canonical.Asset

	// MinUSDVolume, when > 0, requires a window's total USD volume
	// (post-class, post-outlier) to meet the threshold before its
	// VWAP publishes. Applied only for fiat:USD-quoted pairs — for
	// those pairs every contributing trade originates off-chain
	// (CEX/FX) at the uniform 10^8 quote-decimal convention, so the
	// sum/1e8 → USD conversion is exact. Non-USD-quoted pairs are
	// exempt because cross-decimal arithmetic across mixed sources
	// (Stellar 7-decimal vs Soroban variable vs external 1e8) has no
	// clean single-USD interpretation; the dominant launch case is
	// XLM/USD which IS in scope.
	//
	// Default 0 = filter off. Production deployments stamp 10_000
	// (== $10k in window) per the AggregateConfig default, matching
	// L2.1 in `docs/architecture/launch-readiness-backlog.md`.
	MinUSDVolume float64

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

	// Triangulations is the operator-configured set of chain pricing
	// entries. After the per-(pair, window) refresh loop runs in
	// each Tick, the orchestrator iterates each chain, reads each
	// leg's freshly-cached VWAP, multiplies via
	// aggregate.TriangulateChain, and writes the implied target
	// VWAP to its own cache key. Empty (default) = no triangulation.
	//
	// Cardinality: each chain contributes len(Windows) cache keys
	// per tick. Operators tune the chain set explicitly — eager
	// triangulation across every fiat × stablecoin combinatorial
	// would blow out cardinality and bandwidth without proportional
	// downstream value.
	Triangulations []TriangulationChain

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

	// Phase2Thresholds tunes the ADR-0019 Phase 2 freeze condition
	// (3-signal AND on confidence + z + source count). Zero-value
	// fields fall back to the [Default*] package constants — an
	// operator with no override gets the documented stop-gap
	// behaviour. Set per-field to tighten or loosen any single
	// signal without restating the others.
	Phase2Thresholds Phase2Thresholds

	// Baselines, when non-nil, is consulted by the per-tick
	// confidence-score step (ADR-0019 §"Multi-factor confidence
	// score"). The orchestrator computes a [confidence.Score] from
	// the freshly-published VWAP + the cached MultiBaseline and
	// writes the result to Redis at `confidence:<base>:<quote>:<window>`.
	//
	// Nil = confidence step is skipped. Production wiring is an
	// adapter around `*timescale.Store.LatestBaseline`. The score
	// requires both a baseline (this field) and a previous-tick
	// VWAP comparator slot (kept internally) — the first tick after
	// startup always skips because there's no return to score yet.
	Baselines BaselineSource

	// FXStore, when non-nil, enables the X2.5 forex-factor snap rule
	// during triangulation. For each FX leg in a chain (a leg whose
	// Base AND Quote are both AssetFiat), the orchestrator queries the
	// most recent FX-source quote at-or-before the bucket-end
	// timestamp, instead of reading the leg's cached VWAP. This is
	// ADR-0018's across-region consistency primitive: every region
	// serving the same closed bucket queries the same hypertable and
	// gets the same FX rate.
	//
	// Nil = the snap rule is off; FX legs use the cached VWAP path
	// (almost-equivalent in steady state but not strictly compliant
	// with ADR-0018 across multi-region partitions). Wired to
	// timescale.Store at the binary boundary; the unit-test path
	// substitutes a mock implementing only [FXStore].
	FXStore FXStore

	// DivergenceRefresher, when non-nil, is called once per pair
	// per [Tick] to refresh the `div:<asset>` Redis cache so the
	// API's `flags.divergence_warning` flag has a producer (per
	// ADR-0019 / launch-readiness L2.10 + L2.11). Wired to
	// `internal/divergence.Service` at the aggregator binary
	// boundary; nil preserves the pre-Phase behaviour where the
	// cache stays empty and the flag is always false.
	//
	// Drives off the SHORTEST configured window's VWAP per pair —
	// gives operators ~Interval-fresh divergence detection without
	// hammering the external references on every (pair, window)
	// combination per tick.
	DivergenceRefresher DivergenceRefresher

	// StreamPublisher, when non-nil, is called once per successful
	// closed-bucket VWAP write to fan the event out to API-side SSE
	// subscribers (`/v1/price/stream`). Production wiring is the
	// Redis-pub/sub publisher in `internal/api/streaming/redispub`;
	// the matching API-side subscriber republishes on the in-process
	// streaming.Hub so SSE clients receive the event. Best-effort:
	// publish errors log + increment a metric but never block the
	// tick (the VWAP cache write itself is the source of truth).
	//
	// Nil = no fan-out. Leaves `/v1/price/stream` with no producer,
	// matching the pre-launch state where `s.hub == nil` returns 503.
	StreamPublisher StreamPublisher

	// ContributionSink, when non-nil, receives the per-source
	// breakdown of every successful VWAP compute. Production wires
	// `internal/storage/timescale.PriceSourceContributionSink` so
	// the explorer source-contribution donut on every price card
	// reads from a postgres-resident history rather than recomputing
	// at request time. Best-effort — sink failures log + continue.
	//
	// See migrations/0026 + Phase 2 of
	// docs/architecture/explorer-implementation-plan.md.
	ContributionSink ContributionSink

	// Logger is the structured logger. If nil, slog.Default() is
	// used.
	Logger *slog.Logger
}

// ContributionSink is the optional durable-mirror seam for
// per-source contributions to a windowed VWAP. Called once per
// (pair, window) at every successful VWAP compute.
type ContributionSink interface {
	RecordContributions(ctx context.Context, rec ContributionRecord) error
}

// ContributionRecord is the per-(pair, window, tick) shape passed
// to ContributionSink. Decoupled from the storage row shape so the
// sink can evolve without the orchestrator changing.
type ContributionRecord struct {
	Pair          canonical.Pair
	Window        time.Duration
	ComputedAt    time.Time
	Contributions []aggregate.SourceContribution
}

// DivergenceRefresher is the seam the orchestrator uses to keep the
// `div:<asset>` Redis cache populated. Production impl is
// [internal/divergence.Service]; tests substitute a fake that records
// invocations without making network calls.
//
// `ourPrice` is the per-pair shortest-window VWAP the orchestrator
// just computed; `observedAt` is the Tick's wall-clock time. The
// implementation is responsible for fetching external references,
// computing divergence percent, and writing the cache entry.
type DivergenceRefresher interface {
	RefreshPair(ctx context.Context, pair canonical.Pair, ourPrice float64, observedAt time.Time) error
}

// StreamPublisher is the seam the orchestrator uses to fan out
// closed-bucket events. Production impl is
// [internal/api/streaming/redispub.Publisher] (Redis PUBLISH); the
// API binary's matching subscriber (PR 2 of L3.9) republishes the
// event on its in-process [internal/api/streaming.Hub] so SSE
// subscribers on `/v1/price/stream` get fed.
//
// Called once per (pair, window) on every successful VWAP cache
// write — same call site as the freeze writer / confidence cache
// write, just on the publish side. Best-effort: a publish error
// logs + increments a metric but never blocks the next tick (the
// closed-bucket row is durable via the VWAP cache; the stream is
// enrichment, not a source-of-truth).
//
// Nil = no fan-out. Acceptable when no API binary is subscribed
// (e.g. local dev). Tests substitute a fake that records
// invocations.
type StreamPublisher interface {
	PublishClosedBucket(ctx context.Context, pair canonical.Pair, window time.Duration, valueDecimal string, observedAt time.Time) error
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

	// Triangulation pass — runs AFTER the per-pair refresh so each
	// chain's legs read from the freshly-cached VWAPs. Per-chain
	// failures are logged + counted but never abort the tick.
	o.triangulateAll(ctx)

	// Divergence refresh — runs AFTER the per-pair VWAPs are in
	// cache so RefreshPair has a fresh price to compare against
	// external references. Best-effort per-pair (errors logged +
	// counted, never abort the tick); the API's
	// `flags.divergence_warning` reads from the cache this populates.
	o.refreshDivergenceAll(ctx, now)

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

	if o.dropForMinUSDVolume(pair, trades) {
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

	o.flushContributions(ctx, pair, window, trades)

	// Phase 1 anomaly evaluation BEFORE cache write — class-deviation
	// + source-count threshold (the L2.4 stop-gap). On freeze we
	// keep the previous bucket's value in cache (don't overwrite)
	// and emit a freeze marker so flags.frozen=true on the next read.
	stateKey := pair.String() + ":" + window.String()
	if action, ok := o.evaluateAndMaybeFreeze(ctx, pair, window, vwap, trades, stateKey); !ok {
		_ = action
		return nil
	}

	// Phase 2 (ADR-0019): compute confidence + run the 3-signal AND
	// freeze check. Both happen BEFORE the VWAP cache write so a
	// Phase 2 freeze leaves the prior bucket's value intact in cache
	// — same semantic as Phase 1.
	prevForConfidence := o.prevVWAPs[stateKey]
	conf, confOK := o.computeConfidence(ctx, pair, vwap, prevForConfidence, trades)
	if confOK {
		input := confidenceWithSourceCount{
			Confidence:  conf.Score.Confidence,
			ZScore:      conf.ZScore,
			SourceCount: distinctSourceCount(trades),
		}
		if phase2FreezeFires(input, o.cfg.Phase2Thresholds) {
			o.markPhase2Freeze(ctx, pair, input)
			return nil
		}
	}

	// Cache write VWAP. Aggregator writers stay in big.Rat / big.Int
	// land; API readers parse the string back to a decimal. Float
	// encoding is prohibited on this path per ADR-0003.
	value := formatRatFixed(vwap, 12)
	key := cachekeys.VWAP(pair.Base, pair.Quote, window)
	ttl := cachekeys.VWAPTTL(window)
	if err := o.cache.Set(ctx, key, value, ttl).Err(); err != nil {
		return fmt.Errorf("redis set %s: %w", key, err)
	}

	// Cache write confidence (only on successful publish — frozen
	// buckets must NOT carry a stale score forward). Best-effort:
	// confidence enrichment, never a publish-blocking signal.
	if confOK {
		o.cacheConfidence(ctx, pair, window, conf.Score)
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

	o.publishToStream(ctx, pair, window, value, now)
	return nil
}

// publishToStream fans the closed-bucket event out to the
// configured StreamPublisher (Redis pub/sub in production). Pure
// best-effort: never returns an error — failures log + increment
// the per-outcome counter. The VWAP cache write upstream is the
// source of truth; the stream is enrichment for SSE subscribers.
func (o *Orchestrator) publishToStream(
	ctx context.Context,
	pair canonical.Pair,
	window time.Duration,
	value string,
	observedAt time.Time,
) {
	if o.cfg.StreamPublisher == nil {
		return
	}
	if err := o.cfg.StreamPublisher.PublishClosedBucket(ctx, pair, window, value, observedAt); err != nil {
		obs.AggregatorStreamPublishTotal.WithLabelValues("error").Inc()
		o.logger.Warn("stream publish failed",
			"pair", pair.String(), "window", window, "err", err)
		return
	}
	obs.AggregatorStreamPublishTotal.WithLabelValues("ok").Inc()
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

	sources, err := aggregate.ExpandTargetPairWithClassicPegs(target, o.cfg.USDPeggedClassicAssets)
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

// dropForMinUSDVolume returns true (and bumps the matching counters
// + emptyWindows stat) when the post-class + post-outlier window
// fails the per-pair USD-volume threshold. Caller treats the true
// case the same as a literally-empty window — skip the publish and
// move on. Extracted from refreshPairWindow to keep its cognitive
// complexity under the linter cap.
//
// See [Config.MinUSDVolume] for the threshold semantics.
func (o *Orchestrator) dropForMinUSDVolume(pair canonical.Pair, trades []canonical.Trade) bool {
	if o.cfg.MinUSDVolume <= 0 || !minUSDVolumeApplies(pair) {
		return false
	}
	if windowUSDVolume(trades) >= o.cfg.MinUSDVolume {
		return false
	}
	obs.AggregatorDroppedWindowsTotal.WithLabelValues("min_usd_volume").Inc()
	o.mu.Lock()
	o.emptyWindows++
	o.mu.Unlock()
	obs.AggregatorEmptyWindowsTotal.Inc()
	return true
}

// minUSDVolumeApplies reports whether the per-pair USD-volume
// threshold should be enforced for `pair`. True iff the quote asset
// is fiat:USD — the only case where every contributing trade comes
// from off-chain CEX/FX feeds at the uniform 10^8 quote-decimal
// convention. Non-USD-quoted pairs are exempt; see
// [Config.MinUSDVolume] for the rationale.
func minUSDVolumeApplies(pair canonical.Pair) bool {
	return pair.Quote.Type == canonical.AssetFiat && pair.Quote.Code == "USD"
}

// windowUSDVolume sums quote_amount across the supplied trades and
// converts to USD assuming the uniform 10^8 scale that off-chain
// (CEX/FX) sources stamp on
// `internal/sources/external/<venue>::externalAmountDecimals`.
//
// CALLER CONTRACT: only invoke when [minUSDVolumeApplies] returned
// true — that gate guarantees every trade in the slice is off-chain
// and thus at 1e8 scale. Calling on a mixed-decimal slice yields a
// numerically-wrong result.
//
// Empty input yields 0 (a window with zero contributing trades has
// zero USD volume by definition).
func windowUSDVolume(trades []canonical.Trade) float64 {
	if len(trades) == 0 {
		return 0
	}
	sum := new(big.Int)
	for i := range trades {
		amt := trades[i].QuoteAmount.BigInt()
		if amt == nil {
			continue
		}
		sum.Add(sum, amt)
	}
	if sum.Sign() == 0 {
		return 0
	}
	// 1e8 → USD. SetFrac + Float64 produces an IEEE 754 double; the
	// MinUSDVolume comparison is operator-tunable and not a
	// precision-sensitive math step, so float64 is acceptable here.
	rat := new(big.Rat).SetFrac(sum, big.NewInt(100_000_000))
	f, _ := rat.Float64()
	return f
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

// flushContributions emits one ContributionRecord per call to the
// configured sink (if any). Pulled out of refreshPairWindow so the
// hot-path function stays under the gocognit ceiling.
//
// Best-effort: sink failures log at DEBUG and don't propagate. The
// load-bearing operation is the VWAP cache write that happens
// after this returns.
func (o *Orchestrator) flushContributions(
	ctx context.Context,
	pair canonical.Pair,
	window time.Duration,
	trades []canonical.Trade,
) {
	if o.cfg.ContributionSink == nil {
		return
	}
	contributions := aggregate.SourceContributions(trades)
	if len(contributions) == 0 {
		return
	}
	if err := o.cfg.ContributionSink.RecordContributions(ctx, ContributionRecord{
		Pair:          pair,
		Window:        window,
		ComputedAt:    time.Now().UTC(),
		Contributions: contributions,
	}); err != nil {
		o.logger.Debug("contribution sink",
			"pair", pair.String(), "window", window, "err", err)
	}
}
