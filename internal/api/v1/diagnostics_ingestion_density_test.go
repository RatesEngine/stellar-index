package v1

import (
	"testing"
	"time"

	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// TestMergeCoverageIntervals covers the four interesting shapes the
// sweep-line merge has to handle: non-overlapping, overlapping,
// adjacent (touching), and out-of-order input. Behavior must match
// the contract documented on the function.
func TestMergeCoverageIntervals(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   []coverageInterval
		want []coverageInterval
	}{
		{
			name: "empty",
			in:   nil,
			want: nil,
		},
		{
			name: "single",
			in:   []coverageInterval{{1, 100}},
			want: []coverageInterval{{1, 100}},
		},
		{
			name: "non-overlapping",
			in:   []coverageInterval{{1, 100}, {200, 300}},
			want: []coverageInterval{{1, 100}, {200, 300}},
		},
		{
			name: "overlapping",
			in:   []coverageInterval{{1, 100}, {50, 150}},
			want: []coverageInterval{{1, 150}},
		},
		{
			name: "adjacent (End+1 == next.Start)",
			in:   []coverageInterval{{1, 100}, {101, 200}},
			want: []coverageInterval{{1, 200}},
		},
		{
			name: "out-of-order input gets sorted",
			in:   []coverageInterval{{200, 300}, {1, 100}, {150, 250}},
			want: []coverageInterval{{1, 100}, {150, 300}},
		},
		{
			name: "fully nested (inner inside outer)",
			in:   []coverageInterval{{1, 1000}, {500, 600}},
			want: []coverageInterval{{1, 1000}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mergeCoverageIntervals(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d intervals %v, want %d %v", len(got), got, len(tc.want), tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("interval %d = %v, want %v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestSumCoverageIntervals confirms the inclusive-bounds counting
// (End - Start + 1) per interval — easy to get wrong as an
// off-by-one if Go-loop intuition takes over.
func TestSumCoverageIntervals(t *testing.T) {
	t.Parallel()
	got := sumCoverageIntervals([]coverageInterval{{1, 10}, {100, 100}, {1000, 1099}})
	want := int64(10 + 1 + 100)
	if got != want {
		t.Errorf("sum = %d, want %d", got, want)
	}
}

// TestDecoderSetContains pins the substring-vs-token-match
// distinction. "reflector-dex" must NOT match
// "reflector-dex-extended" if that ever ships, and a leading or
// trailing decoder must still work.
func TestDecoderSetContains(t *testing.T) {
	t.Parallel()
	cases := []struct {
		set, source string
		want        bool
	}{
		{"sdex", "sdex", true},
		{"sdex,soroswap", "sdex", true},
		{"sdex,soroswap", "soroswap", true},
		{"sdex,soroswap,aquarius", "soroswap", true},
		{"sdex,soroswap", "aquarius", false},
		{"reflector-dex-extended", "reflector-dex", false},
		{"", "sdex", false},
		{"sdex", "", false},
	}
	for _, tc := range cases {
		if got := decoderSetContains(tc.set, tc.source); got != tc.want {
			t.Errorf("decoderSetContains(%q, %q) = %v, want %v", tc.set, tc.source, got, tc.want)
		}
	}
}

// TestParseBackfillSubFull covers the three-piece parse — start,
// end, decoder. parseBackfillSub already exists for end-only; this
// is the new helper for density projection.
func TestParseBackfillSubFull(t *testing.T) {
	t.Parallel()
	cases := []struct {
		sub                string
		wantStart, wantEnd int64
		wantDecoder        string
	}{
		{"100-200:sdex", 100, 200, "sdex"},
		{"100-200:sdex,soroswap", 100, 200, "sdex,soroswap"},
		{"2-15300000:sdex", 2, 15300000, "sdex"},
		{"malformed", 0, 0, ""},
		{":sdex", 0, 0, ""},
		{"100:sdex", 0, 0, "sdex"}, // missing dash → decoder OK, start/end zero
	}
	for _, tc := range cases {
		gotStart, gotEnd, gotDecoder := parseBackfillSubFull(tc.sub)
		if gotStart != tc.wantStart || gotEnd != tc.wantEnd || gotDecoder != tc.wantDecoder {
			t.Errorf("parseBackfillSubFull(%q) = (%d, %d, %q), want (%d, %d, %q)",
				tc.sub, gotStart, gotEnd, gotDecoder, tc.wantStart, tc.wantEnd, tc.wantDecoder)
		}
	}
}

// TestComputeSourceDensity covers the full pipeline: cursor rows
// → filter by source → completed-portion extraction → interval
// merge → density computation.
func TestComputeSourceDensity(t *testing.T) {
	t.Parallel()
	now := time.Now()

	cases := []struct {
		name           string
		cursors        []timescale.Cursor
		source         string
		genesis        int64
		tip            int64
		wantCovered    int64
		wantDensityMin float64 // inclusive lower bound
		wantDensityMax float64 // inclusive upper bound
	}{
		{
			name:           "no cursors → zero density",
			cursors:        nil,
			source:         "sdex",
			genesis:        1,
			tip:            1000,
			wantCovered:    0,
			wantDensityMin: 0.0,
			wantDensityMax: 0.0,
		},
		{
			name: "single complete range covers full expected → 100%",
			cursors: []timescale.Cursor{
				{Source: "backfill", Sub: "1-1000:sdex", LastLedger: 1000, UpdatedAt: now},
			},
			source:         "sdex",
			genesis:        1,
			tip:            1000,
			wantCovered:    1000,
			wantDensityMin: 1.0,
			wantDensityMax: 1.0,
		},
		{
			name: "partial range (worker only got halfway)",
			cursors: []timescale.Cursor{
				{Source: "backfill", Sub: "1-1000:sdex", LastLedger: 500, UpdatedAt: now},
			},
			source:         "sdex",
			genesis:        1,
			tip:            1000,
			wantCovered:    500,
			wantDensityMin: 0.499,
			wantDensityMax: 0.501,
		},
		{
			name: "two non-overlapping ranges cover ~half",
			cursors: []timescale.Cursor{
				{Source: "backfill", Sub: "1-200:sdex", LastLedger: 200, UpdatedAt: now},
				{Source: "backfill", Sub: "501-800:sdex", LastLedger: 800, UpdatedAt: now},
			},
			source:         "sdex",
			genesis:        1,
			tip:            1000,
			wantCovered:    200 + 300, // [1,200] + [501,800]
			wantDensityMin: 0.499,
			wantDensityMax: 0.501,
		},
		{
			name: "overlapping ranges get merged (don't double-count)",
			cursors: []timescale.Cursor{
				{Source: "backfill", Sub: "1-500:sdex", LastLedger: 500, UpdatedAt: now},
				{Source: "backfill", Sub: "300-1000:sdex", LastLedger: 1000, UpdatedAt: now},
			},
			source:         "sdex",
			genesis:        1,
			tip:            1000,
			wantCovered:    1000, // merged [1, 1000], not 500 + 700 = 1200
			wantDensityMin: 1.0,
			wantDensityMax: 1.0,
		},
		{
			name: "multi-decoder cursor: includes source when present",
			cursors: []timescale.Cursor{
				{Source: "backfill", Sub: "1-1000:sdex,soroswap", LastLedger: 1000, UpdatedAt: now},
			},
			source:         "soroswap",
			genesis:        1,
			tip:            1000,
			wantCovered:    1000,
			wantDensityMin: 1.0,
			wantDensityMax: 1.0,
		},
		{
			name: "multi-decoder cursor: excludes when source absent",
			cursors: []timescale.Cursor{
				{Source: "backfill", Sub: "1-1000:sdex,soroswap", LastLedger: 1000, UpdatedAt: now},
			},
			source:         "aquarius",
			genesis:        1,
			tip:            1000,
			wantCovered:    0,
			wantDensityMin: 0.0,
			wantDensityMax: 0.0,
		},
		{
			name: "live cursor with NO backfill anchor → no credit (0)",
			cursors: []timescale.Cursor{
				{Source: "ledgerstream", Sub: "", LastLedger: 1000, UpdatedAt: now},
			},
			source:  "sdex",
			genesis: 1,
			tip:     1000,
			// A source that's never been backfilled stays 0 even
			// though live ingest is running — live-only coverage
			// from the deploy ledger is not "we have its history",
			// and the cursor-first projection requires at least
			// one backfill anchor proving the decoder ran for this
			// source. extendWithLiveTail's len(merged)==0 early-
			// return enforces this guard.
			wantCovered:    0,
			wantDensityMin: 0.0,
			wantDensityMax: 0.0,
		},
		{
			// Same as above but with FirstLedger populated — the
			// guard is len(merged)==0, NOT FirstLedger==0, so a
			// populated FirstLedger MUST NOT bypass the no-backfill
			// guard.
			name: "live cursor with FirstLedger but NO backfill anchor → still 0",
			cursors: []timescale.Cursor{
				{Source: "ledgerstream", Sub: "", FirstLedger: 1, LastLedger: 1000, UpdatedAt: now},
			},
			source:         "sdex",
			genesis:        1,
			tip:            1000,
			wantCovered:    0,
			wantDensityMin: 0.0,
			wantDensityMax: 0.0,
		},
		{
			name: "live tail with FirstLedger closes the full backfill→tip span",
			cursors: []timescale.Cursor{
				{Source: "backfill", Sub: "1-800:sdex", LastLedger: 800, UpdatedAt: now},
				{Source: "ledgerstream", Sub: "", FirstLedger: 1, LastLedger: 1000, UpdatedAt: now},
			},
			source:         "sdex",
			genesis:        1,
			tip:            1000,
			wantCovered:    1000, // [1,800] ∪ live [1,1000] = [1,1000]
			wantDensityMin: 1.0,
			wantDensityMax: 1.0,
		},
		{
			// Migration 0046: when a live cursor exposes FirstLedger,
			// the live span [FirstLedger, LastLedger] gets merged into
			// the coverage union directly. A non-bridging scenario:
			// FirstLedger=500 means live ingest only walked [500, 1000]
			// (e.g. soroswap-router enabled at L500 with live cursor
			// already past). The pre-FirstLedger interior gap [201, 499]
			// stays uncovered — the FIX for the over-credit case the
			// old head-band heuristic introduced.
			name: "interior gap NOT bridged when live FirstLedger sits above it",
			cursors: []timescale.Cursor{
				{Source: "backfill", Sub: "1-200:sdex", LastLedger: 200, UpdatedAt: now},
				{Source: "backfill", Sub: "500-800:sdex", LastLedger: 800, UpdatedAt: now},
				{Source: "ledgerstream", Sub: "", FirstLedger: 500, LastLedger: 1000, UpdatedAt: now},
			},
			source:  "sdex",
			genesis: 1,
			tip:     1000,
			// [1,200] ∪ [500,800] ∪ live [500,1000] = [1,200] ∪
			// [500,1000]. Hole [201,499] stays uncovered.
			wantCovered:    200 + 501,
			wantDensityMin: 0.700,
			wantDensityMax: 0.702,
		},
		{
			// 2026-05-28 honest-density fix: a NULL FirstLedger no
			// longer falls back to genesis. The previous fallback
			// silently inflated density to 100% for sources whose
			// live cursor pre-dated migration 0046 (live ran continuously
			// but never re-INSERT'd the row, so first_ledger stayed
			// NULL forever). After this change a NULL live cursor
			// contributes no historical span — only backfill cursors
			// credit coverage. UpsertCursor's UPDATE branch now
			// COALESCE-populates first_ledger on the first write
			// after deploy, so this branch only applies in the
			// seconds between deploy and the first live tick.
			name: "NULL FirstLedger contributes no historical span (honest)",
			cursors: []timescale.Cursor{
				{Source: "backfill", Sub: "1-300:sdex", LastLedger: 300, UpdatedAt: now},
				{Source: "backfill", Sub: "700-750:sdex", LastLedger: 750, UpdatedAt: now},
				{Source: "ledgerstream", Sub: "", LastLedger: 1000, UpdatedAt: now}, // FirstLedger=0 → no span
			},
			source:  "sdex",
			genesis: 1,
			tip:     1000,
			// Honest answer: [1,300] ∪ [700,750] = 300 + 51 = 351.
			// The [301,699] and [751,1000] gaps stay exposed because
			// the live cursor doesn't yet know how far back its own
			// coverage extends.
			wantCovered:    351,
			wantDensityMin: 0.350,
			wantDensityMax: 0.352,
		},
		{
			name: "live cursor below tip with FirstLedger=genesis still covers full span",
			cursors: []timescale.Cursor{
				{Source: "backfill", Sub: "1-200:sdex", LastLedger: 200, UpdatedAt: now},
				{Source: "backfill", Sub: "500-800:sdex", LastLedger: 800, UpdatedAt: now},
				{Source: "ledgerstream", Sub: "", FirstLedger: 1, LastLedger: 400, UpdatedAt: now},
			},
			source:  "sdex",
			genesis: 1,
			tip:     1000,
			// live [1,400] ∪ backfill [1,200] ∪ [500,800] =
			// [1,400] ∪ [500,800] = 400 + 301 = 701.
			wantCovered:    701,
			wantDensityMin: 0.700,
			wantDensityMax: 0.702,
		},
		{
			// The original "lower boundary never credited" test
			// pre-dated migration 0046. Under 100%-mission semantics
			// the live cursor's [FirstLedger, last] band closes the
			// [genesis, firstBackfillStart] sub-band exactly. Test
			// renamed to reflect the new contract.
			name: "100% mission: live FirstLedger=genesis + backfill→tip → 100%",
			cursors: []timescale.Cursor{
				{Source: "backfill", Sub: "500-1000:sdex", LastLedger: 1000, UpdatedAt: now},
				{Source: "ledgerstream", Sub: "", FirstLedger: 1, LastLedger: 1000, UpdatedAt: now},
			},
			source:         "sdex",
			genesis:        1,
			tip:            1000,
			wantCovered:    1000, // backfill [500,1000] ∪ live [1,1000] = [1,1000]
			wantDensityMin: 1.0,
			wantDensityMax: 1.0,
		},
		{
			name: "live below backfill top → live span still credited",
			cursors: []timescale.Cursor{
				{Source: "backfill", Sub: "1-900:sdex", LastLedger: 900, UpdatedAt: now},
				{Source: "ledgerstream", Sub: "", FirstLedger: 1, LastLedger: 500, UpdatedAt: now},
			},
			source:         "sdex",
			genesis:        1,
			tip:            1000,
			wantCovered:    900, // backfill [1,900] ∪ live [1,500] = [1,900]
			wantDensityMin: 0.899,
			wantDensityMax: 0.901,
		},
		{
			// Defensive: live cursor's FirstLedger is somehow ABOVE
			// LastLedger (shouldn't happen — UpsertCursor's INSERT
			// path sets them equal, and the UPDATE path only advances
			// LastLedger). Belt-and-braces guard: don't credit
			// anything for a malformed span.
			name: "malformed live span (FirstLedger > LastLedger) credits nothing extra",
			cursors: []timescale.Cursor{
				{Source: "backfill", Sub: "1-500:sdex", LastLedger: 500, UpdatedAt: now},
				{Source: "ledgerstream", Sub: "", FirstLedger: 900, LastLedger: 100, UpdatedAt: now},
			},
			source:         "sdex",
			genesis:        1,
			tip:            1000,
			wantCovered:    500, // only backfill [1,500] survives
			wantDensityMin: 0.499,
			wantDensityMax: 0.501,
		},
		{
			name: "range extends past tip → clamped",
			cursors: []timescale.Cursor{
				{Source: "backfill", Sub: "1-2000:sdex", LastLedger: 2000, UpdatedAt: now},
			},
			source:         "sdex",
			genesis:        1,
			tip:            1000,
			wantCovered:    1000, // clamped to [1, 1000]
			wantDensityMin: 1.0,
			wantDensityMax: 1.0,
		},
		{
			name: "range starts before genesis → clamped",
			cursors: []timescale.Cursor{
				{Source: "backfill", Sub: "1-1000:sdex", LastLedger: 1000, UpdatedAt: now},
			},
			source:         "sdex",
			genesis:        500,
			tip:            1000,
			wantCovered:    501, // [500, 1000] inclusive
			wantDensityMin: 1.0,
			wantDensityMax: 1.0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotCovered, gotDensity := computeSourceDensity(tc.cursors, tc.source, tc.genesis, tc.tip)
			if gotCovered != tc.wantCovered {
				t.Errorf("covered = %d, want %d", gotCovered, tc.wantCovered)
			}
			if gotDensity < tc.wantDensityMin || gotDensity > tc.wantDensityMax {
				t.Errorf("density = %v, want in [%v, %v]", gotDensity, tc.wantDensityMin, tc.wantDensityMax)
			}
		})
	}
}

// TestSourceGenesisLedgerExact guards the per-source first-deploy
// ledgers in `sourceGenesisLedger` against drifting back to rounded
// approximations. Under the granular-coverage mission these values
// are the denominator of `backfill_coverage[].density_pct`, so both
// directions of inexactness are correctness bugs:
//
//   - LOW  → padding the denominator with pre-existence ledgers
//     (100% mathematically unreachable, false-negative gaps).
//   - HIGH → silently hiding genuine early-history ledgers,
//     inflating the density score (false-positive coverage).
//
// The exact values come from per-source WASM-audits at
// `docs/operations/wasm-audits/<src>.md` + the 2026-05-01 r1 walk
// (`evidence/r1-walk-2026-05-01/per-source-final/<src>.json`). If a
// new contract WASM is deployed with an earlier `create_contract`
// ledger (e.g. a factory rebuild on a fork), update the map AND
// this guard's `wantExact` column from the new audit doc.
//
// SDEX is the deliberate exception — Stellar's network-genesis
// ledger 1 carries zero operations by design (it's the genesis
// spec record), so the earliest possible SDEX trade lives in
// ledger 2. The "Soroban-era floor" assertion is therefore gated
// on src != "sdex".
func TestSourceGenesisLedgerExact(t *testing.T) {
	t.Parallel()

	// Soroban activated at L50,457,424 (2024-02-20); no contract-
	// hosted source can have a genesis before this ledger. The
	// floor is a coarse drift detector — any drop below it signals
	// "someone re-rounded back to a deploy-era constant".
	const sorobanActivation int64 = 50_457_424

	// wantExact mirrors the audit-evidence map. Keep in lock-step
	// with `sourceGenesisLedger`. A mismatch on EITHER side
	// (drift in production, or this guard going stale) fails CI.
	wantExact := map[string]int64{
		"sdex":            2,
		"soroswap":        50_746_266,
		"soroswap-router": 50_746_272,
		"aquarius":        52_728_375,
		"phoenix":         51_572_016,
		"comet":           51_499_546,
		"blend":           51_499_546,
		"reflector-dex":   50_644_229,
		"reflector-cex":   50_644_239,
		"reflector-fx":    56_733_481,
		"band":            50_842_736,
		"redstone":        58_758_722,
		"defindex":        57_056_338,
	}

	// 1. Every audited source is in the map with the exact value.
	for src, want := range wantExact {
		got, ok := sourceGenesisLedger[src]
		if !ok {
			t.Errorf("sourceGenesisLedger missing %q (expected exact first-deploy %d)", src, want)
			continue
		}
		if got != want {
			t.Errorf("sourceGenesisLedger[%q] = %d, want %d (see docs/operations/wasm-audits/%s.md)",
				src, got, want, src)
		}
	}

	// 2. No surprise sources crept in without test coverage. cctp +
	// rozo are intentionally absent (see the TODO in the map);
	// adding either here without an audit doc is a process bug.
	for src := range sourceGenesisLedger {
		if _, ok := wantExact[src]; !ok {
			t.Errorf("sourceGenesisLedger has %q but the test guard doesn't — add it to wantExact from docs/operations/wasm-audits/%s.md", src, src)
		}
	}

	// 3. Soroban-era floor: every non-SDEX source's genesis is at
	// or after Soroban activation. A drop below this floor is the
	// signature of "rounded back to a deploy-era constant".
	for src, got := range sourceGenesisLedger {
		if src == "sdex" {
			continue
		}
		if got <= 0 {
			t.Errorf("sourceGenesisLedger[%q] = %d, want > 0 (Soroban sources need a real first-deploy ledger)", src, got)
			continue
		}
		if got < sorobanActivation {
			t.Errorf("sourceGenesisLedger[%q] = %d, below Soroban activation L%d — genesis cannot predate the platform",
				src, got, sorobanActivation)
		}
	}
}
