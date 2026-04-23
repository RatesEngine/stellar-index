//go:build integration

package integration_test

import (
	"context"
	"math/big"
	"testing"
	"time"

	c "github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// TestTradesInRangeAndMarkets covers the two read-paths backing
// /v1/history and /v1/markets: time-bounded trade lookup and
// distinct-pair enumeration. Proves the hypertable indexes + GROUP
// BY behave correctly end-to-end.
func TestTradesInRangeAndMarkets(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	dsn := startTimescale(t, ctx)
	applyMigrations(t, dsn)

	store, err := timescale.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// Two pairs: XLM/USDC and XLM/USDC.fake — enough to exercise
	// DistinctPairs grouping without needing many assets.
	usdc, err := c.NewClassicAsset("USDC", "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")
	if err != nil {
		t.Fatal(err)
	}
	// Same format, different last char so it's a distinct issuer
	// for DistinctPairs. Format-only validation (no CRC).
	fake, err := c.NewClassicAsset("USDX", "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVM")
	if err != nil {
		t.Fatal(err)
	}
	pairA, _ := c.NewPair(c.NativeAsset(), usdc)
	pairB, _ := c.NewPair(c.NativeAsset(), fake)

	// Anchor at a fixed point so the window queries are deterministic.
	t0 := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
	trades := []c.Trade{
		mkIntegrationTrade("sdex", 1, t0.Add(0*time.Minute), pairA, 1_000_000_000, 12_000_000),
		mkIntegrationTrade("sdex", 2, t0.Add(10*time.Minute), pairA, 1_000_000_000, 12_100_000),
		mkIntegrationTrade("sdex", 3, t0.Add(20*time.Minute), pairA, 1_000_000_000, 12_200_000),
		mkIntegrationTrade("sdex", 4, t0.Add(30*time.Minute), pairB, 1_000_000_000, 12_050_000),
	}
	for _, tr := range trades {
		if err := store.InsertTrade(ctx, tr); err != nil {
			t.Fatalf("InsertTrade: %v", err)
		}
	}

	// ─── TradesInRange ──────────────────────────────────────────────
	// Window covering only the middle two pairA trades (10-25 min).
	windowStart := t0.Add(10 * time.Minute)
	windowEnd := t0.Add(25 * time.Minute)
	got, err := store.TradesInRange(ctx, pairA, windowStart, windowEnd, 100)
	if err != nil {
		t.Fatalf("TradesInRange: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d trades in window, want 2", len(got))
	}
	// Must be ordered ts ASC.
	if !got[0].Timestamp.Before(got[1].Timestamp) {
		t.Errorf("trades not in ascending-ts order")
	}
	// Must be pairA only — pairB shouldn't leak in.
	for _, tr := range got {
		if !tr.Pair.Equal(pairA) {
			t.Errorf("pair B leaked into pair A query: %+v", tr.Pair)
		}
	}

	// Empty window → empty slice, no error.
	empty, err := store.TradesInRange(ctx, pairA, t0.Add(-1*time.Hour), t0.Add(-30*time.Minute), 100)
	if err != nil {
		t.Fatalf("TradesInRange (empty): %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("expected empty slice for empty window, got %d", len(empty))
	}

	// from > to rejection.
	if _, err := store.TradesInRange(ctx, pairA, windowEnd, windowStart, 100); err == nil {
		t.Error("TradesInRange should reject from > to")
	}

	// ─── DistinctPairs ──────────────────────────────────────────────
	markets, next, err := store.DistinctPairs(ctx, "", 500)
	if err != nil {
		t.Fatalf("DistinctPairs: %v", err)
	}
	if len(markets) != 2 {
		t.Fatalf("got %d markets, want 2 (XLM/USDC + XLM/USDX)", len(markets))
	}
	if next != "" {
		t.Errorf("expected empty cursor on final page, got %q", next)
	}

	// Every returned market should have LastTradeAt populated.
	for _, m := range markets {
		if m.LastTradeAt.IsZero() {
			t.Errorf("market %s|%s has zero last_trade_at",
				m.Pair.Base.String(), m.Pair.Quote.String())
		}
	}
}

func mkIntegrationTrade(source string, nonce int, ts time.Time, pair c.Pair, base, quote int64) c.Trade {
	// Generate a unique 64-char hex tx_hash per (source, nonce).
	h := make([]byte, 64)
	for i := range h {
		h[i] = '0'
	}
	// Encode nonce + source prefix into the tail.
	suffix := []byte(source)
	for i, b := range suffix {
		if i < 32 {
			h[32+i] = b
		}
	}
	// Nonce as 2-hex-digit suffix (enough for test uniqueness).
	const hex = "0123456789abcdef"
	h[62] = hex[(nonce>>4)&0xf]
	h[63] = hex[nonce&0xf]

	return c.Trade{
		Source:      source,
		Ledger:      uint32(50_000_000 + nonce),
		TxHash:      string(h),
		OpIndex:     0,
		Timestamp:   ts,
		Pair:        pair,
		BaseAmount:  c.NewAmount(big.NewInt(base)),
		QuoteAmount: c.NewAmount(big.NewInt(quote)),
	}
}
