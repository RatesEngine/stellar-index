package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"

	sdkxdr "github.com/stellar/go-stellar-sdk/xdr"

	"github.com/RatesEngine/rates-engine/internal/config"
	"github.com/RatesEngine/rates-engine/internal/consumer"
	"github.com/RatesEngine/rates-engine/internal/ledgerstream"
	"github.com/RatesEngine/rates-engine/internal/pipeline"
	"github.com/RatesEngine/rates-engine/internal/sources/external"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// ─── ratesengine-ops backfill ──────────────────────────────────
//
// Replays a bounded ledger range through the same dispatcher +
// decoder + sink path the live indexer uses, producing trade rows
// into the trades hypertable. CAGGs (1m / 15m / 1h / 4h / 1d / 1w /
// 1mo per migration 0002) auto-materialise on the inserted rows.
//
// Differs from the indexer in three load-bearing ways:
//
//  1. Bounded range [-from, -to]. ledgerstream.Stream exits at -to;
//     no live tail. Backfill is a one-shot operation.
//  2. No cursor row written. The indexer's `ledgerstream` cursor
//     drives "resume from cursor+1" on restart. Backfill has its
//     own explicit -from; if it crashed and were to share that
//     cursor, the indexer would mis-resume from a historical
//     ledger on its next start.
//  3. BackfillSafe gate. internal/sources/external.Registry marks
//     every on-chain Soroban source `BackfillSafe=false` until its
//     decoder has been audited against every WASM version that ran
//     for the replay range (CLAUDE.md "Soroban DeFi contracts
//     upgrade in place"). Backfill refuses to run an unsafe source.
//
// Trade-row idempotency is the storage layer's responsibility — the
// trades hypertable currently dedupes on (source, ledger, tx_hash,
// op_index, ts), so re-running over the same range is a no-op only
// when the replay reproduces the same timestamp too. Aggregator CAGGs
// recompute from the underlying rows so duplicate suppression at
// insert time is sufficient once that storage identity matches.

// backfillOpts holds the parsed + validated CLI inputs. Pulled out
// of the entry point so flag-parsing + validation are unit-testable
// without executing the pipeline.
type backfillOpts struct {
	cfgPath  string
	from     uint32
	to       uint32
	sources  []string // resolved: -source override or cfg.Ingestion.EnabledSources
	bucket   string   // resolved: -bucket override or cfg.Storage.S3BucketArchive
	dryRun   bool
	resume   bool // when true, look up the prior cursor and skip already-processed ledgers
	parallel int  // number of concurrent chunks to run; 1 = sequential (default)
}

// chunkRange is one sub-range of a parallel backfill: [from, to]
// inclusive. Workers process distinct chunkRanges concurrently;
// each writes its own cursor row keyed on the chunk-specific
// (from, to) so resume-on-restart works per-chunk.
type chunkRange struct {
	from uint32
	to   uint32
}

// planBackfillChunks splits [from, to] into n contiguous,
// non-overlapping sub-ranges. The last chunk absorbs any rounding
// remainder so the union of the chunks covers [from, to] exactly.
//
// Caller invariants (enforced by parseBackfillFlags): n >= 1,
// to >= from. With n == 1, returns the original range as a single
// chunk (sequential mode, same shape as the pre-parallelism path).
func planBackfillChunks(from, to uint32, n int) []chunkRange {
	if n <= 1 {
		return []chunkRange{{from: from, to: to}}
	}
	total := uint64(to) - uint64(from) + 1
	size := total / uint64(n)
	if size == 0 {
		// More workers than ledgers; degrade to one chunk per ledger
		// up to the range size, rest unused.
		size = 1
	}
	out := make([]chunkRange, 0, n)
	cur := uint64(from)
	for i := 0; i < n && cur <= uint64(to); i++ {
		end := cur + size - 1
		if i == n-1 || end > uint64(to) {
			end = uint64(to)
		}
		out = append(out, chunkRange{from: uint32(cur), to: uint32(end)})
		cur = end + 1
	}
	return out
}

