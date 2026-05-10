//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// TestIssuersAndCoinsRegistryReads covers the read paths backing
// /v1/issuers (list + detail), /v1/coins (with and without the
// ?issuer= filter), and the issuer→assets join used by the
// showcase /coins/[slug] issuer tab.
//
// The classic_assets + issuers tables are populated by ingest-side
// observers in production. This test inserts directly so we can
// exercise the storage queries against a known shape without
// stitching together an end-to-end ingest harness.
func TestIssuersAndCoinsRegistryReads(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	dsn := startTimescale(t, ctx)
	applyMigrations(t, dsn)

	store, err := timescale.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// Three issuers — one with home_domain set, two without.
	// Observation counts vary so we can test the ranking.
	const (
		issuerA = "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN" // USDC-shape
		issuerB = "GBNZILSTVQZ4R7IKQDGHYGY2QXL5QOFJYQMXPKWRRM5PAV7Y4M67AQUA"
		issuerC = "GDM4RQUQQUVSKQA7S6EM7XBZP3FCGH4Q7CL6TABQ7B2BEJ5ERARM2M5M"
	)
	seedIssuers(t, ctx, store, []seedIssuer{
		{g: issuerA, homeDomain: "centre.io"},
		{g: issuerB, homeDomain: ""}, // null
		{g: issuerC, homeDomain: "example.org"},
	})
	seedClassicAssets(t, ctx, store, []seedAsset{
		{assetID: "USDC-" + issuerA, code: "USDC", issuer: issuerA, slug: "USDC", obs: 41_000_000},
		{assetID: "AQUA-" + issuerB, code: "AQUA", issuer: issuerB, slug: "AQUA", obs: 14_000_000},
		// Two assets for issuerC — verifies the JOIN aggregates
		// observation_count across multiple assets per issuer.
		{assetID: "FOO-" + issuerC, code: "FOO", issuer: issuerC, slug: "FOO", obs: 5_000_000},
		{assetID: "BAR-" + issuerC, code: "BAR", issuer: issuerC, slug: "BAR", obs: 2_000_000},
	})

	t.Run("ListIssuers", func(t *testing.T) {
		got, err := store.ListIssuers(ctx, 10)
		if err != nil {
			t.Fatalf("ListIssuers: %v", err)
		}
		if len(got) != 3 {
			t.Fatalf("got %d issuers, want 3", len(got))
		}
		// Ranking: A (41M) > B (14M) > C (7M total).
		if got[0].GStrkey != issuerA {
			t.Errorf("rank 1 = %s, want %s", got[0].GStrkey, issuerA)
		}
		if got[1].GStrkey != issuerB {
			t.Errorf("rank 2 = %s, want %s", got[1].GStrkey, issuerB)
		}
		if got[2].GStrkey != issuerC {
			t.Errorf("rank 3 = %s, want %s", got[2].GStrkey, issuerC)
		}
		// home_domain flows through.
		if got[0].HomeDomain != "centre.io" {
			t.Errorf("home_domain = %q, want %q", got[0].HomeDomain, "centre.io")
		}
		// NULL home_domain comes back as empty string.
		if got[1].HomeDomain != "" {
			t.Errorf("null home_domain = %q, want empty", got[1].HomeDomain)
		}
		// Per-issuer asset count + total observations aggregate
		// correctly across rows.
		if got[2].AssetCount != 2 {
			t.Errorf("issuerC asset_count = %d, want 2", got[2].AssetCount)
		}
		if got[2].TotalObservationCount != 7_000_000 {
			t.Errorf("issuerC total_obs = %d, want 7_000_000", got[2].TotalObservationCount)
		}
	})

	t.Run("ListIssuers limit clamp", func(t *testing.T) {
		// Out-of-range limit clamps to default 100, not to itself.
		got, err := store.ListIssuers(ctx, 0)
		if err != nil {
			t.Fatalf("ListIssuers(0): %v", err)
		}
		if len(got) != 3 {
			t.Errorf("limit=0 returned %d rows, want 3 (default 100 clamp)", len(got))
		}
		gotHigh, err := store.ListIssuers(ctx, 99999)
		if err != nil {
			t.Fatalf("ListIssuers(99999): %v", err)
		}
		if len(gotHigh) != 3 {
			t.Errorf("limit=99999 returned %d rows, want 3 (table only has 3)", len(gotHigh))
		}
	})

	t.Run("ListCoins no filter", func(t *testing.T) {
		got, err := store.ListCoins(ctx, 10, "", "")
		if err != nil {
			t.Fatalf("ListCoins: %v", err)
		}
		if len(got) != 4 {
			t.Fatalf("got %d coins, want 4", len(got))
		}
		// Ordered by observation_count desc — USDC at top.
		if got[0].Code != "USDC" {
			t.Errorf("rank 1 = %s, want USDC", got[0].Code)
		}
	})

	t.Run("ListCoins issuer filter", func(t *testing.T) {
		got, err := store.ListCoins(ctx, 10, issuerC, "")
		if err != nil {
			t.Fatalf("ListCoins by issuer: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("issuerC filter returned %d rows, want 2", len(got))
		}
		// Both rows should be issuerC.
		for _, r := range got {
			if r.IssuerGStrkey != issuerC {
				t.Errorf("row %s leaked through filter — issuer %s, want %s",
					r.Code, r.IssuerGStrkey, issuerC)
			}
		}
		// Top-of-list within the filter is FOO (5M > 2M).
		if got[0].Code != "FOO" {
			t.Errorf("filter rank 1 = %s, want FOO", got[0].Code)
		}
	})

	t.Run("ListCoins issuer filter — no match", func(t *testing.T) {
		got, err := store.ListCoins(ctx, 10, "GUNKNOWN0000000000000000000000000000000000000000000000XX", "")
		if err != nil {
			t.Fatalf("ListCoins unknown issuer: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("unknown issuer returned %d rows, want 0", len(got))
		}
	})

	t.Run("GetIssuer + ListIssuerAssets", func(t *testing.T) {
		row, err := store.GetIssuer(ctx, issuerA)
		if err != nil {
			t.Fatalf("GetIssuer: %v", err)
		}
		if row.GStrkey != issuerA {
			t.Errorf("g_strkey = %s, want %s", row.GStrkey, issuerA)
		}
		if row.HomeDomain != "centre.io" {
			t.Errorf("home_domain = %q, want centre.io", row.HomeDomain)
		}

		assets, err := store.ListIssuerAssets(ctx, issuerC)
		if err != nil {
			t.Fatalf("ListIssuerAssets: %v", err)
		}
		if len(assets) != 2 {
			t.Fatalf("issuerC assets = %d, want 2", len(assets))
		}
		// Assets returned ordered by observation_count desc.
		if assets[0].Code != "FOO" {
			t.Errorf("first asset = %s, want FOO", assets[0].Code)
		}
	})

	t.Run("GetIssuer not found", func(t *testing.T) {
		_, err := store.GetIssuer(ctx, "GUNKNOWN0000000000000000000000000000000000000000000000XX")
		if err == nil {
			t.Fatal("expected error for unknown issuer, got nil")
		}
		// Caller in handleIssuer relies on errors.Is(err, sql.ErrNoRows).
		if err != sql.ErrNoRows {
			t.Errorf("error = %v, want sql.ErrNoRows", err)
		}
	})
}

type seedIssuer struct {
	g          string
	homeDomain string
}

type seedAsset struct {
	assetID string
	code    string
	issuer  string
	slug    string
	obs     int64
}

// seedIssuers + seedClassicAssets insert directly into the registry
// tables. In production these are populated by the accounts decoder
// + classic_assets observer; bypassing them here keeps the test
// focused on the read-path behaviour.
func seedIssuers(t *testing.T, ctx context.Context, store *timescale.Store, rows []seedIssuer) {
	t.Helper()
	for _, r := range rows {
		var hd any
		if r.homeDomain != "" {
			hd = r.homeDomain
		}
		_, err := store.DB().ExecContext(ctx,
			`INSERT INTO issuers (g_strkey, home_domain) VALUES ($1, $2)`,
			r.g, hd,
		)
		if err != nil {
			t.Fatalf("seed issuer %s: %v", r.g, err)
		}
	}
}

func seedClassicAssets(t *testing.T, ctx context.Context, store *timescale.Store, rows []seedAsset) {
	t.Helper()
	now := time.Now().UTC()
	for _, r := range rows {
		_, err := store.DB().ExecContext(ctx, `
			INSERT INTO classic_assets
			    (asset_id, code, issuer_g_strkey, slug,
			     first_seen_at, first_seen_ledger,
			     last_seen_at,  last_seen_ledger,
			     observation_count)
			VALUES ($1, $2, $3, $4, $5, 1, $6, 100, $7)
		`,
			r.assetID, r.code, r.issuer, r.slug,
			now.Add(-24*time.Hour), now, r.obs,
		)
		if err != nil {
			t.Fatalf("seed asset %s: %v", r.assetID, err)
		}
	}
}
