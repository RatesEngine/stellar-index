// Binary ratesengine-aggregator computes VWAP over the ingested
// canonical trade stream and writes pre-aggregated results to Redis
// so API requests serve from cache rather than recomputing on every
// query.
//
// Wired today (each driven from one orchestrator.Config field —
// `internal/aggregate/orchestrator/`):
//
//   - Rolling-window VWAP per configured pair, written to Redis
//     keys `vwap:<base>:<quote>:<window-seconds>` on a 30 s cadence
//     (configurable). Class-filtered by default
//     (ClassExchange-only); aggregator + oracle classes excluded
//     to avoid double-counting / methodology mixing.
//   - Triangulation worker (XLM/USD × USD/EUR = XLM/EUR), with
//     the X2.5 forex-snap rule for chained-fiat pairs.
//   - Outlier filter on the raw-trade fetch
//     (`OutlierSigmaThreshold`).
//   - Multi-factor confidence score + ADR-0019 anomaly response
//     (Phase 1 + 2 — z-score + confidence + source-count freeze
//     thresholds; freeze.Writer publishes markers consumed by
//     the API binary's freeze.Looker).
//   - Divergence-cache refresh from the Tick (CoinGecko by
//     default, Chainlink HTTP cross-check via FeedMap), feeding
//     the API's `flags.divergence_warning`.
//   - Periodic supply-snapshot worker (XLM via LCM AccountEntry,
//     classic via trustlines + claimable + LP + SAC observers,
//     SEP-41 via Soroban event observer).
//
// CAGG refresh stays Timescale-driven (background job in
// migration 0002's `add_continuous_aggregate_policy` calls); the
// orchestrator does not manually refresh.
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
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/RatesEngine/rates-engine/internal/aggregate/anomaly"
	"github.com/RatesEngine/rates-engine/internal/aggregate/baseline"
	"github.com/RatesEngine/rates-engine/internal/aggregate/changesummary"
	"github.com/RatesEngine/rates-engine/internal/aggregate/freeze"
	"github.com/RatesEngine/rates-engine/internal/aggregate/orchestrator"
	"github.com/RatesEngine/rates-engine/internal/api/streaming/redispub"
	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/config"
	"github.com/RatesEngine/rates-engine/internal/divergence"
	"github.com/RatesEngine/rates-engine/internal/obs"
	"github.com/RatesEngine/rates-engine/internal/storage/redisclient"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
	"github.com/RatesEngine/rates-engine/internal/supply"
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
	// a cache to write to. redisclient.Build picks Sentinel mode
	// (production, ADR-0024) when redis_sentinel_addrs is set,
	// single-node otherwise. Dry-run pings explicitly so config
	// drift surfaces at startup rather than at first tick.
	rdb := redisclient.Build(cfg.Storage)
	if rdb == nil {
		return errors.New("storage.redis_addr or storage.redis_sentinel_addrs is required — aggregator writes VWAP to Redis")
	}
	defer func() { _ = rdb.Close() }()
	mode := redisclient.Mode(cfg.Storage)
	if dryRun {
		pingCtx, cancelPing := context.WithTimeout(rootCtx, 5*time.Second)
		err := rdb.Ping(pingCtx).Err()
		cancelPing()
		if err != nil {
			return fmt.Errorf("redis: ping (%s mode): %w", mode, err)
		}
	}
	logger.Info("redis configured", "mode", mode)

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
		// Optional durable mirror: every freeze decision lands in the
		// freeze_events hypertable so the showcase /anomalies timeline
		// has queryable history. Idempotent on the currently-firing
		// row, so refreshing the Redis TTL doesn't create duplicates.
		// See migrations/0018_create_freeze_events.up.sql + Phase 2
		// of docs/architecture/showcase-site-implementation-plan.md.
		opts := []freeze.WriterOption{
			freeze.WithEventSink(timescale.NewFreezeEventSink(store)),
		}
		w, err := freeze.NewWriter(rdb, 0, opts...) // 0 → cachekeys.FreezeTTL default
		if err != nil {
			return fmt.Errorf("freeze writer: %w", err)
		}
		freezeWriter = w
		logger.Info("anomaly + freeze: wired",
			"thresholds", len(cfg.Anomaly.Thresholds),
			"event_sink", "timescale")
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

	// ─── Divergence service ────────────────────────────────────
	// Builds CoinGecko + Chainlink reference clients from
	// `[divergence]` config; the orchestrator's Tick refreshes
	// the `div:<asset>` cache once per pair per tick. Empty
	// reference list (CoinGecko disabled + Chainlink not
	// configured) leaves the producer nil so the cache stays
	// empty and the API's `flags.divergence_warning` stays
	// false — pre-Phase behaviour preserved.
	var divRefresher orchestrator.DivergenceRefresher
	divRefs := buildDivergenceReferences(cfg.Divergence, logger)
	if len(divRefs) > 0 {
		// Durable per-reference mirror — every (pair, reference) tick
		// lands in the divergence_observations hypertable so the
		// showcase /divergences page can plot deltas over time and
		// post-mortems can verify against ground truth. See
		// migrations/0019 + Phase 2 of the showcase implementation
		// plan.
		divSvc, err := divergence.NewService(divergence.ServiceOptions{
			Cache:                rdb,
			References:           divRefs,
			Threshold:            cfg.Divergence.Threshold,
			MinSourcesForWarning: cfg.Divergence.MinSourcesForWarning,
			PerReferenceTimeout: time.Duration(
				cfg.Divergence.PerReferenceTimeoutSeconds) * time.Second,
			ObservationSink: timescale.NewDivergenceSink(store),
		})
		if err != nil {
			return fmt.Errorf("divergence service: %w", err)
		}
		divRefresher = divSvc
		names := make([]string, len(divRefs))
		for i, r := range divRefs {
			names[i] = r.Name()
		}
		logger.Info("divergence refresher wired",
			"reference_count", len(divRefs),
			"references", names)
	}

	// ─── Closed-bucket stream publisher ────────────────────────
	// L3.9: fan out each successful (pair, window) VWAP cache write
	// to API-side `/v1/price/stream` subscribers via Redis pub/sub.
	// Always wired here — there's no operator config gate yet
	// because the channel is statically named (DefaultChannel) and
	// PUBLISH on a no-subscriber channel is a Redis no-op. The
	// matching API-side subscriber lives in PR 2 of L3.9.
	streamPub, err := redispub.NewPublisher(rdb, redispub.DefaultChannel)
	if err != nil {
		return fmt.Errorf("redispub.NewPublisher: %w", err)
	}
	logger.Info("closed-bucket stream publisher wired",
		"channel", streamPub.Channel())

	orch := orchestrator.New(store, rdb, orchestrator.Config{
		Pairs:              pairs,
		Windows:            windows, // nil → orchestrator.DefaultWindows
		Interval:           time.Duration(cfg.Aggregate.IntervalSeconds) * time.Second,
		MaxTradesPerWindow: cfg.Aggregate.MaxTradesPerWindow,
		Anomaly:            checker,
		FreezeWriter:       freezeWriter,
		Triangulations:     triangulations,
		FXStore:            store, // X2.5: snap fiat-vs-fiat legs to bucket-end FX quote
		Baselines:          baselineLookupAdapter{store: store},
		Phase2Thresholds: orchestrator.Phase2Thresholds{
			ConfidenceMaxFreeze:  cfg.Anomaly.Phase2.ConfidenceMaxFreeze,
			ZScoreMinFreeze:      cfg.Anomaly.Phase2.ZScoreMinFreeze,
			SourceCountMaxFreeze: cfg.Anomaly.Phase2.SourceCountMaxFreeze,
		},
		DisableClassFilter:        cfg.Aggregate.DisableClassFilter,
		EnableStablecoinFiatProxy: cfg.Aggregate.EnableStablecoinFiatProxy,
		USDPeggedClassicAssets:    parseUSDPeggedClassicAssets(cfg.Trades.USDPeggedClassicAssets, logger),
		OutlierSigmaThreshold:     cfg.Aggregate.OutlierSigmaThreshold,
		MinUSDVolume:              cfg.Aggregate.MinUSDVolume,
		DivergenceRefresher:       divRefresher,
		StreamPublisher:           streamPub,
		// Per-source contribution mirror — feeds the showcase
		// source-donut on every price card. See migrations/0026 +
		// Phase 2 of the showcase implementation plan.
		ContributionSink: newContributionSink(store),
		Logger:           logger,
	})

	if dryRun {
		logger.Info("dry-run complete — exiting")
		return nil
	}

	// ─── Metrics HTTP endpoint ──────────────────────────────────
	// Mirrors the indexer's pattern (cmd/ratesengine-indexer/
	// main.go::startMetricsServer). Aggregator counters
	// (ratesengine_aggregator_ticks_total, _vwap_writes_total,
	// _empty_windows_total, _dropped_trades_total,
	// _triangulations_total) register into internal/obs at package
	// init; without a listener they were unreachable. Now Prometheus
	// can scrape them and the aggregator-silent / outlier-storm /
	// class-drop-spike alerts in deploy/monitoring/rules/aggregator.yml
	// can actually fire.
	metricsSrv := startMetricsServer(cfg.Obs, logger)

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

	// ─── Change-summary rollup worker ───────────────────────────
	// Refreshes the change_summary_5m table every 5 min so every
	// list view + delta strip on the showcase reads in O(1) rather
	// than re-scanning prices_1m per request. See
	// migrations/0022 + Phase 3 of the showcase implementation plan.
	changeSummaryWorker, err := changesummary.New(
		changeSummaryPriceSource{store: store},
		changeSummarySink{store: store},
		buildChangeSummaryEntities(pairs),
		logger.With("component", "change-summary"),
		changesummary.Options{Interval: 5 * time.Minute},
	)
	if err != nil {
		return fmt.Errorf("change-summary worker init: %w", err)
	}
	refresherWG.Add(1)
	go func() {
		defer refresherWG.Done()
		_ = changeSummaryWorker.Run(rootCtx)
	}()

	// ─── Supply-snapshot refresh worker (ADR-0011 / Task #57) ────
	// Operator-opted-in via cfg.Supply.AggregatorRefreshEnabled.
	// When false (the default), the systemd-timer-driven path
	// (deploy/systemd/supply-snapshot.timer) remains the
	// operator's mechanism. When true, the goroutine path takes
	// over and the systemd timer should be disabled.
	if cfg.Supply.AggregatorRefreshEnabled {
		bindings, err := buildSupplyRefreshers(cfg, store, logger.With("component", "supply-refresh"))
		if err != nil {
			return fmt.Errorf("supply refresher init: %w", err)
		}
		for _, b := range bindings {
			refresherWG.Add(1)
			go func(binding supplyRefresherBinding) {
				defer refresherWG.Done()
				runSupplyRefresh(rootCtx, binding.refresher, cfg.Supply.AggregatorRefreshCadence, binding.assetKey)
			}(b)
		}

		// Cross-check refresher reads the snapshots the per-asset
		// refreshers above produce and emits the
		// supply_cross_check_divergence_stroops gauge so the
		// supply.yml alert can fire on real divergence per
		// ADR-0011. No-op when the ∩ of [supply].sac_wrappers and
		// the watched-sets is empty (operator hasn't declared any
		// classic ↔ SAC pairs).
		ccRefresher, err := buildCrossCheckRefresher(cfg, store, logger.With("component", "supply-cross-check"))
		if err != nil {
			return fmt.Errorf("supply cross-check refresher init: %w", err)
		}
		if ccRefresher != nil {
			refresherWG.Add(1)
			go func() {
				defer refresherWG.Done()
				runCrossCheckRefresh(rootCtx, ccRefresher, cfg.Supply.AggregatorRefreshCadence)
			}()
		}
	}

	// ─── Run ─────────────────────────────────────────────────────
	logger.Info("orchestrator starting")
	if err := orch.Run(rootCtx); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("orchestrator: %w", err)
	}
	logger.Info("orchestrator stopped", "stats", orch.Stats())

	if metricsSrv != nil {
		shutdownCtx, stopShutdown := context.WithTimeout(context.Background(), 10*time.Second)
		if err := metricsSrv.Shutdown(shutdownCtx); err != nil {
			logger.Warn("metrics server shutdown", "err", err)
		}
		stopShutdown()
	}

	// Wait for the baseline + supply goroutines to honour rootCtx
	// cancellation.
	refresherWG.Wait()
	return nil
}

