package timescale

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/RatesEngine/rates-engine/internal/aggregate/baseline"
	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// HistoryPoint is a single bucket's worth of aggregated price +
// volume, returned by [Store.HistoryPoints]. Wire shape mirrors the
// API's `{t, p, v_usd}` triple — the API binary's adapter is a
// pure passthrough.
type HistoryPoint struct {
	Bucket    time.Time
	VWAP      string  // NUMERIC text from Postgres
	VolumeUSD *string // NULL when no usd_volume column entries — e.g. early classic-only ledgers
}

// HistoryGranularity is the CAGG selector for [Store.HistoryPoints].
// Stable string values matching the API's `granularity` query
// parameter and the migration's table names (prices_<granularity>).
type HistoryGranularity string

const (
	Granularity1m  HistoryGranularity = "1m"
	Granularity15m HistoryGranularity = "15m"
	Granularity1h  HistoryGranularity = "1h"
	Granularity4h  HistoryGranularity = "4h"
	Granularity1d  HistoryGranularity = "1d"
	Granularity1w  HistoryGranularity = "1w"
	Granularity1mo HistoryGranularity = "1mo"
)

// Validate reports whether g is one of the seven supported
// granularities. Caller surfaces unknown granularities as 400.
func (g HistoryGranularity) Validate() error {
	switch g {
	case Granularity1m, Granularity15m, Granularity1h, Granularity4h,
		Granularity1d, Granularity1w, Granularity1mo:
		return nil
	}
	return fmt.Errorf("unknown granularity %q (want one of: 1m, 15m, 1h, 4h, 1d, 1w, 1mo)", g)
}

// closedBucketInterval is the Postgres INTERVAL string that the
// closed-bucket guard subtracts from now() per ADR-0015. Equal to
// the granularity's bucket size.
func (g HistoryGranularity) closedBucketInterval() string {
	switch g {
	case Granularity1m:
		return "1 minute"
	case Granularity15m:
		return "15 minutes"
	case Granularity1h:
		return "1 hour"
	case Granularity4h:
		return "4 hours"
	case Granularity1d:
		return "1 day"
	case Granularity1w:
		return "1 week"
	case Granularity1mo:
		return "1 month"
	}
	return ""
}

// HistoryPoints returns every CLOSED bucket for the pair from the
// CAGG matching `granularity`, ordered chronologically (ASC). Used
// by /v1/history/since-inception to serve the full historical
// series at the requested granularity.
//
// Per ADR-0015 the in-progress bucket is excluded via a
// `bucket + <granularity> <= now()` guard.
//
// `limit` clamps the row count; passing 0 returns all rows. The
// API caller passes the spec-bounded value (default unbounded for
// since-inception; clients paginate via Pagination.next when we
// add that surface).
//
// Returns an empty slice + nil error when the pair has no closed
// buckets at this granularity yet.
func (s *Store) HistoryPoints(ctx context.Context, p canonical.Pair, granularity HistoryGranularity, limit int) ([]HistoryPoint, error) {
	if err := granularity.Validate(); err != nil {
		return nil, err
	}
	// SQL injection guard: granularity goes via Validate() against
	// a fixed enum, then composes into the table name. Body of the
	// query plus params binds via $1/$2. The fmt.Sprintf format is
	// safe — both `table` and `interval` come from a closed enum
	// after Validate(), not from user input.
	table := "prices_" + string(granularity)
	interval := granularity.closedBucketInterval()
	// #nosec G201 — table + interval are derived from the validated
	// enum, not user input. See HistoryGranularity.Validate above.
	q := fmt.Sprintf(`
		SELECT bucket, vwap::text, volume_usd::text
		  FROM %s
		 WHERE base_asset = $1
		   AND quote_asset = $2
		   AND bucket + INTERVAL '%s' <= now()
		 ORDER BY bucket ASC
	`, table, interval)
	args := []any{p.Base.String(), p.Quote.String()}
	if limit > 0 {
		q += " LIMIT $3"
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("timescale: HistoryPoints[%s]: %w", granularity, err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]HistoryPoint, 0, 1024)
	for rows.Next() {
		var pt HistoryPoint
		var vusd sql.NullString
		if err := rows.Scan(&pt.Bucket, &pt.VWAP, &vusd); err != nil {
			return nil, fmt.Errorf("timescale: HistoryPoints[%s] scan: %w", granularity, err)
		}
		if vusd.Valid {
			s := vusd.String
			pt.VolumeUSD = &s
		}
		out = append(out, pt)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("timescale: HistoryPoints[%s] rows: %w", granularity, err)
	}
	return out, nil
}

