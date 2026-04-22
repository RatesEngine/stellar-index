package aquarius

import (
	"fmt"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/stellarrpc"
)

// classify picks the event kind from topic[0]. Returns "" for
// non-Aquarius events so the caller skips cheaply.
func classify(e *stellarrpc.Event) string {
	if len(e.Topic) == 0 {
		return ""
	}
	switch e.Topic[0] {
	case TopicSymbolTrade:
		return EventTrade
	case TopicSymbolDepositLiquidity:
		return EventDepositLiquidity
	case TopicSymbolWithdrawLiquidity:
		return EventWithdrawLiquidity
	case TopicSymbolUpdateReserves:
		return EventUpdateReserves
	case TopicSymbolReservesSync:
		return EventReservesSync
	default:
		return ""
	}
}

// PoolInfo is what a Source.poolCache entry looks like. Populated
// by router reads the first time we see a pool's events.
type PoolInfo struct {
	Type   PoolType
	Tokens []canonical.Asset // index-stable; matches the pool contract's asset order
}

// decodeTrade decodes an Aquarius `trade` event into one or more
// canonical.Trade records.
//
// Aquarius trades carry amounts as Vec<i128> arrays (Q1, Q3).
// Normalisation:
//   - One non-zero `in` slot + one non-zero `out` slot → one Trade.
//   - Multiple non-zero slots on either side → one Trade per
//     (in, out) pair with non-trivial amounts. Rare; only occurs
//     in multi-asset stableswap swaps.
//
// pool is the resolved PoolInfo for this event's contract. Callers
// MUST look it up via Source.lookupPool before calling decodeTrade —
// this function doesn't talk to the RPC.
func decodeTrade(e *stellarrpc.Event, pool PoolInfo, closedAt time.Time) ([]canonical.Trade, error) {
	if pool.Type == PoolConcentrated {
		// Concentrated pools use a different event schema — we
		// punt until Phase-1 WIP ships.
		return nil, ErrConcentratedWIP
	}
	if len(pool.Tokens) < 2 {
		return nil, fmt.Errorf("%w: pool has %d tokens (need >= 2)", ErrMalformedPayload, len(pool.Tokens))
	}

	amountsIn, amountsOut, user, err := decodeTradeAmounts(e.Value)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedPayload, err)
	}
	if len(amountsIn) != len(pool.Tokens) || len(amountsOut) != len(pool.Tokens) {
		return nil, fmt.Errorf("%w: amounts arity %d/%d vs pool arity %d",
			ErrMalformedPayload, len(amountsIn), len(amountsOut), len(pool.Tokens))
	}

	// Find the (in, out) pairs with non-zero amounts on both sides.
	// A naive N×N loop is fine — N is 2–4 in practice.
	var out []canonical.Trade
	for i, inAmt := range amountsIn {
		if inAmt.Sign() <= 0 {
			continue
		}
		for j, outAmt := range amountsOut {
			if j == i || outAmt.Sign() <= 0 {
				continue
			}
			pair, err := canonical.NewPair(pool.Tokens[i], pool.Tokens[j])
			if err != nil {
				return nil, fmt.Errorf("pair: %w", err)
			}
			out = append(out, canonical.Trade{
				Source:      SourceName,
				Ledger:      e.Ledger,
				TxHash:      e.TxHash,
				OpIndex:     uint32(e.OperationIndex),
				Timestamp:   closedAt,
				Pair:        pair,
				BaseAmount:  inAmt,
				QuoteAmount: outAmt,
				Taker:       user,
			})
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: trade event had no non-zero in/out pair", ErrMalformedPayload)
	}
	return out, nil
}

// ─── Stubs awaiting the SDK-backed decoder ─────────────────────────
// decoderHooks lets tests inject fakes. In production these point
// at the real SCVal-decoder implementations (pending SDK dep PR).

var (
	decodeTradeAmounts = stubDecodeTradeAmounts
)

// stubDecodeTradeAmounts returns zero-length slices + an error so
// the decoder fails closed until the real XDR decoder swaps in.
// Unit tests override this via the package-level var.
func stubDecodeTradeAmounts(valueB64 string) (in, out []canonical.Amount, user string, err error) {
	return nil, nil, "", fmt.Errorf("aquarius: SCVal decoder not yet installed (TODO(#0))")
}
