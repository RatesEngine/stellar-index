package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/RatesEngine/rates-engine/internal/completeness"
	"github.com/RatesEngine/rates-engine/internal/config"
	"github.com/RatesEngine/rates-engine/internal/sources/aquarius"
	"github.com/RatesEngine/rates-engine/internal/sources/comet"
	"github.com/RatesEngine/rates-engine/internal/sources/phoenix"
	"github.com/RatesEngine/rates-engine/internal/sources/soroswap"
	"github.com/RatesEngine/rates-engine/internal/stellarrpc"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// verifyReconciliation implements ADR-0033 Claim 2b (projection
// reconciliation) for the Soroban trade sources: it re-derives, per
// ledger, how many canonical.Trade rows the decoder WOULD emit from
// soroban_events (the deterministic recomputation), and compares that
// to the rows actually present in `trades`. Any ledger where the two
// disagree is a projection gap — a row the projector dropped, or a
// phantom row with no backing event.
//
// Scope: the four event-based trade sources (soroswap, aquarius,
// phoenix, comet). SDEX is reconciled differently (Claim 2b classic,
// ADR-0033 Phase 5) because it predates Soroban and has no
// soroban_events. Oracle / liquidity / supply sources are out of scope
// for this command for now (1:N output arity is reconciled the same
// way; adding them is mechanical).
//
// Soroswap's swap event omits token identities, so its decoder needs
// the pair registry seeded from the factory; we seed via RPC the same
// way verify-decoders does. Without a seed, soroswap counts undercount
// pairs created before -from — the command warns and proceeds.
func verifyReconciliation(args []string) error {
	fs := flag.NewFlagSet("verify-reconciliation", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "Path to TOML config file (required)")
	from := fs.Uint("from", 0, "First ledger sequence (inclusive, required)")
	to := fs.Uint("to", 0, "Last ledger sequence (inclusive, required)")
	only := fs.String("source", "", "Limit to one source (soroswap|aquarius|phoenix|comet); default: all four")
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

	// Build the trade-source decoders directly (rather than via the
	// projector registry) so we hold the soroswap decoder handle for
	// seeding. ContractIDs/Topic0Syms prefilters are empty for all
	// four — they match by topic across every contract.
	soroswapDec := soroswap.NewDecoder()
	all := []struct {
		name string
		dec  completeness.Decoder
	}{
		{"soroswap", soroswapDec},
		{"aquarius", aquarius.NewDecoder()},
		{"phoenix", phoenix.NewDecoder()},
		{"comet", comet.NewDecoder()},
	}

	// Seed soroswap pairs (token identities) so its re-derive matches
	// the projector's view.
	if *only == "" || *only == "soroswap" {
		if err := seedSoroswapForRecon(ctx, cfg, soroswapDec); err != nil {
			fmt.Fprintf(os.Stderr, "verify-reconciliation: soroswap seed failed (%v) — soroswap counts may undercount pre-%d pairs\n", err, *from)
		}
	}

	anyGaps := false
	for _, src := range all {
		if *only != "" && src.name != *only {
			continue
		}
		expected, err := completeness.ReDeriveOutputCounts(ctx, store, src.dec, nil, nil, uint32(*from), uint32(*to))
		if err != nil {
			return fmt.Errorf("%s: re-derive: %w", src.name, err)
		}
		actual, err := store.CountRowsByLedger(ctx, "trades", "ledger", "source='"+src.name+"'", uint32(*from), uint32(*to))
		if err != nil {
			return fmt.Errorf("%s: actual counts: %w", src.name, err)
		}
		gaps := completeness.ReconcileCounts(expected, actual)

		expTotal, actTotal := sumCounts(expected), sumCounts(actual)
		if len(gaps) == 0 {
			fmt.Fprintf(os.Stderr, "verify-reconciliation: %-9s OK — expected=%d actual=%d across [%d,%d]\n",
				src.name, expTotal, actTotal, *from, *to)
			continue
		}
		anyGaps = true
		fmt.Fprintf(os.Stderr, "verify-reconciliation: %-9s %d MISMATCHED ledger(s) (expected=%d actual=%d):\n",
			src.name, len(gaps), expTotal, actTotal)
		for i, g := range gaps {
			if i >= *maxList {
				fmt.Fprintf(os.Stdout, "  … %d more (raise -max-list to see)\n", len(gaps)-*maxList)
				break
			}
			fmt.Fprintf(os.Stdout, "  source=%s ledger=%d expected=%d actual=%d (delta %+d)\n",
				src.name, g.Ledger, g.Expected, g.Actual, g.Actual-g.Expected)
		}
	}

	if anyGaps {
		return fmt.Errorf("projection reconciliation found mismatches — see above (ADR-0033 Claim 2b)")
	}
	return nil
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
