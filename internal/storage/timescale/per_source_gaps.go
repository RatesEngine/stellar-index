package timescale

import (
	"context"
	"fmt"
	"time"
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

	// Genesis is the first ledger at which this source could
	// possibly have data — typically the contract's deploy
	// ledger for Soroban sources, or 2 for SDEX (Stellar's
	// first non-genesis ledger). Used by ADR-0031's data-derived
	// density: expected = tip - Genesis + 1; density = distinct
	// / expected. Sources without a known genesis (off-chain
	// CEX/FX) shouldn't be in this registry; their freshness is
	// surfaced through a different signal entirely.
	Genesis int64

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

	// MinGapSizeOverride overrides [GapDetectorMinGapSize] for this
	// target. Default 0 means "use the global default of 1000
	// ledgers." Sparse sources (Blend auctions / admin events that
	// only emit on operator action; CCTP/Rozo which see infrequent
	// cross-chain hops) need a much higher threshold or every
	// quiet stretch trips the page-tier alert as a false positive.
	//
	// Live r1 measurement (2026-05-29): blend_auctions has 8049
	// distinct ledgers across a 5.9M-ledger span — one event per
	// ~735 ledgers AVERAGE, so the 1000-ledger threshold is
	// guaranteed to produce hundreds of "gaps" that aren't gaps.
	// Use a per-target threshold tuned to the source's emit cadence.
	//
	// Setting this to a positive value DOES NOT make the source
	// less monitored — it just shifts the page threshold to a
	// number that distinguishes "natural sparsity" from "writer
	// halted." A 500K-ledger gap on blend_auctions still pages.
	MinGapSizeOverride int64

	// ScanCadence overrides [GapDetectorInterval] for this target.
	// Default 0 means "use the global default of 30 min." Use a
	// LONGER cadence for huge-table targets where the LAG-DISTINCT
	// scan is structurally slow (live r1 incident 2026-05-29: SDEX
	// trades is ~62M rows and the scan takes > 15 min; running it
	// every 30 min meant new cycles stacked on top of unfinished
	// ones, lighting both the `slo_latency_burn` page and starving
	// trade-insert latency). For these targets we accept a 6-hour
	// signal cadence in exchange for postgres not being permanently
	// saturated.
	//
	// The scan still has the per-target Go-side timeout (15 min) +
	// the SQL `SET LOCAL statement_timeout` (5 min) backstop so a
	// single cycle can't run unbounded; the cadence just bounds how
	// often it's allowed to fire at all.
	ScanCadence time.Duration
}

// EffectiveMinGapSize returns the threshold this target should use,
// preferring [MinGapSizeOverride] if non-zero. Single source of
// truth for both [FindPerSourceLedgerGaps] and the alert-rule
// query layer.
func (t GapDetectorTarget) EffectiveMinGapSize() int64 {
	if t.MinGapSizeOverride > 0 {
		return t.MinGapSizeOverride
	}
	return GapDetectorMinGapSize
}

