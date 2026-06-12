package timescale

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
)

// ChangeSummaryRow mirrors the change_summary_5m table. Pointer
// fields are nullable — a young entity with <1h of history has
// every window pointer left nil; the SQL writes those as NULL.
type ChangeSummaryRow struct {
	EntityType   string
	EntityID     string
	RefreshedAt  time.Time
	CurrentValue float64

	H1Value     *float64
	H1DeltaPct  *float64
	H24Value    *float64
	H24DeltaPct *float64
	D7Value     *float64
	D7DeltaPct  *float64
	D30Value    *float64
	D30DeltaPct *float64

	ATHValue *float64
	ATHAt    *time.Time
	ATLValue *float64
	ATLAt    *time.Time

	StreakDirection string
	StreakDays      *int
	Acceleration    string
}

// UpsertChangeSummary inserts or replaces the row keyed on
// (entity_type, entity_id). Refreshed every 5 min by the worker —
// stale rows (>10 min) are surfaced via the diagnostics page so an
// operator can spot a stalled worker.
func (s *Store) UpsertChangeSummary(ctx context.Context, row ChangeSummaryRow) error {
	if row.EntityType == "" {
		return errors.New("timescale: UpsertChangeSummary: empty entity_type")
	}
	if row.EntityID == "" {
		return errors.New("timescale: UpsertChangeSummary: empty entity_id")
	}
	const q = `
		INSERT INTO change_summary_5m (
		    entity_type, entity_id, refreshed_at, current_value,
		    h1_value, h1_delta_pct,
		    h24_value, h24_delta_pct,
		    d7_value, d7_delta_pct,
		    d30_value, d30_delta_pct,
		    ath_value, ath_at,
		    atl_value, atl_at,
		    streak_direction, streak_days,
		    acceleration
		) VALUES (
		    $1, $2, $3, $4,
		    $5, $6, $7, $8, $9, $10, $11, $12,
		    $13, $14, $15, $16,
		    $17, $18, $19
		)
		ON CONFLICT (entity_type, entity_id) DO UPDATE SET
		    refreshed_at      = EXCLUDED.refreshed_at,
		    current_value     = EXCLUDED.current_value,
		    h1_value          = EXCLUDED.h1_value,
		    h1_delta_pct      = EXCLUDED.h1_delta_pct,
		    h24_value         = EXCLUDED.h24_value,
		    h24_delta_pct     = EXCLUDED.h24_delta_pct,
		    d7_value          = EXCLUDED.d7_value,
		    d7_delta_pct      = EXCLUDED.d7_delta_pct,
		    d30_value         = EXCLUDED.d30_value,
		    d30_delta_pct     = EXCLUDED.d30_delta_pct,
		    ath_value         = GREATEST(change_summary_5m.ath_value, EXCLUDED.ath_value),
		    ath_at            = CASE WHEN EXCLUDED.ath_value > change_summary_5m.ath_value
		                             THEN EXCLUDED.ath_at
		                             ELSE change_summary_5m.ath_at END,
		    atl_value         = LEAST(change_summary_5m.atl_value, EXCLUDED.atl_value),
		    atl_at            = CASE WHEN EXCLUDED.atl_value < change_summary_5m.atl_value
		                             THEN EXCLUDED.atl_at
		                             ELSE change_summary_5m.atl_at END,
		    streak_direction  = EXCLUDED.streak_direction,
		    streak_days       = EXCLUDED.streak_days,
		    acceleration      = EXCLUDED.acceleration
	`
	_, err := s.db.ExecContext(ctx, q,
		row.EntityType, row.EntityID, row.RefreshedAt.UTC(), row.CurrentValue,
		floatOrNil(row.H1Value), floatOrNil(row.H1DeltaPct),
		floatOrNil(row.H24Value), floatOrNil(row.H24DeltaPct),
		floatOrNil(row.D7Value), floatOrNil(row.D7DeltaPct),
		floatOrNil(row.D30Value), floatOrNil(row.D30DeltaPct),
		floatOrNil(row.ATHValue), timeOrNil(row.ATHAt),
		floatOrNil(row.ATLValue), timeOrNil(row.ATLAt),
		strOrNil(row.StreakDirection), intOrNil(row.StreakDays),
		strOrNil(row.Acceleration),
	)
	if err != nil {
		return fmt.Errorf("timescale: UpsertChangeSummary %s/%s: %w",
			row.EntityType, row.EntityID, err)
	}
	return nil
}

