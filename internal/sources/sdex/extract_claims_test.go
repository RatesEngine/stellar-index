package sdex

import (
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/xdr"

	c "github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/dispatcher"
)

// extractClaimAtoms covers five op variants. The pre-existing
// decode_test.go suite hits ManageSellOffer happy + the failed-op
// branch; this file fills in the other four (ManageBuyOffer,
// CreatePassiveSellOffer, PathPaymentStrictReceive,
// PathPaymentStrictSend) plus the LiquidityPool ClaimAtom variant
// and the native/alphanum12 asset paths in xdrAssetToCanonical.

// ─── op-variant fixtures ──────────────────────────────────────

func mkManageBuyOfferOp(claims []xdr.ClaimAtom) (xdr.Operation, xdr.OperationResult) {
	op := xdr.Operation{
		Body: xdr.OperationBody{Type: xdr.OperationTypeManageBuyOffer},
	}
	result := xdr.OperationResult{
		Code: xdr.OperationResultCodeOpInner,
		Tr: &xdr.OperationResultTr{
			Type: xdr.OperationTypeManageBuyOffer,
			ManageBuyOfferResult: &xdr.ManageBuyOfferResult{
				Code: xdr.ManageBuyOfferResultCodeManageBuyOfferSuccess,
				Success: &xdr.ManageOfferSuccessResult{
					OffersClaimed: claims,
				},
			},
		},
	}
	return op, result
}

func mkCreatePassiveSellOfferOp(claims []xdr.ClaimAtom) (xdr.Operation, xdr.OperationResult) {
	op := xdr.Operation{
		Body: xdr.OperationBody{Type: xdr.OperationTypeCreatePassiveSellOffer},
	}
	result := xdr.OperationResult{
		Code: xdr.OperationResultCodeOpInner,
		Tr: &xdr.OperationResultTr{
			Type: xdr.OperationTypeCreatePassiveSellOffer,
			CreatePassiveSellOfferResult: &xdr.ManageSellOfferResult{
				Code: xdr.ManageSellOfferResultCodeManageSellOfferSuccess,
				Success: &xdr.ManageOfferSuccessResult{
					OffersClaimed: claims,
				},
			},
		},
	}
	return op, result
}

func mkPathPaymentStrictReceiveOp(claims []xdr.ClaimAtom) (xdr.Operation, xdr.OperationResult) {
	op := xdr.Operation{
		Body: xdr.OperationBody{Type: xdr.OperationTypePathPaymentStrictReceive},
	}
	result := xdr.OperationResult{
		Code: xdr.OperationResultCodeOpInner,
		Tr: &xdr.OperationResultTr{
			Type: xdr.OperationTypePathPaymentStrictReceive,
			PathPaymentStrictReceiveResult: &xdr.PathPaymentStrictReceiveResult{
				Code: xdr.PathPaymentStrictReceiveResultCodePathPaymentStrictReceiveSuccess,
				Success: &xdr.PathPaymentStrictReceiveResultSuccess{
					Offers: claims,
				},
			},
		},
	}
	return op, result
}

func mkPathPaymentStrictSendOp(claims []xdr.ClaimAtom) (xdr.Operation, xdr.OperationResult) {
	op := xdr.Operation{
		Body: xdr.OperationBody{Type: xdr.OperationTypePathPaymentStrictSend},
	}
	result := xdr.OperationResult{
		Code: xdr.OperationResultCodeOpInner,
		Tr: &xdr.OperationResultTr{
			Type: xdr.OperationTypePathPaymentStrictSend,
			PathPaymentStrictSendResult: &xdr.PathPaymentStrictSendResult{
				Code: xdr.PathPaymentStrictSendResultCodePathPaymentStrictSendSuccess,
				Success: &xdr.PathPaymentStrictSendResultSuccess{
					Offers: claims,
				},
			},
		},
	}
	return op, result
}

