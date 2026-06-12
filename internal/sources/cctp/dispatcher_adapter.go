package cctp

import (
	"fmt"

	"github.com/RatesEngine/rates-engine/internal/consumer"
	"github.com/RatesEngine/rates-engine/internal/dispatcher"
	"github.com/RatesEngine/rates-engine/internal/events"
)

// Decoder is the dispatcher-facing view of Circle CCTP v2. It is a
// stateless topic Decoder — unlike Soroswap there is no swap/sync
// correlation: each of the four CCTP events decodes independently
// into one cctp_events row. The deposit_for_burn ↔ message_sent
// pairing the architecture doc describes is a downstream concern,
// correlatable later by (ledger, tx_hash); the decoder does not buffer.
//
// Matching is by topic[0] symbol AND contract id. CLAUDE.md ("Comet
// uses a shared topic") warns that another contract could emit the
// same symbol bytes, so Matches also gates on the event coming from
// one of the three known CCTP contracts.
type Decoder struct{}

// NewDecoder constructs a CCTP Decoder. Stateless — the returned
// value is safe to share.
func NewDecoder() *Decoder { return &Decoder{} }

// Compile-time check that *Decoder satisfies dispatcher.Decoder.
var _ dispatcher.Decoder = (*Decoder)(nil)

// cctpContracts is the set of contract C-strkeys whose events this
// decoder claims. Live ingest only ever sees the current mainnet
// deployment; the set is small and a redeploy is an operator-visible
// event, so a hard-coded set is the right shape (matching the
// arch doc's Option A — contract-id filtering downstream of the
// topic match).
var cctpContracts = map[string]struct{}{
	MainnetTokenMessengerMinter: {},
	MainnetMessageTransmitter:   {},
	MainnetCctpForwarder:        {},
}

// IsCCTPContract reports whether id is one of the known Circle CCTP v2
// contracts on Stellar mainnet.
func IsCCTPContract(id string) bool {
	_, ok := cctpContracts[id]
	return ok
}

// Name implements [dispatcher.Decoder].
func (*Decoder) Name() string { return SourceName }

// Matches implements [dispatcher.Decoder]. Claims an event when its
// topic[0] is one of the four CCTP symbols AND it was emitted by a
// known CCTP contract.
func (*Decoder) Matches(ev events.Event) bool {
	return IsCCTPContract(ev.ContractID) && Classify(&ev) != ""
}

// Decode implements [dispatcher.Decoder]. Emits exactly one
// [Event] per recognised CCTP event, or nothing for an event that
// doesn't match (the dispatcher already filtered via Matches, but
// Decode re-checks so a direct caller is safe). A decode error is
// non-fatal per the dispatcher contract — counted and skipped.
func (*Decoder) Decode(ev events.Event) ([]consumer.Event, error) {
	kind := Classify(&ev)
	if kind == "" || !IsCCTPContract(ev.ContractID) {
		return nil, nil
	}

	observedAt, err := ev.EventClosedAt()
	if err != nil {
		return nil, fmt.Errorf("cctp: %s: %w", kind, err)
	}

	switch kind {
	case EventDepositForBurn:
		d, err := DecodeDepositForBurn(&ev)
		if err != nil {
			return nil, err
		}
		return []consumer.Event{eventFromDepositForBurn(d, observedAt)}, nil
	case EventMintAndWithdraw:
		m, err := DecodeMintAndWithdraw(&ev)
		if err != nil {
			return nil, err
		}
		return []consumer.Event{eventFromMintAndWithdraw(m, observedAt)}, nil
	case EventMessageSent:
		s, err := DecodeMessageSent(&ev)
		if err != nil {
			return nil, err
		}
		return []consumer.Event{eventFromMessageSent(s, observedAt)}, nil
	case EventMessageReceived:
		r, err := DecodeMessageReceived(&ev)
		if err != nil {
			return nil, err
		}
		return []consumer.Event{eventFromMessageReceived(r, observedAt)}, nil
	}
	// Unreachable while Classify and this switch stay in lockstep —
	// Classify already returned non-empty above, and every kind it
	// can return has a case. Returning the sentinel makes the
	// defensive guard real: if a future Classify case lands without a
	// matching switch arm, the dispatcher counts it as a decode error
	// rather than silently dropping the event.
	return nil, fmt.Errorf("%w: %s", ErrUnknownEvent, kind)
}
