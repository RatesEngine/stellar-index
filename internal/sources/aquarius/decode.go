package aquarius

import (
	"fmt"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/events"
	"github.com/RatesEngine/rates-engine/internal/scval"
)

// aquariusTopicArity is the topic-count on every Aquarius trade
// event: [Symbol("trade"), Address(token_in), Address(token_out),
// Address(user)].
const aquariusTopicArity = 4

// classify picks the event kind from topic[0]. Returns "" for
// non-Aquarius events so the caller skips cheaply.
func classify(e *events.Event) string {
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
	default:
		return ""
	}
}

// decodeTrade decodes an Aquarius `trade` event into a single
// canonical.Trade. Unlike the earlier stub, this decoder needs NO
// pool-info cache — token identities are carried directly in the
// event topics.
//
// Verified against aquarius-amm/liquidity_pool_events/src/lib.rs:122-150
// (soroban-sdk 25.0.2):
//
//	e.events().publish(
//	    (Symbol::new(e, "trade"), token_in, token_out, user),
//	    (in_amount as i128, out_amount as i128, fee_amount as i128),
//	);
//
// Topics (4):
//
//	topic[0] = Symbol("trade")
//	topic[1] = Address(token_in)  — sold_asset
//	topic[2] = Address(token_out) — bought_asset
//	topic[3] = Address(user)      — trader (often a router contract)
//
// Body: Vec<ScVal> of length 3 = [i128, i128, i128] —
// (sold_amount, bought_amount, fee). soroban-sdk serializes
// tuple-shaped event bodies as ScvVec (NOT Map, which is only used
// for named-field struct bodies via #[contracttype]).
func decodeTrade(e *events.Event, closedAt time.Time) (canonical.Trade, error) {
	if len(e.Topic) != aquariusTopicArity {
		return canonical.Trade{}, fmt.Errorf("%w: expected %d topics, got %d",
			ErrMalformedPayload, aquariusTopicArity, len(e.Topic))
	}
	soldAsset, err := decodeAssetTopic(e.Topic[1])
	if err != nil {
		return canonical.Trade{}, fmt.Errorf("%w: token_in: %w", ErrMalformedPayload, err)
	}
	boughtAsset, err := decodeAssetTopic(e.Topic[2])
	if err != nil {
		return canonical.Trade{}, fmt.Errorf("%w: token_out: %w", ErrMalformedPayload, err)
	}
	userAddr, err := decodeAddressTopic(e.Topic[3])
	if err != nil {
		return canonical.Trade{}, fmt.Errorf("%w: user: %w", ErrMalformedPayload, err)
	}

	amounts, err := decodeTradeAmounts(e.Value)
	if err != nil {
		return canonical.Trade{}, fmt.Errorf("%w: %w", ErrMalformedPayload, err)
	}

	if amounts.SoldAmount.Sign() <= 0 || amounts.BoughtAmount.Sign() <= 0 {
		return canonical.Trade{}, fmt.Errorf("%w: non-positive amounts sold=%s bought=%s",
			ErrMalformedPayload, amounts.SoldAmount, amounts.BoughtAmount)
	}

	pair, err := canonical.NewPair(soldAsset, boughtAsset)
	if err != nil {
		return canonical.Trade{}, fmt.Errorf("pair: %w", err)
	}

	return canonical.Trade{
		Source:      SourceName,
		Ledger:      e.Ledger,
		TxHash:      e.TxHash,
		OpIndex:     uint32(e.OperationIndex),
		Timestamp:   closedAt,
		Pair:        pair,
		BaseAmount:  amounts.SoldAmount,
		QuoteAmount: amounts.BoughtAmount,
		Taker:       userAddr,
	}, nil
}

// tradeAmounts holds the three i128 values from a trade body.
type tradeAmounts struct {
	SoldAmount   canonical.Amount
	BoughtAmount canonical.Amount
	Fee          canonical.Amount
}

// ─── Real SCVal decoders ────────────────────────────────────────
// Tests swap these via the package-level vars.

var (
	decodeTradeAmounts = sdkDecodeTradeAmounts
	decodeAssetTopic   = sdkDecodeAssetTopic
	decodeAddressTopic = sdkDecodeAddressTopic
)

// sdkDecodeTradeAmounts unpacks the body Vec of 3 i128s.
//
// The contract emits the body as a Rust tuple `(i128, i128, i128)` —
// soroban-sdk serializes this as ScvVec of length 3, in positional
// order (sold, bought, fee). Unlike Map-based bodies we cannot
// decode by field name here; we rely on arity to detect a future
// contract upgrade that changes the tuple shape.
func sdkDecodeTradeAmounts(valueB64 string) (tradeAmounts, error) {
	body, err := scval.Parse(valueB64)
	if err != nil {
		return tradeAmounts{}, fmt.Errorf("parse body: %w", err)
	}
	elts, err := scval.AsTupleN(body, 3)
	if err != nil {
		return tradeAmounts{}, fmt.Errorf("body not a 3-tuple: %w", err)
	}
	sold, err := scval.AsAmountFromI128(elts[0])
	if err != nil {
		return tradeAmounts{}, fmt.Errorf("sold_amount: %w", err)
	}
	bought, err := scval.AsAmountFromI128(elts[1])
	if err != nil {
		return tradeAmounts{}, fmt.Errorf("bought_amount: %w", err)
	}
	fee, err := scval.AsAmountFromI128(elts[2])
	if err != nil {
		return tradeAmounts{}, fmt.Errorf("fee: %w", err)
	}
	return tradeAmounts{SoldAmount: sold, BoughtAmount: bought, Fee: fee}, nil
}

// sdkDecodeAssetTopic converts a topic-slot Address into a
// canonical.Asset. Aquarius only lists Soroban tokens (SAC-wrapped
// or contract-deployed), never symbolic/fiat references, so the
// conversion is unconditional Soroban.
func sdkDecodeAssetTopic(topicB64 string) (canonical.Asset, error) {
	sv, err := scval.Parse(topicB64)
	if err != nil {
		return canonical.Asset{}, fmt.Errorf("parse topic: %w", err)
	}
	addr, err := scval.AsAddressStrkey(sv)
	if err != nil {
		return canonical.Asset{}, err
	}
	return canonical.NewSorobanAsset(addr)
}

// sdkDecodeAddressTopic decodes a topic-slot Address into its
// strkey form. Used for the trader slot — may be a G-strkey (user
// account) or C-strkey (router/contract).
func sdkDecodeAddressTopic(topicB64 string) (string, error) {
	sv, err := scval.Parse(topicB64)
	if err != nil {
		return "", fmt.Errorf("parse topic: %w", err)
	}
	return scval.AsAddressStrkey(sv)
}
