//go:build integration

package integration_test

import (
	"context"
	"math/big"
	"testing"
	"time"

	c "github.com/StellarIndex/stellar-index/internal/canonical"
	"github.com/StellarIndex/stellar-index/internal/storage/timescale"
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
	// AQUA's issuer — a real, CRC-valid mainnet G-strkey, distinct
	// from USDC's issuer above. We need the strkey to round-trip
	// through canonical.NewClassicAsset's CRC check, so a
	// hand-crafted "USDC-issuer with last char tweaked" string
	// (which used to work when validation was format-only) no
	// longer round-trips.
	fake, err := c.NewClassicAsset("AQUA", "GBNZILSTVQZ4R7IKQDGHYGY2QXL5QOFJYQMXPKWRRM5PAV7Y4M67AQUA")
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
	// Force-refresh prices_1m so DistinctPairs sees the seeded
	// trades. Per rc.45 (commit 8717bc20), DistinctPairs reads the
	// 1-min continuous aggregate rather than scanning the raw
	// trades table — without this refresh the seeded rows are
	// present in `trades` but absent from `prices_1m` until the
	// 30 s policy fires (longer than the test window). Mirrors the
	// pattern in test/integration/api_test.go:65-74.
	// DistinctPairs enumerates pairs from prices_1d (the right-granularity
	// rewrite, #20) and reads 24h volume from prices_1m — refresh BOTH, or
	// the pair list comes back empty even though prices_1m has the rows.
	for _, stmt := range []string{
		`CALL refresh_continuous_aggregate('prices_1m', NULL, NULL)`,
		`CALL refresh_continuous_aggregate('prices_1d', NULL, NULL)`,
	} {
		if _, err := store.DB().ExecContext(ctx, stmt); err != nil {
			t.Fatalf("refresh cagg: %v", err)
		}
	}

	markets, next, err := store.DistinctPairs(ctx, "", 500)
	if err != nil {
		t.Fatalf("DistinctPairs: %v", err)
	}
	if len(markets) != 2 {
		t.Fatalf("got %d markets, want 2 (XLM/USDC + XLM/AQUA)", len(markets))
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

	// ─── DistinctPairs pagination round-trip ────────────────────────
	// Limit=1 forces paging; confirm the two pairs come back across
	// pages with a non-empty cursor after page 1 and an empty cursor
	// after page 2. Guards the recent markets.go change where the
	// page-break logic was rewritten — the `hasMore` signal must
	// fire only when a page was actually held back.
	var paged []timescale.Market
	cursor := ""
	for iter := 0; iter < 5; iter++ {
		page, nextC, err := store.DistinctPairs(ctx, cursor, 1)
		if err != nil {
			t.Fatalf("paged iter %d: %v", iter, err)
		}
		if len(page) > 1 {
			t.Fatalf("paged iter %d: limit=1 returned %d rows", iter, len(page))
		}
		paged = append(paged, page...)
		if nextC == "" {
			break
		}
		cursor = nextC
	}
	if len(paged) != 2 {
		t.Errorf("paginated DistinctPairs returned %d total rows, want 2", len(paged))
	}
}

func mkIntegrationTrade(source string, nonce int, ts time.Time, pair c.Pair, base, quote int64) c.Trade {
	// Generate a unique 64-char *hex* tx_hash per (source, nonce).
	// Earlier revision embedded the literal source string ("sdex")
	// into the hash, which broke canonical.Trade.Validate's
	// 64-char-hex check once validation tightened. Now we hex-
	// encode each source byte so the hash stays parseable.
	const hex = "0123456789abcdef"
	h := make([]byte, 64)
	for i := range h {
		h[i] = '0'
	}
	for i, b := range []byte(source) {
		if 2*i+1 >= 32 {
			break
		}
		h[32+2*i] = hex[b>>4]
		h[32+2*i+1] = hex[b&0xf]
	}
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
