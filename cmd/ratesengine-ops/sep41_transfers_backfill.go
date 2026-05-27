package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/RatesEngine/rates-engine/internal/config"
	sep41transfers "github.com/RatesEngine/rates-engine/internal/sources/sep41_transfers"
	"github.com/RatesEngine/rates-engine/internal/sources/sorobanevents"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// sep41TransfersBackfill is the SQL-driven historical-fill
// subcommand for SEP-41 audit-trail events (F-0021 closure).
// Replays soroban_events (ADR-0029 landing zone) filtered to the
// four topic[0] symbols this package owns, runs the live decoder
// against each row, and INSERTs into sep41_transfers.
//
// Migration 0047 MUST be applied before running this — per
// CLAUDE.md "Migrations not auto-deployed".
//
//nolint:funlen,gocognit,gocyclo // linear pipeline.
func sep41TransfersBackfill(args []string) error {
	fs := flag.NewFlagSet("sep41-transfers-backfill", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "Path to TOML config file (required)")
	from := fs.Uint("from", 0, "First ledger sequence (inclusive, required)")
	to := fs.Uint("to", 0, "Last ledger sequence (inclusive, required)")
	contracts := fs.String("contracts", "",
		"Comma-separated SEP-41 contract C-strkeys (defaults to [supply].watched_sep41_contracts)")
	dryRun := fs.Bool("dry-run", false, "Decode without inserting; print summary only")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *cfgPath == "" || *from == 0 || *to == 0 || *to < *from {
		return errors.New("-config, -from, and -to are required; -to must be >= -from")
	}

	cfg, err := config.LoadWithEnv(*cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store, err := timescale.Open(ctx, cfg.Storage.PostgresDSN)
	if err != nil {
		return fmt.Errorf("open postgres: %w", err)
	}
	defer func() { _ = store.Close() }()

	var contractList []string
	if *contracts != "" {
		for _, c := range strings.Split(*contracts, ",") {
			if t := strings.TrimSpace(c); t != "" {
				contractList = append(contractList, t)
			}
		}
	} else if len(cfg.Supply.WatchedSEP41Contracts) > 0 {
		contractList = append(contractList, cfg.Supply.WatchedSEP41Contracts...)
	}

	dec, err := sep41transfers.NewDecoder(buildSEP41DecoderContracts(contractList))
	if err != nil {
		return fmt.Errorf("sep41_transfers decoder: %w", err)
	}

	fmt.Fprintf(os.Stderr,
		"sep41-transfers-backfill: ledgers=[%d,%d] contracts=%d topic0_syms=transfer,approve,set_admin,set_authorized dry_run=%v\n",
		*from, *to, len(contractList), *dryRun)

	startedAt := time.Now()
	var rowsScanned, decodeErrors, eventsEmitted, insertErrors int64

	topic0Syms := []string{
		sep41transfers.SymbolTransfer,
		sep41transfers.SymbolApprove,
		sep41transfers.SymbolSetAdmin,
		sep41transfers.SymbolSetAuthorized,
	}

	err = store.StreamSorobanEvents(ctx, uint32(*from), uint32(*to),
		contractList, topic0Syms,
		func(row sorobanevents.Row) error {
			rowsScanned++
			ev, rerr := sorobanevents.Reconstruct(row)
			if rerr != nil {
				decodeErrors++
				fmt.Fprintf(os.Stderr, "  reconstruct ledger=%d contract=%s: %v\n",
					row.Ledger, row.ContractID, rerr)
				return nil
			}
			outs, derr := dec.Decode(ev)
			if derr != nil {
				decodeErrors++
				fmt.Fprintf(os.Stderr, "  decode ledger=%d contract=%s tx=%s: %v\n",
					row.Ledger, row.ContractID, ev.TxHash, derr)
				return nil
			}
			for _, out := range outs {
				tev, ok := out.(sep41transfers.Event)
				if !ok {
					return fmt.Errorf("decoder emitted %T at ledger %d tx %s", out, row.Ledger, ev.TxHash)
				}
				eventsEmitted++
				if *dryRun {
					continue
				}
				if ierr := store.InsertSEP41Transfer(ctx, timescale.SEP41TransferRow{
					ContractID:      tev.ContractID,
					Ledger:          tev.Ledger,
					TxHash:          tev.TxHash,
					OpIndex:         tev.OpIndex,
					EventIndex:      uint32(row.EventIndex), //nolint:gosec // soroban event_index is non-negative.
					ObservedAt:      tev.ObservedAt,
					Kind:            timescale.SEP41TransferKind(tev.Kind),
					FromAddr:        tev.FromAddr,
					ToAddr:          tev.ToAddr,
					Amount:          tev.Amount,
					LiveUntilLedger: tev.LiveUntilLedger,
					Authorized:      tev.Authorized,
				}); ierr != nil {
					insertErrors++
					fmt.Fprintf(os.Stderr, "  insert ledger=%d tx=%s: %v\n",
						tev.Ledger, tev.TxHash, ierr)
				}
			}
			return nil
		})
	if err != nil {
		return fmt.Errorf("StreamSorobanEvents: %w", err)
	}

	fmt.Fprintf(os.Stderr,
		"sep41-transfers-backfill: done in %s — rows_scanned=%d events_emitted=%d decode_errors=%d insert_errors=%d dry_run=%v\n",
		time.Since(startedAt).Round(time.Second),
		rowsScanned, eventsEmitted, decodeErrors, insertErrors, *dryRun)
	if decodeErrors > 0 || insertErrors > 0 {
		return fmt.Errorf("sep41-transfers-backfill: %d decode errors + %d insert errors", decodeErrors, insertErrors)
	}
	return nil
}

// buildSEP41DecoderContracts ensures NewDecoder receives a non-
// empty list. The synthetic fallback is never seen by Matches()
// in backfill mode (we call Decode directly), but the constructor
// requires non-empty for safety in the live-ingest case.
func buildSEP41DecoderContracts(operatorList []string) []string {
	if len(operatorList) > 0 {
		return operatorList
	}
	return []string{"CAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABSC4"}
}
