package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/RatesEngine/rates-engine/internal/config"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// resume-stalled — resume every stalled backfill cursor with a
// remaining range, producing a sequence of `backfill -resume`-shape
// invocations that march each cursor to its assigned `to` ledger.
//
// Why this exists. The audit-2026-05-26 density read (F-0020 / F-0028
// cluster) showed 167 stalled `backfill` cursors with cumulative
// 100-150 K missing ledgers per source — the dominant population
// preventing 100% decoder density. Each stalled cursor's sub_source
// embeds its target as `<from>-<to>:<decoder-csv>`, so the remaining
// range is well-defined; what was missing was a one-shot way to
// resume every stall without a hand-rolled SQL+shell loop.
//
// This subcommand:
//
//  1. Queries `ingestion_cursors` for `source LIKE 'backfill%'` rows
//     whose `last_updated` is older than `--min-lag` AND whose
//     `last_ledger` is strictly less than the parsed `to` of the
//     range encoded in their `sub_source`.
//  2. For each, derives `[last_ledger+1, to]` + the decoder CSV.
//  3. Invokes the same `runBackfillChunk` path the regular
//     `backfill` subcommand uses, with `-resume` semantics so
//     idempotent re-runs are safe.
//
// Sequencing: stalled cursors run sequentially in this first cut
// — operators wanting concurrency can launch multiple invocations
// against disjoint `--source-filter` regexes. Per-cursor failure
// is logged + continued; the subcommand returns non-zero only when
// at least one cursor errored.
//
// See `docs/operations/backfill-with-live-ingest.md` for the
// posture this subcommand fits into (resume runs at reduced
// parallelism while live ingest is active).
const stalledCursorSubPattern = `^(\d+)-(\d+):(.+)$`

var stalledCursorSubRE = regexp.MustCompile(stalledCursorSubPattern)

// stalledCursorPlan describes one cursor's remaining work. Produced
// by parseStalledCursor + filtered by planResumeStalled before any
// runBackfillChunk call is made.
type stalledCursorPlan struct {
	cursor     timescale.Cursor
	rangeFrom  uint32   // last_ledger + 1
	rangeTo    uint32   // parsed `to` from sub_source
	sources    []string // decoder CSV, sorted
	skip       bool
	skipReason string
}

// parseStalledCursor extracts the from/to/decoder triple from a
// backfill cursor's sub_source ("<from>-<to>:<decoders>") and
// computes the remaining resume range. Returns a plan with
// skip=true + a non-empty skipReason when the cursor isn't
// actionable (already at target; unparseable sub_source).
func parseStalledCursor(c timescale.Cursor) stalledCursorPlan {
	p := stalledCursorPlan{cursor: c}
	m := stalledCursorSubRE.FindStringSubmatch(c.Sub)
	if m == nil {
		p.skip = true
		p.skipReason = "sub_source doesn't match `<from>-<to>:<decoders>` shape"
		return p
	}
	fromN, err := strconv.ParseUint(m[1], 10, 32)
	if err != nil {
		p.skip = true
		p.skipReason = fmt.Sprintf("parse from: %v", err)
		return p
	}
	toN, err := strconv.ParseUint(m[2], 10, 32)
	if err != nil {
		p.skip = true
		p.skipReason = fmt.Sprintf("parse to: %v", err)
		return p
	}
	if c.LastLedger >= uint32(toN) {
		p.skip = true
		p.skipReason = fmt.Sprintf("last_ledger %d already at-or-past target %d (stale-by-time, not by-position)", c.LastLedger, toN)
		return p
	}
	if uint32(fromN) > c.LastLedger+1 {
		// The cursor started later than its sub_source declares.
		// Shouldn't happen in well-formed cursors but worth flagging
		// rather than computing a negative-or-rewinding range.
		p.skip = true
		p.skipReason = fmt.Sprintf("declared from %d > last_ledger+1 %d — cursor inconsistent", fromN, c.LastLedger+1)
		return p
	}
	srcs := splitCSV(m[3])
	if len(srcs) == 0 {
		p.skip = true
		p.skipReason = "empty decoder set"
		return p
	}
	sort.Strings(srcs)
	p.rangeFrom = c.LastLedger + 1
	p.rangeTo = uint32(toN)
	p.sources = srcs
	return p
}

