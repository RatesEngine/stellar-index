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

// projectorReplay rewinds the projector's per-source cursor so the
// projector goroutine re-projects a historical range from
// `soroban_events`. Replaces the family of `*-backfill` subcommands
// (cctp-backfill, rozo-backfill, soroswap-skim-backfill,
// comet-liquidity-backfill, phoenix-backfill, blend-backfill,
// sep41-transfers-backfill, drain-cascade-window) per ADR-0032 Phase 5.
//
// Mechanism:
//   - Read the projector's per-source cursor: (projector, <name>).
//   - If the requested `-from` is less than the current cursor,
//     rewind it. The projector's next cycle picks up at that lower
//     bound and tails forward to the live tip.
//   - If `-from` is already at or below the cursor, no-op (operator
//     is asking for ground that's already been re-walked).
//   - `INSERT … ON CONFLICT DO NOTHING` in every per-source table
//     makes the re-walk idempotent.
//
// This is intentionally a one-shot SQL operation: the projector
// goroutine in `ratesengine-indexer` does the actual work. Operators
// don't need a dedicated subprocess for replay because the
// projector is already in steady-state. See
// docs/operations/runbooks/projector-replay.md.
func projectorReplay(args []string) error {
	fs := flag.NewFlagSet("projector-replay", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "Path to TOML config file (required)")
	source := fs.String("source", "", "Projector source name to rewind (required); see internal/projector/registry.go for the list")
	from := fs.Uint("from", 0, "Rewind the projector cursor to this ledger (inclusive); the projector tails forward to the live tip from here")
	dryRun := fs.Bool("dry-run", false, "Print intended rewind without writing the cursor")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *cfgPath == "" || *source == "" || *from == 0 {
		return errors.New("-config, -source, and -from are required")
	}

	cfg, err := config.LoadWithEnv(*cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := timescale.Open(ctx, cfg.Storage.PostgresDSN)
	if err != nil {
		return fmt.Errorf("open postgres: %w", err)
	}
	defer func() { _ = store.Close() }()

	cursor, err := store.GetCursor(ctx, "projector", *source)
	if err != nil && !errors.Is(err, timescale.ErrNotFound) {
		return fmt.Errorf("read projector cursor: %w", err)
	}
	currentLedger := uint32(0)
	if err == nil {
		currentLedger = cursor.LastLedger
	}
	target := uint32(*from)
	if target == 0 {
		return fmt.Errorf("invalid -from %d", *from)
	}
	if target >= currentLedger {
		_, _ = fmt.Fprintf(os.Stdout,
			"projector cursor for source=%q is already at ledger %d ≤ requested rewind point %d — no action.\n",
			*source, currentLedger, target)
		return nil
	}

	_, _ = fmt.Fprintf(os.Stdout,
		"rewind projector cursor source=%q from %d → %d (delta = %d ledgers)\n",
		*source, currentLedger, target, currentLedger-target)
	// Projector cursor is "last fully-processed ledger." Rewinding
	// to (target - 1) makes the projector start its next cycle at
	// `target` inclusive (see projector.cycleOneSource:fromLedger =
	// cursor.LastLedger + 1).
	rewindTo := target
	if rewindTo > 0 {
		rewindTo--
	}
	if *dryRun {
		_, _ = fmt.Fprintf(os.Stdout,
			"dry-run: would UpsertCursor(projector, %q, %d)\n", *source, rewindTo)
		return nil
	}
	if err := store.UpsertCursor(ctx, "projector", *source, rewindTo); err != nil {
		return fmt.Errorf("rewind cursor: %w", err)
	}
	_, _ = fmt.Fprintf(os.Stdout,
		"projector cursor rewound — next projector cycle (≤ 5s) will start re-projecting from ledger %d\n",
		target)
	return nil
}
