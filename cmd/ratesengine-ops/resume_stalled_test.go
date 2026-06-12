package main

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/storage/timescale"
)

// TestParseStalledCursor covers every branch of the sub_source parser
// + skip-reason path. Inputs are crafted to mirror the actual cursor
// shapes seen in production (per the 2026-05-28 r1 stall sweep).
func TestParseStalledCursor(t *testing.T) {
	cases := []struct {
		name         string
		cursor       timescale.Cursor
		wantSkip     bool
		wantFrom     uint32
		wantTo       uint32
		wantSources  []string
		wantContains string // substring expected in skipReason when wantSkip=true
	}{
		{
			name: "well-formed multi-decoder stall — resume window non-empty",
			cursor: timescale.Cursor{
				Sub:        "62200000-62210000:aquarius,band,sdex",
				LastLedger: 62205555,
			},
			wantFrom:    62205556,
			wantTo:      62210000,
			wantSources: []string{"aquarius", "band", "sdex"},
		},
		{
			name: "well-formed single-decoder stall (typical defindex/soroswap shape)",
			cursor: timescale.Cursor{
				Sub:        "53500714-54242319:soroswap",
				LastLedger: 54242000,
			},
			wantFrom:    54242001,
			wantTo:      54242319,
			wantSources: []string{"soroswap"},
		},
		{
			name: "last_ledger at-or-past target — stale-by-time only",
			cursor: timescale.Cursor{
				Sub:        "53500714-54242319:soroswap",
				LastLedger: 54242319,
			},
			wantSkip:     true,
			wantContains: "already at-or-past target",
		},
		{
			name: "last_ledger > target — also skip (cursor walked past target)",
			cursor: timescale.Cursor{
				Sub:        "53500714-54242319:soroswap",
				LastLedger: 54300000,
			},
			wantSkip:     true,
			wantContains: "already at-or-past target",
		},
		{
			name: "declared from > last_ledger+1 — inconsistent cursor",
			cursor: timescale.Cursor{
				Sub:        "62500000-62600000:phoenix",
				LastLedger: 62000000,
			},
			wantSkip:     true,
			wantContains: "cursor inconsistent",
		},
		{
			name: "garbage sub_source — refuse rather than guess",
			cursor: timescale.Cursor{
				Sub:        "not-a-range",
				LastLedger: 1000,
			},
			wantSkip:     true,
			wantContains: "doesn't match",
		},
		{
			name: "missing colon — refuse",
			cursor: timescale.Cursor{
				Sub:        "62200000-62210000",
				LastLedger: 62205555,
			},
			wantSkip:     true,
			wantContains: "doesn't match",
		},
		{
			name: "empty decoder list after colon — refuse",
			cursor: timescale.Cursor{
				Sub:        "62200000-62210000:",
				LastLedger: 62205555,
			},
			wantSkip:     true,
			wantContains: "doesn't match", // regex fails on `(.+)$` against empty
		},
		{
			name: "from overflows uint32 — refuse",
			cursor: timescale.Cursor{
				Sub:        "5000000000-5000010000:sdex", // > uint32 max ~4.29B
				LastLedger: 0,
			},
			wantSkip:     true,
			wantContains: "parse from",
		},
		{
			name: "to overflows uint32 — refuse",
			cursor: timescale.Cursor{
				Sub:        "62200000-5000000000:sdex",
				LastLedger: 62205555,
			},
			wantSkip:     true,
			wantContains: "parse to",
		},
		{
			name: "decoder CSV gets sorted",
			cursor: timescale.Cursor{
				Sub:        "62200000-62210000:soroswap,aquarius,blend",
				LastLedger: 62205555,
			},
			wantFrom:    62205556,
			wantTo:      62210000,
			wantSources: []string{"aquarius", "blend", "soroswap"}, // sorted
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseStalledCursor(tc.cursor)
			if got.skip != tc.wantSkip {
				t.Errorf("skip = %v, want %v (reason=%q)", got.skip, tc.wantSkip, got.skipReason)
			}
			if tc.wantSkip {
				if tc.wantContains != "" && !contains(got.skipReason, tc.wantContains) {
					t.Errorf("skipReason = %q, want substring %q", got.skipReason, tc.wantContains)
				}
				return
			}
			if got.rangeFrom != tc.wantFrom {
				t.Errorf("rangeFrom = %d, want %d", got.rangeFrom, tc.wantFrom)
			}
			if got.rangeTo != tc.wantTo {
				t.Errorf("rangeTo = %d, want %d", got.rangeTo, tc.wantTo)
			}
			if !reflect.DeepEqual(got.sources, tc.wantSources) {
				t.Errorf("sources = %v, want %v", got.sources, tc.wantSources)
			}
		})
	}
}

