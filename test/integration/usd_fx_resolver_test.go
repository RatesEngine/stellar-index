//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	_ "github.com/lib/pq"

	c "github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// TestVWAPUSDFXResolver_QueriesPrices1m exercises the F-1268
// production path against a real postgres: seed an EURC/USDC
// trade, refresh prices_1m, then call USDPriceAt(EURC, now+1m) and
// verify the resolver picks up the VWAP through the USDC peg.
func TestVWAPUSDFXResolver_QueriesPrices1m(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	dsn := startTimescale(t, ctx)
	applyMigrations(t, dsn)

	store, err := timescale.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	usdcIssuer := "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"
	eurcIssuer := "GDHU6WRG4IEQXM5NZ4BMPKOXHW76MZM4Y2IEMFDVXBSDP6SJY4ITNPP2"
	usdc, _ := c.NewClassicAsset("USDC", usdcIssuer)
	eurc, _ := c.NewClassicAsset("EURC", eurcIssuer)
	pair, _ := c.NewPair(eurc, usdc)

	// Anchor 2h ago so the trade lands inside the prices_1m window
	// the CAGG materialises by default. Single trade is enough —
	// the resolver only needs one VWAP row.
	t0 := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Minute)
	trade := mkIntegrationTrade("sdex", 1, t0,
		pair,
		1_000_000_000, // 100 EURC at 7-decimals
		1_085_000_000) // 108.5 USDC at 7-decimals → 1.085 EUR/USD
	if err := store.InsertTrade(ctx, trade); err != nil {
		t.Fatalf("InsertTrade: %v", err)
	}
	if _, err := store.DB().ExecContext(ctx,
		`CALL refresh_continuous_aggregate('prices_1m', NULL, NULL)`,
	); err != nil {
		t.Fatalf("refresh prices_1m: %v", err)
	}

	// Resolver with USDC's classic asset key on the peg list.
	resolver, err := timescale.NewVWAPUSDFXResolver(store, timescale.VWAPUSDFXResolverOptions{
		USDPegs:   []string{"USDC-" + usdcIssuer},
		Freshness: 0, // disable freshness for deterministic tests
	})
	if err != nil {
		t.Fatalf("NewVWAPUSDFXResolver: %v", err)
	}

	// Query at the bucket-end timestamp. The resolver should hit
	// the seeded row.
	got, ok, err := resolver.USDPriceAt(ctx, eurc, t0.Add(time.Minute))
	if err != nil {
		t.Fatalf("USDPriceAt: %v", err)
	}
	if !ok {
		t.Fatalf("expected resolver to find EURC/USDC VWAP, got ok=false")
	}
	// VWAP = quote/base = 1.085. NUMERIC is exact; CAGG-rendered
	// text strips trailing zeros but preserves precision.
	if got != "1.085" {
		t.Errorf("USDPriceAt = %q, want %q", got, "1.085")
	}
}

// TestVWAPUSDFXResolver_NoMatchReturnsOk False — asset with no
// against-peg row produces (`""`, ok=false, nil err). Pre-Phase-2
// behaviour preserved for assets we don't cover yet.
func TestVWAPUSDFXResolver_NoMatchReturnsOk(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	dsn := startTimescale(t, ctx)
	applyMigrations(t, dsn)

	store, err := timescale.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	usdc, _ := c.NewClassicAsset("USDC", "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")

	resolver, _ := timescale.NewVWAPUSDFXResolver(store, timescale.VWAPUSDFXResolverOptions{
		USDPegs: []string{"USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"},
	})

	// No trades inserted; asking for an obscure asset against the
	// peg → no match. The boundary is (empty rate, ok=false, nil err).
	obscure, _ := c.NewClassicAsset("AQUA", "GBNZILSTVQZ4R7IKQDGHYGY2QXL5QOFJYQMXPKWRRM5PAV7Y4M67AQUA")
	_ = usdc
	got, ok, err := resolver.USDPriceAt(ctx, obscure, time.Now().UTC())
	if err != nil {
		t.Errorf("err = %v, want nil for no-match case", err)
	}
	if ok {
		t.Errorf("expected ok=false for no-data asset, got rate=%q ok=true", got)
	}
	if got != "" {
		t.Errorf("expected empty rate, got %q", got)
	}
}
