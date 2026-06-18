package timescale

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/StellarIndex/stellar-index/internal/divergence"
)

// LedgerProvider is defined by freeze_events.go (#560 landed first).
// Reusing it here keeps the seam consistent across sinks.

// DivergenceSink is the timescale-backed implementation of
// divergence.ObservationSink. Persists every per-reference
// comparison the worker computes to the `divergence_observations`
// hypertable.
//
// Today the worker writes the aggregate (median + boolean firing
// flag) to Redis with a TTL; the per-reference deltas are lost
// after the next tick. This sink keeps a queryable history so
// the explorer /divergences page can plot deltas over time and
// post-mortems can verify "Reflector drifted N% from us at
// ledger X" against ground truth.
type DivergenceSink struct {
	db        *sql.DB
	getLedger LedgerProvider
}

// NewDivergenceSink constructs the sink. Pass an optional ledger
// provider so observations carry observed_at_ledger; nil falls
// back to ledger 0 (acceptable for tests).
func NewDivergenceSink(s *Store, opts ...DivergenceSinkOption) *DivergenceSink {
	sink := &DivergenceSink{db: s.db}
	for _, opt := range opts {
		opt(sink)
	}
	return sink
}

// DivergenceSinkOption tunes a DivergenceSink at construction.
type DivergenceSinkOption func(*DivergenceSink)

// WithDivergenceLedgerProvider wires the ledger seam so inserts
// capture observed_at_ledger.
func WithDivergenceLedgerProvider(p LedgerProvider) DivergenceSinkOption {
	return func(s *DivergenceSink) {
		s.getLedger = p
	}
}

// RecordObservation implements divergence.ObservationSink.
//
// Inserts one row per call; the table's PK (asset_id, quote_id,
// reference, observed_at) makes concurrent inserts at the identical
// microsecond a no-op via ON CONFLICT — but since we control the
// observed_at upstream (the worker sets it) collisions are rare in
// practice.
func (s *DivergenceSink) RecordObservation(ctx context.Context, obs divergence.ObservationRecord) error {
	var ledger uint32
	if s.getLedger != nil {
		ledger = s.getLedger.LatestLedger()
	}

	status := "clear"
	if obs.Firing {
		status = "firing"
	}

	const q = `
		INSERT INTO divergence_observations (
		    asset_id, quote_id, reference,
		    observed_at, observed_at_ledger,
		    our_price, ref_price, delta_pct,
		    status
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (asset_id, quote_id, reference, observed_at) DO NOTHING
	`
	if _, err := s.db.ExecContext(ctx, q,
		obs.Pair.Base.String(), obs.Pair.Quote.String(), obs.Reference,
		obs.ObservedAt.UTC(), int64(ledger),
		obs.OurPrice, obs.RefPrice, obs.DeltaPct,
		status,
	); err != nil {
		return fmt.Errorf("timescale: RecordObservation %s/%s/%s: %w",
			obs.Pair.Base.String(), obs.Pair.Quote.String(), obs.Reference, err)
	}
	return nil
}

// DivergenceRow is one divergence_observations row for the /v1/divergence
// read path. Prices + delta are decimal strings (ADR-0003).
type DivergenceRow struct {
	AssetID          string
	QuoteID          string
	Reference        string
	ObservedAt       time.Time
	ObservedAtLedger int64
	OurPrice         string
	RefPrice         string
	DeltaPct         string
	Status           string
}

// ListDivergenceLatest returns the LATEST observation per (asset,
// quote, reference) within the trailing `sinceDays` window — the
// current cross-reference divergence board. firingOnly restricts to
// rows whose latest status is 'firing'. Ordered by |delta_pct| desc so
// the widest gaps surface first. limit ≤ 500.
//
// DISTINCT ON (asset, quote, reference) with the matching ORDER prefix
// uses the (asset, quote, reference, observed_at DESC) index to pick
// each triple's newest row without a separate sort.
func (s *Store) ListDivergenceLatest(ctx context.Context, sinceDays int, firingOnly bool, limit int) ([]DivergenceRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if sinceDays <= 0 {
		sinceDays = 7
	}
	q := `
		WITH latest AS (
			SELECT DISTINCT ON (asset_id, quote_id, reference)
			       asset_id, quote_id, reference, observed_at, observed_at_ledger,
			       our_price::text, ref_price::text, delta_pct::text, status
			  FROM divergence_observations
			 WHERE observed_at > now() - ($1 || ' days')::interval
			 ORDER BY asset_id, quote_id, reference, observed_at DESC
		)
		SELECT asset_id, quote_id, reference, observed_at, observed_at_ledger,
		       our_price, ref_price, delta_pct, status
		  FROM latest`
	if firingOnly {
		q += ` WHERE status = 'firing'`
	}
	q += ` ORDER BY abs(delta_pct::numeric) DESC LIMIT $2`
	rows, err := s.db.QueryContext(ctx, q, sinceDays, limit)
	if err != nil {
		return nil, fmt.Errorf("timescale: ListDivergenceLatest: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []DivergenceRow
	for rows.Next() {
		var r DivergenceRow
		if err := rows.Scan(&r.AssetID, &r.QuoteID, &r.Reference, &r.ObservedAt,
			&r.ObservedAtLedger, &r.OurPrice, &r.RefPrice, &r.DeltaPct, &r.Status); err != nil {
			return nil, fmt.Errorf("timescale: ListDivergenceLatest scan: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("timescale: ListDivergenceLatest rows: %w", err)
	}
	return out, nil
}
