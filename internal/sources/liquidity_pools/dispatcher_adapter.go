package liquidity_pools

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/StellarAtlas/stellar-atlas/internal/consumer"
	"github.com/StellarAtlas/stellar-atlas/internal/dispatcher"
)

// Observer is the dispatcher-facing LiquidityPoolEntry observer
// per ADR-0022 PR 4/5. Implements
// [dispatcher.LedgerEntryChangeDecoder].
//
// Watched-asset driven via the same operator config the
// trustlines + claimable observers consume.
type Observer struct {
	watched map[string]struct{}
}

var (
	ErrEmptyWatchSet      = errors.New("liquidity_pools: cannot construct Observer with empty watched-asset list")
	ErrUnsupportedLPType  = errors.New("liquidity_pools: only ConstantProduct LP entries supported at v1")
	ErrUnsupportedLPAsset = errors.New("liquidity_pools: LP asset is not a classic credit asset")
)

func NewObserver(watched []string) (*Observer, error) {
	if len(watched) == 0 {
		return nil, ErrEmptyWatchSet
	}
	set := make(map[string]struct{}, len(watched))
	for _, k := range watched {
		if k == "" {
			return nil, errors.New("liquidity_pools: empty asset_key in watched list")
		}
		set[k] = struct{}{}
	}
	return &Observer{watched: set}, nil
}

func (*Observer) Name() string { return SourceName }

// Matches returns true when:
//
//  1. The change is Created/Updated/Restored on a LiquidityPool
//     entry (Removed filtered out at v1; same rationale as
//     claimable_balances), AND
//  2. The pool is ConstantProduct (only LP variant today), AND
//  3. At least one side of the asset pair is in the watched set.
func (o *Observer) Matches(change xdr.LedgerEntryChange) bool {
	lp, ok := lpFromChange(change)
	if !ok {
		return false
	}
	if lp.Body.Type != xdr.LiquidityPoolTypeLiquidityPoolConstantProduct {
		return false
	}
	cp := lp.Body.ConstantProduct
	if cp == nil {
		return false
	}
	for _, a := range []xdr.Asset{cp.Params.AssetA, cp.Params.AssetB} {
		ak, err := assetKeyFromAsset(a)
		if err != nil {
			continue
		}
		if _, watched := o.watched[ak]; watched {
			return true
		}
	}
	return false
}

// Decode implements [dispatcher.LedgerEntryChangeDecoder]. Emits
// one Observation per asset side that's in the watched set —
// a pool with both sides watched produces two Observations; one
// side watched produces one; neither (Match would have skipped).
func (o *Observer) Decode(ctx dispatcher.LedgerEntryChangeContext) ([]consumer.Event, error) {
	lp, ok := lpFromChange(ctx.Change)
	if !ok || lp.Body.ConstantProduct == nil {
		return nil, ErrUnsupportedLPType
	}
	cp := lp.Body.ConstantProduct
	poolID := hex.EncodeToString(lp.LiquidityPoolId[:])

	var outs []consumer.Event
	for _, side := range []struct {
		asset   xdr.Asset
		reserve xdr.Int64
	}{
		{cp.Params.AssetA, cp.ReserveA},
		{cp.Params.AssetB, cp.ReserveB},
	} {
		ak, err := assetKeyFromAsset(side.asset)
		if err != nil {
			continue // native + unsupported variants — skip silently
		}
		if _, watched := o.watched[ak]; !watched {
			continue
		}
		outs = append(outs, Observation{
			PoolID:     poolID,
			AssetKey:   ak,
			Ledger:     ctx.Ledger,
			ObservedAt: ctx.ClosedAt,
			Balance:    big.NewInt(int64(side.reserve)),
		})
	}
	return outs, nil
}

func lpFromChange(change xdr.LedgerEntryChange) (*xdr.LiquidityPoolEntry, bool) {
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
	if entry == nil || entry.Data.Type != xdr.LedgerEntryTypeLiquidityPool {
		return nil, false
	}
	return entry.Data.LiquidityPool, entry.Data.LiquidityPool != nil
}

func assetKeyFromAsset(a xdr.Asset) (string, error) {
	switch a.Type {
	case xdr.AssetTypeAssetTypeCreditAlphanum4:
		a4 := a.AlphaNum4
		if a4 == nil {
			return "", errors.New("liquidity_pools: nil AlphaNum4")
		}
		code := trimTrailingNulls(a4.AssetCode[:])
		pk, ok := a4.Issuer.GetEd25519()
		if !ok {
			return "", fmt.Errorf("liquidity_pools: alphanum4 issuer not Ed25519 (type=%d)", a4.Issuer.Type)
		}
		issuer, err := strkey.Encode(strkey.VersionByteAccountID, pk[:])
		if err != nil {
			return "", fmt.Errorf("liquidity_pools: alphanum4 issuer: %w", err)
		}
		return code + ":" + issuer, nil
	case xdr.AssetTypeAssetTypeCreditAlphanum12:
		a12 := a.AlphaNum12
		if a12 == nil {
			return "", errors.New("liquidity_pools: nil AlphaNum12")
		}
		code := trimTrailingNulls(a12.AssetCode[:])
		pk, ok := a12.Issuer.GetEd25519()
		if !ok {
			return "", fmt.Errorf("liquidity_pools: alphanum12 issuer not Ed25519 (type=%d)", a12.Issuer.Type)
		}
		issuer, err := strkey.Encode(strkey.VersionByteAccountID, pk[:])
		if err != nil {
			return "", fmt.Errorf("liquidity_pools: alphanum12 issuer: %w", err)
		}
		return code + ":" + issuer, nil
	}
	return "", ErrUnsupportedLPAsset
}

func trimTrailingNulls(b []byte) string {
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] != 0 {
			return string(b[:i+1])
		}
	}
	return ""
}

var _ dispatcher.LedgerEntryChangeDecoder = (*Observer)(nil)
