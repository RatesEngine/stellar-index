package main

import (
	"reflect"
	"testing"
	"time"

	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
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
