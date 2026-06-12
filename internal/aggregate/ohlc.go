package aggregate

import (
	"math/big"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
)

// OHLC is an Open/High/Low/Close bar over a bucket of trades, plus
// total base + quote volumes. Prices are exact rationals.
//
// Invariants populated by [ComputeOHLC]:
//
//   - Open is the first (chronologically) trade's price.
//   - Close is the last trade's price.
//   - High ≥ Close, High ≥ Open. Low ≤ Close, Low ≤ Open.
//   - TradeCount is the number of trades that contributed (zero-base
//     trades are silently skipped).
type OHLC struct {
	Open        *big.Rat
	High        *big.Rat
	Low         *big.Rat
	Close       *big.Rat
	BaseVolume  canonical.Amount
	QuoteVolume canonical.Amount
	TradeCount  int
}

// ComputeOHLC produces an [OHLC] bar from the given trades.
//
// trades MUST be sorted by Timestamp, ascending — Open/Close
// depend on ordering. The function doesn't sort internally to
// avoid hiding caller bugs (consistent with [TWAP]).
//
// Returns [ErrNoTrades] when no trade contributes a valid price
// (every input had zero base or zero quote).
func ComputeOHLC(trades []canonical.Trade) (*OHLC, error) {
	if len(trades) == 0 {
		return nil, ErrNoTrades
	}

	bar := &OHLC{}
	totalBase := new(big.Int)
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

		price := new(big.Rat).SetFrac(q, b)

		if bar.TradeCount == 0 {
			bar.Open = new(big.Rat).Set(price)
			bar.High = new(big.Rat).Set(price)
			bar.Low = new(big.Rat).Set(price)
		} else {
			if price.Cmp(bar.High) > 0 {
				bar.High.Set(price)
			}
			if price.Cmp(bar.Low) < 0 {
				bar.Low.Set(price)
			}
		}
		bar.Close = new(big.Rat).Set(price)
		bar.TradeCount++

		totalBase.Add(totalBase, b)
		totalQuote.Add(totalQuote, q)
	}

	if bar.TradeCount == 0 {
		return nil, ErrNoTrades
	}
	bar.BaseVolume = canonical.NewAmount(totalBase)
	bar.QuoteVolume = canonical.NewAmount(totalQuote)
	return bar, nil
}
