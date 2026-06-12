//go:build integration

package integration_test

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	c "github.com/StellarAtlas/stellar-atlas/internal/canonical"
	"github.com/StellarAtlas/stellar-atlas/internal/obs"
	"github.com/StellarAtlas/stellar-atlas/internal/storage/timescale"
)

// TestTradeInsertOutcome_NewVsDuplicate pins the diagnostic metric
// `ratesengine_trade_insert_outcome_total{source, outcome}`: a
// fresh trade increments outcome=new, a re-insertion (same PK)
// increments outcome=duplicate. Live r1 evidence on 2026-05-28
// surfaced a stuck-cursor pattern where the older counter
// (trade_inserts_total) climbed at 157/min while the trades
// hypertable's max(ts) was 11 h old — every attempt was an
// ON CONFLICT DO NOTHING short-circuit. This metric makes that
// failure mode observable.
func TestTradeInsertOutcome_NewVsDuplicate(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	dsn := startTimescale(t, ctx)
	applyMigrations(t, dsn)

	store, err := timescale.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	usdc, err := c.NewClassicAsset("USDC", "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")
	if err != nil {
		t.Fatalf("USDC: %v", err)
	}
	pair, err := c.NewPair(c.NativeAsset(), usdc)
	if err != nil {
		t.Fatalf("pair: %v", err)
	}

	const src = "test-outcome-metric"
	trade := c.Trade{
		Source:      src,
		Ledger:      62_900_000,
		TxHash:      "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		OpIndex:     0,
		Timestamp:   time.Date(2026, 5, 28, 1, 0, 0, 0, time.UTC),
		Pair:        pair,
		BaseAmount:  c.NewAmount(big.NewInt(1_000_000_000)),
		QuoteAmount: c.NewAmount(big.NewInt(150_000_000)),
	}

	beforeNew := testutil.ToFloat64(obs.TradeInsertOutcomeTotal.WithLabelValues(src, "new"))
	beforeDup := testutil.ToFloat64(obs.TradeInsertOutcomeTotal.WithLabelValues(src, "duplicate"))

	// First insert — must take the `new` branch.
	if err := store.InsertTrade(ctx, trade); err != nil {
		t.Fatalf("InsertTrade #1: %v", err)
	}
	gotNew := testutil.ToFloat64(obs.TradeInsertOutcomeTotal.WithLabelValues(src, "new")) - beforeNew
	gotDup := testutil.ToFloat64(obs.TradeInsertOutcomeTotal.WithLabelValues(src, "duplicate")) - beforeDup
	if gotNew != 1 {
		t.Errorf("after fresh insert: outcome=new delta = %v, want 1", gotNew)
	}
	if gotDup != 0 {
		t.Errorf("after fresh insert: outcome=duplicate delta = %v, want 0", gotDup)
	}

	// Replay the exact same trade — must take the `duplicate` branch.
	if err := store.InsertTrade(ctx, trade); err != nil {
		t.Fatalf("InsertTrade #2 (replay): %v", err)
	}
	gotNew = testutil.ToFloat64(obs.TradeInsertOutcomeTotal.WithLabelValues(src, "new")) - beforeNew
	gotDup = testutil.ToFloat64(obs.TradeInsertOutcomeTotal.WithLabelValues(src, "duplicate")) - beforeDup
	if gotNew != 1 {
		t.Errorf("after exact replay: outcome=new delta = %v, want still 1", gotNew)
	}
	if gotDup != 1 {
		t.Errorf("after exact replay: outcome=duplicate delta = %v, want 1", gotDup)
	}
}
