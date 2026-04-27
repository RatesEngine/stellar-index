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
	"syscall"

	"cloud.google.com/go/bigquery"
	"google.golang.org/api/iterator"

	"github.com/RatesEngine/rates-engine/internal/config"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// ─── ratesengine-ops hubble-check ──────────────────────────────────
//
// Compares our SDEX trades hypertable against SDF's published
// `hubble-public.crypto_stellar.history_trades` BigQuery table for
// the same ledger range. Reports every ledger where our row count
// disagrees with Hubble's.
//
// What this catches that nothing else does:
//
//   - Decoder coverage gaps (we missed a ClaimAtom shape we should
//     have decoded; row count is short).
//   - Decoder over-eagerness (we emitted extra rows for events that
//     aren't trades; row count is long).
//   - Backfill correctness gate before the since-inception OHLC ships
//     to API consumers.
//
// What it does NOT catch (yet):
//
//   - Per-trade amount errors that net to zero across a ledger
//     (vanishingly rare). v2 will add per-(ledger, tx_hash, asset
//     pair) sum comparison.
//   - Soroban DEX trades. Hubble has no decoded `history_trades`
//     view of Soroswap/Aquarius/Phoenix/Comet — they live as raw
//     events in `history_contract_events`. A Soroban-aware count
//     check is a separate subcommand we'll build once the per-WASM
//     decoder audit lands.
//   - Off-chain (CEX/FX) sources. Hubble is on-chain only.
//
// Cost. The query is a count(*) over `history_trades` filtered by
// `ledger_sequence` between two values, partitioned by close_at —
// BigQuery prunes to the relevant partitions. A 1 M-ledger range
// scans roughly 5–10 GB at $5/TB on-demand, so ≤$0.05 per check.
// Reservation pricing is roughly free at our scale. Operators who
// need to cap spend can pass -dry-run-bytes to print the dry-run
// estimate before executing.
//
// Auth. Uses Application Default Credentials. Easiest:
//
//   gcloud auth application-default login --project=<your-bq-project>
//
// or set GOOGLE_APPLICATION_CREDENTIALS to a service-account JSON
// with roles/bigquery.dataViewer + roles/bigquery.jobUser on the
// supplied -bigquery-project.

func hubbleCheck(args []string) error {
	fs := flag.NewFlagSet("hubble-check", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "path to ratesengine.toml (required)")
	from := fs.Uint("from", 0, "starting ledger sequence, inclusive (required)")
	to := fs.Uint("to", 0, "ending ledger sequence, inclusive (required)")
	project := fs.String("bigquery-project", "",
		"GCP project ID to bill BigQuery queries against (required)")
	maxDiffs := fs.Int("max-mismatches", 50,
		"cap on number of divergent ledgers reported; the rest are summarised")
	dryRunBytes := fs.Bool("dry-run-bytes", false,
		"print the BigQuery dry-run byte estimate, then exit (no real query)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *cfgPath == "" {
		return errors.New("-config required")
	}
	if *from == 0 {
		return errors.New("-from must be > 0")
	}
	if *to <= *from {
		return fmt.Errorf("-to (%d) must be > -from (%d)", *to, *from)
	}
	if *project == "" {
		return errors.New("-bigquery-project required")
	}

	cfg, err := config.LoadWithEnv(*cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	logger := mkBackfillLogger() // re-uses the ops-style stderr text logger from backfill.go

	bqClient, err := bigquery.NewClient(ctx, *project)
	if err != nil {
		return fmt.Errorf("bigquery client: %w", err)
	}
	defer func() { _ = bqClient.Close() }()

	if *dryRunBytes {
		bytes, err := hubbleDryRun(ctx, bqClient, uint32(*from), uint32(*to))
		if err != nil {
			return fmt.Errorf("dry-run: %w", err)
		}
		_, _ = fmt.Fprintf(os.Stderr,
			"hubble-check dry-run: would scan ~%d bytes (~$%.4f at $5/TB on-demand)\n",
			bytes, float64(bytes)/1e12*5.0)
		return nil
	}

	store, err := timescale.Open(ctx, cfg.Storage.PostgresDSN)
	if err != nil {
		return fmt.Errorf("storage: %w", err)
	}
	defer func() { _ = store.Close() }()

	logger.Info("querying our SDEX trades", "from", *from, "to", *to)
	ours, err := fetchOurSDEXCounts(ctx, store, uint32(*from), uint32(*to))
	if err != nil {
		return fmt.Errorf("query trades hypertable: %w", err)
	}

	logger.Info("querying Hubble", "from", *from, "to", *to, "project", *project)
	theirs, err := fetchHubbleCounts(ctx, bqClient, uint32(*from), uint32(*to))
	if err != nil {
		return fmt.Errorf("query Hubble: %w", err)
	}

	diffs := diffLedgerCounts(ours, theirs)
	if len(diffs) == 0 {
		logger.Info("hubble-check OK — every ledger's SDEX trade count matches",
			"from", *from, "to", *to,
			"ledgers_checked", len(ours)+len(theirs)) // upper bound
		return nil
	}

	reportLedgerDiffs(diffs, *maxDiffs)
	return fmt.Errorf("hubble-check FAIL — %d ledger(s) disagree on SDEX trade count", len(diffs))
}

// fetchOurSDEXCounts returns map[ledger]→trade count for our SDEX
// rows in [from, to]. Ledgers with zero rows on our side simply
// don't appear in the map (sparse representation — diffLedgerCounts
// treats absence as zero, same as Hubble).
func fetchOurSDEXCounts(ctx context.Context, store *timescale.Store, from, to uint32) (map[uint32]int, error) {
	const q = `
		SELECT ledger, COUNT(*)
		FROM trades
		WHERE source = 'sdex'
		  AND ledger BETWEEN $1 AND $2
		GROUP BY ledger
	`
	rows, err := store.DB().QueryContext(ctx, q, from, to)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make(map[uint32]int, 1024)
	for rows.Next() {
		var ledger uint32
		var n int
		if err := rows.Scan(&ledger, &n); err != nil {
			return nil, err
		}
		out[ledger] = n
	}
	return out, rows.Err()
}

// fetchHubbleCounts queries Hubble for per-ledger SDEX trade counts.
// Trade type 1 (orderbook) and trade type 2 (classic liquidity-pool)
// are both included — our SDEX decoder handles both ClaimAtom kinds
// and stamps both as source='sdex'.
func fetchHubbleCounts(ctx context.Context, client *bigquery.Client, from, to uint32) (map[uint32]int, error) {
	q := client.Query("SELECT ledger_sequence, COUNT(*) AS n " +
		"FROM `hubble-public.crypto_stellar.history_trades` " +
		"WHERE ledger_sequence BETWEEN @from AND @to " +
		"GROUP BY ledger_sequence")
	q.Parameters = []bigquery.QueryParameter{
		{Name: "from", Value: int64(from)},
		{Name: "to", Value: int64(to)},
	}
	it, err := q.Read(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[uint32]int, 1024)
	for {
		var row struct {
			LedgerSequence int64 `bigquery:"ledger_sequence"`
			N              int64 `bigquery:"n"`
		}
		err := it.Next(&row)
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, err
		}
		out[uint32(row.LedgerSequence)] = int(row.N)
	}
	return out, nil
}

