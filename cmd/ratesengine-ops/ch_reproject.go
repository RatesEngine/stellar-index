package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/RatesEngine/rates-engine/internal/completeness"
	"github.com/RatesEngine/rates-engine/internal/config"
	"github.com/RatesEngine/rates-engine/internal/dispatcher"
	"github.com/RatesEngine/rates-engine/internal/events"
	"github.com/RatesEngine/rates-engine/internal/sources/sdex"
	"github.com/RatesEngine/rates-engine/internal/storage/clickhouse"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// chReproject is the ADR-0034 Phase-4 validation: it re-derives a ledger
// range's protocol output from the ClickHouse Tier-1 lake using the EXISTING
// decoders, and compares to the currently-served Postgres protocol tables —
// i.e. "what would rebuilding Postgres from ClickHouse change?". For each
// source's target table it reports CH-re-derived vs served per-ledger counts.
//
//   - ClickHouse side: a single pass over stellar.contract_events, feeding each
//     event to every source decoder (Matches-gated, per-source-independent),
//     bucketed by EventKind() — then SumKinds projects the kinds that land in
//     each target table.
//   - Served side: the actual protocol-table row counts (store.CountRowsByLedger).
//
// A CH count > served is the headline migration win: the lake recovers trades
// the live/soroban_events path silently dropped (the event_index collision).
// CH < served flags a CH-side gap (e.g. redstone needs op_args the extractor
// doesn't capture yet). The baseline is deliberately the served tables, NOT
// soroban_events — that landing zone is being decommissioned and re-derives
// unreliably (and scanning it loads the live DB).
//
// soroswap is re-derived WITHOUT RPC pair-seeding, so its trade count
// undercounts pairs created before the range; that delta is expected here
// (absolute soroswap correctness is verify-reconciliation's job) and flagged.
func chReproject(args []string) error { //nolint:gocognit,gocyclo,funlen // linear: build cats, two re-derives, compare; splitting reduces clarity.
	fs := flag.NewFlagSet("ch-reproject", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "path to ratesengine.toml (required)")
	from := fs.Uint("from", 0, "first ledger sequence (inclusive, required)")
	to := fs.Uint("to", 0, "last ledger sequence (inclusive, required)")
	chAddr := fs.String("ch-addr", "127.0.0.1:9300", "ClickHouse native address")
	maxList := fs.Int("max-list", 20, "max mismatched ledgers to print per kind")
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

	lo, hi := uint32(*from), uint32(*to)

	cat, _ := buildReconciliationCatalogue(cfg)

	// ─── ClickHouse side: one pass, every event-based decoder per event ──
	fmt.Fprintf(os.Stderr, "ch-reproject: re-deriving [%d,%d] from ClickHouse %s\n", lo, hi, *chAddr)
	// Bucket per SOURCE then kind: the 3 reflector variants all emit the same
	// EventKind ("reflector.update") but route to oracle_updates under distinct
	// source filters, so a kind-only bucket would merge them. Per-source keying
	// keeps each variant's output (and its contractIDs prefilter) separate.
	chBySrc := make(map[string]map[string]map[uint32]int)
	chStart := time.Now()
	cherr := clickhouse.StreamContractEvents(ctx, *chAddr, lo, hi, nil, func(ev events.Event) error {
		for _, src := range cat {
			if src.dec == nil { // census-only (sdex) — op-based, not in contract_events
				continue
			}
			// Oracle decoders match by TOPIC, not contract — restrict each to
			// its own contract (mirrors the PG path's contractIDs prefilter).
			if len(src.contractIDs) > 0 && !containsStr(src.contractIDs, ev.ContractID) {
				continue
			}
			if !src.dec.Matches(ev) {
				continue
			}
			outs, derr := src.dec.Decode(ev)
			if derr != nil {
				continue // soft-fail, mirroring the projector + the PG re-derive
			}
			bk := chBySrc[src.name]
			if bk == nil {
				bk = make(map[string]map[uint32]int)
				chBySrc[src.name] = bk
			}
			for _, out := range outs {
				k := out.EventKind()
				if bk[k] == nil {
					bk[k] = make(map[uint32]int)
				}
				bk[k][ev.Ledger]++
			}
		}
		return nil
	})
	if cherr != nil {
		return fmt.Errorf("ch-reproject: clickhouse stream: %w", cherr)
	}
	fmt.Fprintf(os.Stderr, "ch-reproject: ClickHouse pass done in %s\n", time.Since(chStart).Round(time.Second))

	// ─── SDEX side: op-based re-derivation ───────────────────────────────
	// SDEX trades are op-derived (operations + operation_results), NOT
	// event-derived, so they don't flow through StreamContractEvents. A
	// separate op pass feeds each trade-bearing op to the SDEX decoder; the
	// passive-offer + one-side-zero fixes recover here for all history, so
	// CH > served is expected (the live path dropped them).
	sdexByLedger := make(map[uint32]int)
	sdexDec := sdex.NewDecoder()
	sdexStart := time.Now()
	serr := clickhouse.StreamSDEXOps(ctx, *chAddr, lo, hi, func(op clickhouse.SDEXOp) error {
		// SDEX Decode never returns a non-nil error — it soft-fails per claim
		// atom internally (drops malformed/both-zero, keeps the rest).
		outs, _ := sdexDec.Decode(dispatcher.OpContext{
			Ledger:   op.Ledger,
			ClosedAt: op.ClosedAt,
			TxHash:   op.TxHash,
			TxSource: op.Source,
			OpIndex:  int(op.OpIndex),
			Op:       op.Op,
			OpResult: op.OpResult,
		})
		sdexByLedger[op.Ledger] += len(outs)
		return nil
	})
	if serr != nil {
		return fmt.Errorf("ch-reproject: sdex op stream: %w", serr)
	}
	fmt.Fprintf(os.Stderr, "ch-reproject: SDEX op pass done in %s\n", time.Since(sdexStart).Round(time.Second))

	// ─── compare CH re-derive vs the served protocol tables, per target ──
	fmt.Printf("\n=== ch-reproject: rebuild-from-ClickHouse vs served tables (ADR-0034 Phase 4) ===\n")
	fmt.Printf("range: %d..%d  (CH > served ⇒ lake recovers silently-dropped rows; CH < served ⇒ CH-side gap)\n\n", lo, hi)
	fmt.Printf("%-34s %12s %12s  %s\n", "source/table", "CH-rederive", "served", "verdict")

	anyDiff := false
	for _, src := range cat {
		if src.dec == nil {
			// sdex: op-based re-derivation (sdexByLedger, above). One target
			// (trades WHERE source='sdex'); CH > served = recovered fills.
			if src.name == "sdex" {
				for _, tgt := range src.targets {
					actual, aerr := store.CountRowsByLedger(ctx, tgt.table, "ledger", tgt.whereFilter, lo, hi)
					if aerr != nil {
						return fmt.Errorf("ch-reproject: %s/%s served counts: %w", src.name, tgt.table, aerr)
					}
					gaps := completeness.ReconcileCounts(actual, sdexByLedger)
					label := src.name + "/" + tgt.table
					chTotal, servedTotal := sumCounts(sdexByLedger), sumCounts(actual)
					if len(gaps) == 0 {
						fmt.Printf("%-34s %12d %12d  OK\n", label, chTotal, servedTotal)
						continue
					}
					anyDiff = true
					fmt.Printf("%-34s %12d %12d  %d ledger(s) differ\n", label, chTotal, servedTotal, len(gaps))
					for i, g := range gaps {
						if i >= *maxList {
							fmt.Printf("    … %d more (raise -max-list)\n", len(gaps)-*maxList)
							break
						}
						fmt.Printf("    ledger=%d served=%d CH=%d (delta %+d)\n", g.Ledger, g.Expected, g.Actual, g.Actual-g.Expected)
					}
				}
			}
			continue
		}
		for _, tgt := range src.targets {
			expected := completeness.SumKinds(chBySrc[src.name], tgt.kinds...) // CH-re-derived per ledger, this source only
			actual, aerr := store.CountRowsByLedger(ctx, tgt.table, "ledger", tgt.whereFilter, lo, hi)
			if aerr != nil {
				return fmt.Errorf("ch-reproject: %s/%s served counts: %w", src.name, tgt.table, aerr)
			}
			gaps := completeness.ReconcileCounts(actual, expected) // actual=served baseline, "actual"=CH
			label := src.name + "/" + tgt.table
			chTotal, servedTotal := sumCounts(expected), sumCounts(actual)
			if len(gaps) == 0 {
				fmt.Printf("%-34s %12d %12d  OK\n", label, chTotal, servedTotal)
				continue
			}
			anyDiff = true
			note := ""
			if src.name == "soroswap" {
				note = " (soroswap unseeded — undercount expected)"
			}
			fmt.Printf("%-34s %12d %12d  %d ledger(s) differ%s\n", label, chTotal, servedTotal, len(gaps), note)
			for i, g := range gaps {
				if i >= *maxList {
					fmt.Printf("    … %d more (raise -max-list)\n", len(gaps)-*maxList)
					break
				}
				fmt.Printf("    ledger=%d served=%d CH=%d (delta %+d)\n", g.Ledger, g.Expected, g.Actual, g.Actual-g.Expected)
			}
		}
	}

	if anyDiff {
		// Not an error per se — differences are the point of the report (they
		// quantify what rebuilding from CH changes). Surface for the operator.
		fmt.Printf("\nch-reproject: differences found — review above (CH>served = recovered loss; CH<served = CH gap)\n")
		return nil
	}
	fmt.Printf("\n✅ ch-reproject: CH re-derivation matches the served tables exactly\n")
	return nil
}

func containsStr(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
