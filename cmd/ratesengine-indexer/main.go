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

	"github.com/stellar/go-stellar-sdk/support/datastore"
	sdkxdr "github.com/stellar/go-stellar-sdk/xdr"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/config"
	"github.com/RatesEngine/rates-engine/internal/consumer"
	"github.com/RatesEngine/rates-engine/internal/dispatcher"
	"github.com/RatesEngine/rates-engine/internal/ledgerstream"
	"github.com/RatesEngine/rates-engine/internal/obs"
	"github.com/RatesEngine/rates-engine/internal/sources/aquarius"
	"github.com/RatesEngine/rates-engine/internal/sources/phoenix"
	"github.com/RatesEngine/rates-engine/internal/sources/reflector"
	"github.com/RatesEngine/rates-engine/internal/sources/sdex"
	"github.com/RatesEngine/rates-engine/internal/sources/soroswap"
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
	disp, err := buildDispatcher(cfg.Ingestion.EnabledSources, cfg.Oracle)
	if err != nil {
		return fmt.Errorf("build dispatcher: %w", err)
	}

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
		persistEvents(rootCtx, logger, store, events)
	}()

	// ─── Ledgerstream → dispatcher loop ─────────────────────────
	lsConfig := ledgerstreamConfig(cfg)
	streamErr := make(chan error, 1)
	go func() {
		streamErr <- ledgerstream.Stream(rootCtx, lsConfig, from, 0, /*unbounded*/
			func(lcm sdkxdr.LedgerCloseMeta) error {
				return processAndPersist(rootCtx, disp, events, store, logger, lcm, cfg.Stellar.Network)
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

// ─── Dispatcher wiring ──────────────────────────────────────────

// buildDispatcher maps cfg.Ingestion.EnabledSources to a configured
// Dispatcher. Unknown source names are a fatal config error.
//
// Reflector variants each take the mainnet contract address from
// cfg.Oracle; any variant enabled without its corresponding
// contract configured is rejected at startup.
func buildDispatcher(names []string, oracle config.OracleConfig) (*dispatcher.Dispatcher, error) {
	var decoders []dispatcher.Decoder
	var opDecoders []dispatcher.OpDecoder
	for _, name := range names {
		switch strings.ToLower(name) {
		case soroswap.SourceName:
			// Decoder loads pair registry lazily from factory
			// new_pair events seen during ingest. Operator can
			// also call SeedPair at startup from Timescale's
			// distinct (source, pair_contract) set — future
			// ratesengine-ops subcommand.
			decoders = append(decoders, soroswap.NewDecoder())
		case aquarius.SourceName:
			decoders = append(decoders, aquarius.NewDecoder())
		case phoenix.SourceName:
			decoders = append(decoders, phoenix.NewDecoder())
		case reflector.SourceDEX:
			if oracle.Reflector.DEXContract == "" {
				return nil, fmt.Errorf(
					"source %q enabled but oracle.reflector.dex_contract is empty",
					name)
			}
			decoders = append(decoders,
				reflector.NewDecoder(reflector.VariantDEX, oracle.Reflector.DEXContract))
		case reflector.SourceCEX:
			if oracle.Reflector.CEXContract == "" {
				return nil, fmt.Errorf(
					"source %q enabled but oracle.reflector.cex_contract is empty",
					name)
			}
			decoders = append(decoders,
				reflector.NewDecoder(reflector.VariantCEX, oracle.Reflector.CEXContract))
		case reflector.SourceFX:
			if oracle.Reflector.FXContract == "" {
				return nil, fmt.Errorf(
					"source %q enabled but oracle.reflector.fx_contract is empty",
					name)
			}
			decoders = append(decoders,
				reflector.NewDecoder(reflector.VariantFX, oracle.Reflector.FXContract))
		case sdex.SourceName:
			opDecoders = append(opDecoders, sdex.NewDecoder())
		default:
			return nil, fmt.Errorf("unknown source %q in ingestion.enabled_sources — check internal/sources/", name)
		}
	}
	disp := dispatcher.New(decoders...)
	for _, od := range opDecoders {
		disp.AddOpDecoder(od)
	}
	return disp, nil
}

// ─── Ledger processing ─────────────────────────────────────────

// processAndPersist is invoked by ledgerstream for each received
// LedgerCloseMeta. Runs the dispatcher, forwards outputs to the
// sink channel, and persists the last-processed cursor after
// successful emission.
//
// Returns a non-nil error only if the context is canceled mid-
// ledger (ledgerstream treats that as shutdown). Per-event decode
// errors are absorbed by the dispatcher.
func processAndPersist(
	ctx context.Context,
	disp *dispatcher.Dispatcher,
	events chan<- consumer.Event,
	store *timescale.Store,
	logger *slog.Logger,
	lcm sdkxdr.LedgerCloseMeta,
	networkPassphrase string,
) error {
	outputs, err := disp.ProcessLedger(lcm, networkPassphrase)
	if err != nil {
		// Hard structural error (bad LCM) — log + keep going so a
		// single malformed ledger doesn't abort the whole
		// pipeline. The ledgerstream retry layer will eventually
		// surface persistent failures via its own error channel.
		logger.Warn("dispatcher rejected ledger",
			"ledger", lcm.LedgerSequence(),
			"err", err,
		)
		return nil
	}
	for _, ev := range outputs {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case events <- ev:
		}
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

// ─── Config → ledgerstream ──────────────────────────────────────

// ledgerstreamConfig builds a ledgerstream.Config from our TOML
// config. Only S3/MinIO is wired today; Filesystem is reserved
// for tests, GCS for a hypothetical cloud deploy.
func ledgerstreamConfig(cfg config.Config) ledgerstream.Config {
	return ledgerstream.Config{
		DataStore: datastore.DataStoreConfig{
			Type: "S3",
			Params: map[string]string{
				"destination_bucket_path": cfg.Storage.S3BucketLive,
				"region":                  cfg.Storage.S3Region,
				"endpoint_url":            cfg.Storage.S3Endpoint,
			},
			NetworkPassphrase: cfg.Stellar.Network,
			Compression:       "zstd",
		},
	}
}

// ─── Metrics + sink — unchanged from prior revision ─────────────

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

// persistEvents is the event-sink loop. Writes each dispatcher
// output to the right hypertable. Every accepted event increments
// per-source Prometheus counters.
func persistEvents(ctx context.Context, logger *slog.Logger, store *timescale.Store, in <-chan consumer.Event) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-in:
			if !ok {
				return
			}
			handleOneEvent(ctx, logger, store, ev)
		}
	}
}

// handleOneEvent dispatches one event to its hypertable insert.
// Panic recovery keeps the sink alive when a single malformed
// Amount would otherwise crash the SQL driver — the source-level
// decoder error metric has already counted the upstream event.
func handleOneEvent(ctx context.Context, logger *slog.Logger, store *timescale.Store, ev consumer.Event) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("panic in event sink — recovered",
				"panic", fmt.Sprintf("%v", r),
				"kind", ev.EventKind(),
				"source", ev.Source())
			obs.SourceInsertErrorsTotal.WithLabelValues(ev.Source(), "panic").Inc()
		}
	}()

	source := ev.Source()
	if source == "" {
		logger.Warn("event with empty source", "kind", ev.EventKind())
		source = "_unknown"
	}
	obs.SourceEventsTotal.WithLabelValues(source).Inc()
	obs.SourceLastEventUnix.WithLabelValues(source).Set(float64(time.Now().Unix()))

	switch e := ev.(type) {
	case soroswap.TradeEvent:
		persistTrade(ctx, logger, store, e.Trade)
	case aquarius.TradeEvent:
		persistTrade(ctx, logger, store, e.Trade)
	case phoenix.TradeEvent:
		persistTrade(ctx, logger, store, e.Trade)
	case sdex.TradeEvent:
		persistTrade(ctx, logger, store, e.Trade)
	case reflector.UpdateEvent:
		persistOracle(ctx, logger, store, e.Update)
	default:
		// A source emitted an event type the sink doesn't know how
		// to persist. Usually means a new source was registered in
		// buildDispatcher but the type-switch wasn't updated in
		// lock-step. Count + log — silent drops would otherwise
		// look like "metrics say we're ingesting but the tables
		// stay empty" from the operator's POV.
		obs.SourceInsertErrorsTotal.WithLabelValues(source, "unhandled").Inc()
		logger.Warn("unhandled event kind",
			"kind", ev.EventKind(),
			"source", source)
	}
}

