package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"

	"cloud.google.com/go/bigquery"
	"google.golang.org/api/iterator"

	"github.com/StellarAtlas/stellar-atlas/internal/config"
	"github.com/StellarAtlas/stellar-atlas/internal/storage/timescale"
)

// ─── ratesengine-ops hubble-check ──────────────────────────────────
//
// Compares our SDEX trades hypertable against SDF's published
// `crypto-stellar.crypto_stellar.history_trades` BigQuery table for
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

func hubbleCheck(args []string) error { //nolint:gocognit,gocyclo,funlen // flag parse + several diagnostic early-exit modes; linear, splitting reduces clarity.
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
	zeroAmounts := fs.Bool("zero-amounts", false,
		"diagnostic: count Hubble trades with a zero selling/buying amount in the "+
			"range (one-side-zero vs both-zero), then exit — explains SDEX off-by-one")
	dupCheck := fs.Bool("dup-check", false,
		"diagnostic: compare COUNT(*) vs COUNT(DISTINCT op_id,order) in the range to "+
			"detect duplicate Hubble rows (batch-load dups), then exit")
	detail := fs.Bool("detail", false,
		"diagnostic: dump each Hubble trade in the range (ledger, op_id, order, "+
			"trade_type, sell/buy asset_id + amount), then exit")
	withAmounts := fs.Bool("with-amounts", false,
		"in addition to per-ledger COUNT(*), compare SUM(selling_amount) + "+
			"SUM(buying_amount). Catches per-trade amount errors that net to "+
			"zero at the count level. Slightly higher BigQuery scan cost.")
	toleranceBps := fs.Int("tolerance-bps", 0,
		"sum-mismatch tolerance in basis points of the larger side. 0 = strict "+
			"(any difference is divergence). Useful only when -with-amounts is set.")
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

	if *zeroAmounts {
		oneSide, both, anyZero, err := hubbleZeroAmountCounts(ctx, bqClient, uint32(*from), uint32(*to))
		if err != nil {
			return fmt.Errorf("zero-amounts: %w", err)
		}
		_, _ = fmt.Fprintf(os.Stderr,
			"hubble zero-amount trades %d..%d: one_side_zero=%d both_zero=%d any_zero=%d\n",
			*from, *to, oneSide, both, anyZero)
		return nil
	}

	if *detail {
		if err := hubbleTradeDetail(ctx, bqClient, uint32(*from), uint32(*to)); err != nil {
			return fmt.Errorf("detail: %w", err)
		}
		return nil
	}

	if *dupCheck {
		total, distinct, err := hubbleDupCheck(ctx, bqClient, uint32(*from), uint32(*to))
		if err != nil {
			return fmt.Errorf("dup-check: %w", err)
		}
		_, _ = fmt.Fprintf(os.Stderr,
			"hubble dup-check %d..%d: rows=%d distinct(op_id,order)=%d duplicates=%d\n",
			*from, *to, total, distinct, total-distinct)
		return nil
	}

	store, err := timescale.Open(ctx, cfg.Storage.PostgresDSN)
	if err != nil {
		return fmt.Errorf("storage: %w", err)
	}
	defer func() { _ = store.Close() }()

	if *withAmounts {
		return runHubbleCheckWithAmounts(ctx, logger, store, bqClient,
			uint32(*from), uint32(*to), *maxDiffs, *toleranceBps)
	}

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
	q := client.Query("SELECT history_operation_id >> 32 AS ledger_sequence, COUNT(*) AS n " +
		"FROM `crypto-stellar.crypto_stellar.history_trades` " +
		"WHERE history_operation_id BETWEEN @from AND @to " +
		"GROUP BY ledger_sequence")
	q.Parameters = []bigquery.QueryParameter{
		// Hubble history_trades has no ledger_sequence column — the ledger is
		// the high 32 bits of history_operation_id (TOID). Filter + group by
		// the TOID range so [from,to] maps to [from<<32, ((to+1)<<32)-1].
		{Name: "from", Value: int64(from) << 32},
		{Name: "to", Value: (int64(to)+1)<<32 - 1},
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
	q := client.Query("SELECT history_operation_id >> 32 AS ledger_sequence, COUNT(*) AS n " +
		"FROM `crypto-stellar.crypto_stellar.history_trades` " +
		"WHERE history_operation_id BETWEEN @from AND @to " +
		"GROUP BY ledger_sequence")
	q.Parameters = []bigquery.QueryParameter{
		// Hubble history_trades has no ledger_sequence column — the ledger is
		// the high 32 bits of history_operation_id (TOID). Filter + group by
		// the TOID range so [from,to] maps to [from<<32, ((to+1)<<32)-1].
		{Name: "from", Value: int64(from) << 32},
		{Name: "to", Value: (int64(to)+1)<<32 - 1},
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

// ─── -with-amounts variant ────────────────────────────────────────
//
// Per-ledger COUNT(*) catches missing or extra trades but doesn't
// catch per-trade amount errors that net to zero across a ledger
// (one over-counted + one under-counted = same total count). The
// -with-amounts variant adds SUM(selling_amount) + SUM(buying_amount)
// to both sides and reports any ledger where ANY of count, sell-sum,
// or buy-sum diverges past the configured tolerance.
//
// Tolerance is in basis points of the larger side's value — covers
// minor floating-point drift between Postgres NUMERIC sums and
// BigQuery NUMERIC sums (none expected at integer-stroop scale, but
// the knob exists for future-proofing).

// ledgerStats is the per-ledger numeric record compared by the
// -with-amounts path. Counts are int (matches the count-only path);
// sum_sell + sum_buy are big.Int because Postgres + BigQuery NUMERIC
// can exceed int64 in pathological ranges.
type ledgerStats struct {
	Count   int
	SumSell *big.Int // ↔ Hubble selling_amount sum  ↔ our base_amount sum
	SumBuy  *big.Int // ↔ Hubble buying_amount sum   ↔ our quote_amount sum
}

// ledgerStatDiff is one row of the -with-amounts divergence report.
type ledgerStatDiff struct {
	Ledger uint32
	Ours   ledgerStats
	Theirs ledgerStats
	// Reasons names which fields diverged: "count" / "sum_sell" /
	// "sum_buy", in that order.
	Reasons []string
}

func runHubbleCheckWithAmounts(
	ctx context.Context,
	logger *slog.Logger,
	store *timescale.Store,
	bqClient *bigquery.Client,
	from, to uint32,
	maxDiffs, toleranceBps int,
) error {
	logger.Info("querying our SDEX trades (with amounts)", "from", from, "to", to)
	ours, err := fetchOurSDEXStats(ctx, store, from, to)
	if err != nil {
		return fmt.Errorf("query trades hypertable: %w", err)
	}

	logger.Info("querying Hubble (with amounts)", "from", from, "to", to)
	theirs, err := fetchHubbleStats(ctx, bqClient, from, to)
	if err != nil {
		return fmt.Errorf("query Hubble: %w", err)
	}

	diffs := diffLedgerStats(ours, theirs, toleranceBps)
	if len(diffs) == 0 {
		logger.Info("hubble-check OK — every ledger's SDEX count + amounts match",
			"from", from, "to", to,
			"tolerance_bps", toleranceBps)
		return nil
	}

	reportLedgerStatDiffs(diffs, maxDiffs)
	return fmt.Errorf("hubble-check FAIL — %d ledger(s) disagree on count or amount sums", len(diffs))
}

// fetchOurSDEXStats is the -with-amounts counterpart of
// fetchOurSDEXCounts. Per-ledger (count, sum(base_amount),
// sum(quote_amount)). NUMERIC sums round-tripped via TEXT to keep
// big.Int precision.
func fetchOurSDEXStats(ctx context.Context, store *timescale.Store, from, to uint32) (map[uint32]ledgerStats, error) {
	const q = `
		SELECT ledger, COUNT(*),
		       COALESCE(SUM(base_amount)::text,  '0'),
		       COALESCE(SUM(quote_amount)::text, '0')
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

	out := make(map[uint32]ledgerStats, 1024)
	for rows.Next() {
		var ledger uint32
		var n int
		var sumBaseStr, sumQuoteStr string
		if err := rows.Scan(&ledger, &n, &sumBaseStr, &sumQuoteStr); err != nil {
			return nil, err
		}
		sb, ok := new(big.Int).SetString(sumBaseStr, 10)
		if !ok {
			return nil, fmt.Errorf("parse SUM(base_amount) for ledger %d: %q", ledger, sumBaseStr)
		}
		sq, ok := new(big.Int).SetString(sumQuoteStr, 10)
		if !ok {
			return nil, fmt.Errorf("parse SUM(quote_amount) for ledger %d: %q", ledger, sumQuoteStr)
		}
		out[ledger] = ledgerStats{Count: n, SumSell: sb, SumBuy: sq}
	}
	return out, rows.Err()
}

// hubbleZeroAmountCounts counts Hubble history_trades in the range with a zero
// amount: oneSide (exactly one of selling/buying is 0), both (both 0), anyZero
// (either). Diagnoses the SDEX off-by-one: our decoder's
// soldAmount<=0||boughtAmount<=0 guard drops all of these, while Hubble records
// them — so anyZero should equal the per-ledger trade-count shortfall.
func hubbleZeroAmountCounts(ctx context.Context, client *bigquery.Client, from, to uint32) (oneSide, both, anyZero int64, err error) {
	q := client.Query("SELECT " +
		"COUNTIF((selling_amount = 0) != (buying_amount = 0)) AS one_side, " +
		"COUNTIF(selling_amount = 0 AND buying_amount = 0) AS both, " +
		"COUNTIF(selling_amount = 0 OR buying_amount = 0) AS any_zero " +
		"FROM `crypto-stellar.crypto_stellar.history_trades` " +
		"WHERE history_operation_id BETWEEN @from AND @to")
	q.Parameters = []bigquery.QueryParameter{
		{Name: "from", Value: int64(from) << 32},
		{Name: "to", Value: (int64(to)+1)<<32 - 1},
	}
	it, qerr := q.Read(ctx)
	if qerr != nil {
		return 0, 0, 0, qerr
	}
	var row struct {
		OneSide int64 `bigquery:"one_side"`
		Both    int64 `bigquery:"both"`
		AnyZero int64 `bigquery:"any_zero"`
	}
	if e := it.Next(&row); e != nil && !errors.Is(e, iterator.Done) {
		return 0, 0, 0, e
	}
	return row.OneSide, row.Both, row.AnyZero, nil
}

// hubbleTradeDetail dumps each Hubble trade in the range to stdout — used to
// identify the exact trades behind a per-ledger count discrepancy.
func hubbleTradeDetail(ctx context.Context, client *bigquery.Client, from, to uint32) error {
	q := client.Query("SELECT history_operation_id >> 32 AS ledger, history_operation_id AS opid, `order` AS ord, " +
		"trade_type, selling_asset_id, buying_asset_id, " +
		"CAST(selling_amount AS STRING) AS sell, CAST(buying_amount AS STRING) AS buy " +
		"FROM `crypto-stellar.crypto_stellar.history_trades` " +
		"WHERE history_operation_id BETWEEN @from AND @to " +
		"ORDER BY history_operation_id, `order`")
	q.Parameters = []bigquery.QueryParameter{
		{Name: "from", Value: int64(from) << 32},
		{Name: "to", Value: (int64(to)+1)<<32 - 1},
	}
	it, err := q.Read(ctx)
	if err != nil {
		return err
	}
	for {
		var row struct {
			Ledger    int64  `bigquery:"ledger"`
			Opid      int64  `bigquery:"opid"`
			Ord       int64  `bigquery:"ord"`
			TradeType int64  `bigquery:"trade_type"`
			SellAsset int64  `bigquery:"selling_asset_id"`
			BuyAsset  int64  `bigquery:"buying_asset_id"`
			Sell      string `bigquery:"sell"`
			Buy       string `bigquery:"buy"`
		}
		e := it.Next(&row)
		if errors.Is(e, iterator.Done) {
			break
		}
		if e != nil {
			return e
		}
		fmt.Printf("ledger=%d opid=%d order=%d type=%d sellAsset=%d buyAsset=%d sell=%s buy=%s\n",
			row.Ledger, row.Opid, row.Ord, row.TradeType, row.SellAsset, row.BuyAsset, row.Sell, row.Buy)
	}
	return nil
}

// hubbleDupCheck returns total row count and distinct (history_operation_id,
// order) count for the range. A gap means Hubble carries duplicate trade rows
// (overlapping batch loads) — the same artifact seen in history_contract_events.
func hubbleDupCheck(ctx context.Context, client *bigquery.Client, from, to uint32) (total, distinct int64, err error) {
	q := client.Query("SELECT COUNT(*) AS total, " +
		"COUNT(DISTINCT CONCAT(CAST(history_operation_id AS STRING), '-', CAST(`order` AS STRING))) AS distinct_trades " +
		"FROM `crypto-stellar.crypto_stellar.history_trades` " +
		"WHERE history_operation_id BETWEEN @from AND @to")
	q.Parameters = []bigquery.QueryParameter{
		{Name: "from", Value: int64(from) << 32},
		{Name: "to", Value: (int64(to)+1)<<32 - 1},
	}
	it, qerr := q.Read(ctx)
	if qerr != nil {
		return 0, 0, qerr
	}
	var row struct {
		Total    int64 `bigquery:"total"`
		Distinct int64 `bigquery:"distinct_trades"`
	}
	if e := it.Next(&row); e != nil && !errors.Is(e, iterator.Done) {
		return 0, 0, e
	}
	return row.Total, row.Distinct, nil
}

// fetchHubbleStats is the -with-amounts counterpart of
// fetchHubbleCounts. Per-ledger (count, sum(selling_amount),
// sum(buying_amount)).
func fetchHubbleStats(ctx context.Context, client *bigquery.Client, from, to uint32) (map[uint32]ledgerStats, error) {
	q := client.Query("SELECT history_operation_id >> 32 AS ledger, COUNT(*) AS n, " +
		"COALESCE(CAST(SUM(selling_amount) AS STRING), '0') AS sum_sell, " +
		"COALESCE(CAST(SUM(buying_amount)  AS STRING), '0') AS sum_buy " +
		"FROM `crypto-stellar.crypto_stellar.history_trades` " +
		"WHERE history_operation_id BETWEEN @from AND @to " +
		"GROUP BY ledger")
	q.Parameters = []bigquery.QueryParameter{
		// Hubble history_trades has no ledger_sequence column — the ledger is
		// the high 32 bits of history_operation_id (TOID). Filter + group by
		// the TOID range so [from,to] maps to [from<<32, ((to+1)<<32)-1].
		{Name: "from", Value: int64(from) << 32},
		{Name: "to", Value: (int64(to)+1)<<32 - 1},
	}
	it, err := q.Read(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[uint32]ledgerStats, 1024)
	for {
		var row struct {
			Ledger  int64  `bigquery:"ledger"`
			N       int64  `bigquery:"n"`
			SumSell string `bigquery:"sum_sell"`
			SumBuy  string `bigquery:"sum_buy"`
		}
		err := it.Next(&row)
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, err
		}
		ss, ok := new(big.Int).SetString(row.SumSell, 10)
		if !ok {
			return nil, fmt.Errorf("parse Hubble SUM(selling_amount) for ledger %d: %q", row.Ledger, row.SumSell)
		}
		sb, ok := new(big.Int).SetString(row.SumBuy, 10)
		if !ok {
			return nil, fmt.Errorf("parse Hubble SUM(buying_amount) for ledger %d: %q", row.Ledger, row.SumBuy)
		}
		out[uint32(row.Ledger)] = ledgerStats{Count: int(row.N), SumSell: ss, SumBuy: sb}
	}
	return out, nil
}

// diffLedgerStats compares per-ledger stats and returns ledgers where
// count, sum_sell, or sum_buy diverges past the tolerance. Sparse-map
// semantics (missing key = zero stats) match diffLedgerCounts so the
// "missing on one side" case is reported the same way.
func diffLedgerStats(ours, theirs map[uint32]ledgerStats, toleranceBps int) []ledgerStatDiff {
	seen := make(map[uint32]struct{}, len(ours)+len(theirs))
	for k := range ours {
		seen[k] = struct{}{}
	}
	for k := range theirs {
		seen[k] = struct{}{}
	}
	out := make([]ledgerStatDiff, 0, len(seen))
	for k := range seen {
		a, b := ours[k], theirs[k]
		if a.SumSell == nil {
			a.SumSell = big.NewInt(0)
		}
		if a.SumBuy == nil {
			a.SumBuy = big.NewInt(0)
		}
		if b.SumSell == nil {
			b.SumSell = big.NewInt(0)
		}
		if b.SumBuy == nil {
			b.SumBuy = big.NewInt(0)
		}
		var reasons []string
		if a.Count != b.Count {
			reasons = append(reasons, "count")
		}
		if !sumsWithinTolerance(a.SumSell, b.SumSell, toleranceBps) {
			reasons = append(reasons, "sum_sell")
		}
		if !sumsWithinTolerance(a.SumBuy, b.SumBuy, toleranceBps) {
			reasons = append(reasons, "sum_buy")
		}
		if len(reasons) > 0 {
			out = append(out, ledgerStatDiff{
				Ledger: k, Ours: a, Theirs: b, Reasons: reasons,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ledger < out[j].Ledger })
	return out
}

// sumsWithinTolerance reports whether |a-b| <= toleranceBps/10000
// of max(|a|,|b|). Zero tolerance = strict equality. Useful only as
// a future-proofing knob; integer-stroop sums on both sides should
// match exactly when the underlying trades match.
func sumsWithinTolerance(a, b *big.Int, toleranceBps int) bool {
	if a.Cmp(b) == 0 {
		return true
	}
	if toleranceBps <= 0 {
		return false
	}
	diff := new(big.Int).Abs(new(big.Int).Sub(a, b))
	larger := new(big.Int).Set(a)
	if b.CmpAbs(a) > 0 {
		larger = new(big.Int).Set(b)
	}
	larger = larger.Abs(larger)
	// |a-b| * 10000 <= toleranceBps * larger
	lhs := new(big.Int).Mul(diff, big.NewInt(10000))
	rhs := new(big.Int).Mul(big.NewInt(int64(toleranceBps)), larger)
	return lhs.Cmp(rhs) <= 0
}

func reportLedgerStatDiffs(diffs []ledgerStatDiff, maxDiffs int) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	limit := maxDiffs
	if limit > len(diffs) {
		limit = len(diffs)
	}
	for i := 0; i < limit; i++ {
		d := diffs[i]
		logger.Warn("ledger stats mismatch",
			"ledger", d.Ledger,
			"reasons", strings.Join(d.Reasons, ","),
			"ours_count", d.Ours.Count,
			"hubble_count", d.Theirs.Count,
			"ours_sum_sell", d.Ours.SumSell.String(),
			"hubble_sum_sell", d.Theirs.SumSell.String(),
			"ours_sum_buy", d.Ours.SumBuy.String(),
			"hubble_sum_buy", d.Theirs.SumBuy.String(),
		)
	}
	if len(diffs) > maxDiffs {
		logger.Warn("output truncated — re-run with -max-mismatches=N to see more",
			"shown", maxDiffs,
			"total", len(diffs))
	}
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
