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

	"github.com/StellarAtlas/stellar-atlas/internal/config"
	"github.com/StellarAtlas/stellar-atlas/internal/storage/timescale"
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

// soroban-aware source list. A stalled cursor whose decoder CSV
// contains any of these names is gated against the data-gap list
// from FindSorobanEventsLedgerGaps. SDEX (classic) is intentionally
// NOT in this list — its gap detection is a follow-up and requires
// a different statistical threshold (SDEX trade density across
// history is bursty in a way Soroban activity isn't).
var sorobanDecoderNames = map[string]struct{}{
	"aquarius":        {},
	"band":            {},
	"blend":           {},
	"cctp":            {},
	"comet":           {},
	"defindex":        {},
	"phoenix":         {},
	"redstone":        {},
	"reflector-cex":   {},
	"reflector-dex":   {},
	"reflector-fx":    {},
	"rozo":            {},
	"soroban-events":  {},
	"soroswap":        {},
	"soroswap-router": {},
}

// planHasSorobanDecoder reports whether any decoder in the plan's
// sources is Soroban-era — i.e. the plan's remaining range can be
// gated against the FindSorobanEventsLedgerGaps result. Mixed-set
// plans (containing both Soroban + SDEX decoders) count as Soroban
// for this gate: if the Soroban portion has no real gap, the SDEX
// portion is either (a) also clean (sibling cursor covered it) or
// (b) a real SDEX gap that the operator can find with future SDEX
// gap detection. Either way, walking the whole range to be safe is
// the F-0020 multi-day mistake; better to skip + flag for follow-up.
func planHasSorobanDecoder(sources []string) bool {
	for _, s := range sources {
		if _, ok := sorobanDecoderNames[s]; ok {
			return true
		}
	}
	return false
}

// gateAgainstDataGaps narrows the actionable plan list to those
// whose remaining range overlaps a real data-gap. For Soroban-era
// plans we have data-derived ground truth (soroban_events
// FindSorobanEventsLedgerGaps); for SDEX-only plans we currently
// don't, so they're skipped with `data-gap detection not available
// for SDEX` unless --force-classic-cursors is set.
//
// This is the F-0020 follow-up fix to resume-stalled: the original
// dry-run on r1 surfaced 50 "actionable" plans, most of which were
// false positives — sibling cursors had already completed the work
// and the data was in trades / soroban_events. Walking them would
// have been days of redundant LCM I/O.
func gateAgainstDataGaps(plans []stalledCursorPlan, gaps []timescale.LedgerGap, forceClassic bool) []stalledCursorPlan {
	out := make([]stalledCursorPlan, len(plans))
	copy(out, plans)
	for i := range out {
		if out[i].skip {
			continue
		}
		hasSoroban := planHasSorobanDecoder(out[i].sources)
		if !hasSoroban {
			if forceClassic {
				continue // operator opt-in: trust cursor inventory for SDEX
			}
			out[i].skip = true
			out[i].skipReason = "data-derived gap detection not yet implemented for non-Soroban decoders (SDEX). Pass --force-classic-cursors to act on cursor inventory alone."
			continue
		}
		if !overlapsAnyDataGap(out[i].rangeFrom, out[i].rangeTo, gaps) {
			out[i].skip = true
			out[i].skipReason = "remaining range fully covered by sibling cursors (no soroban_events gap overlap) — cursor inventory false-positive"
		}
	}
	return out
}

