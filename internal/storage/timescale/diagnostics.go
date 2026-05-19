package timescale

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// FXCoverage is the storage-layer projection of how much FX-history
// the fx_quotes hypertable currently holds. Powers the
// /v1/diagnostics/ingestion surface so operators can see at a glance
// whether the Frankfurter / Massive backfill ran to completion +
// whether the daily live-write tick is keeping up.
//
// Empty / zero-valued when fx_quotes has no rows yet — handlers
// project that as "—" rather than an error.
type FXCoverage struct {
	// EarliestQuote / LatestQuote are MIN/MAX(bucket). Zero
	// values when fx_quotes is empty.
	EarliestQuote time.Time
	LatestQuote   time.Time
	// TotalQuotes is the total row count across all tickers +
	// dates. Useful for sanity-checking against an expected
	// "tickers × days" multiplier.
	TotalQuotes int64
	// CurrenciesCount is COUNT(DISTINCT ticker) — i.e. how many
	// distinct fiat currencies have at least one quote.
	CurrenciesCount int
}

// FXCoverageStats returns the current coverage state of the
// fx_quotes hypertable. A single GROUPING() aggregate over the
// hypertable; cheap (~1ms on a populated table because the
// hypertable's index sits on bucket).
func (s *Store) FXCoverageStats(ctx context.Context) (FXCoverage, error) {
	const q = `
		SELECT
		    MIN(bucket),
		    MAX(bucket),
		    COUNT(*),
		    COUNT(DISTINCT ticker)
		FROM fx_quotes
	`
	var (
		minB, maxB sql.NullTime
		total      int64
		currencies int
	)
	if err := s.db.QueryRowContext(ctx, q).Scan(&minB, &maxB, &total, &currencies); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return FXCoverage{}, nil
		}
		return FXCoverage{}, err
	}
	out := FXCoverage{TotalQuotes: total, CurrenciesCount: currencies}
	if minB.Valid {
		out.EarliestQuote = minB.Time
	}
	if maxB.Valid {
		out.LatestQuote = maxB.Time
	}
	return out, nil
}

// SupplyCoverage is the storage-layer projection of how many assets
// have a supply snapshot today + how recently the most recent
// snapshot was written. Powers the ingestion-diagnostics surface so
// operators can spot a stalled supply observer
// (LastSnapshotAt > a few minutes ago) without paging through
// asset_supply_history by hand.
//
// "Classic" vs "SEP-41" splits asset_key by prefix — SEP-41 contract
// IDs start with "C", classic assets are "native" or
// "CODE:G-strkey". The split mirrors the three-domain supply
// algorithm split (XLM / classic / SEP-41).
type SupplyCoverage struct {
	ClassicAssets  int
	SEP41Assets    int
	LastSnapshotAt time.Time
	LatestLedger   int64
}

// LedgerRangeToTimeRange returns the MIN/MAX(ts) of trades whose
// ledger falls in [fromLedger, toLedger]. Used by the backfill
// tool to translate a ledger-range chunk into the timestamp range
// needed for CAGG materialisation. Returns ErrNotFound when no
// trades exist in the range — caller treats that as "nothing to
// refresh" rather than an error.
//
// O(log N) via the trades_source_ledger_idx index plus a per-chunk
// scan; sub-second on a populated hypertable.
func (s *Store) LedgerRangeToTimeRange(ctx context.Context, fromLedger, toLedger uint32) (time.Time, time.Time, error) {
	const q = `SELECT MIN(ts), MAX(ts) FROM trades WHERE ledger BETWEEN $1 AND $2`
	var minTs, maxTs sql.NullTime
	if err := s.db.QueryRowContext(ctx, q, fromLedger, toLedger).Scan(&minTs, &maxTs); err != nil {
		return time.Time{}, time.Time{}, err
	}
	if !minTs.Valid || !maxTs.Valid {
		return time.Time{}, time.Time{}, ErrNotFound
	}
	return minTs.Time, maxTs.Time, nil
}

// allowedCAGGViews is the strict allow-list of view names accepted
// by RefreshContinuousAggregate. Required because we string-format
// the view name into the SQL — the procedure's first arg is REGCLASS
// and pgx doesn't placeholder it. Allow-list keeps SQL injection
// off the table even though callers are internal.
var allowedCAGGViews = map[string]bool{
	"prices_1m":  true,
	"prices_15m": true,
	"prices_1h":  true,
	"prices_4h":  true,
	"prices_1d":  true,
	"prices_1w":  true,
	"prices_1mo": true,
}