func mkLiquidityPoolClaim(soldAsset, boughtAsset xdr.Asset, soldAmount, boughtAmount int64) xdr.ClaimAtom {
	var poolID xdr.PoolId
	for i := range poolID {
		poolID[i] = byte(i ^ 0x55)
	}
	return xdr.ClaimAtom{
		Type: xdr.ClaimAtomTypeClaimAtomTypeLiquidityPool,
		LiquidityPool: &xdr.ClaimLiquidityAtom{
			LiquidityPoolId: poolID,
			AssetSold:       soldAsset,
			AmountSold:      xdr.Int64(soldAmount),
			AssetBought:     boughtAsset,
			AmountBought:    xdr.Int64(boughtAmount),
		},
	}
}

// ─── extractClaimAtoms: the four extra op types ───────────────

func TestExtractClaimAtoms_managBuyOffer_returnsClaims(t *testing.T) {
	xlm := xdr.Asset{Type: xdr.AssetTypeAssetTypeNative}
	usdc := mkAlphanum4Asset(t, "USDC", 0x10)
	claim := mkOrderBookClaim(t, 0x20, 1, xlm, usdc, 1_000_000_000, 12_420_000)

	op, result := mkManageBuyOfferOp([]xdr.ClaimAtom{claim})
	got := extractClaimAtoms(op, result)
	if len(got) != 1 {
		t.Fatalf("got %d claims, want 1", len(got))
	}
}

func TestExtractClaimAtoms_createPassiveSellOffer_returnsClaims(t *testing.T) {
	xlm := xdr.Asset{Type: xdr.AssetTypeAssetTypeNative}
	usdc := mkAlphanum4Asset(t, "USDC", 0x10)
	claim := mkOrderBookClaim(t, 0x20, 1, xlm, usdc, 1, 1)

	op, result := mkCreatePassiveSellOfferOp([]xdr.ClaimAtom{claim})
	got := extractClaimAtoms(op, result)
	if len(got) != 1 {
		t.Fatalf("got %d claims, want 1", len(got))
	}
}

func TestExtractClaimAtoms_pathPaymentStrictReceive_returnsClaims(t *testing.T) {
	xlm := xdr.Asset{Type: xdr.AssetTypeAssetTypeNative}
	usdc := mkAlphanum4Asset(t, "USDC", 0x10)
	claim := mkOrderBookClaim(t, 0x20, 1, xlm, usdc, 1, 1)

	op, result := mkPathPaymentStrictReceiveOp([]xdr.ClaimAtom{claim})
	got := extractClaimAtoms(op, result)
	if len(got) != 1 {
		t.Fatalf("got %d claims, want 1", len(got))
	}
}

func TestExtractClaimAtoms_pathPaymentStrictSend_returnsClaims(t *testing.T) {
	xlm := xdr.Asset{Type: xdr.AssetTypeAssetTypeNative}
	usdc := mkAlphanum4Asset(t, "USDC", 0x10)
	claim := mkOrderBookClaim(t, 0x20, 1, xlm, usdc, 1, 1)

	op, result := mkPathPaymentStrictSendOp([]xdr.ClaimAtom{claim})
	got := extractClaimAtoms(op, result)
	if len(got) != 1 {
		t.Fatalf("got %d claims, want 1", len(got))
	}
}

func TestExtractClaimAtoms_unrelatedOpType_returnsNil(t *testing.T) {
	// Payment is classic but not trade-relevant — must yield nil.
	op := xdr.Operation{
		Body: xdr.OperationBody{Type: xdr.OperationTypePayment},
	}
	result := xdr.OperationResult{
		Code: xdr.OperationResultCodeOpInner,
		Tr: &xdr.OperationResultTr{
			Type: xdr.OperationTypePayment,
			PaymentResult: &xdr.PaymentResult{
				Code: xdr.PaymentResultCodePaymentSuccess,
			},
		},
	}
	if got := extractClaimAtoms(op, result); got != nil {
		t.Errorf("got %v, want nil for non-trade op", got)
	}
}

