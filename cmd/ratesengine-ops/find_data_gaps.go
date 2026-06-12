package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/config"
	"github.com/StellarAtlas/stellar-atlas/internal/storage/timescale"
)

// find-data-gaps — data-derived gap detector for Soroban-era ledger
// ingest coverage.
//
// Why this exists. The cursor-derived density projection in
// `/v1/diagnostics/ingestion` measures process state ("did we walk
// this ledger") and can read 100% while underlying data is missing.
// This subcommand scans the soroban_events hypertable directly and
// reports contiguous ledger-coverage gaps >= --min-gap-size.
// Operators use the output as the input to a *targeted* backfill —
// `ratesengine-ops backfill --from <gap.start> --to <gap.end>
// --source soroban-events` — rather than re-running every stalled
// cursor on faith.
//
// The threshold default of 1000 ledgers filters out the legitimate
// "no Soroban contract emitted in this block" stretches that are
// expected on mainnet (Soroban activity is dense but not gap-free).
// A 1000-ledger contiguous gap is ~1.5 h of network time and
// implies an ingest-side failure, not network quiet.
//
// JSON output mode (`--output json`) emits a plan-shaped document
// ready to feed automation; text mode is the operator-friendly
// default. Both modes report `total_missing_ledgers` so a fleet of
// invocations can be aggregated for a global gap-size dashboard.
const findDataGapsDefaultMinGapSize = int64(1000)

type findDataGapsReport struct {
	ScannedAt           time.Time             `json:"scanned_at"`
	Source              string                `json:"source"`
	Table               string                `json:"table"`
	MinGapSize          int64                 `json:"min_gap_size"`
	FromLedger          int64                 `json:"from_ledger"`
	ToLedger            int64                 `json:"to_ledger"`
	Gaps                []timescale.LedgerGap `json:"gaps"`
	TotalMissingLedgers int64                 `json:"total_missing_ledgers"`
}

// findDataGapsMultiReport groups one report per per-source target.
// Emitted by `find-data-gaps --source all` (the default when no
// --source is given). Operators reading JSON pipe it through `jq`
// to filter by source; humans reading text get one block per
// target.
type findDataGapsMultiReport struct {
	ScannedAt time.Time            `json:"scanned_at"`
	Reports   []findDataGapsReport `json:"reports"`
}

func findDataGaps(args []string) error {
	fs := flag.NewFlagSet("find-data-gaps", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "path to ratesengine.toml (required)")
	from := fs.Int64("from", 0,
		"scan from this ledger (default 0 = first ledger in soroban_events). "+
			"Soroban era starts at L50,457,424 on pubnet; values earlier than "+
			"that yield no gaps because the table is empty there.")
	to := fs.Int64("to", 0,
		"scan to this ledger (default 0 = current tip from the live cursor)")
	minGapSize := fs.Int64("min-gap-size", findDataGapsDefaultMinGapSize,
		"only report gaps >= this many contiguous ledgers. 1000 default "+
			"filters out legitimate no-Soroban-activity stretches; lower "+
			"values surface more noise from quiet periods.")
	output := fs.String("output", "text",
		"output format: text (operator-friendly) or json (machine-readable plan)")
	source := fs.String("source", "all",
		"which per-source target(s) to scan: a single source name "+
			"(e.g. blend-positions, soroban-events), \"all\" (every "+
			"registered target — default), or a comma-separated subset")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *cfgPath == "" {
		return errors.New("-config required")
	}
	if *minGapSize < 1 {
		return fmt.Errorf("-min-gap-size (%d) must be >= 1", *minGapSize)
	}
	if *output != "text" && *output != "json" {
		return fmt.Errorf("-output (%q) must be \"text\" or \"json\"", *output)
	}

	targets, err := resolveFindDataGapsTargets(*source)
	if err != nil {
		return err
	}

	cfg, err := config.LoadWithEnv(*cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	rootCtx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	store, err := timescale.Open(rootCtx, cfg.Storage.PostgresDSN)
	if err != nil {
		return fmt.Errorf("storage: %w", err)
	}
	defer func() { _ = store.Close() }()

	// Default `to` = live cursor's last_ledger (current tip).
	if *to == 0 {
		c, err := store.GetCursor(rootCtx, "ledgerstream", "")
		if err != nil {
			return fmt.Errorf("resolve tip from live cursor: %w", err)
		}
		*to = int64(c.LastLedger)
	}
	if *to < *from {
		return fmt.Errorf("-to (%d) < -from (%d)", *to, *from)
	}

	multi := findDataGapsMultiReport{ScannedAt: time.Now().UTC()}
	for _, target := range targets {
		gaps, err := store.FindPerSourceLedgerGaps(rootCtx, target, *from, *to, *minGapSize)
		if err != nil {
			return fmt.Errorf("find gaps source=%s table=%s: %w", target.Source, target.Table, err)
		}
		report := findDataGapsReport{
			ScannedAt:  multi.ScannedAt,
			Source:     target.Source,
			Table:      target.Table,
			MinGapSize: *minGapSize,
			FromLedger: *from,
			ToLedger:   *to,
			Gaps:       gaps,
		}
		for _, g := range gaps {
			report.TotalMissingLedgers += g.Size
		}
		multi.Reports = append(multi.Reports, report)
	}

	switch *output {
	case "json":
		return writeFindDataGapsJSON(multi)
	default:
		writeFindDataGapsMultiText(multi)
		return nil
	}
}

// resolveFindDataGapsTargets maps the --source flag value to the
// concrete list of [timescale.GapDetectorTarget] to scan. "all"
// returns every registered target; a single name matches by Source;
// a comma-separated list filters the registry to that subset.
// An unmatched name is a hard error so a typo doesn't silently scan
// nothing.
func resolveFindDataGapsTargets(source string) ([]timescale.GapDetectorTarget, error) {
	if source == "" || source == "all" {
		return timescale.DefaultGapDetectorTargets, nil
	}
	wanted := make(map[string]bool)
	for _, s := range splitCSV(source) {
		wanted[s] = true
	}
	out := make([]timescale.GapDetectorTarget, 0, len(wanted))
	matched := make(map[string]bool, len(wanted))
	for _, t := range timescale.DefaultGapDetectorTargets {
		if wanted[t.Source] {
			out = append(out, t)
			matched[t.Source] = true
		}
	}
	for name := range wanted {
		if !matched[name] {
			return nil, fmt.Errorf("-source %q does not match any registered gap-detector target", name)
		}
	}
	return out, nil
}

func writeFindDataGapsJSON(m findDataGapsMultiReport) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(m)
}

