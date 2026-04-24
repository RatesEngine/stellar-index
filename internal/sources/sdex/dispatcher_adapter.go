package sdex

import (
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/consumer"
	"github.com/RatesEngine/rates-engine/internal/dispatcher"
)

// Decoder is the dispatcher-facing OpDecoder for classic SDEX
// trades. Unlike the Soroban sources (events.Event-driven) SDEX
// operates on xdr.Operation + xdr.OperationResult pairs via
// dispatcher.OpContext.
//
// Stateless — every claim atom is self-contained. The Decoder
// owns no correlation buffer; a single op may emit N trades
// (one per claim atom) but all of them come from the same
// OpContext in one Decode call.
type Decoder struct{}

// NewDecoder constructs an SDEX Decoder.
func NewDecoder() *Decoder { return &Decoder{} }

// Name implements [dispatcher.OpDecoder].
func (*Decoder) Name() string { return SourceName }

// Matches implements [dispatcher.OpDecoder]. True for every op
// type that can produce ClaimAtoms: ManageOffer (sell/buy),
// CreatePassiveSellOffer, PathPayment (strict-receive + strict-
// send). See matchesTradeOp for the authoritative list.
func (*Decoder) Matches(op xdr.Operation) bool {
	return matchesTradeOp(op)
}

// Decode implements [dispatcher.OpDecoder]. Returns zero trades
// for ops that didn't cross the book (failed op, or offer rested
// without claiming anything), one trade per claim atom otherwise.
//
// The Taker on every emitted trade is the tx source account
// (the buyer-side actor who placed the aggressive offer). Maker
// comes from each claim atom — usually a G-address, but for
// liquidity-pool fills it's the pool hash (hex-encoded).
func (*Decoder) Decode(ctx dispatcher.OpContext) ([]consumer.Event, error) {
	atoms := extractClaimAtoms(ctx.Op, ctx.OpResult)
	if len(atoms) == 0 {
		return nil, nil
	}
	taker := ctx.TxSource
	if ctx.OpSource != "" {
		// Per-op source overrides tx-level. Rare (most ops don't
		// set their own source), but the XDR allows it and some
		// advanced callers use it.
		taker = ctx.OpSource
	}

	out := make([]consumer.Event, 0, len(atoms))
	for i, atom := range atoms {
		trade, err := decodeClaimAtom(
			atom,
			ctx.Ledger,
			ctx.ClosedAt,
			ctx.TxHash,
			ctx.OpIndex,
			i,
			taker,
		)
		if err != nil {
			// Per-claim failure counted by the dispatcher; return
			// partial success so the other claims in this op still
			// land.
			continue
		}
		out = append(out, TradeEvent{Trade: trade})
	}
	return out, nil
}

// TradeEvent is the [consumer.Event] this source emits. Matches
// the pattern of soroswap.TradeEvent / aquarius.TradeEvent etc.
type TradeEvent struct {
	Trade canonical.Trade
}

// EventKind implements [consumer.Event].
func (TradeEvent) EventKind() string { return "sdex.trade" }

// Source implements [consumer.Event].
func (TradeEvent) Source() string { return SourceName }

// Compile-time checks — catches interface drift at build time.
var (
	_ dispatcher.OpDecoder = (*Decoder)(nil)
	_ consumer.Event       = TradeEvent{}
)
