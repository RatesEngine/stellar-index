package aggregate

import (
	"errors"
	"math/big"

	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// ErrNoTrades is returned from [VWAP] when the input slice is empty
// or every trade has zero base volume. Callers treat this as "price
// unavailable for this window" — not a programming error.
var ErrNoTrades = errors.New("aggregate: no trades in window")

// VWAP returns the volume-weighted average price of a slice of
// trades as an exact-precision big.Rat (quote-per-base).
//
// Definition: VWAP = Σ(QuoteAmount_i) / Σ(BaseAmount_i). Trades
// whose base OR quote is non-positive are skipped — they can't
// contribute to a meaningful weighted price. canonical.Trade.Validate
// already enforces both > 0, but VWAP is defensive because callers
// occasionally construct Trade values bypassing Validate (tests,
// future aggregator rollups).
//
// Returns [ErrNoTrades] when the sum of base volumes is zero (either
// an empty input or every trade skipped). The returned *big.Rat is
// always strictly positive under the canonical.Trade invariants
// (base > 0, quote > 0).
func VWAP(trades []canonical.Trade) (*big.Rat, error) {
	if len(trades) == 0 {
		return nil, ErrNoTrades
	}

	sumQuote := new(big.Int)
	sumBase := new(big.Int)
	for i := range trades {
		t := &trades[i]
		b := t.BaseAmount.BigInt()
		if b.Sign() <= 0 {
			continue
		}
		q := t.QuoteAmount.BigInt()
		if q.Sign() <= 0 {
			continue
		}
		sumBase.Add(sumBase, b)
		sumQuote.Add(sumQuote, q)
	}
	if sumBase.Sign() == 0 {
		return nil, ErrNoTrades
	}
	return new(big.Rat).SetFrac(sumQuote, sumBase), nil
}

// TotalBaseVolume returns Σ(BaseAmount_i) as an Amount.
func TotalBaseVolume(trades []canonical.Trade) canonical.Amount {
	sum := new(big.Int)
	for i := range trades {
		sum.Add(sum, trades[i].BaseAmount.BigInt())
	}
	return canonical.NewAmount(sum)
}

// TotalQuoteVolume returns Σ(QuoteAmount_i) as an Amount.
func TotalQuoteVolume(trades []canonical.Trade) canonical.Amount {
	sum := new(big.Int)
	for i := range trades {
		sum.Add(sum, trades[i].QuoteAmount.BigInt())
	}
	return canonical.NewAmount(sum)
}
