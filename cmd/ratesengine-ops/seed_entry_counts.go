package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/config"
	"github.com/StellarAtlas/stellar-atlas/internal/storage/timescale"
)

// seedEntryCounts authoritatively recomputes the per-source entry
// tally (source_entry_counts, migration 0035) from a full GROUP BY
// over `trades` + `oracle_updates`, overwriting the table.
//
// Why this exists: the writers (InsertTrade / InsertOracleUpdate)
// keep source_entry_counts live by bumping it atomically +
// idempotently on every NEW row. But a fresh table starts at 0 and
// only counts entries ingested SINCE the counter went live — it does
// not know about the ~60M+ rows of pre-existing history. This
// subcommand is the one-shot reconciliation: run it ONCE after the
// all-time backfill completes to fold in (a) all pre-counter history
// and (b) any O(process-crash) increment drift. Re-running is safe
// and converges (it SETs, not ADDs).
//
// When to run: AFTER the all-time backfill is complete and ingest is
// only appending at the live tip. The GROUP BY scans every `trades`
// chunk in one transaction — fine within the 4096
// max_locks_per_transaction budget (819,200-entry table) and quick
// once the disk-IO contention from the backfill is gone, but slow +
// lock-hungry if run mid-backfill. The scan window is not perfectly
// race-free against concurrent tip-ingest (a few seconds × ~150
// trades/s of in-flight rows may be over/under-counted by O(low
// thousands) out of 60M+, <0.01%); it self-corrects on the next run.
// Run during quiescence for exactness.
//
// Flags:
//
//	-config PATH   TOML config (required) — postgres DSN.
//	-timeout DUR   Wall-clock budget. Default 30m (a full trades
//	               GROUP BY over ~2,700+ chunks is minutes post-
//	               backfill; generous headroom).
func seedEntryCounts(args []string) error {
	fs := flag.NewFlagSet("seed-entry-counts", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "path to ratesengine.toml (required)")
	timeout := fs.Duration("timeout", 30*time.Minute, "wall-clock budget for the recount")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *cfgPath == "" {
		return errors.New("-config required")
	}

	cfg, err := config.LoadWithEnv(*cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	store, err := timescale.Open(ctx, cfg.Storage.PostgresDSN)
	if err != nil {
		return fmt.Errorf("storage: %w", err)
	}
	defer func() { _ = store.Close() }()

	fmt.Fprintln(os.Stderr, "seed-entry-counts: recomputing source_entry_counts from trades + oracle_updates (this scans every trades chunk — run post-backfill)…")

	n, err := store.SeedSourceEntryCounts(ctx)
	if err != nil {
		return fmt.Errorf("recount: %w", err)
	}
	fmt.Fprintf(os.Stderr, "seed-entry-counts: %d source rows reconciled\n", n)
	return nil
}
