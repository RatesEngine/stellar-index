// Binary ratesengine-indexer runs the production ingestion
// pipeline:
//
//	Galexie MinIO → internal/ledgerstream → internal/dispatcher
//	              → per-source decoders → canonical.Trade /
//	                canonical.OracleUpdate → TimescaleDB
//
// Per docs/architecture/ingest-pipeline.md this is the SINGLE
// production code path. No stellar-rpc client, no per-source
// goroutines, no poll loops. One goroutine drives ledgerstream +
// dispatcher; a second drains the resulting consumer.Events to
// Timescale with panic isolation.
//
// Flags:
//
//	-config PATH    TOML config file (required)
//	-dry-run        Load config, open connections, validate, exit.
//	                No ledgers consumed. Boot sanity only.
//
// Graceful shutdown: SIGINT + SIGTERM cancel the root context;
// the binary waits up to 30 s for in-flight work to finish before
// hard-exiting.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	sdkxdr "github.com/stellar/go-stellar-sdk/xdr"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/canonical/discovery"
	"github.com/RatesEngine/rates-engine/internal/config"
	"github.com/RatesEngine/rates-engine/internal/consumer"
	"github.com/RatesEngine/rates-engine/internal/currency"
	"github.com/RatesEngine/rates-engine/internal/dispatcher"
	"github.com/RatesEngine/rates-engine/internal/dispatcher/statsflush"
	"github.com/RatesEngine/rates-engine/internal/ledgerstream"
	"github.com/RatesEngine/rates-engine/internal/obs"
	"github.com/RatesEngine/rates-engine/internal/pipeline"
	"github.com/RatesEngine/rates-engine/internal/projector"
	"github.com/RatesEngine/rates-engine/internal/sources/external"
	externalbinance "github.com/RatesEngine/rates-engine/internal/sources/external/binance"
	externalbitstamp "github.com/RatesEngine/rates-engine/internal/sources/external/bitstamp"
	externalchainlink "github.com/RatesEngine/rates-engine/internal/sources/external/chainlink"
	externalcoinbase "github.com/RatesEngine/rates-engine/internal/sources/external/coinbase"
	externalcoingecko "github.com/RatesEngine/rates-engine/internal/sources/external/coingecko"
	externalcoinmarketcap "github.com/RatesEngine/rates-engine/internal/sources/external/coinmarketcap"
	externalcryptocompare "github.com/RatesEngine/rates-engine/internal/sources/external/cryptocompare"
	externalecb "github.com/RatesEngine/rates-engine/internal/sources/external/ecb"
	externalexchangerates "github.com/RatesEngine/rates-engine/internal/sources/external/exchangeratesapi"
	externalkraken "github.com/RatesEngine/rates-engine/internal/sources/external/kraken"
	externalpolygonforex "github.com/RatesEngine/rates-engine/internal/sources/external/polygonforex"
	"github.com/RatesEngine/rates-engine/internal/sources/sorobanevents"
	"github.com/RatesEngine/rates-engine/internal/storage/clickhouse"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
	"github.com/RatesEngine/rates-engine/internal/version"
)

// cursorSource is the single `source` label stored in the
// ingestion_cursors table for the ledgerstream pipeline. There's
// exactly one cursor now — the whole pipeline tracks one
// last-processed ledger. (Per-source cursors were part of the
// pre-165 orchestrator topology.)
const cursorSource = "ledgerstream"

// main is a thin shim over realMain so deferred functions (notably
// the SilenceSDKChecksumWarnings flush) execute on every exit path.
// os.Exit skips defers — see SilenceSDKChecksumWarnings docstring
// for the regression that drove this shape.
func main() {
	os.Exit(realMain())
}

func realMain() int {
	// Wrap fd 2 with a line-filter BEFORE any aws-sdk-go-v2 code
	// captures os.Stderr (config.LoadDefaultConfig binds the
	// default logger at that point). Drops the per-S3-GET
	// "Response has no supported checksum" WARN that floods
	// journald when MinIO is the backend — every GetObjectInput
	// in go-stellar-sdk's datastore/s3.go hardcodes ChecksumMode:
	// Enabled, so the previous env-var approach was a no-op for
	// our use. Fail-soft: any pipe/dup2 error logs to the original
	// stderr and startup continues with unfiltered output.
	//
	// The flush MUST run before the process exits or short-lived
	// runs (e.g. -version, -dry-run failure path) lose buffered
	// output — see the rc.77 regression documented in
	// SilenceSDKChecksumWarnings.
	flush := pipeline.SilenceSDKChecksumWarnings()
	defer flush()

	var (
		cfgPath     = flag.String("config", "", "Path to TOML config file (required)")
		dryRun      = flag.Bool("dry-run", false, "Load config + open connections + exit without ingesting")
		showVersion = flag.Bool("version", false, "Print version and exit")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println(version.String())
		return 0
	}

	if *cfgPath == "" {
		fmt.Fprintln(os.Stderr, "ratesengine-indexer: -config is required")
		flag.Usage()
		return 2
	}

	if err := run(*cfgPath, *dryRun); err != nil {
		fmt.Fprintf(os.Stderr, "ratesengine-indexer: %v\n", err)
		return 1
	}
	return 0
}

