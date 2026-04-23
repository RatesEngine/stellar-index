// Binary ratesengine-indexer runs the ingestion fleet: one goroutine
// per configured source, each feeding its events into the trades
// hypertable via internal/storage/timescale.
//
// Flags:
//
//	-config PATH    TOML config file (required)
//	-dry-run        Load config, open connections, validate, exit.
//	                No events consumed. Useful for boot sanity checks.
//
// Environment overrides for secrets apply on top of the file (see
// internal/config/load.go LoadWithEnv). Logging is JSON-formatted
// via slog at the level configured in [obs] section.
//
// Graceful shutdown: SIGINT + SIGTERM trigger context cancellation;
// the binary waits up to 30 s for in-flight work to finish before
// hard-exiting.
//
// Orchestration lives in internal/consumer/orchestrator.go — this
// binary is a thin launcher that wires config + storage + RPC +
// sources together and hands them to the Orchestrator.
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

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/config"
	"github.com/RatesEngine/rates-engine/internal/consumer"
	"github.com/RatesEngine/rates-engine/internal/obs"
	"github.com/RatesEngine/rates-engine/internal/sources/aquarius"
	"github.com/RatesEngine/rates-engine/internal/sources/phoenix"
	"github.com/RatesEngine/rates-engine/internal/sources/reflector"
	"github.com/RatesEngine/rates-engine/internal/sources/soroswap"
	"github.com/RatesEngine/rates-engine/internal/stellarrpc"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
	"github.com/RatesEngine/rates-engine/internal/version"
)

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

	// Root context with SIGINT/SIGTERM cancel.
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

	// ─── RPC client ─────────────────────────────────────────────
	// Pick the first endpoint; retries + round-robin are a follow-up.
	if len(cfg.Stellar.RPCEndpoints) == 0 {
		return fmt.Errorf("stellar.rpc_endpoints is empty — nothing to ingest from")
	}
	rpc := stellarrpc.New(cfg.Stellar.RPCEndpoints[0], stellarrpc.WithTimeout(30*time.Second))

	vi, err := rpc.VersionInfo(rootCtx)
	if err != nil {
		// Under normal run, orchestrator retry+backoff handles
		// transient RPC outages, so a boot-time failure is a soft
		// warning — the ingester may recover without operator action.
		// Under -dry-run the operator is explicitly asking "does this
		// config work?" — answering "yes" when RPC is unreachable is
		// a lie. Fail hard there.
		if dryRun {
			return fmt.Errorf("rpc: version probe failed: %w", err)
		}
		logger.Warn("rpc version probe failed (continuing)", "err", err)
	} else {
		logger.Info("rpc reachable",
			"endpoint", rpc.Endpoint(),
			"rpc_version", vi.Version,
			"captive_core", vi.CaptiveCoreVersion,
			"protocol", vi.ProtocolVersion,
		)
	}

	// ─── Source registry ────────────────────────────────────────
	sources, err := buildSources(cfg.Ingestion.EnabledSources, rpc, cfg.Oracle)
	if err != nil {
		return err
	}
	if len(sources) == 0 {
		return fmt.Errorf("no sources enabled in ingestion.enabled_sources")
	}
	for _, s := range sources {
		logger.Info("source registered", "name", s.Name())
	}

	if dryRun {
		logger.Info("dry-run complete — exiting")
		return nil
	}

	// ─── Metrics HTTP endpoint ──────────────────────────────────
	// The indexer emits SourceEvents, SourceLag, CursorLastLedger,
	// SourceDecode/Orphan/InsertErrors, OracleLastUpdate, etc. —
	// every ingestion alert in deploy/monitoring/rules/ depends
	// on Prometheus being able to scrape these. Without this
	// server the alerts' metric expressions always return "no
	// data" and never fire.
	var metricsSrv *http.Server
	if cfg.Obs.MetricsListen != "" {
		mux := http.NewServeMux()
		mux.Handle("GET /metrics", obs.Handler())
		mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok\n"))
		})
		metricsSrv = &http.Server{
			Addr:              cfg.Obs.MetricsListen,
			Handler:           mux,
			ReadHeaderTimeout: 5 * time.Second,
		}
		go func() {
			logger.Info("metrics endpoint listening", "addr", cfg.Obs.MetricsListen)
			if err := metricsSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				logger.Error("metrics server exited", "err", err)
			}
		}()
	} else {
		logger.Warn("obs.metrics_listen is empty — /metrics endpoint disabled; Prometheus alerts on source metrics will not fire")
	}

	// ─── Orchestration ──────────────────────────────────────────
	// consumer.Orchestrator runs each source in its own goroutine
	// with restart-backoff + periodic cursor persistence.
	orch := consumer.New(
		cursorAdapter{store},
		sources,
		consumer.Config{
			BackfillFromLedger: cfg.Ingestion.BackfillFromLedger,
		},
		logger,
	)

	orchDone := make(chan error, 1)
	go func() { orchDone <- orch.Run(rootCtx) }()

	// Consumer goroutine: pull events off the orchestrator, persist.
	sinkDone := make(chan struct{})
	go func() {
		defer close(sinkDone)
		persistEvents(rootCtx, logger, store, orch.Events())
	}()

	// Wait for shutdown signal OR orchestrator exit.
	select {
	case <-rootCtx.Done():
		logger.Info("shutdown signal received — draining for up to 30s")
	case err := <-orchDone:
		if err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("orchestrator exited with error", "err", err)
			return err
		}
	}

	shutdownCtx, stopDrain := context.WithTimeout(context.Background(), 30*time.Second)
	defer stopDrain()

	if metricsSrv != nil {
		// Stop /metrics first so Prometheus's `up{job="indexer"}`
		// goes to 0 promptly (k8s pod-termination is expected to
		// look offline — better than staying up while the sink
		// drain holds it). Shutdown is synchronous; any in-flight
		// scrape either completes or is aborted at shutdownCtx
		// expiry, which is also the sink-drain budget.
		if err := metricsSrv.Shutdown(shutdownCtx); err != nil {
			logger.Warn("metrics server shutdown", "err", err)
		}
	}

	select {
	case <-sinkDone:
		logger.Info("clean shutdown")
	case <-shutdownCtx.Done():
		logger.Warn("drain timeout exceeded — hard exit")
	}
	return nil
}