// CAGGsLiveForever is the set of price aggregates with no retention
// policy — these are the ones the backfill tool refreshes after
// each chunk. The minute-grain CAGGs (1m/15m) have a 30-day
// retention by design (per migration 0002), so refreshing historical
// buckets there is wasted work.
//
// Per-entry MinWindow is the Timescale-imposed minimum refresh
// window: refresh_continuous_aggregate rejects (`SQLSTATE 22023:
// refresh window too small`) any window narrower than 2× bucket
// width. The backfill caller pads the chunk's actual ts range up
// to the entry's MinWindow before invoking refresh; the padded
// area beyond the chunk has no new trades, so the no-op buckets
// are nearly free. Caught live 2026-05-14 on the first 10k-ledger
// test backfill — chunk's natural ts range was ~4h, which was
// fine for prices_1h but failed every coarser CAGG.
type CAGGSpec struct {
	Name      string
	MinWindow time.Duration
}

var CAGGsLiveForever = []CAGGSpec{
	{Name: "prices_1h", MinWindow: 3 * time.Hour},
	{Name: "prices_4h", MinWindow: 12 * time.Hour},
	{Name: "prices_1d", MinWindow: 3 * 24 * time.Hour},
	{Name: "prices_1w", MinWindow: 3 * 7 * 24 * time.Hour},
	// 1mo CAGG uses calendar months, not 30-day windows. Padding
	// to ~93 days (3 calendar months) trivially clears the
	// "must span >= 2 buckets" minimum without depending on month
	// arithmetic at the storage seam.
	{Name: "prices_1mo", MinWindow: 93 * 24 * time.Hour},
}

// PadRefreshWindow expands [from, to] to span at least minWindow
// while staying centered on the original midpoint. Used by the
// backfill tool's per-chunk CAGG-refresh helper to satisfy the
// 2-buckets-minimum invariant. Padded area beyond the chunk's
// actual data is materialized as empty buckets (cheap).
func PadRefreshWindow(from, to time.Time, minWindow time.Duration) (time.Time, time.Time) {
	span := to.Sub(from)
	if span >= minWindow {
		return from, to
	}
	pad := (minWindow - span) / 2
	return from.Add(-pad), to.Add(pad)
}

// RefreshContinuousAggregate force-materialises a continuous
// aggregate over the given time window. Calls Timescale's
// `refresh_continuous_aggregate(view, from, to)` procedure, which
// blocks until the materialisation completes.
//
// Required after backfill runs because the policy refresher only
// rolls forward — historical inserts (ts < now()-policy_window)
// don't trigger materialisation, and the 90-day retention on raw
// trades drops chunks before the policy's natural cadence picks
// them up. The backfill tool calls this at the end of each chunk
// to make CAGG materialisation atomic with the trade insert.
//
// Idempotent: refreshing an already-materialised range is a no-op.
// Fail-loud on unknown view name (defends against typo-driven
// SQL injection through the view-name string format).
func (s *Store) RefreshContinuousAggregate(ctx context.Context, viewName string, from, to time.Time) error {
	if !allowedCAGGViews[viewName] {
		return fmt.Errorf("timescale: RefreshContinuousAggregate: unknown view %q", viewName)
	}
	// CALL refresh_continuous_aggregate(view, $1::timestamptz, $2::timestamptz).
	// The first arg is REGCLASS in Timescale's signature, which pgx
	// can't placeholder; concatenating from the allow-list is safe.
	// Time params need explicit ::timestamptz casts: lib/pq's
	// implementation of stored-procedure CALL doesn't propagate the
	// declared parameter types from the procedure signature, so an
	// untyped placeholder fails with `42P18: could not determine
	// data type of parameter $1`. Caught live 2026-05-14 on the
	// first real backfill that exercised this path.
	q := fmt.Sprintf(`CALL refresh_continuous_aggregate('%s', $1::timestamptz, $2::timestamptz)`, viewName)
	// Retry on 55P03 (concurrent refresh) — Timescale serializes
	// refresh of the same CAGG, so two parallel callers (e.g.
	// backfill chunks racing on prices_1mo) collide. Backoff is
	// short because the other caller's refresh is fast for our
	// padded windows. Caught live 2026-05-14 on a `-parallel 4`
	// SDEX backfill: every chunk's prices_1mo refresh raced.
	const maxAttempts = 5
	for attempt := 0; attempt < maxAttempts; attempt++ {
		_, err := s.db.ExecContext(ctx, q, from, to)
		if err == nil {
			return nil
		}
		if !isConcurrentRefreshErr(err) || attempt == maxAttempts-1 {
			return fmt.Errorf("timescale: RefreshContinuousAggregate(%s): %w", viewName, err)
		}
		// Exponential backoff with jitter: 200ms, 400ms, 800ms, 1.6s.
		// Sleep capped via select so ctx-cancel exits promptly.
		backoff := time.Duration(200*(1<<attempt)) * time.Millisecond
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
	}
	return nil // unreachable
}