// backfillCursorSource is the value stamped on every backfill
// cursor row in the ingestion_cursors table. Distinct from the
// indexer's "ledgerstream" so a backfill crash + restart does NOT
// pollute the indexer's resume position.
const backfillCursorSource = "backfill"

// backfillCursorSub returns the sub-source key that distinguishes
// concurrent / overlapping backfill runs. We need the (from, to,
// sorted-sources) tuple in the key so two operators replaying
// different ranges or different source subsets don't share a cursor
// row and step on each other.
func backfillCursorSub(opts backfillOpts) string {
	sorted := make([]string, len(opts.sources))
	copy(sorted, opts.sources)
	sort.Strings(sorted)
	return fmt.Sprintf("%d-%d:%s", opts.from, opts.to, strings.Join(sorted, ","))
}

func backfill(args []string) error {
	opts, cfg, err := parseBackfillFlags(args)
	if err != nil {
		return err
	}

	if opts.dryRun {
		chunks := planBackfillChunks(opts.from, opts.to, opts.parallel)
		_, _ = fmt.Fprintf(os.Stderr,
			"backfill dry-run:\n  range:    [%d, %d] (%d ledgers)\n  sources:  %v\n  bucket:   %s\n  parallel: %d (chunks: %d)\n",
			opts.from, opts.to, opts.to-opts.from+1, opts.sources, opts.bucket, opts.parallel, len(chunks))
		for i, c := range chunks {
			_, _ = fmt.Fprintf(os.Stderr, "  chunk %d: [%d, %d] (%d ledgers)\n", i, c.from, c.to, c.to-c.from+1)
		}
		return nil
	}

	rootCtx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	logger := mkBackfillLogger()

	store, err := timescale.Open(rootCtx, cfg.Storage.PostgresDSN)
	if err != nil {
		return fmt.Errorf("storage: %w", err)
	}
	defer func() { _ = store.Close() }()

	chunks := planBackfillChunks(opts.from, opts.to, opts.parallel)
	logger.Info("backfill starting",
		"from", opts.from,
		"to", opts.to,
		"sources", opts.sources,
		"bucket", opts.bucket,
		"parallel", opts.parallel,
		"chunks", len(chunks),
	)

	// Sequential fast-path. Same shape as the pre-parallelism code,
	// minus the redundant goroutine + channel hop. Lets `-parallel 1`
	// (the default) keep its existing semantics: one cursor row, one
	// ledgerstream, one events channel.
	if len(chunks) == 1 {
		return runBackfillChunk(rootCtx, logger, opts, cfg, store, chunks[0])
	}

	// Parallel path. Each chunk is independent: its own dispatcher,
	// its own events channel, its own PersistEvents goroutine, its
	// own cursor row keyed on the chunk-specific (from, to). The
	// shared store's connection pool fans across chunks (postgres
	// max_connections is the only ceiling — typical 100 vs ~3 conns
	// per chunk = 30+ chunks supported on stock config).
	var wg sync.WaitGroup
	errCh := make(chan error, len(chunks))
	for i, c := range chunks {
		wg.Add(1)
		go func(i int, c chunkRange) {
			defer wg.Done()
			chunkLogger := logger.With("chunk", i, "chunk_from", c.from, "chunk_to", c.to)
			if err := runBackfillChunk(rootCtx, chunkLogger, opts, cfg, store, c); err != nil {
				errCh <- fmt.Errorf("chunk %d [%d, %d]: %w", i, c.from, c.to, err)
			}
		}(i, c)
	}
	wg.Wait()
	close(errCh)

	var combined []error
	for e := range errCh {
		combined = append(combined, e)
	}
	if len(combined) > 0 {
		return errors.Join(combined...)
	}

	logger.Info("backfill complete",
		"from", opts.from,
		"to", opts.to,
		"ledgers", opts.to-opts.from+1,
		"parallel", opts.parallel,
	)
	return nil
}

