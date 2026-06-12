package accounts

import (
	"errors"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/StellarAtlas/stellar-atlas/internal/consumer"
	"github.com/StellarAtlas/stellar-atlas/internal/dispatcher"
)

// Observer is the dispatcher-facing AccountEntry observer per
// ADR-0021. Implements [dispatcher.LedgerEntryChangeDecoder].
//
// Watched-set driven: the observer only fires on changes touching
// G-strkeys explicitly listed in operator config — see
// [NewObserver]. Switching to a "watch every account" mode is a
// non-trivial table-size decision that needs its own ADR; the
// current scope is the operator-curated set (SDF reserves +
// classic-asset issuers + future validator accounts).
type Observer struct {
	// watched maps G-strkey → true. Map lookup is O(1) per change;
	// the watched set is typically small (single-digit accounts at
	// v1).
	watched map[string]struct{}
}

// ErrEmptyWatchSet is returned by [NewObserver] when the input
// list is empty. An observer with no accounts to watch is a
// configuration error — operators that don't want the observer
// active should simply not register it with the dispatcher.
var ErrEmptyWatchSet = errors.New("accounts: cannot construct Observer with empty watched-account list")

// NewObserver constructs an [Observer] watching the supplied
// G-strkey list. The list is deduplicated; empty strings are
// rejected as a configuration error to surface mistyped TOML
// before live ingest.
func NewObserver(watched []string) (*Observer, error) {
	if len(watched) == 0 {
		return nil, ErrEmptyWatchSet
	}
	set := make(map[string]struct{}, len(watched))
	for _, acc := range watched {
		if acc == "" {
			return nil, errors.New("accounts: empty G-strkey in watched-account list")
		}
		set[acc] = struct{}{}
	}
	return &Observer{watched: set}, nil
}

// Name implements [dispatcher.LedgerEntryChangeDecoder].
func (*Observer) Name() string { return SourceName }

// Matches implements [dispatcher.LedgerEntryChangeDecoder]. Returns
// true only when:
//
//  1. The change touches an AccountEntry (not Trustline /
//     ContractCode / etc.), AND
//  2. The AccountEntry's AccountId is in the watched-set.
//
// Both checks are cheap — type discrimination + map lookup. No
// SCVal parsing or balance extraction at the match step (Decode
// does that work after the match wins).
func (o *Observer) Matches(change xdr.LedgerEntryChange) bool {
	id, err := accountIDFromChange(change)
	if err != nil {
		return false
	}
	_, watched := o.watched[id]
	return watched
}

// Decode implements [dispatcher.LedgerEntryChangeDecoder]. Emits
// exactly one [Observation] per matched change.
func (o *Observer) Decode(ctx dispatcher.LedgerEntryChangeContext) ([]consumer.Event, error) {
	obs, err := extractObservation(ctx.Change)
	if err != nil {
		return nil, err
	}
	obs.Ledger = ctx.Ledger
	obs.ObservedAt = ctx.ClosedAt
	return []consumer.Event{obs}, nil
}

// Compile-time check.
var _ dispatcher.LedgerEntryChangeDecoder = (*Observer)(nil)