//nolint:funlen,gocognit,gocyclo // top-level binary lifecycle; splitting reduces readability of dependency-construction order
func run(cfgPath string, dryRun bool) error {
	cfg, err := config.LoadWithEnv(cfgPath)
	if err != nil {
		return err
	}

	logger := mkLogger(cfg.Obs)
	logger.Info("starting",
		"version", version.String(),
		"region", cfg.Region.ID,
		"network", cfg.Stellar.Network,
		"sources", cfg.Ingestion.EnabledSources,
		"dry_run", dryRun,
	)

	rootCtx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// ─── Storage ────────────────────────────────────────────────
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

	// Resilience-ping goroutine. Probes the *sql.DB pool every
	// 60 s and emits `ratesengine_postgres_ping_total` +
	// `ratesengine_postgres_ping_failure_streak`. This is the
	// observability signal for F-0151 (2026-05-26 cascade left
	// dead conns in the pool for ~14 h after postgres@15-main
	// recovered); the actual reconnect path is the pool's
	// `PoolConnMaxLifetime` safety-net, which forces a re-dial
	// every 30 min regardless of liveness. The two together
	// cap a cascade-gap at the lifetime interval AND surface it
	// to alerting in minutes.
	postgresPingStop, postgresPingDone := watchPostgresPing(rootCtx, store, logger.With("component", "postgres-ping"))
	defer func() {
		postgresPingStop()
		<-postgresPingDone
	}()

	// USD-volume quote spec — wires on-chain DEX trades into
	// usd_volume population per launch-readiness L2.2 phase 1.
	// Operator declares which classic credits they trust as
	// USD-pegged in `[trades].usd_pegged_classic_assets`; SAC
	// wrappers come transitively via `[supply.sac_wrappers]`. Empty
	// list → spec stays nil → off-chain-only behaviour preserved.
	if len(cfg.Trades.USDPeggedClassicAssets) > 0 {
		spec, err := timescale.NewUSDVolumeQuoteSpec(
			cfg.Trades.USDPeggedClassicAssets,
			cfg.Supply.SACWrappers,
		)
		if err != nil {
			return fmt.Errorf("usd-volume quote spec: %w", err)
		}
		store.SetUSDVolumeQuoteSpec(spec)
		logger.Info("on-chain usd_volume enabled",
			"classic_pegs", len(cfg.Trades.USDPeggedClassicAssets),
			"sac_wrappers", len(cfg.Supply.SACWrappers),
		)

		// L2.2 Phase 2 / F-1268: FX-anchor multiplication. The
		// quote spec above covers any USD-pegged classic the
		// operator declared; this resolver covers everything else
		// by looking up `<quote>/<peg>` in prices_1m at the trade's
		// timestamp. Together they take non-NULL `usd_volume`
		// coverage from "USD-pegged stablecoins only" → "any quote
		// that's traded against a USD-pegged stablecoin in the
		// observation window". Cached per-asset-per-minute to keep
		// the trade-insert hot path affordable.
		fxResolver, err := timescale.NewVWAPUSDFXResolver(store, timescale.VWAPUSDFXResolverOptions{
			USDPegs: cfg.Trades.USDPeggedClassicAssets,
		})
		if err != nil {
			return fmt.Errorf("usd-volume fx resolver: %w", err)
		}
		store.SetUSDVolumeFXResolver(fxResolver)
		logger.Info("on-chain usd_volume Phase 2 (FX-anchor) enabled",
			"pegs_for_resolver", len(cfg.Trades.USDPeggedClassicAssets),
		)
	}

	// ─── Dispatcher + decoders ─────────────────────────────────
	// Soroswap pair registry: load from postgres so the decoder
	// boots with every previously-seen pair, and arm a live-upsert
	// hook so factory new_pair events persist as they stream. Empty
	// registry on a fresh deployment is fine — operators run
	// `ratesengine-ops seed-soroswap-pairs` once to bootstrap.
	soroswapOpts, err := pipeline.SoroswapPersistenceOptions(rootCtx, store, logger, rootCtx)
	if err != nil {
		return fmt.Errorf("soroswap registry: %w", err)
	}
	disp, err := pipeline.BuildDispatcher(cfg.Ingestion.EnabledSources, cfg.Oracle, soroswapOpts...)
	if err != nil {
		return fmt.Errorf("build dispatcher: %w", err)
	}

	// ─── Supply observers (opt-in via [supply] watched-sets) ──────
	// L2.12a wire-up complete: accounts (Algorithm 1, XLM),
	// trustlines / claimable / liquidity_pools / sac_balances
	// (Algorithm 2 LCM-based components), and sep41_supply
	// (Algorithm 3 event-stream). Empty watched-set per observer
	// leaves it unregistered → no behaviour change for deployments
	// that haven't opted in.
	supplyObservers, err := pipeline.RegisterSupplyEntryDecoders(disp, cfg.Supply)
	if err != nil {
		return fmt.Errorf("supply observers (entry): %w", err)
	}
	supplyEvents, err := pipeline.RegisterSupplyEventDecoders(disp, cfg.Supply)
	if err != nil {
		return fmt.Errorf("supply observers (event): %w", err)
	}
	supplyObservers = append(supplyObservers, supplyEvents...)
	if len(supplyObservers) > 0 {
		logger.Info("supply observers wired",
			"observers", supplyObservers,
			"sdf_reserve_accounts", len(cfg.Supply.SDFReserveAccounts),
			"watched_classic_assets", len(cfg.Supply.WatchedClassicAssets),
			"sac_wrappers", len(cfg.Supply.SACWrappers),
			"watched_sep41_contracts", len(cfg.Supply.WatchedSEP41Contracts))
	} else {
		// Silent supply-pipeline absence is the bug-class behind r1's
		// asset_supply_history sitting empty for 6+ days post-deploy
		// (ops task #95 / #97). When every [supply] watched-set is
		// empty no observer registers, F2 fields on /v1/assets/{id}
		// stay null forever, and the only signal is "the table has
		// zero rows when someone finally checks." Surface it loudly
		// at boot so an operator who forgot to populate the watched-
		// sets sees it the next time they tail the indexer log.
		logger.Warn("supply pipeline is OFF — no [supply] watched-sets configured",
			"hint", "set sdf_reserve_accounts (Algorithm 1 XLM), watched_classic_assets (Algorithm 2), and/or watched_sep41_contracts (Algorithm 3) in your TOML to enable; see ADR-0011/0021/0022/0023",
			"effect", "asset_supply_history will not populate; F2 fields (market_cap_usd, fdv_usd, circulating_supply, total_supply, max_supply) on /v1/assets/{id} will be null for every asset")
	}

	// ─── Decoder-stats periodic flush ────────────────────────────
	// Snapshots dispatcher.Stats() every 5 min and writes per-source
	// deltas to the decoder_stats_5m hypertable. Powers
	// /v1/diagnostics/decoders + the explorer /diagnostics page.
	// See migrations/0020 + Phase 2 of the explorer implementation
	// plan.
	decoderStatsFlusher := statsflush.New(disp, store,
		logger.With("component", "decoder-stats-flush"),
		statsflush.Options{Interval: 5 * time.Minute})
	decoderStatsCtx, decoderStatsCancel := context.WithCancel(rootCtx)
	decoderStatsDone := make(chan struct{})
	go func() {
		defer close(decoderStatsDone)
		_ = decoderStatsFlusher.Run(decoderStatsCtx)
	}()
	defer func() {
		decoderStatsCancel()
		<-decoderStatsDone
	}()

	// ─── SEP-41 auto-discovery sink ──────────────────────────────
	// Buffers Hits to a channel; a worker goroutine drains them to
	// timescale.Store.RecordDiscovered. The dispatcher's Push call
	// is non-blocking — repeated (contract_id, event_type) pairs are
	// deduplicated in-process before enqueue (the recorder upserts on
	// the same key, so re-enqueue is wasted work). If the buffer
	// still fills (Postgres outage, cold-start burst), Hits drop and
	// increment DroppedCount; the seen-mark rolls back so a later
	// Push for the same key retries. Operators alert on a sustained
	// drop climb. See internal/canonical/discovery/sink.go.
	discoverySink := discovery.NewAsyncSink(discoveryRecorderAdapter{s: store}, discovery.AsyncSinkOptions{
		BufferSize:    1024,
		RecordTimeout: 2 * time.Second,
		Logger:        logger.With("component", "discovery"),
	})
	discoverySink.Start()
	discoveryMetricsStop, discoveryMetricsDone := watchDiscoveryDrops(discoverySink, logger.With("component", "discovery"))
	defer func() {
		discoveryMetricsStop()
		<-discoveryMetricsDone
	}()
	defer func() {
		discoverySink.Stop()
		if dropped := discoverySink.DroppedCount(); dropped > 0 {
			logger.Warn("discovery: hits dropped on shutdown drain",
				"dropped", dropped)
		}
	}()
	disp.SetDiscoverySink(discoverySink)
	logger.Info("discovery sink wired", "buffer_size", 1024)

	// ─── Soroban-events raw-event landing zone (ADR-0029) ────────
	// Every Soroban contract event the dispatcher routes is also
	// captured to the `soroban_events` hypertable. Additive — does
	// not replace per-source decoders (trades, oracle_updates,
	// blend_auctions, cctp_events, rozo_events, sep41_supply_events,
	// ...). Unblocks future per-source decoder backfills: they
	// become SQL `INSERT ... SELECT FROM soroban_events` queries
	// rather than hours of MinIO re-walking. See
	// internal/sources/sorobanevents + migration 0041.
	rawEventSink := sorobanevents.NewAsyncSink(store, sorobanevents.AsyncSinkOptions{
		BufferSize:    4096,
		BatchSize:     1000,
		FlushInterval: time.Second,
		WriteTimeout:  10 * time.Second,
		Logger:        logger.With("component", "soroban-events"),
	})
	rawEventSink.Start()
	// Ctx-cancel safety net (see backfill.go for rationale):
	// PushEvent blocks under back-pressure, but the dispatcher hot
	// path has no ctx awareness, so an unbounded postgres stall
	// could pin the streaming loop past SIGTERM. Watch rootCtx and
	// Stop the sink early so blocked producers unblock and the
	// dispatcher can honour cancellation. The deferred Stop below
	// is idempotent and will still run for the success path.
	go func() {
		<-rootCtx.Done()
		rawEventSink.Stop()
	}()
	defer func() {
		rawEventSink.Stop()
		logger.Info("soroban-events sink drained on shutdown",
			"written", rawEventSink.WrittenCount(),
			"dropped", rawEventSink.DroppedCount(),
			"skipped", rawEventSink.SkippedCount(),
		)
		if dropped := rawEventSink.DroppedCount(); dropped > 0 {
			logger.Warn("soroban-events: rows dropped at shutdown — last batch may be partial",
				"dropped", dropped)
		}
	}()
	disp.SetRawEventSink(rawEventSink)
	logger.Info("soroban-events sink wired",
		"buffer_size", 4096, "batch_size", 1000)

	// ─── ClickHouse real-time dual-sink (ADR-0034 #18) ─────────
	// When enabled, each ledger's structural extract is pushed to ClickHouse
	// inline (non-blocking), keeping the Tier-1 lake within ~seconds of the
	// chain for the real-time block explorer — vs the ~10-min ch-live-catchup
	// timer (which stays as the completeness backstop for anything this sink
	// drops under CH pressure). Off by default; the sink never blocks ingest.
	var chLiveSink *clickhouse.LiveSink
	if cfg.Storage.ClickHouseLiveSink {
		// The struct-tag default is example/docs-only (not applied at runtime),
		// so fall back to the local CH default if the operator enabled the sink
		// without setting clickhouse_addr — avoids a "missing address" boot fail.
		chAddr := cfg.Storage.ClickHouseAddr
		if chAddr == "" {
			chAddr = "127.0.0.1:9300"
		}
		chLiveSink, err = clickhouse.NewLiveSink(rootCtx, chAddr, clickhouse.LiveSinkOptions{
			Logger: logger.With("component", "ch-live-sink"),
		})
		if err != nil {
			return fmt.Errorf("clickhouse live-sink: %w", err)
		}
		chLiveSink.Start()
		defer func() {
			chLiveSink.Stop()
			logger.Info("ch live-sink drained on shutdown",
				"written", chLiveSink.WrittenCount(),
				"dropped", chLiveSink.DroppedCount(),
				"errored", chLiveSink.ErroredCount())
		}()
		logger.Info("ClickHouse real-time dual-sink enabled", "addr", cfg.Storage.ClickHouseAddr)
	}

	setSourceEnabled(cfg.Ingestion.EnabledSources, true)
	defer setSourceEnabled(cfg.Ingestion.EnabledSources, false)

	// ─── Starting ledger ───────────────────────────────────────
	from, err := resolveStartLedger(rootCtx, store, cfg.Ingestion.BackfillFromLedger)
	if err != nil {
		return fmt.Errorf("resolve start ledger: %w", err)
	}
	logger.Info("starting ledger resolved", "from", from)

	if dryRun {
		logger.Info("dry-run complete — exiting")
		return nil
	}

	// ─── Metrics HTTP endpoint ──────────────────────────────────
	metricsSrv := startMetricsServer(cfg.Obs, logger)

	// ─── Sink goroutine ────────────────────────────────────────
	// Sink mode depends on the projector config (ADR-0032 Phase 4):
	//   - projector disabled OR persist_per_source=true →
	//     SinkModeAll: write everything, parallel-mode with projector
	//     if it's running.
	//   - projector enabled AND persist_per_source=false →
	//     SinkModeSkipProjected: skip Soroban-derived events so the
	//     projector is sole writer for that subset; non-Soroban
	//     events (sdex, external, band, supply observers) still
	//     ride this path.
	sinkMode := pipeline.SinkModeAll
	if cfg.Ingestion.Projector.Enabled && !cfg.Ingestion.Projector.PersistPerSource {
		sinkMode = pipeline.SinkModeSkipProjected
		logger.Info("dispatcher events-goroutine: SKIP-PROJECTED mode — projector is sole writer for Soroban-derived events (ADR-0032 Phase 4)")
	}
	events := make(chan consumer.Event, 256)
	sinkDone := make(chan struct{})
	go func() {
		defer close(sinkDone)
		pipeline.PersistEvents(rootCtx, logger, store, events, sinkMode)
	}()

	// ─── Projector (ADR-0032) ──────────────────────────────────
	// Phase 3 (parallel mode, persist_per_source=true): projector
	// + dispatcher both write Soroban-derived events; PK
	// duplicates resolved by ON CONFLICT DO NOTHING. Phase 4
	// (persist_per_source=false): projector is sole writer.
	var projectorDone chan struct{}
	if cfg.Ingestion.Projector.Enabled {
		registry, perr := projector.BuildRegistry(cfg.Ingestion.EnabledSources, cfg.Oracle, soroswapOpts...)
		if perr != nil {
			return fmt.Errorf("projector registry: %w", perr)
		}
		// Sink wraps the same pipeline.HandleEvent the events
		// goroutine uses; decoded rows take the same per-source
		// write path. See internal/pipeline/sink.go.
		sinkFn := func(ctx context.Context, ev consumer.Event) {
			pipeline.HandleEvent(ctx, logger, store, ev)
		}
		proj := projector.New(store, registry, sinkFn, logger.With("component", "projector"))
		projectorDone = make(chan struct{})
		go func() {
			defer close(projectorDone)
			if err := proj.Run(rootCtx); err != nil && !errors.Is(err, context.Canceled) {
				logger.Warn("projector exited with error", "err", err)
			}
		}()
		logger.Info("projector wired (Phase 3 parallel mode)",
			"sources", len(registry.Sources))
	} else {
		logger.Info("projector disabled — dispatcher per-source sinks remain primary")
	}

	// Verified-currency catalogue (R-018 Phase 1.1 / 1.2). Drives
	// the CG poller's ticker map and the aggregator pair set — so
	// adding a verified currency to `internal/currency/data/seed.yaml`
	// automatically expands cross-check coverage.
	verifiedCurrencies, err := currency.LoadEmbedded()
	if err != nil {
		return fmt.Errorf("verified-currency catalogue: %w", err)
	}
	logger.Info("verified-currency catalogue loaded", "entries", len(verifiedCurrencies.All()))

	// ─── External streamers (off-chain CEX/FX/aggregators) ──────
	// Parallel to the Galexie → dispatcher path — same sink.
	// Per-venue goroutines live inside external.Run; we just
	// collect the shutdown wait func to block on during drain.
	externalWait, externalSources, err := startExternalConnectors(rootCtx, cfg.External, verifiedCurrencies, events, logger)
	if err != nil {
		return fmt.Errorf("external connectors: %w", err)
	}
	setSourceEnabled(externalSources, true)
	defer setSourceEnabled(externalSources, false)

	// ─── Ledgerstream → dispatcher loop ─────────────────────────
	// StreamArchiveThenLive switches from S3BucketArchive to S3BucketLive
	// at cfg.Ingestion.LiveSeamLedger. When seam=0 or from>=seam, this
	// degrades to a plain live-only Stream (the historical default).
	archiveCfg := pipeline.LedgerstreamConfig(cfg, cfg.Storage.S3BucketArchive)
	liveCfg := pipeline.LedgerstreamConfig(cfg, cfg.Storage.S3BucketLive)
	streamErr := make(chan error, 1)
	go func() {
		streamErr <- ledgerstream.StreamArchiveThenLive(
			rootCtx, archiveCfg, liveCfg, from, cfg.Ingestion.LiveSeamLedger, logger,
			func(lcm sdkxdr.LedgerCloseMeta) error {
				if perr := processAndPersistCursor(rootCtx, disp, events, store, logger, lcm, cfg.Stellar.Passphrase()); perr != nil {
					return perr
				}
				// Real-time CH fan-out: push the structural extract (non-blocking;
				// a slow CH never stalls ingest — drops are backstopped by the
				// catch-up timer). Extract is a 2nd decode of the LCM, negligible
				// at the live rate.
				if chLiveSink != nil {
					if ext, eerr := clickhouse.ExtractLedger(lcm, cfg.Stellar.Passphrase()); eerr == nil {
						chLiveSink.PushLedger(ext)
					}
				}
				return nil
			},
		)
	}()

	// ─── Shutdown ──────────────────────────────────────────────
	select {
	case <-rootCtx.Done():
		logger.Info("shutdown signal received — draining for up to 30s")
	case err := <-streamErr:
		if err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("ledgerstream exited with error", "err", err)
			return err
		}
	}

	shutdownCtx, stopDrain := context.WithTimeout(context.Background(), 30*time.Second)
	defer stopDrain()

	if metricsSrv != nil {
		if err := metricsSrv.Shutdown(shutdownCtx); err != nil {
			logger.Warn("metrics server shutdown", "err", err)
		}
	}

	// Wait for external connectors to finish draining before
	// closing the shared events channel — otherwise an in-flight
	// trade write on a closed channel panics the runner goroutine.
	externalWait()

	// Close events channel so the sink returns after draining.
	close(events)
	select {
	case <-sinkDone:
		logger.Info("clean shutdown")
	case <-shutdownCtx.Done():
		logger.Warn("drain timeout exceeded — hard exit")
	}

	// Wait for projector goroutines to exit. Each cycle is bounded
	// by PerSourceTimeout (60s) so this returns promptly under
	// rootCtx cancellation.
	if projectorDone != nil {
		select {
		case <-projectorDone:
			logger.Info("projector drained")
		case <-shutdownCtx.Done():
			logger.Warn("projector drain timeout — hard exit")
		}
	}
	return nil
}