// runBackfillChunk processes a single chunkRange end-to-end:
// dispatcher build → events channel → PersistEvents goroutine →
// ledgerstream over [chunk.from, chunk.to] → cursor upsert per
// ledger. Cursor sub_source is chunk-specific (uses chunk.from /
// chunk.to, NOT the overall opts.from / opts.to) so concurrent
// chunks never share a cursor row.
func runBackfillChunk(ctx context.Context, logger *slog.Logger, opts backfillOpts, cfg config.Config, store *timescale.Store, chunk chunkRange) error {
	chunkOpts := opts
	chunkOpts.from = chunk.from
	chunkOpts.to = chunk.to
	cursorSub := backfillCursorSub(chunkOpts)

	startFrom := chunk.from
	if opts.resume {
		c, err := store.GetCursor(ctx, backfillCursorSource, cursorSub)
		switch {
		case errors.Is(err, timescale.ErrNotFound):
			logger.Info("no prior cursor — starting from chunk-from", "from", chunk.from)
		case err != nil:
			return fmt.Errorf("load resume cursor: %w", err)
		case c.LastLedger >= chunk.to:
			logger.Info("prior cursor at or past chunk-to — already complete",
				"cursor", c.LastLedger, "to", chunk.to)
			return nil
		default:
			startFrom = c.LastLedger + 1
			logger.Info("resuming from prior cursor",
				"cursor", c.LastLedger,
				"start_from", startFrom,
				"to", chunk.to,
				"remaining", chunk.to-startFrom+1)
		}
	}

	// Soroswap pair registry — load and arm live-upsert. Each chunk
	// runs its own dispatcher so each calls this independently; the
	// store is shared so chunks see each other's upserted pairs on
	// next load. See internal/pipeline/soroswap_registry.go.
	soroswapOpts, err := pipeline.SoroswapPersistenceOptions(ctx, store, logger, ctx)
	if err != nil {
		return fmt.Errorf("soroswap registry: %w", err)
	}
	disp, err := pipeline.BuildDispatcher(opts.sources, cfg.Oracle, soroswapOpts...)
	if err != nil {
		return fmt.Errorf("build dispatcher: %w", err)
	}

	events := make(chan consumer.Event, 256)
	sinkDone := make(chan struct{})
	go func() {
		defer close(sinkDone)
		pipeline.PersistEvents(ctx, logger, store, events)
	}()

	streamCfg := pipeline.LedgerstreamConfig(cfg, opts.bucket)
	streamErr := ledgerstream.Stream(ctx, streamCfg, startFrom, chunk.to,
		func(lcm sdkxdr.LedgerCloseMeta) error {
			if err := pipeline.ProcessLedger(ctx, disp, events, logger, lcm, cfg.Stellar.Passphrase()); err != nil {
				return err
			}
			if err := store.UpsertCursor(ctx, backfillCursorSource, cursorSub, lcm.LedgerSequence()); err != nil {
				logger.Warn("backfill cursor upsert",
					"ledger", lcm.LedgerSequence(),
					"err", err)
			}
			return nil
		},
	)

	close(events)
	<-sinkDone

	if streamErr != nil && !errors.Is(streamErr, context.Canceled) {
		return fmt.Errorf("stream: %w", streamErr)
	}

	logger.Info("chunk complete",
		"from", chunk.from,
		"to", chunk.to,
		"ledgers", chunk.to-chunk.from+1,
	)
	return nil
}

