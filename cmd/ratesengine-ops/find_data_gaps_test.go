package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/storage/timescale"
)

// TestFindDataGapsReport_TotalMissingLedgers pins the simple sum that
// the subcommand reports. Operators reading the dashboard look at
// this number first to decide whether the gap is structurally
// significant or just no-Soroban-activity noise.
func TestFindDataGapsReport_TotalMissingLedgers(t *testing.T) {
	cases := []struct {
		name string
		gaps []timescale.LedgerGap
		want int64
	}{
		{name: "no gaps", gaps: nil, want: 0},
		{name: "one gap", gaps: []timescale.LedgerGap{{Start: 100, End: 200, Size: 101}}, want: 101},
		{
			name: "two gaps — the 2026-05-26 cascade signature",
			gaps: []timescale.LedgerGap{
				{Start: 62642781, End: 62735517, Size: 92737},
				{Start: 62746866, End: 62757524, Size: 10659},
			},
			want: 103396,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := findDataGapsReport{Gaps: tc.gaps}
			for _, g := range r.Gaps {
				r.TotalMissingLedgers += g.Size
			}
			if r.TotalMissingLedgers != tc.want {
				t.Errorf("total = %d, want %d", r.TotalMissingLedgers, tc.want)
			}
		})
	}
}

// TestWriteFindDataGapsText_NoGaps verifies the operator-friendly
// path when the scanned target is gap-free — the message has to be
// unambiguous so an operator scanning logs doesn't assume the
// subcommand silently no-op'd.
func TestWriteFindDataGapsText_NoGaps(t *testing.T) {
	out := captureStdout(t, func() {
		writeFindDataGapsText(findDataGapsReport{
			ScannedAt:  time.Now().UTC(),
			Source:     "soroban-events",
			Table:      "soroban_events",
			MinGapSize: 1000,
			FromLedger: 0,
			ToLedger:   62763483,
		})
	})
	if !strings.Contains(out, "ledgers=[0, 62763483]") {
		t.Errorf("missing scan summary; got %q", out)
	}
	if !strings.Contains(out, "no gaps found") {
		t.Errorf("missing clean-coverage signal; got %q", out)
	}
}

// TestWriteFindDataGapsText_WithGaps pins the operator-facing format
// including the suggested backfill commands. The format is part of
// the subcommand's contract — operators may grep for `ratesengine-
// ops backfill` lines in the output and pipe them into bash.
func TestWriteFindDataGapsText_WithGaps(t *testing.T) {
	r := findDataGapsReport{
		ScannedAt:  time.Now().UTC(),
		Source:     "soroban-events",
		Table:      "soroban_events",
		MinGapSize: 1000,
		FromLedger: 0,
		ToLedger:   62763483,
		Gaps: []timescale.LedgerGap{
			{Start: 62642781, End: 62735517, Size: 92737},
			{Start: 62746866, End: 62757524, Size: 10659},
		},
		TotalMissingLedgers: 103396,
	}
	out := captureStdout(t, func() {
		writeFindDataGapsText(r)
	})
	want := []string{
		"source=soroban-events table=soroban_events",
		"ledgers=[0, 62763483] min_gap_size=1000",
		"2 gap(s), totalling 103396 missing ledgers",
		"[62642781, 62735517]  size=92737",
		"[62746866, 62757524]  size=10659",
		"ratesengine-ops backfill --config /etc/ratesengine.toml --from 62642781 --to 62735517 --source soroban-events",
		"ratesengine-ops backfill --config /etc/ratesengine.toml --from 62746866 --to 62757524 --source soroban-events",
	}
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("output missing %q; got:\n%s", w, out)
		}
	}
}

// TestWriteFindDataGapsJSON_Shape pins the JSON contract so a future
// rename of LedgerGap or its JSON tags surfaces here. Operator
// scripts piping into `jq '.gaps[] | "\(.start) \(.end)"'`
// depend on the lowercase keys.
func TestWriteFindDataGapsJSON_Shape(t *testing.T) {
	multi := findDataGapsMultiReport{
		ScannedAt: time.Date(2026, 5, 28, 18, 29, 50, 0, time.UTC),
		Reports: []findDataGapsReport{
			{
				ScannedAt:  time.Date(2026, 5, 28, 18, 29, 50, 0, time.UTC),
				Source:     "soroban-events",
				Table:      "soroban_events",
				MinGapSize: 1000,
				FromLedger: 0,
				ToLedger:   62763483,
				Gaps: []timescale.LedgerGap{
					{Start: 62642781, End: 62735517, Size: 92737},
				},
				TotalMissingLedgers: 92737,
			},
		},
	}
	out := captureStdout(t, func() {
		if err := writeFindDataGapsJSON(multi); err != nil {
			t.Fatal(err)
		}
	})
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	for _, k := range []string{"scanned_at", "reports"} {
		if _, ok := parsed[k]; !ok {
			t.Errorf("missing top-level key %q in %v", k, parsed)
		}
	}
	reports, ok := parsed["reports"].([]any)
	if !ok || len(reports) != 1 {
		t.Fatalf("reports shape unexpected: %v", parsed["reports"])
	}
	firstReport, _ := reports[0].(map[string]any)
	for _, k := range []string{"source", "table", "min_gap_size", "from_ledger", "to_ledger", "gaps", "total_missing_ledgers"} {
		if _, ok := firstReport[k]; !ok {
			t.Errorf("missing per-report key %q in %v", k, firstReport)
		}
	}
	gaps, ok := firstReport["gaps"].([]any)
	if !ok || len(gaps) != 1 {
		t.Fatalf("gaps shape unexpected: %v", firstReport["gaps"])
	}
	first, ok := gaps[0].(map[string]any)
	if !ok {
		t.Fatalf("gap[0] shape unexpected: %v", gaps[0])
	}
	for _, k := range []string{"start", "end", "size"} {
		if _, ok := first[k]; !ok {
			t.Errorf("missing gap key %q in %v", k, first)
		}
	}
}

func TestResolveFindDataGapsTargets(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		source    string
		wantCount int
		wantErr   bool
	}{
		{name: "all sources", source: "all", wantCount: len(timescale.DefaultGapDetectorTargets)},
		{name: "empty defaults to all", source: "", wantCount: len(timescale.DefaultGapDetectorTargets)},
		{name: "single existing source", source: "blend-positions", wantCount: 1},
		{name: "two existing sources", source: "blend-positions,phoenix-liquidity", wantCount: 2},
		{name: "with whitespace", source: " blend-positions , phoenix-liquidity ", wantCount: 2},
		{name: "unknown source fails", source: "bogus", wantErr: true},
		{name: "partial unknown fails fast", source: "blend-positions,bogus", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := resolveFindDataGapsTargets(tc.source)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
			if !tc.wantErr && len(out) != tc.wantCount {
				t.Errorf("got %d targets; want %d", len(out), tc.wantCount)
			}
		})
	}
}

// captureStdout runs f with os.Stdout redirected to a pipe and
// returns whatever was written. Lets us test the text/json writers
// without exposing them to take an io.Writer (a future-proofing
// refactor we can do separately).
func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	saved := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan struct{})
	var buf bytes.Buffer
	go func() {
		_, _ = io.Copy(&buf, r)
		close(done)
	}()
	f()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	<-done
	os.Stdout = saved
	return buf.String()
}
