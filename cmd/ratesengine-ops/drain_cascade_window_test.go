package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestSplitCSV(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"blend", []string{"blend"}},
		{"blend,phoenix", []string{"blend", "phoenix"}},
		{"  blend , phoenix  ", []string{"blend", "phoenix"}},
		{",,,blend,,phoenix,,,", []string{"blend", "phoenix"}},
	}
	for _, tc := range cases {
		got := splitCSV(tc.in)
		if len(got) != len(tc.want) {
			t.Errorf("splitCSV(%q) = %v; want %v", tc.in, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("splitCSV(%q)[%d] = %q; want %q", tc.in, i, got[i], tc.want[i])
			}
		}
	}
}

func TestDrainPlanAll(t *testing.T) {
	t.Parallel()
	plan := drainPlan("")
	if len(plan) != 7 {
		t.Fatalf("drainPlan(empty) has %d steps; want 7", len(plan))
	}
	wantOrder := []string{
		"sep41-transfers", "cctp", "rozo", "soroswap-skim",
		"comet-liquidity", "blend", "phoenix",
	}
	for i, step := range plan {
		if step.source != wantOrder[i] {
			t.Errorf("drainPlan[%d].source = %q; want %q", i, step.source, wantOrder[i])
		}
		if step.run == nil {
			t.Errorf("drainPlan[%d].run is nil for %s", i, step.source)
		}
	}
}

func TestDrainPlanSubset(t *testing.T) {
	t.Parallel()
	plan := drainPlan("blend,phoenix")
	if len(plan) != 2 {
		t.Fatalf("drainPlan(subset) has %d steps; want 2", len(plan))
	}
	got := []string{plan[0].source, plan[1].source}
	want := []string{"blend", "phoenix"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("drainPlan[%d].source = %q; want %q", i, got[i], want[i])
		}
	}
}

func TestDrainPlanSubsetUnknown(t *testing.T) {
	t.Parallel()
	plan := drainPlan("blend,nonexistent,phoenix")
	if len(plan) != 2 {
		t.Fatalf("unknown source name should be silently dropped; got %d steps", len(plan))
	}
}

func TestWriteDrainReportTextEmptySources(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	writeDrainReportText(&buf, drainReport{
		FromLedger: 100, ToLedger: 200, DurationSec: 0,
	})
	out := buf.String()
	want := "drain-cascade-window: ledgers=[100, 200] dry_run=false halt_on_error=false total=0.0s failed=0/0\n"
	if out != want {
		t.Errorf("empty report text mismatch:\n got:  %q\n want: %q", out, want)
	}
}

func TestWriteDrainReportTextWithOKAndFail(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	writeDrainReportText(&buf, drainReport{
		FromLedger:  62642781,
		ToLedger:    62735517,
		DurationSec: 95.4,
		FailedCount: 1,
		Sources: []drainResult{
			{Source: "blend", Subcommand: "blend-backfill", DurationSec: 42.1, OK: true},
			{Source: "phoenix", Subcommand: "phoenix-backfill", DurationSec: 12.3, OK: false, Error: "boom"},
		},
	})
	out := buf.String()
	// Pin the exact operator-facing format so docs / runbook
	// copy-paste examples don't silently drift.
	mustContain := []string{
		"drain-cascade-window: ledgers=[62642781, 62735517]",
		"failed=1/2",
		"  OK  blend                    42.1s  blend-backfill",
		"FAIL  phoenix                  12.3s  phoenix-backfill",
		"         err: boom",
	}
	for _, want := range mustContain {
		if !strings.Contains(out, want) {
			t.Errorf("text output missing %q\nfull:\n%s", want, out)
		}
	}
}

func TestWriteDrainReportJSONShape(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	writeDrainReportJSON(&buf, drainReport{
		FromLedger:  62642781,
		ToLedger:    62735517,
		DryRun:      true,
		HaltOnError: false,
		DurationSec: 95.4,
		FailedCount: 1,
		Sources: []drainResult{
			{Source: "blend", Subcommand: "blend-backfill", DurationSec: 42.1, OK: true},
			{Source: "phoenix", Subcommand: "phoenix-backfill", DurationSec: 12.3, OK: false, Error: "boom"},
		},
	})
	var parsed map[string]any
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("json output is not valid JSON: %v", err)
	}
	mustHaveKeys := []string{
		"from_ledger", "to_ledger", "dry_run", "halt_on_error",
		"total_duration_seconds", "failed_count", "sources",
	}
	for _, k := range mustHaveKeys {
		if _, ok := parsed[k]; !ok {
			t.Errorf("json output missing key %q", k)
		}
	}
	srcs, ok := parsed["sources"].([]any)
	if !ok || len(srcs) != 2 {
		t.Fatalf("sources should be a 2-element array; got %v", parsed["sources"])
	}
	first, _ := srcs[0].(map[string]any)
	for _, k := range []string{"source", "subcommand", "duration_seconds", "ok"} {
		if _, ok := first[k]; !ok {
			t.Errorf("sources[0] missing key %q", k)
		}
	}
}

// fakeSubcommand replaces a real *Backfill func during tests of the
// orchestrator's per-step dispatch. We bypass the live drainPlan by
// running steps directly so the test doesn't need flag parsing /
// store wiring.
type fakeSubcommand struct {
	calls int
	err   error
}

func (f *fakeSubcommand) run(_ []string) error {
	f.calls++
	return f.err
}

func TestDrainPlanCallsEachStepOnce(t *testing.T) {
	t.Parallel()
	a, b := &fakeSubcommand{}, &fakeSubcommand{err: errors.New("nope")}
	plan := []drainStep{
		{source: "a", subcommand: "a-backfill", run: a.run},
		{source: "b", subcommand: "b-backfill", run: b.run},
	}
	for _, step := range plan {
		_ = step.run(nil)
	}
	if a.calls != 1 || b.calls != 1 {
		t.Errorf("expected 1 call each; got a=%d b=%d", a.calls, b.calls)
	}
}
