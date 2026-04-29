// Binary ratesengine-aggregator computes VWAP over the ingested
// canonical trade stream and writes pre-aggregated results to Redis
// so API requests serve from cache rather than recomputing on every
// query.
//
// v1 scope (PR 182):
//
//   - Rolling-window VWAP per configured pair, written to Redis
//     keys `vwap:<base>:<quote>:<window-seconds>` on a 30 s cadence
//     (configurable).
//   - Passthrough single-source aggregation: every trade in the
//     window contributes regardless of source class. The API
//     already computes VWAP on-query; this binary moves the
//     computation off the hot path.
//
// Deferred to follow-up PRs (each drops into orchestrator.Config
// without shape change):
//
//   - CAGG refresh driver (Timescale's background job handles it
//     today; manual triggering lands when we want tight refresh
//     guarantees).
//   - Triangulation worker (XLM/USD × USD/EUR = XLM/EUR).
//   - Divergence detector (flags aggregator-class drift).
//   - Outlier filter wrap on the raw-trade fetch.
//
// Already wired through TOML (see [aggregate] in
// docs/reference/config/README.md):
//
//   - disable_class_filter            — opt out of ClassExchange-only VWAP.
//   - enable_stablecoin_fiat_proxy    — expand XLM/fiat:USD to pull
//     XLM/USDT/USDC/DAI/PYUSD/USDP
//     and collapse onto the target.
//   - interval_seconds                — tick cadence override.
//   - max_trades_per_window           — per-window scan cap.
//
// Flags:
//
//	-config PATH    TOML config file (required).
//	-dry-run        Load config, open connections, validate, exit.
//
// Graceful shutdown: SIGINT + SIGTERM cancel the root context;
// the orchestrator's Tick unwinds on the next iteration.
//
// ⚠ CAGG TWAP CAVEAT ⚠
//
// Migration 0002 defines a `twap` column in prices_1m / _15m / _1h /
// _4h / _1d / _1w / _1mo as `avg(quote_amount / base_amount)` — the
// arithmetic mean of observed trade prices, NOT a time-weighted
// average. True TWAP needs inter-trade durations that the CAGG
// definitions don't capture. The v1 orchestrator sidesteps this by
// computing VWAP (not TWAP) from raw trades; TWAP-via-CAGG lands
// with either internal/aggregate/twap.go (Go-side) or a corrected
// CAGG that stores per-bucket duration.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/RatesEngine/rates-engine/internal/aggregate/anomaly"
	"github.com/RatesEngine/rates-engine/internal/aggregate/baseline"
	"github.com/RatesEngine/rates-engine/internal/aggregate/freeze"
	"github.com/RatesEngine/rates-engine/internal/aggregate/orchestrator"
	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/config"
	"github.com/RatesEngine/rates-engine/internal/obs"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
	"github.com/RatesEngine/rates-engine/internal/version"
)

// Baseline-refresh tunables. Deliberately not surfaced as TOML knobs
// in this slice — sensible defaults today, an operator override only
// if production usage shows we need it.
const (
	// baselineRefreshCadence: how often to recompute every pair's
	// baseline. 30-day rolling MAD barely moves minute-to-minute, so
	// hourly is plenty (and cheap on the hypertable).
	baselineRefreshCadence = 1 * time.Hour
	// baselineRefreshConcurrency: pairs computed in flight at once.
	// 4 keeps the DB connection pool well-fed without saturating it
	// even on the largest pair sets.
	baselineRefreshConcurrency = 4
)

func main() {
	var (
		cfgPath = flag.String("config", "", "Path to TOML config file (required)")
		dryRun  = flag.Bool("dry-run", false, "Load config + open connections + exit without running the ticker")
	)
	flag.Parse()

	if *cfgPath == "" {
		fmt.Fprintln(os.Stderr, "ratesengine-aggregator: -config is required")
		flag.Usage()
		os.Exit(2)
	}

	if err := run(*cfgPath, *dryRun); err != nil {
		fmt.Fprintf(os.Stderr, "ratesengine-aggregator: %v\n", err)
		os.Exit(1)
	}
}

