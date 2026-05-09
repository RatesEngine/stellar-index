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
	"github.com/RatesEngine/rates-engine/internal/dispatcher"
	"github.com/RatesEngine/rates-engine/internal/dispatcher/statsflush"
	"github.com/RatesEngine/rates-engine/internal/ledgerstream"
	"github.com/RatesEngine/rates-engine/internal/obs"
	"github.com/RatesEngine/rates-engine/internal/pipeline"
	"github.com/RatesEngine/rates-engine/internal/sources/external"
	externalbinance "github.com/RatesEngine/rates-engine/internal/sources/external/binance"
	externalbitstamp "github.com/RatesEngine/rates-engine/internal/sources/external/bitstamp"
	externalcoinbase "github.com/RatesEngine/rates-engine/internal/sources/external/coinbase"
	externalcoingecko "github.com/RatesEngine/rates-engine/internal/sources/external/coingecko"
	externalcoinmarketcap "github.com/RatesEngine/rates-engine/internal/sources/external/coinmarketcap"
	externalcryptocompare "github.com/RatesEngine/rates-engine/internal/sources/external/cryptocompare"
	externalecb "github.com/RatesEngine/rates-engine/internal/sources/external/ecb"
	externalexchangerates "github.com/RatesEngine/rates-engine/internal/sources/external/exchangeratesapi"
	externalkraken "github.com/RatesEngine/rates-engine/internal/sources/external/kraken"
	externalpolygonforex "github.com/RatesEngine/rates-engine/internal/sources/external/polygonforex"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
	"github.com/RatesEngine/rates-engine/internal/version"
)

// cursorSource is the single `source` label stored in the
// ingestion_cursors table for the ledgerstream pipeline. There's
// exactly one cursor now — the whole pipeline tracks one
// last-processed ledger. (Per-source cursors were part of the
// pre-165 orchestrator topology.)
const cursorSource = "ledgerstream"

func main() {
	var (
		cfgPath     = flag.String("config", "", "Path to TOML config file (required)")
		dryRun      = flag.Bool("dry-run", false, "Load config + open connections + exit without ingesting")
		showVersion = flag.Bool("version", false, "Print version and exit")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println(version.String())
		return
	}

	if *cfgPath == "" {
		fmt.Fprintln(os.Stderr, "ratesengine-indexer: -config is required")
		flag.Usage()
		os.Exit(2)
	}

	if err := run(*cfgPath, *dryRun); err != nil {
		fmt.Fprintf(os.Stderr, "ratesengine-indexer: %v\n", err)
		os.Exit(1)
	}
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
	events := make(chan consumer.Event, 256)
	sinkDone := make(chan struct{})
	go func() {
		defer close(sinkDone)
		pipeline.PersistEvents(rootCtx, logger, store, events)
	}()

	// ─── External streamers (off-chain CEX/FX/aggregators) ──────
	// Parallel to the Galexie → dispatcher path — same sink.
	// Per-venue goroutines live inside external.Run; we just
	// collect the shutdown wait func to block on during drain.
	externalWait, externalSources, err := startExternalConnectors(rootCtx, cfg.External, events, logger)
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
				return processAndPersistCursor(rootCtx, disp, events, store, logger, lcm, cfg.Stellar.Passphrase())
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
	aggregatorPairs := defaultAggregatorPairs()

	if cfg.CoinGecko.Enabled {
		p := externalcoingecko.NewPoller()
		if cfg.CoinGecko.PollInterval > 0 {
			p.Interval = cfg.CoinGecko.PollInterval
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
		pollers = append(pollers, external.PollerSpec{
			Poller: p,
			Pairs:  aggregatorPairs,
		})
		logger.Info("external poller enabled",
			"source", externalcoinmarketcap.SourceName,
			"pairs", len(aggregatorPairs))
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

// defaultAggregatorPairs is the pair set aggregators (CoinGecko /
// CMC / CryptoCompare) query for cross-check. Mirrors the cross-
// venue VWAP coverage so the divergence detector has an apples-to-
// apples reference for every pair we publish a price for. Operators
// can narrow via a future per-poller Symbols override.
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
	if err := store.UpsertCursor(ctx, cursorSource, "", lcm.LedgerSequence()); err != nil {
		logger.Warn("cursor upsert",
			"ledger", lcm.LedgerSequence(),
			"err", err,
		)
	}
	recordCursorMetric(lcm.LedgerSequence())
	return nil
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
