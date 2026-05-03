package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestParse_RealBacklog — the actual launch-readiness backlog
// MUST parse cleanly. Catches regressions where someone changes
// the table format in a way the parser can't follow.
func TestParse_RealBacklog(t *testing.T) {
	const path = "../../../docs/architecture/launch-readiness-backlog.md"
	rows, err := parseFile(path)
	if err != nil {
		t.Fatalf("parseFile: %v", err)
	}
	if len(rows) < 30 {
		t.Errorf("parsed %d rows; expected ≥ 30 — table format change?", len(rows))
	}
	// Every row should have a recognised status.
	for _, r := range rows {
		if r.Status == "?" {
			t.Errorf("row %s has unrecognised status", r.ID)
		}
	}
	// Every row should have a known surface prefix.
	for _, r := range rows {
		switch r.Surface {
		case "L1", "L2", "L3", "L4", "L5", "L6", "L7":
			// ok
		default:
			t.Errorf("row %s has unknown surface %q", r.ID, r.Surface)
		}
	}
}

// TestEngineeringReady_AllShipped — when every L1-L5 row is ✅,
// engineeringReady returns true (regardless of L6 status).
func TestEngineeringReady_AllShipped(t *testing.T) {
	rows := []Row{
		{ID: "L1.1", Status: "✅", Surface: "L1"},
		{ID: "L2.1", Status: "✅", Surface: "L2"},
		{ID: "L3.1", Status: "✅", Surface: "L3"},
		{ID: "L4.1", Status: "✅", Surface: "L4"},
		{ID: "L5.1", Status: "✅", Surface: "L5"},
		{ID: "L6.4", Status: "🔴", Surface: "L6"}, // operator-action-only — must not block
		{ID: "L7.1", Status: "⏳", Surface: "L7"},
	}
	if !engineeringReady(rows) {
		t.Error("engineeringReady should be true when L1-L5 are ✅, L6/L7 should be ignored")
	}
}

// TestEngineeringReady_OpsRunbookOK — 🟡 (operator-runbook-ready)
// is acceptable for L4/L5 but NOT for L1-L3.
func TestEngineeringReady_OpsRunbookOK(t *testing.T) {
	t.Run("L4_yellow_ok", func(t *testing.T) {
		rows := []Row{{ID: "L4.11", Status: "🟡", Surface: "L4"}}
		if !engineeringReady(rows) {
			t.Error("L4 with 🟡 should be ready (operator-runbook gated)")
		}
	})
	t.Run("L3_yellow_blocks", func(t *testing.T) {
		rows := []Row{{ID: "L3.5", Status: "🟡", Surface: "L3"}}
		if engineeringReady(rows) {
			t.Error("L3 with 🟡 should NOT be ready — engineering tier requires ✅/⚠")
		}
	})
}

// TestEngineeringReady_CaveatOK — ⚠ (shipped-with-caveat) is
// acceptable across all engineering + ops tiers.
func TestEngineeringReady_CaveatOK(t *testing.T) {
	rows := []Row{
		{ID: "L2.2", Status: "⚠", Surface: "L2"},
		{ID: "L5.4", Status: "⚠", Surface: "L5"},
	}
	if !engineeringReady(rows) {
		t.Error("⚠ should count as ready in both engineering and ops tiers")
	}
}

// TestEngineeringReady_GreenBlocks — 🟢 (in flight) blocks every
// engineering + ops tier.
func TestEngineeringReady_GreenBlocks(t *testing.T) {
	rows := []Row{{ID: "L3.9", Status: "🟢", Surface: "L3"}}
	if engineeringReady(rows) {
		t.Error("🟢 in L3 should block")
	}
}

// TestNormaliseStatus — picks the first known emoji from the column.
func TestNormaliseStatus(t *testing.T) {
	cases := map[string]string{
		"✅":           "✅",
		"🟢":           "🟢",
		"⚠":           "⚠",
		"🟡 designed":  "🟡",
		"shipped ✅":   "✅",
		"random text": "?",
		"":            "?",
	}
	for in, want := range cases {
		if got := normaliseStatus(in); got != want {
			t.Errorf("normaliseStatus(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestParse_HandlesPipesInDescription — descriptions can contain
// `|` characters (e.g. backtick-wrapped paths or markdown links).
// The status is always the LAST non-empty cell, so even a row
// where the description wraps multiple `|` should land the right
// status.
func TestParse_HandlesPipesInDescription(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "backlog.md")
	body := "# Header\n\n" +
		"| ID | Item | Status |\n" +
		"|---|---|---|\n" +
		"| L1.1 | Description with `|` pipe-ish | ✅ |\n" +
		"| L2.1 | Plain description | 🟢 |\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	rows, err := parseFile(path)
	if err != nil {
		t.Fatalf("parseFile: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if rows[0].Status != "✅" || rows[1].Status != "🟢" {
		t.Errorf("statuses = [%q, %q], want [✅, 🟢]", rows[0].Status, rows[1].Status)
	}
}

// TestSurfaceFor — extracts the first two characters as the surface.
func TestSurfaceFor(t *testing.T) {
	cases := map[string]string{
		"L1.1":   "L1",
		"L2.12a": "L2",
		"L7.7":   "L7",
		"":       "",
		"?":      "",
	}
	for in, want := range cases {
		if got := surfaceFor(in); got != want {
			t.Errorf("surfaceFor(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestSurfaceReadiness_NamesBlocker — when a row blocks, the
// returned reason names the row ID so the operator knows which
// to chase.
func TestSurfaceReadiness_NamesBlocker(t *testing.T) {
	rows := []Row{
		{ID: "L3.5", Status: "🟢", Surface: "L3"},
	}
	ready, reason := surfaceReadiness("L3", rows)
	if ready {
		t.Fatal("expected not ready")
	}
	if !strings.Contains(reason, "L3.5") {
		t.Errorf("reason should name L3.5; got %q", reason)
	}
}
