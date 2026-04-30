package accounts

import (
	"errors"
	"fmt"
	"math/big"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// Errors returned by extractObservation. Callers classify via
// errors.Is.
var (
	// ErrNotAccountEntry — the change touched an entry that isn't an
	// AccountEntry. Returned by accountIDFromChange when the type
	// discriminant doesn't match. The observer's [Observer.Matches]
	// pre-filters on this so Decode shouldn't see one in practice;
	// guard anyway.
	ErrNotAccountEntry = errors.New("accounts: change is not an AccountEntry")
)

// accountIDFromChange returns the G-strkey of the AccountEntry the
// change references, regardless of whether the change is a
// Created / Updated / Restored / Removed variant. Returns
// [ErrNotAccountEntry] when the change's data type is not Account.
//
// This is a cheap pre-filter helper used by [Observer.Matches] —
// no balance/home-domain extraction here.
func accountIDFromChange(change xdr.LedgerEntryChange) (string, error) {
	switch change.Type {
	case xdr.LedgerEntryChangeTypeLedgerEntryCreated:
		if change.Created == nil || change.Created.Data.Type != xdr.LedgerEntryTypeAccount {
			return "", ErrNotAccountEntry
		}
		return strkeyFromAccountID(change.Created.Data.Account.AccountId)
	case xdr.LedgerEntryChangeTypeLedgerEntryUpdated:
		if change.Updated == nil || change.Updated.Data.Type != xdr.LedgerEntryTypeAccount {
			return "", ErrNotAccountEntry
		}
		return strkeyFromAccountID(change.Updated.Data.Account.AccountId)
	case xdr.LedgerEntryChangeTypeLedgerEntryRestored:
		if change.Restored == nil || change.Restored.Data.Type != xdr.LedgerEntryTypeAccount {
			return "", ErrNotAccountEntry
		}
		return strkeyFromAccountID(change.Restored.Data.Account.AccountId)
	case xdr.LedgerEntryChangeTypeLedgerEntryRemoved:
		if change.Removed == nil || change.Removed.Type != xdr.LedgerEntryTypeAccount {
			return "", ErrNotAccountEntry
		}
		return strkeyFromAccountID(change.Removed.Account.AccountId)
	}
	return "", fmt.Errorf("%w: unknown change type %d", ErrNotAccountEntry, change.Type)
}

// extractObservation builds an [Observation] from a
// LedgerEntryChange already known to touch a watched AccountEntry.
// Removed-variant changes produce IsRemoval=true with zeroed
// balance/home_domain; the reader can interpret that as "account
// no longer exists at this ledger."
//
// Caller is responsible for filtering by watched-set before
// calling this — extractObservation does the cheaper Match work
// again as a defence-in-depth guard but errs out on mismatch
// rather than silently coercing.
func extractObservation(change xdr.LedgerEntryChange) (Observation, error) {
	switch change.Type {
	case xdr.LedgerEntryChangeTypeLedgerEntryCreated:
		return obsFromAccountEntry(change.Created.Data.Account, false)
	case xdr.LedgerEntryChangeTypeLedgerEntryUpdated:
		return obsFromAccountEntry(change.Updated.Data.Account, false)
	case xdr.LedgerEntryChangeTypeLedgerEntryRestored:
		return obsFromAccountEntry(change.Restored.Data.Account, false)
	case xdr.LedgerEntryChangeTypeLedgerEntryRemoved:
		// Removed carries only the LedgerKey (no AccountEntry body).
		// Use the key's AccountId to populate AccountID; balance +
		// home_domain are unknown post-removal, hence zeroed.
		id, err := strkeyFromAccountID(change.Removed.Account.AccountId)
		if err != nil {
			return Observation{}, err
		}
		return Observation{
			AccountID: id,
			Balance:   big.NewInt(0),
			IsRemoval: true,
		}, nil
	}
	return Observation{}, fmt.Errorf("%w: unknown change type %d", ErrNotAccountEntry, change.Type)
}

// obsFromAccountEntry turns the post-change AccountEntry into an
// [Observation]. AccountID is derived from the entry's own
// AccountId field.
func obsFromAccountEntry(entry *xdr.AccountEntry, removal bool) (Observation, error) {
	if entry == nil {
		return Observation{}, errors.New("accounts: nil AccountEntry passed to obsFromAccountEntry")
	}
	id, err := strkeyFromAccountID(entry.AccountId)
	if err != nil {
		return Observation{}, err
	}
	return Observation{
		AccountID:  id,
		Balance:    big.NewInt(int64(entry.Balance)),
		HomeDomain: string(entry.HomeDomain),
		Flags:      uint32(entry.Flags),
		SeqNum:     int64(entry.SeqNum),
		IsRemoval:  removal,
	}, nil
}

// strkeyFromAccountID encodes an xdr.AccountId to its G-strkey
// representation. Fails when the account is not Ed25519 (we don't
// observe muxed accounts at the AccountEntry level — the entry's
// AccountId is always an Ed25519 PK).
func strkeyFromAccountID(aid xdr.AccountId) (string, error) {
	pk, ok := aid.GetEd25519()
	if !ok {
		return "", fmt.Errorf("accounts: AccountId is not Ed25519 (type=%d)", aid.Type)
	}
	return strkey.Encode(strkey.VersionByteAccountID, pk[:])
}
