package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"
)

// drainCascadeWindow runs every per-source `*-backfill` subcommand
// over a [from, to] ledger range in a single operator invocation.
//
// Motivation (F-0020 post-mortem): when the Postgres back-pressure
// cascade halted the soroban_events writer across ~103 K ledgers
// (62,642,781 → 62,757,524) every downstream per-source classifier
// table (blend_*, comet_*, phoenix_*, soroswap_skim_events, …) was
// silently left short of the same window. Repair takes 7 separate
// subcommand invocations today; this orchestrator collapses them
// into one, with structured per-source results so operators can
// confirm completeness without scraping stderr.
//
// Sources covered: cctp, rozo, soroswap-skim, comet-liquidity,
// phoenix, blend, sep41-transfers. These are the subset of Soroban
// sources that already have a dedicated `*-backfill` subcommand
// streaming from soroban_events through the source's decoder (no
// MinIO re-walk). Sources without a dedicated subcommand
// (aquarius, reflector-*, redstone, soroswap-main, soroswap-router,
// defindex) are out of scope here — they need either the slower
// `backfill --source <name>` re-walk or their own dedicated
// subcommand (planned, separate PR).
//
// Idempotency: every per-source subcommand already inserts via ON
// CONFLICT DO NOTHING, so re-running drain-cascade-window over an
// already-drained window is a clean no-op (rows_scanned > 0,
// rows_inserted == 0).
//
// Error handling: by default a single per-source failure does NOT
// halt the orchestrator — the remaining sources still run, and the
// final summary reports per-source ok/fail. Use --halt-on-error to
// switch to fail-fast (useful when the failure is a config /
// connectivity problem that will hit every source).
//
//nolint:gocognit // linear pipeline; the per-source switch reads better inline.
func drainCascadeWindow(args []string) error {
	fs := flag.NewFlagSet("drain-cascade-window", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "path to ratesengine.toml (required)")
	from := fs.Uint("from", 0, "first ledger sequence (inclusive, required)")
	to := fs.Uint("to", 0, "last ledger sequence (inclusive, required)")
	output := fs.String("output", "text", "output format: text|json")
	dryRun := fs.Bool("dry-run", false, "decode without inserting (passed to each subcommand)")
	haltOnError := fs.Bool("halt-on-error", false, "stop on first per-source failure instead of continuing")
	sourceSubset := fs.String("sources", "", "comma-separated subset of source names to drain (default: all)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *cfgPath == "" || *from == 0 || *to == 0 || *to < *from {
		return errors.New("-config, -from, and -to are required; -to must be >= -from")
	}
	if *output != "text" && *output != "json" {
		return fmt.Errorf("invalid -output %q; use text or json", *output)
	}

	plan := drainPlan(*sourceSubset)
	if len(plan) == 0 {
		return fmt.Errorf("no sources matched -sources=%q", *sourceSubset)
	}

	childArgs := []string{
		"-config", *cfgPath,
		"-from", strconv.FormatUint(uint64(*from), 10),
		"-to", strconv.FormatUint(uint64(*to), 10),
	}
	if *dryRun {
		childArgs = append(childArgs, "-dry-run")
	}

	results := make([]drainResult, 0, len(plan))
	startedAt := time.Now()
	for _, step := range plan {
		stepStart := time.Now()
		err := step.run(childArgs)
		res := drainResult{
			Source:      step.source,
			Subcommand:  step.subcommand,
			DurationSec: time.Since(stepStart).Round(time.Millisecond).Seconds(),
			OK:          err == nil,
		}
		if err != nil {
			res.Error = err.Error()
		}
		results = append(results, res)
		if err != nil && *haltOnError {
			break
		}
	}
	totalDur := time.Since(startedAt).Round(time.Second)

	report := drainReport{
		FromLedger:  uint32(*from),
		ToLedger:    uint32(*to),
		DryRun:      *dryRun,
		HaltOnError: *haltOnError,
		DurationSec: totalDur.Seconds(),
		Sources:     results,
	}
	for _, r := range results {
		if !r.OK {
			report.FailedCount++
		}
	}

	switch *output {
	case "json":
		writeDrainReportJSON(os.Stdout, report)
	default:
		writeDrainReportText(os.Stdout, report)
	}

	if report.FailedCount > 0 {
		return fmt.Errorf("drain-cascade-window: %d/%d sources failed", report.FailedCount, len(results))
	}
	return nil
}