//nolint:gocognit,gocyclo,funlen // top-level binary lifecycle — splitting reduces readability of dependency-construction order
func run(cfgPath string, dryRun bool) error {
	cfg, err := config.LoadWithEnv(cfgPath)
	if err != nil {
		return err
	}

	logger := mkLogger(cfg.Obs)
	logger.Info("starting",
		"version", version.String(),
		"region", cfg.Region.ID,
		"dry_run", dryRun,
	)

	rootCtx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// ─── Storage ─────────────────────────────────────────────────
	store, err := timescale.Open(rootCtx, cfg.Storage.PostgresDSN)
	if err != nil {
		return fmt.Errorf("storage: %w", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			logger.Warn("storage close", "err", err)
		}
	}()
	logger.Info("storage connected")

	// ─── Redis ───────────────────────────────────────────────────
	// Required for the aggregator — no useful pre-compute without
	// a cache to write to. Dry-run pings explicitly so config
	// drift surfaces at startup rather than at first tick.
	if cfg.Storage.RedisAddr == "" {
		return errors.New("storage.redis_addr is required — aggregator writes VWAP to Redis")
	}
	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.Storage.RedisAddr,
		Password: cfg.Storage.RedisPassword,
	})
	defer func() { _ = rdb.Close() }()
	if dryRun {
		pingCtx, cancelPing := context.WithTimeout(rootCtx, 5*time.Second)
		err := rdb.Ping(pingCtx).Err()
		cancelPing()
		if err != nil {
			return fmt.Errorf("redis: ping: %w", err)
		}
		logger.Info("redis reachable", "addr", cfg.Storage.RedisAddr)
	}

	// ─── Pair + window resolution ────────────────────────────────
	// Operator override via [aggregate].pairs / .windows wins; an
	// empty list falls back to the built-in defaults so a fresh
	// deployment with no aggregator config still runs.
	pairs, err := cfg.Aggregate.AggregatorPairs()
	if err != nil {
		// Validate() already rejected this at startup; reaching here
		// means validation was bypassed. Fail loud rather than silently
		// fall back.
		return fmt.Errorf("aggregator: %w", err)
	}
	if len(pairs) == 0 {
		pairs = defaultPairs()
		logger.Info("aggregator pair set: using built-in default", "count", len(pairs))
	} else {
		logger.Info("aggregator pair set: operator override", "count", len(pairs))
	}

	windows, err := cfg.Aggregate.AggregatorWindows()
	if err != nil {
		return fmt.Errorf("aggregator: %w", err)
	}
	if len(windows) > 0 {
		logger.Info("aggregator windows: operator override", "count", len(windows))
	}

	// ─── Anomaly checker + freeze writer (ADR-0019) ─────────────
	// Wired only when the operator has flipped anomaly.enabled in
	// TOML. nil values mean "feature off" — orchestrator skips the
	// evaluate-and-maybe-freeze step.
	checker, err := buildAnomalyChecker(cfg.Anomaly)
	if err != nil {
		return fmt.Errorf("anomaly checker: %w", err)
	}
	var freezeWriter orchestrator.FreezeMarker
	if checker != nil && rdb != nil {
		w, err := freeze.NewWriter(rdb, 0) // 0 → cachekeys.FreezeTTL default
		if err != nil {
			return fmt.Errorf("freeze writer: %w", err)
		}
		freezeWriter = w
		logger.Info("anomaly + freeze: wired", "thresholds", len(cfg.Anomaly.Thresholds))
	} else if checker != nil {
		logger.Warn("anomaly enabled but no Redis — freeze markers won't be written; anomaly metric still emits")
	}

	// ─── Triangulation chains ───────────────────────────────────
	triangulations, err := buildTriangulations(cfg.Aggregate)
	if err != nil {
		return fmt.Errorf("triangulations: %w", err)
	}
	if len(triangulations) > 0 {
		logger.Info("triangulation chains: configured", "count", len(triangulations))
	}

	orch := orchestrator.New(store, rdb, orchestrator.Config{
		Pairs:              pairs,
		Windows:            windows, // nil → orchestrator.DefaultWindows
		Interval:           time.Duration(cfg.Aggregate.IntervalSeconds) * time.Second,
		MaxTradesPerWindow: cfg.Aggregate.MaxTradesPerWindow,
		Anomaly:            checker,
		FreezeWriter:       freezeWriter,
		Triangulations:     triangulations,
		Baselines:          baselineLookupAdapter{store: store},
		Phase2Thresholds: orchestrator.Phase2Thresholds{
			ConfidenceMaxFreeze:  cfg.Anomaly.Phase2.ConfidenceMaxFreeze,
			ZScoreMinFreeze:      cfg.Anomaly.Phase2.ZScoreMinFreeze,
			SourceCountMaxFreeze: cfg.Anomaly.Phase2.SourceCountMaxFreeze,
		},
		DisableClassFilter:        cfg.Aggregate.DisableClassFilter,
		EnableStablecoinFiatProxy: cfg.Aggregate.EnableStablecoinFiatProxy,
		OutlierSigmaThreshold:     cfg.Aggregate.OutlierSigmaThreshold,
		Logger:                    logger,
	})

	if dryRun {
		logger.Info("dry-run complete — exiting")
		return nil
	}

	// ─── Baseline refresh worker (ADR-0019 Phase 2) ─────────────
	// Slow-cadence loop alongside the orchestrator: hourly pulls
	// each pair's 30-day VWAP window from prices_1m, computes
	// Median + MAD via internal/aggregate/baseline, and UPSERTs the
	// row into volatility_baseline_1m. Outcomes go to Prometheus.
	//
	// Runs in its own goroutine so a slow refresh cycle never holds
	// up orch.Run's tick.
	refresher := baseline.NewRefresher(
		baselineSourceAdapter{store: store},
		baselineSinkAdapter{store: store},
		baseline.DefaultWindow,
		logger.With("component", "baseline-refresh"),
	)
	var refresherWG sync.WaitGroup
	refresherWG.Add(1)
	go func() {
		defer refresherWG.Done()
		runBaselineRefresh(rootCtx, refresher, pairs, logger.With("component", "baseline-refresh"))
	}()

	// ─── Run ─────────────────────────────────────────────────────
	logger.Info("orchestrator starting")
	if err := orch.Run(rootCtx); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("orchestrator: %w", err)
	}
	logger.Info("orchestrator stopped", "stats", orch.Stats())

	// Wait for the baseline goroutine to honour rootCtx cancellation.
	refresherWG.Wait()
	return nil
}

