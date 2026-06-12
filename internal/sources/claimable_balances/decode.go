package claimable_balances

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"
)

var (
	ErrNotClaimable              = errors.New("claimable_balances: change is not a ClaimableBalanceEntry")
	ErrUnsupportedClaimableAsset = errors.New("claimable_balances: claimable asset is not a classic credit asset")
)

// extractFromChange returns (claimable_id, asset_key, balance)
// for a Created/Updated/Restored ClaimableBalanceEntry-delta.
// Removed variants are filtered out at the [Observer.Matches]
// level (see package doc).
func extractFromChange(change xdr.LedgerEntryChange) (claimableID, assetKey string, balance *big.Int, err error) {
	switch change.Type {
	case xdr.LedgerEntryChangeTypeLedgerEntryCreated:
		return decodePresent(change.Created.Data.ClaimableBalance)
	case xdr.LedgerEntryChangeTypeLedgerEntryUpdated:
		return decodePresent(change.Updated.Data.ClaimableBalance)
	case xdr.LedgerEntryChangeTypeLedgerEntryRestored:
		return decodePresent(change.Restored.Data.ClaimableBalance)
	}
	return "", "", nil, fmt.Errorf("%w: change type %d not supported by this observer", ErrNotClaimable, change.Type)
}

func decodePresent(cb *xdr.ClaimableBalanceEntry) (string, string, *big.Int, error) {
	if cb == nil {
		return "", "", nil, errors.New("claimable_balances: nil ClaimableBalanceEntry")
	}
	id, err := claimableIDHex(cb.BalanceId)
	if err != nil {
		return "", "", nil, err
	}
	ak, err := assetKeyFromAsset(cb.Asset)
	if err != nil {
		return "", "", nil, err
	}
	return id, ak, big.NewInt(int64(cb.Amount)), nil
}

// claimableIDHex encodes a ClaimableBalanceId to a hex string.
// Only the V0 variant is defined in current XDR; future variants
// would extend this switch.
func claimableIDHex(id xdr.ClaimableBalanceId) (string, error) {
	if id.V0 == nil {
		return "", fmt.Errorf("claimable_balances: ClaimableBalanceId variant %d has no V0 hash", id.Type)
	}
	return hex.EncodeToString(id.V0[:]), nil
}

// assetKeyFromAsset converts the XDR asset on a claimable
// balance to supply.AssetKey form (CODE:ISSUER). Native + any
// future non-credit variants return [ErrUnsupportedClaimableAsset].
func assetKeyFromAsset(a xdr.Asset) (string, error) {
	switch a.Type {
	case xdr.AssetTypeAssetTypeCreditAlphanum4:
		a4 := a.AlphaNum4
		if a4 == nil {
			return "", errors.New("claimable_balances: nil AlphaNum4 with discriminant CreditAlphanum4")
		}
		code := trimTrailingNulls(a4.AssetCode[:])
		pk, ok := a4.Issuer.GetEd25519()
		if !ok {
			return "", fmt.Errorf("claimable_balances: alphanum4 issuer not Ed25519 (type=%d)", a4.Issuer.Type)
		}
		issuer, err := strkey.Encode(strkey.VersionByteAccountID, pk[:])
		if err != nil {
			return "", fmt.Errorf("claimable_balances: alphanum4 issuer encode: %w", err)
		}
		return code + ":" + issuer, nil
	case xdr.AssetTypeAssetTypeCreditAlphanum12:
		a12 := a.AlphaNum12
		if a12 == nil {
			return "", errors.New("claimable_balances: nil AlphaNum12 with discriminant CreditAlphanum12")
		}
		code := trimTrailingNulls(a12.AssetCode[:])
		pk, ok := a12.Issuer.GetEd25519()
		if !ok {
			return "", fmt.Errorf("claimable_balances: alphanum12 issuer not Ed25519 (type=%d)", a12.Issuer.Type)
		}
		issuer, err := strkey.Encode(strkey.VersionByteAccountID, pk[:])
		if err != nil {
			return "", fmt.Errorf("claimable_balances: alphanum12 issuer encode: %w", err)
		}
		return code + ":" + issuer, nil
	case xdr.AssetTypeAssetTypeNative:
		return "", ErrUnsupportedClaimableAsset
	}
	return "", fmt.Errorf("%w: asset type %d", ErrUnsupportedClaimableAsset, a.Type)
}

// trimTrailingNulls strips zero bytes from the right side of an
// asset-code byte slice.
func trimTrailingNulls(b []byte) string {
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] != 0 {
			return string(b[:i+1])
		}
	}
	return ""
}