// HistoryPointsInRange is [HistoryPoints] with an explicit
// [from, to) time bound on the bucket column. Used by /v1/chart
// to serve a rolling-window view (timeframe → from = now-tf, to =
// now); the same closed-bucket guard from [HistoryPoints] applies.
//
// `from` zero disables the lower bound (equivalent to since-
// inception); `to` zero disables the upper bound. `limit` 0 returns
// all rows.
//
// Empty slice + nil error when the pair has no closed buckets in
// the requested window.
func (s *Store) HistoryPointsInRange(
	ctx context.Context,
	p canonical.Pair,
	granularity HistoryGranularity,
	from, to time.Time,
	limit int,
) ([]HistoryPoint, error) {
	if err := granularity.Validate(); err != nil {
		return nil, err
	}
	table := "prices_" + string(granularity)
	interval := granularity.closedBucketInterval()
	// Build args incrementally so the placeholder count matches the
	// optional from/to/limit clauses.
	args := []any{p.Base.String(), p.Quote.String()}
	clauses := "base_asset = $1\n   AND quote_asset = $2\n   AND bucket + INTERVAL '" + interval + "' <= now()"
	if !from.IsZero() {
		args = append(args, from.UTC())
		clauses += fmt.Sprintf("\n   AND bucket >= $%d", len(args))
	}
	if !to.IsZero() {
		args = append(args, to.UTC())
		clauses += fmt.Sprintf("\n   AND bucket < $%d", len(args))
	}
	// #nosec G201 — table + interval are derived from the validated
	// enum, not user input. See HistoryGranularity.Validate.
	q := fmt.Sprintf(`
		SELECT bucket, vwap::text, volume_usd::text
		  FROM %s
		 WHERE %s
		 ORDER BY bucket ASC
	`, table, clauses)
	if limit > 0 {
		args = append(args, limit)
		q += fmt.Sprintf(" LIMIT $%d", len(args))
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("timescale: HistoryPointsInRange[%s]: %w", granularity, err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]HistoryPoint, 0, 1024)
	for rows.Next() {
		var pt HistoryPoint
		var vusd sql.NullString
		if err := rows.Scan(&pt.Bucket, &pt.VWAP, &vusd); err != nil {
			return nil, fmt.Errorf("timescale: HistoryPointsInRange[%s] scan: %w", granularity, err)
		}
		if vusd.Valid {
			s := vusd.String
			pt.VolumeUSD = &s
		}
		out = append(out, pt)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("timescale: HistoryPointsInRange[%s] rows: %w", granularity, err)
	}
	return out, nil
}

// Vwap1mRow is one row from the prices_1m continuous aggregate.
// The fields mirror migrations/0002_create_price_aggregates.up.sql
// — see that file for the SQL semantics. Bucket is the START of the
// 1-minute window; the window's END is `bucket + 1 minute`.
type Vwap1mRow struct {
	Bucket     time.Time
	BaseAsset  string
	QuoteAsset string
	// VWAP, FirstPrice, LastPrice, HighPrice, LowPrice are decimal
	// strings exactly as Postgres serialises NUMERIC. Storing them
	// as strings avoids a float round-trip (ADR-0003) — handlers
	// that need a numeric value parse with big.Rat.
	VWAP       string
	TradeCount int64
	Sources    []string
}

