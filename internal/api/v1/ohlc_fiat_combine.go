package v1

import (
	"context"
	"math/big"
	"sort"
	"time"

	"github.com/StellarIndex/stellar-index/internal/aggregate"
	"github.com/StellarIndex/stellar-index/internal/canonical"
)

// ohlcSeriesFiatCombined builds a fiat-denominated (e.g. XLM/USD) OHLC
// series by COMBINING every USD-pegged constituent series per bucket,
// rather than the first-hit single-pair read used for non-fiat quotes.
//
// Why: the continuous aggregates key bars by the real stored quote_asset
// (`native/USDC-GA5Z…`, `crypto:XLM/fiat:USD`, …). A fiat quote like
// `fiat:USD` has no trades of its own except the recent direct CEX feeds,
// so first-hit served only ~5 weeks while the deep history sits under the
// USD-pegged stablecoin pairs (Circle USDC back to 2021). Combining them
// — using the SAME constituent set the live aggregator computes its VWAP
// over (aggregate.ExpandTargetPairWithClassicPegs) — yields the full
// multi-year series and keeps the historical bars methodologically
// consistent with the live /v1/price path (CLAUDE.md "stablecoins-as-fiat
// is aggregator policy, late-bound at compute time").
//
// Combine math per bucket (exact in NUMERIC/big.Rat):
//   - volume      = Σ base_volume                (exact)
//   - quote_vol   = Σ quote_volume               (exact)
//   - high / low  = max(high) / min(low)         (exact)
//   - open/close  = Σ(price·base_vol) / Σ base_vol  (base-volume-weighted)
//     — the only approximation, and only in buckets where >1 constituent
//     trades; deep-history buckets have a single constituent (USDC) so
//     open/close are exact there. Base-volume weighting matches the VWAP
//     definition (Σ price·base / Σ base).
//
// Each constituent read goes through the cached HistoryReader, so repeat
// requests hit the per-pair cache.
func (s *Server) ohlcSeriesFiatCombined(
	ctx context.Context,
	pair canonical.Pair,
	interval ohlcInterval,
	from, to time.Time,
	limit int,
) ([]OHLCSeriesBar, error) {
	src := s.usdPeggedConstituents(pair)

	acc := make(map[time.Time]*ohlcBucketAcc, 256)
	for _, sp := range src {
		bars, err := s.history.OHLCSeries(ctx, sp, string(interval), from, to, limit)
		if err != nil {
			// Propagate real errors (DB / context) — a constituent that
			// simply has no rows returns an empty slice + nil, not an error.
			return nil, err
		}
		for i := range bars {
			b := &bars[i]
			a := acc[b.T]
			if a == nil {
				a = newOHLCBucketAcc()
				acc[b.T] = a
			}
			a.add(b)
		}
	}
	if len(acc) == 0 {
		return nil, nil
	}

	out := make([]OHLCSeriesBar, 0, len(acc))
	for t, a := range acc {
		out = append(out, a.finalize(t))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].T.Before(out[j].T) })
	// Match OHLCSeries' earliest-N-in-window semantics (ORDER BY bucket
	// ASC LIMIT n): the handler sizes [from,to] to `limit` intervals, so
	// this only bites when a caller passes an explicit wide window.
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// usdPeggedConstituents enumerates the source (base,quote) cagg pairs to
// combine for a fiat-quoted target, across both XLM dual-form base
// aliases (native ↔ crypto:XLM) and the aggregator's USD-peg expansion
// (direct pair + stablecoin backers + operator-declared classic pegs).
// Deduplicated.
func (s *Server) usdPeggedConstituents(pair canonical.Pair) []canonical.Pair {
	seen := make(map[string]struct{})
	var out []canonical.Pair
	for _, b := range assetAliases(pair.Base) {
		tgt, err := canonical.NewPair(b, pair.Quote)
		if err != nil {
			continue
		}
		expanded, err := aggregate.ExpandTargetPairWithClassicPegs(tgt, s.usdPeggedClassics)
		if err != nil {
			// Malformed target — fall back to the direct pair only.
			expanded = []canonical.Pair{tgt}
		}
		for _, sp := range expanded {
			k := sp.Base.String() + "\x00" + sp.Quote.String()
			if _, dup := seen[k]; dup {
				continue
			}
			seen[k] = struct{}{}
			out = append(out, sp)
		}
	}
	return out
}

// ohlcBucketAcc accumulates the combine across constituent bars sharing a
// bucket timestamp. All arithmetic is big.Rat/big.Int to preserve the
// NUMERIC precision the wire contract promises (no float round-trip).
type ohlcBucketAcc struct {
	openNum  *big.Rat // Σ(open  · base_vol)
	closeNum *big.Rat // Σ(close · base_vol)
	high     *big.Rat
	low      *big.Rat
	baseVol  *big.Rat // Σ base_vol (weight denominator)
	quoteVol *big.Rat // Σ quote_vol
	n        int64
	have     bool
}

func newOHLCBucketAcc() *ohlcBucketAcc {
	return &ohlcBucketAcc{
		openNum:  new(big.Rat),
		closeNum: new(big.Rat),
		baseVol:  new(big.Rat),
		quoteVol: new(big.Rat),
	}
}

func (a *ohlcBucketAcc) add(b *OHLCSeriesBar) {
	open := ratFromDecimal(b.O)
	closeP := ratFromDecimal(b.C)
	high := ratFromDecimal(b.H)
	low := ratFromDecimal(b.L)
	bv := ratFromDecimal(b.VBase)
	qv := ratFromDecimal(b.VQuote)
	if open == nil || closeP == nil || high == nil || low == nil || bv == nil {
		return // unparseable row — skip rather than corrupt the bucket
	}

	a.openNum.Add(a.openNum, new(big.Rat).Mul(open, bv))
	a.closeNum.Add(a.closeNum, new(big.Rat).Mul(closeP, bv))
	a.baseVol.Add(a.baseVol, bv)
	if qv != nil {
		a.quoteVol.Add(a.quoteVol, qv)
	}
	a.n += b.N
	if !a.have {
		a.high, a.low, a.have = high, low, true
	} else {
		if high.Cmp(a.high) > 0 {
			a.high = high
		}
		if low.Cmp(a.low) < 0 {
			a.low = low
		}
	}
}

func (a *ohlcBucketAcc) finalize(t time.Time) OHLCSeriesBar {
	open, closeP := new(big.Rat), new(big.Rat)
	if a.baseVol.Sign() > 0 {
		open.Quo(a.openNum, a.baseVol)
		closeP.Quo(a.closeNum, a.baseVol)
	}
	return OHLCSeriesBar{
		T:      t,
		O:      ratToDecimal(open, ohlcPriceDigits),
		H:      ratToDecimal(a.high, ohlcPriceDigits),
		L:      ratToDecimal(a.low, ohlcPriceDigits),
		C:      ratToDecimal(closeP, ohlcPriceDigits),
		VBase:  ratToDecimal(a.baseVol, 0),
		VQuote: ratToDecimal(a.quoteVol, ohlcPriceDigits),
		N:      a.n,
	}
}

// ratFromDecimal parses a NUMERIC decimal string (e.g. "0.19056",
// "3997807371333934") into a big.Rat. Returns nil on parse failure or
// empty input.
func ratFromDecimal(s string) *big.Rat {
	if s == "" {
		return nil
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return nil
	}
	return r
}
