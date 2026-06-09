package clickhouse

import (
	"context"
	"fmt"

	"github.com/RatesEngine/rates-engine/internal/events"
)

// ReconcileEventStreamer adapts the CH contract_events read path to the
// completeness package's EventStreamer seam (projection reconciliation sourced
// from the certified lake, off the serving DB). Satisfies
// completeness.EventStreamer structurally.
type ReconcileEventStreamer struct{ Addr string }

// StreamContractEvents streams events.Event for [from,to] narrowed by the
// source's prefilter — no FINAL (the projector's downstream writes are
// idempotent; the reconcile only counts).
func (s ReconcileEventStreamer) StreamContractEvents(ctx context.Context, from, to uint32, contractIDs, topic0Syms []string, fn func(events.Event) error) error {
	return StreamContractEventsFiltered(ctx, s.Addr, from, to, contractIDs, topic0Syms, nil, fn)
}

// ContiguousWatermark returns the highest ledger L such that stellar.ledgers
// contains every ledger in [from, L] with NO hole — i.e. the lake is provably
// complete from `from` up to L. It is the real-time projector's safe upper read
// bound when reading forward events from CH (ADR-0034 #10 feed-switch).
//
// Why it's needed: the live dual-sink (LiveSink) is best-effort — it DROPS whole
// ledgers under buffer pressure and a flush can partially fail — so CH can have
// holes near the tip. The projector advances its per-source cursor to the upper
// bound unconditionally (to skip event-free stretches), so reading past a hole
// would silently lose that ledger's protocol events. Clamping the upper bound to
// this watermark makes the projector stall AT a hole until the catch-up timer
// heals it, rather than skipping over it.
//
// Completeness is keyed off the ledgers table, which is a per-ledger commit
// marker: Sink.Flush writes stellar.ledgers LAST, so a ledger_seq present there
// guarantees that ledger's contract_events (and all other tables) are already
// durable. A buffer-full drop drops the whole extract, so it leaves no ledgers
// row either — either way "present in ledgers" ⟹ "complete in CH".
//
// Returns from-1 when CH has not yet reached `from` (nothing complete to read);
// callers treat tip <= from as idle.
func ContiguousWatermark(ctx context.Context, addr string, from uint32) (uint32, error) {
	conn, err := openRead(ctx, addr)
	if err != nil {
		return 0, err
	}
	defer func() { _ = conn.Close() }()

	// ch_max: highest ledger present in the lake.
	// first_gap_start: the lowest missing ledger >= from (0 when there is none).
	//
	// The leadInFrame frame (CURRENT ROW .. 1 FOLLOWING) returns the current
	// row's own value for the last row in the partition, so the final ledger
	// never registers a spurious trailing gap. min() over an empty gap set
	// returns 0 (UInt default), which we read as "no hole".
	// Both columns are wrapped toUInt64(ifNull(…, 0)) so they scan as plain
	// non-nullable uint64 regardless of CH's promotion rules: scalar subqueries
	// are Nullable, max(ledger_seq) is UInt32 but min(ledger_seq+1) widens to
	// UInt64, and an empty set yields NULL. ifNull(…,0) maps "no gap" / "empty
	// lake" to 0; toUInt64 unifies the width. The driver rejects type
	// mismatches, so this normalization is load-bearing.
	const q = `
		SELECT
			toUInt64(ifNull((SELECT max(ledger_seq) FROM stellar.ledgers), 0)) AS ch_max,
			toUInt64(ifNull((SELECT min(gap_start) FROM (
				SELECT ledger_seq + 1 AS gap_start
				FROM (
					SELECT ledger_seq,
					       leadInFrame(ledger_seq) OVER (
					           ORDER BY ledger_seq ROWS BETWEEN CURRENT ROW AND 1 FOLLOWING
					       ) AS nxt
					FROM (SELECT DISTINCT ledger_seq FROM stellar.ledgers WHERE ledger_seq >= ?)
				)
				WHERE nxt > ledger_seq + 1
			)), 0)) AS first_gap_start`

	var chMax, firstGap uint64
	if err := conn.QueryRow(ctx, q, from).Scan(&chMax, &firstGap); err != nil {
		return 0, fmt.Errorf("clickhouse: contiguous watermark from %d: %w", from, err)
	}
	// Ledger sequences are always well within uint32.
	return watermark(from, uint32(chMax), uint32(firstGap)), nil
}

