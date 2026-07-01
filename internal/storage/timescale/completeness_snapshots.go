package timescale

import (
	"context"
	"fmt"
	"time"
)

// CompletenessSnapshot is one source's completeness verdict
// (migration 0052, ADR-0033 Phase 6).
type CompletenessSnapshot struct {
	Source        string
	Genesis       uint32
	Tip           uint32
	Watermark     uint32
	CoveragePct   float64
	Complete      bool
	FirstProblem  uint32 // 0 = none
	SubstrateOK   bool
	RecognitionOK bool
	ProjectionOK  bool
	Detail        string
	ComputedAt    time.Time
}

// UpsertCompletenessSnapshot writes (or refreshes) a source's verdict.
func (s *Store) UpsertCompletenessSnapshot(ctx context.Context, snap CompletenessSnapshot) error {
	const q = `
        INSERT INTO completeness_snapshots (
            source, genesis_ledger, tip_ledger, watermark_ledger,
            coverage_pct, complete, first_problem_ledger,
            substrate_ok, recognition_ok, projection_ok, detail, computed_at
        ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11, now())
        ON CONFLICT (source) DO UPDATE SET
            genesis_ledger       = EXCLUDED.genesis_ledger,
            tip_ledger           = EXCLUDED.tip_ledger,
            watermark_ledger     = EXCLUDED.watermark_ledger,
            coverage_pct         = EXCLUDED.coverage_pct,
            complete             = EXCLUDED.complete,
            first_problem_ledger = EXCLUDED.first_problem_ledger,
            substrate_ok         = EXCLUDED.substrate_ok,
            recognition_ok       = EXCLUDED.recognition_ok,
            projection_ok        = EXCLUDED.projection_ok,
            detail               = EXCLUDED.detail,
            computed_at          = now()
        -- CS-083: never let a regressive-window run (a smaller -to, or a
        -- mid-walk stall) overwrite a more-advanced verdict — that's how a
        -- source read complete=true pinned at a STALE tip. Apply the update
        -- only when this run advanced (or held) the tip, OR it found a
        -- problem (a newly-discovered problem must always be recorded, even
        -- if it lowers the watermark). The tip is monotonic (network head
        -- only grows), so a smaller tip means a stale/partial run.
        WHERE EXCLUDED.tip_ledger >= completeness_snapshots.tip_ledger
           OR EXCLUDED.first_problem_ledger > 0`
	if _, err := s.db.ExecContext(ctx, q,
		snap.Source, int64(snap.Genesis), int64(snap.Tip), int64(snap.Watermark),
		snap.CoveragePct, snap.Complete, int64(snap.FirstProblem),
		snap.SubstrateOK, snap.RecognitionOK, snap.ProjectionOK, snap.Detail,
	); err != nil {
		return fmt.Errorf("timescale: UpsertCompletenessSnapshot (%s): %w", snap.Source, err)
	}
	return nil
}

// ListCompletenessSnapshots returns every source's verdict, source-sorted.
func (s *Store) ListCompletenessSnapshots(ctx context.Context) ([]CompletenessSnapshot, error) {
	const q = `
        SELECT source, genesis_ledger, tip_ledger, watermark_ledger,
               coverage_pct, complete, first_problem_ledger,
               substrate_ok, recognition_ok, projection_ok, detail, computed_at
        FROM completeness_snapshots
        ORDER BY source`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("timescale: ListCompletenessSnapshots: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []CompletenessSnapshot
	for rows.Next() {
		var (
			snap                        CompletenessSnapshot
			genesis, tip, wm, firstProb int64
		)
		if err := rows.Scan(
			&snap.Source, &genesis, &tip, &wm,
			&snap.CoveragePct, &snap.Complete, &firstProb,
			&snap.SubstrateOK, &snap.RecognitionOK, &snap.ProjectionOK, &snap.Detail, &snap.ComputedAt,
		); err != nil {
			return nil, fmt.Errorf("timescale: ListCompletenessSnapshots scan: %w", err)
		}
		snap.Genesis = uint32(genesis)
		snap.Tip = uint32(tip)
		snap.Watermark = uint32(wm)
		snap.FirstProblem = uint32(firstProb)
		out = append(out, snap)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("timescale: ListCompletenessSnapshots rows: %w", err)
	}
	return out, nil
}
