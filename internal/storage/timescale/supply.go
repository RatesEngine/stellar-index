package timescale

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/StellarIndex/stellar-index/internal/supply"
)

// InsertSupply appends a [supply.Supply] snapshot to
// asset_supply_history. Idempotent on
// (asset_key, ledger_sequence, time) — re-deriving at the same
// ledger is a no-op via ON CONFLICT DO NOTHING. The aggregator
// writes one snapshot per asset-affecting bucket close.
//
// The third column (`time`) is required by TimescaleDB's unique-
// index constraint that the partition column be part of any
// uniqueness invariant on a hypertable — see
// migrations/0005_create_asset_supply_history.up.sql:55-61. In
// practice two writes for the same (asset, ledger) carry the same
// `time` derived from the ledger close timestamp, so the
// (asset_key, ledger_sequence) uniqueness invariant the migration's
// comment describes is preserved at the application level.
//
// Validates that AssetKey + TotalSupply + CirculatingSupply are
// populated (the supply-package computers always populate them; this
// is a defensive guard against an upstream bug calling InsertSupply
// with a zero-value struct). Per-field non-negativity is enforced by
// the migration's CHECK constraints — a violation here surfaces as a
// pgx error rather than a quiet write of bad data.
func (s *Store) InsertSupply(ctx context.Context, snap supply.Supply) error {
	if snap.AssetKey == "" {
		return errors.New("timescale: InsertSupply: AssetKey is empty")
	}
	if snap.TotalSupply == nil {
		return fmt.Errorf("timescale: InsertSupply %s: TotalSupply is nil", snap.AssetKey)
	}
	if snap.CirculatingSupply == nil {
		return fmt.Errorf("timescale: InsertSupply %s: CirculatingSupply is nil", snap.AssetKey)
	}

	var maxSupply sql.NullString
	if snap.MaxSupply != nil {
		maxSupply = sql.NullString{Valid: true, String: snap.MaxSupply.String()}
	}

	// F-1205 follow-up (codex audit-2026-05-12): use the named-
	// constraint form. Timescale hypertables in PG 16 + TS 2.16
	// don't expose unique constraints to ON CONFLICT's column-
	// inference path, so the cleaner `ON CONFLICT (cols)` syntax
	// fails with `there is no unique or exclusion constraint
	// matching the ON CONFLICT specification`. The named-target
	// form bypasses inference. Constraint added by migration 0030.
	const q = `
		INSERT INTO asset_supply_history
		    (time, asset_key, total_supply, circulating_supply, max_supply, basis, ledger_sequence)
		VALUES
		    ($1, $2, $3::numeric, $4::numeric, $5::numeric, $6, $7)
		ON CONFLICT ON CONSTRAINT asset_supply_history_asset_ledger_idx DO NOTHING
	`
	_, err := s.db.ExecContext(ctx, q,
		snap.ObservedAt.UTC(),
		snap.AssetKey,
		snap.TotalSupply.String(),
		snap.CirculatingSupply.String(),
		maxSupply,
		string(snap.Basis),
		int64(snap.LedgerSequence),
	)
	if err != nil {
		return fmt.Errorf("timescale: InsertSupply %s @ ledger %d: %w",
			snap.AssetKey, snap.LedgerSequence, err)
	}
	return nil
}

// LatestSupply returns the most-recent snapshot for assetKey. Used
// by the API's /v1/assets/{id} F2-fields path. Returns
// [ErrNotFound] when the asset has no recorded supply (the asset-
// detail handler then publishes nil for every supply field).
func (s *Store) LatestSupply(ctx context.Context, assetKey string) (supply.Supply, error) {
	const q = `
		SELECT time, total_supply::text, circulating_supply::text, max_supply::text, basis, ledger_sequence
		  FROM asset_supply_history
		 WHERE asset_key = $1
		 ORDER BY time DESC
		 LIMIT 1
	`
	var (
		observedAt     time.Time
		totalStr       string
		circulatingStr string
		maxStr         sql.NullString
		basis          string
		ledger         int64
	)
	err := s.db.QueryRowContext(ctx, q, assetKey).Scan(
		&observedAt, &totalStr, &circulatingStr, &maxStr, &basis, &ledger,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return supply.Supply{}, ErrNotFound
	}
	if err != nil {
		return supply.Supply{}, fmt.Errorf("timescale: LatestSupply %s: %w", assetKey, err)
	}
	return assembleSupply(assetKey, observedAt, totalStr, circulatingStr, maxStr, basis, ledger)
}

