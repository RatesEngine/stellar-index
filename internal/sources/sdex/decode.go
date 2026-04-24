package sdex

import (
	"fmt"
	"time"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// matchesTradeOp reports whether an op is one that could emit
// ClaimAtoms (a classic DEX trade). Covers every op type that
// stellar-core's ledger meta produces trades from.
func matchesTradeOp(op xdr.Operation) bool {
	switch op.Body.Type {
	case xdr.OperationTypeManageSellOffer,
		xdr.OperationTypeManageBuyOffer,
		xdr.OperationTypeCreatePassiveSellOffer,
		xdr.OperationTypePathPaymentStrictReceive,
		xdr.OperationTypePathPaymentStrictSend:
		return true
	}
	return false
}

// extractClaimAtoms pulls the OffersClaimed slice from whichever
// op-result variant this op produced. Returns nil when the op
// succeeded but matched no offers (no trade occurred), or when the
// op failed — both are "no trades," not an error.
func extractClaimAtoms(op xdr.Operation, result xdr.OperationResult) []xdr.ClaimAtom {
	if result.Code != xdr.OperationResultCodeOpInner {
		return nil
	}
	tr, ok := result.GetTr()
	if !ok {
		return nil
	}
	switch op.Body.Type {
	case xdr.OperationTypeManageSellOffer:
		r, ok := tr.GetManageSellOfferResult()
		if !ok || r.Code != xdr.ManageSellOfferResultCodeManageSellOfferSuccess {
			return nil
		}
		success := r.MustSuccess()
		return success.OffersClaimed

	case xdr.OperationTypeManageBuyOffer:
		r, ok := tr.GetManageBuyOfferResult()
		if !ok || r.Code != xdr.ManageBuyOfferResultCodeManageBuyOfferSuccess {
			return nil
		}
		success := r.MustSuccess()
		return success.OffersClaimed

	case xdr.OperationTypeCreatePassiveSellOffer:
		// CreatePassiveSellOffer shares the ManageSellOfferResult
		// union arm — same OffersClaimed shape.
		r, ok := tr.GetCreatePassiveSellOfferResult()
		if !ok || r.Code != xdr.ManageSellOfferResultCodeManageSellOfferSuccess {
			return nil
		}
		success := r.MustSuccess()
		return success.OffersClaimed

	case xdr.OperationTypePathPaymentStrictReceive:
		r, ok := tr.GetPathPaymentStrictReceiveResult()
		if !ok || r.Code != xdr.PathPaymentStrictReceiveResultCodePathPaymentStrictReceiveSuccess {
			return nil
		}
		success := r.MustSuccess()
		return success.Offers

	case xdr.OperationTypePathPaymentStrictSend:
		r, ok := tr.GetPathPaymentStrictSendResult()
		if !ok || r.Code != xdr.PathPaymentStrictSendResultCodePathPaymentStrictSendSuccess {
			return nil
		}
		success := r.MustSuccess()
		return success.Offers
	}
	return nil
}

// decodeClaimAtom turns one ClaimAtom into a canonical.Trade.
// tradeIndex is the 0-based position of this claim within the op
// — used to generate unique OpIndex values for multi-claim trades
// via a fanout stride (same pattern as aquarius + reflector).
//
// Taker is the tx-level source for the first claim, which matches
// what a human would read as "the account that placed this trade."
// (Subsequent claims in the same op are still that same taker —
// they're chained fills of a single offer-side action.)
func decodeClaimAtom(
	atom xdr.ClaimAtom,
	ledgerSeq uint32,
	closedAt time.Time,
	txHash string,
	opIdx int,
	tradeIndex int,
	takerAccount string,
) (canonical.Trade, error) {
	var (
		sellerAccount string
		soldAsset     xdr.Asset
		boughtAsset   xdr.Asset
		soldAmount    xdr.Int64
		boughtAmount  xdr.Int64
	)

	switch atom.Type {
	case xdr.ClaimAtomTypeClaimAtomTypeOrderBook:
		ob := atom.MustOrderBook()
		sellerAccount, _ = strkey.Encode(strkey.VersionByteAccountID, ob.SellerId.Ed25519[:])
		soldAsset = ob.AssetSold
		boughtAsset = ob.AssetBought
		soldAmount = ob.AmountSold
		boughtAmount = ob.AmountBought

	case xdr.ClaimAtomTypeClaimAtomTypeLiquidityPool:
		// Liquidity-pool claim: the counterparty is a classic
		// liquidity pool (NOT the Soroban AMMs) — identified by
		// its pool ID, not a G-address. We record the pool ID as
		// the Maker so analysts can distinguish order-book trades
		// from LP trades.
		lp := atom.MustLiquidityPool()
		sellerAccount = ""
		// PoolID is a Hash; encode as hex for the Maker field.
		poolHex := fmt.Sprintf("%x", lp.LiquidityPoolId)
		sellerAccount = poolHex
		soldAsset = lp.AssetSold
		boughtAsset = lp.AssetBought
		soldAmount = lp.AmountSold
		boughtAmount = lp.AmountBought

	case xdr.ClaimAtomTypeClaimAtomTypeV0:
		// Legacy — pre-CAP-27 shape. Shouldn't appear on recent
		// ledgers (protocol 18+), but surface rather than silently
		// drop so we notice if backfill replays old history.
		return canonical.Trade{}, fmt.Errorf("%w: ClaimAtomTypeV0 (legacy, pre-P18)",
			ErrUnknownClaimAtomType)

	default:
		return canonical.Trade{}, fmt.Errorf("%w: type=%d", ErrUnknownClaimAtomType, atom.Type)
	}

	if soldAmount <= 0 || boughtAmount <= 0 {
		return canonical.Trade{}, fmt.Errorf("%w: non-positive amounts sold=%d bought=%d",
			ErrMalformedClaimAtom, soldAmount, boughtAmount)
	}

	base, err := xdrAssetToCanonical(soldAsset)
	if err != nil {
		return canonical.Trade{}, fmt.Errorf("%w: sold asset: %w", ErrMalformedClaimAtom, err)
	}
	quote, err := xdrAssetToCanonical(boughtAsset)
	if err != nil {
		return canonical.Trade{}, fmt.Errorf("%w: bought asset: %w", ErrMalformedClaimAtom, err)
	}
	pair, err := canonical.NewPair(base, quote)
	if err != nil {
		return canonical.Trade{}, fmt.Errorf("%w: pair: %w", ErrMalformedClaimAtom, err)
	}

	return canonical.Trade{
		Source:      SourceName,
		Ledger:      ledgerSeq,
		TxHash:      txHash,
		OpIndex:     uint32(opIdx)*opIndexFanoutStride + uint32(tradeIndex),
		Timestamp:   closedAt,
		Pair:        pair,
		BaseAmount:  amountFromInt64(soldAmount),
		QuoteAmount: amountFromInt64(boughtAmount),
		Taker:       takerAccount,
		Maker:       sellerAccount,
	}, nil
}

// xdrAssetToCanonical converts an xdr.Asset to canonical.Asset.
// Handles the three classic asset variants: native, credit
// alphanum4, credit alphanum12. Soroban SAC-wrapped classic assets
// arrive here as classic — the SAC address is metadata, not
// canonical identity.
func xdrAssetToCanonical(a xdr.Asset) (canonical.Asset, error) {
	switch a.Type {
	case xdr.AssetTypeAssetTypeNative:
		return canonical.NativeAsset(), nil
	case xdr.AssetTypeAssetTypeCreditAlphanum4:
		a4 := a.MustAlphaNum4()
		code := trimTrailingNulls(a4.AssetCode[:])
		issuer, err := strkey.Encode(strkey.VersionByteAccountID, a4.Issuer.Ed25519[:])
		if err != nil {
			return canonical.Asset{}, fmt.Errorf("alphanum4 issuer: %w", err)
		}
		return canonical.NewClassicAsset(code, issuer)
	case xdr.AssetTypeAssetTypeCreditAlphanum12:
		a12 := a.MustAlphaNum12()
		code := trimTrailingNulls(a12.AssetCode[:])
		issuer, err := strkey.Encode(strkey.VersionByteAccountID, a12.Issuer.Ed25519[:])
		if err != nil {
			return canonical.Asset{}, fmt.Errorf("alphanum12 issuer: %w", err)
		}
		return canonical.NewClassicAsset(code, issuer)
	}
	return canonical.Asset{}, fmt.Errorf("unsupported asset type %d", a.Type)
}

// amountFromInt64 converts a classic-Stellar 7-decimal-scaled
// int64 amount (the XDR form) to canonical.Amount. No precision
// loss — classic amounts fit in int64.
func amountFromInt64(n xdr.Int64) canonical.Amount {
	return canonical.FromInt128Parts(int64(n>>63), uint64(int64(n)))
}

// trimTrailingNulls removes zero bytes from the end of a classic
// asset-code byte slice. AssetCode arrays are fixed-size (4 or 12
// bytes) with nulls padding short codes like "USDC" (4 bytes in a
// 4-array, no padding; "AQUA" in a 12-array has 8 nulls).
func trimTrailingNulls(b []byte) string {
	n := len(b)
	for n > 0 && b[n-1] == 0 {
		n--
	}
	return string(b[:n])
}

// opIndexFanoutStride spaces the synthetic op_index values for
// multi-claim operations. A single ManageOffer can cross many book
// levels in one op; each claim becomes a distinct Trade with a
// unique (source, ledger, tx_hash, op_index, ts) primary key.
//
// 1024 handles any plausible on-chain op (stellar caps ops-per-tx
// at 100, and classic DEX depth rarely exceeds a few dozen claims
// per aggressive trade). If we ever see this cap in the wild we
// have bigger problems.
const opIndexFanoutStride = 1024
