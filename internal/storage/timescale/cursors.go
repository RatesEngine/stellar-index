package timescale

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Cursor is a per-source ingestion marker. Sub is an optional
// differentiator for sources that track multiple positions
// independently (e.g. Soroswap tracks factory events + per-pair
// events separately; Soroswap's consumer.go sets Sub to the pair's
// contract ID for pair cursors, "" for the factory cursor).
//
// FirstLedger is the earliest ledger this cursor's range covers.
// For backfill cursors it is the `from` end of the assigned range
// (also embedded in Sub as "<from>-<to>:<decoders>"). For the live
// ledgerstream cursor it is the first ledger the live indexer
// ingested in this region — populated on the first INSERT via
// UpsertCursor; preserved by the ON CONFLICT DO UPDATE so the live
// cursor's [FirstLedger, LastLedger] coverage span survives every
// subsequent advance. Zero when the column is NULL on disk
// (rows that pre-date migration 0046); the density-coverage
// projection falls back to sourceGenesisLedger for NULL live
// cursors. See migration 0046.
type Cursor struct {
	Source      string
	Sub         string
	FirstLedger uint32
	LastLedger  uint32
	UpdatedAt   time.Time
}

// GetCursor returns the stored cursor or ErrNotFound. Callers on
// first run typically translate ErrNotFound to "start from
// configured backfill-from-ledger" rather than an error condition.
//
// first_ledger is read via COALESCE(..., 0) so a NULL column on a
// pre-migration-0046 row scans cleanly as FirstLedger=0. Callers
// distinguishing "no first_ledger persisted" from "covers ledger 0"
// MUST use ListCursors + sourceGenesisLedger fallback semantics
// (the density-projection path); GetCursor's zero is unambiguous
// for non-zero-genesis sources.
func (s *Store) GetCursor(ctx context.Context, source, sub string) (Cursor, error) {
	const q = `
        SELECT source, COALESCE(sub_source, ''),
               COALESCE(first_ledger, 0), last_ledger, last_updated
          FROM ingestion_cursors
         WHERE source = $1 AND sub_source = $2
    `
	var c Cursor
	err := s.db.QueryRowContext(ctx, q, source, sub).Scan(
		&c.Source, &c.Sub, &c.FirstLedger, &c.LastLedger, &c.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Cursor{}, ErrNotFound
	}
	if err != nil {
		return Cursor{}, fmt.Errorf("timescale: GetCursor: %w", err)
	}
	return c, nil
}

// ListCursors returns every row in ingestion_cursors ordered by
// (source, sub_source). Used by diagnostic tooling — not a hot path.
func (s *Store) ListCursors(ctx context.Context) ([]Cursor, error) {
	const q = `
        SELECT source, COALESCE(sub_source, ''),
               COALESCE(first_ledger, 0), last_ledger, last_updated
          FROM ingestion_cursors
         ORDER BY source ASC, sub_source ASC
    `
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("timescale: ListCursors: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Cursor
	for rows.Next() {
		var c Cursor
		if err := rows.Scan(&c.Source, &c.Sub, &c.FirstLedger, &c.LastLedger, &c.UpdatedAt); err != nil {
			return nil, fmt.Errorf("timescale: ListCursors scan: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("timescale: ListCursors rows: %w", err)
	}
	return out, nil
}

// UpsertCursor stores the cursor, advancing any existing row for
// (source, sub). The last_updated column is server-side `now()`.
//
// Monotonic-advance guard: the `WHERE` clause on DO UPDATE refuses
// to regress last_ledger. A lower-or-equal value is a silent no-op
// at the DB layer — protects against a caller that forgot its own
// guard (the orchestrator's cursorPersister has one too; this is
// defense-in-depth) and against two indexers briefly racing during
// a misconfigured deploy. Inserts of brand-new (source, sub) rows
// still succeed regardless; the WHERE only gates the UPDATE path.
//
// first_ledger semantics (migration 0046, 100% density mission):
//
//   - INSERT path: first_ledger = lastLedger. The first time this
//     (source, sub) is seen we capture the starting ledger as the
//     cursor's lower-bound coverage anchor. For the live cursor
//     (source='ledgerstream', sub-source empty) that's the first ledger this
//     region's live indexer ingested — the diagnostic density calc
//     credits the [first_ledger, last_ledger] band as covered.
//
//   - UPDATE path: first_ledger is INTENTIONALLY NOT TOUCHED. The
//     SET clause lists only last_ledger + last_updated, so the
//     row's original first_ledger is preserved across every cursor
//     advance. Live indexer restarts/resumes do not stomp it; the
//     coverage anchor only ever moves backwards by an explicit
//     operator action (DELETE + re-insert, not via this path).
func (s *Store) UpsertCursor(ctx context.Context, source, sub string, lastLedger uint32) error {
	const q = `
        INSERT INTO ingestion_cursors (source, sub_source, first_ledger, last_ledger, last_updated)
        VALUES ($1, $2, $3, $3, now())
        ON CONFLICT (source, sub_source)
        DO UPDATE SET last_ledger  = EXCLUDED.last_ledger,
                      last_updated = EXCLUDED.last_updated
         WHERE EXCLUDED.last_ledger > ingestion_cursors.last_ledger
    `
	_, err := s.db.ExecContext(ctx, q, source, sub, lastLedger)
	if err != nil {
		return fmt.Errorf("timescale: UpsertCursor: %w", err)
	}
	return nil
}