// startExternalConnectors builds the enabled off-chain connectors
// from config and hands them to external.Run. Returns the wait
// function the shutdown path calls to drain cleanly. A nil-op wait
// is returned when no external sources are enabled — keeps the
// shutdown sequence unconditional.
func startExternalConnectors( //nolint:gocognit,gocyclo,funlen // dispatch-heavy; splitting would reduce linearity
	ctx context.Context,
	cfg config.ExternalConfig,
	catalogue *currency.Catalogue,
	events chan<- consumer.Event,
	logger *slog.Logger,
) (func(), []string, error) {
	var streamers []external.StreamerSpec
	var pollers []external.PollerSpec
	var enabled []string

	if cfg.Binance.Enabled {
		pairMap, err := externalbinance.DefaultPairs()
		if err != nil {
			return nil, nil, fmt.Errorf("binance default pairs: %w", err)
		}
		pairs, err := externalbinance.DefaultPairList()
		if err != nil {
			return nil, nil, fmt.Errorf("binance default pair list: %w", err)
		}
		s := externalbinance.NewStreamer(pairMap)
		s.Logger = logger
		streamers = append(streamers, external.StreamerSpec{
			Streamer: s,
			Pairs:    pairs,
		})
		logger.Info("external connector enabled",
			"source", externalbinance.SourceName,
			"pairs", len(pairs))
		enabled = append(enabled, externalbinance.SourceName)
	}

	if cfg.Kraken.Enabled {
		pairMap, err := externalkraken.DefaultPairs()
		if err != nil {
			return nil, nil, fmt.Errorf("kraken default pairs: %w", err)
		}
		pairs, err := externalkraken.DefaultPairList()
		if err != nil {
			return nil, nil, fmt.Errorf("kraken default pair list: %w", err)
		}
		s := externalkraken.NewStreamer(pairMap)
		s.Logger = logger
		streamers = append(streamers, external.StreamerSpec{
			Streamer: s,
			Pairs:    pairs,
		})
		logger.Info("external connector enabled",
			"source", externalkraken.SourceName,
			"pairs", len(pairs))
		enabled = append(enabled, externalkraken.SourceName)
	}

	if cfg.Bitstamp.Enabled {
		pairMap, err := externalbitstamp.DefaultPairs()
		if err != nil {
			return nil, nil, fmt.Errorf("bitstamp default pairs: %w", err)
		}
		pairs, err := externalbitstamp.DefaultPairList()
		if err != nil {
			return nil, nil, fmt.Errorf("bitstamp default pair list: %w", err)
		}
		s := externalbitstamp.NewStreamer(pairMap)
		s.Logger = logger
		streamers = append(streamers, external.StreamerSpec{
			Streamer: s,
			Pairs:    pairs,
		})
		logger.Info("external connector enabled",
			"source", externalbitstamp.SourceName,
			"pairs", len(pairs))
		enabled = append(enabled, externalbitstamp.SourceName)
	}

	if cfg.Coinbase.Enabled {
		pairMap, err := externalcoinbase.DefaultPairs()
		if err != nil {
			return nil, nil, fmt.Errorf("coinbase default pairs: %w", err)
		}
		pairs, err := externalcoinbase.DefaultPairList()
		if err != nil {
			return nil, nil, fmt.Errorf("coinbase default pair list: %w", err)
		}
		s := externalcoinbase.NewStreamer(pairMap)
		s.Logger = logger
		streamers = append(streamers, external.StreamerSpec{
			Streamer: s,
			Pairs:    pairs,
		})
		logger.Info("external connector enabled",
			"source", externalcoinbase.SourceName,
			"pairs", len(pairs))
		enabled = append(enabled, externalcoinbase.SourceName)
	}

	if cfg.ExchangeRatesApi.Enabled {
		// APIKey is resolved via env override at config load time
		// (see config.ApplyEnvOverrides → EXCHANGERATESAPI_KEY).
		p, err := externalexchangerates.NewPoller(cfg.ExchangeRatesApi.APIKey)
		if err != nil {
			return nil, nil, fmt.Errorf("exchangeratesapi: %w", err)
		}
		if cfg.ExchangeRatesApi.Base != "" {
			p.Base = cfg.ExchangeRatesApi.Base
		}
		pollers = append(pollers, external.PollerSpec{
			Poller: p,
			Pairs:  defaultFXPairs(p.Base),
		})
		logger.Info("external poller enabled",
			"source", externalexchangerates.SourceName,
			"base", p.Base)
		enabled = append(enabled, externalexchangerates.SourceName)
	}

	if cfg.PolygonForex.Enabled {
		p, err := externalpolygonforex.NewPoller(cfg.PolygonForex.APIKey)
		if err != nil {
			return nil, nil, fmt.Errorf("polygon-forex: %w", err)
		}
		if cfg.PolygonForex.Base != "" {
			p.Base = cfg.PolygonForex.Base
		}
		// Pair list: the union of every fiat appearing in the
		// enabled streamers' default pair sets. For v1 we use
		// a static default set — EUR/GBP/JPY/CAD/AUD/CHF + any
		// base-currency override. Operators can extend via config
		// in a follow-up PR.
		pairs := defaultFXPairs(p.Base)
		pollers = append(pollers, external.PollerSpec{
			Poller: p,
			Pairs:  pairs,
		})
		logger.Info("external poller enabled",
			"source", externalpolygonforex.SourceName,
			"base", p.Base,
			"symbols", len(pairs))
		enabled = append(enabled, externalpolygonforex.SourceName)
	}

	// Aggregator pollers: cross-check only, class=aggregator →
	// emitted OracleUpdates excluded from VWAP. Pair list is the
	// union of fiat-quoted crypto pairs across enabled streamers;
	// aggregator pollers follow wherever the exchanges are
	// targeting.
	//
	// R-018 Phase 1.2: derive the set from the verified-currency
	// catalogue so adding USDT / EURC / a new global crypto to the
	// seed yaml automatically expands aggregator coverage. The
	// hardcoded list (`defaultAggregatorPairs`) remains as a
	// fallback when the catalogue isn't wired.
	aggregatorPairs := aggregatorPairsFromCatalogue(catalogue)
	if len(aggregatorPairs) == 0 {
		aggregatorPairs = defaultAggregatorPairs()
	}

	if cfg.CoinGecko.Enabled {
		p := externalcoingecko.NewPoller()
		if cfg.CoinGecko.PollInterval > 0 {
			p.Interval = cfg.CoinGecko.PollInterval
		}
		// Catalogue-derived ticker map (R-018 Phase 1.2). Empty
		// map (catalogue not wired or seed has no coingecko_id
		// entries) means the poller falls back to the package
		// default, preserving the original hardcoded coverage.
		if ids := catalogue.CoinGeckoIDs(); len(ids) > 0 {
			p.TickerToID = ids
		}
		// CoinGecko's "public no-auth" tier was tightened in late 2024
		// — unauthenticated requests get throttled aggressively or
		// rejected outright (observed live on r1 2026-05-09 as one
		// 429 per minute). Read the demo (free signup) and pro keys
		// from env so operators can fix without a code-side toml
		// schema change. Pro key wins when both are set.
		if k := strings.TrimSpace(os.Getenv("COINGECKO_API_KEY")); k != "" {
			p.APIKey = k
		}
		if k := strings.TrimSpace(os.Getenv("COINGECKO_DEMO_API_KEY")); k != "" {
			p.DemoAPIKey = k
		}
		authMode := "anonymous"
		if p.APIKey != "" {
			authMode = "pro"
		} else if p.DemoAPIKey != "" {
			authMode = "demo"
		}
		pollers = append(pollers, external.PollerSpec{
			Poller: p,
			Pairs:  aggregatorPairs,
		})
		logger.Info("external poller enabled",
			"source", externalcoingecko.SourceName,
			"pairs", len(aggregatorPairs),
			"poll_interval", p.PollInterval(),
			"auth_mode", authMode)
		enabled = append(enabled, externalcoingecko.SourceName)
	}

	if cfg.CoinMarketCap.Enabled {
		p, err := externalcoinmarketcap.NewPoller(cfg.CoinMarketCap.APIKey)
		if err != nil {
			return nil, nil, fmt.Errorf("coinmarketcap: %w", err)
		}
		// F-1237 (codex audit-2026-05-12): bind the verified-
		// currency catalogue's CMC IDs so the poller queries by
		// `id=<numeric>` for any ticker with an authoritative ID,
		// disambiguating polluted tickers (LUNA, LUNC, etc.).
		p.CMCIDs = catalogue.CoinMarketCapIDs()
		pollers = append(pollers, external.PollerSpec{
			Poller: p,
			Pairs:  aggregatorPairs,
		})
		logger.Info("external poller enabled",
			"source", externalcoinmarketcap.SourceName,
			"pairs", len(aggregatorPairs),
			"cmc_ids", len(p.CMCIDs))
		enabled = append(enabled, externalcoinmarketcap.SourceName)
	}

	if cfg.CryptoCompare.Enabled {
		p, err := externalcryptocompare.NewPoller(cfg.CryptoCompare.APIKey)
		if err != nil {
			return nil, nil, fmt.Errorf("cryptocompare: %w", err)
		}
		pollers = append(pollers, external.PollerSpec{
			Poller: p,
			Pairs:  aggregatorPairs,
		})
		logger.Info("external poller enabled",
			"source", externalcryptocompare.SourceName,
			"pairs", len(aggregatorPairs))
		enabled = append(enabled, externalcryptocompare.SourceName)
	}

	if cfg.Chainlink.Enabled {
		feedMap, pairs, err := chainlinkFeedSetFromConfig(cfg.Chainlink.FeedMap)
		if err != nil {
			return nil, nil, fmt.Errorf("chainlink: %w", err)
		}
		if len(pairs) == 0 {
			logger.Warn("chainlink ingest enabled but feed_map is empty after parse — skipping",
				"source", externalchainlink.SourceName)
		} else {
			p := externalchainlink.NewPoller(cfg.Chainlink.RPCUrl, feedMap)
			if cfg.Chainlink.PollInterval > 0 {
				p.Interval = cfg.Chainlink.PollInterval
			}
			p.Logger = logger
			pollers = append(pollers, external.PollerSpec{
				Poller: p,
				Pairs:  pairs,
			})
			authMode := "anonymous"
			switch {
			case strings.Contains(cfg.Chainlink.RPCUrl, "alchemy"):
				authMode = "alchemy"
			case strings.Contains(cfg.Chainlink.RPCUrl, "infura"):
				authMode = "infura"
			case strings.Contains(cfg.Chainlink.RPCUrl, "quicknode"):
				authMode = "quicknode"
			}
			logger.Info("external poller enabled",
				"source", externalchainlink.SourceName,
				"feeds", len(pairs),
				"poll_interval", p.PollInterval(),
				"rpc_provider", authMode)
			enabled = append(enabled, externalchainlink.SourceName)
		}
	}

	if cfg.ECB.Enabled {
		p := externalecb.NewPoller()
		// ECB speaks fiat-only; derive the pair list from anything
		// with a fiat side. defaultFXPairs builds fiat/<base>
		// crosses; ECB's poller further filters to fiats it has
		// rates for.
		pairs := defaultFXPairs("EUR")
		pollers = append(pollers, external.PollerSpec{
			Poller: p,
			Pairs:  pairs,
		})
		logger.Info("external poller enabled",
			"source", externalecb.SourceName,
			"pairs", len(pairs))
		enabled = append(enabled, externalecb.SourceName)
	}

	if len(streamers) == 0 && len(pollers) == 0 {
		logger.Info("no external connectors enabled")
		return func() {}, nil, nil
	}

	wait, err := external.Run(ctx, streamers, pollers, events, logger)
	if err != nil {
		return nil, nil, err
	}
	return wait, enabled, nil
}