// runBaselineRefresh ticks the baseline refresher on
// [baselineRefreshCadence], emitting per-outcome Prometheus counters
// for each cycle. Returns on rootCtx cancellation.
//
// Initial cycle runs immediately on startup so the
// volatility_baseline_1m table is populated as fast as possible —
// without this, a fresh deployment waits a full cadence interval
// before the API can rely on baseline lookups for the confidence
// score.
func runBaselineRefresh(ctx context.Context, r *baseline.Refresher, pairs []canonical.Pair, logger *slog.Logger) {
	tick := func() {
		started := time.Now()
		sum := r.RefreshAll(ctx, pairs, baselineRefreshConcurrency)
		obs.AggregatorBaselineRefreshTotal.WithLabelValues("ok").Add(float64(sum.OK))
		obs.AggregatorBaselineRefreshTotal.WithLabelValues("not_enough_samples").Add(float64(sum.NotEnoughSamples))
		obs.AggregatorBaselineRefreshTotal.WithLabelValues("read_error").Add(float64(sum.ReadErrors))
		obs.AggregatorBaselineRefreshTotal.WithLabelValues("write_error").Add(float64(sum.WriteErrors))
		logger.Info("baseline refresh complete",
			"ok", sum.OK,
			"not_enough_samples", sum.NotEnoughSamples,
			"read_errors", sum.ReadErrors,
			"write_errors", sum.WriteErrors,
			"elapsed", time.Since(started).String(),
		)
	}

	tick() // immediate first refresh

	ticker := time.NewTicker(baselineRefreshCadence)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tick()
		}
	}
}

// baselineLookupAdapter wraps *timescale.Store to satisfy
// orchestrator.BaselineSource — returns the cached MultiBaseline
// for a pair plus its computed_at timestamp. Used by the per-tick
// confidence-score step to read the baseline for z-score lookup.
//
// Returns the bare zero-value triple when the pair has no row;
// the orchestrator's confidence step treats that as bootstrap.
type baselineLookupAdapter struct{ store *timescale.Store }

func (a baselineLookupAdapter) LatestBaseline(ctx context.Context, pair canonical.Pair) (baseline.MultiBaseline, time.Time, error) {
	sb, err := a.store.LatestBaseline(ctx, pair)
	if err != nil {
		return baseline.MultiBaseline{}, time.Time{}, err
	}
	return sb.Multi, sb.ComputedAt, nil
}

// baselineSourceAdapter wraps *timescale.Store to satisfy
// baseline.TimedVWAPSource.
type baselineSourceAdapter struct{ store *timescale.Store }

func (a baselineSourceAdapter) TimedVWAPsForPair1m(ctx context.Context, pair canonical.Pair, from, to time.Time) ([]baseline.TimedVWAP, error) {
	return a.store.TimedVWAPsForPair1m(ctx, pair, from, to)
}