// GetChangeSummary returns the current row for (entity_type,
// entity_id), or sql.ErrNoRows when the worker hasn't computed it
// yet. API handlers translate that into the price-not-found path.
func (s *Store) GetChangeSummary(ctx context.Context, entityType, entityID string) (ChangeSummaryRow, error) {
	const q = `
		SELECT entity_type, entity_id, refreshed_at, current_value,
		       h1_value, h1_delta_pct, h24_value, h24_delta_pct,
		       d7_value, d7_delta_pct, d30_value, d30_delta_pct,
		       ath_value, ath_at, atl_value, atl_at,
		       streak_direction, streak_days, acceleration
		  FROM change_summary_5m
		 WHERE entity_type = $1 AND entity_id = $2
	`
	var row ChangeSummaryRow
	var (
		h1V, h1D, h24V, h24D, d7V, d7D, d30V, d30D sql.NullFloat64
		athV, atlV                                 sql.NullFloat64
		athAt, atlAt                               sql.NullTime
		streakDir, accel                           sql.NullString
		streakDays                                 sql.NullInt64
	)
	err := s.db.QueryRowContext(ctx, q, entityType, entityID).Scan(
		&row.EntityType, &row.EntityID, &row.RefreshedAt, &row.CurrentValue,
		&h1V, &h1D, &h24V, &h24D, &d7V, &d7D, &d30V, &d30D,
		&athV, &athAt, &atlV, &atlAt,
		&streakDir, &streakDays, &accel,
	)
	if err != nil {
		return ChangeSummaryRow{}, err
	}
	row.H1Value = nullFloat(h1V)
	row.H1DeltaPct = nullFloat(h1D)
	row.H24Value = nullFloat(h24V)
	row.H24DeltaPct = nullFloat(h24D)
	row.D7Value = nullFloat(d7V)
	row.D7DeltaPct = nullFloat(d7D)
	row.D30Value = nullFloat(d30V)
	row.D30DeltaPct = nullFloat(d30D)
	row.ATHValue = nullFloat(athV)
	row.ATLValue = nullFloat(atlV)
	if athAt.Valid {
		t := athAt.Time
		row.ATHAt = &t
	}
	if atlAt.Valid {
		t := atlAt.Time
		row.ATLAt = &t
	}
	if streakDir.Valid {
		row.StreakDirection = streakDir.String
	}
	if streakDays.Valid {
		v := int(streakDays.Int64)
		row.StreakDays = &v
	}
	if accel.Valid {
		row.Acceleration = accel.String
	}
	return row, nil
}

func floatOrNil(f *float64) any {
	if f == nil {
		return nil
	}
	return *f
}

func timeOrNil(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC()
}

func intOrNil(i *int) any {
	if i == nil {
		return nil
	}
	return *i
}

func strOrNil(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullFloat(n sql.NullFloat64) *float64 {
	if !n.Valid {
		return nil
	}
	return &n.Float64
}

// TimedVWAPs1m is a thin adapter so [changesummary.PriceSource] is
// satisfied by the existing TimedVWAPsForPair1m without dragging the
// baseline package's TimedVWAP type into the changesummary public
// surface. Returns oldest-first.
func (s *Store) TimedVWAPs1mForChangeSummary(ctx context.Context, pair canonical.Pair, from, to time.Time) ([]ChangeSummaryPoint, error) {
	const q = `
		SELECT bucket + INTERVAL '1 minute', vwap::text
		  FROM prices_1m
		 WHERE base_asset = $1
		   AND quote_asset = $2
		   AND bucket >= $3
		   AND bucket <  $4
		 ORDER BY bucket ASC
	`
	rows, err := s.db.QueryContext(ctx, q,
		pair.Base.String(), pair.Quote.String(), from.UTC(), to.UTC())
	if err != nil {
		return nil, fmt.Errorf("timescale: TimedVWAPs1mForChangeSummary: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]ChangeSummaryPoint, 0, 256)
	for rows.Next() {
		var p ChangeSummaryPoint
		if err := rows.Scan(&p.At, &p.Value); err != nil {
			return nil, fmt.Errorf("timescale: TimedVWAPs1mForChangeSummary scan: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ChangeSummaryPoint is the read-projection used by the changesummary
// worker. Decoupled from baseline.TimedVWAP so the worker package
// doesn't need to import baseline.
type ChangeSummaryPoint struct {
	At    time.Time
	Value string
}
