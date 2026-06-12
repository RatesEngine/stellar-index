package trustlines

import (
	"errors"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/StellarAtlas/stellar-atlas/internal/consumer"
	"github.com/StellarAtlas/stellar-atlas/internal/dispatcher"
)

// Observer is the dispatcher-facing TrustlineEntry observer per
// ADR-0022 PR 2/5. Implements
// [dispatcher.LedgerEntryChangeDecoder].
//
// Watched-asset driven: the observer only fires on changes
// touching trustlines whose asset_key is in the operator-
// configured watched set. Non-classic-credit Trustline variants
// (native, pool-share) are skipped at the Match level.
type Observer struct {
	// watched maps asset_key (CODE:ISSUER) → struct{}{}. Map
	// lookup is O(1) per change; the watched set is bounded by
	// operator config, typically single-digit assets at v1.
	watched map[string]struct{}
}

// ErrEmptyWatchSet is returned by [NewObserver] when the input
// list is empty. An observer with no assets to watch is a
// configuration error — operators that don't want trustline
// observation should simply not register the observer.
var ErrEmptyWatchSet = errors.New("trustlines: cannot construct Observer with empty watched-asset list")

// NewObserver constructs an [Observer] watching the supplied
// asset_key list. Asset keys are deduplicated; empty strings
// rejected as a configuration error.
func NewObserver(watched []string) (*Observer, error) {
	if len(watched) == 0 {
		return nil, ErrEmptyWatchSet
	}
	set := make(map[string]struct{}, len(watched))
	for _, k := range watched {
		if k == "" {
			return nil, errors.New("trustlines: empty asset_key in watched list")
		}
		set[k] = struct{}{}
	}
	return &Observer{watched: set}, nil
}

// Name implements [dispatcher.LedgerEntryChangeDecoder].
func (*Observer) Name() string { return SourceName }

// Matches implements [dispatcher.LedgerEntryChangeDecoder].
// Returns true when:
//
//  1. The change touches a TrustlineEntry, AND
//  2. The trustline's asset is a classic credit (alphanum4/12;
//     native + pool-share filtered out), AND
//  3. The asset_key is in the watched set.
//
// Match work is bounded — type discriminator, Asset variant,
// asset_key derivation, map lookup. No NUMERIC parsing or
// balance extraction.
func (o *Observer) Matches(change xdr.LedgerEntryChange) bool {
	if !changeIsTrustline(change) {
		return false
	}
	asset, ok := trustlineAssetFromChange(change)
	if !ok {
		return false
	}
	if !isClassicCreditAsset(asset.Type) {
		return false
	}
	ak, err := assetKeyFromTrustLineAsset(asset)
	if err != nil {
		return false
	}
	_, watched := o.watched[ak]
	return watched
}

// Decode implements [dispatcher.LedgerEntryChangeDecoder].
// Emits exactly one [Observation] per matched change.
func (o *Observer) Decode(ctx dispatcher.LedgerEntryChangeContext) ([]consumer.Event, error) {
	accountID, assetKey, balance, isRemoval, err := extractTrustlineFromChange(ctx.Change)
	if err != nil {
		return nil, err
	}
	return []consumer.Event{Observation{
		AccountID:  accountID,
		AssetKey:   assetKey,
		Ledger:     ctx.Ledger,
		ObservedAt: ctx.ClosedAt,
		Balance:    balance,
		IsRemoval:  isRemoval,
	}}, nil
}

// changeIsTrustline reports whether the change's data type is
// Trustline, regardless of the change variant (Created /
// Updated / Restored / Removed). Cheap pre-filter.
func changeIsTrustline(change xdr.LedgerEntryChange) bool {
	switch change.Type {
	case xdr.LedgerEntryChangeTypeLedgerEntryCreated:
		return change.Created != nil && change.Created.Data.Type == xdr.LedgerEntryTypeTrustline
	case xdr.LedgerEntryChangeTypeLedgerEntryUpdated:
		return change.Updated != nil && change.Updated.Data.Type == xdr.LedgerEntryTypeTrustline
	case xdr.LedgerEntryChangeTypeLedgerEntryRestored:
		return change.Restored != nil && change.Restored.Data.Type == xdr.LedgerEntryTypeTrustline
	case xdr.LedgerEntryChangeTypeLedgerEntryRemoved:
		return change.Removed != nil && change.Removed.Type == xdr.LedgerEntryTypeTrustline
	}
	return false
}

// trustlineAssetFromChange extracts the TrustLineAsset from a
// change already known to touch a Trustline entry.
func trustlineAssetFromChange(change xdr.LedgerEntryChange) (xdr.TrustLineAsset, bool) {
	switch change.Type {
	case xdr.LedgerEntryChangeTypeLedgerEntryCreated:
		return change.Created.Data.TrustLine.Asset, true
	case xdr.LedgerEntryChangeTypeLedgerEntryUpdated:
		return change.Updated.Data.TrustLine.Asset, true
	case xdr.LedgerEntryChangeTypeLedgerEntryRestored:
		return change.Restored.Data.TrustLine.Asset, true
	case xdr.LedgerEntryChangeTypeLedgerEntryRemoved:
		return change.Removed.TrustLine.Asset, true
	}
	return xdr.TrustLineAsset{}, false
}

func isClassicCreditAsset(t xdr.AssetType) bool {
	return t == xdr.AssetTypeAssetTypeCreditAlphanum4 ||
		t == xdr.AssetTypeAssetTypeCreditAlphanum12
}

// Compile-time check.
var _ dispatcher.LedgerEntryChangeDecoder = (*Observer)(nil)