// SubstrateProblem returns the earliest ledger in [from,to] where the CH lake's
// substrate fails (ADR-0033 Claim 1): a missing ledger (contiguity gap) or a
// hash-chain break (prev_hash != the prior ledger's ledger_hash). Returns
// (0, false) when the substrate is intact over the whole range — i.e. the lake
// is provably continuous + hash-linked, the strongest "we captured everything"
// claim. This is the cheap, re-runnable form of the one-shot #7 certification.
//
// Both checks run over a per-ledger dedup (GROUP BY ledger_seq, argMax by
// ingested_at) so ReplacingMergeTree duplicate parts don't create false breaks.
func SubstrateProblem(ctx context.Context, addr string, from, to uint32) (problem uint32, hasProblem bool, detail string, err error) {
	conn, oerr := openRead(ctx, addr)
	if oerr != nil {
		return 0, false, "", oerr
	}
	defer func() { _ = conn.Close() }()

	// First contiguity gap (first missing ledger), 0 if none.
	const gapQ = `
		SELECT toUInt64(ifNull((SELECT min(gap_start) FROM (
			SELECT ledger_seq + 1 AS gap_start
			FROM (
				SELECT ledger_seq, leadInFrame(ledger_seq) OVER (
					ORDER BY ledger_seq ROWS BETWEEN CURRENT ROW AND 1 FOLLOWING
				) AS nxt
				FROM (SELECT DISTINCT ledger_seq FROM stellar.ledgers WHERE ledger_seq BETWEEN ? AND ?)
			)
			WHERE nxt > ledger_seq + 1
		)), 0))`
	var firstGap uint64
	if qerr := conn.QueryRow(ctx, gapQ, from, to).Scan(&firstGap); qerr != nil {
		return 0, false, "", fmt.Errorf("clickhouse: substrate contiguity [%d,%d]: %w", from, to, qerr)
	}

	// First hash-chain break: prev_hash != the immediately-prior ledger's hash.
	const chainQ = `
		SELECT toUInt64(ifNull((SELECT min(ledger_seq) FROM (
			SELECT ledger_seq, prev_hash,
			       lagInFrame(ledger_hash) OVER (ORDER BY ledger_seq) AS prior_hash
			FROM (
				SELECT ledger_seq, argMax(ledger_hash, ingested_at) AS ledger_hash, argMax(prev_hash, ingested_at) AS prev_hash
				FROM stellar.ledgers WHERE ledger_seq BETWEEN ? AND ?
				GROUP BY ledger_seq
			)
		) WHERE ledger_seq > ? AND prior_hash != '' AND prev_hash != prior_hash), 0))`
	var firstBreak uint64
	if qerr := conn.QueryRow(ctx, chainQ, from, to, from).Scan(&firstBreak); qerr != nil {
		return 0, false, "", fmt.Errorf("clickhouse: substrate hash-chain [%d,%d]: %w", from, to, qerr)
	}

	switch {
	case firstGap == 0 && firstBreak == 0:
		return 0, false, "", nil
	case firstGap != 0 && (firstBreak == 0 || firstGap <= firstBreak):
		return uint32(firstGap), true, fmt.Sprintf("substrate: missing ledger at %d", firstGap), nil
	default:
		return uint32(firstBreak), true, fmt.Sprintf("substrate: hash-chain break at %d", firstBreak), nil
	}
}

// watermark is the pure interpretation of a ContiguousWatermark query result:
//   - chMax < from        → from-1 (CH has not reached `from`; nothing complete)
//   - firstGap == 0        → chMax (no hole at or above `from`; complete to the tip)
//   - otherwise            → firstGap-1 (complete up to just before the first hole)
func watermark(from, chMax, firstGap uint32) uint32 {
	if chMax < from {
		return from - 1
	}
	if firstGap == 0 {
		return chMax
	}
	return firstGap - 1
}
