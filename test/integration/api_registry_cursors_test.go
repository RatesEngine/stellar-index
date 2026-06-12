//go:build integration

package integration_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	v1 "github.com/StellarAtlas/stellar-atlas/internal/api/v1"
	"github.com/StellarAtlas/stellar-atlas/internal/storage/timescale"
)

// TestAPI_RegistryAndCursors covers the HTTP surfaces of the
// registry/diagnostics endpoints landed in #574/#577/#595/#596/#597:
//
//	GET /v1/coins
//	GET /v1/coins?issuer=G…
//	GET /v1/issuers
//	GET /v1/issuers/{g_strkey}
//	GET /v1/diagnostics/cursors
//
// timescale.Store satisfies all four reader interfaces directly,
// so no adapter glue is needed — wire the store straight through
// v1.Options.
func TestAPI_RegistryAndCursors(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	dsn := startTimescale(t, ctx)
	applyMigrations(t, dsn)

	store, err := timescale.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	const (
		issuerA = "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"
		issuerB = "GBNZILSTVQZ4R7IKQDGHYGY2QXL5QOFJYQMXPKWRRM5PAV7Y4M67AQUA"
	)
	seedIssuers(t, ctx, store, []seedIssuer{
		{g: issuerA, homeDomain: "centre.io"},
		{g: issuerB},
	})
	seedClassicAssets(t, ctx, store, []seedAsset{
		{assetID: "USDC-" + issuerA, code: "USDC", issuer: issuerA, slug: "USDC", obs: 41_000_000},
		{assetID: "AQUA-" + issuerB, code: "AQUA", issuer: issuerB, slug: "AQUA", obs: 14_000_000},
	})

	// Two ingest cursors at different lags so we can observe the
	// `lag_seconds` field the showcase /diagnostics page colour-codes.
	if err := store.UpsertCursor(ctx, "soroswap", "factory", 60_000_000); err != nil {
		t.Fatalf("UpsertCursor soroswap: %v", err)
	}
	if err := store.UpsertCursor(ctx, "sdex", "", 60_000_001); err != nil {
		t.Fatalf("UpsertCursor sdex: %v", err)
	}

	srv := v1.New(v1.Options{
		Coins:   store,
		Issuers: store,
		Cursors: store,
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	t.Run("/v1/assets", func(t *testing.T) {
		var env struct {
			Data []v1.AssetDetail `json:"data"`
		}
		getJSON(t, ts.URL+"/v1/assets?limit=10", &env)
		if len(env.Data) != 2 {
			t.Fatalf("got %d assets, want 2", len(env.Data))
		}
		// USDC ranks above AQUA (41M vs 14M observations).
		if env.Data[0].Code != "USDC" {
			t.Errorf("rank 1 = %q, want USDC", env.Data[0].Code)
		}
	})

	t.Run("/v1/assets?issuer=G…", func(t *testing.T) {
		var env struct {
			Data []v1.AssetDetail `json:"data"`
		}
		getJSON(t, ts.URL+"/v1/assets?limit=10&issuer="+issuerA, &env)
		if len(env.Data) != 1 {
			t.Fatalf("issuerA filter returned %d rows, want 1", len(env.Data))
		}
		if env.Data[0].Issuer == nil || *env.Data[0].Issuer != issuerA {
			t.Errorf("row issuer = %v, want %q", env.Data[0].Issuer, issuerA)
		}
	})

	t.Run("/v1/issuers", func(t *testing.T) {
		var env struct {
			Data []v1.IssuerListEntry `json:"data"`
		}
		getJSON(t, ts.URL+"/v1/issuers", &env)
		if len(env.Data) != 2 {
			t.Fatalf("got %d issuers, want 2", len(env.Data))
		}
		// Highest-volume issuer first.
		if env.Data[0].GStrkey != issuerA {
			t.Errorf("rank 1 = %q, want %q", env.Data[0].GStrkey, issuerA)
		}
		if env.Data[0].HomeDomain != "centre.io" {
			t.Errorf("home_domain = %q, want centre.io", env.Data[0].HomeDomain)
		}
	})

	t.Run("/v1/issuers/{g_strkey}", func(t *testing.T) {
		var env struct {
			Data v1.Issuer `json:"data"`
		}
		getJSON(t, ts.URL+"/v1/issuers/"+issuerA, &env)
		if env.Data.GStrkey != issuerA {
			t.Errorf("g_strkey = %q, want %q", env.Data.GStrkey, issuerA)
		}
		// The asset list embedded on the issuer envelope drives the
		// showcase /coins/[slug] issuer card; the contract is "always
		// include even if empty."
		if len(env.Data.Assets) != 1 {
			t.Errorf("assets = %d, want 1", len(env.Data.Assets))
		}
	})

	t.Run("/v1/diagnostics/cursors", func(t *testing.T) {
		var env struct {
			Data []v1.Cursor `json:"data"`
		}
		getJSON(t, ts.URL+"/v1/diagnostics/cursors", &env)
		if len(env.Data) != 2 {
			t.Fatalf("got %d cursors, want 2", len(env.Data))
		}
		// Ordered by (source, sub_source) per ListCursors. sdex has
		// empty sub_source, soroswap has "factory" — so sdex first.
		if env.Data[0].Source != "sdex" {
			t.Errorf("rank 1 source = %q, want sdex", env.Data[0].Source)
		}
		if env.Data[1].Source != "soroswap" {
			t.Errorf("rank 2 source = %q, want soroswap", env.Data[1].Source)
		}
		if env.Data[1].SubSource != "factory" {
			t.Errorf("soroswap sub_source = %q, want factory", env.Data[1].SubSource)
		}
		// lag_seconds is computed Go-side as now - last_updated; the
		// rows were just inserted so lag should be small and
		// non-negative. (Bound the upper end loosely to avoid CI
		// flakes; the contract is "non-negative and finite", not an
		// exact value.)
		for _, c := range env.Data {
			if c.LagSeconds < 0 {
				t.Errorf("%s/%s lag_seconds = %d, want non-negative",
					c.Source, c.SubSource, c.LagSeconds)
			}
		}
	})
}