// parseBackfillFlags parses CLI args, loads config, and validates the
// result. Returns the resolved opts + the loaded config so the entry
// point can wire them up. Returns a non-nil error on any validation
// failure, including the BackfillSafe gate.
//
// Split out from backfill() so unit tests can drive validation
// without spinning up postgres + galexie.
func parseBackfillFlags(args []string) (backfillOpts, config.Config, error) {
	var opts backfillOpts
	var cfg config.Config

	fs := flag.NewFlagSet("backfill", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "path to ratesengine.toml (required)")
	from := fs.Uint("from", 0, "starting ledger sequence (inclusive, required)")
	to := fs.Uint("to", 0, "ending ledger sequence (inclusive, required)")
	sourceCSV := fs.String("source", "",
		"comma-separated source names; default = cfg.Ingestion.EnabledSources")
	bucketOverride := fs.String("bucket", "",
		"galexie bucket override; default = cfg.Storage.S3BucketArchive")
	dryRun := fs.Bool("dry-run", false,
		"validate config + sources + range, then exit without running")
	resume := fs.Bool("resume", false,
		"continue from a prior backfill cursor (keyed on -from/-to/-source). "+
			"On a fresh range with no prior cursor, behaves the same as without "+
			"-resume. Idempotent: re-runs over already-processed ledgers are a "+
			"no-op via the trades hypertable's unique index.")
	parallel := fs.Int("parallel", 1,
		"number of concurrent chunks (default 1 = sequential). The range is "+
			"split into N contiguous, non-overlapping sub-ranges; each chunk "+
			"runs its own dispatcher + ledgerstream + sink with a chunk-specific "+
			"cursor row, so -resume picks up per-chunk on restart. Throughput "+
			"scales linearly with cores until postgres max_connections or the "+
			"galexie bucket's S3 list throughput becomes the bottleneck "+
			"(typical safe range: 4-16 on a 16-core box).")
	if err := fs.Parse(args); err != nil {
		return opts, cfg, err
	}

	if *cfgPath == "" {
		return opts, cfg, errors.New("-config required")
	}
	if *from == 0 {
		return opts, cfg, errors.New("-from must be > 0 (refuse to default to genesis)")
	}
	if *to <= *from {
		return opts, cfg, fmt.Errorf("-to (%d) must be > -from (%d)", *to, *from)
	}
	if *parallel < 1 {
		return opts, cfg, fmt.Errorf("-parallel (%d) must be >= 1", *parallel)
	}

	loaded, err := config.LoadWithEnv(*cfgPath)
	if err != nil {
		return opts, cfg, fmt.Errorf("load config: %w", err)
	}
	cfg = loaded

	sources := cfg.Ingestion.EnabledSources
	if *sourceCSV != "" {
		sources = splitCSV(*sourceCSV)
	}
	if len(sources) == 0 {
		return opts, cfg, errors.New("no sources to backfill — set -source or cfg.Ingestion.EnabledSources")
	}

	if unsafeSources := unsafeBackfillSources(sources); len(unsafeSources) > 0 {
		return opts, cfg, fmt.Errorf(
			"refusing to backfill — sources not BackfillSafe (per-WASM-hash audit pending): %v; "+
				"run ratesengine-ops wasm-history -from %d -to %d -contracts <CID> for each on-chain source, "+
				"review every emitted WASM hash against the current decoder, then flip BackfillSafe=true in "+
				"internal/sources/external/registry.go in the same PR (see CLAUDE.md \"Soroban DeFi contracts "+
				"upgrade in place\")",
			unsafeSources, *from, *to)
	}

	bucket := cfg.Storage.S3BucketArchive
	if *bucketOverride != "" {
		bucket = *bucketOverride
	}
	if bucket == "" {
		return opts, cfg, errors.New("no bucket — set -bucket or cfg.Storage.S3BucketArchive")
	}

	opts = backfillOpts{
		cfgPath:  *cfgPath,
		from:     uint32(*from),
		to:       uint32(*to),
		sources:  sources,
		bucket:   bucket,
		dryRun:   *dryRun,
		resume:   *resume,
		parallel: *parallel,
	}
	return opts, cfg, nil
}

// unsafeBackfillSources filters `sources` to those whose registry
// entry has BackfillSafe=false. The intent is to fail fast on a list
// the operator can paste into a wasm-history audit ticket.
func unsafeBackfillSources(sources []string) []string {
	var out []string
	for _, s := range sources {
		if !external.BackfillSafe(s) {
			out = append(out, s)
		}
	}
	return out
}

// mkBackfillLogger returns a slog.Logger configured for one-shot
// ops invocations: text format on stderr, info level. Verbose
// debugging is rare — operators typically run backfill from a tmux
// session and want readable progress lines, not structured JSON.
func mkBackfillLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
}

// splitCSV (used here for -source parsing) is defined in
// cross_region_check.go — kept there so this binary has one
// canonical comma-splitting helper across subcommands.