// supplyRefresherBinding pairs a [supply.Refresher] with the
// asset_key that labels its outcome metrics. Per-asset binding
// lets `ratesengine_aggregator_supply_refresh_total{asset_key,outcome}`
// surface which watched asset is failing without operators
// having to grep logs.
type supplyRefresherBinding struct {
	refresher *supply.Refresher
	assetKey  string
}

// buildSupplyRefreshers composes one [supply.Refresher] per
// watched asset across all three algorithms:
//
//   - One XLMComputer-backed refresher (Algorithm 1, native XLM).
//   - One ClassicComputer-backed refresher per entry in
//     [supply] watched_classic_assets (Algorithm 2, classic
//     credits), each bound via [supply.NewAssetBoundClassicComputer].
//   - One SEP41Computer-backed refresher per entry in
//     [supply] watched_sep41_contracts (Algorithm 3, SEP-41
//     Soroban tokens), each bound via
//     [supply.NewAssetBoundSEP41Computer].
//
// Returns an error on operator-config inconsistencies (per
// [config.SupplyConfig.Validate] + per-asset parse errors).
func buildSupplyRefreshers(cfg config.Config, store *timescale.Store, logger *slog.Logger) ([]supplyRefresherBinding, error) {
	out := make([]supplyRefresherBinding, 0, 1+len(cfg.Supply.WatchedClassicAssets)+len(cfg.Supply.WatchedSEP41Contracts))

	xlmRefresher, err := buildXLMRefresher(cfg, store, logger)
	if err != nil {
		return nil, fmt.Errorf("xlm refresher: %w", err)
	}
	out = append(out, supplyRefresherBinding{refresher: xlmRefresher, assetKey: "XLM"})

	classicBindings, err := buildClassicRefreshers(cfg, store, logger)
	if err != nil {
		return nil, err
	}
	out = append(out, classicBindings...)

	sep41Bindings, err := buildSEP41Refreshers(cfg, store, logger)
	if err != nil {
		return nil, err
	}
	out = append(out, sep41Bindings...)

	return out, nil
}

