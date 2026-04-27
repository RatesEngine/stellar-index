package main

import (
	"testing"
)

// TestDiffLedgerCounts_AllAgree is the green-pass case: identical
// counts on both sides return zero diffs. No allocation, no fanfare.
func TestDiffLedgerCounts_AllAgree(t *testing.T) {
	ours := map[uint32]int{100: 5, 101: 2, 102: 0}
	theirs := map[uint32]int{100: 5, 101: 2, 102: 0}
	if got := diffLedgerCounts(ours, theirs); len(got) != 0 {
		t.Errorf("expected zero diffs on identical maps, got %d: %+v", len(got), got)
	}
}

// TestDiffLedgerCounts_Missing is the most common alert path:
// we're missing rows on a ledger Hubble has data for. Catches
// decoder coverage gaps — exactly the failure mode this tool exists
// to detect.
func TestDiffLedgerCounts_Missing(t *testing.T) {
	ours := map[uint32]int{100: 5}
	theirs := map[uint32]int{100: 7}
	got := diffLedgerCounts(ours, theirs)
	if len(got) != 1 {
		t.Fatalf("expected 1 diff, got %d", len(got))
	}
	if got[0].Ours != 5 || got[0].Theirs != 7 {
		t.Errorf("got %+v, want {100, 5, 7}", got[0])
	}
}

// TestDiffLedgerCounts_Extra is the rarer-but-still-real case: we
// emitted MORE rows than Hubble. Either we're decoding events
// that aren't trades, or we're double-counting something. Either
// way it's a bug.
func TestDiffLedgerCounts_Extra(t *testing.T) {
	ours := map[uint32]int{200: 9}
	theirs := map[uint32]int{200: 4}
	got := diffLedgerCounts(ours, theirs)
	if len(got) != 1 {
		t.Fatalf("expected 1 diff, got %d", len(got))
	}
	if got[0].Ours != 9 || got[0].Theirs != 4 {
		t.Errorf("got %+v, want {200, 9, 4}", got[0])
	}
}

// TestDiffLedgerCounts_AbsenceIsZero confirms the sparse-map
// semantics: a ledger present on one side and absent on the other
// is treated as count=0 on the absent side, NOT as "skip". A
// ledger where Hubble has 3 rows and we have 0 is a divergence we
// must report — that's the decoder-skipping-an-entire-shape case.
func TestDiffLedgerCounts_AbsenceIsZero(t *testing.T) {
	ours := map[uint32]int{}
	theirs := map[uint32]int{500: 3}
	got := diffLedgerCounts(ours, theirs)
	if len(got) != 1 {
		t.Fatalf("expected 1 diff (absence ≡ zero on missing key), got %d", len(got))
	}
	if got[0].Ledger != 500 || got[0].Ours != 0 || got[0].Theirs != 3 {
		t.Errorf("got %+v, want {500, 0, 3}", got[0])
	}

	// And the symmetric case: we have rows for a ledger Hubble doesn't.
	got = diffLedgerCounts(map[uint32]int{600: 2}, map[uint32]int{})
	if len(got) != 1 || got[0].Ledger != 600 || got[0].Ours != 2 || got[0].Theirs != 0 {
		t.Errorf("symmetric absence: got %+v, want {600, 2, 0}", got)
	}
}

// TestDiffLedgerCounts_SortedByLedger locks down the report
// ordering. The diff list goes through reportLedgerDiffs which
// caps + writes to stderr — operators expect to see ledgers in
// ascending order so the report can be diffed across runs.
// Without sorting, map iteration order makes the output
// non-deterministic and hard to grep.
func TestDiffLedgerCounts_SortedByLedger(t *testing.T) {
	ours := map[uint32]int{300: 1, 100: 1, 200: 1}
	theirs := map[uint32]int{300: 9, 100: 9, 200: 9}
	got := diffLedgerCounts(ours, theirs)
	if len(got) != 3 {
		t.Fatalf("expected 3 diffs, got %d", len(got))
	}
	for i, want := range []uint32{100, 200, 300} {
		if got[i].Ledger != want {
			t.Errorf("position %d: got ledger=%d, want %d (output must be sorted ascending)",
				i, got[i].Ledger, want)
		}
	}
}

// TestDiffLedgerCounts_BothEmpty covers the edge case: no data
// either side. This happens on a -from -to range that's outside
// the ingested window. NOT an error condition (no divergence to
// report); the operator gets an "all clear" signal and can move on.
func TestDiffLedgerCounts_BothEmpty(t *testing.T) {
	if got := diffLedgerCounts(map[uint32]int{}, map[uint32]int{}); len(got) != 0 {
		t.Errorf("empty inputs should yield empty output, got %+v", got)
	}
	if got := diffLedgerCounts(nil, nil); len(got) != 0 {
		t.Errorf("nil inputs should yield empty output, got %+v", got)
	}
}

// TestHubbleCheck_FlagValidation exercises the argv-parsing guards.
// We don't load config or hit BigQuery — those run later in
// hubbleCheck() — so this test just stops at the flag-validation
// boundary. Same fail-loud philosophy as backfill: refuse to run
// on missing required flags rather than guessing defaults.
func TestHubbleCheck_FlagValidation(t *testing.T) {
	cases := []struct {
		name       string
		args       []string
		wantSubstr string
	}{
		{"missing-config", []string{"-from", "100", "-to", "200", "-bigquery-project", "p"}, "-config required"},
		{"missing-from", []string{"-config", "/dev/null", "-to", "200", "-bigquery-project", "p"}, "-from must be > 0"},
		{"to-equals-from", []string{"-config", "/dev/null", "-from", "100", "-to", "100", "-bigquery-project", "p"}, "must be > -from"},
		{"missing-project", []string{"-config", "/dev/null", "-from", "100", "-to", "200"}, "-bigquery-project required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := hubbleCheck(tc.args)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantSubstr)
			}
			if !contains(err.Error(), tc.wantSubstr) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantSubstr)
			}
		})
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
