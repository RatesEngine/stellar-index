package aggregate_test

import (
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/aggregate"
	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
)

// mkTradeAt is mkTrade with a custom timestamp.
func mkTradeAt(base, quote int64, ts time.Time) canonical.Trade {
	t := mkTrade(base, quote)
	t.Timestamp = ts
	return t
}

func TestTWAP_SingleIntervalUniform(t *testing.T) {
	// Two trades at t=0, t=60s, both priced at 100. windowEnd=120s.
	// First price active for 60s, second for 60s. TWAP = 100.
	t0 := time.Unix(0, 0).UTC()
	trades := []canonical.Trade{
		mkTradeAt(1, 100, t0),
		mkTradeAt(1, 100, t0.Add(60*time.Second)),
	}
	got, err := aggregate.TWAP(trades, t0.Add(120*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if got.Cmp(big.NewRat(100, 1)) != 0 {
		t.Errorf("TWAP = %v, want 100", got)
	}
}

func TestTWAP_TimeWeighting(t *testing.T) {
	// Price 100 active for 10s, price 200 active for 30s.
	// TWAP = (100×10 + 200×30) / 40 = 7000/40 = 175.
	t0 := time.Unix(0, 0).UTC()
	trades := []canonical.Trade{
		mkTradeAt(1, 100, t0),                     // 100 for 10s
		mkTradeAt(1, 200, t0.Add(10*time.Second)), // 200 for 30s
	}
	got, err := aggregate.TWAP(trades, t0.Add(40*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	want := big.NewRat(175, 1)
	if got.Cmp(want) != 0 {
		t.Errorf("TWAP = %v, want 175", got)
	}
}

func TestTWAP_EmptyReturnsErr(t *testing.T) {
	_, err := aggregate.TWAP(nil, time.Now())
	if !errors.Is(err, aggregate.ErrNoTrades) {
		t.Fatalf("err = %v, want ErrNoTrades", err)
	}
}

func TestTWAP_AllZeroDurationReturnsErr(t *testing.T) {
	// windowEnd equals the single trade's timestamp → zero duration.
	t0 := time.Unix(100, 0).UTC()
	_, err := aggregate.TWAP([]canonical.Trade{mkTradeAt(1, 100, t0)}, t0)
	if !errors.Is(err, aggregate.ErrNoTrades) {
		t.Fatalf("err = %v, want ErrNoTrades (zero-duration window)", err)
	}
}

func TestTWAP_SkipsZeroBaseTrades(t *testing.T) {
	// The zero-base middle trade must not contribute to either the
	// weighted sum or the duration accumulator.
	t0 := time.Unix(0, 0).UTC()
	trades := []canonical.Trade{
		mkTradeAt(1, 100, t0),
		mkTradeAt(0, 999, t0.Add(30*time.Second)),
		mkTradeAt(1, 200, t0.Add(60*time.Second)),
	}
	// Price 100 active t=0..30s (30s), skipped slot 30..60s,
	// price 200 active 60..90s (30s).
	// TWAP = (100*30 + 200*30) / 60 = 150.
	got, err := aggregate.TWAP(trades, t0.Add(90*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if got.Cmp(big.NewRat(150, 1)) != 0 {
		t.Errorf("TWAP = %v, want 150", got)
	}
}

func TestTWAP_NonPositiveFinalDurationClamps(t *testing.T) {
	// windowEnd BEFORE the last trade's timestamp means the last
	// trade's slot is negative — we clamp to zero rather than error,
	// so the TWAP reflects only the prior trades.
	t0 := time.Unix(0, 0).UTC()
	trades := []canonical.Trade{
		mkTradeAt(1, 100, t0),                     // 100 for 10s
		mkTradeAt(1, 500, t0.Add(10*time.Second)), // late trade, ignored
	}
	got, err := aggregate.TWAP(trades, t0.Add(10*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	// Only the first trade contributed: price 100 × 10s / 10s = 100.
	if got.Cmp(big.NewRat(100, 1)) != 0 {
		t.Errorf("TWAP = %v, want 100 (late trade clamped)", got)
	}
}
