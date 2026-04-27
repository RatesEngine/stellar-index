package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	sdkxdr "github.com/stellar/go-stellar-sdk/xdr"

	"github.com/RatesEngine/rates-engine/internal/config"
	"github.com/RatesEngine/rates-engine/internal/consumer"
	"github.com/RatesEngine/rates-engine/internal/ledgerstream"
	"github.com/RatesEngine/rates-engine/internal/pipeline"
	"github.com/RatesEngine/rates-engine/internal/sources/external"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// ─── ratesengine-ops backfill ──────────────────────────────────
//
// Replays a bounded ledger range through the same dispatcher +
// decoder + sink path the live indexer uses, producing trade rows
// into the trades hypertable. CAGGs (1m / 15m / 1h / 4h / 1d / 1w /
// 1mo per migration 0002) auto-materialise on the inserted rows.
//
// Differs from the indexer in three load-bearing ways:
//
//  1. Bounded range [-from, -to]. ledgerstream.Stream exits at -to;
//     no live tail. Backfill is a one-shot operation.
//  2. No cursor row written. The indexer's `ledgerstream` cursor
//     drives "resume from cursor+1" on restart. Backfill has its
//     own explicit -from; if it crashed and were to share that
//     cursor, the indexer would mis-resume from a historical
//     ledger on its next start.
//  3. BackfillSafe gate. internal/sources/external.Registry marks
//     every on-chain Soroban source `BackfillSafe=false` until its
//     decoder has been audited against every WASM version that ran
//     for the replay range (CLAUDE.md "Soroban DeFi contracts
//     upgrade in place"). Backfill refuses to run an unsafe source.
//
// Trade-row idempotency is the storage layer's responsibility — the
// trades hypertable's unique index on (source, ledger, tx_hash,
// op_index) makes re-running over the same range a no-op rather
// than producing duplicates. Aggregator CAGGs recompute from the
// underlying rows so duplicate suppression at insert time is
// sufficient.

// backfillOpts holds the parsed + validated CLI inputs. Pulled out
// of the entry point so flag-parsing + validation are unit-testable
// without executing the pipeline.
type backfillOpts struct {
	cfgPath string
	from    uint32
	to      uint32
	sources []string // resolved: -source override or cfg.Ingestion.EnabledSources
	bucket  string   // resolved: -bucket override or cfg.Storage.S3BucketArchive
	dryRun  bool
}

func backfill(args []string) error {
	opts, cfg, err := parseBackfillFlags(args)
	if err != nil {
		return err
	}

	if opts.dryRun {
		_, _ = fmt.Fprintf(os.Stderr,
			"backfill dry-run:\n  range:   [%d, %d] (%d ledgers)\n  sources: %v\n  bucket:  %s\n",
			opts.from, opts.to, opts.to-opts.from+1, opts.sources, opts.bucket)
		return nil
	}

	rootCtx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	logger := mkBackfillLogger()
	logger.Info("backfill starting",
		"from", opts.from,
		"to", opts.to,
		"sources", opts.sources,
		"bucket", opts.bucket,
	)

	store, err := timescale.Open(rootCtx, cfg.Storage.PostgresDSN)
	if err != nil {
		return fmt.Errorf("storage: %w", err)
	}
	defer func() { _ = store.Close() }()

	disp, err := pipeline.BuildDispatcher(opts.sources, cfg.Oracle)
	if err != nil {
		return fmt.Errorf("build dispatcher: %w", err)
	}

	events := make(chan consumer.Event, 256)
	sinkDone := make(chan struct{})
	go func() {
		defer close(sinkDone)
		pipeline.PersistEvents(rootCtx, logger, store, events)
	}()

	streamCfg := pipeline.LedgerstreamConfig(cfg, opts.bucket)
	streamErr := ledgerstream.Stream(rootCtx, streamCfg, opts.from, opts.to,
		func(lcm sdkxdr.LedgerCloseMeta) error {
			return pipeline.ProcessLedger(rootCtx, disp, events, logger, lcm, cfg.Stellar.Passphrase())
		},
	)

	close(events)
	<-sinkDone

	if streamErr != nil && !errors.Is(streamErr, context.Canceled) {
		return fmt.Errorf("stream: %w", streamErr)
	}

	logger.Info("backfill complete",
		"from", opts.from,
		"to", opts.to,
		"ledgers", opts.to-opts.from+1,
	)
	return nil
}

