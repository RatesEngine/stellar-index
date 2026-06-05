package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"sync"
	"time"

	sdkxdr "github.com/stellar/go-stellar-sdk/xdr"
	"golang.org/x/sync/errgroup"

	"github.com/RatesEngine/rates-engine/internal/config"
	"github.com/RatesEngine/rates-engine/internal/ledgerstream"
	"github.com/RatesEngine/rates-engine/internal/storage/clickhouse"
)

// runCHBackfill walks a bounded ledger range from galexie and writes the
// Tier-1 structural rows to ClickHouse (ADR-0034 Phase 2). Mirrors the
// census-backfill ledgerstream walk; the per-ledger work is
// clickhouse.ExtractLedger -> Sink. Idempotent (ReplacingMergeTree), so a
// re-run over the same range is safe.
//
// -parallel N splits [from,to] into N contiguous chunks, each walked by its
// own goroutine with its own Sink (ClickHouse ingests concurrent writers
// well — this is the throughput unlock Postgres couldn't give us). N=1 is
// the plain single-walker path.
func chBackfill(args []string) error {
	fs := flag.NewFlagSet("ch-backfill", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "path to ratesengine.toml (required)")
	from := fs.Uint("from", 0, "first ledger sequence (inclusive, required)")
	to := fs.Uint("to", 0, "last ledger sequence (inclusive, required)")
	bucket := fs.String("bucket", "", "override storage bucket (default cfg.Storage.S3BucketLive)")
	chAddr := fs.String("ch-addr", "127.0.0.1:9300", "ClickHouse native address")
	flushEvery := fs.Int("flush-every", 500, "flush to ClickHouse every N ledgers (per worker)")
	parallel := fs.Int("parallel", 1, "number of concurrent range-walkers")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *cfgPath == "" || *from == 0 || *to == 0 || *to < *from {
		return fmt.Errorf("-config, -from, -to are required; -to must be >= -from")
	}
	if *parallel < 1 {
		*parallel = 1
	}

	cfg, err := config.LoadWithEnv(*cfgPath)
	if err != nil {
		return err
	}

	ctx, cancel := signalContext()
	defer cancel()

	streamBucket := cfg.Storage.S3BucketLive
	if *bucket != "" {
		streamBucket = *bucket
	}
	lsCfg := newBoundedLedgerStreamConfig(cfg, streamBucket)
	passphrase := cfg.Stellar.Passphrase()

	chunks := splitRange(uint32(*from), uint32(*to), *parallel)
	fmt.Fprintf(os.Stderr, "ch-backfill: streaming ledgers %d..%d from %q -> ClickHouse %s (%d worker(s))\n",
		*from, *to, streamBucket, *chAddr, len(chunks))

	var (
		mu      sync.Mutex
		total   int64
		start   = time.Now()
		lastLog = time.Now()
	)
	logProgress := func(workerIdx int, seq uint32) {
		mu.Lock()
		total++
		t := total
		if time.Since(lastLog) >= 15*time.Second {
			rate := float64(t) / time.Since(start).Seconds()
			fmt.Fprintf(os.Stderr, "ch-backfill: %d ledgers (worker %d at %d, %.1f ledgers/s)\n",
				t, workerIdx, seq, rate)
			lastLog = time.Now()
		}
		mu.Unlock()
	}

	g, gctx := errgroup.WithContext(ctx)
	for i, chunk := range chunks {
		i, chunk := i, chunk // capture
		g.Go(func() error {
			return chBackfillChunk(gctx, i, chunk, lsCfg, passphrase, *chAddr, *flushEvery, logProgress)
		})
	}
	walkErr := g.Wait()

	if walkErr != nil && !errors.Is(walkErr, context.Canceled) {
		return fmt.Errorf("ch-backfill: stream (%d done): %w", total, walkErr)
	}
	elapsed := time.Since(start)
	rate := 0.0
	if elapsed.Seconds() > 0 {
		rate = float64(total) / elapsed.Seconds()
	}
	fmt.Fprintf(os.Stderr, "ch-backfill: done — %d ledgers in %s (%.1f ledgers/s)\n",
		total, elapsed.Round(time.Second), rate)
	return nil
}

// chBackfillChunk walks one chunk's range with its own Sink. A ClickHouse
// write failure is fatal (returns the error so errgroup cancels siblings);
// re-running the range is safe under ReplacingMergeTree.
func chBackfillChunk(
	ctx context.Context,
	idx int,
	chunk rangeChunk,
	lsCfg ledgerstream.Config,
	passphrase, chAddr string,
	flushEvery int,
	logProgress func(workerIdx int, seq uint32),
) error {
	sink, err := clickhouse.Open(ctx, chAddr, flushEvery)
	if err != nil {
		return err
	}
	defer func() { _ = sink.Close(ctx) }()

	walkErr := ledgerstream.Stream(ctx, lsCfg, chunk.from, chunk.to,
		func(lcm sdkxdr.LedgerCloseMeta) error {
			ext, eerr := clickhouse.ExtractLedger(lcm, passphrase)
			if eerr != nil {
				fmt.Fprintf(os.Stderr, "ch-backfill: worker %d extract ledger %d: %v\n", idx, lcm.LedgerSequence(), eerr)
				return nil
			}
			if aerr := sink.Add(ctx, ext); aerr != nil {
				return aerr // a ClickHouse write failure is fatal; retry the range
			}
			logProgress(idx, lcm.LedgerSequence())
			return nil
		},
	)
	if walkErr != nil {
		return walkErr
	}
	// Clean completion: flush the chunk's tail before Close so the final
	// partial batch lands.
	return sink.Flush(ctx)
}
