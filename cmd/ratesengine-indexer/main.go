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
// Current scope: this is a thin end-to-end wiring, not the final
// orchestrator. Full orchestration (per-source restart backoffs,
// cursor-driven backfill bootstrap, Prometheus metrics, health
// endpoint) lands in a follow-up PR that extracts
// internal/consumer/orchestrator.go.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/config"
	"github.com/RatesEngine/rates-engine/internal/consumer"
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
	// Run each source's StreamLive in its own goroutine, draining
	// events into the store. Very minimal: no backfill-on-gap yet,
	// no per-source restart backoffs, no metrics. This is the
	// "binary starts and does something useful" milestone.
	out := make(chan consumer.Event, 1024)

	var wg sync.WaitGroup
	for _, src := range sources {
		src := src // capture
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := src.StreamLive(rootCtx, out)
			if err != nil && rootCtx.Err() == nil {
				logger.Error("source stream ended with error", "source", src.Name(), "err", err)
			}
		}()
	}

	// Consumer goroutine: pull events, persist, loop until ctx done.
	wg.Add(1)
	go func() {
		defer wg.Done()
		persistEvents(rootCtx, logger, store, out)
	}()

	// Wait for shutdown signal.
	<-rootCtx.Done()
	logger.Info("shutdown signal received — draining for up to 30s")

	shutdownCtx, stopDrain := context.WithTimeout(context.Background(), 30*time.Second)
	defer stopDrain()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		logger.Info("clean shutdown")
	case <-shutdownCtx.Done():
		logger.Warn("drain timeout exceeded — hard exit")
	}
	return nil
}

// buildSources constructs a Source per configured name. Unknown
// names are a fatal config error.
func buildSources(names []string, rpc *stellarrpc.Client) ([]consumer.Source, error) {
	var out []consumer.Source
	for _, name := range names {
		switch strings.ToLower(name) {
		case soroswap.SourceName:
			out = append(out, soroswap.New(rpc))
		// TODO(#0): aquarius, phoenix, comet, blend, sdex, reflector,
		// redstone, band, cex-*, fx-*. Each adds one case here.
		default:
			return nil, fmt.Errorf("unknown source %q in ingestion.enabled_sources — check internal/sources/", name)
		}
	}
	return out, nil
}

// persistEvents is the event-sink loop. Writes Trade events to the
// trades hypertable; logs unknown event kinds as a soft warning.
func persistEvents(ctx context.Context, logger *slog.Logger, store *timescale.Store, in <-chan consumer.Event) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-in:
			switch e := ev.(type) {
			case soroswap.TradeEvent:
				persistTrade(ctx, logger, store, e.Trade)
			default:
				logger.Warn("unhandled event kind", "kind", ev.EventKind())
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