func buildClassicRefreshers(cfg config.Config, store *timescale.Store, logger *slog.Logger) ([]supplyRefresherBinding, error) {
	if len(cfg.Supply.WatchedClassicAssets) == 0 {
		return nil, nil
	}
	classicReader := supply.NewStorageClassicSupplyReader(store)
	classicComputer, err := supply.NewClassicComputer(supply.Policy{}, classicReader)
	if err != nil {
		return nil, fmt.Errorf("classic computer: %w", err)
	}
	out := make([]supplyRefresherBinding, 0, len(cfg.Supply.WatchedClassicAssets))
	for _, raw := range cfg.Supply.WatchedClassicAssets {
		asset, err := canonical.ParseAsset(raw)
		if err != nil {
			return nil, fmt.Errorf("parse watched classic asset %q: %w", raw, err)
		}
		bound, err := supply.NewAssetBoundClassicComputer(classicComputer, asset)
		if err != nil {
			return nil, fmt.Errorf("bind classic computer to %q: %w", raw, err)
		}
		assetKey, err := supply.AssetKey(asset)
		if err != nil {
			return nil, fmt.Errorf("derive asset_key for %q: %w", raw, err)
		}
		out = append(out, supplyRefresherBinding{
			refresher: supply.NewRefresher(
				supplyAggregatorLedgers{s: store},
				bound,
				supplyAggregatorInserter{s: store},
				logger.With("asset", raw),
			),
			assetKey: assetKey,
		})
	}
	return out, nil
}