func setSourceEnabled(sources []string, enabled bool) {
	val := 0.0
	if enabled {
		val = 1
	}
	for _, source := range sources {
		if source == "" {
			continue
		}
		obs.SourceEnabled.WithLabelValues(strings.ToLower(source)).Set(val)
	}
}

func watchDiscoveryDrops(sink *discovery.AsyncSink, logger *slog.Logger) (context.CancelFunc, <-chan struct{}) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()

		var lastDropped, lastSkipped uint64
		flush := func() {
			lastDropped = emitDiscoveryDropMetricDelta(lastDropped, sink.DroppedCount(), logger)
			lastSkipped = emitDiscoverySkipMetricDelta(lastSkipped, sink.SkippedCount())
		}

		for {
			select {
			case <-ticker.C:
				flush()
			case <-ctx.Done():
				flush()
				return
			}
		}
	}()
	return cancel, done
}

func emitDiscoveryDropMetricDelta(prev, current uint64, logger *slog.Logger) uint64 {
	if current <= prev {
		return current
	}
	delta := current - prev
	obs.DiscoveryDroppedHitsTotal.Add(float64(delta))
	if logger != nil {
		logger.Warn("discovery: hits dropped",
			"delta", delta,
			"total", current,
		)
	}
	return current
}

// watchPostgresPing fires a [timescale.Store.PingContext] every
// 60 s and emits the F-0151 resilience metrics. The actual
// reconnect path lives in database/sql via
// [timescale.PoolConnMaxLifetime]; this goroutine is the
// OBSERVABILITY hook so a stuck pool surfaces in minutes via the
// `ratesengine_postgres_ping_failing` alert instead of hours of
// silent drift.
//
// Logs a structured warning when the consecutive-failure streak
// crosses 3 (≈3 min). At that point the pool is almost certainly
// wedged; the lifetime safety-net will refresh the next conn the
// pool hands out, but the live signal is in this log line and the
// metric.
//
// Returns (cancel, done) following the [watchDiscoveryDrops]
// shape so main's shutdown sequence is uniform.
func watchPostgresPing(parent context.Context, store *timescale.Store, logger *slog.Logger) (context.CancelFunc, <-chan struct{}) {
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()

		var failures int
		// Probe once immediately so the metric is non-empty
		// before the first 60 s tick — gives a clean scrape
		// at process boot.
		failures = postgresPingProbe(ctx, store, logger, failures)
		for {
			select {
			case <-ticker.C:
				failures = postgresPingProbe(ctx, store, logger, failures)
			case <-ctx.Done():
				return
			}
		}
	}()
	return cancel, done
}