// isConcurrentRefreshErr reports whether err is the
// SQLSTATE 55P03 ("could not refresh continuous aggregate due to
// a concurrent refresh") emitted by Timescale when two callers
// race on the same CAGG. Matched by message substring rather
// than driver-typed code so we don't take a hard pq dep here.
func isConcurrentRefreshErr(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "55P03") ||
		strings.Contains(err.Error(), "concurrent refresh"))
}

// CAGGCoverage describes the time range and row count of the
// hourly-or-larger continuous aggregate (prices_1h is canonical).
// This is the source-of-truth "do we have historical aggregates"
// answer — raw trades have a 90-day retention but the hourly+
// CAGGs are retained forever (migration 0002), so a healthy
// since-genesis backfill leaves a wide CAGGCoverage even though
// the raw trades table only spans the last 90 days.
type CAGGCoverage struct {
	EarliestBucket time.Time
	LatestBucket   time.Time
	BucketCount    int64
}

// CAGGCoverageStats returns the earliest + latest buckets in
// prices_1h. Sub-second under the (base_asset, quote_asset, bucket)
// index. Empty when the CAGG has not yet been materialised at all
// (cold-start before any aggregator tick).
func (s *Store) CAGGCoverageStats(ctx context.Context) (CAGGCoverage, error) {
	const q = `SELECT MIN(bucket), MAX(bucket), COUNT(*) FROM prices_1h`
	var (
		minB, maxB sql.NullTime
		count      int64
	)
	if err := s.db.QueryRowContext(ctx, q).Scan(&minB, &maxB, &count); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return CAGGCoverage{}, nil
		}
		return CAGGCoverage{}, err
	}
	out := CAGGCoverage{BucketCount: count}
	if minB.Valid {
		out.EarliestBucket = minB.Time
	}
	if maxB.Valid {
		out.LatestBucket = maxB.Time
	}
	return out, nil
}

// BackfillCoverage is one row of the per-source coverage summary —
// the earliest + latest ledgers we have any trade for, plus the
// total trade count. Lets the diagnostics surface answer the
// operator's first question: "do we have data from genesis to
// tip?" — yes if EarliestLedger ≤ source's known genesis and
// LatestLedger ≈ network tip; gaps inside that range aren't
// detected by this projection (would need a much heavier
// distinct-ledger scan).
//
// CEX/FX sources report (0, 0) because their trades carry no
// Stellar ledger context — we record TradeCount but the
// EarliestLedger / LatestLedger columns are meaningless.
type BackfillCoverage struct {
	Source         string
	EarliestLedger int64
	LatestLedger   int64
	TradeCount     int64
}

// BackfillCoverageStats is intentionally a no-op (returns no rows).
// Retained only for interface/return-type compatibility with the
// CoverageCache scaffolding, which is removed in the server-side
// snapshot-pregeneration refactor (#16).
//
// Why it does nothing: it used to scan `trades` per source for
// earliest/latest ledger + an approximate trade count, cached by
// CoverageCache and read via buildBackfillCoverage. The 2026-05
// cursor-first refactor made that output 100% dead — every mapped
// source's density/covered/earliest/latest is derived from the
// backfill-cursor union, and buildBackfillCoverage's cacheRows path
// `continue`s past every source this function scanned (all are in
// sourceGenesisLedger). So the result was thrown away entirely
// while the function still ran ~13 per-source ts-ordered scans + a
// ~15s approximate_row_count('trades') every refresh interval.
// Oracle sources (band/redstone/reflector-*) write to
// oracle_updates and have ZERO `trades` rows, so their scan could
// not chunk-exclude and walked the full ~2700-chunk hypertable to
// the statement-timeout (57014) — the root cause of the
// CoverageCache cold-start hang and a primary SLO-burn contributor.
// #12 only time-bounded that wasted work; this removes it entirely
// (the honest fix). Cursor-first coverage + the source_entry_counts
// tally already supply everything the diagnostics surface needs.
func (s *Store) BackfillCoverageStats(_ context.Context) ([]BackfillCoverage, error) {
	return nil, nil
}