func buildSEP41Refreshers(cfg config.Config, store *timescale.Store, logger *slog.Logger) ([]supplyRefresherBinding, error) {
	if len(cfg.Supply.WatchedSEP41Contracts) == 0 {
		return nil, nil
	}
	sep41Reader := supply.NewStorageSEP41SupplyReader(supplyAggregatorSEP41Store{s: store})
	sep41Computer, err := supply.NewSEP41Computer(supply.Policy{}, sep41Reader)
	if err != nil {
		return nil, fmt.Errorf("sep41 computer: %w", err)
	}
	out := make([]supplyRefresherBinding, 0, len(cfg.Supply.WatchedSEP41Contracts))
	for _, contractID := range cfg.Supply.WatchedSEP41Contracts {
		asset, err := canonical.NewSorobanAsset(contractID)
		if err != nil {
			return nil, fmt.Errorf("watched sep41 contract %q: %w", contractID, err)
		}
		bound, err := supply.NewAssetBoundSEP41Computer(sep41Computer, asset)
		if err != nil {
			return nil, fmt.Errorf("bind sep41 computer to %q: %w", contractID, err)
		}
		out = append(out, supplyRefresherBinding{
			refresher: supply.NewRefresher(
				supplyAggregatorLedgers{s: store},
				bound,
				supplyAggregatorInserter{s: store},
				logger.With("asset", contractID),
			),
			assetKey: contractID, // supply.AssetKey form for SEP-41 is the bare contract id
		})
	}
	return out, nil
}