// RecentClosedVWAP1mForPair returns up to `limit` most-recent CLOSED
// 1-minute buckets from the prices_1m CAGG for the given pair,
// newest first. Same closed-bucket guard as
// [LatestClosedVWAP1mForPair] (ADR-0015).
//
// Returns an empty slice + nil error when the pair has no closed
// buckets in scope. The caller (typically the SEP-40 prices
// endpoint) distinguishes "no observations" from "asset unknown"
// by combining this with an asset-existence check.
//
// limit is the caller's responsibility to clamp; this method
// assumes a sane bound and doesn't second-guess.
func (s *Store) RecentClosedVWAP1mForPair(ctx context.Context, p canonical.Pair, limit int) ([]Vwap1mRow, error) {
	const q = `
        SELECT bucket, base_asset, quote_asset, vwap::text, trade_count, sources
          FROM prices_1m
         WHERE base_asset = $1
           AND quote_asset = $2
           AND bucket + INTERVAL '1 minute' <= now()
         ORDER BY bucket DESC
         LIMIT $3
    `
	rows, err := s.db.QueryContext(ctx, q,
		p.Base.String(), p.Quote.String(), limit,
	)
	if err != nil {
		return nil, fmt.Errorf("timescale: RecentClosedVWAP1mForPair: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]Vwap1mRow, 0, limit)
	for rows.Next() {
		var row Vwap1mRow
		if err := rows.Scan(
			&row.Bucket, &row.BaseAsset, &row.QuoteAsset,
			&row.VWAP, &row.TradeCount,
			(*stringArray)(&row.Sources),
		); err != nil {
			return nil, fmt.Errorf("timescale: RecentClosedVWAP1mForPair scan: %w", err)
		}
		normalizeVwapSources(&row)
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("timescale: RecentClosedVWAP1mForPair rows: %w", err)
	}
	return out, nil
}

// LatestClosedVWAP1mForPair returns the most-recent CLOSED 1-minute
// bucket from the prices_1m CAGG for the given pair. Per ADR-0015
// the API serves only closed buckets — this method explicitly
// excludes the in-progress bucket via a `bucket + 1 minute <= now()`
// guard, even though the CAGG's refresh policy already drops the
// open bucket from materialised rows.
//
// Returns [sql.ErrNoRows] when the pair has no closed bucket yet —
// callers translate that to the API's price-not-found problem or
// fall back to the latest-trade path.
func (s *Store) LatestClosedVWAP1mForPair(ctx context.Context, p canonical.Pair) (Vwap1mRow, error) {
	const q = `
        SELECT bucket, base_asset, quote_asset, vwap::text, trade_count, sources
          FROM prices_1m
         WHERE base_asset = $1
           AND quote_asset = $2
           AND bucket + INTERVAL '1 minute' <= now()
         ORDER BY bucket DESC
         LIMIT 1
    `
	var row Vwap1mRow
	err := s.db.QueryRowContext(ctx, q,
		p.Base.String(), p.Quote.String(),
	).Scan(
		&row.Bucket,
		&row.BaseAsset,
		&row.QuoteAsset,
		&row.VWAP,
		&row.TradeCount,
		(*stringArray)(&row.Sources),
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Vwap1mRow{}, sql.ErrNoRows
	}
	if err != nil {
		return Vwap1mRow{}, fmt.Errorf("timescale: LatestClosedVWAP1mForPair: %w", err)
	}
	normalizeVwapSources(&row)
	return row, nil
}

// TimedVWAPsForPair1m returns chronologically-ordered (oldest-first)
// (vwap, bucket_end) pairs from prices_1m for the half-open window
// [from, to). Used by the multi-window baseline refresher (which
// needs the timestamp to apply [baseline.SplitByLookback] for the
// 1d / 7d / 30d sub-windows).
//
// The bucket-end timestamp is the bucket's start + 1 minute (CAGG
// stores the start; the API surface uses the end). Empty slice +
// nil error when the pair has no closed buckets in the window.
func (s *Store) TimedVWAPsForPair1m(ctx context.Context, p canonical.Pair, from, to time.Time) ([]baseline.TimedVWAP, error) {
	if !to.After(from) {
		return nil, fmt.Errorf("timescale: TimedVWAPsForPair1m: to %v <= from %v", to, from)
	}
	const q = `
        SELECT vwap::float8, bucket + INTERVAL '1 minute'
          FROM prices_1m
         WHERE base_asset = $1
           AND quote_asset = $2
           AND bucket >= $3
           AND bucket <  $4
         ORDER BY bucket ASC
    `
	rows, err := s.db.QueryContext(ctx, q,
		p.Base.String(), p.Quote.String(),
		from.UTC(), to.UTC(),
	)
	if err != nil {
		return nil, fmt.Errorf("timescale: TimedVWAPsForPair1m: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]baseline.TimedVWAP, 0, 256)
	for rows.Next() {
		var t baseline.TimedVWAP
		if err := rows.Scan(&t.VWAP, &t.BucketEnd); err != nil {
			return nil, fmt.Errorf("timescale: TimedVWAPsForPair1m scan: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("timescale: TimedVWAPsForPair1m rows: %w", err)
	}
	return out, nil
}

// VWAPsForPair1m returns chronologically-ordered (oldest-first) VWAP
// values from prices_1m where bucket falls in [from, to). Used by
// the baseline refresher to pull the 30-day training window for a
// pair. Returns the bare float series (not the full Vwap1mRow) —
// the caller's downstream consumer (`baseline.ReturnsFromVWAPs`)
// only needs the prices, not the metadata.
//
// Empty slice + nil error when the pair has no closed buckets in
// the window.
//
// VWAP is parsed from the NUMERIC column via the Postgres double-
// precision cast — the baseline math runs in float64 anyway and
// the small precision loss on a per-bucket VWAP doesn't matter for
// statistical aggregates over hundreds of buckets.
func (s *Store) VWAPsForPair1m(ctx context.Context, p canonical.Pair, from, to time.Time) ([]float64, error) {
	if !to.After(from) {
		return nil, fmt.Errorf("timescale: VWAPsForPair1m: to %v <= from %v", to, from)
	}
	const q = `
        SELECT vwap::float8
          FROM prices_1m
         WHERE base_asset = $1
           AND quote_asset = $2
           AND bucket >= $3
           AND bucket <  $4
         ORDER BY bucket ASC
    `
	rows, err := s.db.QueryContext(ctx, q,
		p.Base.String(), p.Quote.String(),
		from.UTC(), to.UTC(),
	)
	if err != nil {
		return nil, fmt.Errorf("timescale: VWAPsForPair1m: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]float64, 0, 256)
	for rows.Next() {
		var v float64
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("timescale: VWAPsForPair1m scan: %w", err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("timescale: VWAPsForPair1m rows: %w", err)
	}
	return out, nil
}

// stringArray is a [sql.Scanner] for Postgres TEXT[] / VARCHAR[]
// columns scanning into a Go []string. Used by the `sources` column
// in prices_1m.
//
// Implements minimal parsing of the Postgres array text format:
// `{a,b,c}`. Quoted entries (with embedded commas) aren't supported
// — fine here because source names are identifier-shaped.
type stringArray []string

// normalizeVwapSources enforces a stable lexical ordering for the
// materialized-view `sources` array before it crosses the storage
// boundary. The current CAGG SQL uses `array_agg(DISTINCT source)`
// without an ORDER BY, so sorting here restores the public contract's
// deterministic contributor ordering for both existing and future rows
// without requiring an immediate CAGG rebuild.
func normalizeVwapSources(row *Vwap1mRow) {
	if len(row.Sources) < 2 {
		return
	}
	sort.Strings(row.Sources)
}

// Scan implements [sql.Scanner].
func (a *stringArray) Scan(src any) error {
	if src == nil {
		*a = nil
		return nil
	}
	var s string
	switch v := src.(type) {
	case []byte:
		s = string(v)
	case string:
		s = v
	default:
		return fmt.Errorf("stringArray: unsupported scan type %T", src)
	}
	if len(s) < 2 || s[0] != '{' || s[len(s)-1] != '}' {
		return fmt.Errorf("stringArray: malformed Postgres array literal %q", s)
	}
	inner := s[1 : len(s)-1]
	if inner == "" {
		*a = []string{}
		return nil
	}
	out := []string{}
	start := 0
	for i := 0; i <= len(inner); i++ {
		if i == len(inner) || inner[i] == ',' {
			elt := inner[start:i]
			// NULL elements come through as the literal "NULL"
			// (case-sensitive); array_agg(DISTINCT source) over a
			// non-null source column never produces these, but
			// guard anyway.
			if elt != "NULL" {
				out = append(out, elt)
			}
			start = i + 1
		}
	}
	*a = out
	return nil
}

// Volume24hUSDForAsset returns the trailing 24h USD-denominated
// trade volume for an asset, summing across every pair the asset
// participates in (as base OR quote). Reads from the prices_1m
// CAGG (1440 rolled buckets per 24h) — far cheaper than scanning
// the trades hypertable directly.
//
// Returns "0" when the asset has zero trades in the window;
// callers presenting the field should treat zero distinctly from
// null (zero = "asset existed and had no trades", null = "asset
// not tracked"). The string return matches the rest of the
// aggregate API — Postgres NUMERIC sums don't fit cleanly into
// any Go fixed-width type.
//
// assetKey is the canonical asset string (e.g.
// "native", "USDC:GA5...", "soroban:CC..."); matches what the
// trades hypertable stores in base_asset / quote_asset.
//
// # Scope caveat (launch-readiness L2.2)
//
// The CAGG sums `coalesce(usd_volume, 0)` per row. Per-trade
// `usd_volume` is populated at insert time (see `tradeUSDVolume`
// in trades.go) only for off-chain CEX/FX sources with `fiat:USD`
// or USD-pegged-stablecoin quotes; on-chain trades (Stellar
// SDEX, Soroswap, Aquarius, Phoenix, Comet) currently store
// NULL, so they contribute 0 to this sum. Operators comparing
// against external aggregators (CoinGecko, CMC) will see
// systematically lower numbers until the on-chain FX-anchor
// backfill lands. The OpenAPI surface (`volume_24h_usd`) carries
// the same caveat in its description.
func (s *Store) Volume24hUSDForAsset(ctx context.Context, assetKey string) (string, error) {
	const q = `
        SELECT COALESCE(sum(volume_usd), 0)::text
          FROM prices_1m
         WHERE (base_asset = $1 OR quote_asset = $1)
           AND bucket >= now() - INTERVAL '24 hours'
           AND bucket  < now()
    `
	var out string
	if err := s.db.QueryRowContext(ctx, q, assetKey).Scan(&out); err != nil {
		return "", fmt.Errorf("timescale: Volume24hUSDForAsset(%s): %w", assetKey, err)
	}
	return out, nil
}
