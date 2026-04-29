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
		cfgPath = flag.String("config", "", "Path to TOML config file (required)")
		dryRun  = flag.Bool("dry-run", false, "Load config + open connections + exit without ingesting")
	)
	flag.Parse()

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

//nolint:funlen // top-level binary lifecycle; splitting reduces readability of dependency-construction order
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

	// ─── Dispatcher + decoders ─────────────────────────────────
	disp, err := pipeline.BuildDispatcher(cfg.Ingestion.EnabledSources, cfg.Oracle)
	if err != nil {
		return fmt.Errorf("build dispatcher: %w", err)
	}

	// ─── SEP-41 auto-discovery sink ──────────────────────────────
	// Buffers Hits to a channel; a worker goroutine drains them to
	// timescale.Store.RecordDiscovered. The dispatcher's Push call
	// is non-blocking — if the buffer fills (Postgres outage,
	// workload spike beyond BufferSize), Hits drop and increment
	// DroppedCount. Operators alert on a sustained climb. See
	// internal/canonical/discovery/sink.go for the contract.
	discoverySink := discovery.NewAsyncSink(discoveryRecorderAdapter{s: store}, discovery.AsyncSinkOptions{
		BufferSize:    1024,
		RecordTimeout: 2 * time.Second,
		Logger:        logger.With("component", "discovery"),
	})
	discoverySink.Start()
	defer func() {
		discoverySink.Stop()
		if dropped := discoverySink.DroppedCount(); dropped > 0 {
			logger.Warn("discovery: hits dropped on shutdown drain",
				"dropped", dropped)
		}
	}()
	disp.SetDiscoverySink(discoverySink)
	logger.Info("discovery sink wired", "buffer_size", 1024)

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
	externalWait, err := startExternalConnectors(rootCtx, cfg.External, events, logger)
	if err != nil {
		return fmt.Errorf("external connectors: %w", err)
	}

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
) (func(), error) {
	var streamers []external.StreamerSpec
	var pollers []external.PollerSpec

	if cfg.Binance.Enabled {
		pairMap, err := externalbinance.DefaultPairs()
		if err != nil {
			return nil, fmt.Errorf("binance default pairs: %w", err)
		}
		pairs, err := externalbinance.DefaultPairList()
		if err != nil {
			return nil, fmt.Errorf("binance default pair list: %w", err)
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
	}

	if cfg.Kraken.Enabled {
		pairMap, err := externalkraken.DefaultPairs()
		if err != nil {
			return nil, fmt.Errorf("kraken default pairs: %w", err)
		}
		pairs, err := externalkraken.DefaultPairList()
		if err != nil {
			return nil, fmt.Errorf("kraken default pair list: %w", err)
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
	}

	if cfg.Bitstamp.Enabled {
		pairMap, err := externalbitstamp.DefaultPairs()
		if err != nil {
			return nil, fmt.Errorf("bitstamp default pairs: %w", err)
		}
		pairs, err := externalbitstamp.DefaultPairList()
		if err != nil {
			return nil, fmt.Errorf("bitstamp default pair list: %w", err)
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
	}

	if cfg.Coinbase.Enabled {
		pairMap, err := externalcoinbase.DefaultPairs()
		if err != nil {
			return nil, fmt.Errorf("coinbase default pairs: %w", err)
		}
		pairs, err := externalcoinbase.DefaultPairList()
		if err != nil {
			return nil, fmt.Errorf("coinbase default pair list: %w", err)
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
	}

	if cfg.ExchangeRatesApi.Enabled {
		// APIKey is resolved via env override at config load time
		// (see config.ApplyEnvOverrides → EXCHANGERATESAPI_KEY).
		p, err := externalexchangerates.NewPoller(cfg.ExchangeRatesApi.APIKey)
		if err != nil {
			return nil, fmt.Errorf("exchangeratesapi: %w", err)
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
	}

	if cfg.PolygonForex.Enabled {
		p, err := externalpolygonforex.NewPoller(cfg.PolygonForex.APIKey)
		if err != nil {
			return nil, fmt.Errorf("polygon-forex: %w", err)
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
	}

	// Aggregator pollers: cross-check only, class=aggregator →
	// emitted OracleUpdates excluded from VWAP. Pair list is the
	// union of fiat-quoted crypto pairs across enabled streamers;
	// aggregator pollers follow wherever the exchanges are
	// targeting.
	aggregatorPairs := defaultAggregatorPairs()

	if cfg.CoinGecko.Enabled {
		p := externalcoingecko.NewPoller()
		pollers = append(pollers, external.PollerSpec{
			Poller: p,
			Pairs:  aggregatorPairs,
		})
		logger.Info("external poller enabled",
			"source", externalcoingecko.SourceName,
			"pairs", len(aggregatorPairs))
	}

	if cfg.CoinMarketCap.Enabled {
		p, err := externalcoinmarketcap.NewPoller(cfg.CoinMarketCap.APIKey)
		if err != nil {
			return nil, fmt.Errorf("coinmarketcap: %w", err)
		}
		pollers = append(pollers, external.PollerSpec{
			Poller: p,
			Pairs:  aggregatorPairs,
		})
		logger.Info("external poller enabled",
			"source", externalcoinmarketcap.SourceName,
			"pairs", len(aggregatorPairs))
	}

	if cfg.CryptoCompare.Enabled {
		p, err := externalcryptocompare.NewPoller(cfg.CryptoCompare.APIKey)
		if err != nil {
			return nil, fmt.Errorf("cryptocompare: %w", err)
		}
		pollers = append(pollers, external.PollerSpec{
			Poller: p,
			Pairs:  aggregatorPairs,
		})
		logger.Info("external poller enabled",
			"source", externalcryptocompare.SourceName,
			"pairs", len(aggregatorPairs))
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
	}

	if len(streamers) == 0 && len(pollers) == 0 {
		logger.Info("no external connectors enabled")
		return func() {}, nil
	}

	return external.Run(ctx, streamers, pollers, events, logger)
}

// defaultAggregatorPairs is the pair set aggregators (CoinGecko /
// CMC / CryptoCompare) query for cross-check. Intentionally broad
// — covers XLM against the common fiats + BTC/USD + ETH/USD as
// reference anchors. Operators can narrow via a future per-poller
// Symbols override; for v1 this fixed set matches what the
// divergence detector will want to compare against the aggregator's
// output.
func defaultAggregatorPairs() []canonical.Pair {
	cryptos := []string{"XLM", "BTC", "ETH"}
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
	obs.CursorLastLedger.WithLabelValues(cursorSource, "").Set(float64(lcm.LedgerSequence()))
	return nil
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

func mkLogger(obs config.ObsConfig) *slog.Logger {
	level := slog.LevelInfo
	switch strings.ToLower(obs.LogLevel) {
	case "debug":
		level = slog.LevelDebug
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	var handler slog.Handler
	opts := &slog.HandlerOptions{Level: level}
	switch strings.ToLower(obs.LogFormat) {
	case "console", "text":
		handler = slog.NewTextHandler(os.Stderr, opts)
	default:
		handler = slog.NewJSONHandler(os.Stderr, opts)
	}
	return slog.New(handler).With("binary", "ratesengine-indexer")
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
