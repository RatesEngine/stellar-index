package timescale

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/RatesEngine/rates-engine/internal/aggregate/baseline"
	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// StoredBaseline is the wire shape between the storage layer and
// the aggregator's refresh worker. Wraps [baseline.Baseline] with
// the metadata the persistence layer needs to track refresh
// freshness.
//
// The aggregator computes a [baseline.Baseline] in memory using the
// math primitives in `internal/aggregate/baseline`, packages it
// here with timing metadata, and calls [Store.UpsertBaseline] to
// persist. The API hot path reads via [Store.LatestBaseline] (or
// the bulk variant when the aggregator's confidence-score loop
// needs every active pair at once).
type StoredBaseline struct {
	Pair        canonical.Pair
	ComputedAt  time.Time
	WindowStart time.Time
	WindowEnd   time.Time
	Baseline    baseline.Baseline
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
// Validates that the wrapped Baseline.N >= [baseline.MinSamples] —
// a bootstrap-window baseline (n < 2) is meaningless to persist
// and the migration's CHECK constraint would reject it anyway;
// catching it here gives a clearer error.
func (s *Store) UpsertBaseline(ctx context.Context, sb StoredBaseline) error {
	if sb.Baseline.N < baseline.MinSamples {
		return fmt.Errorf("timescale: UpsertBaseline %s: sample_count=%d < %d",
			sb.Pair.String(), sb.Baseline.N, baseline.MinSamples)
	}
	if !sb.WindowEnd.After(sb.WindowStart) {
		return fmt.Errorf("timescale: UpsertBaseline %s: window_end %v <= window_start %v",
			sb.Pair.String(), sb.WindowEnd, sb.WindowStart)
	}

	const q = `
		INSERT INTO volatility_baseline_1m
		    (base_asset, quote_asset, computed_at, window_start, window_end, median, mad, sample_count)
		VALUES
		    ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (base_asset, quote_asset) DO UPDATE SET
		    computed_at  = EXCLUDED.computed_at,
		    window_start = EXCLUDED.window_start,
		    window_end   = EXCLUDED.window_end,
		    median       = EXCLUDED.median,
		    mad          = EXCLUDED.mad,
		    sample_count = EXCLUDED.sample_count
	`
	_, err := s.db.ExecContext(ctx, q,
		sb.Pair.Base.String(),
		sb.Pair.Quote.String(),
		sb.ComputedAt.UTC(),
		sb.WindowStart.UTC(),
		sb.WindowEnd.UTC(),
		sb.Baseline.Median,
		sb.Baseline.MAD,
		sb.Baseline.N,
	)
	if err != nil {
		return fmt.Errorf("timescale: UpsertBaseline %s: %w", sb.Pair.String(), err)
	}
	return nil
}

// LatestBaseline returns the current baseline for `pair`. Returns
// [ErrBaselineNotFound] when no row exists for the pair (the
// caller should fall through to the bootstrap policy).
//
// API hot path; covered by the (base_asset, quote_asset) primary-
// key index — point lookup, not a scan.
func (s *Store) LatestBaseline(ctx context.Context, pair canonical.Pair) (StoredBaseline, error) {
	const q = `
		SELECT computed_at, window_start, window_end, median, mad, sample_count
		  FROM volatility_baseline_1m
		 WHERE base_asset = $1 AND quote_asset = $2
	`
	var sb StoredBaseline
	sb.Pair = pair
	err := s.db.QueryRowContext(ctx, q,
		pair.Base.String(), pair.Quote.String(),
	).Scan(
		&sb.ComputedAt,
		&sb.WindowStart,
		&sb.WindowEnd,
		&sb.Baseline.Median,
		&sb.Baseline.MAD,
		&sb.Baseline.N,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return StoredBaseline{}, ErrBaselineNotFound
		}
		return StoredBaseline{}, fmt.Errorf("timescale: LatestBaseline %s: %w", pair.String(), err)
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