func TestExtractClaimAtoms_resultCodeNotInner_returnsNil(t *testing.T) {
	// Op-level failure (e.g. opBadAuth) — extractClaimAtoms must
	// short-circuit before peeking into Tr.
	op := xdr.Operation{
		Body: xdr.OperationBody{Type: xdr.OperationTypeManageSellOffer},
	}
	result := xdr.OperationResult{Code: xdr.OperationResultCodeOpBadAuth}
	if got := extractClaimAtoms(op, result); got != nil {
		t.Errorf("got %v, want nil for non-OpInner result", got)
	}
}

// ─── decodeClaimAtom: LiquidityPool variant + asset variants ───

func TestDecodeClaimAtom_liquidityPool_recordsPoolHexAsMaker(t *testing.T) {
	xlm := xdr.Asset{Type: xdr.AssetTypeAssetTypeNative}
	usdc := mkAlphanum4Asset(t, "USDC", 0x10)
	atom := mkLiquidityPoolClaim(xlm, usdc, 1_000_000_000, 12_420_000)

	trade, err := decodeClaimAtom(
		atom,
		1, time.Unix(1_770_000_000, 0).UTC(),
		"abcd", 0, 0,
		"GTAKER0000000000000000000000000000000000000000000000000000",
	)
	if err != nil {
		t.Fatalf("decodeClaimAtom: %v", err)
	}
	// Maker should be the hex pool id, not a G-strkey.
	if len(trade.Maker) != 64 {
		t.Errorf("Maker = %q (len %d), want 64-char hex pool id",
			trade.Maker, len(trade.Maker))
	}
	if trade.Source != SourceName {
		t.Errorf("Source = %q, want %q", trade.Source, SourceName)
	}
}

func TestDecodeClaimAtom_unknownClaimType_returnsError(t *testing.T) {
	// A junk-typed ClaimAtom must surface ErrUnknownClaimAtomType.
	atom := xdr.ClaimAtom{Type: xdr.ClaimAtomType(99)}
	_, err := decodeClaimAtom(atom, 1, time.Now(), "abcd", 0, 0, "GT")
	if err == nil {
		t.Error("expected error on unknown claim atom type, got nil")
	}
}

// ─── xdrAssetToCanonical: native + alphanum12 paths ────────────

func TestXdrAssetToCanonical_native(t *testing.T) {
	got, err := xdrAssetToCanonical(xdr.Asset{Type: xdr.AssetTypeAssetTypeNative})
	if err != nil {
		t.Fatalf("xdrAssetToCanonical(native): %v", err)
	}
	if !got.Equal(c.NativeAsset()) {
		t.Errorf("got %+v, want native asset", got)
	}
}

func TestXdrAssetToCanonical_alphanum12(t *testing.T) {
	_, issuer := mkAccount(t, 0x10)
	var code [12]byte
	copy(code[:], "LONGTOKEN")
	a := xdr.Asset{
		Type: xdr.AssetTypeAssetTypeCreditAlphanum12,
		AlphaNum12: &xdr.AlphaNum12{
			AssetCode: code,
			Issuer:    issuer,
		},
	}
	got, err := xdrAssetToCanonical(a)
	if err != nil {
		t.Fatalf("xdrAssetToCanonical(alphanum12): %v", err)
	}
	if got.Code != "LONGTOKEN" {
		t.Errorf("Code = %q, want \"LONGTOKEN\"", got.Code)
	}
}

// ─── End-to-end through Decoder.Decode for the new op types ────

func TestDecoder_pathPaymentStrictReceive_end2end(t *testing.T) {
	d := NewDecoder()
	xlm := xdr.Asset{Type: xdr.AssetTypeAssetTypeNative}
	usdc := mkAlphanum4Asset(t, "USDC", 0x10)
	claim := mkOrderBookClaim(t, 0x20, 1, xlm, usdc, 1_000_000_000, 12_420_000)
	op, result := mkPathPaymentStrictReceiveOp([]xdr.ClaimAtom{claim})

	out, err := d.Decode(dispatcher.OpContext{
		Op: op, OpResult: result,
		Ledger: 1, ClosedAt: time.Unix(1_770_000_000, 0).UTC(),
		TxHash: "abcd", OpIndex: 0,
		TxSource: "GTAKER0000000000000000000000000000000000000000000000000000",
	})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("got %d trades, want 1", len(out))
	}
}