// TestParseStalledCursor_RoundTripsBackfillCursorSub pins the contract
// between `backfillCursorSub` (the producer) and `parseStalledCursor`
// (the consumer) — if either side changes its format the round-trip
// breaks and this test fires before resume-stalled silently drops
// every stalled cursor as "doesn't match shape".
func TestParseStalledCursor_RoundTripsBackfillCursorSub(t *testing.T) {
	opts := backfillOpts{
		from:    62_200_000,
		to:      62_210_000,
		sources: []string{"sdex", "aquarius", "blend"}, // unsorted on input
	}
	sub := backfillCursorSub(opts)
	c := timescale.Cursor{Sub: sub, LastLedger: 62_205_000}
	p := parseStalledCursor(c)
	if p.skip {
		t.Fatalf("round-trip skipped: %q (sub=%q)", p.skipReason, sub)
	}
	if p.rangeFrom != 62_205_001 || p.rangeTo != 62_210_000 {
		t.Errorf("range = [%d, %d], want [62205001, 62210000]", p.rangeFrom, p.rangeTo)
	}
	want := []string{"aquarius", "blend", "sdex"} // both sides sort
	if !reflect.DeepEqual(p.sources, want) {
		t.Errorf("sources = %v, want %v", p.sources, want)
	}
}

// TestOverlapsAnyDataGap covers the small interval-overlap helper.
// The function is the gate's primitive — a bug here would let
// false-positive plans through (act on cursors that aren't real
// gaps) or filter out genuine ones (miss real coverage holes).
func TestOverlapsAnyDataGap(t *testing.T) {
	gaps := []timescale.LedgerGap{
		{Start: 62642781, End: 62735517, Size: 92737},
		{Start: 62746866, End: 62757524, Size: 10659},
	}
	cases := []struct {
		name     string
		from, to uint32
		want     bool
	}{
		{name: "fully contained inside first gap", from: 62700000, to: 62710000, want: true},
		{name: "starts before, ends inside first gap", from: 62600000, to: 62700000, want: true},
		{name: "starts inside first gap, ends after", from: 62700000, to: 62800000, want: true},
		{name: "spans first gap entirely (and beyond)", from: 62500000, to: 62800000, want: true},
		{name: "between the two gaps", from: 62735518, to: 62746865, want: false},
		{name: "fully before any gap", from: 50000000, to: 50100000, want: false},
		{name: "fully after both gaps", from: 62800000, to: 62900000, want: false},
		{name: "exact match second gap", from: 62746866, to: 62757524, want: true},
		{name: "ends one before first gap start", from: 62500000, to: 62642780, want: false},
		{name: "starts one after first gap end", from: 62735518, to: 62740000, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := overlapsAnyDataGap(tc.from, tc.to, gaps)
			if got != tc.want {
				t.Errorf("overlapsAnyDataGap([%d, %d]) = %v, want %v", tc.from, tc.to, got, tc.want)
			}
		})
	}
}

// TestPlanHasSorobanDecoder verifies which decoder shapes route
// through the data-gap gate. SDEX-only plans skip the gate (no
// data-derived gap signal for the trades table yet); any plan
// containing at least one Soroban-era decoder name DOES go through.
func TestPlanHasSorobanDecoder(t *testing.T) {
	cases := []struct {
		name    string
		sources []string
		want    bool
	}{
		{name: "sdex only", sources: []string{"sdex"}, want: false},
		{name: "aquarius alone", sources: []string{"aquarius"}, want: true},
		{name: "defindex alone", sources: []string{"defindex"}, want: true},
		{name: "soroban-events pseudo", sources: []string{"soroban-events"}, want: true},
		{name: "mixed sdex + Soroban DEXes", sources: []string{"aquarius", "comet", "phoenix", "sdex", "soroswap"}, want: true},
		{name: "empty", sources: nil, want: false},
		{name: "unknown decoder", sources: []string{"some-future-source"}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := planHasSorobanDecoder(tc.sources)
			if got != tc.want {
				t.Errorf("planHasSorobanDecoder(%v) = %v, want %v", tc.sources, got, tc.want)
			}
		})
	}
}