// overlapsAnyDataGap returns true if [from, to] intersects any gap
// in the sorted slice. O(n) linear scan — gaps slices are tiny
// (single-digit count in steady state) so a sort + binary-search
// would be premature.
func overlapsAnyDataGap(from, to uint32, gaps []timescale.LedgerGap) bool {
	planFrom := int64(from)
	planTo := int64(to)
	for _, g := range gaps {
		if g.End < planFrom || g.Start > planTo {
			continue
		}
		return true
	}
	return false
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
type resumeStalledOpts struct {
	cfgPath        string
	minLag         time.Duration
	maxResumes     int
	sourceFilter   string
	bucket         string
	dryRun         bool
	parallel       int
	refreshCAGGs   bool
	forceClassic   bool
	dataGapMinSize int64
}

func parseResumeStalledFlags(args []string) (resumeStalledOpts, config.Config, error) {
	var opts resumeStalledOpts
	var cfg config.Config
	fs := flag.NewFlagSet("resume-stalled", flag.ContinueOnError)
	fs.StringVar(&opts.cfgPath, "config", "", "path to ratesengine.toml (required)")
	fs.DurationVar(&opts.minLag, "min-lag", time.Hour,
		"only resume cursors stalled longer than this (lag = now - last_updated)")
	fs.IntVar(&opts.maxResumes, "max-resumes", 0,
		"cap on cursors resumed in this run (0 = no cap)")
	fs.StringVar(&opts.sourceFilter, "source-filter", "",
		"only resume cursors whose decoder CSV contains this source name")
	bucketOverride := fs.String("bucket", "",
		"galexie bucket override; default = cfg.Storage.S3BucketArchive")
	fs.BoolVar(&opts.dryRun, "dry-run", false,
		"print the resume plan + exit without invoking backfill")
	fs.IntVar(&opts.parallel, "parallel", 1,
		"per-cursor backfill parallelism (default 1 = sequential)")
	fs.BoolVar(&opts.refreshCAGGs, "refresh-caggs", true,
		"forward to runBackfillChunk's CAGG-refresh path")
	fs.BoolVar(&opts.forceClassic, "force-classic-cursors", false,
		"act on SDEX-only stalled cursors using cursor-inventory alone, "+
			"bypassing the data-derived gap gate")
	fs.Int64Var(&opts.dataGapMinSize, "data-gap-min-size", int64(timescale.GapDetectorMinGapSize),
		"threshold passed to FindSorobanEventsLedgerGaps for the data-gap gate")
	if err := fs.Parse(args); err != nil {
		return opts, cfg, err
	}
	if opts.cfgPath == "" {
		return opts, cfg, errors.New("-config required")
	}
	if opts.minLag < 0 {
		return opts, cfg, fmt.Errorf("-min-lag (%s) must be >= 0", opts.minLag)
	}
	if opts.parallel < 1 {
		return opts, cfg, fmt.Errorf("-parallel (%d) must be >= 1", opts.parallel)
	}
	loaded, err := config.LoadWithEnv(opts.cfgPath)
	if err != nil {
		return opts, cfg, fmt.Errorf("load config: %w", err)
	}
	cfg = loaded
	opts.bucket = cfg.Storage.S3BucketArchive
	if *bucketOverride != "" {
		opts.bucket = *bucketOverride
	}
	if opts.bucket == "" {
		return opts, cfg, errors.New("no bucket — set -bucket or cfg.Storage.S3BucketArchive")
	}
	return opts, cfg, nil
}

func resumeStalled(args []string) error {
	opts, cfg, err := parseResumeStalledFlags(args)
	if err != nil {
		return err
	}

	rootCtx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	logger := mkBackfillLogger()

	store, err := timescale.Open(rootCtx, cfg.Storage.PostgresDSN)
	if err != nil {
		return fmt.Errorf("storage: %w", err)
	}
	defer func() { _ = store.Close() }()

	plans, err := planResumeStalled(rootCtx, store, opts.minLag, opts.sourceFilter, opts.maxResumes)
	if err != nil {
		return fmt.Errorf("plan: %w", err)
	}

	// Gate Soroban-era plans against the data-derived soroban_events
	// gap list. Cursors whose remaining range is fully covered by
	// sibling cursors (no gap overlap) are skipped here as
	// false-positives — the F-0020 follow-up fix. Resolve the live
	// cursor's tip the same way find-data-gaps does so the gate
	// matches the diagnostic CLI exactly.
	tipCursor, err := store.GetCursor(rootCtx, "ledgerstream", "")
	if err != nil {
		return fmt.Errorf("resolve tip for data-gap gate: %w", err)
	}
	dataGaps, err := store.FindSorobanEventsLedgerGaps(rootCtx, 0, int64(tipCursor.LastLedger), opts.dataGapMinSize)
	if err != nil {
		return fmt.Errorf("find data gaps for gate: %w", err)
	}
	plans = gateAgainstDataGaps(plans, dataGaps, opts.forceClassic)

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
		"min_lag", opts.minLag.String(),
		"source_filter", opts.sourceFilter,
		"data_gaps", len(dataGaps),
		"force_classic_cursors", opts.forceClassic,
		"dry_run", opts.dryRun,
	)

	if opts.dryRun || actionable == 0 {
		for _, p := range plans {
			printResumePlan(p)
		}
		return nil
	}

	failures := executeResumePlans(rootCtx, logger, plans, opts.cfgPath, opts.bucket, opts.parallel, opts.refreshCAGGs, cfg, store)
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
