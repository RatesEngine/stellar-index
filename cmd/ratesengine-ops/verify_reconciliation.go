package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/RatesEngine/rates-engine/internal/completeness"
	"github.com/RatesEngine/rates-engine/internal/config"
	"github.com/RatesEngine/rates-engine/internal/sources/soroswap"
	"github.com/RatesEngine/rates-engine/internal/stellarrpc"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// verifyReconciliation implements ADR-0033 Claim 2b (projection
// reconciliation): per ledger, the rows a source SHOULD have produced
// must equal the rows actually in its table.
//
// Two oracles for "should have produced", by source class:
//
//   - Soroban trade sources (soroswap, aquarius, phoenix, comet) —
//     re-derive by running the real decoder over soroban_events
//     (deterministic recomputation). Correlation sources reconcile
//     correctly because each logical record's events share one
//     (ledger, tx, op).
//   - SDEX — predates Soroban, so there is no soroban_events to
//     re-derive from. Use the LCM-derived classic_trade_effect_count
//     census in ledger_ingest_log (one ClaimAtom = one trade). This is
//     gated on the substrate record covering the range; if it has gaps,
//     run `census-backfill` first. The external Hubble anchor
//     (`hubble-check`) is the defense-in-depth cross-check.
//
// Exits non-zero if any mismatch is found. Cron/CI-gateable.
func verifyReconciliation(args []string) error { //nolint:gocognit,gocyclo,funlen // linear per-source loop; splitting reduces clarity (same as backfillRouter).
	fs := flag.NewFlagSet("verify-reconciliation", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "Path to TOML config file (required)")
	from := fs.Uint("from", 0, "First ledger sequence (inclusive, required)")
	to := fs.Uint("to", 0, "Last ledger sequence (inclusive, required)")
	only := fs.String("source", "", "Limit to one source (soroswap|aquarius|phoenix|comet|sdex); default: all")
	maxList := fs.Int("max-list", 50, "Max gap ledgers to print per source")
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

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()

	store, err := timescale.Open(ctx, cfg.Storage.PostgresDSN)
	if err != nil {
		return fmt.Errorf("storage open: %w", err)
	}
	defer func() { _ = store.Close() }()

	lo, hi := uint32(*from), uint32(*to)

	catalogue, soroswapDec := buildReconciliationCatalogue(cfg)
	if *only == "" || *only == "soroswap" {
		if err := seedSoroswapForRecon(ctx, cfg, soroswapDec); err != nil {
			fmt.Fprintf(os.Stderr, "verify-reconciliation: soroswap seed failed (%v) — soroswap counts may undercount pre-%d pairs\n", err, lo)
		}
	}

	anyGaps := false
	for _, src := range catalogue {
		if *only != "" && src.name != *only {
			continue
		}

		// Re-derive once per source (bucketed by EventKind), or fetch the
		// SDEX census; the per-target diff below projects the kinds for
		// each table.
		var byKind map[string]map[uint32]int
		var censusExpected map[uint32]int
		if src.census {
			c, cerr := sdexCensusExpected(ctx, store, lo, hi)
			if cerr != nil {
				return fmt.Errorf("%s: %w", src.name, cerr)
			}
			censusExpected = c
		} else {
			bk, derr := completeness.ReDeriveOutputCountsByKind(ctx, store, src.dec, src.contractIDs, src.topic0Syms, lo, hi)
			if derr != nil {
				return fmt.Errorf("%s: re-derive: %w", src.name, derr)
			}
			byKind = bk
		}

		for _, tgt := range src.targets {
			expected := censusExpected
			if !src.census {
				expected = completeness.SumKinds(byKind, tgt.kinds...)
			}
			actual, aerr := store.CountRowsByLedger(ctx, tgt.table, "ledger", tgt.whereFilter, lo, hi)
			if aerr != nil {
				return fmt.Errorf("%s/%s: actual counts: %w", src.name, tgt.table, aerr)
			}
			gaps := completeness.ReconcileCounts(expected, actual)
			label := src.name + "/" + tgt.table
			expTotal, actTotal := sumCounts(expected), sumCounts(actual)
			if len(gaps) == 0 {
				fmt.Fprintf(os.Stderr, "verify-reconciliation: %-28s OK — expected=%d actual=%d\n", label, expTotal, actTotal)
				continue
			}
			anyGaps = true
			fmt.Fprintf(os.Stderr, "verify-reconciliation: %-28s %d MISMATCHED ledger(s) (expected=%d actual=%d):\n",
				label, len(gaps), expTotal, actTotal)
			for i, g := range gaps {
				if i >= *maxList {
					_, _ = fmt.Fprintf(os.Stdout, "  … %d more (raise -max-list to see)\n", len(gaps)-*maxList)
					break
				}
				_, _ = fmt.Fprintf(os.Stdout, "  %s ledger=%d expected=%d actual=%d (delta %+d)\n",
					label, g.Ledger, g.Expected, g.Actual, g.Actual-g.Expected)
			}
		}
	}

	if anyGaps {
		return fmt.Errorf("projection reconciliation found mismatches — see above (ADR-0033 Claim 2b)")
	}
	return nil
}

// sdexCensusExpected returns the SDEX per-ledger expected trade count
// from the LCM census, guarded on ledger_ingest_log fully covering the
// range (else the census is incomplete and reads as false gaps).
func sdexCensusExpected(ctx context.Context, store *timescale.Store, lo, hi uint32) (map[uint32]int, error) {
	gaps, err := store.FindLedgerIngestGaps(ctx, lo, hi)
	if err != nil {
		return nil, err
	}
	if len(gaps) > 0 {
		return nil, fmt.Errorf("ledger_ingest_log has %d gap(s) in [%d,%d] — run `census-backfill` first (first gap %d-%d)",
			len(gaps), lo, hi, gaps[0].Start, gaps[0].End)
	}
	return store.ClassicTradeEffectCountsByLedger(ctx, lo, hi)
}

func sumCounts(m map[uint32]int) int {
	total := 0
	for _, v := range m {
		total += v
	}
	return total
}

// seedSoroswapForRecon seeds the soroswap pair registry from the
// factory via RPC — mirrors verify-decoders so the re-derive resolves
// token identities for pairs created before the audited range.
func seedSoroswapForRecon(ctx context.Context, cfg config.Config, dec *soroswap.Decoder) error {
	if cfg.Oracle.Soroswap.FactoryContract == "" {
		return fmt.Errorf("oracle.soroswap.factory_contract empty")
	}
	endpoint := cfg.Oracle.Soroswap.SeedRPCEndpoint
	if endpoint == "" && len(cfg.Stellar.RPCEndpoints) > 0 {
		endpoint = cfg.Stellar.RPCEndpoints[0]
	}
	if endpoint == "" {
		return fmt.Errorf("no RPC endpoint (set oracle.soroswap.seed_rpc_endpoint or stellar.rpc_endpoints)")
	}
	seedCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	rpc := stellarrpc.New(endpoint, stellarrpc.WithTimeout(60*time.Second))
	n, err := dec.SeedFromFactoryRPC(seedCtx, rpc, cfg.Oracle.Soroswap.FactoryContract)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "verify-reconciliation: seeded %d soroswap pairs\n", n)
	return nil
}