// SupplyCoverageStats returns the current coverage state of the
// asset_supply_history hypertable. One window-function query that
// reads the latest row per asset_key and partitions by SEP-41 vs
// classic. The btree index on (asset_key, ledger_sequence, time)
// makes the DISTINCT ON cheap.
func (s *Store) SupplyCoverageStats(ctx context.Context) (SupplyCoverage, error) {
	const q = `
		WITH latest AS (
		    SELECT DISTINCT ON (asset_key)
		        asset_key, time, ledger_sequence
		    FROM asset_supply_history
		    ORDER BY asset_key, ledger_sequence DESC, time DESC
		)
		SELECT
		    COUNT(*) FILTER (WHERE asset_key LIKE 'C%' AND LENGTH(asset_key) = 56) AS sep41,
		    COUNT(*) FILTER (WHERE NOT (asset_key LIKE 'C%' AND LENGTH(asset_key) = 56)) AS classic,
		    MAX(time)         AS last_at,
		    MAX(ledger_sequence) AS last_ledger
		FROM latest
	`
	var (
		sep41, classic int
		lastAt         sql.NullTime
		lastLedger     sql.NullInt64
	)
	if err := s.db.QueryRowContext(ctx, q).Scan(&sep41, &classic, &lastAt, &lastLedger); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SupplyCoverage{}, nil
		}
		return SupplyCoverage{}, err
	}
	out := SupplyCoverage{
		ClassicAssets: classic,
		SEP41Assets:   sep41,
	}
	if lastAt.Valid {
		out.LastSnapshotAt = lastAt.Time
	}
	if lastLedger.Valid {
		out.LatestLedger = lastLedger.Int64
	}
	return out, nil
}

// SourceEntryCounts returns the per-source running entry tally from
// `source_entry_counts` (migration 0035) — trades + oracle_updates,
// keyed by source. This is a ~20-row PK scan of a tiny tally table,
// so it is O(1)-ish and ALWAYS fast — unlike BackfillCoverageStats
// it does not touch the trades/oracle_updates hypertables, so it
// stays responsive even during an all-time backfill (the whole
// reason the counter exists). Powers the `entries` column on
// /v1/diagnostics/ingestion.
func (s *Store) SourceEntryCounts(ctx context.Context) (map[string]int64, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT source, entry_count FROM source_entry_counts`)
	if err != nil {
		return nil, fmt.Errorf("timescale: SourceEntryCounts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make(map[string]int64, 32)
	for rows.Next() {
		var src string
		var n int64
		if err := rows.Scan(&src, &n); err != nil {
			return nil, fmt.Errorf("timescale: SourceEntryCounts scan: %w", err)
		}
		out[src] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("timescale: SourceEntryCounts rows: %w", err)
	}
	return out, nil
}

// SeedSourceEntryCounts authoritatively recomputes source_entry_counts
// from a full GROUP BY over `trades` + `oracle_updates` and overwrites
// the tally (SET, not ADD — so re-running converges). Returns the
// number of source rows reconciled.
//
// This is the heavy reconciliation the writers' incremental bump can
// never do on its own (a fresh counter doesn't know pre-counter
// history). Operator one-shot via `ratesengine-ops seed-entry-counts`
// — run post-backfill: the GROUP BY scans every trades chunk in one
// transaction (fine within the 4096 max_locks budget, slow + IO-hungry
// mid-backfill). A source appears once even if it somehow wrote to
// both tables (sum over the UNION ALL).
func (s *Store) SeedSourceEntryCounts(ctx context.Context) (int64, error) {
	const q = `
        INSERT INTO source_entry_counts AS sec (source, entry_count, updated_at)
        SELECT source, sum(c)::bigint, now()
        FROM (
            SELECT source, count(*) AS c FROM trades         GROUP BY source
            UNION ALL
            SELECT source, count(*) AS c FROM oracle_updates GROUP BY source
        ) u
        GROUP BY source
        ON CONFLICT (source) DO UPDATE
          SET entry_count = EXCLUDED.entry_count,
              updated_at  = EXCLUDED.updated_at
    `
	res, err := s.db.ExecContext(ctx, q)
	if err != nil {
		return 0, fmt.Errorf("timescale: SeedSourceEntryCounts: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}
