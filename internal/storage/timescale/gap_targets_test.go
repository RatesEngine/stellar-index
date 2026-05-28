package timescale

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestGapDetectorTargetsCoverAllPerSourceHypertables is the
// load-bearing lint guard for ADR-0030's per-source coverage
// invariant. Every per-source hypertable that ships in a
// migration MUST appear in [DefaultGapDetectorTargets] in the
// same PR — without this, a new source's table can land with no
// data-derived coverage signal at all (the exact failure mode
// F-0020 exhibited at the soroban_events layer).
//
// The test walks migrations/*.up.sql for `CREATE TABLE <name>`
// statements whose name matches the per-source naming
// conventions, then asserts each name is registered as a target
// (or appears in [excludedFromGapDetector] with a documented
// reason).
//
// Failure mode: the test prints the missing table and the
// shortest plausible target registration so the operator can
// copy-paste into per_source_gaps.go. This is intentional — a
// failing CI run should not require the author to also know the
// fix.
func TestGapDetectorTargetsCoverAllPerSourceHypertables(t *testing.T) {
	t.Parallel()

	migrations, err := filepath.Glob(findRepoRoot(t) + "/migrations/*.up.sql")
	if err != nil {
		t.Fatalf("glob migrations: %v", err)
	}
	if len(migrations) == 0 {
		t.Fatal("no migrations found — repo root resolution must be broken")
	}

	registered := make(map[string]bool, len(DefaultGapDetectorTargets))
	for _, target := range DefaultGapDetectorTargets {
		registered[target.Table] = true
	}

	createTablePattern := regexp.MustCompile(`(?m)^\s*CREATE\s+TABLE(?:\s+IF\s+NOT\s+EXISTS)?\s+([a-z][a-z0-9_]*)`)
	perSourcePattern := regexp.MustCompile(`_(events|liquidity|positions|emissions|admin|transfers|swaps|stake_events|supply_events|auctions)$`)

	for _, path := range migrations {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		matches := createTablePattern.FindAllStringSubmatch(string(body), -1)
		for _, m := range matches {
			table := m[1]
			if !perSourcePattern.MatchString(table) {
				continue
			}
			if excludedFromGapDetector[table] != "" {
				continue
			}
			if !registered[table] {
				t.Errorf(
					"per-source hypertable %q (in %s) is not registered in DefaultGapDetectorTargets.\n"+
						"Add to internal/storage/timescale/per_source_gaps.go:\n"+
						"  {Source: %q, Table: %q, LedgerColumn: \"ledger\"},\n"+
						"OR add to excludedFromGapDetector with a documented reason.",
					table, filepath.Base(path), inferSourceName(table), table,
				)
			}
		}
	}
}

// excludedFromGapDetector lists tables whose name matches the per-
// source pattern but which legitimately shouldn't be registered as
// a gap-detector target. Each entry MUST have a reason; "leftover
// from refactor" is not a valid reason — delete the entry or the
// table instead.
var excludedFromGapDetector = map[string]string{
	"freeze_events":    "system-state table, not per-source ingest. Populated on demand by /v1/admin/freeze handler; no continuous-coverage invariant.",
	"mev_events":       "MEV detection sidecar — populated only when an op's effects suggest sandwich/frontrun. Sparse-by-design, not a coverage signal.",
	"api_usage_events": "HTTP-request usage logging for the platform API, not Stellar-network ingest. No coverage invariant.",
}

// inferSourceName guesses the canonical source-label slug for a
// table by stripping the table-suffix and replacing underscores
// with hyphens. Used in the lint failure message to suggest a
// reasonable Source value; the engineer should still verify.
func inferSourceName(table string) string {
	base := table
	for _, suffix := range []string{
		"_events", "_liquidity", "_positions", "_emissions",
		"_admin", "_transfers", "_swaps", "_stake_events",
		"_supply_events", "_auctions",
	} {
		if strings.HasSuffix(base, suffix) {
			base = strings.TrimSuffix(base, suffix)
			// Re-suffix with the semantic name so the source label
			// disambiguates per-source-multi-table layouts like blend.
			tag := strings.TrimPrefix(suffix, "_")
			return strings.ReplaceAll(base, "_", "-") + "-" + tag
		}
	}
	return strings.ReplaceAll(base, "_", "-")
}

// findRepoRoot walks up from the test working directory until it
// finds a `go.mod` — the package's CWD during `go test` is the
// package dir, not the repo root, so the migrations/ glob needs
// help.
func findRepoRoot(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	dir := cwd
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("could not locate repo root (go.mod) starting from %s", cwd)
	return ""
}

func TestInferSourceName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		table string
		want  string
	}{
		{"sep41_transfers", "sep41-transfers"},
		{"blend_positions", "blend-positions"},
		{"phoenix_stake_events", "phoenix-stake-events"},
		{"soroban_events", "soroban-events"},
		{"cctp_events", "cctp-events"},
	}
	for _, tc := range cases {
		if got := inferSourceName(tc.table); got != tc.want {
			t.Errorf("inferSourceName(%q) = %q; want %q", tc.table, got, tc.want)
		}
	}
}
