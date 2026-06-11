package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	sdkxdr "github.com/stellar/go-stellar-sdk/xdr"

	"github.com/RatesEngine/rates-engine/internal/config"
	"github.com/RatesEngine/rates-engine/internal/dispatcher"
	"github.com/RatesEngine/rates-engine/internal/ledgerstream"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// censusBackfill populates the ledger_ingest_log substrate record for
// a historical ledger range (ADR-0033 Phase 2). The live indexer
// writes this record going forward; this subcommand walks Galexie
// metadata for [from, to] and writes the decoder-independent census
// (soroban_event_count + classic_trade_effect_count) plus the
// hash-chain anchors for ledgers that predate live capture.
//
// No decoder runs — this is a pure structural walk (dispatcher.
// CensusLedger), so it is fast and safe to re-run: UpsertLedgerIngestLog
// is ON CONFLICT DO UPDATE, so overlapping ranges and re-runs converge.
//
// Resume: checkpoints into ingestion_cursors as
// (source='census-backfill', sub_source='<from>-<to>'). Re-running the
// same -from/-to resumes from the last processed ledger. Restart-safe.
func censusBackfill(args []string) error { //nolint:gocognit,gocyclo,funlen // linear walk + checkpoint loop; splitting reduces clarity (same as backfillRouter).
	fs := flag.NewFlagSet("census-backfill", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "Path to TOML config file (required)")
	from := fs.Uint("from", 0, "First ledger sequence (inclusive, required)")
	to := fs.Uint("to", 0, "Last ledger sequence (inclusive, required)")
	resume := fs.Bool("resume", true, "Resume from saved cursor if a checkpoint exists for this from/to pair")
	bucket := fs.String("bucket", "", "Override storage bucket (default cfg.Storage.S3BucketLive)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *cfgPath == "" || *from == 0 || *to == 0 || *to < *from {
		return fmt.Errorf("-config, -from, -to are required; -to must be >= -from")
	}

	cfg, err := config.LoadWithEnv(*cfgPath)
	if err != nil {
		return err
	}

	ctx, cancel := signalContext()
	defer cancel()

	store, err := timescale.Open(ctx, cfg.Storage.PostgresDSN)
	if err != nil {
		return fmt.Errorf("storage open: %w", err)
	}
	defer func() { _ = store.Close() }()

	cursorSrc := "census-backfill"
	cursorSub := fmt.Sprintf("%d-%d", *from, *to)
	startLedger := uint32(*from)
	if *resume {
		prior, gerr := store.GetCursor(ctx, cursorSrc, cursorSub)
		if gerr == nil && prior.LastLedger >= uint32(*from) {
			startLedger = prior.LastLedger + 1
			fmt.Fprintf(os.Stderr, "census-backfill: resuming at ledger %d (prior last_ledger=%d)\n",
				startLedger, prior.LastLedger)
		} else if gerr != nil && !errors.Is(gerr, timescale.ErrNotFound) {
			fmt.Fprintf(os.Stderr, "census-backfill: read prior cursor failed (%v) — starting from -from\n", gerr)
		}
	}
	if startLedger > uint32(*to) {
		fmt.Fprintf(os.Stderr, "census-backfill: cursor already at or past -to (%d ≥ %d) — nothing to do\n",
			startLedger, *to)
		return nil
	}

	streamBucket := cfg.Storage.S3BucketLive
	if *bucket != "" {
		streamBucket = *bucket
	}
	lsCfg := newBoundedLedgerStreamConfig(cfg, streamBucket)
	passphrase := cfg.Stellar.Passphrase()

	fmt.Fprintf(os.Stderr, "census-backfill: streaming ledgers %d..%d from bucket %q\n",
		startLedger, *to, streamBucket)

	var (
		total          int
		skipped        int
		lastProcessed  uint32
		lastCheckpoint = time.Now()
	)
	const checkpointInterval = 30 * time.Second

	checkpoint := func(seq uint32) {
		if err := store.UpsertCursor(ctx, cursorSrc, cursorSub, seq); err != nil {
			fmt.Fprintf(os.Stderr, "census-backfill: checkpoint at %d failed: %v\n", seq, err)
		}
	}

	walkErr := ledgerstream.Stream(ctx, lsCfg, startLedger, uint32(*to),
		func(lcm sdkxdr.LedgerCloseMeta) error {
			total++
			census, cerr := dispatcher.CensusLedger(lcm, passphrase)
			if cerr != nil {
				fmt.Fprintf(os.Stderr, "census-backfill: ledger %d census: %v\n", lcm.LedgerSequence(), cerr)
				return nil
			}
			// A ledger we could not fully read (a malformed tx, or a tx
			// whose GetTransactionEvents failed — e.g. an unsupported
			// future meta version) must NOT get an authoritative
			// "complete" substrate row: its SorobanEventCount undercounts,
			// so a projection reconcile against it would falsely pass
			// (G15-06). Skip it; a later re-run on a fixed reader writes
			// the real row.
			if census.TxReadErrors > 0 || census.TxEventReadErrors > 0 {
				skipped++
				fmt.Fprintf(os.Stderr, "census-backfill: ledger %d had %d tx read errors + %d tx-event read errors; skipping substrate row\n",
					census.LedgerSeq, census.TxReadErrors, census.TxEventReadErrors)
				return nil
			}
			row := timescale.LedgerIngestRow{
				LedgerSeq:               census.LedgerSeq,
				LedgerCloseTime:         census.LedgerCloseTime,
				LedgerHash:              census.LedgerHash[:],
				PrevLedgerHash:          census.PrevLedgerHash[:],
				SorobanEventCount:       census.SorobanEventCount,
				ClassicTradeEffectCount: census.ClassicTradeEffectCount,
			}
			if ierr := store.UpsertLedgerIngestLog(ctx, row); ierr != nil {
				fmt.Fprintf(os.Stderr, "census-backfill: upsert ledger %d: %v\n", census.LedgerSeq, ierr)
				return nil
			}
			lastProcessed = census.LedgerSeq
			if time.Since(lastCheckpoint) >= checkpointInterval {
				checkpoint(lastProcessed)
				lastCheckpoint = time.Now()
				fmt.Fprintf(os.Stderr, "census-backfill: %d ledgers processed (at %d, %d skipped)\n",
					total, lastProcessed, skipped)
			}
			return nil
		},
	)

	// Flush a final checkpoint at the last ledger we actually wrote, so
	// a resume picks up exactly after it (whether we finished or were
	// interrupted / hit an archive gap).
	if lastProcessed > 0 {
		checkpoint(lastProcessed)
	}

	if walkErr != nil && !errors.Is(walkErr, context.Canceled) {
		return fmt.Errorf("census-backfill stream (last processed %d): %w", lastProcessed, walkErr)
	}
	fmt.Fprintf(os.Stderr, "census-backfill: done — %d ledgers processed, %d skipped, last %d\n",
		total, skipped, lastProcessed)
	return nil
}
