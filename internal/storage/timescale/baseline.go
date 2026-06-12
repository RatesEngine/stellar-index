package timescale

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/aggregate/baseline"
	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
)

// StoredBaseline is the wire shape between the storage layer and
// the aggregator's refresh worker. Wraps a [baseline.MultiBaseline]
// (1d / 7d / 30d windows per ADR-0019 Phase 2) with the metadata
// the persistence layer needs to track refresh freshness.
//
// Day1 / Day7 may be nil when the pair's data history is too short
// for that window — the migration's CHECK constraints + the wire
// shape's nil-pointer convention together encode "bootstrap on this
// scale". Day30 is required (a stored row with n_30d < MinSamples
// fails pre-flight in [Store.UpsertBaseline]).
type StoredBaseline struct {
	Pair        canonical.Pair
	ComputedAt  time.Time
	WindowStart time.Time
	WindowEnd   time.Time
	Multi       baseline.MultiBaseline
}

// ErrBaselineNotFound is returned by [Store.LatestBaseline] when
// the requested pair has never had a baseline written. Callers in
// the aggregator's confidence-score loop translate this into the
// "bootstrap" branch (ADR-0019 §"Bootstrap (warmup) policy").
var ErrBaselineNotFound = errors.New("timescale: baseline not found for pair")

// UpsertBaseline writes (or overwrites) the baseline row for the
// given pair. UPSERT semantics: one row per pair, the latest
// refresh wins. The aggregator's refresh worker is the only writer.
//
// Validates that:
//
//   - Day30 is non-nil (a row without a 30d baseline is meaningless;
//     the table's NOT NULL constraint on median/mad/sample_count
//     would reject it anyway, but pre-flight gives a clearer error)
//   - Day30.N >= [baseline.MinSamples]
//   - WindowEnd > WindowStart
func (s *Store) UpsertBaseline(ctx context.Context, sb StoredBaseline) error {
	if sb.Multi.Day30 == nil {
		return fmt.Errorf("timescale: UpsertBaseline %s: 30d baseline is nil", sb.Pair.String())
	}
	if sb.Multi.Day30.N < baseline.MinSamples {
		return fmt.Errorf("timescale: UpsertBaseline %s: 30d sample_count=%d < %d",
			sb.Pair.String(), sb.Multi.Day30.N, baseline.MinSamples)
	}
	if !sb.WindowEnd.After(sb.WindowStart) {
		return fmt.Errorf("timescale: UpsertBaseline %s: window_end %v <= window_start %v",
			sb.Pair.String(), sb.WindowEnd, sb.WindowStart)
	}

	const q = `
		INSERT INTO volatility_baseline_1m
		    (base_asset, quote_asset, computed_at, window_start, window_end,
		     median, mad, sample_count,
		     median_1d, mad_1d, n_1d,
		     median_7d, mad_7d, n_7d)
		VALUES
		    ($1, $2, $3, $4, $5,
		     $6, $7, $8,
		     $9, $10, $11,
		     $12, $13, $14)
		ON CONFLICT (base_asset, quote_asset) DO UPDATE SET
		    computed_at  = EXCLUDED.computed_at,
		    window_start = EXCLUDED.window_start,
		    window_end   = EXCLUDED.window_end,
		    median       = EXCLUDED.median,
		    mad          = EXCLUDED.mad,
		    sample_count = EXCLUDED.sample_count,
		    median_1d    = EXCLUDED.median_1d,
		    mad_1d       = EXCLUDED.mad_1d,
		    n_1d         = EXCLUDED.n_1d,
		    median_7d    = EXCLUDED.median_7d,
		    mad_7d       = EXCLUDED.mad_7d,
		    n_7d         = EXCLUDED.n_7d
	`
	d1Median, d1MAD, d1N := nullableBaseline(sb.Multi.Day1)
	d7Median, d7MAD, d7N := nullableBaseline(sb.Multi.Day7)

	_, err := s.db.ExecContext(ctx, q,
		sb.Pair.Base.String(),
		sb.Pair.Quote.String(),
		sb.ComputedAt.UTC(),
		sb.WindowStart.UTC(),
		sb.WindowEnd.UTC(),
		sb.Multi.Day30.Median,
		sb.Multi.Day30.MAD,
		sb.Multi.Day30.N,
		d1Median, d1MAD, d1N,
		d7Median, d7MAD, d7N,
	)
	if err != nil {
		return fmt.Errorf("timescale: UpsertBaseline %s: %w", sb.Pair.String(), err)
	}
	return nil
}

