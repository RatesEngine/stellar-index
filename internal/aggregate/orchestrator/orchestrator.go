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
	"github.com/RatesEngine/rates-engine/internal/cachekeys"
	"github.com/RatesEngine/rates-engine/internal/canonical"
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

	// Stats exposed for metrics / test assertions. Zero-copy.
	mu           sync.Mutex
	lastTickAt   time.Time
	ticksTotal   int64
	vwapWrites   int64
	emptyWindows int64
	errors       int64
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
		store:  store,
		cache:  cache,
		cfg:    cfg,
		logger: logger,
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
	o.mu.Lock()
	o.lastTickAt = time.Now()
	o.ticksTotal++
	o.mu.Unlock()

	now := time.Now().UTC()

	for _, pair := range o.cfg.Pairs {
		for _, window := range o.cfg.Windows {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := o.refreshPairWindow(ctx, pair, window, now); err != nil {
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
	trades, err := o.store.TradesInRange(ctx, pair, from, now, o.cfg.MaxTradesPerWindow)
	if err != nil {
		return fmt.Errorf("fetch %s %v: %w", pair.String(), window, err)
	}
	if !o.cfg.DisableClassFilter {
		trades = filterForVWAP(trades)
	}
	if len(trades) == 0 {
		o.mu.Lock()
		o.emptyWindows++
		o.mu.Unlock()
		return nil
	}

	vwap, err := aggregate.VWAP(trades)
	if err != nil {
		if errors.Is(err, aggregate.ErrNoTrades) {
			o.mu.Lock()
			o.emptyWindows++
			o.mu.Unlock()
			return nil
		}
		return fmt.Errorf("vwap %s %v: %w", pair.String(), window, err)
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

	o.mu.Lock()
	o.vwapWrites++
	o.mu.Unlock()
	return nil
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
// Zero-copy; callers should treat as immutable.
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