// EffectiveScanCadence returns the per-target scan cadence,
// preferring [ScanCadence] if non-zero. Single source of truth
// for the scheduler in [RunGapDetector].
func (t GapDetectorTarget) EffectiveScanCadence() time.Duration {
	if t.ScanCadence > 0 {
		return t.ScanCadence
	}
	return GapDetectorInterval
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
	// Soroban era starts at L50,457,424 on pubnet. SEP-41 tokens
	// have no single deploy-ledger genesis (the standard is
	// implemented by every Soroban token); use the era boundary
	// as the conservative lower bound — anything earlier has no
	// SEP-41 emissions by definition.
	{Source: "sep41-transfers", Table: "sep41_transfers", LedgerColumn: "ledger", Genesis: 50_457_424},
	// SEP-41 supply events fire only on mint/burn/clawback — much
	// rarer than transfers. Live r1: most token issuers go many
	// hours without a supply mutation.
	{Source: "sep41-supply", Table: "sep41_supply_events", LedgerColumn: "ledger", Genesis: 50_457_424, MinGapSizeOverride: 100000},
	// CCTP / Rozo are cross-chain bridges with sparse traffic
	// (hours-to-days between events). 100K-ledger gap threshold
	// silences quiet-period false positives without losing
	// "writer wedged for >1.5 days" pages. CCTP/Rozo are new
	// (2026-05-20 deploy) so the genesis is recent.
	{Source: "cctp", Table: "cctp_events", LedgerColumn: "ledger", Genesis: 62_403_000, MinGapSizeOverride: 100000},
	{Source: "rozo", Table: "rozo_events", LedgerColumn: "ledger", Genesis: 62_403_000, MinGapSizeOverride: 100000},
	// comet_liquidity: pool-events are sparse; 2026-05-29 find-data-
	// gaps showed 17 natural gaps across cascade-era data with max
	// 7826 ledgers (~11h of natural pool silence). 50K threshold.
	{Source: "comet-liquidity", Table: "comet_liquidity", LedgerColumn: "ledger", Genesis: 51_499_546, MinGapSizeOverride: 50000},
	{Source: "soroswap-skim", Table: "soroswap_skim_events", LedgerColumn: "ledger", Genesis: 50_746_266},
	{Source: "phoenix-liquidity", Table: "phoenix_liquidity", LedgerColumn: "ledger", Genesis: 51_572_016},
	{Source: "phoenix-stake", Table: "phoenix_stake_events", LedgerColumn: "ledger", Genesis: 51_572_016},
	// blend_auctions: live r1 (2026-05-28) showed 8049 distinct
	// ledgers across a 5.9M-ledger span = one event per ~735
	// ledgers. 2026-05-29 measurement bumped the 50K override to
	// 100K because the observed max gap (53515) was just over the
	// previous threshold — pages on natural sparsity.
	{Source: "blend-auctions", Table: "blend_auctions", LedgerColumn: "ledger", Genesis: 51_499_546, MinGapSizeOverride: 100000},
	// blend_positions: live ingest only started 2026-05-28 (rc.83
	// migration); 7635-ledger max gap = pre-history boundary +
	// natural sparsity. 50K threshold.
	{Source: "blend-positions", Table: "blend_positions", LedgerColumn: "ledger", Genesis: 51_499_546, MinGapSizeOverride: 50000},
	// blend_emissions: emissions update on operator action (rare).
	// blend_admin: admin actions are rare by design.
	{Source: "blend-emissions", Table: "blend_emissions", LedgerColumn: "ledger", Genesis: 51_499_546, MinGapSizeOverride: 100000},
	{Source: "blend-admin", Table: "blend_admin", LedgerColumn: "ledger", Genesis: 51_499_546, MinGapSizeOverride: 100000},
	// soroban-events spans the entire Soroban era from pubnet
	// activation. Same lower bound as sep41-transfers. Long
	// ScanCadence: 50M+ rows, scan dominates postgres for 5+ min
	// per cycle so 30 min cadence starves trade-insert latency
	// (live r1 incident 2026-05-29 → /goal directive).
	{Source: "soroban-events", Table: "soroban_events", LedgerColumn: "ledger", Genesis: 50_457_424, ScanCadence: 6 * time.Hour},
	// SDEX is classic-DEX and does NOT flow through soroban_events.
	// Its rows live in the unified `trades` hypertable alongside
	// every other trade-emitting source; the WhereFilter slices
	// only the SDEX subset for the gap scan. Before this target,
	// SDEX had zero data-derived coverage signal — a symmetric F-
	// 0020-class incident in the classic-DEX path would have gone
	// undetected. (See ADR-0030.)
	//
	// 1M-ledger threshold: live r1 (2026-05-29) measurement showed
	// 35.86M distinct ledgers across the 61.9M-ledger span from
	// SDEX inception → tip (~58% density). Pre-2024 SDEX was very
	// thinly traded — the largest natural-sparsity contiguous gap
	// at the time of measurement was 574,674 ledgers, so the 1K
	// default would page constantly on historical data. A 1M-ledger
	// gap (~1.5 weeks of network time) on SDEX still pages because
	// recent SDEX is densely active (>1M trades / day on 2026-05-27).
	// SDEX scans 62M trades rows — long ScanCadence so we don't
	// pile concurrent runs on postgres (see soroban-events comment).
	{Source: "sdex", Table: "trades", LedgerColumn: "ledger", WhereFilter: "source = 'sdex'", Genesis: 2, MinGapSizeOverride: 1000000, ScanCadence: 6 * time.Hour},
	// SDEX offer-state events (OfferCreated/OfferUpdated/OfferRemoved)
	// land in their own hypertable — complement to the trade flow.
	// An offer-events writer halt would not show up in the trades
	// gauge above; the dedicated target catches it.
	{Source: "sdex-offers", Table: "sdex_offer_events", LedgerColumn: "ledger", Genesis: 2},
	// Soroban-DEX sources also write into the unified `trades`
	// hypertable; each gets a per-source gap-detector target with
	// a source-tagged WhereFilter. Without these the API's
	// `backfill_coverage` listing reports 0% for those sources
	// (live r1 incident 2026-06-01: post Phase 2 removal of the
	// cursor-derived density path, the snapshot-derived path had
	// no rows for these sources). Genesis matches each contract's
	// pubnet deployment ledger.
	{Source: "aquarius", Table: "trades", LedgerColumn: "ledger", WhereFilter: "source = 'aquarius'", Genesis: 52_728_375, MinGapSizeOverride: 100000},
	{Source: "soroswap", Table: "trades", LedgerColumn: "ledger", WhereFilter: "source = 'soroswap'", Genesis: 50_746_266, MinGapSizeOverride: 100000},
	{Source: "phoenix", Table: "trades", LedgerColumn: "ledger", WhereFilter: "source = 'phoenix'", Genesis: 51_572_016, MinGapSizeOverride: 100000},
	{Source: "comet", Table: "trades", LedgerColumn: "ledger", WhereFilter: "source = 'comet'", Genesis: 51_499_546, MinGapSizeOverride: 100000},
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
	// a compile-time const from DefaultGapDetectorTargets; ADR-0030
	// makes this invariant load-bearing).
	filter := ""
	if target.WhereFilter != "" {
		filter = " AND (" + target.WhereFilter + ")"
	}
	//nolint:gosec // G201: identifiers from compile-time const list per ADR-0030
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

	// SQL-level statement_timeout backstop: when the Go-side ctx
	// times out mid-query the database/sql driver tries to cancel
	// via PG's async cancellation protocol — best-effort. r1
	// incident 2026-05-29: three concurrent SDEX scans accumulated
	// over multiple cycles because Go cancellation didn't reach
	// PG; the queries kept running and starved trade-insert
	// latency. A session statement_timeout makes PG itself abort,
	// no leak possible. 5 min is well below the gap-detector's
	// per-target Go-side timeout so this never even fires in the
	// happy path.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("timescale: FindPerSourceLedgerGaps begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, "SET LOCAL statement_timeout = '300000'"); err != nil {
		return nil, fmt.Errorf("timescale: FindPerSourceLedgerGaps SET: %w", err)
	}
	rows, err := tx.QueryContext(ctx, query, from, to, minGapSize)
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