// nullableBaseline converts a `*baseline.Baseline` into the three
// (NullFloat, NullFloat, NullInt) pgx-compatible scalars that the
// `*_1d` / `*_7d` columns expect. Nil pointer → all-NULL.
func nullableBaseline(b *baseline.Baseline) (sql.NullFloat64, sql.NullFloat64, sql.NullInt64) {
	if b == nil {
		return sql.NullFloat64{}, sql.NullFloat64{}, sql.NullInt64{}
	}
	return sql.NullFloat64{Valid: true, Float64: b.Median},
		sql.NullFloat64{Valid: true, Float64: b.MAD},
		sql.NullInt64{Valid: true, Int64: int64(b.N)}
}

// LatestBaseline returns the current baseline for `pair`. Returns
// [ErrBaselineNotFound] when no row exists for the pair (the
// caller should fall through to the bootstrap policy).
//
// API hot path; covered by the (base_asset, quote_asset) primary-
// key index — point lookup, not a scan.
func (s *Store) LatestBaseline(ctx context.Context, pair canonical.Pair) (StoredBaseline, error) {
	const q = `
		SELECT computed_at, window_start, window_end,
		       median, mad, sample_count,
		       median_1d, mad_1d, n_1d,
		       median_7d, mad_7d, n_7d
		  FROM volatility_baseline_1m
		 WHERE base_asset = $1 AND quote_asset = $2
	`
	var (
		sb              StoredBaseline
		median30        float64
		mad30           float64
		n30             int
		d1Median, d1MAD sql.NullFloat64
		d1N             sql.NullInt64
		d7Median, d7MAD sql.NullFloat64
		d7N             sql.NullInt64
	)
	sb.Pair = pair
	err := s.db.QueryRowContext(ctx, q,
		pair.Base.String(), pair.Quote.String(),
	).Scan(
		&sb.ComputedAt,
		&sb.WindowStart,
		&sb.WindowEnd,
		&median30, &mad30, &n30,
		&d1Median, &d1MAD, &d1N,
		&d7Median, &d7MAD, &d7N,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return StoredBaseline{}, ErrBaselineNotFound
		}
		return StoredBaseline{}, fmt.Errorf("timescale: LatestBaseline %s: %w", pair.String(), err)
	}
	sb.Multi = baseline.MultiBaseline{
		Day30: &baseline.Baseline{Median: median30, MAD: mad30, N: n30},
	}
	if d1N.Valid {
		sb.Multi.Day1 = &baseline.Baseline{
			Median: d1Median.Float64, MAD: d1MAD.Float64, N: int(d1N.Int64),
		}
	}
	if d7N.Valid {
		sb.Multi.Day7 = &baseline.Baseline{
			Median: d7Median.Float64, MAD: d7MAD.Float64, N: int(d7N.Int64),
		}
	}
	return sb, nil
}

// CountBaselines returns the row count of volatility_baseline_1m.
// Diagnostic helper for the aggregator's "how many pairs have a
// baseline yet" metrics; not for production hot paths.
func (s *Store) CountBaselines(ctx context.Context) (int64, error) {
	const q = `SELECT count(*) FROM volatility_baseline_1m`
	var n int64
	if err := s.db.QueryRowContext(ctx, q).Scan(&n); err != nil {
		return 0, fmt.Errorf("timescale: CountBaselines: %w", err)
	}
	return n, nil
}
