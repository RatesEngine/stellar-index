package rozo

import (
	"fmt"

	"github.com/RatesEngine/rates-engine/internal/consumer"
	"github.com/RatesEngine/rates-engine/internal/dispatcher"
	"github.com/RatesEngine/rates-engine/internal/events"
)

// Decoder is the dispatcher-facing view of Rozo v1 Payment. It is a
// stateless topic Decoder — each payment / flush event decodes
// independently into one rozo_events row, no correlation state.
//
// Matching is by topic[0] symbol AND contract id. CLAUDE.md ("Comet
// uses a shared topic") warns that `symbol_short!` gives no protocol
// namespace, so another contract could emit "payment" / "flush" —
// Matches gates on the event coming from a known Rozo v1 contract.
type Decoder struct{}

// NewDecoder constructs a Rozo Decoder. Stateless — the returned
// value is safe to share.
func NewDecoder() *Decoder { return &Decoder{} }

// Compile-time check that *Decoder satisfies dispatcher.Decoder.
var _ dispatcher.Decoder = (*Decoder)(nil)

// rozoContracts is the set of v1 Payment contract C-strkeys this
// decoder claims, built from [MainnetPaymentContracts]. All three
// deployments emit the identical PaymentEvent / FlushEvent schema.
var rozoContracts = func() map[string]struct{} {
	m := make(map[string]struct{}, len(MainnetPaymentContracts))
	for _, c := range MainnetPaymentContracts {
		m[c] = struct{}{}
	}
	return m
}()

// IsRozoContract reports whether id is one of the known Rozo v1
// Payment contracts on Stellar mainnet.
func IsRozoContract(id string) bool {
	_, ok := rozoContracts[id]
	return ok
}

// Name implements [dispatcher.Decoder].
func (*Decoder) Name() string { return SourceName }

// Matches implements [dispatcher.Decoder]. Claims an event when its
// topic[0] is a Rozo v1 symbol AND it was emitted by a known Rozo
// contract.
func (*Decoder) Matches(ev events.Event) bool {
	return IsRozoContract(ev.ContractID) && Classify(&ev) != ""
}

// Decode implements [dispatcher.Decoder]. Emits exactly one [Event]
// per recognised Rozo event, or nothing for an event that doesn't
// match. A decode error is non-fatal per the dispatcher contract —
// counted and skipped.
func (*Decoder) Decode(ev events.Event) ([]consumer.Event, error) {
	kind := Classify(&ev)
	if kind == "" || !IsRozoContract(ev.ContractID) {
		return nil, nil
	}

	observedAt, err := ev.EventClosedAt()
	if err != nil {
		return nil, fmt.Errorf("rozo: %s: %w", kind, err)
	}

	switch kind {
	case EventPayment:
		p, err := DecodePayment(&ev)
		if err != nil {
			return nil, err
		}
		return []consumer.Event{eventFromPayment(p, observedAt)}, nil
	case EventFlush:
		f, err := DecodeFlush(&ev)
		if err != nil {
			return nil, err
		}
		return []consumer.Event{eventFromFlush(f, observedAt)}, nil
	}
	// Unreachable while Classify and this switch stay in lockstep —
	// Classify already returned non-empty above, and every kind it
	// can return has a case. Returning the sentinel makes the
	// defensive guard real: if a future Classify case lands without a
	// matching switch arm, the dispatcher counts it as a decode error
	// rather than silently dropping the event.
	return nil, fmt.Errorf("%w: %s", ErrUnknownEvent, kind)
}
