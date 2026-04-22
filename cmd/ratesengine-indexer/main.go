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
// internal/config/load.go ApplyEnvOverrides). Logging is JSON-
// formatted via slog at the level configured in [obs] section.
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
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	cfg.ApplyEnvOverrides()

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
	sources, err := buildSources(cfg.Ingestion.EnabledSources, rpc)
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
// names are a fatal config error.
func buildSources(names []string, rpc *stellarrpc.Client) ([]consumer.Source, error) {
	var out []consumer.Source
	for _, name := range names {
		switch strings.ToLower(name) {
		case soroswap.SourceName:
			out = append(out, soroswap.New(rpc))
		case aquarius.SourceName:
			out = append(out, aquarius.New(rpc))
		case phoenix.SourceName:
			out = append(out, phoenix.New(rpc))
		// TODO(#0): comet, blend, sdex, reflector, redstone, band,
		// cex-*, fx-*. Each adds one case here.
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
		case ev := <-in:
			// Metric emission happens before the type-switch so the
			// counter reflects every event the orchestrator emitted,
			// even ones we couldn't type-dispatch (which shouldn't
			// happen but guards against future-source mis-wiring).
			source := ev.Source()
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
				logger.Warn("unhandled event kind",
					"kind", ev.EventKind(),
					"source", source)
			}
		}
	}
}

func persistTrade(ctx context.Context, logger *slog.Logger, store *timescale.Store, t canonical.Trade) {
	if err := store.InsertTrade(ctx, t); err != nil {
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