// drainStep names one entry in the orchestrator's per-source plan.
// `subcommand` is the wire name (logged + reported); `run` is the
// existing function this binary already exposes for that subcommand.
type drainStep struct {
	source     string
	subcommand string
	run        func([]string) error
}

// drainResult is one source's outcome after drainStep.run returns.
// Fields are snake_case in JSON to match the rest of the ops CLI.
type drainResult struct {
	Source      string  `json:"source"`
	Subcommand  string  `json:"subcommand"`
	DurationSec float64 `json:"duration_seconds"`
	OK          bool    `json:"ok"`
	Error       string  `json:"error,omitempty"`
}

// drainReport is the operator-visible summary, structured so
// `--output json` can pipe straight into jq / Prometheus pushgateway
// / CI assertion.
type drainReport struct {
	FromLedger  uint32        `json:"from_ledger"`
	ToLedger    uint32        `json:"to_ledger"`
	DryRun      bool          `json:"dry_run"`
	HaltOnError bool          `json:"halt_on_error"`
	DurationSec float64       `json:"total_duration_seconds"`
	FailedCount int           `json:"failed_count"`
	Sources     []drainResult `json:"sources"`
}

// drainPlan returns the ordered per-source plan. Sources run in the
// order defined here so the operator log reads top-down through the
// dependency graph (lighter-weight emitters first, the bigger
// blend / phoenix decoders last). If `subset` is non-empty, the
// plan is filtered to that comma-separated set of source names.
func drainPlan(subset string) []drainStep {
	all := []drainStep{
		{source: "sep41-transfers", subcommand: "sep41-transfers-backfill", run: sep41TransfersBackfill},
		{source: "cctp", subcommand: "cctp-backfill", run: cctpBackfill},
		{source: "rozo", subcommand: "rozo-backfill", run: rozoBackfill},
		{source: "soroswap-skim", subcommand: "soroswap-skim-backfill", run: soroswapSkimBackfill},
		{source: "comet-liquidity", subcommand: "comet-liquidity-backfill", run: cometLiquidityBackfill},
		{source: "blend", subcommand: "blend-backfill", run: blendBackfill},
		{source: "phoenix", subcommand: "phoenix-backfill", run: phoenixBackfill},
	}
	if subset == "" {
		return all
	}
	wanted := make(map[string]bool)
	for _, s := range splitCSV(subset) {
		wanted[s] = true
	}
	out := make([]drainStep, 0, len(all))
	for _, step := range all {
		if wanted[step.source] {
			out = append(out, step)
		}
	}
	return out
}

// writeDrainReportText emits the operator-friendly summary on stdout.
// Format pinned by drain_cascade_window_test.go so operator tooling
// (CI, runbook copy-paste) doesn't silently shift.
func writeDrainReportText(w io.Writer, r drainReport) {
	_, _ = fmt.Fprintf(w, "drain-cascade-window: ledgers=[%d, %d] dry_run=%v halt_on_error=%v total=%.1fs failed=%d/%d\n",
		r.FromLedger, r.ToLedger, r.DryRun, r.HaltOnError, r.DurationSec, r.FailedCount, len(r.Sources))
	for _, s := range r.Sources {
		status := "  OK"
		if !s.OK {
			status = "FAIL"
		}
		_, _ = fmt.Fprintf(w, "  %s  %-20s  %7.1fs  %s\n", status, s.Source, s.DurationSec, s.Subcommand)
		if s.Error != "" {
			_, _ = fmt.Fprintf(w, "         err: %s\n", s.Error)
		}
	}
}

// writeDrainReportJSON emits the structured report on stdout.
// Pretty-printed because operators piping to jq prefer 2-space
// indent; size cost is trivial (7 sources × ~200 bytes).
func writeDrainReportJSON(w io.Writer, r drainReport) {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(r)
}
