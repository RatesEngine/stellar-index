package blend_backstop

import (
	"github.com/StellarIndex/stellar-index/internal/consumer"
	"github.com/StellarIndex/stellar-index/internal/dispatcher"
	"github.com/StellarIndex/stellar-index/internal/events"
)

// Decoder is the dispatcher-facing view of the Blend Backstop. It is a
// stateless topic Decoder — each of the ten backstop events decodes
// independently into one blend_backstop_events row; there is no
// cross-event correlation (unlike Soroswap's swap/sync pairing).
//
// Matching is by topic[0] symbol AND contract id. The backstop's
// symbols (claim / withdraw / queue_withdrawal / gulp_emissions)
// OVERLAP with Blend POOL event symbols, so the contract-id gate is
// LOAD-BEARING — never match on the symbol alone (CLAUDE.md "Comet
// uses a shared topic" + ADR-0035 factory-anchored gating).
type Decoder struct{}

// NewDecoder constructs a Backstop Decoder. Stateless — the returned
// value is safe to share.
func NewDecoder() *Decoder { return &Decoder{} }

// Compile-time check that *Decoder satisfies dispatcher.Decoder.
var _ dispatcher.Decoder = (*Decoder)(nil)

// backstopContracts is the set of contract C-strkeys whose events this
// decoder claims — both the V2 singleton and the V1 deployment that
// preceded it (a backfill range would replay either). A redeploy is an
// operator-visible event, so a hard-coded set is the right shape.
var backstopContracts = map[string]struct{}{
	MainnetBackstopV2: {},
	MainnetBackstopV1: {},
}

// IsBackstopContract reports whether id is one of the known Blend
// Backstop contracts on Stellar mainnet (V1 or V2).
func IsBackstopContract(id string) bool {
	_, ok := backstopContracts[id]
	return ok
}

// Name implements [dispatcher.Decoder].
func (*Decoder) Name() string { return SourceName }

// Matches implements [dispatcher.Decoder]. Claims an event when its
// topic[0] is one of the ten backstop symbols AND it was emitted by a
// known backstop contract. The contract gate disambiguates the
// symbol-overlap with Blend pool events.
func (*Decoder) Matches(ev events.Event) bool {
	return IsBackstopContract(ev.ContractID) && Classify(&ev) != ""
}

// Decode implements [dispatcher.Decoder]. Emits exactly one [Event]
// per recognised backstop event, or nothing for an event that doesn't
// match (the dispatcher already filtered via Matches, but Decode
// re-checks so a direct caller is safe). A decode error is non-fatal
// per the dispatcher contract — counted and skipped.
func (*Decoder) Decode(ev events.Event) ([]consumer.Event, error) {
	if !IsBackstopContract(ev.ContractID) || Classify(&ev) == "" {
		return nil, nil
	}
	return project(&ev)
}