// postgresPingProbe runs a single ping cycle and returns the new
// consecutive-failure count. Extracted from watchPostgresPing to
// stay under the gocognit threshold.
func postgresPingProbe(ctx context.Context, store *timescale.Store, logger *slog.Logger, prevFailures int) int {
	pctx, pcancel := context.WithTimeout(ctx, 5*time.Second)
	defer pcancel()
	if err := store.PingContext(pctx); err != nil {
		failures := prevFailures + 1
		obs.PostgresPingTotal.WithLabelValues("error").Inc()
		obs.PostgresPingFailureStreak.Set(float64(failures))
		logPostgresPingFailure(logger, err, failures)
		return failures
	}
	if prevFailures > 0 && logger != nil {
		logger.Info("postgres ping recovered", "previous_streak", prevFailures)
	}
	obs.PostgresPingTotal.WithLabelValues("ok").Inc()
	obs.PostgresPingFailureStreak.Set(0)
	return 0
}

// logPostgresPingFailure emits the per-failure log line. The
// `streak == 3` threshold message is what operators grep for when
// chasing F-0151 — "pool may be wedged" is the canonical string.
func logPostgresPingFailure(logger *slog.Logger, err error, streak int) {
	if logger == nil {
		return
	}
	if streak == 3 {
		logger.Error("postgres ping failed 3x — pool may be wedged",
			"err", err,
			"streak", streak,
			"safety_net", "ConnMaxLifetime refresh pending",
		)
		return
	}
	logger.Warn("postgres ping failed", "err", err, "streak", streak)
}