func buildXLMRefresher(cfg config.Config, store *timescale.Store, logger *slog.Logger) (*supply.Refresher, error) {
	staticReader, err := supply.NewConfigReserveBalanceReader(cfg.Supply.ReserveBalancesStroops)
	if err != nil {
		return nil, fmt.Errorf("config reserve reader: %w", err)
	}
	chained := supplyAggregatorChainReader{
		live:   supply.NewLCMReserveBalanceReader(supplyAggregatorStoreLookup{s: store}),
		static: staticReader,
	}
	computer, err := supply.NewXLMComputer(cfg.Supply.SDFReserveAccounts, chained)
	if err != nil {
		return nil, fmt.Errorf("xlm computer: %w", err)
	}
	return supply.NewRefresher(
		supplyAggregatorLedgers{s: store},
		computer,
		supplyAggregatorInserter{s: store},
		logger.With("asset", "native"),
	), nil
}

// runSupplyRefresh ticks the supply refresher on `cadence`,
// emitting per-(asset_key, outcome) Prometheus counters for each
// cycle. Returns on ctx cancellation.
//
// Initial cycle runs immediately on startup so a fresh deployment
// gets at least one snapshot in `asset_supply_history` before the
// first cadence interval elapses.
//
// Per-tick logging happens inside [supply.Refresher.Tick]; the
// goroutine just drives the loop and emits the outcome metric
// labeled with the bound asset_key so operators can chart
// per-asset bootstrap progress + isolate failure modes per asset.
func runSupplyRefresh(ctx context.Context, r *supply.Refresher, cadence time.Duration, assetKey string) {
	tick := func() {
		out := r.Tick(ctx)
		obs.AggregatorSupplyRefreshTotal.WithLabelValues(assetKey, string(out.Kind)).Inc()
	}

	tick() // immediate first refresh

	ticker := time.NewTicker(cadence)
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

// supplyAggregatorLedgers adapts *timescale.Store to
// supply.LedgerLookup. Resolves the latest known chain ledger as
// the max last_ledger across every ingestion cursor — same shape
// as cmd/ratesengine-ops/supply.go::resolveSnapshotLedger but
// inlined here so the aggregator path stays self-contained.
type supplyAggregatorLedgers struct{ s *timescale.Store }

func (a supplyAggregatorLedgers) LatestKnownLedger(ctx context.Context) (uint32, time.Time, error) {
	cursors, err := a.s.ListCursors(ctx)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("ListCursors: %w", err)
	}
	var maxLedger uint32
	for _, c := range cursors {
		if c.LastLedger > maxLedger {
			maxLedger = c.LastLedger
		}
	}
	if maxLedger == 0 {
		return 0, time.Time{}, errors.New("no ingestion cursors yet — refresher will retry next tick")
	}
	return maxLedger, time.Now().UTC(), nil
}

// supplyAggregatorStoreLookup adapts *timescale.Store to
// supply.AccountObservationLookup. Mirrors
// cmd/ratesengine-ops/supply.go::supplyStoreLookup.
type supplyAggregatorStoreLookup struct{ s *timescale.Store }

func (a supplyAggregatorStoreLookup) LatestAccountObservationAtOrBefore(ctx context.Context, accountID string, asOfLedger uint32) (supply.AccountObservationRow, error) {
	row, err := a.s.LatestAccountObservationAtOrBefore(ctx, accountID, asOfLedger)
	if err != nil {
		return supply.AccountObservationRow{}, err
	}
	return supply.AccountObservationRow{
		Balance:   row.Balance,
		IsRemoval: row.IsRemoval,
		Ledger:    row.Ledger,
	}, nil
}

// supplyAggregatorChainReader is the same chained-fallback reader
// pattern from cmd/ratesengine-ops/supply.go::supplyChainReader.
// Inlined here because the aggregator is its own binary and we
// don't want to lift the helper into a shared package — the
// indirection cost outweighs the duplication for a 20-line struct.
type supplyAggregatorChainReader struct {
	live   supply.ReserveBalanceReader
	static supply.ReserveBalanceReader
}

func (c supplyAggregatorChainReader) ReserveBalanceTotal(ctx context.Context, accounts []string, ledger uint32) (*big.Int, error) {
	out, err := c.live.ReserveBalanceTotal(ctx, accounts, ledger)
	if err == nil {
		return out, nil
	}
	if errors.Is(err, supply.ErrNoObservation) {
		return c.static.ReserveBalanceTotal(ctx, accounts, ledger)
	}
	return nil, err
}