// SupplyHistory returns snapshots for assetKey between [from, to)
// in ascending time order. limit caps the result count; pass 0 for
// no limit. Used by the asset-detail historical-supply chart.
//
// Empty slice + nil error when no rows match the window — the asset
// is known but has no supply observations in the requested range.
func (s *Store) SupplyHistory(ctx context.Context, assetKey string, from, to time.Time, limit int) ([]supply.Supply, error) {
	q := `
		SELECT time, total_supply::text, circulating_supply::text, max_supply::text, basis, ledger_sequence
		  FROM asset_supply_history
		 WHERE asset_key = $1
		   AND time >= $2
		   AND time < $3
		 ORDER BY time ASC
	`
	args := []any{assetKey, from.UTC(), to.UTC()}
	if limit > 0 {
		q += " LIMIT $4"
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("timescale: SupplyHistory %s: %w", assetKey, err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]supply.Supply, 0, 128)
	for rows.Next() {
		var (
			observedAt     time.Time
			totalStr       string
			circulatingStr string
			maxStr         sql.NullString
			basis          string
			ledger         int64
		)
		if err := rows.Scan(&observedAt, &totalStr, &circulatingStr, &maxStr, &basis, &ledger); err != nil {
			return nil, fmt.Errorf("timescale: SupplyHistory %s scan: %w", assetKey, err)
		}
		snap, err := assembleSupply(assetKey, observedAt, totalStr, circulatingStr, maxStr, basis, ledger)
		if err != nil {
			return nil, err
		}
		out = append(out, snap)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("timescale: SupplyHistory %s rows: %w", assetKey, err)
	}
	return out, nil
}

// SupplyDayPoint is one day's last-known circulating supply from the
// supply_1d continuous aggregate (migration 0066). Used as the supply
// leg of crypto market-cap-over-time.
type SupplyDayPoint struct {
	Bucket      time.Time
	Circulating *big.Int
}

// DailyCirculatingSupply returns daily last-known circulating supply
// for assetKey from the supply_1d CAGG, ascending by day, within
// [from, to] — plus the single most-recent point STRICTLY before
// `from` (the carry-in row) so the caller can forward-fill the
// leading edge of a windowed market-cap series. Empty slice + nil
// error when the asset has no supply snapshots at all.
//
// A zero `from` means "from inception"; the carry-in clause then
// matches nothing (no bucket precedes the epoch) and the window
// clause returns the full series, which is the intended behaviour.
func (s *Store) DailyCirculatingSupply(ctx context.Context, assetKey string, from, to time.Time) ([]SupplyDayPoint, error) {
	const q = `
		(SELECT bucket, circulating_supply::text
		   FROM supply_1d
		  WHERE asset_key = $1 AND bucket < $2
		  ORDER BY bucket DESC
		  LIMIT 1)
		UNION ALL
		(SELECT bucket, circulating_supply::text
		   FROM supply_1d
		  WHERE asset_key = $1 AND bucket >= $2 AND bucket <= $3
		  ORDER BY bucket ASC)
		ORDER BY bucket ASC`
	rows, err := s.db.QueryContext(ctx, q, assetKey, from.UTC(), to.UTC())
	if err != nil {
		return nil, fmt.Errorf("timescale: DailyCirculatingSupply %s: %w", assetKey, err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]SupplyDayPoint, 0, 256)
	for rows.Next() {
		var (
			bucket  time.Time
			circStr string
		)
		if err := rows.Scan(&bucket, &circStr); err != nil {
			return nil, fmt.Errorf("timescale: DailyCirculatingSupply %s scan: %w", assetKey, err)
		}
		circ, ok := new(big.Int).SetString(circStr, 10)
		if !ok {
			return nil, fmt.Errorf("timescale: DailyCirculatingSupply %s parse %q", assetKey, circStr)
		}
		out = append(out, SupplyDayPoint{Bucket: bucket.UTC(), Circulating: circ})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("timescale: DailyCirculatingSupply %s rows: %w", assetKey, err)
	}
	return out, nil
}

// assembleSupply parses the text-cast NUMERIC columns into *big.Int
// and assembles a supply.Supply. Centralised so InsertSupply's
// round-trip and SupplyHistory share identical decode logic — a bug
// in one is a bug in both, easier to fix once.
func assembleSupply(assetKey string, observedAt time.Time, totalStr, circulatingStr string, maxStr sql.NullString, basis string, ledger int64) (supply.Supply, error) {
	total, ok := new(big.Int).SetString(totalStr, 10)
	if !ok {
		return supply.Supply{}, fmt.Errorf("timescale: parse total_supply %q for %s", totalStr, assetKey)
	}
	circulating, ok := new(big.Int).SetString(circulatingStr, 10)
	if !ok {
		return supply.Supply{}, fmt.Errorf("timescale: parse circulating_supply %q for %s", circulatingStr, assetKey)
	}
	var maxSupply *big.Int
	if maxStr.Valid {
		maxSupply, ok = new(big.Int).SetString(maxStr.String, 10)
		if !ok {
			return supply.Supply{}, fmt.Errorf("timescale: parse max_supply %q for %s", maxStr.String, assetKey)
		}
	}
	return supply.Supply{
		AssetKey:          assetKey,
		TotalSupply:       total,
		CirculatingSupply: circulating,
		MaxSupply:         maxSupply,
		Basis:             supply.Basis(basis),
		LedgerSequence:    uint32(ledger), //nolint:gosec // ledger is a positive uint32 by domain; CHECK constraint enforces >= 1
		ObservedAt:        observedAt,
	}, nil
}
