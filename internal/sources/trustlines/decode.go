package trustlines

import (
	"errors"
	"fmt"
	"math/big"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// Errors returned by the decoder. Match-time errors mean
// "no Observation will be emitted for this change" rather than
// "fatal" — see [Observer.Matches].
var (
	// ErrNotTrustline — change touches a non-Trustline entry. The
	// Match pre-filter blocks these before Decode runs; guarded
	// here as defence-in-depth.
	ErrNotTrustline = errors.New("trustlines: change is not a TrustLineEntry")

	// ErrUnsupportedTrustLineAsset — Trustline references either
	// native XLM (Algorithm 1, covered by accounts observer) or a
	// pool-share asset (covered by liquidity_pools observer at
	// Task #65). Algorithm 2's trustline component is for classic
	// credits only; other variants are skipped at Match time.
	ErrUnsupportedTrustLineAsset = errors.New("trustlines: TrustLineAsset is not a classic credit asset")
)

// extractTrustlineFromChange returns (account_id, asset_key,
// balance, is_removal) for a TrustlineEntry-delta. Match has
// already filtered out non-Trustline / non-classic-credit
// changes; this function does the value extraction.
//
// Removed-variant changes use the LedgerKey to recover identity;
// balance is zero (the entry no longer exists post-change).
func extractTrustlineFromChange(change xdr.LedgerEntryChange) (accountID, assetKey string, balance *big.Int, isRemoval bool, err error) {
	switch change.Type {
	case xdr.LedgerEntryChangeTypeLedgerEntryCreated:
		tl := change.Created.Data.TrustLine
		return decodePresentTrustline(tl, false)
	case xdr.LedgerEntryChangeTypeLedgerEntryUpdated:
		tl := change.Updated.Data.TrustLine
		return decodePresentTrustline(tl, false)
	case xdr.LedgerEntryChangeTypeLedgerEntryRestored:
		tl := change.Restored.Data.TrustLine
		return decodePresentTrustline(tl, false)
	case xdr.LedgerEntryChangeTypeLedgerEntryRemoved:
		key := change.Removed.TrustLine
		acct, err := strkeyFromAccountID(key.AccountId)
		if err != nil {
			return "", "", nil, true, err
		}
		ak, err := assetKeyFromTrustLineAsset(key.Asset)
		if err != nil {
			return "", "", nil, true, err
		}
		return acct, ak, big.NewInt(0), true, nil
	}
	return "", "", nil, false, fmt.Errorf("%w: unknown change type %d", ErrNotTrustline, change.Type)
}

func decodePresentTrustline(tl *xdr.TrustLineEntry, removal bool) (string, string, *big.Int, bool, error) {
	if tl == nil {
		return "", "", nil, removal, errors.New("trustlines: nil TrustLineEntry")
	}
	acct, err := strkeyFromAccountID(tl.AccountId)
	if err != nil {
		return "", "", nil, removal, err
	}
	ak, err := assetKeyFromTrustLineAsset(tl.Asset)
	if err != nil {
		return "", "", nil, removal, err
	}
	return acct, ak, big.NewInt(int64(tl.Balance)), removal, nil
}

// assetKeyFromTrustLineAsset converts the XDR asset variant on a
// TrustlineEntry to the supply.AssetKey form (CODE:ISSUER).
//
// Native + pool-share asset types return [ErrUnsupportedTrustLineAsset]
// — those entries shouldn't reach Decode (Match filters them) but
// the guard ensures a misbehaving call doesn't write garbage.
func assetKeyFromTrustLineAsset(a xdr.TrustLineAsset) (string, error) {
	switch a.Type {
	case xdr.AssetTypeAssetTypeCreditAlphanum4:
		a4 := a.AlphaNum4
		if a4 == nil {
			return "", errors.New("trustlines: nil AlphaNum4 with discriminant CreditAlphanum4")
		}
		code := trimTrailingNulls(a4.AssetCode[:])
		issuer, err := strkey.Encode(strkey.VersionByteAccountID, a4.Issuer.Ed25519[:])
		if err != nil {
			return "", fmt.Errorf("trustlines: alphanum4 issuer encode: %w", err)
		}
		return code + ":" + issuer, nil
	case xdr.AssetTypeAssetTypeCreditAlphanum12:
		a12 := a.AlphaNum12
		if a12 == nil {
			return "", errors.New("trustlines: nil AlphaNum12 with discriminant CreditAlphanum12")
		}
		code := trimTrailingNulls(a12.AssetCode[:])
		issuer, err := strkey.Encode(strkey.VersionByteAccountID, a12.Issuer.Ed25519[:])
		if err != nil {
			return "", fmt.Errorf("trustlines: alphanum12 issuer encode: %w", err)
		}
		return code + ":" + issuer, nil
	case xdr.AssetTypeAssetTypeNative,
		xdr.AssetTypeAssetTypePoolShare:
		return "", ErrUnsupportedTrustLineAsset
	}
	return "", fmt.Errorf("%w: type %d", ErrUnsupportedTrustLineAsset, a.Type)
}

func strkeyFromAccountID(aid xdr.AccountId) (string, error) {
	pk, ok := aid.GetEd25519()
	if !ok {
		return "", fmt.Errorf("trustlines: AccountId is not Ed25519 (type=%d)", aid.Type)
	}
	return strkey.Encode(strkey.VersionByteAccountID, pk[:])
}

// trimTrailingNulls strips zero bytes from the right side of an
// asset-code byte slice. AssetCode arrays are fixed-size (4 or
// 12 bytes) so shorter codes are zero-padded on the wire.
func trimTrailingNulls(b []byte) string {
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] != 0 {
			return string(b[:i+1])
		}
	}
	return ""
}