// supplyAggregatorInserter adapts *timescale.Store to
// supply.SnapshotInserter.
type supplyAggregatorInserter struct{ s *timescale.Store }

func (a supplyAggregatorInserter) InsertSupply(ctx context.Context, snap supply.Supply) error {
	return a.s.InsertSupply(ctx, snap)
}

// supplyAggregatorSEP41Store adapts *timescale.Store to
// supply.SEP41SupplyStore by projecting the timescale
// SEP41KindTotals row into the supply-package's identical-shape
// type. Required because the supply package defines its own
// type (avoiding a cyclic import — timescale already imports
// supply for InsertSupply).
type supplyAggregatorSEP41Store struct{ s *timescale.Store }

func (a supplyAggregatorSEP41Store) SEP41KindTotalsAtOrBefore(ctx context.Context, contractID string, asOfLedger uint32) (supply.SEP41KindTotals, error) {
	t, err := a.s.SEP41KindTotalsAtOrBefore(ctx, contractID, asOfLedger)
	if err != nil {
		return supply.SEP41KindTotals{}, err
	}
	return supply.SEP41KindTotals{
		Mint:     t.Mint,
		Burn:     t.Burn,
		Clawback: t.Clawback,
	}, nil
}

func (a supplyAggregatorSEP41Store) SACBalanceForContractAtOrBefore(ctx context.Context, holder, assetKey string, asOfLedger uint32) (*big.Int, error) {
	return a.s.SACBalanceForContractAtOrBefore(ctx, holder, assetKey, asOfLedger)
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
// parseUSDPeggedClassicAssets resolves the operator-declared
// `[trades].usd_pegged_classic_assets` strings (e.g.
// `"USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"`)
// into canonical assets so the orchestrator's stablecoin-fiat-proxy
// expansion can also pull XLM/USDC-GA5Z…-style classic-quoted trades
// when the target is `XLM/fiat:USD`.
//
// Soft-fails: a single malformed entry is logged and skipped rather
// than aborting startup. Validate() at config load already
// roundtrips each entry through canonical.NewClassicAsset; reaching
// a parse error here would mean the validator regressed, in which
// case the safe behaviour is "skip and keep serving" — a missing
// classic peg is a smaller failure than the binary refusing to
// start.
func parseUSDPeggedClassicAssets(raws []string, logger *slog.Logger) []canonical.Asset {
	if len(raws) == 0 {
		return nil
	}
	out := make([]canonical.Asset, 0, len(raws))
	for _, raw := range raws {
		asset, err := canonical.ParseAsset(raw)
		if err != nil {
			logger.Warn("usd_pegged_classic_assets: skipping malformed entry",
				"raw", raw, "err", err)
			continue
		}
		if asset.Type != canonical.AssetClassic {
			logger.Warn("usd_pegged_classic_assets: ignoring non-classic asset",
				"raw", raw, "type", asset.Type)
			continue
		}
		out = append(out, asset)
	}
	return out
}

func defaultPairs() []canonical.Pair {
	cryptos := []string{"XLM", "BTC", "ETH"}
	fiats := []string{"USD", "EUR", "GBP"}
	var out []canonical.Pair
	for _, c := range cryptos {
		bases := make([]canonical.Asset, 0, 2)
		ca, err := canonical.NewCryptoAsset(c)
		if err == nil {
			bases = append(bases, ca)
		}
		// XLM has two on-the-wire identities: the abstract `crypto:XLM`
		// ticker (off-chain CEX/FX trades report this form) and the
		// Stellar-protocol `native` form (every on-chain DEX / SDEX
		// trade is stored as native/<quote>). The aggregator publishes
		// one VWAP per (base, quote) cache key; the API resolves the
		// caller's asset literally, so a customer querying
		// `?asset=native` won't see a `crypto:XLM` VWAP and vice versa.
		// Including both ensures whichever form the customer uses lands
		// on a populated cache key as long as ANY source publishes
		// trades under that form. On r1 today only the native side has
		// data (no CEX connectors enabled); a future deployment with
		// Binance/Coinbase running will populate the abstract side too.
		if c == "XLM" {
			bases = append(bases, canonical.NativeAsset())
		}
		for _, base := range bases {
			for _, f := range fiats {
				fa, err := canonical.NewFiatAsset(f)
				if err != nil {
					continue
				}
				p, err := canonical.NewPair(base, fa)
				if err != nil {
					continue
				}
				out = append(out, p)
			}
		}
	}
	return out
}

// mkLogger builds the structured logger with the configured format /
// level. Parallel to cmd/ratesengine-indexer.
// startMetricsServer mounts a /metrics + /healthz listener at
// cfg.MetricsListen and returns the *http.Server so the caller can
// orchestrate graceful shutdown. Empty MetricsListen disables the
// listener and logs a warning — aggregator alert rules in
// deploy/monitoring/rules/aggregator.yml expect these counters to
// be scrapable, so a quiet aggregator with no listener is a
// configuration mistake worth flagging.
//
// Single-host coexistence: the indexer also reads obs.metrics_listen
// (default "127.0.0.1:9464"). If the operator hasn't overridden this
// for the aggregator, we shift to ":9465" automatically so a default
// single-host deploy doesn't have one binary silently lose its
// metrics listener to "address already in use." Operators on
// multi-host deploys override obs.metrics_listen per-host and never
// hit the shift.
func startMetricsServer(cfg config.ObsConfig, logger *slog.Logger) *http.Server {
	if cfg.MetricsListen == "" {
		logger.Warn("obs.metrics_listen is empty — /metrics endpoint disabled; aggregator-silent / outlier-storm / class-drop-spike alerts will not fire")
		return nil
	}
	addr := cfg.MetricsListen
	if addr == aggregatorMetricsCollidingDefault {
		addr = aggregatorMetricsShiftedAddr
		logger.Info("obs.metrics_listen left at indexer default; shifting aggregator to "+aggregatorMetricsShiftedAddr+" to avoid single-host port collision",
			"original", aggregatorMetricsCollidingDefault, "shifted_to", aggregatorMetricsShiftedAddr)
	}
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", obs.Handler())
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		logger.Info("metrics endpoint listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("metrics server exited", "err", err)
		}
	}()
	return srv
}