// hubbleDryRun returns the BigQuery dry-run byte estimate for the
// count-only query. Used by -dry-run-bytes to give operators a cost
// preview before running for real.
func hubbleDryRun(ctx context.Context, client *bigquery.Client, from, to uint32) (int64, error) {
	q := client.Query("SELECT ledger_sequence, COUNT(*) AS n " +
		"FROM `hubble-public.crypto_stellar.history_trades` " +
		"WHERE ledger_sequence BETWEEN @from AND @to " +
		"GROUP BY ledger_sequence")
	q.Parameters = []bigquery.QueryParameter{
		{Name: "from", Value: int64(from)},
		{Name: "to", Value: int64(to)},
	}
	q.DryRun = true
	job, err := q.Run(ctx)
	if err != nil {
		return 0, err
	}
	stat := job.LastStatus()
	if stat == nil || stat.Statistics == nil {
		return 0, errors.New("bigquery returned no statistics for dry-run")
	}
	return stat.Statistics.TotalBytesProcessed, nil
}

// ledgerDiff captures one ledger where our count and Hubble's
// disagree.
type ledgerDiff struct {
	Ledger uint32
	Ours   int
	Theirs int
}

// diffLedgerCounts compares two count maps and returns the ledgers
// where they differ. Sparse on both sides — a ledger absent from
// `ours` is treated as count=0 (and same for `theirs`). Output is
// sorted by ledger ascending so the report is deterministic across
// runs and easy to grep against the verifier's progress log.
func diffLedgerCounts(ours, theirs map[uint32]int) []ledgerDiff {
	seen := make(map[uint32]struct{}, len(ours)+len(theirs))
	for k := range ours {
		seen[k] = struct{}{}
	}
	for k := range theirs {
		seen[k] = struct{}{}
	}
	out := make([]ledgerDiff, 0, len(seen))
	for k := range seen {
		a, b := ours[k], theirs[k]
		if a != b {
			out = append(out, ledgerDiff{Ledger: k, Ours: a, Theirs: b})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ledger < out[j].Ledger })
	return out
}

// reportLedgerDiffs writes a human-readable diff report to stderr.
// Capped at maxDiffs so a 100k-ledger divergence doesn't flood the
// terminal — the summary line names the cap so operators know to
// re-run with -max-mismatches=N for a wider report.
func reportLedgerDiffs(diffs []ledgerDiff, maxDiffs int) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	limit := maxDiffs
	if limit > len(diffs) {
		limit = len(diffs)
	}
	for i := 0; i < limit; i++ {
		d := diffs[i]
		var sign string
		switch {
		case d.Ours < d.Theirs:
			sign = "MISSING (we have fewer rows than Hubble — decoder coverage gap?)"
		case d.Ours > d.Theirs:
			sign = "EXTRA (we have more rows than Hubble — over-eager decoder?)"
		}
		logger.Warn("ledger count mismatch",
			"ledger", d.Ledger,
			"ours", d.Ours,
			"hubble", d.Theirs,
			"signal", sign,
		)
	}
	if len(diffs) > maxDiffs {
		logger.Warn("output truncated — re-run with -max-mismatches=N to see more",
			"shown", maxDiffs,
			"total", len(diffs))
	}
}
