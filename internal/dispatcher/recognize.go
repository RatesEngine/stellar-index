package dispatcher

import "github.com/StellarAtlas/stellar-atlas/internal/events"

// Recognize reports whether any registered event decoder claims this
// event, returning the matching decoder's name. It runs the SAME
// Matches() predicates the live dispatch walk uses, but with NO decode
// and NO side effects (it does not touch eventsSeen / unmatchedHits).
//
// This is the oracle for ADR-0033 Claim 2a (recognition): feed it the
// distinct (contract_id, topic) shapes actually present in
// soroban_events and any shape it returns false for is an on-chain
// event the system would silently drop — a recognition gap. Because it
// uses the real Matches() logic rather than a hand-maintained topic
// list, it cannot drift from what the decoders actually handle.
//
// ContractCallDecoders are intentionally excluded: they bind to
// InvokeContract op args, emit no Soroban events, and so never produce
// soroban_events rows to recognize.
func (d *Dispatcher) Recognize(ev events.Event) (string, bool) {
	for _, dec := range d.decoders {
		if dec.Matches(ev) {
			return dec.Name(), true
		}
	}
	return "", false
}