// aggregatorMetricsCollidingDefault is the indexer's default
// metrics_listen address; the aggregator auto-shifts when it sees
// this exact value so single-host deploys don't collide silently.
const (
	aggregatorMetricsCollidingDefault = "127.0.0.1:9464"
	aggregatorMetricsShiftedAddr      = "127.0.0.1:9465"
)

func mkLogger(cfg config.ObsConfig) *slog.Logger {
	return obs.NewLogger(cfg, "ratesengine-aggregator")
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
//
// The aggregate.triangulation_enabled master switch is honoured here:
// when false, return a nil slice so the orchestrator's
// `len(cfg.Triangulations) == 0` short-circuit skips the triangulation
// tick entirely regardless of how many rows the operator left in the
// aggregate.triangulations table. Validation still runs first (so a
// malformed row is caught even when the switch is off — operators
// who fix the typo and flip the switch on shouldn't get a delayed
// surprise).
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
	if !cfg.TriangulationEnabled {
		return nil, nil
	}
	return out, nil
}

// buildCrossCheckRefresher composes the periodic supply-cross-check
// emitter from the operator's `[supply]` config:
//
//   - For every entry in `sac_wrappers` whose ClassicKey appears in
//     the watched-classic set AND whose ContractID appears in the
//     watched-SEP-41 set, derive one [supply.CrossCheckPair].
//   - Wire the refresher to read snapshots via timescale's
//     LatestSupply and emit gauges/counters via obs.
//
// Returns (nil, nil) when no pair survives the watched-set
// intersection — silently no-op so operators that haven't yet
// configured both sides of a wrapper don't see a startup error.
func buildCrossCheckRefresher(cfg config.Config, store *timescale.Store, logger *slog.Logger) (*supply.CrossCheckRefresher, error) {
	if len(cfg.Supply.SACWrappers) == 0 {
		return nil, nil
	}

	watchedClassic := make(map[string]struct{}, len(cfg.Supply.WatchedClassicAssets))
	for _, raw := range cfg.Supply.WatchedClassicAssets {
		asset, err := canonical.ParseAsset(raw)
		if err != nil {
			return nil, fmt.Errorf("parse watched classic asset %q: %w", raw, err)
		}
		k, err := supply.AssetKey(asset)
		if err != nil {
			return nil, fmt.Errorf("derive AssetKey for %q: %w", raw, err)
		}
		watchedClassic[k] = struct{}{}
	}
	watchedSEP41 := make(map[string]struct{}, len(cfg.Supply.WatchedSEP41Contracts))
	for _, c := range cfg.Supply.WatchedSEP41Contracts {
		watchedSEP41[c] = struct{}{}
	}

	pairs := make([]supply.CrossCheckPair, 0, len(cfg.Supply.SACWrappers))
	for sacID, classicKey := range cfg.Supply.SACWrappers {
		if _, ok := watchedClassic[classicKey]; !ok {
			continue
		}
		if _, ok := watchedSEP41[sacID]; !ok {
			continue
		}
		pairs = append(pairs, supply.CrossCheckPair{ClassicKey: classicKey, SACKey: sacID})
	}
	if len(pairs) == 0 {
		return nil, nil
	}

	logger.Info("cross-check pairs registered", "count", len(pairs))
	return supply.NewCrossCheckRefresher(
		pairs,
		supplyAggregatorSnapshotReader{s: store},
		obsCrossCheckEmitter{},
		logger,
	)
}