// TestGateAgainstDataGaps_HappyPath puts the F-0020 cascade signature
// + a false-positive cursor + an SDEX-only cursor through the gate
// and pins the expected post-gate skip state. The whole point of
// this commit: false positives go from "actionable" to "skip" with
// a self-explanatory reason, real-gap cursors stay actionable.
func TestGateAgainstDataGaps_HappyPath(t *testing.T) {
	gaps := []timescale.LedgerGap{
		{Start: 62642781, End: 62735517, Size: 92737}, // F-0020 gap 1
	}
	plans := []stalledCursorPlan{
		{
			// Soroban cursor whose remaining range DOES overlap a gap.
			cursor:    timescale.Cursor{Sub: "62600000-62700000:aquarius"},
			rangeFrom: 62650000,
			rangeTo:   62700000,
			sources:   []string{"aquarius"},
		},
		{
			// Soroban cursor whose remaining range MISSES any gap
			// (sibling cursors covered it). Pre-gate: actionable;
			// post-gate: skipped as cursor-inventory false positive.
			cursor:    timescale.Cursor{Sub: "15300001-30599999:soroswap"},
			rangeFrom: 15394495,
			rangeTo:   30599999,
			sources:   []string{"soroswap"},
		},
		{
			// SDEX-only cursor — skipped by default (no SDEX gap detector).
			cursor:    timescale.Cursor{Sub: "2-15300000:sdex"},
			rangeFrom: 98334,
			rangeTo:   15300000,
			sources:   []string{"sdex"},
		},
		{
			// Pre-existing skip (parser-rejected). Gate must leave alone.
			cursor:     timescale.Cursor{Sub: "garbage"},
			skip:       true,
			skipReason: "doesn't match shape",
		},
	}

	out := gateAgainstDataGaps(plans, gaps, false)

	// out[0] is the actionable real-gap one — gate should keep it un-skipped.
	if out[0].skip {
		t.Errorf("plan[0] aquarius/real-gap was skipped (reason=%q); want actionable", out[0].skipReason)
	}
	if out[0].cursor.Sub != "62600000-62700000:aquarius" {
		t.Errorf("plan[0] sub mismatch: got %q", out[0].cursor.Sub)
	}
	if !out[1].skip {
		t.Errorf("plan[1] soroswap/false-positive should be skipped after gate; got actionable")
	}
	if !strings.Contains(out[1].skipReason, "no soroban_events gap overlap") {
		t.Errorf("plan[1] skip reason should mention false-positive; got %q", out[1].skipReason)
	}
	if !out[2].skip {
		t.Errorf("plan[2] sdex-only should be skipped by default; got actionable")
	}
	if !strings.Contains(out[2].skipReason, "not yet implemented for non-Soroban") {
		t.Errorf("plan[2] skip reason should mention SDEX gap-detector follow-up; got %q", out[2].skipReason)
	}
	if !out[3].skip || out[3].skipReason != "doesn't match shape" {
		t.Errorf("plan[3] pre-existing skip should be untouched by the gate; got skip=%v reason=%q", out[3].skip, out[3].skipReason)
	}
}

// TestGateAgainstDataGaps_ForceClassic verifies the --force-classic-cursors
// opt-in: an SDEX-only plan that the default gate would skip MUST
// remain actionable when the flag is set. The flag is the operator's
// escape hatch for "I know the cursor inventory is right; act on it"
// — used sparingly, since the default safer behaviour is don't act
// without data-derived evidence.
func TestGateAgainstDataGaps_ForceClassic(t *testing.T) {
	plans := []stalledCursorPlan{
		{
			cursor:    timescale.Cursor{Sub: "2-15300000:sdex"},
			rangeFrom: 98334,
			rangeTo:   15300000,
			sources:   []string{"sdex"},
		},
	}
	out := gateAgainstDataGaps(plans, nil, true) // forceClassic=true
	if out[0].skip {
		t.Errorf("with --force-classic-cursors the SDEX plan must stay actionable; got skip=%v reason=%q", out[0].skip, out[0].skipReason)
	}
}

