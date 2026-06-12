package claimable_balances

import (
	"errors"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/StellarAtlas/stellar-atlas/internal/consumer"
	"github.com/StellarAtlas/stellar-atlas/internal/dispatcher"
)

// Observer is the dispatcher-facing ClaimableBalanceEntry
// observer per ADR-0022 PR 3/5. Implements
// [dispatcher.LedgerEntryChangeDecoder].
//
// Watched-asset driven via the same operator config the
// trustlines observer (#304) consumes. Removed-variant changes
// are filtered out at Match — see package doc for the
// asset-key-not-on-LedgerKey rationale.
type Observer struct {
	watched map[string]struct{}
}

var ErrEmptyWatchSet = errors.New("claimable_balances: cannot construct Observer with empty watched-asset list")

// NewObserver constructs an [Observer] watching the supplied
// asset_key list.
func NewObserver(watched []string) (*Observer, error) {
	if len(watched) == 0 {
		return nil, ErrEmptyWatchSet
	}
	set := make(map[string]struct{}, len(watched))
	for _, k := range watched {
		if k == "" {
			return nil, errors.New("claimable_balances: empty asset_key in watched list")
		}
		set[k] = struct{}{}
	}
	return &Observer{watched: set}, nil
}

func (*Observer) Name() string { return SourceName }

// Matches implements [dispatcher.LedgerEntryChangeDecoder].
// Returns true when:
//
//  1. The change touches a ClaimableBalance entry, AND
//  2. The change variant is Created / Updated / Restored
//     (Removed filtered out at v1; see package doc), AND
//  3. The claimable's asset is a classic credit, AND
//  4. The asset_key is in the watched set.
func (o *Observer) Matches(change xdr.LedgerEntryChange) bool {
	cb, ok := claimableFromChange(change)
	if !ok {
		return false
	}
	asset := cb.Asset
	if asset.Type != xdr.AssetTypeAssetTypeCreditAlphanum4 &&
		asset.Type != xdr.AssetTypeAssetTypeCreditAlphanum12 {
		return false
	}
	ak, err := assetKeyFromAsset(asset)
	if err != nil {
		return false
	}
	_, watched := o.watched[ak]
	return watched
}

// Decode implements [dispatcher.LedgerEntryChangeDecoder].
func (o *Observer) Decode(ctx dispatcher.LedgerEntryChangeContext) ([]consumer.Event, error) {
	id, ak, balance, err := extractFromChange(ctx.Change)
	if err != nil {
		return nil, err
	}
	return []consumer.Event{Observation{
		ClaimableID: id,
		AssetKey:    ak,
		Ledger:      ctx.Ledger,
		ObservedAt:  ctx.ClosedAt,
		Balance:     balance,
	}}, nil
}

// claimableFromChange returns the ClaimableBalanceEntry pointer
// for a Created / Updated / Restored change, or (nil, false)
// otherwise. Removed-variant changes return (nil, false) — they
// carry only a LedgerKey, not the entry body.
func claimableFromChange(change xdr.LedgerEntryChange) (*xdr.ClaimableBalanceEntry, bool) {
	var entry *xdr.LedgerEntry
	switch change.Type {
	case xdr.LedgerEntryChangeTypeLedgerEntryCreated:
		entry = change.Created
	case xdr.LedgerEntryChangeTypeLedgerEntryUpdated:
		entry = change.Updated
	case xdr.LedgerEntryChangeTypeLedgerEntryRestored:
		entry = change.Restored
	default:
		return nil, false
	}
	if entry == nil || entry.Data.Type != xdr.LedgerEntryTypeClaimableBalance {
		return nil, false
	}
	return entry.Data.ClaimableBalance, entry.Data.ClaimableBalance != nil
}

var _ dispatcher.LedgerEntryChangeDecoder = (*Observer)(nil)