// resumeStalled is the subcommand entry point.
func resumeStalled(args []string) error {
	fs := flag.NewFlagSet("resume-stalled", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "path to ratesengine.toml (required)")
	minLag := fs.Duration("min-lag", time.Hour,
		"only resume cursors stalled longer than this (lag = now - last_updated)")
	maxResumes := fs.Int("max-resumes", 0,
		"cap on cursors resumed in this run (0 = no cap). Safety knob "+
			"for large stall populations; operators typically pass 1-5 "+
			"first to validate behaviour, then 0 for the full sweep.")
	sourceFilter := fs.String("source-filter", "",
		"only resume cursors whose decoder CSV contains this source name "+
			"as a substring (e.g. \"defindex\" to resume just defindex stalls). "+
			"Empty = all stalled cursors.")
	bucketOverride := fs.String("bucket", "",
		"galexie bucket override; default = cfg.Storage.S3BucketArchive")
	dryRun := fs.Bool("dry-run", false,
		"print the resume plan + exit without invoking backfill")
	parallel := fs.Int("parallel", 1,
		"per-cursor backfill parallelism (default 1 = sequential within "+
			"each cursor). The outer loop across cursors is always "+
			"sequential — concurrent operator invocations against disjoint "+
			"`--source-filter` values are the parallel-across-cursors path.")
	refreshCAGGs := fs.Bool("refresh-caggs", true,
		"forward to runBackfillChunk's CAGG-refresh path (same default as "+
			"the regular `backfill` subcommand). Disable only when debugging "+
			"a specific refresh failure.")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *cfgPath == "" {
		return errors.New("-config required")
	}
	if *minLag < 0 {
		return fmt.Errorf("-min-lag (%s) must be >= 0", *minLag)
	}
	if *parallel < 1 {
		return fmt.Errorf("-parallel (%d) must be >= 1", *parallel)
	}

	cfg, err := config.LoadWithEnv(*cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	bucket := cfg.Storage.S3BucketArchive
	if *bucketOverride != "" {
		bucket = *bucketOverride
	}
	if bucket == "" {
		return errors.New("no bucket — set -bucket or cfg.Storage.S3BucketArchive")
	}

	rootCtx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	logger := mkBackfillLogger()

	store, err := timescale.Open(rootCtx, cfg.Storage.PostgresDSN)
	if err != nil {
		return fmt.Errorf("storage: %w", err)
	}
	defer func() { _ = store.Close() }()

	plans, err := planResumeStalled(rootCtx, store, *minLag, *sourceFilter, *maxResumes)
	if err != nil {
		return fmt.Errorf("plan: %w", err)
	}

	actionable := 0
	for _, p := range plans {
		if !p.skip {
			actionable++
		}
	}
	logger.Info("resume-stalled plan",
		"candidates", len(plans),
		"actionable", actionable,
		"skipped", len(plans)-actionable,
		"min_lag", minLag.String(),
		"source_filter", *sourceFilter,
		"dry_run", *dryRun,
	)

	if *dryRun || actionable == 0 {
		for _, p := range plans {
			printResumePlan(p)
		}
		return nil
	}

	failures := executeResumePlans(rootCtx, logger, plans, *cfgPath, bucket, *parallel, *refreshCAGGs, cfg, store)
	if len(failures) > 0 {
		return fmt.Errorf("resume-stalled: %d of %d cursors failed: %w",
			len(failures), actionable, errors.Join(failures...))
	}
	logger.Info("resume-stalled: all cursors complete",
		"cursors", actionable,
	)
	return nil
}

// executeResumePlans iterates the planned cursors and runs each one
// via runResumeForCursor. Per-cursor failures are collected + the
// loop continues; the outer subcommand decides whether to return an
// error based on the accumulated list. Extracted from resumeStalled
// to stay under the gocognit ceiling.
func executeResumePlans(
	ctx context.Context,
	logger *slog.Logger,
	plans []stalledCursorPlan,
	cfgPath string,
	bucket string,
	parallel int,
	refreshCAGGs bool,
	cfg config.Config,
	store *timescale.Store,
) []error {
	var failures []error
	for _, p := range plans {
		if p.skip {
			logger.Info("resume-stalled: skipping cursor",
				"sub_source", p.cursor.Sub,
				"last_ledger", p.cursor.LastLedger,
				"reason", p.skipReason,
			)
			continue
		}
		if err := runOneCursorPlan(ctx, logger, p, cfgPath, bucket, parallel, refreshCAGGs, cfg, store); err != nil {
			failures = append(failures, fmt.Errorf("cursor %q: %w", p.cursor.Sub, err))
		}
	}
	return failures
}

// runOneCursorPlan wires one stalledCursorPlan through the regular
// backfill chunk path. Same shape the `backfill` subcommand uses for
// a single user-specified range — just driven from the cursor row
// instead of CLI flags.
func runOneCursorPlan(
	ctx context.Context,
	logger *slog.Logger,
	p stalledCursorPlan,
	cfgPath string,
	bucket string,
	parallel int,
	refreshCAGGs bool,
	cfg config.Config,
	store *timescale.Store,
) error {
	opts := backfillOpts{
		cfgPath:      cfgPath,
		from:         p.rangeFrom,
		to:           p.rangeTo,
		sources:      p.sources,
		bucket:       bucket,
		resume:       true, // monotonic-advance on the existing cursor row
		parallel:     parallel,
		refreshCAGGs: refreshCAGGs,
	}
	chunks := planBackfillChunks(opts.from, opts.to, opts.parallel)
	cursorLogger := logger.With(
		"cursor_sub_source", p.cursor.Sub,
		"cursor_last_ledger", p.cursor.LastLedger,
		"resume_from", p.rangeFrom,
		"resume_to", p.rangeTo,
		"sources", p.sources,
	)
	cursorLogger.Info("resume-stalled: cursor start", "chunks", len(chunks))
	if err := runResumeForCursor(ctx, cursorLogger, opts, cfg, store, chunks); err != nil {
		cursorLogger.Error("resume-stalled: cursor failed", "err", err)
		return err
	}
	cursorLogger.Info("resume-stalled: cursor complete")
	return nil
}

// runResumeForCursor runs the chunk loop for a single cursor's
// remaining range. Identical-shape to the regular `backfill`
// subcommand's loop (sequential fast-path for single chunk; goroutine
// fan-out for parallel > 1). Extracted so the outer cursor loop in
// resumeStalled stays readable.
func runResumeForCursor(
	ctx context.Context,
	logger *slog.Logger,
	opts backfillOpts,
	cfg config.Config,
	store *timescale.Store,
	chunks []chunkRange,
) error {
	if len(chunks) == 1 {
		return runBackfillChunk(ctx, logger, opts, cfg, store, chunks[0])
	}
	type result struct{ err error }
	resultCh := make(chan result, len(chunks))
	for i, c := range chunks {
		go func(i int, c chunkRange) {
			chunkLogger := logger.With("chunk", i, "chunk_from", c.from, "chunk_to", c.to)
			err := runBackfillChunk(ctx, chunkLogger, opts, cfg, store, c)
			if err != nil {
				resultCh <- result{err: fmt.Errorf("chunk %d [%d, %d]: %w", i, c.from, c.to, err)}
				return
			}
			resultCh <- result{}
		}(i, c)
	}
	var combined []error
	for range chunks {
		if r := <-resultCh; r.err != nil {
			combined = append(combined, r.err)
		}
	}
	if len(combined) > 0 {
		return errors.Join(combined...)
	}
	return nil
}

// planResumeStalled is the read-side of the subcommand: gather + filter
// + compute plans, no side effects. Split out so the dry-run path uses
// the exact same logic as the apply path.
func planResumeStalled(
	ctx context.Context,
	store *timescale.Store,
	minLag time.Duration,
	sourceFilter string,
	maxResumes int,
) ([]stalledCursorPlan, error) {
	rows, err := store.ListCursors(ctx)
	if err != nil {
		return nil, fmt.Errorf("list cursors: %w", err)
	}
	now := time.Now().UTC()
	var plans []stalledCursorPlan
	for _, c := range rows {
		if !strings.HasPrefix(c.Source, "backfill") {
			continue
		}
		lag := now.Sub(c.UpdatedAt)
		if lag < minLag {
			continue
		}
		if sourceFilter != "" && !strings.Contains(c.Sub, sourceFilter) {
			continue
		}
		plans = append(plans, parseStalledCursor(c))
		if maxResumes > 0 && len(plans) >= maxResumes {
			break
		}
	}
	return plans, nil
}

// printResumePlan emits one human-readable line per cursor describing
// what the apply pass would do (or why it would skip). Used by the
// dry-run path.
func printResumePlan(p stalledCursorPlan) {
	if p.skip {
		fmt.Fprintf(os.Stderr, "  SKIP  %s  reason=%s\n",
			truncate(p.cursor.Sub, 65), p.skipReason)
		return
	}
	fmt.Fprintf(os.Stderr, "  PLAN  %s  resume=[%d, %d] (%d ledgers) sources=%v\n",
		truncate(p.cursor.Sub, 65), p.rangeFrom, p.rangeTo,
		p.rangeTo-p.rangeFrom+1, p.sources)
}
