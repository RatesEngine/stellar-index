package completeness

import (
	"context"
	"sort"

	"github.com/RatesEngine/rates-engine/internal/consumer"
	"github.com/RatesEngine/rates-engine/internal/events"
	"github.com/RatesEngine/rates-engine/internal/sources/sorobanevents"
)

// Decoder is the per-source decode surface the reconciler re-runs over
// soroban_events. Satisfied by every internal/sources/<venue> decoder
// (and by dispatcher routing). Matches + Decode are the SAME functions
// the projector uses, so the re-derive is the deterministic
// recomputation of what the projector should have written — not a
// parallel reimplementation that could disagree for the wrong reason.
type Decoder interface {
	Matches(events.Event) bool
	Decode(events.Event) ([]consumer.Event, error)
}

// SorobanEventStreamer is the read side the reconciler needs.
// *timescale.Store satisfies it via StreamSorobanEvents.
type SorobanEventStreamer interface {
	StreamSorobanEvents(
		ctx context.Context,
		from, to uint32,
		contractIDs []string,
		topic0Syms []string,
		excludeTopic0Syms []string,
		fn func(sorobanevents.Row) error,
	) error
}

// ProjectionGap is a ledger where the number of rows the decoder would
// emit from soroban_events (Expected) disagrees with the number
// actually present in the protocol table (Actual). Expected > Actual is
// a projection/persistence drop; Actual > Expected is a phantom row
// (or a pre-event_index-fix duplicate). Either is a real discrepancy
// localized to one ledger (ADR-0033 Claim 2b).
type ProjectionGap struct {
	Ledger   uint32
	Expected int
	Actual   int
}

// ReDeriveOutputCounts re-runs the decoder over the raw events in
// [from, to] and returns how many outputs it emits per ledger. Outputs
// are attributed to the triggering row's ledger; because every
// correlation group (Phoenix's 8 events, Soroswap's swap+sync) shares a
// single (ledger, tx, op), the group always completes within its own
// ledger and the per-ledger count is exact.
//
// contractIDs / topic0Syms are the same SQL prefilters the projector
// passes for this source (empty = match-by-topic across all contracts).
// Malformed / undecodable rows are skipped (mirroring the projector's
// soft-fail) — they surface separately via the recognition audit.
func ReDeriveOutputCounts(
	ctx context.Context,
	s SorobanEventStreamer,
	dec Decoder,
	contractIDs, topic0Syms []string,
	from, to uint32,
) (map[uint32]int, error) {
	counts := make(map[uint32]int)
	err := s.StreamSorobanEvents(ctx, from, to, contractIDs, topic0Syms, nil,
		func(row sorobanevents.Row) error {
			ev, rerr := sorobanevents.Reconstruct(row)
			if rerr != nil {
				return nil //nolint:nilerr // soft-fail like the projector; recognition audit catches shape issues.
			}
			if !dec.Matches(ev) {
				return nil
			}
			outs, derr := dec.Decode(ev)
			if derr != nil {
				return nil //nolint:nilerr // deterministically-broken row; skip, don't abort the audit.
			}
			if len(outs) > 0 {
				counts[row.Ledger] += len(outs)
			}
			return nil
		})
	if err != nil {
		return nil, err
	}
	return counts, nil
}

// ReDeriveOutputCountsByKind is the multi-table generalization of
// ReDeriveOutputCounts: it returns the re-derived output counts keyed by
// the output's EventKind(), then by ledger. A single decoder routes
// different output kinds to different tables (soroswap emits
// "soroswap.trade" → trades AND "soroswap.skim" → soroswap_skim_events;
// blend emits five kinds across four tables), so reconciliation per
// table must count ONLY the kinds that land in that table — counting
// every output would overcount any table that receives a subset.
//
// Stream once per source; SumKinds then projects the kinds for each
// target table. Same soft-fail semantics as ReDeriveOutputCounts.
func ReDeriveOutputCountsByKind(
	ctx context.Context,
	s SorobanEventStreamer,
	dec Decoder,
	contractIDs, topic0Syms []string,
	from, to uint32,
) (map[string]map[uint32]int, error) {
	byKind := make(map[string]map[uint32]int)
	err := s.StreamSorobanEvents(ctx, from, to, contractIDs, topic0Syms, nil,
		func(row sorobanevents.Row) error {
			ev, rerr := sorobanevents.Reconstruct(row)
			if rerr != nil {
				return nil //nolint:nilerr // soft-fail like the projector.
			}
			if !dec.Matches(ev) {
				return nil
			}
			outs, derr := dec.Decode(ev)
			if derr != nil {
				return nil //nolint:nilerr // deterministically-broken row; skip.
			}
			for _, out := range outs {
				k := out.EventKind()
				if byKind[k] == nil {
					byKind[k] = make(map[uint32]int)
				}
				byKind[k][row.Ledger]++
			}
			return nil
		})
	if err != nil {
		return nil, err
	}
	return byKind, nil
}

// SumKinds projects a by-kind count map down to a single per-ledger
// count over the given EventKinds — the expected row count for the
// table those kinds route to.
func SumKinds(byKind map[string]map[uint32]int, kinds ...string) map[uint32]int {
	out := make(map[uint32]int)
	for _, k := range kinds {
		for ledger, c := range byKind[k] {
			out[ledger] += c
		}
	}
	return out
}

// ReconcileCounts diffs expected (decoder re-derive) against actual
// (protocol-table rows) per ledger and returns every ledger where they
// disagree, sorted ascending. An empty result means every ledger the
// decoder would have produced rows for has exactly those rows — Claim
// 2b holds for the range.
func ReconcileCounts(expected, actual map[uint32]int) []ProjectionGap {
	var gaps []ProjectionGap
	seen := make(map[uint32]bool, len(expected))
	for ledger, exp := range expected {
		seen[ledger] = true
		if act := actual[ledger]; act != exp {
			gaps = append(gaps, ProjectionGap{Ledger: ledger, Expected: exp, Actual: act})
		}
	}
	for ledger, act := range actual {
		if seen[ledger] {
			continue
		}
		// Expected this ledger to produce nothing, but the table has
		// rows — a phantom (row with no backing captured event).
		if act != 0 {
			gaps = append(gaps, ProjectionGap{Ledger: ledger, Expected: 0, Actual: act})
		}
	}
	sort.Slice(gaps, func(i, j int) bool { return gaps[i].Ledger < gaps[j].Ledger })
	return gaps
}
