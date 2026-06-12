package aggregate_test

import (
	"errors"
	"math/big"
	"testing"

	"github.com/StellarAtlas/stellar-atlas/internal/aggregate"
	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
)

func TestComputeOHLC_SimpleBar(t *testing.T) {
	// Prices in order: 100, 150, 80, 120. Volumes all 1 base.
	trades := []canonical.Trade{
		mkTrade(1, 100),
		mkTrade(1, 150),
		mkTrade(1, 80),
		mkTrade(1, 120),
	}
	bar, err := aggregate.ComputeOHLC(trades)
	if err != nil {
		t.Fatal(err)
	}
	if bar.Open.Cmp(big.NewRat(100, 1)) != 0 {
		t.Errorf("Open = %v, want 100", bar.Open)
	}
	if bar.Close.Cmp(big.NewRat(120, 1)) != 0 {
		t.Errorf("Close = %v, want 120", bar.Close)
	}
	if bar.High.Cmp(big.NewRat(150, 1)) != 0 {
		t.Errorf("High = %v, want 150", bar.High)
	}
	if bar.Low.Cmp(big.NewRat(80, 1)) != 0 {
		t.Errorf("Low = %v, want 80", bar.Low)
	}
	if bar.TradeCount != 4 {
		t.Errorf("TradeCount = %d, want 4", bar.TradeCount)
	}
	if bar.BaseVolume.BigInt().Cmp(big.NewInt(4)) != 0 {
		t.Errorf("BaseVolume = %v, want 4", bar.BaseVolume)
	}
	if bar.QuoteVolume.BigInt().Cmp(big.NewInt(450)) != 0 {
		t.Errorf("QuoteVolume = %v, want 450", bar.QuoteVolume)
	}
}

func TestComputeOHLC_SingleTrade(t *testing.T) {
	// O=H=L=C = the single price.
	bar, err := aggregate.ComputeOHLC([]canonical.Trade{mkTrade(2, 200)})
	if err != nil {
		t.Fatal(err)
	}
	want := big.NewRat(100, 1)
	for name, got := range map[string]*big.Rat{
		"Open": bar.Open, "High": bar.High, "Low": bar.Low, "Close": bar.Close,
	} {
		if got.Cmp(want) != 0 {
			t.Errorf("%s = %v, want 100", name, got)
		}
	}
	if bar.TradeCount != 1 {
		t.Errorf("TradeCount = %d, want 1", bar.TradeCount)
	}
}

func TestComputeOHLC_SkipsInvalidTrades(t *testing.T) {
	// Zero-base + zero-quote must be skipped — shouldn't contaminate
	// O/H/L/C with 0 prices.
	trades := []canonical.Trade{
		mkTrade(0, 999), // invalid (zero base)
		mkTrade(1, 100),
		mkTrade(1, 0), // invalid (zero quote)
		mkTrade(1, 200),
	}
	bar, err := aggregate.ComputeOHLC(trades)
	if err != nil {
		t.Fatal(err)
	}
	if bar.TradeCount != 2 {
		t.Errorf("TradeCount = %d, want 2", bar.TradeCount)
	}
	if bar.Open.Cmp(big.NewRat(100, 1)) != 0 {
		t.Errorf("Open = %v (should skip invalid leading trades)", bar.Open)
	}
	if bar.Close.Cmp(big.NewRat(200, 1)) != 0 {
		t.Errorf("Close = %v", bar.Close)
	}
	if bar.Low.Sign() <= 0 {
		t.Errorf("Low = %v — a zero-quote trade contaminated the bar", bar.Low)
	}
}

func TestComputeOHLC_EmptyReturnsErr(t *testing.T) {
	_, err := aggregate.ComputeOHLC(nil)
	if !errors.Is(err, aggregate.ErrNoTrades) {
		t.Fatalf("err = %v, want ErrNoTrades", err)
	}
	_, err = aggregate.ComputeOHLC([]canonical.Trade{mkTrade(0, 100), mkTrade(1, 0)})
	if !errors.Is(err, aggregate.ErrNoTrades) {
		t.Fatalf("err (all-invalid) = %v, want ErrNoTrades", err)
	}
}

func TestComputeOHLC_PriceInvariants(t *testing.T) {
	// Property check: across a randomised mix of prices, the OHLC
	// invariants must hold.
	trades := []canonical.Trade{
		mkTrade(1, 42), mkTrade(1, 7), mkTrade(1, 99),
		mkTrade(1, 13), mkTrade(1, 256), mkTrade(1, 1),
	}
	bar, err := aggregate.ComputeOHLC(trades)
	if err != nil {
		t.Fatal(err)
	}
	if bar.High.Cmp(bar.Open) < 0 || bar.High.Cmp(bar.Close) < 0 {
		t.Errorf("High < Open or Close: %v < %v/%v", bar.High, bar.Open, bar.Close)
	}
	if bar.Low.Cmp(bar.Open) > 0 || bar.Low.Cmp(bar.Close) > 0 {
		t.Errorf("Low > Open or Close: %v > %v/%v", bar.Low, bar.Open, bar.Close)
	}
	if bar.Low.Cmp(bar.High) > 0 {
		t.Errorf("Low > High: %v > %v", bar.Low, bar.High)
	}
}
