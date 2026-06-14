package sep41_supply

import (
	"errors"
	"fmt"
	"math/big"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/StellarIndex/stellar-index/internal/events"
	"github.com/StellarIndex/stellar-index/internal/scval"
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
// (burn / clawback) Address from the topic vector. Topic[0] is the
// event symbol; the counterparty POSITION depends on the on-chain
// SHAPE, which changed across protocol versions — and the topic
// COUNT alone does not disambiguate, so we branch on the TYPE of
// topic[2]:
//
//	mint / clawback
//	  legacy SAC     ["mint", admin(Addr), to(Addr)]            → counterparty = topic[2]
//	  CAP-67 / spec  ["mint", to(Addr), sep0011_asset(String)]  → counterparty = topic[1]
//	  bare SEP-41    ["mint", to(Addr)]                          → counterparty = topic[1]
//	burn             ["burn", from(Addr) (, sep0011_asset)]      → counterparty = topic[1] (all shapes)
//
// Discriminator: if topic[2] decodes as an Address, it is the legacy
// admin-prefixed form and the counterparty is topic[2]; otherwise
// topic[2] is the sep0011_asset String (CAP-67 / Whisk, mainnet
// 2025-09-03) — or absent (bare spec) — and the counterparty is
// topic[1]. Verified against the r1 lake (2026-06-15): 99.96% of
// recent mints + 100% of clawbacks are the CAP-67 shape, which the
// previous fixed-topic[2] decode DROPPED entirely (AsAddressStrkey
// returns ErrScValType on the String → the whole row was lost →
// total_supply under-counted). burn's topic[1] was correct all along.
//
// Older / shorter topic vectors surface ErrShortTopic so the caller
// drops the row rather than writing garbage.
func decodeCounterparty(ev *events.Event, kind string) (string, error) {
	switch kind {
	case SymbolBurn:
		return addressAtTopic(ev, 1)
	case SymbolMint, SymbolClawback:
		// Legacy admin-prefixed form iff topic[2] is itself an Address;
		// CAP-67 puts the sep0011_asset String there instead.
		if len(ev.Topic) >= 3 {
			if sv, err := scval.Parse(ev.Topic[2]); err == nil && sv.Type == xdr.ScValTypeScvAddress {
				return scval.AsAddressStrkey(sv)
			}
		}
		// CAP-67 (["mint", to, sep0011_asset]) or bare spec (["mint", to]).
		return addressAtTopic(ev, 1)
	default:
		return "", fmt.Errorf("%w: %q", ErrUnknownSEP41Symbol, kind)
	}
}

// addressAtTopic parses topic[idx] as an Address strkey, surfacing
// ErrShortTopic when the vector is too short.
func addressAtTopic(ev *events.Event, idx int) (string, error) {
	if len(ev.Topic) <= idx {
		return "", fmt.Errorf("%w: expects topic[%d], got len=%d", ErrShortTopic, idx, len(ev.Topic))
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
