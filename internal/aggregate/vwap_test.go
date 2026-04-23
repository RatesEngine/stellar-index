package aggregate_test

import (
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/RatesEngine/rates-engine/internal/aggregate"
	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// mkTrade builds a Trade for testing. base/quote are expressed in
// the asset's smallest unit (stroops/stroops-analogue).
func mkTrade(base, quote int64) canonical.Trade {
	return canonical.Trade{
		Source:      "test",
		Ledger:      1,
		TxHash:      "0000000000000000000000000000000000000000000000000000000000000001",
		OpIndex:     0,
		Timestamp:   time.Unix(0, 0).UTC(),
		BaseAmount:  canonical.NewAmount(big.NewInt(base)),
		QuoteAmount: canonical.NewAmount(big.NewInt(quote)),
	}
}

func TestVWAP_SingleTrade(t *testing.T) {
	// One trade of 100 base for 200 quote → price = 2.0 exactly.
	trades := []canonical.Trade{mkTrade(100, 200)}
	got, err := aggregate.VWAP(trades)
	if err != nil {
		t.Fatal(err)
	}
	if got.Cmp(big.NewRat(2, 1)) != 0 {
		t.Errorf("VWAP = %v, want 2/1", got)
	}
}

func TestVWAP_WeightedAverage(t *testing.T) {
	// 100 @ 2.0 (20 base × 40 quote) + 300 @ 3.0 (100 base × 300 quote)
	// → total quote / total base = (40+300)/(20+100) = 340/120 = 17/6.
	trades := []canonical.Trade{
		mkTrade(20, 40),
		mkTrade(100, 300),
	}
	got, err := aggregate.VWAP(trades)
	if err != nil {
		t.Fatal(err)
	}
	want := big.NewRat(17, 6)
	if got.Cmp(want) != 0 {
		t.Errorf("VWAP = %v, want 17/6 (%v)", got, want)
	}
}

func TestVWAP_PrecisionExact(t *testing.T) {
	// Amounts that float64 can't represent exactly — must stay
	// rational. 1e20 base, 3 quote → VWAP = 3/1e20. If we round
	// through float64 at any point we lose this.
	base, _ := new(big.Int).SetString("100000000000000000000", 10) // 1e20
	quote := big.NewInt(3)
	trade := canonical.Trade{
		Source: "test", Ledger: 1,
		TxHash:  "0000000000000000000000000000000000000000000000000000000000000001",
		OpIndex: 0, Timestamp: time.Unix(0, 0).UTC(),
		BaseAmount:  canonical.NewAmount(base),
		QuoteAmount: canonical.NewAmount(quote),
	}
	got, err := aggregate.VWAP([]canonical.Trade{trade})
	if err != nil {
		t.Fatal(err)
	}
	want := new(big.Rat).SetFrac(quote, base)
	if got.Cmp(want) != 0 {
		t.Errorf("VWAP = %v, want %v (precision lost)", got, want)
	}
}

func TestVWAP_EmptyReturnsErr(t *testing.T) {
	_, err := aggregate.VWAP(nil)
	if !errors.Is(err, aggregate.ErrNoTrades) {
		t.Fatalf("err = %v, want ErrNoTrades", err)
	}
	_, err = aggregate.VWAP([]canonical.Trade{})
	if !errors.Is(err, aggregate.ErrNoTrades) {
		t.Fatalf("err (empty slice) = %v, want ErrNoTrades", err)
	}
}

func TestVWAP_ZeroBaseTradesSkipped(t *testing.T) {
	// A malformed trade with zero base should be ignored; the other
	// trade's price should drive the result.
	trades := []canonical.Trade{
		mkTrade(0, 999), // should be skipped
		mkTrade(10, 50), // price 5.0
	}
	got, err := aggregate.VWAP(trades)
	if err != nil {
		t.Fatal(err)
	}
	if got.Cmp(big.NewRat(5, 1)) != 0 {
		t.Errorf("VWAP = %v, want 5/1 (zero-base skip)", got)
	}
}

func TestVWAP_AllZeroBaseReturnsErr(t *testing.T) {
	_, err := aggregate.VWAP([]canonical.Trade{mkTrade(0, 10), mkTrade(0, 20)})
	if !errors.Is(err, aggregate.ErrNoTrades) {
		t.Fatalf("err = %v, want ErrNoTrades", err)
	}
}

func TestVWAP_NegativeAmountsSkipped(t *testing.T) {
	// canonical.Trade.Validate rejects negative amounts, but VWAP is
	// called from rollup paths that sometimes bypass Validate (tests,
	// intermediate buckets). A negative base or quote would pollute
	// the sum and flip the sign of the result — skip them defensively.
	trades := []canonical.Trade{
		mkTrade(-10, 50), // negative base, skip
		mkTrade(10, -50), // negative quote, skip
		mkTrade(10, 50),  // legit: price 5.0
	}
	got, err := aggregate.VWAP(trades)
	if err != nil {
		t.Fatal(err)
	}
	if got.Cmp(big.NewRat(5, 1)) != 0 {
		t.Errorf("VWAP = %v, want 5/1 (negative skip)", got)
	}
}

func TestVWAP_I128ScaleExactPrecision(t *testing.T) {
	// Realistic Soroban magnitudes — amounts that would lose
	// precision if they passed through float64. ADR-0003 invariant:
	// prices stay as *big.Rat throughout the pipeline.
	//
	// Mix of trades near u128 boundaries + one near the low end,
	// proving the big.Int arithmetic doesn't overflow or round.
	tradeA := canonical.Trade{ // base=10^36, quote=2*10^36 → price 2
		Source: "test", Ledger: 1,
		TxHash:  "0000000000000000000000000000000000000000000000000000000000000001",
		OpIndex: 0, Timestamp: time.Unix(0, 0).UTC(),
		BaseAmount:  mustBigAmount("1000000000000000000000000000000000000"), // 10^36
		QuoteAmount: mustBigAmount("2000000000000000000000000000000000000"), // 2×10^36
	}
	tradeB := canonical.Trade{ // base=10^38, quote=3*10^38 → price 3
		Source: "test", Ledger: 2,
		TxHash:  "0000000000000000000000000000000000000000000000000000000000000002",
		OpIndex: 0, Timestamp: time.Unix(0, 0).UTC(),
		BaseAmount:  mustBigAmount("100000000000000000000000000000000000000"), // 10^38
		QuoteAmount: mustBigAmount("300000000000000000000000000000000000000"), // 3×10^38
	}
	// VWAP = (2e36 + 3e38) / (1e36 + 1e38) = 302e36 / 101e36 = 302/101.
	got, err := aggregate.VWAP([]canonical.Trade{tradeA, tradeB})
	if err != nil {
		t.Fatal(err)
	}
	want := big.NewRat(302, 101)
	if got.Cmp(want) != 0 {
		t.Errorf("VWAP = %v, want 302/101 — precision lost at i128 scale", got)
	}
}

func mustBigAmount(s string) canonical.Amount {
	n, ok := new(big.Int).SetString(s, 10)
	if !ok {
		panic("bad big int literal: " + s)
	}
	return canonical.NewAmount(n)
}

func TestTotalVolumes(t *testing.T) {
	trades := []canonical.Trade{
		mkTrade(10, 100),
		mkTrade(25, 250),
		mkTrade(5, 50),
	}
	wantBase := big.NewInt(40)
	wantQuote := big.NewInt(400)
	if got := aggregate.TotalBaseVolume(trades).BigInt(); got.Cmp(wantBase) != 0 {
		t.Errorf("TotalBaseVolume = %v, want %v", got, wantBase)
	}
	if got := aggregate.TotalQuoteVolume(trades).BigInt(); got.Cmp(wantQuote) != 0 {
		t.Errorf("TotalQuoteVolume = %v, want %v", got, wantQuote)
	}
}