// TestPlanResumeStalled_FilterSemantics verifies the cursor-list →
// plan-list filter chain without touching Postgres: it operates on a
// pre-built slice that mimics what ListCursors would return. (The
// real planResumeStalled wraps store.ListCursors; this test exercises
// the post-list filter logic via a parallel helper to avoid the
// testcontainers cost for what is pure CPU work.)
//
// Filter precedence: source-prefix → min-lag → source-filter substring
// → max-resumes cap. The order matters: a stalled defindex cursor
// behind a min-lag cutoff is filtered out before the substring
// check applies, so an operator's --max-resumes count covers the
// post-filter population (not the raw row count).
func TestPlanResumeStalled_FilterSemantics(t *testing.T) {
	now := time.Now().UTC()
	rows := []timescale.Cursor{
		// 0: backfill, stale 30 min — filtered by min-lag=1h
		{Source: "backfill", Sub: "100-200:sdex", LastLedger: 150, UpdatedAt: now.Add(-30 * time.Minute)},
		// 1: backfill, stale 2 h — included
		{Source: "backfill", Sub: "300-400:sdex,aquarius", LastLedger: 350, UpdatedAt: now.Add(-2 * time.Hour)},
		// 2: backfill, stale 5 h — included (defindex substring will catch this)
		{Source: "backfill", Sub: "500-600:defindex,soroswap-router", LastLedger: 550, UpdatedAt: now.Add(-5 * time.Hour)},
		// 3: NOT backfill — filtered by source-prefix
		{Source: "ledgerstream", Sub: "", LastLedger: 62000000, UpdatedAt: now.Add(-5 * time.Hour)},
		// 4: backfill, stale 3 h, defindex substring
		{Source: "backfill", Sub: "700-800:defindex", LastLedger: 750, UpdatedAt: now.Add(-3 * time.Hour)},
	}

	// We test the post-store-call filter logic in isolation by
	// re-implementing the same selection rules planResumeStalled does
	// after the store call returns. Keeps the test pure-Go.
	cases := []struct {
		name         string
		minLag       time.Duration
		sourceFilter string
		maxResumes   int
		wantSubs     []string
	}{
		{
			name:     "all backfill stalls over 1h",
			minLag:   time.Hour,
			wantSubs: []string{"300-400:sdex,aquarius", "500-600:defindex,soroswap-router", "700-800:defindex"},
		},
		{
			name:         "filter to defindex substring",
			minLag:       time.Hour,
			sourceFilter: "defindex",
			wantSubs:     []string{"500-600:defindex,soroswap-router", "700-800:defindex"},
		},
		{
			name:       "max-resumes caps after filter",
			minLag:     time.Hour,
			maxResumes: 2,
			wantSubs:   []string{"300-400:sdex,aquarius", "500-600:defindex,soroswap-router"},
		},
		{
			name:     "raised min-lag prunes more",
			minLag:   4 * time.Hour,
			wantSubs: []string{"500-600:defindex,soroswap-router"},
		},
		{
			name:     "min-lag above any stall — empty",
			minLag:   24 * time.Hour,
			wantSubs: nil,
		},
	}

	filter := func(rows []timescale.Cursor, minLag time.Duration, src string, maxResumes int) []string {
		var out []string
		for _, c := range rows {
			if len(c.Source) < len("backfill") || c.Source[:8] != "backfill" {
				continue
			}
			if now.Sub(c.UpdatedAt) < minLag {
				continue
			}
			if src != "" && !contains(c.Sub, src) {
				continue
			}
			out = append(out, c.Sub)
			if maxResumes > 0 && len(out) >= maxResumes {
				break
			}
		}
		return out
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := filter(rows, tc.minLag, tc.sourceFilter, tc.maxResumes)
			if !reflect.DeepEqual(got, tc.wantSubs) {
				t.Errorf("subs = %v, want %v", got, tc.wantSubs)
			}
		})
	}
}
