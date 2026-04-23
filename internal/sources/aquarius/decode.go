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
	//
	// OpIndex MUST differ across the emitted trades. The primary key
	// on the trades hypertable is (source, ledger, tx_hash, op_index,
	// ts); if we reused e.OperationIndex for every trade, `InsertTrade`'s
	// ON CONFLICT DO NOTHING would silently drop all but the first.
	//
	// Flat sub-counter fanout:
	//
	//     op_index = e.OperationIndex × opIndexFanoutStride + n
	//
	// where `n` is the 0-based position of this trade in the emitted
	// slice. Using a flat counter (not a 2D i,j encoding) bounds the
	// sub-index by the number of valid (in, out) pairs — at most
	// N×(N-1) ≤ 12 for a 4-token pool, well below the 256 stride.
	// The earlier i×stride+j scheme collided across adjacent
	// operations (op=0,i=1,j=0 → 256 == op=1,i=0,j=0 → 256).
	var out []canonical.Trade
	var n uint32
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
			if n >= opIndexFanoutStride {
				// Should never hit in practice (N×(N-1) ≪ stride),
				// but refuse loudly rather than silently colliding
				// into the next operation's OpIndex range.
				return nil, fmt.Errorf("%w: too many (in,out) pairs (%d) for stride %d",
					ErrMalformedPayload, n+1, opIndexFanoutStride)
			}
			out = append(out, canonical.Trade{
				Source:      SourceName,
				Ledger:      e.Ledger,
				TxHash:      e.TxHash,
				OpIndex:     uint32(e.OperationIndex)*opIndexFanoutStride + n,
				Timestamp:   closedAt,
				Pair:        pair,
				BaseAmount:  inAmt,
				QuoteAmount: outAmt,
				Taker:       user,
			})
			n++
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: trade event had no non-zero in/out pair", ErrMalformedPayload)
	}
	return out, nil
}

// opIndexFanoutStride spaces the synthetic op_index values so
// multi-trade events from one operation don't collide with adjacent
// operations. 256 is overkill for 2–4 token pools but leaves
// headroom for future pool sizes without a schema change.
const opIndexFanoutStride = 256

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
