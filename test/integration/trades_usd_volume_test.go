//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	c "github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// TestInsertTrade_PopulatesUSDVolume proves the L2.2 caveat fix
// shipped end-to-end: a binance + fiat:USD trade lands with a
// non-NULL `usd_volume` column matching the expected sum/1e8
// conversion, and an on-chain trade lands with `usd_volume IS NULL`.
func TestInsertTrade_PopulatesUSDVolume(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	dsn := startTimescale(t, ctx)
	applyMigrations(t, dsn)

	store, err := timescale.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	usd, err := c.NewFiatAsset("USD")
	if err != nil {
		t.Fatal(err)
	}
	xlm, err := c.NewCryptoAsset("XLM")
	if err != nil {
		t.Fatal(err)
	}
	xlmUSD, _ := c.NewPair(xlm, usd)
	xlmUSDC, _ := c.NewPair(xlm,
		func() c.Asset {
			a, _ := c.NewCryptoAsset("USDC")
			return a
		}())

	// Anchor in the past for deterministic queries.
	ts := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)

	// Binance + fiat:USD: usd_volume = 12_000_000 / 1e8 = 0.12.
	binTrade := mkIntegrationTrade("binance", 1, ts, xlmUSD, 100_000_000, 12_000_000)
	// Soroswap + USDC (on-chain DEX): out of scope → usd_volume NULL.
	swapTrade := mkIntegrationTrade("soroswap", 2, ts, xlmUSDC, 100_000_000, 12_000_000)

	for _, tr := range []c.Trade{binTrade, swapTrade} {
		if err := store.InsertTrade(ctx, tr); err != nil {
			t.Fatalf("InsertTrade %s: %v", tr.Source, err)
		}
	}

	const q = `SELECT usd_volume FROM trades WHERE source = $1 AND ledger = $2`

	t.Run("binance + fiat:USD: usd_volume populated", func(t *testing.T) {
		var v sql.NullString
		if err := store.DB().QueryRowContext(ctx, q, "binance", binTrade.Ledger).Scan(&v); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if !v.Valid {
			t.Fatal("usd_volume = NULL, want a populated value")
		}
		// FloatString(8) on 12_000_000/1e8 → "0.12000000"; Postgres
		// NUMERIC may render as "0.12000000" or trim trailing zeros
		// depending on the column scale — accept either form.
		if v.String != "0.12000000" && v.String != "0.12" {
			t.Errorf("usd_volume = %q, want 0.12 or 0.12000000", v.String)
		}
	})

	t.Run("soroswap + USDC (on-chain): usd_volume NULL", func(t *testing.T) {
		var v sql.NullString
		if err := store.DB().QueryRowContext(ctx, q, "soroswap", swapTrade.Ledger).Scan(&v); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if v.Valid {
			t.Errorf("usd_volume = %q, want NULL (on-chain source out of scope)", v.String)
		}
	})
}
