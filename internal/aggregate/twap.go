package aggregate

import (
	"math/big"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
)

// TWAP returns the time-weighted average price over the given
// trades, with each trade's price active until the next trade's
// timestamp (or windowEnd for the final trade).
//
// Requirements:
//
//   - trades must be sorted by Timestamp, ascending. The function
//     does NOT sort internally — doing so silently would hide
//     caller bugs. If trades are unsorted, results are meaningless.
//   - windowEnd must be ≥ the last trade's timestamp. A windowEnd
//     earlier than the last trade's timestamp means the final
//     trade's slot is negative; we clamp to zero for that slot
//     rather than return an error, but ordering upstream is still
//     a bug.
//
// Returns [ErrNoTrades] for an empty slice or when the total
// duration is zero (every trade at the exact same timestamp as
// windowEnd and each other).
//
// Formula: TWAP = Σ(price_i × Δt_i) / Σ(Δt_i), where Δt_i is the
// duration the i-th price was "current."
//
// Trades with zero base volume are skipped — they have no defined
// price.
func TWAP(trades []canonical.Trade, windowEnd time.Time) (*big.Rat, error) {
	if len(trades) == 0 {
		return nil, ErrNoTrades
	}

	// weightedSum accumulates Σ(price_i × Δt_i) as a Rat.
	// totalSeconds accumulates Σ(Δt_i) as int64 nanoseconds.
	weightedSum := new(big.Rat)
	var totalNanos int64

	for i := range trades {
		base := trades[i].BaseAmount.BigInt()
		if base.Sign() <= 0 {
			continue
		}
		quote := trades[i].QuoteAmount.BigInt()
		if quote.Sign() <= 0 {
			continue
		}

		var dur time.Duration
		if i == len(trades)-1 {
			dur = windowEnd.Sub(trades[i].Timestamp)
		} else {
			dur = trades[i+1].Timestamp.Sub(trades[i].Timestamp)
		}
		if dur <= 0 {
			continue
		}

		price := new(big.Rat).SetFrac(quote, base)
		// Weight = Δt in nanoseconds (integer). Scaling by the same
		// factor on top + bottom, it cancels in the final division —
		// so raw nanoseconds is a valid weight choice.
		weight := big.NewRat(int64(dur), 1)
		price.Mul(price, weight)
		weightedSum.Add(weightedSum, price)
		totalNanos += int64(dur)
	}

	if totalNanos == 0 {
		return nil, ErrNoTrades
	}
	return new(big.Rat).Quo(weightedSum, big.NewRat(totalNanos, 1)), nil
}