// parseBackfillFlags parses CLI args, loads config, and validates the
// result. Returns the resolved opts + the loaded config so the entry
// point can wire them up. Returns a non-nil error on any validation
// failure, including the BackfillSafe gate.
//
// Split out from backfill() so unit tests can drive validation
// without spinning up postgres + galexie.
func parseBackfillFlags(args []string) (backfillOpts, config.Config, error) {
	var opts backfillOpts
	var cfg config.Config

	fs := flag.NewFlagSet("backfill", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "path to ratesengine.toml (required)")
	from := fs.Uint("from", 0, "starting ledger sequence (inclusive, required)")
	to := fs.Uint("to", 0, "ending ledger sequence (inclusive, required)")
	sourceCSV := fs.String("source", "",
		"comma-separated source names; default = cfg.Ingestion.EnabledSources")
	bucketOverride := fs.String("bucket", "",
		"galexie bucket override; default = cfg.Storage.S3BucketArchive")
	dryRun := fs.Bool("dry-run", false,
		"validate config + sources + range, then exit without running")
	if err := fs.Parse(args); err != nil {
		return opts, cfg, err
	}

	if *cfgPath == "" {
		return opts, cfg, errors.New("-config required")
	}
	if *from == 0 {
		return opts, cfg, errors.New("-from must be > 0 (refuse to default to genesis)")
	}
	if *to <= *from {
		return opts, cfg, fmt.Errorf("-to (%d) must be > -from (%d)", *to, *from)
	}

	loaded, err := config.LoadWithEnv(*cfgPath)
	if err != nil {
		return opts, cfg, fmt.Errorf("load config: %w", err)
	}
	cfg = loaded

	sources := cfg.Ingestion.EnabledSources
	if *sourceCSV != "" {
		sources = splitCSV(*sourceCSV)
	}
	if len(sources) == 0 {
		return opts, cfg, errors.New("no sources to backfill — set -source or cfg.Ingestion.EnabledSources")
	}

	if unsafeSources := unsafeBackfillSources(sources); len(unsafeSources) > 0 {
		return opts, cfg, fmt.Errorf(
			"refusing to backfill — sources not BackfillSafe (per-WASM-hash audit pending): %v; "+
				"run ratesengine-ops wasm-history -from %d -to %d -contracts <CID> for each on-chain source, "+
				"review every emitted WASM hash against the current decoder, then flip BackfillSafe=true in "+
				"internal/sources/external/registry.go in the same PR (see CLAUDE.md \"Soroban DeFi contracts "+
				"upgrade in place\")",
			unsafeSources, *from, *to)
	}

	bucket := cfg.Storage.S3BucketArchive
	if *bucketOverride != "" {
		bucket = *bucketOverride
	}
	if bucket == "" {
		return opts, cfg, errors.New("no bucket — set -bucket or cfg.Storage.S3BucketArchive")
	}

	opts = backfillOpts{
		cfgPath: *cfgPath,
		from:    uint32(*from),
		to:      uint32(*to),
		sources: sources,
		bucket:  bucket,
		dryRun:  *dryRun,
	}
	return opts, cfg, nil
}

// unsafeBackfillSources filters `sources` to those whose registry
// entry has BackfillSafe=false. The intent is to fail fast on a list
// the operator can paste into a wasm-history audit ticket.
func unsafeBackfillSources(sources []string) []string {
	var out []string
	for _, s := range sources {
		if !external.BackfillSafe(s) {
			out = append(out, s)
		}
	}
	return out
}

// mkBackfillLogger returns a slog.Logger configured for one-shot
// ops invocations: text format on stderr, info level. Verbose
// debugging is rare — operators typically run backfill from a tmux
// session and want readable progress lines, not structured JSON.
func mkBackfillLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
}

// splitCSV (used here for -source parsing) is defined in
// cross_region_check.go — kept there so this binary has one
// canonical comma-splitting helper across subcommands.
