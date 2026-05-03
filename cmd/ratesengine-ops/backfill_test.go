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
// TestBackfill_AllSorobanSourcesPass confirms the gate accepts
// every audited on-chain Soroban source. As of 2026-04-29 all 8
// sources (soroswap, phoenix, aquarius, comet, reflector-{dex,cex,
// fx}, redstone, band) have completed their WASM-history audits
// (see docs/operations/wasm-audits/) and should pass cleanly.
//
// The gate-rejects-unsafe path is unit-tested in
// TestUnsafeBackfillSources_PureFunction below using a synthetic
// source name (which bypasses config validation) — that's where
// the regression coverage for "an unaudited source must be refused"
// lives. This test is the positive-side counterpart.
func TestBackfill_AllSorobanSourcesPass(t *testing.T) {
	// Use the DEX subset — oracle sources need oracle.* contract
	// IDs in config which writeMinimalConfig doesn't populate.
	// (Oracle sources also flipped 2026-04-29; the gate logic is
	// the same.)
	cfg := writeMinimalConfig(t, []string{"soroswap", "phoenix", "aquarius", "comet", "sdex"})
	args := []string{"-config", cfg, "-from", "21000000", "-to", "21001000", "-dry-run"}
	_, _, err := parseBackfillFlags(args)
	if err != nil {
		t.Fatalf("expected acceptance — every Soroban DEX source has been audited; got: %v", err)
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
	// Every audited Soroban source is BackfillSafe=true now, so
	// this test verifies the override mechanism with a config that
	// has multiple safe sources and the operator narrows to one.
	cfg := writeMinimalConfig(t, []string{"soroswap", "aquarius", "sdex"})
	args := []string{"-config", cfg, "-from", "100", "-to", "200", "-source", "sdex", "-dry-run"}
	opts, _, err := parseBackfillFlags(args)
	if err != nil {
		t.Fatalf("override should let sdex-only through; got: %v", err)
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
	// As of 2026-04-29 every on-chain Soroban source has been
	// audited; use a synthetic typo'd source to exercise the
	// fail-closed Lookup-fallback path. Same shape as the gate
	// test above.
	got := unsafeBackfillSources([]string{"unknown-future-source", "binance", "sdex", "kraken", "comet"})
	want := []string{"unknown-future-source"}
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

// planBackfillChunks tests — the parallelism plan must satisfy two
// invariants: the union of chunks covers [from, to] exactly with no
// gaps and no overlaps; chunk count equals the requested parallel
// (clamped to range size when the operator asks for more workers
// than ledgers).
func TestPlanBackfillChunks(t *testing.T) {
	cases := []struct {
		name     string
		from     uint32
		to       uint32
		n        int
		wantLen  int
		wantLast uint32 // last chunk's `to` should equal `to`
	}{
		{"sequential n=1", 100, 200, 1, 1, 200},
		{"even split n=4", 100, 199, 4, 4, 199},
		{"uneven split n=3 absorbs remainder in last", 100, 200, 3, 3, 200},
		{"n=0 treated as 1 (defensive)", 100, 200, 0, 1, 200},
		{"workers > range — degrades", 100, 102, 8, 3, 102},
		{"single ledger range", 500, 500, 4, 1, 500},
		{"adjacent to uint32 max", 4_294_967_290, 4_294_967_295, 2, 2, 4_294_967_295},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := planBackfillChunks(tc.from, tc.to, tc.n)
			if len(got) != tc.wantLen {
				t.Fatalf("len = %d, want %d (chunks: %v)", len(got), tc.wantLen, got)
			}
			if got[0].from != tc.from {
				t.Errorf("first chunk.from = %d, want %d", got[0].from, tc.from)
			}
			if got[len(got)-1].to != tc.wantLast {
				t.Errorf("last chunk.to = %d, want %d", got[len(got)-1].to, tc.wantLast)
			}
			// Coverage invariant: chunks are contiguous + non-overlapping.
			for i := 1; i < len(got); i++ {
				if got[i].from != got[i-1].to+1 {
					t.Errorf("chunk %d.from = %d, want %d (no gap, no overlap with chunk %d)",
						i, got[i].from, got[i-1].to+1, i-1)
				}
			}
		})
	}
}

// TestParseBackfillFlags_Parallel — exercise the flag's
// validation without the full integration plumbing.
func TestParseBackfillFlags_Parallel(t *testing.T) {
	cfgPath := writeMinimalConfig(t, []string{"sdex"})
	t.Run("default is 1", func(t *testing.T) {
		opts, _, err := parseBackfillFlags([]string{"-config", cfgPath, "-from", "100", "-to", "200"})
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if opts.parallel != 1 {
			t.Errorf("parallel = %d, want 1", opts.parallel)
		}
	})
	t.Run("explicit 8 accepted", func(t *testing.T) {
		opts, _, err := parseBackfillFlags([]string{"-config", cfgPath, "-from", "100", "-to", "200", "-parallel", "8"})
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if opts.parallel != 8 {
			t.Errorf("parallel = %d, want 8", opts.parallel)
		}
	})
	t.Run("zero rejected", func(t *testing.T) {
		_, _, err := parseBackfillFlags([]string{"-config", cfgPath, "-from", "100", "-to", "200", "-parallel", "0"})
		if err == nil {
			t.Fatal("expected error for parallel=0")
		}
	})
	t.Run("negative rejected", func(t *testing.T) {
		_, _, err := parseBackfillFlags([]string{"-config", cfgPath, "-from", "100", "-to", "200", "-parallel", "-3"})
		if err == nil {
			t.Fatal("expected error for parallel=-3")
		}
	})
}
