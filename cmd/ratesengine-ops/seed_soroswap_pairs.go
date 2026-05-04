package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/RatesEngine/rates-engine/internal/config"
	"github.com/RatesEngine/rates-engine/internal/sources/soroswap"
	"github.com/RatesEngine/rates-engine/internal/stellarrpc"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// seedSoroswapPairs reads every pair contract the Soroswap factory
// has ever deployed via stellar-rpc simulateTransaction, then writes
// the (pair, token0, token1) tuples to the soroswap_pairs registry
// table.
//
// Run once on first deployment (or after a factory reset) so the
// indexer + parallel backfill chunks boot with the registry primed.
// Subsequent factory new_pair events are upserted live by the
// indexer (see internal/pipeline/soroswap_registry.go), so this
// subcommand is one-shot bootstrap, not a regular cron.
//
// Flags:
//
//	-config PATH    TOML config (required). Used for postgres DSN +
//	                soroswap factory contract + RPC fallback.
//	-rpc URL        Override RPC endpoint. Falls back to
//	                cfg.Oracle.Soroswap.SeedRPCEndpoint, then to the
//	                first cfg.Stellar.RPCEndpoints entry.
//	-timeout DUR    Total wall-clock budget. Default 15m — enough
//	                for ~200 mainnet pairs at 300ms throttle.
//
// The sweep is ~3N+1 simulateTransaction calls with a 300ms throttle
// between each, so wall-time scales linearly with pair count. Public
// stellar-rpc endpoints rate-limit at ~3-5 req/s; the throttle keeps
// us comfortably below.
//
// Idempotent on every level:
//   - The factory's view functions are pure reads.
//   - UpsertSoroswapPair is ON CONFLICT (pair_strkey) DO UPDATE.
//   - Re-running after the indexer has already learned new pairs is
//     safe (every row is rewritten with the same data).
func seedSoroswapPairs(args []string) error {
	fs := flag.NewFlagSet("seed-soroswap-pairs", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "path to ratesengine.toml (required)")
	rpcOverride := fs.String("rpc", "", "stellar-rpc endpoint URL (overrides config)")
	timeout := fs.Duration("timeout", 15*time.Minute, "wall-clock budget for the sweep")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *cfgPath == "" {
		return errors.New("-config required")
	}

	cfg, err := config.LoadWithEnv(*cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if cfg.Oracle.Soroswap.FactoryContract == "" {
		return errors.New("oracle.soroswap.factory_contract is empty — refusing to sweep an unset factory")
	}

	endpoint := *rpcOverride
	if endpoint == "" {
		endpoint = cfg.Oracle.Soroswap.SeedRPCEndpoint
	}
	if endpoint == "" && len(cfg.Stellar.RPCEndpoints) > 0 {
		endpoint = cfg.Stellar.RPCEndpoints[0]
	}
	if endpoint == "" {
		return errors.New("no RPC endpoint — set -rpc, oracle.soroswap.seed_rpc_endpoint, or stellar.rpc_endpoints")
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	store, err := timescale.Open(ctx, cfg.Storage.PostgresDSN)
	if err != nil {
		return fmt.Errorf("storage: %w", err)
	}
	defer func() { _ = store.Close() }()

	fmt.Fprintf(os.Stderr, "seed-soroswap-pairs: factory=%s rpc=%s\n",
		cfg.Oracle.Soroswap.FactoryContract, endpoint)

	// Decoder.SeedPair fires the WithPairUpsertHook callback for every
	// pair the factory walk discovers, so SeedFromFactoryRPC + the hook
	// is the entire pipeline. The decoder's in-memory map is throwaway
	// here — postgres is the durable side.
	var (
		upserts atomic.Int64
		failed  atomic.Int64
	)
	dec := soroswap.NewDecoder(soroswap.WithPairUpsertHook(func(pair, t0, t1 string) {
		if t0 == "" || t1 == "" {
			fmt.Fprintf(os.Stderr, "  skip pair %s — empty token strkey\n", pair)
			failed.Add(1)
			return
		}
		if err := store.UpsertSoroswapPair(ctx, pair, t0, t1); err != nil {
			fmt.Fprintf(os.Stderr, "  upsert pair %s: %v\n", pair, err)
			failed.Add(1)
			return
		}
		upserts.Add(1)
	}))

	rpc := stellarrpc.New(endpoint, stellarrpc.WithTimeout(60*time.Second))
	count, err := dec.SeedFromFactoryRPC(ctx, rpc, cfg.Oracle.Soroswap.FactoryContract)
	if err != nil {
		return fmt.Errorf("rpc seed: %w (in-memory: %d, persisted: %d, failed: %d)",
			err, count, upserts.Load(), failed.Load())
	}

	fmt.Fprintf(os.Stderr,
		"seed-soroswap-pairs: %d pairs persisted to soroswap_pairs (%d failed)\n",
		upserts.Load(), failed.Load())
	if failed.Load() > 0 {
		return fmt.Errorf("%d upserts failed — check logs above", failed.Load())
	}
	return nil
}