// baselineSinkAdapter wraps *timescale.Store to satisfy
// baseline.Sink. The adapter builds a timescale.StoredBaseline so
// the dep direction stays clean — the storage package doesn't need
// to import the baseline package as a Sink consumer.
type baselineSinkAdapter struct{ store *timescale.Store }

func (a baselineSinkAdapter) UpsertBaseline(
	ctx context.Context,
	pair canonical.Pair,
	computedAt, windowStart, windowEnd time.Time,
	m baseline.MultiBaseline,
) error {
	return a.store.UpsertBaseline(ctx, timescale.StoredBaseline{
		Pair:        pair,
		ComputedAt:  computedAt,
		WindowStart: windowStart,
		WindowEnd:   windowEnd,
		Multi:       m,
	})
}

// defaultPairs is the v1 aggregator coverage set. XLM/BTC/ETH across
// USD/EUR/GBP gives the RFP's major-pair coverage without per-
// operator tuning. Parallel to cmd/ratesengine-indexer's
// defaultAggregatorPairs (kept per-binary so each can evolve
// independently).
func defaultPairs() []canonical.Pair {
	cryptos := []string{"XLM", "BTC", "ETH"}
	fiats := []string{"USD", "EUR", "GBP"}
	var out []canonical.Pair
	for _, c := range cryptos {
		ca, err := canonical.NewCryptoAsset(c)
		if err != nil {
			continue
		}
		for _, f := range fiats {
			fa, err := canonical.NewFiatAsset(f)
			if err != nil {
				continue
			}
			p, err := canonical.NewPair(ca, fa)
			if err != nil {
				continue
			}
			out = append(out, p)
		}
	}
	return out
}

// mkLogger builds the structured logger with the configured format /
// level. Parallel to cmd/ratesengine-indexer.
func mkLogger(cfg config.ObsConfig) *slog.Logger {
	lvl := slog.LevelInfo
	switch cfg.LogLevel {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	}
	opts := &slog.HandlerOptions{Level: lvl}
	var h slog.Handler
	if cfg.LogFormat == "console" {
		h = slog.NewTextHandler(os.Stderr, opts)
	} else {
		h = slog.NewJSONHandler(os.Stderr, opts)
	}
	return slog.New(h)
}

// buildAnomalyChecker constructs an anomaly.Checker from
// AnomalyConfig. Returns (nil, nil) when anomaly.enabled is false
// — the orchestrator treats nil as "feature off" and publishes
// every bucket without evaluation.
//
// Per-class threshold overrides merge over anomaly.DefaultThresholds
// — an operator who only specifies the stablecoin row inherits
// crypto/treasury/governance/default values from the package.
//
// Per-asset classifications are applied via Classifier overrides;
// anything not in the map falls through to ClassDefault.
func buildAnomalyChecker(cfg config.AnomalyConfig) (*anomaly.Checker, error) {
	if !cfg.Enabled {
		return nil, nil
	}

	thresholds := anomaly.DefaultThresholds()
	for className, t := range cfg.Thresholds {
		thresholds[anomaly.AssetClass(className)] = anomaly.Thresholds{
			WarnPct:   t.WarnPct,
			FreezePct: t.FreezePct,
		}
	}

	overrides := make(map[string]anomaly.AssetClass, len(cfg.Classifications))
	for assetID, className := range cfg.Classifications {
		overrides[assetID] = anomaly.AssetClass(className)
	}
	classifier := anomaly.NewClassifier(overrides)

	return anomaly.NewChecker(thresholds, classifier)
}

// buildTriangulations resolves the operator-supplied triangulation
// rows into orchestrator.TriangulationChain values, validating each
// chain's structure (chainable legs, endpoints match target). An
// invalid chain fails-loud at startup rather than silently emitting
// missing-leg metrics in production.
func buildTriangulations(cfg config.AggregateConfig) ([]orchestrator.TriangulationChain, error) {
	resolved, err := cfg.AggregatorTriangulations()
	if err != nil {
		return nil, err
	}
	out := make([]orchestrator.TriangulationChain, 0, len(resolved))
	for i, r := range resolved {
		chain := orchestrator.TriangulationChain{Target: r.Target, Legs: r.Legs}
		if err := orchestrator.ValidateTriangulationChain(chain); err != nil {
			return nil, fmt.Errorf("aggregate.triangulations[%d]: %w", i, err)
		}
		out = append(out, chain)
	}
	return out, nil
}