// runCrossCheckRefresh ticks the cross-check refresher on `cadence`,
// returning on ctx cancellation. Initial cycle runs immediately so a
// fresh deployment surfaces the per-pair `outcome=missing_snapshot`
// metric on first scrape (operators see "the cross-checker is
// running but has nothing to compare yet" rather than silence).
func runCrossCheckRefresh(ctx context.Context, r *supply.CrossCheckRefresher, cadence time.Duration) {
	tick := func() { _ = r.Tick(ctx) }
	tick()

	ticker := time.NewTicker(cadence)
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

// supplyAggregatorSnapshotReader adapts *timescale.Store to
// supply.SnapshotReader. Maps timescale's ErrNotFound onto
// supply.ErrNoSnapshot so the refresher distinguishes the bootstrap
// state (no rows yet) from transient read failures.
type supplyAggregatorSnapshotReader struct{ s *timescale.Store }

func (a supplyAggregatorSnapshotReader) LatestSupply(ctx context.Context, assetKey string) (supply.Supply, error) {
	snap, err := a.s.LatestSupply(ctx, assetKey)
	if errors.Is(err, timescale.ErrNotFound) {
		return supply.Supply{}, fmt.Errorf("LatestSupply %s: %w", assetKey, supply.ErrNoSnapshot)
	}
	return snap, err
}

// obsCrossCheckEmitter wires supply.CrossCheckEmitter onto the
// package-level obs Prometheus collectors. Kept as a tiny adapter so
// the supply package stays Prometheus-agnostic and unit-testable.
type obsCrossCheckEmitter struct{}

func (obsCrossCheckEmitter) Divergence(classicKey string, stroops float64) {
	obs.SupplyCrossCheckDivergenceStroops.WithLabelValues(classicKey).Set(stroops)
}

func (obsCrossCheckEmitter) Outcome(kind supply.CrossCheckOutcomeKind) {
	obs.SupplyCrossCheckTotal.WithLabelValues(string(kind)).Inc()
}

// buildDivergenceReferences mirrors the API binary's helper of the
// same name. Builds the CoinGecko + Chainlink reference clients the
// `divergence.Service` runs on each tick. Kept in lockstep with
// `cmd/ratesengine-api/main.go::buildDivergenceReferences` —
// drift here would mean the aggregator and API see different
// divergence semantics for the same pair.
func buildDivergenceReferences(cfg config.DivergenceConfig, logger *slog.Logger) []divergence.Reference {
	var refs []divergence.Reference

	if cfg.CoinGecko.Enabled {
		refs = append(refs, divergence.NewCoinGeckoReference(divergence.CoinGeckoOptions{
			BaseURL: cfg.CoinGecko.BaseURL,
			IDMap:   cfg.CoinGecko.IDMap,
		}))
	}

	if cfg.Chainlink.Enabled {
		if len(cfg.Chainlink.FeedMap) == 0 {
			logger.Warn("divergence: chainlink enabled but FeedMap is empty — skipping")
		} else {
			feedMap := make(map[string]divergence.ChainlinkFeed, len(cfg.Chainlink.FeedMap))
			for pair, f := range cfg.Chainlink.FeedMap {
				feedMap[pair] = divergence.ChainlinkFeed{
					Address:  f.Address,
					Decimals: f.Decimals,
					Invert:   f.Invert,
				}
			}
			refs = append(refs, divergence.NewChainlinkReference(divergence.ChainlinkOptions{
				RPCURL:  cfg.Chainlink.RPCURL,
				FeedMap: feedMap,
			}))
		}
	}

	return refs
}
