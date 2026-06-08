package main

import (
	"flag"
	"fmt"
	"math/big"
	"os"
	"sort"
	"time"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/RatesEngine/rates-engine/internal/config"
	"github.com/RatesEngine/rates-engine/internal/scval"
	"github.com/RatesEngine/rates-engine/internal/storage/clickhouse"
)

// decodeFlowAmount extracts the amount from a mint/burn/clawback event's data
// body. Returns ok=false on an undecodable body (with a short reason for skip
// diagnostics) so the supply scan can skip-and-continue without an err-return.
func decodeFlowAmount(dataXDR string) (*big.Int, string, bool) {
	sv, err := scval.Parse(dataXDR)
	if err != nil {
		return nil, "parse-error", false
	}
	// Bare i128 (common shape).
	if amt, err := scval.AsAmountFromI128(sv); err == nil {
		return amt.BigInt(), "", true
	}
	// Map variant {amount, to_muxed_id, ...} — SEP-41/CAP-67 carry the amount in
	// an `amount` field when a muxed destination is present (CLAUDE.md SEP-41
	// note). Mirror sep41_transfers' type-test.
	if sv.Type == xdr.ScValTypeScvMap {
		entries, merr := scval.AsMap(sv)
		if merr != nil {
			return nil, "map-parse-error", false
		}
		amtVal, ok := scval.MapField(entries, "amount")
		if !ok {
			return nil, "map-no-amount", false
		}
		if amt, aerr := scval.AsAmountFromI128(amtVal); aerr == nil {
			return amt.BigInt(), "", true
		}
		return nil, "map-amount-not-i128", false
	}
	return nil, sv.Type.String(), false
}

// chSupply derives total supply for EVERY token from the ClickHouse lake by
// summing CAP-67 classic + SEP-41 mint/burn/clawback flows per contract:
//
//	supply(contract) = Σ mint − Σ burn − Σ clawback   (baseline 0 at genesis)
//
// (ADR-0034 + docs/architecture/clickhouse-supply-from-ch.md.) The contract_id
// is the asset's SAC (classic) or token (SEP-41) contract — a unique per-token
// key. This is the read/aggregate proof; -write to persist is a follow-up
// (the snapshot shape + classic↔SAC asset_key mapping + XLM total_coins).
//
// Defaults to a report (top-N contracts by supply + coverage count). Window
// [from,to] per partition for the full-history run; a single all-history pass
// holds one in-memory map (thousands of contracts — bounded).
func chSupply(args []string) error {
	fs := flag.NewFlagSet("ch-supply", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "path to ratesengine.toml (required)")
	from := fs.Uint("from", 0, "first ledger sequence (inclusive, required)")
	to := fs.Uint("to", 0, "last ledger sequence (inclusive, required)")
	chAddr := fs.String("ch-addr", "127.0.0.1:9300", "ClickHouse native address")
	topN := fs.Int("top", 25, "print the top-N contracts by absolute supply")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *cfgPath == "" || *from == 0 || *to == 0 || *to < *from {
		return fmt.Errorf("-config, -from, -to are required; -to must be >= -from")
	}
	if _, err := config.LoadWithEnv(*cfgPath); err != nil {
		return err
	}

	ctx, cancel := signalContext()
	defer cancel()
	lo, hi := uint32(*from), uint32(*to)

	supplyByContract := make(map[string]*big.Int)
	flowsByContract := make(map[string]int)
	skipByType := make(map[string]int)
	var (
		flows        uint64
		decodeErrors uint64
		start        = time.Now()
		lastLog      = time.Now()
	)

	fmt.Fprintf(os.Stderr, "ch-supply: summing mint/burn/clawback flows for [%d,%d] from %s\n", lo, hi, *chAddr)
	err := clickhouse.StreamMintBurnFlows(ctx, *chAddr, lo, hi, func(f clickhouse.MintBurnFlow) error {
		flows++
		v, skipType, ok := decodeFlowAmount(f.DataXDR)
		if !ok {
			// Undecodable / non-i128 body (some SEP-41 variants carry a map);
			// skip rather than misparse. skipType pinpoints the shape to handle.
			decodeErrors++
			skipByType[skipType]++
			return nil
		}
		acc := supplyByContract[f.ContractID]
		if acc == nil {
			acc = big.NewInt(0)
			supplyByContract[f.ContractID] = acc
		}
		switch f.Kind {
		case "mint":
			acc.Add(acc, v)
		case "burn", "clawback":
			acc.Sub(acc, v)
		}
		flowsByContract[f.ContractID]++

		if time.Since(lastLog) >= 15*time.Second {
			rate := float64(flows) / time.Since(start).Seconds()
			fmt.Fprintf(os.Stderr, "ch-supply: %d flows, %d contracts (%.0f flows/s)\n",
				flows, len(supplyByContract), rate)
			lastLog = time.Now()
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("ch-supply: stream: %w", err)
	}

	// ─── report ──────────────────────────────────────────────────────────
	type row struct {
		contract string
		supply   *big.Int
		flows    int
	}
	rows := make([]row, 0, len(supplyByContract))
	for c, s := range supplyByContract {
		rows = append(rows, row{c, s, flowsByContract[c]})
	}
	sort.Slice(rows, func(i, j int) bool {
		ai := new(big.Int).Abs(rows[i].supply)
		aj := new(big.Int).Abs(rows[j].supply)
		return ai.Cmp(aj) > 0
	})

	fmt.Printf("\n=== ch-supply [%d,%d] ===\n", lo, hi)
	fmt.Printf("flows: %d  decode-skipped: %d  tokens (distinct contracts): %d\n",
		flows, decodeErrors, len(supplyByContract))
	fmt.Printf("skip-by-type: %v\n\n", skipByType)
	fmt.Printf("%-58s %30s %12s\n", "contract", "supply (raw)", "flows")
	for i, r := range rows {
		if i >= *topN {
			fmt.Printf("… %d more tokens\n", len(rows)-*topN)
			break
		}
		fmt.Printf("%-58s %30s %12d\n", r.contract, r.supply.String(), r.flows)
	}
	return nil
}
