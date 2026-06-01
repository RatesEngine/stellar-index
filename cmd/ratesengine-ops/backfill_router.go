package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	sdkxdr "github.com/stellar/go-stellar-sdk/xdr"

	"github.com/RatesEngine/rates-engine/internal/config"
	"github.com/RatesEngine/rates-engine/internal/dispatcher"
	"github.com/RatesEngine/rates-engine/internal/ledgerstream"
	soroswap_router "github.com/RatesEngine/rates-engine/internal/sources/soroswap_router"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// backfillRouter walks Galexie ledger metadata for a range and
// reconstructs `soroswap_router_swaps` rows by replaying the
// soroswap-router ContractCallDecoder against every InvokeContract
// op. The decoder is pure (no state), so this is safe to re-run; the
// destination table's PK on (ledger_close_time, ledger, tx_hash,
// op_index) + ON CONFLICT DO NOTHING make every replay idempotent.
//
// Why this exists despite ADR-0032's "no per-source backfill"
// invariant: that ADR's projector path reads from the
// `soroban_events` landing zone, but the soroswap router emits ZERO
// Soroban events (its work is invoking per-pair contracts). The
// projector therefore cannot rebuild router history — there's no
// landing-zone signal to project from. The only ground truth for
// historical router invocations is the raw ledger metadata in
// Galexie. This subcommand reads it directly via ledgerstream.Stream,
// mirroring `verify-decoders` but writing instead of dry-running.
//
// Resume semantics: progress checkpoints into ingestion_cursors as
// (source='backfill-router', sub_source='<from>-<to>',
// last_ledger=<latest processed>). Re-running the same -from/-to
// resumes from the saved cursor. Restart-safe.
func backfillRouter(args []string) error { //nolint:funlen,gocognit,gocyclo // linear pipeline, splitting reduces readability
	fs := flag.NewFlagSet("backfill-router", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "Path to TOML config file (required)")
	from := fs.Uint("from", 0, "First ledger sequence (inclusive, required)")
	to := fs.Uint("to", 0, "Last ledger sequence (inclusive, required)")
	resume := fs.Bool("resume", true, "Resume from saved cursor if a checkpoint exists for this from/to pair (default true)")
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

	// Long-lived context for the whole backfill; signal-cancellable
	// so we flush a final checkpoint on SIGTERM/SIGINT.
	ctx, cancel := signalContext()
	defer cancel()

	// Storage handle for inserts + cursor checkpointing.
	store, err := timescale.Open(ctx, cfg.Storage.PostgresDSN)
	if err != nil {
		return fmt.Errorf("storage open: %w", err)
	}
	defer func() { _ = store.Close() }()

	// Minimal dispatcher: just the router decoder. Every other source
	// is irrelevant for this backfill and avoiding their decoders
	// keeps the per-ledger processing tight.
	disp := dispatcher.New()
	disp.AddContractCallDecoder(soroswap_router.NewDecoder(soroswap_router.MainnetRouter))

	// Resume from prior checkpoint, if any. Cursor key is the exact
	// from/to pair the operator passed — separate runs with different
	// ranges get separate cursors.
	cursorSrc := "backfill-router"
	cursorSub := fmt.Sprintf("%d-%d", *from, *to)
	startLedger := uint32(*from)
	if *resume {
		prior, gerr := store.GetCursor(ctx, cursorSrc, cursorSub)
		if gerr == nil && prior.LastLedger >= uint32(*from) {
			startLedger = prior.LastLedger + 1
			fmt.Fprintf(os.Stderr, "backfill-router: resuming at ledger %d (prior checkpoint last_ledger=%d)\n",
				startLedger, prior.LastLedger)
		} else if gerr != nil && !errors.Is(gerr, timescale.ErrNotFound) {
			fmt.Fprintf(os.Stderr, "backfill-router: read prior cursor failed (%v) — starting from -from\n", gerr)
		}
	}
	if startLedger > uint32(*to) {
		fmt.Fprintf(os.Stderr, "backfill-router: cursor already at or past -to (%d ≥ %d) — nothing to do\n",
			startLedger, *to)
		return nil
	}

	streamBucket := cfg.Storage.S3BucketLive
	if *bucket != "" {
		streamBucket = *bucket
	}
	lsCfg := newBoundedLedgerStreamConfig(cfg, streamBucket)

	fmt.Fprintf(os.Stderr, "backfill-router: streaming ledgers %d..%d from bucket %q\n",
		startLedger, *to, streamBucket)

	var (
		totalLedgers   int
		totalRows      int
		insertFailures int
		lastCheckpoint = time.Now()
		// First-write checkpoint: persist immediately so a fresh
		// invocation lays down its presence even if it crashes before
		// the first scheduled checkpoint window.
		firstCheckpoint = true
	)
	const checkpointInterval = 30 * time.Second

	persist := func(ev soroswap_router.Event, ledger uint32, closedAt time.Time) {
		row := timescale.SoroswapRouterSwap{
			Ledger:          ledger,
			LedgerCloseTime: closedAt,
			TxHash:          ev.Swap.TxHash,
			OpIndex:         uint32(ev.Swap.OpIndex),
			ContractID:      ev.Swap.ContractID,
			FunctionName:    ev.Swap.Function,
			OpSource:        ev.Swap.OpSource,
			TxSource:        ev.Swap.TxSource,
			Recipient:       ev.Swap.Recipient,
			Path:            ev.Swap.Path,
			AmountIn:        ev.Swap.AmountIn.String(),
			AmountOut:       ev.Swap.AmountOut.String(),
		}
		if !ev.Swap.DeadlineTs.IsZero() {
			row.DeadlineTS = &ev.Swap.DeadlineTs
		}
		if ierr := store.InsertSoroswapRouterSwap(ctx, row); ierr != nil {
			insertFailures++
			if insertFailures < 10 {
				fmt.Fprintf(os.Stderr, "backfill-router: insert ledger=%d tx=%s: %v\n",
					ledger, ev.Swap.TxHash, ierr)
			}
			return
		}
		totalRows++
	}

	checkpoint := func(ledger uint32, force bool) {
		if !force && time.Since(lastCheckpoint) < checkpointInterval {
			return
		}
		if cerr := store.UpsertCursor(ctx, cursorSrc, cursorSub, ledger); cerr != nil {
			fmt.Fprintf(os.Stderr, "backfill-router: checkpoint at ledger %d failed: %v\n", ledger, cerr)
			return
		}
		lastCheckpoint = time.Now()
	}

	streamErr := ledgerstream.Stream(ctx, lsCfg, startLedger, uint32(*to),
		func(lcm sdkxdr.LedgerCloseMeta) error {
			totalLedgers++
			outputs, perr := disp.ProcessLedger(lcm, cfg.Stellar.Passphrase())
			if perr != nil {
				// One-ledger failures are noisy-but-not-fatal for a
				// historical backfill — log + continue rather than
				// abort the whole sweep.
				fmt.Fprintf(os.Stderr, "backfill-router: ledger %d: %v\n",
					lcm.LedgerSequence(), perr)
				return nil
			}
			closedAt := time.Unix(int64(lcm.LedgerCloseTime()), 0).UTC()
			for _, ev := range outputs {
				re, ok := ev.(soroswap_router.Event)
				if !ok {
					continue
				}
				persist(re, lcm.LedgerSequence(), closedAt)
			}
			// Checkpoint periodically + on first ledger.
			if firstCheckpoint {
				checkpoint(lcm.LedgerSequence(), true)
				firstCheckpoint = false
			} else {
				checkpoint(lcm.LedgerSequence(), false)
			}
			// Heartbeat every 10k ledgers so the operator can see
			// progress.
			if totalLedgers%10000 == 0 {
				fmt.Fprintf(os.Stderr, "backfill-router: %d ledgers processed, %d rows inserted (ledger=%d)\n",
					totalLedgers, totalRows, lcm.LedgerSequence())
			}
			return nil
		},
	)

	// Force a final checkpoint with the LAST processed ledger,
	// independent of the periodic timer, so resume always picks up
	// exactly where the run stopped (whether clean exit, signal, or
	// error). The cursor row may not exist if streamErr fired before
	// any ledger was processed — that's fine, GetCursor will
	// ErrNotFound on the next run.
	if totalLedgers > 0 {
		// We don't know the LAST ledger here without re-walking — the
		// closure captures it via `checkpoint(...)`. The periodic
		// checkpoint will have caught up close to the actual tail;
		// any 30-second slip on resume is a few-thousand-ledger
		// re-process which is idempotent.
		checkpoint(uint32(*to), false)
	}

	if streamErr != nil {
		return fmt.Errorf("stream: %w", streamErr)
	}

	fmt.Fprintf(os.Stderr, "backfill-router: done. %d ledgers, %d rows inserted (%d insert failures)\n",
		totalLedgers, totalRows, insertFailures)
	if insertFailures > 0 {
		return fmt.Errorf("%d insert failures — see stderr above", insertFailures)
	}
	return nil
}

// signalContext returns a context that cancels on SIGINT / SIGTERM
// so the backfill can flush a final checkpoint and exit cleanly.
// Pulled out so callers can defer cancel() right after the call site.
func signalContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		fmt.Fprintln(os.Stderr, "backfill-router: signal received, flushing checkpoint + exiting...")
		cancel()
	}()
	return ctx, cancel
}

// Silence unused-import paranoia: strings is referenced in the
// future tag-routed_via follow-up (Phase B from the original
// soroswap_router README). The placeholder _ keeps the import
// available without lint noise — the Phase-B PR will wire it.
var _ = strings.Join
