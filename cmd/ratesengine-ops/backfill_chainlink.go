package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/config"
	"github.com/RatesEngine/rates-engine/internal/sources/external/chainlink"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// chainlinkFeedSetFromConfig — same shape + reasoning as the
// adapter in cmd/ratesengine-indexer/main.go. Duplicated rather
// than promoted to a shared internal package because the cmd/
// boundary is the natural seam: the chainlink package itself can't
// import config (no cycle today, no cycle tomorrow), and a
// dedicated cmd-helper package for two ~15-line functions would be
// over-engineering. Keep them in sync by convention.
func chainlinkFeedSetFromConfig(in map[string]config.ChainlinkFeedSetting) (map[string]chainlink.FeedSpec, []canonical.Pair, error) {
	adapted := make(map[string]chainlink.FeedSpec, len(in))
	for k, v := range in {
		adapted[k] = chainlink.FeedSpec{
			Address:  v.Address,
			Decimals: v.Decimals,
			Invert:   v.Invert,
		}
	}
	return chainlink.BuildFeedSet(adapted)
}

// backfillChainlink walks each configured Chainlink feed's
// AnswerUpdated event log across the requested block range and
// inserts one canonical.OracleUpdate row per historical round
// into oracle_updates.
//
// All-time backfill of 516 feeds at 5k blocks/chunk on Alchemy
// free tier completes in roughly 7 hours of wall time with the
// default per-chunk sleep; the operator should run this overnight.
//
// Idempotent: the oracle_updates PK (source, ledger, tx_hash,
// op_index, ts) plus the deterministic syntheticTxHash(feed,
// roundId) means re-running this command over an already-backfilled
// range is a no-op (InsertOracleUpdate uses ON CONFLICT DO NOTHING).
//
//nolint:gocognit,gocyclo,funlen // ops-CLI subcommand: flag parsing + config load + insert loop in one function is the most readable shape
func backfillChainlink(args []string) error {
	fs := flag.NewFlagSet("backfill-chainlink", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "Path to TOML config file (required)")
	fromBlock := fs.Uint64("from-block", 0, "Inclusive lower bound block. 0 = the post-Merge marker (Sep 2022) — sane default for ETH-mainnet Chainlink feeds, which predominantly post-date the Merge.")
	toBlock := fs.Uint64("to-block", 0, "Inclusive upper bound block. 0 = current head (queried via eth_blockNumber).")
	chunkBlocks := fs.Uint64("chunk-blocks", chainlink.DefaultBackfillChunkBlocks, "Blocks per eth_getLogs call. 5000 is the safe default for Alchemy / Infura response-size caps.")
	sleepMs := fs.Int("sleep-ms", 0, "Per-chunk pause in milliseconds. Use to throttle under provider rate limits (e.g. 100ms for ~10 req/s).")
	dryRun := fs.Bool("dry-run", false, "Fetch + decode but don't write to Timescale.")
	progressEvery := fs.Int("progress-every", 1000, "Print a progress line every N rounds inserted.")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *cfgPath == "" {
		fs.Usage()
		return fmt.Errorf("-config required")
	}

	cfg, err := config.LoadWithEnv(*cfgPath)
	if err != nil {
		return err
	}
	if !cfg.External.Chainlink.Enabled {
		// Backfill is operator-triggered so we don't strictly require
		// the live poller to be on — but if neither the toggle nor an
		// RPC URL is set, there's nothing to point at.
		if cfg.External.Chainlink.RPCUrl == "" {
			return fmt.Errorf("chainlink: [external.chainlink].rpc_url is empty and enabled = false — set the URL (or env CHAINLINK_RPC_URL) before backfilling")
		}
	}
	if cfg.External.Chainlink.RPCUrl == "" {
		return fmt.Errorf("chainlink: [external.chainlink].rpc_url is empty (env CHAINLINK_RPC_URL also unset)")
	}

	feedMap, pairs, err := chainlinkFeedSetFromConfig(cfg.External.Chainlink.FeedMap)
	if err != nil {
		return fmt.Errorf("feed_map: %w", err)
	}
	if len(pairs) == 0 {
		return fmt.Errorf("chainlink: feed_map is empty after parse — nothing to backfill")
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	poller := chainlink.NewPoller(cfg.External.Chainlink.RPCUrl, feedMap)
	poller.Logger = logger

	opts := chainlink.BackfillOptions{
		FromBlock:   *fromBlock,
		ToBlock:     *toBlock,
		ChunkBlocks: *chunkBlocks,
		Sleep:       time.Duration(*sleepMs) * time.Millisecond,
	}

	// All-time backfills can take hours — give a generous ceiling and
	// rely on the operator's Ctrl-C / systemd-timeout for actual
	// cancellation. The Backfill walk respects ctx.Err() in its inner
	// loop so cancellation is responsive.
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Hour)
	defer cancel()

	fmt.Fprintf(os.Stderr,
		"backfill-chainlink: feeds=%d from-block=%d to-block=%d chunk=%d sleep=%dms dry-run=%v\n",
		len(pairs), *fromBlock, *toBlock, *chunkBlocks, *sleepMs, *dryRun)

	updates := make(chan canonical.OracleUpdate, 1024)

	var store *timescale.Store
	if !*dryRun {
		store, err = timescale.Open(ctx, cfg.Storage.PostgresDSN)
		if err != nil {
			return fmt.Errorf("storage: %w", err)
		}
		defer func() { _ = store.Close() }()
	}

	t0 := time.Now()
	walkErr := make(chan error, 1)
	go func() {
		walkErr <- poller.Backfill(ctx, pairs, opts, updates)
	}()

	inserted, skipped := 0, 0
	for u := range updates {
		if *dryRun {
			inserted++
			if *progressEvery > 0 && inserted%*progressEvery == 0 {
				fmt.Fprintf(os.Stderr, "  ... %d decoded\n", inserted)
			}
			continue
		}
		if err := store.InsertOracleUpdate(ctx, u); err != nil {
			skipped++
			fmt.Fprintf(os.Stderr, "insert oracle_update (%s round=%s tx=%s): %v\n",
				u.Source, u.Price.String(), u.TxHash, err)
			continue
		}
		inserted++
		if *progressEvery > 0 && inserted%*progressEvery == 0 {
			fmt.Fprintf(os.Stderr, "  ... %d inserted, %d skipped\n", inserted, skipped)
		}
	}
	walkResult := <-walkErr

	fmt.Fprintf(os.Stderr,
		"backfill-chainlink: done — %d inserted, %d skipped in %v\n",
		inserted, skipped, time.Since(t0).Round(time.Second))
	return walkResult
}