// emitDiscoverySkipMetricDelta updates the skip counter without
// logging — skips are the healthy steady-state under in-process
// dedup and would dominate logs unhelpfully.
func emitDiscoverySkipMetricDelta(prev, current uint64) uint64 {
	if current <= prev {
		return current
	}
	obs.DiscoverySkippedHitsTotal.Add(float64(current - prev))
	return current
}

// aggregatorPairsFromCatalogue builds the aggregator pair set from
// the verified-currency catalogue. Includes every catalogue ticker
// that has a `coingecko_id` set — CMC and CryptoCompare key off the
// ticker symbol directly, so a missing CG slug is the conservative
// "skip" signal (no upstream coverage worth polling).
//
// Cross with the operator's fiat list (currently fixed at USD / EUR
// / GBP — the divergence layer's coverage). Operators who want
// narrower polling can add a per-poller Symbols override in a
// future config field; for now the catalogue is the single knob.
func aggregatorPairsFromCatalogue(cat *currency.Catalogue) []canonical.Pair {
	if cat == nil {
		return nil
	}
	cgIDs := cat.CoinGeckoIDs()
	if len(cgIDs) == 0 {
		return nil
	}
	fiats := []string{"USD", "EUR", "GBP"}
	out := make([]canonical.Pair, 0, len(cgIDs)*len(fiats))
	for ticker := range cgIDs {
		ca, err := canonical.NewCryptoAsset(ticker)
		if err != nil {
			// Ticker not on the canonical crypto allow-list (e.g. a
			// future entry we haven't added). Skip — adding to the
			// allow-list is a separate, deliberate change.
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

// defaultAggregatorPairs is the pre-catalogue hardcoded pair set
// aggregators (CoinGecko / CMC / CryptoCompare) query for
// cross-check. Retained as a fallback when the verified-currency
// catalogue isn't wired (test fixtures, future config overrides).
// New entries should be added to `internal/currency/data/seed.yaml`
// instead — that list is the canonical source.
func defaultAggregatorPairs() []canonical.Pair {
	// Anchors + top-cap globals. XLM first per its product-special
	// status; the rest in alphabetical order to keep diffs minimal.
	cryptos := []string{
		"XLM", "BTC", "ETH",
		"ADA", "ATOM", "AVAX", "BCH", "BNB", "DASH", "DOGE", "DOT",
		"LINK", "LTC", "NEAR", "SHIB", "SOL", "TON", "TRX", "UNI", "XRP",
	}
	fiats := []string{"USD", "EUR", "GBP"}
	out := make([]canonical.Pair, 0, len(cryptos)*len(fiats))
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

// defaultFXPairs returns the G10-ish fiat cross-rate set against
// the given base currency. These feed ExchangeRatesApi / Polygon.io
// / ECB — all FX pollers share the same target currency list.
// Operator overrides via per-poller Symbols field when needed.
func defaultFXPairs(base string) []canonical.Pair {
	baseAsset, err := canonical.NewFiatAsset(base)
	if err != nil {
		// Base not on the ADR-0010 allow-list — poller will no-op.
		return nil
	}
	targets := []string{"EUR", "GBP", "JPY", "CAD", "AUD", "CHF", "NZD", "SEK", "NOK", "MXN"}
	out := make([]canonical.Pair, 0, len(targets))
	for _, code := range targets {
		if code == base {
			continue
		}
		a, err := canonical.NewFiatAsset(code)
		if err != nil {
			continue
		}
		p, err := canonical.NewPair(a, baseAsset)
		if err != nil {
			continue
		}
		out = append(out, p)
	}
	return out
}

// chainlinkFeedSetFromConfig is the tiny adapter that bridges the
// operator-facing config schema (config.ChainlinkFeedSetting) and
// the chainlink package's runtime FeedSpec. Kept in the cmd dir
// because the chainlink package can't import config (would create
// a cycle: config has no dep on chainlink today, and we want to
// keep it that way).
//
// The actual default-fallback + parse logic lives in
// chainlink.BuildFeedSet so both the indexer (live poll) and
// ratesengine-ops (backfill subcommand) hit the same code path.
func chainlinkFeedSetFromConfig(in map[string]config.ChainlinkFeedSetting) (map[string]externalchainlink.FeedSpec, []canonical.Pair, error) {
	adapted := make(map[string]externalchainlink.FeedSpec, len(in))
	for k, v := range in {
		adapted[k] = externalchainlink.FeedSpec{
			Address:  v.Address,
			Decimals: v.Decimals,
			Invert:   v.Invert,
		}
	}
	return externalchainlink.BuildFeedSet(adapted)
}

// ─── Dispatcher wiring ──────────────────────────────────────────
// ─── Ledger processing ─────────────────────────────────────────
// resolveStartLedger chooses where to begin ingesting on startup:
//  1. A persisted cursor wins — resume from one ledger past it.
//  2. Otherwise, cfg.Ingestion.BackfillFromLedger.
//  3. Otherwise, an error — we refuse to pick a default ledger
//     silently because that's how operators end up re-ingesting
//     genesis by accident.
func resolveStartLedger(ctx context.Context, store *timescale.Store, backfillFrom uint32) (uint32, error) {
	c, err := store.GetCursor(ctx, cursorSource, "")
	switch {
	case errors.Is(err, timescale.ErrNotFound):
		if backfillFrom == 0 {
			return 0, fmt.Errorf(
				"no persisted cursor and ingestion.backfill_from_ledger=0 — " +
					"set backfill_from_ledger to an explicit start, e.g. the " +
					"current network tip",
			)
		}
		return backfillFrom, nil
	case err != nil:
		return 0, fmt.Errorf("load cursor: %w", err)
	}
	return c.LastLedger + 1, nil
}

// processAndPersistCursor wraps pipeline.ProcessLedger with the
// indexer-specific cursor upsert + cursor metric. The cursor lets a
// restart resume from cursor+1 instead of replaying from the seam
// every boot. Backfill (`ratesengine-ops backfill`) does NOT call
// this — it has explicit -from/-to and shares no cursor row with
// the indexer.
func processAndPersistCursor(
	ctx context.Context,
	disp *dispatcher.Dispatcher,
	events chan<- consumer.Event,
	store *timescale.Store,
	logger *slog.Logger,
	lcm sdkxdr.LedgerCloseMeta,
	networkPassphrase string,
) error {
	if err := pipeline.ProcessLedger(ctx, disp, events, logger, lcm, networkPassphrase); err != nil {
		return err
	}
	// ADR-0033 Phase 2: write the substrate-continuity record AFTER
	// the ledger's events have persisted, so ledger_ingest_log is an
	// authoritative "this ledger is fully done" marker — unlike the
	// cursor below, which is operational resume state. Best-effort:
	// a write failure here must not stall ingest (the gap surfaces in
	// the substrate continuity check, which is the whole point).
	recordLedgerIngest(ctx, store, logger, lcm, networkPassphrase)
	if err := store.UpsertCursor(ctx, cursorSource, "", lcm.LedgerSequence()); err != nil {
		logger.Warn("cursor upsert",
			"ledger", lcm.LedgerSequence(),
			"err", err,
		)
	}
	recordCursorMetric(lcm.LedgerSequence())
	return nil
}

// recordLedgerIngest computes the decoder-independent LCM census and
// writes the ledger_ingest_log substrate record (ADR-0033 Phase 2).
// The census is a SECOND, independent walk of the LCM (not the decode
// walk) on purpose: a bug in the dispatch walk cannot then hide itself
// in the census it's reconciled against. Cost is a cheap structural
// re-parse (no body decode); DB writes dominate the per-ledger budget.
func recordLedgerIngest(
	ctx context.Context,
	store *timescale.Store,
	logger *slog.Logger,
	lcm sdkxdr.LedgerCloseMeta,
	networkPassphrase string,
) {
	census, err := dispatcher.CensusLedger(lcm, networkPassphrase)
	if err != nil {
		logger.Warn("ledger census", "ledger", lcm.LedgerSequence(), "err", err)
		return
	}
	if census.TxReadErrors > 0 {
		// A malformed tx means the census may undercount this ledger's
		// primitives. Don't write an authoritative substrate row we
		// can't stand behind — leave the ledger as a substrate gap so
		// it's re-examined rather than silently recorded wrong.
		logger.Warn("ledger census tx read errors; skipping substrate record",
			"ledger", census.LedgerSeq, "tx_read_errors", census.TxReadErrors)
		return
	}
	row := timescale.LedgerIngestRow{
		LedgerSeq:               census.LedgerSeq,
		LedgerCloseTime:         census.LedgerCloseTime,
		LedgerHash:              census.LedgerHash[:],
		PrevLedgerHash:          census.PrevLedgerHash[:],
		SorobanEventCount:       census.SorobanEventCount,
		ClassicTradeEffectCount: census.ClassicTradeEffectCount,
	}
	if err := store.UpsertLedgerIngestLog(ctx, row); err != nil {
		logger.Warn("ledger ingest log upsert", "ledger", census.LedgerSeq, "err", err)
	}
}

func recordCursorMetric(ledger uint32) {
	obs.CursorLastLedger.WithLabelValues(cursorSource).Set(float64(ledger))
}

func startMetricsServer(obsCfg config.ObsConfig, logger *slog.Logger) *http.Server {
	if obsCfg.MetricsListen == "" {
		logger.Warn("obs.metrics_listen is empty — /metrics endpoint disabled; Prometheus alerts on source metrics will not fire")
		return nil
	}
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", obs.Handler())
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	srv := &http.Server{
		Addr:              obsCfg.MetricsListen,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		logger.Info("metrics endpoint listening", "addr", obsCfg.MetricsListen)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("metrics server exited", "err", err)
		}
	}()
	return srv
}

func mkLogger(cfg config.ObsConfig) *slog.Logger {
	return obs.NewLogger(cfg, "ratesengine-indexer")
}

// discoveryRecorderAdapter wraps *timescale.Store to satisfy
// discovery.Recorder. The Store methods are named RecordDiscovered /
// IsKnownDiscovered (suffixed with the table they touch); the
// interface uses Record / IsKnown — this thin adapter bridges the
// two without renaming the storage-layer methods (which would
// collide with future RecordX / IsKnownX domains added to Store).
type discoveryRecorderAdapter struct {
	s *timescale.Store
}

func (a discoveryRecorderAdapter) Record(ctx context.Context, hit discovery.Hit) error {
	return a.s.RecordDiscovered(ctx, hit)
}

func (a discoveryRecorderAdapter) IsKnown(ctx context.Context, contractID string) (bool, error) {
	return a.s.IsKnownDiscovered(ctx, contractID)
}

// Compile-time guard.
var _ discovery.Recorder = discoveryRecorderAdapter{}