// cursorAdapter bridges *timescale.Store and consumer.CursorStore.
// Translates timescale.ErrNotFound → consumer.ErrNoCursor and
// converts between the twin Cursor shapes.
type cursorAdapter struct{ s *timescale.Store }

func (a cursorAdapter) GetCursor(ctx context.Context, source, sub string) (consumer.Cursor, error) {
	c, err := a.s.GetCursor(ctx, source, sub)
	if errors.Is(err, timescale.ErrNotFound) {
		return consumer.Cursor{}, consumer.ErrNoCursor
	}
	if err != nil {
		return consumer.Cursor{}, err
	}
	return consumer.Cursor{
		Source: c.Source, Sub: c.Sub,
		LastLedger: c.LastLedger, UpdatedAt: c.UpdatedAt,
	}, nil
}

func (a cursorAdapter) UpsertCursor(ctx context.Context, source, sub string, lastLedger uint32) error {
	return a.s.UpsertCursor(ctx, source, sub, lastLedger)
}

// buildSources constructs a Source per configured name. Unknown
// names are a fatal config error. Sources that require per-source
// config (Reflector's contract addresses, future CEX/FX creds)
// read from `oracle` (and future `cex`, `fx` config sections);
// missing required fields are also fatal at startup.
func buildSources(names []string, rpc *stellarrpc.Client, oracle config.OracleConfig) ([]consumer.Source, error) {
	var out []consumer.Source
	for _, name := range names {
		switch strings.ToLower(name) {
		case soroswap.SourceName:
			out = append(out, soroswap.New(rpc))
		case aquarius.SourceName:
			out = append(out, aquarius.New(rpc))
		case phoenix.SourceName:
			out = append(out, phoenix.New(rpc))

		case reflector.SourceDEX:
			if oracle.Reflector.DEXContract == "" {
				return nil, fmt.Errorf(
					"source %q enabled but oracle.reflector.dex_contract is empty — see docs/discovery/oracles/reflector.md",
					name)
			}
			out = append(out, reflector.NewDEX(rpc, oracle.Reflector.DEXContract))
		case reflector.SourceCEX:
			if oracle.Reflector.CEXContract == "" {
				return nil, fmt.Errorf(
					"source %q enabled but oracle.reflector.cex_contract is empty — see docs/discovery/oracles/reflector.md",
					name)
			}
			out = append(out, reflector.NewCEX(rpc, oracle.Reflector.CEXContract))
		case reflector.SourceFX:
			if oracle.Reflector.FXContract == "" {
				return nil, fmt.Errorf(
					"source %q enabled but oracle.reflector.fx_contract is empty — see docs/discovery/oracles/reflector.md",
					name)
			}
			out = append(out, reflector.NewFX(rpc, oracle.Reflector.FXContract))

		// TODO(#0): comet, blend, sdex, redstone, band, cex-*, fx-*.
		default:
			return nil, fmt.Errorf("unknown source %q in ingestion.enabled_sources — check internal/sources/", name)
		}
	}
	return out, nil
}

// persistEvents is the event-sink loop. Writes Trade events to the
// trades hypertable; logs unknown event kinds as a soft warning.
// Every accepted event increments per-source Prometheus counters.
func persistEvents(ctx context.Context, logger *slog.Logger, store *timescale.Store, in <-chan consumer.Event) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-in:
			if !ok {
				// Events channel closed — orchestrator is shutting
				// down. Exit cleanly rather than looping on nil events.
				return
			}
			handleOneEvent(ctx, logger, store, ev)
		}
	}
}

// handleOneEvent processes one event with panic recovery. A panic
// in a single event (e.g. a malformed Amount that blows up in the
// SQL driver) must not take down the sink. The source sending the
// event has its own decoder-error metric; here we focus on keeping
// the sink alive.
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
		// A Source() returning empty is a contract violation —
		// would show up as "empty-string" label in metrics and
		// confuse dashboards. Log + attribute to a sentinel.
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
	case reflector.UpdateEvent:
		persistOracle(ctx, logger, store, e.Update)
	default:
		// A source emitted an event type this sink doesn't know how
		// to persist. Usually means a new source was registered in
		// buildSources() but the type-switch wasn't updated in
		// lock-step. Count + log so ops dashboards flag it — silent
		// drops would otherwise look like "metrics say we're ingesting
		// but the tables stay empty" from the operator's POV.
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

// persistOracle writes one OracleUpdate to the oracle_updates
// hypertable. Mirrors persistTrade for the oracle side — the
// indexer's event-sink type-switch routes each event type to its
// matching sink.
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

// mkLogger returns a slog logger at the configured level + format.
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
	return slog.New(handler).With(
		"binary", "ratesengine-indexer",
	)
}
