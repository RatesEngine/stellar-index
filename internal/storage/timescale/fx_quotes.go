package timescale

import (
	"context"
	"fmt"
	"time"
)

// FXQuote is one (date, ticker) snapshot from the forex pipeline.
// Rates are NUMERIC in the DB; we round-trip them through string
// in the wire shape but use float64 here for the in-process
// comparison + chart math (precision loss above 2^53 isn't a
// concern for fx rates which are O(1)–O(10000)).
type FXQuote struct {
	Bucket     time.Time
	Ticker     string
	RateUSD    float64
	InverseUSD float64
	Source     string
}

// InsertFXQuoteBatch upserts a slice of fx quotes. Idempotent on
// the (ticker, bucket) primary key — re-running with the same
// (ticker, date) updates `rate_usd` + `inverse_usd` + `source`,
// preserving the original `observed_at` only by virtue of the
// DEFAULT not firing on UPDATE. (We deliberately don't refresh
// observed_at because it's diagnostic: the row's first observation
// date is more useful than its most-recent.)
//
// Empty slice is a no-op.
func (s *Store) InsertFXQuoteBatch(ctx context.Context, quotes []FXQuote) error {
	if len(quotes) == 0 {
		return nil
	}
	const stmt = `
		INSERT INTO fx_quotes (bucket, ticker, rate_usd, inverse_usd, source)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (ticker, bucket) DO UPDATE
		   SET rate_usd    = EXCLUDED.rate_usd,
		       inverse_usd = EXCLUDED.inverse_usd,
		       source      = EXCLUDED.source
	`
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("timescale: InsertFXQuoteBatch begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, q := range quotes {
		if q.Ticker == "" || q.RateUSD <= 0 || q.InverseUSD <= 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, stmt,
			q.Bucket, q.Ticker, q.RateUSD, q.InverseUSD, q.Source,
		); err != nil {
			return fmt.Errorf("timescale: InsertFXQuoteBatch ticker=%q: %w", q.Ticker, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("timescale: InsertFXQuoteBatch commit: %w", err)
	}
	return nil
}

// ListFXHistory returns daily snapshots for `ticker` in
// [from, to], ascending. Empty slice when nothing matches.
//
// Used by /v1/currencies/{ticker} to populate `history_1y`,
// `history_all`, etc. on the response.
func (s *Store) ListFXHistory(ctx context.Context, ticker string, from, to time.Time) ([]FXQuote, error) {
	const stmt = `
		SELECT bucket, ticker, rate_usd, inverse_usd, COALESCE(source, '')
		  FROM fx_quotes
		 WHERE ticker = $1
		   AND bucket BETWEEN $2 AND $3
		 ORDER BY bucket ASC
	`
	rows, err := s.db.QueryContext(ctx, stmt, ticker, from, to)
	if err != nil {
		return nil, fmt.Errorf("timescale: ListFXHistory: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []FXQuote
	for rows.Next() {
		var q FXQuote
		if err := rows.Scan(&q.Bucket, &q.Ticker, &q.RateUSD, &q.InverseUSD, &q.Source); err != nil {
			return nil, fmt.Errorf("timescale: ListFXHistory scan: %w", err)
		}
		out = append(out, q)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("timescale: ListFXHistory rows: %w", err)
	}
	return out, nil
}

// LatestFXBucketPerTicker returns the most-recent (ticker, bucket)
// the table holds. Used by the forex worker's gap-detector to
// resume backfill from the newest persisted date instead of
// re-inserting everything.
//
// Empty map when the table is empty.
func (s *Store) LatestFXBucketPerTicker(ctx context.Context) (map[string]time.Time, error) {
	const stmt = `
		SELECT ticker, MAX(bucket)
		  FROM fx_quotes
		 GROUP BY ticker
	`
	rows, err := s.db.QueryContext(ctx, stmt)
	if err != nil {
		return nil, fmt.Errorf("timescale: LatestFXBucketPerTicker: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]time.Time{}
	for rows.Next() {
		var ticker string
		var bucket time.Time
		if err := rows.Scan(&ticker, &bucket); err != nil {
			return nil, fmt.Errorf("timescale: LatestFXBucketPerTicker scan: %w", err)
		}
		out[ticker] = bucket
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("timescale: LatestFXBucketPerTicker rows: %w", err)
	}
	return out, nil
}
