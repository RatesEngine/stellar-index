package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/RatesEngine/rates-engine/internal/config"
	"github.com/RatesEngine/rates-engine/internal/sources/comet"
	"github.com/RatesEngine/rates-engine/internal/sources/sorobanevents"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// cometLiquidityBackfill is the SQL-driven historical-fill
// subcommand for Comet liquidity events (the four POOL kinds added
// in PR #26: join_pool / exit_pool / deposit / withdraw).
//
// Comet topic shape: topic[0]="POOL" Symbol, topic[1] = the event
// kind Symbol. We pull all POOL-prefixed rows then match topic_1_xdr
// against the four pre-encoded liquidity-kind blobs in the callback
// — swap rows are filtered out here (they already land in `trades`
// via live ingest).
//
// One LiquidityEvent per source event row: a multi-token join_pool
// emits one POOL event per token, each becomes its own
// comet_liquidity row. No correlation buffer required.
//
//nolint:funlen,gocognit // linear pipeline.
func cometLiquidityBackfill(args []string) error {
	fs := flag.NewFlagSet("comet-liquidity-backfill", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "Path to TOML config file (required)")
	from := fs.Uint("from", 0, "First ledger sequence (inclusive, required)")
	to := fs.Uint("to", 0, "Last ledger sequence (inclusive, required)")
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

	// Pre-decode the four liquidity-kind topic[1] blobs so the
	// per-row callback is byte-equality only.
	liquidityTopic1 := make([][]byte, 0, 4)
	for _, b64 := range []string{
		comet.TopicSymbolJoinPool,
		comet.TopicSymbolExitPool,
		comet.TopicSymbolDeposit,
		comet.TopicSymbolWithdraw,
	} {
		raw, derr := base64.StdEncoding.DecodeString(b64)
		if derr != nil {
			return fmt.Errorf("decode TopicSymbol blob %q: %w", b64, derr)
		}
		liquidityTopic1 = append(liquidityTopic1, raw)
	}

	fmt.Fprintf(os.Stderr,
		"comet-liquidity-backfill: ledgers=[%d,%d] topic_0_sym=%q dry_run=%v\n",
		*from, *to, comet.EventTopic0, *dryRun)

	dec := comet.NewDecoder()
	startedAt := time.Now()
	var (
		rowsScanned   int64
		liquidityRows int64
		decodeErrors  int64
		eventsEmitted int64
		insertErrors  int64
	)

	err = store.StreamSorobanEvents(ctx, uint32(*from), uint32(*to),
		nil, // no contract filter — POOL is shared across every Comet pool contract
		[]string{comet.EventTopic0},
		func(row sorobanevents.Row) error {
			rowsScanned++
			if !topic1IsLiquidity(row.Topic1XDR, liquidityTopic1) {
				return nil // swap or unknown topic[1] — skip
			}
			liquidityRows++
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
				liq, ok := out.(comet.LiquidityEvent)
				if !ok {
					// Defensive: classify upstream should never route a
					// non-liquidity topic[1] through this filter. If it
					// happens, surface rather than silently drop.
					return fmt.Errorf("comet.Decoder emitted %T for a liquidity-topic row at ledger %d tx %s", out, row.Ledger, ev.TxHash)
				}
				eventsEmitted++
				if *dryRun {
					continue
				}
				if ierr := store.InsertCometLiquidity(ctx, timescale.CometLiquidityEvent{
					ContractID:      liq.ContractID,
					Ledger:          liq.Ledger,
					LedgerCloseTime: liq.ObservedAt,
					TxHash:          liq.TxHash,
					OpIndex:         liq.OpIndex,
					Kind:            timescale.CometLiquidityKind(liq.Kind),
					Caller:          liq.Caller,
					Token:           liq.Token,
					Amount:          liq.Amount,
					PoolAmountIn:    liq.PoolAmountIn,
				}); ierr != nil {
					insertErrors++
					fmt.Fprintf(os.Stderr, "  insert ledger=%d tx=%s: %v\n",
						liq.Ledger, liq.TxHash, ierr)
				}
			}
			return nil
		})
	if err != nil {
		return fmt.Errorf("StreamSorobanEvents: %w", err)
	}

	fmt.Fprintf(os.Stderr,
		"comet-liquidity-backfill: done in %s — rows_scanned=%d liquidity_rows=%d events_emitted=%d decode_errors=%d insert_errors=%d dry_run=%v\n",
		time.Since(startedAt).Round(time.Second),
		rowsScanned, liquidityRows, eventsEmitted, decodeErrors, insertErrors, *dryRun)
	if decodeErrors > 0 || insertErrors > 0 {
		return fmt.Errorf("comet-liquidity-backfill: %d decode errors + %d insert errors (see stderr)", decodeErrors, insertErrors)
	}
	return nil
}

// topic1IsLiquidity reports whether the given topic[1] XDR bytes
// match one of the four Comet liquidity-kind Symbol blobs.
func topic1IsLiquidity(t1 []byte, liquidityKinds [][]byte) bool {
	for _, k := range liquidityKinds {
		if bytes.Equal(t1, k) {
			return true
		}
	}
	return false
}
