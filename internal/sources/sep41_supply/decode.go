package sep41_supply

import (
	"errors"
	"fmt"
	"math/big"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/StellarAtlas/stellar-atlas/internal/events"
	"github.com/StellarAtlas/stellar-atlas/internal/scval"
)

var (
	ErrUnknownSEP41Symbol = errors.New("sep41_supply: topic[0] is not a supply-affecting SEP-41 symbol")
	ErrShortTopic         = errors.New("sep41_supply: topic too short for event variant")
	ErrAmountNotI128      = errors.New("sep41_supply: event Value is not I128")
)

// classify returns the supply-event symbol for a Soroban event's
// topic[0] (one of mint / burn / clawback), or empty for any
// other topic. The pre-encoded base64 SCVal blobs in events.go
// make this a cheap string compare in the dispatch hot path.
func classify(ev *events.Event) string {
	if len(ev.Topic) == 0 {
		return ""
	}
	switch ev.Topic[0] {
	case TopicSymbolMint:
		return SymbolMint
	case TopicSymbolBurn:
		return SymbolBurn
	case TopicSymbolClawback:
		return SymbolClawback
	default:
		return ""
	}
}

// decodeAmount extracts the i128 Value (the event's amount) and
// converts it to *big.Int. Per ADR-0011 / ADR-0023 amounts are
// non-negative (the kind discriminates direction); the storage
// writer rejects negatives upstream, so this just guards against
// SDK quirks.
func decodeAmount(ev *events.Event) (*big.Int, error) {
	sv, err := scval.Parse(ev.Value)
	if err != nil {
		return nil, fmt.Errorf("sep41_supply: parse Value: %w", err)
	}
	if sv.Type != xdr.ScValTypeScvI128 {
		return nil, fmt.Errorf("%w: got %s", ErrAmountNotI128, sv.Type)
	}
	amt, err := scval.AsAmountFromI128(sv)
	if err != nil {
		return nil, fmt.Errorf("sep41_supply: i128 → amount: %w", err)
	}
	out := amt.BigInt()
	if out.Sign() < 0 {
		return nil, fmt.Errorf("sep41_supply: negative amount %s (kind discriminates direction)", out)
	}
	return out, nil
}

// decodeCounterparty extracts the recipient (mint) or holder
// (burn / clawback) Address from the topic vector. Topic[0] is
// the event symbol; the counterparty position varies per kind:
//
//	mint     topic[2] = to        (topic[1] is admin)
//	burn     topic[1] = from
//	clawback topic[2] = from      (topic[1] is admin)
//
// Returns the strkey form. Older SEP-41 implementations may emit
// shorter topic vectors; we surface ErrShortTopic so the caller
// can drop the row rather than write garbage.
func decodeCounterparty(ev *events.Event, kind string) (string, error) {
	var idx int
	switch kind {
	case SymbolMint, SymbolClawback:
		idx = 2
	case SymbolBurn:
		idx = 1
	default:
		return "", fmt.Errorf("%w: %q", ErrUnknownSEP41Symbol, kind)
	}
	if len(ev.Topic) <= idx {
		return "", fmt.Errorf("%w: %s expects topic[%d], got len=%d", ErrShortTopic, kind, idx, len(ev.Topic))
	}
	sv, err := scval.Parse(ev.Topic[idx])
	if err != nil {
		return "", fmt.Errorf("sep41_supply: parse topic[%d]: %w", idx, err)
	}
	addr, err := scval.AsAddressStrkey(sv)
	if err != nil {
		return "", fmt.Errorf("sep41_supply: counterparty address at topic[%d]: %w", idx, err)
	}
	return addr, nil
}
