package phoenix

import (
	"fmt"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/stellarrpc"
)

// RawSwap is the partial set of fields observed for a single swap.
// We fill it as the 8 distinct events arrive (Q1). Arrival order
// is NOT guaranteed; we populate slots by field-name and check
// completeness via [SwapFieldCount].
type RawSwap struct {
	Ledger   uint32
	TxHash   string
	OpIndex  uint32
	Pool     string    // event.ContractID of the first arriving event
	ClosedAt time.Time

	// Populated slots. A nil-valued slot means we haven't seen that
	// field yet.
	Sender         *stellarrpc.Event
	SellToken      *stellarrpc.Event
	OfferAmount    *stellarrpc.Event
	ActualReceived *stellarrpc.Event
	BuyToken       *stellarrpc.Event
	ReturnAmount   *stellarrpc.Event
	SpreadAmount   *stellarrpc.Event
	ReferralFee    *stellarrpc.Event
}

// Complete reports whether all 8 slots are populated.
func (r *RawSwap) Complete() bool {
	return r.Sender != nil &&
		r.SellToken != nil &&
		r.OfferAmount != nil &&
		r.ActualReceived != nil &&
		r.BuyToken != nil &&
		r.ReturnAmount != nil &&
		r.SpreadAmount != nil &&
		r.ReferralFee != nil
}

// fieldsPresent returns the count of populated slots. Diagnostic
// helper used by the orphan reporter.
func (r *RawSwap) fieldsPresent() int {
	n := 0
	for _, p := range [...]*stellarrpc.Event{
		r.Sender, r.SellToken, r.OfferAmount, r.ActualReceived,
		r.BuyToken, r.ReturnAmount, r.SpreadAmount, r.ReferralFee,
	} {
		if p != nil {
			n++
		}
	}
	return n
}

// assign stores e in the slot identified by topic[1]. Returns
// ErrUnknownField for non-swap-field events — the caller skips
// those.
func (r *RawSwap) assign(e *stellarrpc.Event, fieldTopic string) error {
	switch fieldTopic {
	case TopicSymbolSender:
		r.Sender = e
	case TopicSymbolSellToken:
		r.SellToken = e
	case TopicSymbolOfferAmount:
		r.OfferAmount = e
	case TopicSymbolActualReceived:
		r.ActualReceived = e
	case TopicSymbolBuyToken:
		r.BuyToken = e
	case TopicSymbolReturnAmount:
		r.ReturnAmount = e
	case TopicSymbolSpreadAmount:
		r.SpreadAmount = e
	case TopicSymbolReferralFee:
		r.ReferralFee = e
	default:
		return fmt.Errorf("%w: %q", ErrUnknownField, fieldTopic)
	}
	return nil
}

// groupKey is the (ledger, tx_hash, op_index) triple — a single
// swap operation's events all share this key (Q4 multihops split
// on op_index naturally).
type groupKey struct {
	Ledger  uint32
	TxHash  string
	OpIndex uint32
}

func keyOf(e *stellarrpc.Event) groupKey {
	return groupKey{Ledger: e.Ledger, TxHash: e.TxHash, OpIndex: uint32(e.OperationIndex)}
}

// classify identifies a Phoenix swap event by matching
// (topic[0], topic[1]). Returns the topic[1] blob when this is a
// swap-field event; returns "" otherwise.
func classify(e *stellarrpc.Event) (fieldTopic string, isSwap bool) {
	if len(e.Topic) < 2 {
		return "", false
	}
	if e.Topic[0] != TopicSymbolSwap {
		return "", false
	}
	return e.Topic[1], true
}

// decodeSwap finalises a complete RawSwap into a canonical.Trade.
// Field mapping (per Q3):
//   - Trade.Pair.Base    = asset parsed from SellToken event body
//   - Trade.Pair.Quote   = asset parsed from BuyToken event body
//   - Trade.BaseAmount   = OfferAmount
//   - Trade.QuoteAmount  = ActualReceived (after fees — what actually changed hands)
//   - Trade.Taker        = sender address
func decodeSwap(r *RawSwap) (canonical.Trade, error) {
	if !r.Complete() {
		return canonical.Trade{}, fmt.Errorf("%w: have %d/8 fields",
			ErrIncompleteSwap, r.fieldsPresent())
	}

	sender, err := decodeAddress(r.Sender.Value)
	if err != nil {
		return canonical.Trade{}, fmt.Errorf("sender: %w", err)
	}
	sellToken, err := decodeAsset(r.SellToken.Value)
	if err != nil {
		return canonical.Trade{}, fmt.Errorf("sell_token: %w", err)
	}
	buyToken, err := decodeAsset(r.BuyToken.Value)
	if err != nil {
		return canonical.Trade{}, fmt.Errorf("buy_token: %w", err)
	}
	offer, err := decodeI128(r.OfferAmount.Value)
	if err != nil {
		return canonical.Trade{}, fmt.Errorf("offer_amount: %w", err)
	}
	received, err := decodeI128(r.ActualReceived.Value)
	if err != nil {
		return canonical.Trade{}, fmt.Errorf("actual received amount: %w", err)
	}

	if offer.Sign() <= 0 || received.Sign() <= 0 {
		return canonical.Trade{}, fmt.Errorf("%w: non-positive amount (offer %s / received %s)",
			ErrMalformedPayload, offer, received)
	}

	pair, err := canonical.NewPair(sellToken, buyToken)
	if err != nil {
		return canonical.Trade{}, fmt.Errorf("pair: %w", err)
	}

	return canonical.Trade{
		Source:      SourceName,
		Ledger:      r.Ledger,
		TxHash:      r.TxHash,
		OpIndex:     r.OpIndex,
		Timestamp:   r.ClosedAt,
		Pair:        pair,
		BaseAmount:  offer,
		QuoteAmount: received,
		Taker:       sender,
	}, nil
}

// ─── Stubs awaiting the SDK-backed decoder ─────────────────────────
// Tests swap these via the package-level vars.

var (
	decodeAddress = stubDecodeAddress // SCVal::Address → "G..." / "C..."
	decodeAsset   = stubDecodeAsset   // SCVal::Address → canonical.Asset
	decodeI128    = stubDecodeI128    // SCVal::I128 → canonical.Amount
)

func stubDecodeAddress(valueB64 string) (string, error) {
	return "", fmt.Errorf("phoenix: SCVal address decoder not yet installed (TODO(#0))")
}

func stubDecodeAsset(valueB64 string) (canonical.Asset, error) {
	return canonical.Asset{}, fmt.Errorf("phoenix: SCVal asset decoder not yet installed (TODO(#0))")
}

func stubDecodeI128(valueB64 string) (canonical.Amount, error) {
	return canonical.Amount{}, fmt.Errorf("phoenix: SCVal i128 decoder not yet installed (TODO(#0))")
}
