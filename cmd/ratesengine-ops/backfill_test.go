package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeMinimalConfig drops a TOML config that has just enough fields
// for parseBackfillFlags + config.LoadWithEnv to succeed. The
// dispatcher / store / passphrase fields don't matter for these
// tests — we exit before opening any of them via -dry-run.
func writeMinimalConfig(t *testing.T, sources []string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "ratesengine.toml")
	body := `
[region]
id   = "r1"
name = "TestRegion"

[stellar]
network = "pubnet"

[storage]
postgres_dsn = "postgres://x:y@localhost/test?sslmode=disable"
s3_endpoint  = "http://127.0.0.1:9000"
s3_bucket_archive = "galexie-archive"
s3_bucket_live    = "galexie-live"
s3_region    = "r1"

[ingestion]
enabled_sources = ["` + strings.Join(sources, `","`) + `"]
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestBackfill_RejectsMissingFlags locks down the no-default-genesis
// guard. Operators have wiped the trades hypertable by typing
// `backfill -config PATH` without -from before — the flag is
// required so a fat-finger can't trigger a multi-day genesis replay.
func TestBackfill_RejectsMissingFlags(t *testing.T) {
	cfg := writeMinimalConfig(t, []string{"sdex"})
	cases := []struct {
		name       string
		args       []string
		wantSubstr string
	}{
		{"missing-config", []string{"-from", "100", "-to", "200"}, "-config required"},
		{"missing-from", []string{"-config", cfg, "-to", "200"}, "-from must be > 0"},
		{"to-equals-from", []string{"-config", cfg, "-from", "100", "-to", "100"}, "must be > -from"},
		{"to-below-from", []string{"-config", cfg, "-from", "100", "-to", "50"}, "must be > -from"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := parseBackfillFlags(tc.args)
			if err == nil {
				t.Fatalf("expected error containing %q; got nil", tc.wantSubstr)
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantSubstr)
			}
		})
	}
}

// TestBackfill_BackfillSafeGate is the load-bearing safety check.
// Every on-chain Soroban source defaults to BackfillSafe=false in
// the registry until its WASM-history audit lands. This test
// confirms the subcommand REFUSES to start when the operator's
// source list contains an unsafe source — and that the error
// message names the source so they know which audit to run.
func TestBackfill_BackfillSafeGate(t *testing.T) {
	cfg := writeMinimalConfig(t, []string{"comet", "sdex", "soroswap", "phoenix", "aquarius"})
	args := []string{"-config", cfg, "-from", "21000000", "-to", "21001000", "-dry-run"}
	_, _, err := parseBackfillFlags(args)
	if err == nil {
		t.Fatal("expected refusal — comet is BackfillSafe=false; got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "comet") {
		t.Errorf("error message should name comet (unsafe); got: %s", msg)
	}
	if strings.Contains(msg, "sdex") {
		t.Errorf("sdex is BackfillSafe=true; should NOT be in the unsafe list. got: %s", msg)
	}
	if strings.Contains(msg, "soroswap") {
		t.Errorf("soroswap is BackfillSafe=true (audited 2026-04-29); should NOT be in the unsafe list. got: %s", msg)
	}
	if strings.Contains(msg, "phoenix") {
		t.Errorf("phoenix is BackfillSafe=true (audited 2026-04-29); should NOT be in the unsafe list. got: %s", msg)
	}
	if strings.Contains(msg, "aquarius") {
		t.Errorf("aquarius is BackfillSafe=true (audited 2026-04-29); should NOT be in the unsafe list. got: %s", msg)
	}
	// Ops needs to know what to do next — error must point at the audit tool.
	if !strings.Contains(msg, "wasm-history") {
		t.Errorf("error should reference the wasm-history subcommand so ops can act; got: %s", msg)
	}
}

// TestBackfill_AllSafeSourcesAccepted confirms the gate doesn't
// false-positive: a list that's all BackfillSafe=true sources (sdex
// is the canonical pre-Soroban one + every off-chain CEX) reaches
// the dry-run output without complaint.
func TestBackfill_AllSafeSourcesAccepted(t *testing.T) {
	cfg := writeMinimalConfig(t, []string{"sdex"})
	args := []string{"-config", cfg, "-from", "21000000", "-to", "21001000", "-dry-run"}
	opts, _, err := parseBackfillFlags(args)
	if err != nil {
		t.Fatalf("expected acceptance for sdex-only list; got: %v", err)
	}
	if opts.from != 21000000 || opts.to != 21001000 {
		t.Errorf("opts.from/to mismatch: got [%d, %d]", opts.from, opts.to)
	}
	if !opts.dryRun {
		t.Error("opts.dryRun should be true")
	}
	if len(opts.sources) != 1 || opts.sources[0] != "sdex" {
		t.Errorf("opts.sources = %v, want [sdex]", opts.sources)
	}
}

// TestBackfill_SourceFlagOverridesConfig verifies the -source CSV
// overrides the config's enabled_sources, so an operator with
// `[soroswap, aquarius, sdex]` in the config can still backfill a
// subset (e.g. just sdex while the Soroban audits land).
func TestBackfill_SourceFlagOverridesConfig(t *testing.T) {
	cfg := writeMinimalConfig(t, []string{"comet", "sdex"})
	args := []string{"-config", cfg, "-from", "100", "-to", "200", "-source", "sdex", "-dry-run"}
	opts, _, err := parseBackfillFlags(args)
	if err != nil {
		t.Fatalf("override should let sdex-only through despite config having unsafe sources; got: %v", err)
	}
	if len(opts.sources) != 1 || opts.sources[0] != "sdex" {
		t.Errorf("opts.sources = %v, want [sdex]", opts.sources)
	}
}

// TestBackfill_BucketOverride verifies -bucket replaces the default
// cfg.Storage.S3BucketArchive. Useful for ad-hoc replays against a
// staging bucket without editing the live config.
func TestBackfill_BucketOverride(t *testing.T) {
	cfg := writeMinimalConfig(t, []string{"sdex"})
	args := []string{"-config", cfg, "-from", "100", "-to", "200", "-bucket", "scratch-bucket", "-dry-run"}
	opts, _, err := parseBackfillFlags(args)
	if err != nil {
		t.Fatal(err)
	}
	if opts.bucket != "scratch-bucket" {
		t.Errorf("opts.bucket = %q, want scratch-bucket", opts.bucket)
	}
}

// TestBackfillCursorSub_StableAcrossSourceOrder verifies the cursor
// key construction is deterministic regardless of how the operator
// types -source. Without sorting, `-source soroswap,sdex` and
// `-source sdex,soroswap` produce different cursor rows and a
// resume after a typo-driven re-order silently re-runs the whole
// range — exactly the failure mode -resume exists to prevent.
func TestBackfillCursorSub_StableAcrossSourceOrder(t *testing.T) {
	a := backfillOpts{from: 100, to: 200, sources: []string{"soroswap", "sdex", "binance"}}
	b := backfillOpts{from: 100, to: 200, sources: []string{"binance", "sdex", "soroswap"}}
	if backfillCursorSub(a) != backfillCursorSub(b) {
		t.Errorf("cursor sub depends on source-list order:\n  a=%q\n  b=%q",
			backfillCursorSub(a), backfillCursorSub(b))
	}
}

// TestBackfillCursorSub_DistinctRangesAndSources confirms that
// changing the range OR the source list produces a different
// cursor row — a different replay shouldn't share state with a
// previous one.
func TestBackfillCursorSub_DistinctRangesAndSources(t *testing.T) {
	base := backfillOpts{from: 100, to: 200, sources: []string{"sdex"}}

	cases := []struct {
		name string
		opts backfillOpts
	}{
		{"different-from", backfillOpts{from: 101, to: 200, sources: []string{"sdex"}}},
		{"different-to", backfillOpts{from: 100, to: 201, sources: []string{"sdex"}}},
		{"different-sources", backfillOpts{from: 100, to: 200, sources: []string{"binance"}}},
		{"extra-source", backfillOpts{from: 100, to: 200, sources: []string{"sdex", "binance"}}},
	}
	baseSub := backfillCursorSub(base)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := backfillCursorSub(tc.opts)
			if got == baseSub {
				t.Errorf("cursor sub collided with base: both = %q", got)
			}
		})
	}
}

// TestUnsafeBackfillSources_PureFunction is a unit-level check on
// the helper itself — proves we filter rather than fail-on-first.
// The error message in the gate test relies on getting the FULL
// unsafe list back, not just the first one.
func TestUnsafeBackfillSources_PureFunction(t *testing.T) {
	// 5 inputs: 1 unsafe Soroban, 1 SDEX (safe), 3 off-chain/audited (safe).
	got := unsafeBackfillSources([]string{"aquarius", "binance", "comet", "sdex", "kraken"})
	want := []string{"comet"}
	if len(got) != len(want) {
		t.Fatalf("unsafeBackfillSources returned %d entries, want %d: %v", len(got), len(want), got)
	}
	for i, s := range want {
		if got[i] != s {
			t.Errorf("entry %d: got %q, want %q", i, got[i], s)
		}
	}

	// Empty input → empty output (not nil-vs-empty drama in the
	// caller — caller does len() check).
	if out := unsafeBackfillSources(nil); len(out) != 0 {
		t.Errorf("empty input should return empty; got %v", out)
	}
}
