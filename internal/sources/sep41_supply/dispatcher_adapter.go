package sep41_supply

import (
	"errors"
	"fmt"

	"github.com/RatesEngine/rates-engine/internal/consumer"
	"github.com/RatesEngine/rates-engine/internal/events"
)

// Decoder is the dispatcher-facing SEP-41 supply observer per
// ADR-0023. Implements [dispatcher.Decoder] (the events-based
// hook). Operator-watched-set driven via [NewDecoder].
type Decoder struct {
	// watched maps SEP-41 contract C-strkey → struct{}{}. Map
	// lookup is O(1) per event; the watched set is bounded by
	// operator config (typically single-digit contracts at v1).
	watched map[string]struct{}
}

// ErrEmptyWatchSet is returned by [NewDecoder] when the input
// list is empty. A decoder with no contracts to watch is a
// configuration error — operators that don't want SEP-41 supply
// observation should simply not register the decoder.
var ErrEmptyWatchSet = errors.New("sep41_supply: cannot construct Decoder with empty watched-contract list")

// NewDecoder constructs a Decoder watching the supplied
// SEP-41 contract C-strkey list. Empty strings are rejected as
// a configuration error.
func NewDecoder(watched []string) (*Decoder, error) {
	if len(watched) == 0 {
		return nil, ErrEmptyWatchSet
	}
	set := make(map[string]struct{}, len(watched))
	for _, c := range watched {
		if c == "" {
			return nil, errors.New("sep41_supply: empty contract id in watched list")
		}
		set[c] = struct{}{}
	}
	return &Decoder{watched: set}, nil
}

// Name implements [dispatcher.Decoder].
func (*Decoder) Name() string { return SourceName }

// Matches implements [dispatcher.Decoder]. Returns true when:
//
//  1. The event is a contract event from a watched contract id.
//  2. topic[0] decodes to one of mint / burn / clawback.
//
// Transfers are explicitly NOT matched — they don't affect total
// supply (the discovery sniffer in internal/canonical/discovery
// records transfers separately, for the discovered_assets
// surface). Match is cheap: contract-id map lookup + base64
// byte-equality on topic[0].
func (d *Decoder) Matches(ev events.Event) bool {
	if ev.Type != "contract" {
		return false
	}
	if _, watched := d.watched[ev.ContractID]; !watched {
		return false
	}
	return classify(&ev) != ""
}

// Decode implements [dispatcher.Decoder]. Emits exactly one
// [Event] per matched event.
func (d *Decoder) Decode(ev events.Event) ([]consumer.Event, error) {
	kind := classify(&ev)
	if kind == "" {
		return nil, fmt.Errorf("%w: topic[0]=%q", ErrUnknownSEP41Symbol, firstTopic(&ev))
	}
	amount, err := decodeAmount(&ev)
	if err != nil {
		return nil, err
	}
	counterparty, err := decodeCounterparty(&ev, kind)
	if err != nil {
		return nil, err
	}
	closedAt, err := ev.EventClosedAt()
	if err != nil {
		return nil, fmt.Errorf("sep41_supply: parse closed-at: %w", err)
	}
	return []consumer.Event{Event{
		ContractID:   ev.ContractID,
		Ledger:       ev.Ledger,
		TxHash:       ev.TxHash,
		OpIndex:      uint32(ev.OperationIndex), //nolint:gosec // OperationIndex is non-negative by Soroban spec; uint32 cast is safe.
		ObservedAt:   closedAt,
		Kind:         kind,
		Amount:       amount,
		Counterparty: counterparty,
	}}, nil
}

func firstTopic(e *events.Event) string {
	if len(e.Topic) == 0 {
		return "<empty>"
	}
	return e.Topic[0]
}
