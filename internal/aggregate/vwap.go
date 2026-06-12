package aggregate

import (
	"errors"
	"math/big"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
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

// SourceContribution captures one source's share of a windowed VWAP
// — the building block the explorer source-contribution donut renders.
//
// Weight is a fraction in [0, 1]; sums to 1.0 across all
// contributions for the same trade slice (modulo float rounding).
type SourceContribution struct {
	Source      string
	Weight      float64
	BaseVolume  *big.Int
	QuoteVolume *big.Int
	TradeCount  int
}

// SourceContributions returns one [SourceContribution] per distinct
// trade.Source in `trades`. Each entry's Weight is its
// quote-volume share (i.e. how much that source contributed to the
// VWAP's numerator).
//
// Skips the same edge cases as [VWAP] — trades with non-positive
// base or quote volumes don't contribute. Returns nil for an empty
// or all-skipped input.
func SourceContributions(trades []canonical.Trade) []SourceContribution {
	if len(trades) == 0 {
		return nil
	}
	type accum struct {
		base  *big.Int
		quote *big.Int
		count int
	}
	bySource := make(map[string]*accum)
	totalQuote := new(big.Int)
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
		a, ok := bySource[t.Source]
		if !ok {
			a = &accum{base: new(big.Int), quote: new(big.Int)}
			bySource[t.Source] = a
		}
		a.base.Add(a.base, b)
		a.quote.Add(a.quote, q)
		a.count++
		totalQuote.Add(totalQuote, q)
	}
	if totalQuote.Sign() == 0 {
		return nil
	}
	totalQuoteF, _ := new(big.Float).SetInt(totalQuote).Float64()
	out := make([]SourceContribution, 0, len(bySource))
	for source, a := range bySource {
		quoteF, _ := new(big.Float).SetInt(a.quote).Float64()
		weight := quoteF / totalQuoteF
		out = append(out, SourceContribution{
			Source:      source,
			Weight:      weight,
			BaseVolume:  a.base,
			QuoteVolume: a.quote,
			TradeCount:  a.count,
		})
	}
	return out
}
