package defindex

import (
	"github.com/RatesEngine/rates-engine/internal/consumer"
	"github.com/RatesEngine/rates-engine/internal/events"
)

// Decoder implements dispatcher.Decoder (the event-based variant —
// not ContractCallDecoder). DeFindex vault contracts publish
// Soroban contract events on every user-facing flow (deposit /
// withdraw / rescue / rebalance / ...) so the standard event path
// is the right hook. Phase A only matches deposit + withdraw on
// the 3 known vaults.
//
// Stateless. Matching is O(1) — contract-ID set lookup + two
// byte-equal topic compares — before any SCVal parsing.
type Decoder struct {
	// vaults is the contract-ID set this decoder owns. Defaults to
	// [MainnetVaults] in production; tests can substitute a
	// per-test set.
	vaults map[string]struct{}
}

// NewDecoder constructs a vault-event decoder bound to the given
// contract-ID set. Mainnet callers pass [MainnetVaults]; tests can
// pass a smaller set.
func NewDecoder(vaults map[string]struct{}) *Decoder {
	return &Decoder{vaults: vaults}
}

// Name implements [dispatcher.Decoder].
func (d *Decoder) Name() string { return SourceName }

// Matches implements [dispatcher.Decoder]. Cheap predicate:
// contract ID is one of the configured vaults AND the topic shape
// matches a deposit/withdraw event. The dispatcher only calls
// Decode() when this returns true.
func (d *Decoder) Matches(ev events.Event) bool {
	if _, ok := d.vaults[ev.ContractID]; !ok {
		return false
	}
	return classify(&ev) != ""
}

// Decode implements [dispatcher.Decoder]. Emits one Event per
// matched vault flow. Returning an error is a "skip + count"
// signal per the dispatcher's contract — a malformed event doesn't
// abort the ledger, just gets dropped + counted.
func (d *Decoder) Decode(ev events.Event) ([]consumer.Event, error) {
	kind := classify(&ev)
	if kind == "" {
		// Defensive — Matches should have filtered.
		return nil, ErrUnknownEvent
	}
	flow, err := decodeFlow(&ev, kind)
	if err != nil {
		return nil, err
	}
	return []consumer.Event{Event{Flow: flow}}, nil
}
