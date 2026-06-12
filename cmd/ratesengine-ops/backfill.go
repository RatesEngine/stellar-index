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
	"time"

	sdkxdr "github.com/stellar/go-stellar-sdk/xdr"

	"github.com/RatesEngine/rates-engine/internal/config"
	"github.com/RatesEngine/rates-engine/internal/consumer"
	"github.com/RatesEngine/rates-engine/internal/dispatcher"
	"github.com/RatesEngine/rates-engine/internal/ledgerstream"
	"github.com/RatesEngine/rates-engine/internal/pipeline"
	"github.com/RatesEngine/rates-engine/internal/sources/external"
	"github.com/RatesEngine/rates-engine/internal/sources/sorobanevents"
	"github.com/RatesEngine/rates-engine/internal/sources/soroswap"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// SorobanEventsPseudoSource is the backfill-only source name that
// populates the `soroban_events` raw-event landing zone (ADR-0029).
// It is NOT registered with internal/sources/external.Registry —
// soroban-events is a pseudo-source: the catch-all dispatcher hook
// sees every event regardless of per-source decoder routing, so
// the backfill swaps the per-source decoder chain for "no decoders,
// raw sink only" when this name is requested.
//
// Used as -source soroban-events on the ops command line. Not
// permitted alongside other sources in the same invocation (the
// pseudo-source isn't a peer of trades / oracles — it's the
// distinct "raw-event capture" mode of operation).
const SorobanEventsPseudoSource = "soroban-events"

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
	// refreshCAGGs controls the post-chunk continuous-aggregate
	// materialisation call. Defaults to true. Pre-2026-05-13
	// backfills did not refresh CAGGs and consequently lost their
	// data to the 90-day raw-trades retention before the policy
	// refresher's natural cadence picked the inserts up. Operators
	// should leave this on; the only legitimate reason to disable
	// is debugging a refresh failure where re-running the
	// underlying chunk is the desired recovery path.
	refreshCAGGs bool
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

// buildChunkDispatcher constructs the per-chunk dispatcher and,
// when the soroban-events pseudo-source is in play, wires the
// RawEventSink (ADR-0029). Returns the dispatcher + the sink (nil
// when not pseudo so the caller can branch on it for teardown).
//
// Factored out of runBackfillChunk to keep that function within
// the funlen limit; the dispatcher-construction surface had grown
// to ~50 lines that read linearly but blew the limit when combined
// with the chunk lifecycle.
func buildChunkDispatcher(
	ctx context.Context,
	logger *slog.Logger,
	opts backfillOpts,
	cfg config.Config,
	store *timescale.Store,
	pseudo bool,
) (*dispatcher.Dispatcher, *sorobanevents.AsyncSink, error) {
	realSources := filterOutSorobanEventsPseudo(opts.sources)

	var soroswapOpts []soroswap.DecoderOption
	if !pseudo && len(realSources) > 0 {
		// Soroswap pair registry — load and arm live-upsert. Each
		// chunk runs its own dispatcher so each calls this
		// independently; the store is shared so chunks see each
		// other's upserted pairs on next load. See
		// internal/pipeline/soroswap_registry.go.
		var err error
		soroswapOpts, err = pipeline.SoroswapPersistenceOptions(ctx, store, logger, ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("soroswap registry: %w", err)
		}
	}
	// gated=nil: backfill writes the raw soroban_events landing + the
	// non-projected sinks; the projected sources' (blend, …) decoder output
	// is dropped by pipeline.IsProjectedEvent, so an empty gate registry
	// here only suppresses output that would be discarded anyway. The
	// projector is the sole writer of those tables and warms its own gate.
	disp, err := pipeline.BuildDispatcher(realSources, cfg.Oracle, nil, soroswapOpts...)
	if err != nil {
		return nil, nil, fmt.Errorf("build dispatcher: %w", err)
	}
	if !pseudo {
		return disp, nil, nil
	}

	// soroban-events pseudo-source: wire the RawEventSink (ADR-0029).
	// Sink lifecycle is local to this chunk so a parallel-N backfill
	// gets N independent sinks each with their own batch buffer.
	// Stop()'d at chunk end to flush any partial batch.
	rawSink := sorobanevents.NewAsyncSink(store, sorobanevents.AsyncSinkOptions{
		BufferSize:    4096,
		BatchSize:     1000,
		FlushInterval: time.Second,
		WriteTimeout:  10 * time.Second,
		Logger:        logger.With("component", "soroban-events-sink"),
	})
	rawSink.Start()
	disp.SetRawEventSink(rawSink)
	return disp, rawSink, nil
}

