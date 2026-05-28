package timescale

import (
	"context"
	"fmt"
)

// GapDetectorTarget identifies one (source, table) pair the
// data-derived gap detector scans for contiguous coverage gaps.
// The `source` label appears in the emitted metric; the `table`
// label disambiguates when a single source spans multiple
// hypertables (e.g. blend → blend_positions + blend_emissions +
// blend_admin). The `ledgerColumn` is normally "ledger" — every
// per-source migration has uniformly named the ledger sequence
// column "ledger" — but the field is explicit to keep the
// invariant load-bearing rather than implicit.
//
// SQL-injection safety: the gap detector's `FindPerSourceLedgerGaps`
// interpolates `table` and `ledgerColumn` into the query directly
// (Postgres doesn't allow `$1` binding for identifiers). The values
// are NEVER sourced from user input — they come from
// [DefaultGapDetectorTargets] which is a compile-time const list.
// ADR-0030 promotes this to a hard invariant: adding a per-source
// hypertable means adding a target here in the same PR, never
// taking the value from a flag or env var.
type GapDetectorTarget struct {
	Source       string
	Table        string
	LedgerColumn string

	// WhereFilter is an optional additional SQL predicate ANDed into
	// the gap-finding query's WHERE clause (without the leading
	// "AND" — e.g. `source = 'sdex'`). Used when one table holds
	// rows for multiple sources and we want to scan the slice that
	// belongs to one source.
	//
	// SAFETY: like Table and LedgerColumn this is interpolated
	// directly; it must come from [DefaultGapDetectorTargets] (a
	// compile-time const) and never from user input. ADR-0030
	// makes this invariant load-bearing.
	WhereFilter string
}

// DefaultGapDetectorTargets is the registered set of per-source
// hypertables the gap detector scans every cycle. Each per-source
// migration that creates a hypertable MUST add itself here in the
// same PR; the [TestGapDetectorTargetsCoverMigrations] guard fails
// CI if a new `*_events|*_liquidity|*_positions|*_emissions|*_admin|
// *_transfers|*_swaps|*_stake_events|*_supply_events` hypertable
// ships without a target.
//
// The order is the per-cycle scan order; lighter tables scan first
// so a slow target (typically soroban_events itself) doesn't delay
// the lighter signals.
//
// `soroban-events` is the historical first target, kept at the end
// because its scan is by far the most expensive (~5min on r1 vs
// <30s for the per-source tables).
var DefaultGapDetectorTargets = []GapDetectorTarget{
	{Source: "sep41-transfers", Table: "sep41_transfers", LedgerColumn: "ledger"},
	{Source: "sep41-supply", Table: "sep41_supply_events", LedgerColumn: "ledger"},
	{Source: "cctp", Table: "cctp_events", LedgerColumn: "ledger"},
	{Source: "rozo", Table: "rozo_events", LedgerColumn: "ledger"},
	{Source: "comet-liquidity", Table: "comet_liquidity", LedgerColumn: "ledger"},
	{Source: "soroswap-skim", Table: "soroswap_skim_events", LedgerColumn: "ledger"},
	{Source: "phoenix-liquidity", Table: "phoenix_liquidity", LedgerColumn: "ledger"},
	{Source: "phoenix-stake", Table: "phoenix_stake_events", LedgerColumn: "ledger"},
	{Source: "blend-auctions", Table: "blend_auctions", LedgerColumn: "ledger"},
	{Source: "blend-positions", Table: "blend_positions", LedgerColumn: "ledger"},
	{Source: "blend-emissions", Table: "blend_emissions", LedgerColumn: "ledger"},
	{Source: "blend-admin", Table: "blend_admin", LedgerColumn: "ledger"},
	{Source: "soroban-events", Table: "soroban_events", LedgerColumn: "ledger"},
	// SDEX is classic-DEX and does NOT flow through soroban_events.
	// Its rows live in the unified `trades` hypertable alongside
	// every other trade-emitting source; the WhereFilter slices
	// only the SDEX subset for the gap scan. Before this target,
	// SDEX had zero data-derived coverage signal — a symmetric F-
	// 0020-class incident in the classic-DEX path would have gone
	// undetected. (See ADR-0030.)
	{Source: "sdex", Table: "trades", LedgerColumn: "ledger", WhereFilter: "source = 'sdex'"},
	// SDEX offer-state events (OfferCreated/OfferUpdated/OfferRemoved)
	// land in their own hypertable — complement to the trade flow.
	// An offer-events writer halt would not show up in the trades
	// gauge above; the dedicated target catches it.
	{Source: "sdex-offers", Table: "sdex_offer_events", LedgerColumn: "ledger"},
}

// FindPerSourceLedgerGaps finds contiguous ledger-coverage gaps
// >= minGapSize in the named hypertable, restricted to
// [from, to]. Same LAG()-over-DISTINCT shape as
// [FindSorobanEventsLedgerGaps] but parameterised so the gap
// detector can iterate over every per-source target with one code
// path.
//
// SAFETY: target.Table and target.LedgerColumn are interpolated
// directly into the SQL (Postgres `$N` binding does not work for
// identifiers). Callers MUST pass a [GapDetectorTarget] from
// [DefaultGapDetectorTargets] (a compile-time const list); never
// from user input. The function does NOT validate the identifier
// shape — the invariant is upstream, in the caller.
func (s *Store) FindPerSourceLedgerGaps(ctx context.Context, target GapDetectorTarget, from, to, minGapSize int64) ([]LedgerGap, error) {
	if from < 0 || to < from {
		return nil, fmt.Errorf("timescale: FindPerSourceLedgerGaps invalid range [%d,%d]", from, to)
	}
	if to == 0 {
		// Defensive: caller passed an unresolved tip. Return empty
		// rather than scanning the whole table.
		return nil, nil
	}

	// Identifier interpolation is safe-by-construction (callers pass
	// a compile-time const). #nosec G201.
	filter := ""
	if target.WhereFilter != "" {
		filter = " AND (" + target.WhereFilter + ")"
	}
	query := fmt.Sprintf(`
		WITH ledgers AS (
		    SELECT DISTINCT %[1]s AS ledger
		    FROM %[2]s
		    WHERE %[1]s BETWEEN $1 AND $2%[3]s
		),
		ordered AS (
		    SELECT ledger, LAG(ledger) OVER (ORDER BY ledger) AS prev_l
		    FROM ledgers
		)
		SELECT prev_l + 1 AS gap_start,
		       ledger - 1 AS gap_end,
		       ledger - prev_l - 1 AS gap_size
		FROM ordered
		WHERE prev_l IS NOT NULL
		  AND ledger - prev_l - 1 >= $3
		ORDER BY gap_size DESC
	`, target.LedgerColumn, target.Table, filter)

	rows, err := s.db.QueryContext(ctx, query, from, to, minGapSize)
	if err != nil {
		return nil, fmt.Errorf("timescale: FindPerSourceLedgerGaps %s [%d,%d, min %d]: %w",
			target.Table, from, to, minGapSize, err)
	}
	defer func() { _ = rows.Close() }()

	var out []LedgerGap
	for rows.Next() {
		var g LedgerGap
		if err := rows.Scan(&g.Start, &g.End, &g.Size); err != nil {
			return nil, fmt.Errorf("timescale: FindPerSourceLedgerGaps scan: %w", err)
		}
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("timescale: FindPerSourceLedgerGaps rows.Err: %w", err)
	}
	return out, nil
}