// writeFindDataGapsMultiText emits one report block per target.
// Reports with zero gaps are still printed so the operator can
// confirm every target was scanned.
func writeFindDataGapsMultiText(m findDataGapsMultiReport) {
	// fmt.Fprint{,f,ln} errors against os.Stdout are not actionable —
	// a broken-pipe on operator-CLI output is the operator's terminal,
	// not a system fault — so we swallow them via _, _ = ... rather
	// than threading an error all the way up to main.
	_, _ = fmt.Fprintf(os.Stdout, "find-data-gaps: %d targets scanned at %s\n",
		len(m.Reports), m.ScannedAt.Format(time.RFC3339))
	for _, r := range m.Reports {
		writeFindDataGapsText(r)
	}
}

func writeFindDataGapsText(r findDataGapsReport) {
	_, _ = fmt.Fprintf(os.Stdout,
		"\n  source=%s table=%s ledgers=[%d, %d] min_gap_size=%d\n",
		r.Source, r.Table, r.FromLedger, r.ToLedger, r.MinGapSize)
	if len(r.Gaps) == 0 {
		_, _ = fmt.Fprintln(os.Stdout, "    no gaps found — coverage clean above the threshold")
		return
	}
	_, _ = fmt.Fprintf(os.Stdout, "    %d gap(s), totalling %d missing ledgers:\n", len(r.Gaps), r.TotalMissingLedgers)
	for i, g := range r.Gaps {
		_, _ = fmt.Fprintf(os.Stdout, "      %2d  [%d, %d]  size=%d ledgers\n", i+1, g.Start, g.End, g.Size)
	}
	_, _ = fmt.Fprintln(os.Stdout, "    Targeted backfill plan:")
	for i, g := range r.Gaps {
		// soroban-events uses the binary `backfill` subcommand (re-walk
		// MinIO into the soroban_events raw landing zone). Per-source
		// classifier tables (trades, blend_*, phoenix_*, …) are
		// projected from soroban_events by the projector
		// (ADR-0032); operators rewind the per-source projector
		// cursor with `projector-replay`. Non-projected sources
		// (sdex, external) still use their own backfill paths.
		switch r.Source {
		case "soroban-events":
			_, _ = fmt.Fprintf(os.Stdout,
				"      %2d  ratesengine-ops backfill --config /etc/ratesengine.toml --from %d --to %d --source soroban-events\n",
				i+1, g.Start, g.End)
		case "sdex":
			_, _ = fmt.Fprintf(os.Stdout,
				"      %2d  ratesengine-ops backfill --config /etc/ratesengine.toml --from %d --to %d --source sdex\n",
				i+1, g.Start, g.End)
		default:
			_, _ = fmt.Fprintf(os.Stdout,
				"      %2d  ratesengine-ops projector-replay --config /etc/ratesengine.toml --source %s --from %d\n",
				i+1, r.Source, g.Start)
		}
	}
}