// runBackfillChunk processes a single chunkRange end-to-end:
// dispatcher build → events channel → PersistEvents goroutine →
// ledgerstream over [chunk.from, chunk.to] → cursor upsert per
// ledger. Cursor sub_source is chunk-specific (uses chunk.from /
// chunk.to, NOT the overall opts.from / opts.to) so concurrent
// chunks never share a cursor row.
//
//nolint:gocognit,funlen // chunk lifecycle is linear setup → stream → teardown; splitting reduces readability of dependency-construction order.
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

	pseudo := hasSorobanEventsPseudo(opts.sources)
	disp, rawSink, err := buildChunkDispatcher(ctx, logger, opts, cfg, store, pseudo)
	if err != nil {
		return err
	}

	events := make(chan consumer.Event, 256)
	sinkDone := make(chan struct{})
	go func() {
		defer close(sinkDone)
		// Backfill always uses SinkModeAll — the projector only runs
		// in the indexer, not in `ratesengine-ops backfill`, so this
		// subcommand keeps writing every event class itself. See
		// ADR-0032 § "Out of scope for projector".
		pipeline.PersistEvents(ctx, logger, store, events, pipeline.SinkModeAll)
	}()

	// Ctx-cancel safety net for the raw-event sink (ADR-0029).
	// PushEvent applies back-pressure (blocks when the buffer is
	// full) — required for cursor coherence — but the dispatcher's
	// hot path has no ctx awareness, so a blocked PushEvent would
	// pin the Stream callback even after rootCtx is cancelled. If
	// ctx fires while the producer is mid-dispatch we early-Stop
	// the sink so blocked PushEvent calls unblock via the stopping
	// channel, the dispatcher returns, and ledgerstream.Stream can
	// honour cancellation.
	if rawSink != nil {
		go func() {
			select {
			case <-ctx.Done():
				rawSink.Stop()
			case <-sinkDone:
			}
		}()
	}

	streamCfg := pipeline.LedgerstreamConfig(cfg, opts.bucket)
	// Count actual LCM callbacks so the chunk-complete log line can
	// distinguish "this chunk walked N ledgers" from "the chunk
	// covered an N-ledger range." F-0159 (2026-05-26): a backfill
	// run against a bucket with no files in the target range logged
	// `chunk complete ... ledgers=5331` and exited in 200ms — the
	// `ledgers=` value was the chunk's [from,to] range size, not the
	// count of ledgers actually walked. Operators read the log as a
	// false-positive "backfill complete" and moved on without the
	// gap being filled.
	var walked uint64
	streamErr := ledgerstream.Stream(ctx, streamCfg, startFrom, chunk.to,
		func(lcm sdkxdr.LedgerCloseMeta) error {
			walked++
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

	// Drain the soroban-events sink (if wired). PushEvent applies
	// back-pressure on a full buffer so the producer above (the
	// ledgerstream callback) slows down to match storage throughput;
	// produced rows are durably enqueued before the cursor advances.
	// Stop() signals the worker to drain the residual buffer and
	// flush the final batch, with the AsyncSink's per-batch timeout
	// capping the wait. DroppedCount only ticks up if a producer
	// raced past the stopping signal — investigate as a bug if
	// non-zero outside a forced shutdown.
	if rawSink != nil {
		rawSink.Stop()
		logger.Info("soroban-events sink drained",
			"written", rawSink.WrittenCount(),
			"dropped", rawSink.DroppedCount(),
			"skipped", rawSink.SkippedCount(),
		)
		if dropped := rawSink.DroppedCount(); dropped > 0 {
			logger.Warn("soroban-events: rows dropped at shutdown race",
				"dropped", dropped,
				"impact", "the dropped rows are NOT in soroban_events — investigate; only expected on hard kill")
		}
	}

	if streamErr != nil && !errors.Is(streamErr, context.Canceled) {
		return fmt.Errorf("stream: %w", streamErr)
	}

	// F-0159: report BOTH the chunk's range size and the count of
	// ledgers actually walked. The old `ledgers=` field was the
	// range size; if it didn't match `ledgers_walked` the bucket
	// was missing files. Fail loudly on a complete miss across a
	// non-zero range — that's almost always a bucket-mistargeting
	// bug (wrong --bucket flag, wrong endpoint, wrong region) and
	// the silent success was misleading enough to ship a
	// false-positive "gap filled" signal in production.
	chunkSize := chunk.to - chunk.from + 1
	logger.Info("chunk complete",
		"from", chunk.from,
		"to", chunk.to,
		"chunk_size_ledgers", chunkSize,
		"ledgers_walked", walked,
	)
	if chunkSize > 0 && walked == 0 {
		return fmt.Errorf(
			"backfill walked 0 of %d ledgers in range [%d,%d] from bucket %q — "+
				"bucket likely has no files in this range; check --bucket and the "+
				"galexie-archive/-live mirror for the target range",
			chunkSize, chunk.from, chunk.to, opts.bucket,
		)
	}

	// CAGG refresh path: skipped for the soroban-events pseudo-source
	// (the soroban_events hypertable has no CAGGs built on top of it
	// — future per-source decoders read from it directly via SELECT).
	if pseudo {
		logger.Info("skipping CAGG refresh — soroban-events has no CAGGs")
		return nil
	}

	// Force-materialise the long-lived CAGGs over the chunk's
	// timestamp range. Without this, historical inserts get dropped
	// by the 90-day retention policy on the raw trades table BEFORE
	// the policy refresher's natural cadence picks them up — which
	// is what happened to the May 2026 SDEX backfill (cursors
	// completed, trades inserted, retention dropped them within 24h,
	// no CAGG materialisation, ~80M trades of work lost).
	//
	// We refresh prices_1h / 4h / 1d / 1w / 1mo (the no-retention
	// CAGGs per migration 0002). prices_1m and prices_15m have
	// 30-day retention by design, so refreshing them for historical
	// ranges would just be wasted work.
	if opts.refreshCAGGs {
		if err := refreshCAGGsForChunk(ctx, logger, store, chunk); err != nil {
			return fmt.Errorf("post-chunk CAGG refresh: %w", err)
		}
	} else {
		logger.Warn("skipping CAGG refresh (-refresh-caggs=false)",
			"impact", "historical inserts will be dropped by 90-day retention before CAGG policy materialises them",
		)
	}
	return nil
}

// refreshCAGGsForChunk derives the ts range covered by the just-
// inserted trades and force-refreshes every long-lived CAGG over
// that range. Idempotent — re-refreshing an already-materialised
// range is a no-op. Soft-degrades on individual view failures so
// one wedged CAGG doesn't leave the others un-materialised.
func refreshCAGGsForChunk(ctx context.Context, logger *slog.Logger, store *timescale.Store, chunk chunkRange) error {
	tsFrom, tsTo, err := store.LedgerRangeToTimeRange(ctx, chunk.from, chunk.to)
	if err != nil {
		// No trades inserted in the chunk — nothing to refresh.
		// The dispatcher dropped every event (e.g. all sources
		// were disabled, or the range had no on-chain activity
		// matching any decoder).
		if errors.Is(err, timescale.ErrNotFound) {
			logger.Info("no trades in chunk — skipping CAGG refresh",
				"from", chunk.from, "to", chunk.to)
			return nil
		}
		return fmt.Errorf("derive ts range: %w", err)
	}
	logger.Info("refreshing CAGGs for chunk",
		"from", chunk.from, "to", chunk.to,
		"ts_from", tsFrom.UTC().Format(time.RFC3339),
		"ts_to", tsTo.UTC().Format(time.RFC3339),
	)
	for _, spec := range timescale.CAGGsLiveForever {
		// Pad the chunk's ts range to the per-CAGG minimum so the
		// refresh procedure doesn't reject with "refresh window
		// too small". For tiny chunks (10k ledgers ≈ 4h) the
		// padded area is mostly empty — Timescale iterates the
		// padded buckets quickly with nothing to materialize.
		padFrom, padTo := timescale.PadRefreshWindow(tsFrom, tsTo, spec.MinWindow)
		if err := store.RefreshContinuousAggregate(ctx, spec.Name, padFrom, padTo); err != nil {
			logger.Error("CAGG refresh failed",
				"view", spec.Name, "err", err)
			// Don't abort the chunk — log and continue. An
			// operator can re-refresh manually via psql; the
			// trade rows are still in the hypertable until next
			// retention run, so a same-day re-attempt works.
			continue
		}
	}
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
	refreshCAGGs := fs.Bool("refresh-caggs", true,
		"force-refresh the long-lived continuous aggregates "+
			"(prices_1h / 4h / 1d / 1w / 1mo) over each chunk's "+
			"timestamp range immediately after the trade-insert loop "+
			"completes. Required for historical backfills — without "+
			"this, the 90-day raw-trades retention will drop the just-"+
			"inserted chunks before the policy refresher's natural "+
			"cadence materialises them. Disable only when debugging a "+
			"specific refresh failure.")
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

	if err := checkBackfillSources(sources, uint32(*from), uint32(*to)); err != nil {
		return opts, cfg, err
	}

	// The soroban-events pseudo-source is exclusive: it captures
	// every event regardless of per-source decoder routing, so
	// running it alongside other sources would double-bill ledger
	// reads without changing what soroban_events sees. Refuse rather
	// than silently merge.
	if hasSorobanEventsPseudo(sources) && len(sources) > 1 {
		return opts, cfg, fmt.Errorf(
			"-source %q is exclusive — run it alone, not mixed with other source names %v",
			SorobanEventsPseudoSource, sources)
	}

	bucket := cfg.Storage.S3BucketArchive
	if *bucketOverride != "" {
		bucket = *bucketOverride
	}
	if bucket == "" {
		return opts, cfg, errors.New("no bucket — set -bucket or cfg.Storage.S3BucketArchive")
	}

	opts = backfillOpts{
		cfgPath:      *cfgPath,
		from:         uint32(*from),
		to:           uint32(*to),
		sources:      sources,
		bucket:       bucket,
		dryRun:       *dryRun,
		resume:       *resume,
		parallel:     *parallel,
		refreshCAGGs: *refreshCAGGs,
	}
	return opts, cfg, nil
}

// hasSorobanEventsPseudo reports whether `sources` contains the
// `soroban-events` pseudo-source name. The pseudo-source is the
// catch-all raw-event capture mode; it must be filtered out of
// every BackfillSafe / KnownSources check because it isn't in any
// of those registries — it's the dispatcher's RawEventSink seam.
func hasSorobanEventsPseudo(sources []string) bool {
	for _, s := range sources {
		if strings.EqualFold(s, SorobanEventsPseudoSource) {
			return true
		}
	}
	return false
}

// filterOutSorobanEventsPseudo returns `sources` with any
// `soroban-events` entry removed. Used to feed the remaining
// real-source list to BuildDispatcher / BackfillSafe checks.
func filterOutSorobanEventsPseudo(sources []string) []string {
	out := make([]string, 0, len(sources))
	for _, s := range sources {
		if strings.EqualFold(s, SorobanEventsPseudoSource) {
			continue
		}
		out = append(out, s)
	}
	return out
}

// checkBackfillSources returns nil when every source in `sources`
// is BackfillSafe, otherwise an error explaining which subset
// blocked the run and why. Distinguishes supply-observer names
// from genuinely audit-pending Soroban sources so the operator
// gets a targeted message rather than the generic WASM-audit one.
// F-1243 (audit-2026-05-12).
//
// Special-case: the `soroban-events` pseudo-source is exempt from
// the BackfillSafe gate. It captures raw events to the
// soroban_events landing zone without per-source decoding, so the
// "Soroban DeFi contracts upgrade in place" concern doesn't apply —
// the raw XDR is stored as-is for FUTURE decoders to interpret
// against the right WASM-version mapping.
func checkBackfillSources(sources []string, fromLedger, toLedger uint32) error {
	// Strip the pseudo-source before the BackfillSafe lookup; it
	// isn't in external.Registry by design.
	realSources := filterOutSorobanEventsPseudo(sources)
	unsafeSources := unsafeBackfillSources(realSources)
	if len(unsafeSources) == 0 {
		return nil
	}
	var supplyObservers, sorobanPending []string
	for _, s := range unsafeSources {
		if isKnownSupplyObserverName(s) {
			supplyObservers = append(supplyObservers, s)
		} else {
			sorobanPending = append(sorobanPending, s)
		}
	}
	if len(supplyObservers) > 0 {
		return fmt.Errorf(
			"refusing to backfill — these are supply observers, not price/oracle sources, and don't run "+
				"through `ratesengine-ops backfill`: %v; supply observers plug into the indexer's "+
				"LedgerEntryChange / OpDecoder / SEP-41 event hooks (there's no historical replay path here). "+
				"Use the supply-snapshot systemd timer for current state, or open a wasm-audit ticket if "+
				"you actually need a historical SEP-41 supply window — that's the only one that has a "+
				"chance of working with a future supply-backfill command (F-1243)",
			supplyObservers)
	}
	return fmt.Errorf(
		"refusing to backfill — sources not BackfillSafe (per-WASM-hash audit pending): %v; "+
			"run ratesengine-ops wasm-history -from %d -to %d -contracts <CID> for each on-chain source, "+
			"review every emitted WASM hash against the current decoder, then flip BackfillSafe=true in "+
			"internal/sources/external/registry.go in the same PR (see CLAUDE.md \"Soroban DeFi contracts "+
			"upgrade in place\")",
		sorobanPending, fromLedger, toLedger)
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

// knownSupplyObserverNames is the closed set of supply-observer
// package names the indexer registers. None of these are in
// external.Registry (supply observers plug into a different
// dispatcher hook than price/oracle sources) — but we want a
// targeted error message when an operator tries to backfill one,
// rather than the generic "WASM-hash audit pending" message that
// drove the F-1243 audit finding.
//
// Update this set when a new supply observer ships under
// internal/supply/. Keeping the list local to backfill.go avoids
// a cross-cutting "supply registry" abstraction for this single
// error-message use case.
var knownSupplyObserverNames = map[string]struct{}{
	"accounts":           {},
	"trustlines":         {},
	"claimable_balances": {},
	"sac_balances":       {},
	"sep41_supply":       {},
	"liquidity_pools":    {},
}

// isKnownSupplyObserverName reports whether `name` matches one of
// the known supply observers. Used by the backfill flag-parser to
// emit a tailored error rather than the generic BackfillSafe one.
func isKnownSupplyObserverName(name string) bool {
	_, ok := knownSupplyObserverNames[name]
	return ok
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
