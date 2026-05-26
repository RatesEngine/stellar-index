package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/RatesEngine/rates-engine/internal/config"
	"github.com/RatesEngine/rates-engine/internal/sources/phoenix"
	"github.com/RatesEngine/rates-engine/internal/sources/sorobanevents"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// phoenixBackfill is the SQL-driven historical-fill subcommand for
// Phoenix's liquidity + stake event surfaces (the 4 actions added
// in PR #27: provide_liquidity, withdraw_liquidity, bond, unbond).
//
// Phoenix encodes the action as topic[0] = String — so the SQL
// filter `topic_0_sym IN (provide_liquidity, withdraw_liquidity,
// bond, unbond)` exactly matches the per-action events the decoder
// claims. Each action emits N (3-5) events per (ledger, tx_hash,
// op_index); the decoder's per-action correlation buffer
// accumulates them and emits a single LiquidityEvent / StakeEvent
// when complete.
//
// Because we feed events in (ledger_close_time, ledger, tx_hash,
// op_index) order, all events for one action's instance arrive
// adjacently and the buffer's age-based eviction (5 min based on
// event ClosedAt) only triggers for genuinely-incomplete groups
// (chain-level partial transmissions, decoder-version mismatch,
// etc.). Orphans are surfaced via Decoder.EvictedOrphans().
//
// Swap (topic[0]=String("swap")) is NOT backfilled here — Phoenix
// swaps already land in `trades` via live ingest. Adding swap-
// reassembly into this subcommand would double-write rows;
// follow-up if needed.
//
//nolint:funlen,gocognit,gocyclo // linear pipeline; the type-switch over (LiquidityEvent | StakeEvent | unexpected) inflates cyclo but reads better inline than as a separate dispatch helper.
func phoenixBackfill(args []string) error {
	fs := flag.NewFlagSet("phoenix-backfill", flag.ContinueOnError)
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

	actions := []string{
		phoenix.EventActionProvideLiquidity,
		phoenix.EventActionWithdrawLiquidity,
		phoenix.EventActionBond,
		phoenix.EventActionUnbond,
	}
	fmt.Fprintf(os.Stderr,
		"phoenix-backfill: ledgers=[%d,%d] actions=%v dry_run=%v\n",
		*from, *to, actions, *dryRun)

	dec := phoenix.NewDecoder()
	startedAt := time.Now()
	var (
		rowsScanned   int64
		decodeErrors  int64
		liquidityRows int64
		stakeRows     int64
		insertErrors  int64
	)

	err = store.StreamSorobanEvents(ctx, uint32(*from), uint32(*to),
		nil, // no contract filter — Phoenix factory emits across many pool + per-pool stake contracts
		actions,
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
				switch e := out.(type) {
				case phoenix.LiquidityEvent:
					liquidityRows++
					if *dryRun {
						continue
					}
					c := e.Change
					sharesStr := ""
					if c.Action == phoenix.EventActionWithdrawLiquidity {
						sharesStr = c.SharesAmount.String()
					}
					if ierr := store.InsertPhoenixLiquidityChange(ctx, timescale.PhoenixLiquidityChange{
						Pool:         c.Pool,
						Ledger:       c.Ledger,
						ObservedAt:   c.ClosedAt,
						TxHash:       c.TxHash,
						OpIndex:      uint32(c.OpIndex),
						Action:       timescale.PhoenixLiquidityAction(c.Action),
						Sender:       c.Sender,
						TokenA:       c.TokenA,
						TokenB:       c.TokenB,
						AmountA:      c.AmountA.String(),
						AmountB:      c.AmountB.String(),
						SharesAmount: sharesStr,
					}); ierr != nil {
						insertErrors++
						fmt.Fprintf(os.Stderr, "  insert liquidity ledger=%d tx=%s: %v\n",
							c.Ledger, c.TxHash, ierr)
					}
				case phoenix.StakeEvent:
					stakeRows++
					if *dryRun {
						continue
					}
					c := e.Change
					if ierr := store.InsertPhoenixStakeEvent(ctx, timescale.PhoenixStakeEvent{
						StakeContract: c.Contract,
						Ledger:        c.Ledger,
						ObservedAt:    c.ClosedAt,
						TxHash:        c.TxHash,
						OpIndex:       uint32(c.OpIndex),
						Action:        timescale.PhoenixStakeAction(c.Action),
						User:          c.User,
						LPToken:       c.LPToken,
						Amount:        c.Amount.String(),
					}); ierr != nil {
						insertErrors++
						fmt.Fprintf(os.Stderr, "  insert stake ledger=%d tx=%s: %v\n",
							c.Ledger, c.TxHash, ierr)
					}
				default:
					// TradeEvent (swap reassembly) — should not happen
					// since we filter on liquidity+stake actions, but
					// surface defensively.
					return fmt.Errorf("phoenix.Decoder emitted unexpected %T at ledger %d tx %s", out, row.Ledger, ev.TxHash)
				}
			}
			return nil
		})
	if err != nil {
		return fmt.Errorf("StreamSorobanEvents: %w", err)
	}

	fmt.Fprintf(os.Stderr,
		"phoenix-backfill: done in %s — rows_scanned=%d liquidity_rows=%d stake_rows=%d decode_errors=%d insert_errors=%d orphans_evicted=%d dry_run=%v\n",
		time.Since(startedAt).Round(time.Second),
		rowsScanned, liquidityRows, stakeRows, decodeErrors, insertErrors, dec.EvictedOrphans(), *dryRun)
	if decodeErrors > 0 || insertErrors > 0 {
		return fmt.Errorf("phoenix-backfill: %d decode errors + %d insert errors (see stderr)", decodeErrors, insertErrors)
	}
	return nil
}