func persistTrade(ctx context.Context, logger *slog.Logger, store *timescale.Store, t canonical.Trade) {
	if err := store.InsertTrade(ctx, t); err != nil {
		obs.SourceInsertErrorsTotal.WithLabelValues(t.Source, "trade").Inc()
		logger.Error("insert trade failed",
			"source", t.Source,
			"ledger", t.Ledger,
			"tx_hash", t.TxHash,
			"op_index", t.OpIndex,
			"err", err,
		)
		return
	}
	logger.Debug("trade ingested",
		"source", t.Source,
		"ledger", t.Ledger,
		"pair", t.Pair.String(),
	)
}

func persistOracle(ctx context.Context, logger *slog.Logger, store *timescale.Store, u canonical.OracleUpdate) {
	if err := store.InsertOracleUpdate(ctx, u); err != nil {
		obs.SourceInsertErrorsTotal.WithLabelValues(u.Source, "oracle").Inc()
		logger.Error("insert oracle update failed",
			"source", u.Source,
			"ledger", u.Ledger,
			"tx_hash", u.TxHash,
			"op_index", u.OpIndex,
			"asset", u.Asset.String(),
			"err", err,
		)
		return
	}
	obs.OracleLastUpdateUnix.WithLabelValues(u.Source, u.Asset.String()).
		Set(float64(u.Timestamp.Unix()))
	logger.Debug("oracle update ingested",
		"source", u.Source,
		"ledger", u.Ledger,
		"asset", u.Asset.String(),
		"price", u.Price.String(),
		"decimals", u.Decimals,
	)
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
