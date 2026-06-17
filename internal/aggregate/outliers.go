package aggregate

import (
	"math"
	"math/big"

	"github.com/StellarIndex/stellar-index/internal/canonical"
)

// FilterOutliers returns a copy of trades with prices further than
// sigma standard deviations from the mean removed.
//
// Price is computed per-trade as QuoteAmount/BaseAmount. Trades with
// zero base amount are dropped unconditionally (they have no
// defined price). Statistics are computed on the float64 projection
// — acceptable here because outlier detection is a heuristic, not a
// value-serving computation.
//
// sigma <= 0 is a no-op (returns a shallow copy of the input). A
// sigma of 0 would reject every trade which is never what callers
// want; rather than panic we return the input unchanged. Pass a
// positive threshold like 3.0 or 4.0 (the Phase-1 default).
//
// With fewer than 3 trades the filter can't meaningfully compute σ
// and returns the input unchanged. Under those conditions the
// caller's price signal is already degraded; we don't compound the
// issue by potentially dropping half the data.
func FilterOutliers(trades []canonical.Trade, sigma float64) []canonical.Trade {
	if sigma <= 0 || len(trades) < 3 {
		out := make([]canonical.Trade, len(trades))
		copy(out, trades)
		return out
	}

	prices := make([]float64, 0, len(trades))
	validIdx := make([]int, 0, len(trades))
	for i := range trades {
		p, ok := priceFloat(&trades[i])
		if !ok {
			continue
		}
		prices = append(prices, p)
		validIdx = append(validIdx, i)
	}
	if len(prices) < 3 {
		out := make([]canonical.Trade, 0, len(validIdx))
		for _, i := range validIdx {
			out = append(out, trades[i])
		}
		return out
	}

	mean, stdev := meanStdev(prices)
	if stdev == 0 {
		// All prices identical — nothing is an outlier.
		out := make([]canonical.Trade, 0, len(validIdx))
		for _, i := range validIdx {
			out = append(out, trades[i])
		}
		return out
	}
	threshold := sigma * stdev

	out := make([]canonical.Trade, 0, len(validIdx))
	for k, p := range prices {
		if math.Abs(p-mean) > threshold {
			continue
		}
		out = append(out, trades[validIdx[k]])
	}
	return out
}

// priceFloat projects a trade's price (quote-per-base) to float64.
// Reports ok=false for zero-base or zero-quote trades.
func priceFloat(t *canonical.Trade) (float64, bool) {
	b := t.BaseAmount.BigInt()
	if b.Sign() <= 0 {
		return 0, false
	}
	q := t.QuoteAmount.BigInt()
	if q.Sign() <= 0 {
		return 0, false
	}
	// big.Rat → big.Float → float64. Two-step so we don't pin float64
	// precision limits inside big.Rat.SetFrac.
	r := new(big.Rat).SetFrac(q, b)
	f, _ := new(big.Float).SetRat(r).Float64()
	return f, true
}

// meanStdev returns the arithmetic mean and sample standard
// deviation of xs. xs must contain ≥ 2 elements.
func meanStdev(xs []float64) (mean, stdev float64) {
	var sum float64
	for _, x := range xs {
		sum += x
	}
	mean = sum / float64(len(xs))

	var sumSq float64
	for _, x := range xs {
		d := x - mean
		sumSq += d * d
	}
	// Sample stdev (Bessel's correction): /(n-1), not /n. Matches
	// the Phase-1 formula cited in docs/ctx-proposal.md.
	stdev = math.Sqrt(sumSq / float64(len(xs)-1))
	return mean, stdev
}
