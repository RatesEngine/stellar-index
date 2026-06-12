package rozo

import (
	"errors"
	"fmt"

	"github.com/StellarAtlas/stellar-atlas/internal/events"
	"github.com/StellarAtlas/stellar-atlas/internal/scval"
)

// ErrUnknownEvent flags an event whose topic[0] symbol doesn't
// match either of the two v1 Payment topics this decoder handles.
// The dispatcher routes events to this decoder when topic[0]
// matches the registered Symbol bytes; this is a defensive
// double-check for the (rare) case where the dispatcher gets a
// topic-bytes match against a different protocol that happens to
// use the same symbol (Soroban's symbol_short! gives no protocol
// namespace, so collisions are possible — see CLAUDE.md's
// "Comet uses a shared ('POOL', <event>) topic" warning).
var ErrUnknownEvent = errors.New("rozo: unknown event topic")

// ErrMalformedBody flags a v1 Payment / Flush event whose ScMap
// body doesn't have the field names the contract docs specify.
// Per the contract source (v1/stellar/payment/src/lib.rs), the
// fields are stable: { from, destination, amount, memo } for
// PaymentEvent, { token, destination, amount } for FlushEvent.
// A mismatch surfaces here rather than silently producing a
// half-populated event.
var ErrMalformedBody = errors.New("rozo: malformed event body")

// Classify reports which v1 event the given Event is, or empty
// string if the topic doesn't match. The dispatcher uses this to
// route to the right decoder; classify is also exported so tests
// can verify topic-byte equality at the package boundary.
//
// Contract-id filtering happens DOWNSTREAM — the topic
// `symbol_short!("payment")` could collide with any future
// contract emitting the same symbol bytes, so the consumer is
// expected to drop events whose ContractID isn't a known Rozo
// contract before invoking the decoder.
func Classify(e *events.Event) string {
	if len(e.Topic) < 1 {
		return ""
	}
	switch e.Topic[0] {
	case TopicSymbolPayment:
		return EventPayment
	case TopicSymbolFlush:
		return EventFlush
	}
	return ""
}

// DecodePayment turns one PaymentEvent-shaped Event into a
// canonical Payment value.
//
// On-wire body shape (ScMap from Soroban's #[contracttype] macro):
//
//	{ "amount": ScVal(I128), "destination": ScVal(Address),
//	  "from": ScVal(Address), "memo": ScVal(String) }
//
// Field order in the ScMap is alphabetical (Soroban macro
// behaviour); the decoder uses scval.MustMapField for explicit
// field lookup so ordering changes don't break us.
func DecodePayment(e *events.Event) (Payment, error) {
	body, err := scval.Parse(e.Value)
	if err != nil {
		return Payment{}, fmt.Errorf("rozo: payment body parse: %w", err)
	}
	entries, err := scval.AsMap(body)
	if err != nil {
		return Payment{}, fmt.Errorf("rozo: payment body not a map: %w", err)
	}

	fromSV, err := scval.MustMapField(entries, "from")
	if err != nil {
		return Payment{}, fmt.Errorf("%w: missing 'from': %w", ErrMalformedBody, err)
	}
	from, err := scval.AsAddressStrkey(fromSV)
	if err != nil {
		return Payment{}, fmt.Errorf("rozo: payment 'from' address: %w", err)
	}

	destSV, err := scval.MustMapField(entries, "destination")
	if err != nil {
		return Payment{}, fmt.Errorf("%w: missing 'destination': %w", ErrMalformedBody, err)
	}
	destination, err := scval.AsAddressStrkey(destSV)
	if err != nil {
		return Payment{}, fmt.Errorf("rozo: payment 'destination' address: %w", err)
	}

	amountSV, err := scval.MustMapField(entries, "amount")
	if err != nil {
		return Payment{}, fmt.Errorf("%w: missing 'amount': %w", ErrMalformedBody, err)
	}
	amount, err := scval.AsAmountFromI128(amountSV)
	if err != nil {
		return Payment{}, fmt.Errorf("rozo: payment 'amount' i128: %w", err)
	}

	memoSV, err := scval.MustMapField(entries, "memo")
	if err != nil {
		return Payment{}, fmt.Errorf("%w: missing 'memo': %w", ErrMalformedBody, err)
	}
	memo, err := scval.AsString(memoSV)
	if err != nil {
		return Payment{}, fmt.Errorf("rozo: payment 'memo' string: %w", err)
	}

	return Payment{
		Ledger:      e.Ledger,
		TxHash:      e.TxHash,
		OpIndex:     e.OperationIndex,
		ClosedAt:    e.LedgerClosedAt,
		ContractID:  e.ContractID,
		From:        from,
		Destination: destination,
		Amount:      amount.String(),
		Memo:        memo,
	}, nil
}

// DecodeFlush turns one FlushEvent-shaped Event into a canonical
// Flush value.
//
// On-wire body shape (ScMap):
//
//	{ "amount": ScVal(I128), "destination": ScVal(Address),
//	  "token": ScVal(Address) }
func DecodeFlush(e *events.Event) (Flush, error) {
	body, err := scval.Parse(e.Value)
	if err != nil {
		return Flush{}, fmt.Errorf("rozo: flush body parse: %w", err)
	}
	entries, err := scval.AsMap(body)
	if err != nil {
		return Flush{}, fmt.Errorf("rozo: flush body not a map: %w", err)
	}

	tokenSV, err := scval.MustMapField(entries, "token")
	if err != nil {
		return Flush{}, fmt.Errorf("%w: missing 'token': %w", ErrMalformedBody, err)
	}
	token, err := scval.AsAddressStrkey(tokenSV)
	if err != nil {
		return Flush{}, fmt.Errorf("rozo: flush 'token' address: %w", err)
	}

	destSV, err := scval.MustMapField(entries, "destination")
	if err != nil {
		return Flush{}, fmt.Errorf("%w: missing 'destination': %w", ErrMalformedBody, err)
	}
	destination, err := scval.AsAddressStrkey(destSV)
	if err != nil {
		return Flush{}, fmt.Errorf("rozo: flush 'destination' address: %w", err)
	}

	amountSV, err := scval.MustMapField(entries, "amount")
	if err != nil {
		return Flush{}, fmt.Errorf("%w: missing 'amount': %w", ErrMalformedBody, err)
	}
	amount, err := scval.AsAmountFromI128(amountSV)
	if err != nil {
		return Flush{}, fmt.Errorf("rozo: flush 'amount' i128: %w", err)
	}

	return Flush{
		Ledger:      e.Ledger,
		TxHash:      e.TxHash,
		OpIndex:     e.OperationIndex,
		ClosedAt:    e.LedgerClosedAt,
		ContractID:  e.ContractID,
		Token:       token,
		Destination: destination,
		Amount:      amount.String(),
	}, nil
}
