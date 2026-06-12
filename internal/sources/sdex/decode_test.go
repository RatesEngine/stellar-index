package sdex

import (
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"

	c "github.com/StellarAtlas/stellar-atlas/internal/canonical"
	"github.com/StellarAtlas/stellar-atlas/internal/dispatcher"
)

// ─── fixture builders ────────────────────────────────────────────

// mkAccount returns a valid G-strkey + corresponding xdr.AccountId
// from a seed byte.
func mkAccount(t *testing.T, seed byte) (string, xdr.AccountId) {
	t.Helper()
	var pub xdr.Uint256
	pub[0] = seed
	aid := xdr.AccountId{
		Type:    xdr.PublicKeyTypePublicKeyTypeEd25519,
		Ed25519: &pub,
	}
	s, err := strkey.Encode(strkey.VersionByteAccountID, pub[:])
	if err != nil {
		t.Fatalf("strkey.Encode: %v", err)
	}
	return s, aid
}

// mkAlphanum4Asset builds a USDC-shaped classic asset from a seed
// for the issuer.
func mkAlphanum4Asset(t *testing.T, code string, issuerSeed byte) xdr.Asset {
	t.Helper()
	_, issuer := mkAccount(t, issuerSeed)
	var codeArr [4]byte
	copy(codeArr[:], code)
	return xdr.Asset{
		Type: xdr.AssetTypeAssetTypeCreditAlphanum4,
		AlphaNum4: &xdr.AlphaNum4{
			AssetCode: codeArr,
			Issuer:    issuer,
		},
	}
}

// mkOrderBookClaim builds an OrderBook ClaimAtom describing one
// filled offer.
func mkOrderBookClaim(t *testing.T,
	sellerSeed byte,
	offerID int64,
	soldAsset, boughtAsset xdr.Asset,
	soldAmount, boughtAmount int64,
) xdr.ClaimAtom {
	t.Helper()
	var pub xdr.Uint256
	pub[0] = sellerSeed
	return xdr.ClaimAtom{
		Type: xdr.ClaimAtomTypeClaimAtomTypeOrderBook,
		OrderBook: &xdr.ClaimOfferAtom{
			SellerId:     xdr.AccountId{Type: xdr.PublicKeyTypePublicKeyTypeEd25519, Ed25519: &pub},
			OfferId:      xdr.Int64(offerID),
			AssetSold:    soldAsset,
			AmountSold:   xdr.Int64(soldAmount),
			AssetBought:  boughtAsset,
			AmountBought: xdr.Int64(boughtAmount),
		},
	}
}

// mkManageSellOfferOp wraps a set of claim atoms in a
// ManageSellOffer operation + success result.
func mkManageSellOfferOp(claims []xdr.ClaimAtom) (xdr.Operation, xdr.OperationResult) {
	op := xdr.Operation{
		Body: xdr.OperationBody{
			Type: xdr.OperationTypeManageSellOffer,
		},
	}
	result := xdr.OperationResult{
		Code: xdr.OperationResultCodeOpInner,
		Tr: &xdr.OperationResultTr{
			Type: xdr.OperationTypeManageSellOffer,
			ManageSellOfferResult: &xdr.ManageSellOfferResult{
				Code: xdr.ManageSellOfferResultCodeManageSellOfferSuccess,
				Success: &xdr.ManageOfferSuccessResult{
					OffersClaimed: claims,
				},
			},
		},
	}
	return op, result
}

// ─── Decoder tests ───────────────────────────────────────────────

func TestDecoder_matchesTradeOps(t *testing.T) {
	cases := []struct {
		name string
		typ  xdr.OperationType
		want bool
	}{
		{"ManageSellOffer", xdr.OperationTypeManageSellOffer, true},
		{"ManageBuyOffer", xdr.OperationTypeManageBuyOffer, true},
		{"CreatePassiveSellOffer", xdr.OperationTypeCreatePassiveSellOffer, true},
		{"PathPaymentStrictReceive", xdr.OperationTypePathPaymentStrictReceive, true},
		{"PathPaymentStrictSend", xdr.OperationTypePathPaymentStrictSend, true},
		{"Payment (never a trade)", xdr.OperationTypePayment, false},
		{"CreateAccount (never)", xdr.OperationTypeCreateAccount, false},
	}
	d := NewDecoder()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			op := xdr.Operation{Body: xdr.OperationBody{Type: tc.typ}}
			if got := d.Matches(op); got != tc.want {
				t.Errorf("Matches(%s) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

func TestDecoder_orderBookClaim_roundTrip(t *testing.T) {
	xlm := xdr.Asset{Type: xdr.AssetTypeAssetTypeNative}
	usdc := mkAlphanum4Asset(t, "USDC", 0x10)

	// Trade shape: seller sold 100 XLM, bought 12 USDC.
	claim := mkOrderBookClaim(t, 0x20 /*offerID*/, 42,
		xlm, usdc, 1_000_000_000 /*100 XLM*/, 12_000_000 /*12 USDC*/)
	op, result := mkManageSellOfferOp([]xdr.ClaimAtom{claim})

	taker, _ := mkAccount(t, 0x01)
	d := NewDecoder()
	outs, err := d.Decode(dispatcher.OpContext{
		Ledger:   62_000_000,
		ClosedAt: time.Now().UTC().Truncate(time.Second),
		TxHash:   "a1b2c3",
		TxSource: taker,
		OpIndex:  3,
		Op:       op,
		OpResult: result,
	})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(outs) != 1 {
		t.Fatalf("got %d outputs, want 1", len(outs))
	}
	ev, ok := outs[0].(TradeEvent)
	if !ok {
		t.Fatalf("wrong output type %T", outs[0])
	}

	// Base = XLM (native), Quote = USDC classic, amounts preserved.
	if ev.Trade.Pair.Base.Type != c.AssetNative {
		t.Errorf("Base.Type = %q, want native", ev.Trade.Pair.Base.Type)
	}
	if ev.Trade.Pair.Quote.Code != "USDC" {
		t.Errorf("Quote.Code = %q, want USDC", ev.Trade.Pair.Quote.Code)
	}
	if ev.Trade.BaseAmount.BigInt().Int64() != 1_000_000_000 {
		t.Errorf("BaseAmount = %s, want 1_000_000_000", ev.Trade.BaseAmount)
	}
	if ev.Trade.QuoteAmount.BigInt().Int64() != 12_000_000 {
		t.Errorf("QuoteAmount = %s, want 12_000_000", ev.Trade.QuoteAmount)
	}
	if ev.Trade.Taker != taker {
		t.Errorf("Taker = %q, want %q", ev.Trade.Taker, taker)
	}
	seller, _ := mkAccount(t, 0x20)
	if ev.Trade.Maker != seller {
		t.Errorf("Maker = %q, want %q", ev.Trade.Maker, seller)
	}
	if ev.Trade.Source != SourceName {
		t.Errorf("Source = %q", ev.Trade.Source)
	}
	// OpIndex uses the fanout stride: 3*1024 + 0 = 3072.
	if ev.Trade.OpIndex != 3*1024 {
		t.Errorf("OpIndex = %d, want %d", ev.Trade.OpIndex, 3*1024)
	}
}

func TestDecoder_multiClaim_opIndexUnique(t *testing.T) {
	// One ManageSellOffer crosses the book through two resting
	// offers → two claims → two trades, each with a distinct
	// OpIndex so the trades hypertable's primary key doesn't
	// collide.
	xlm := xdr.Asset{Type: xdr.AssetTypeAssetTypeNative}
	usdc := mkAlphanum4Asset(t, "USDC", 0x10)

	claims := []xdr.ClaimAtom{
		mkOrderBookClaim(t, 0x21, 1, xlm, usdc, 100_000_000, 1_200_000),
		mkOrderBookClaim(t, 0x22, 2, xlm, usdc, 200_000_000, 2_500_000),
	}
	op, result := mkManageSellOfferOp(claims)

	taker, _ := mkAccount(t, 0x01)
	outs, err := NewDecoder().Decode(dispatcher.OpContext{
		Ledger: 1, TxHash: "hash", OpIndex: 5,
		ClosedAt: time.Now(),
		TxSource: taker,
		Op:       op, OpResult: result,
	})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(outs) != 2 {
		t.Fatalf("got %d outputs, want 2", len(outs))
	}
	seen := map[uint32]bool{}
	for i, o := range outs {
		ev := o.(TradeEvent)
		if seen[ev.Trade.OpIndex] {
			t.Errorf("duplicate OpIndex %d (trade %d)", ev.Trade.OpIndex, i)
		}
		seen[ev.Trade.OpIndex] = true
	}
	if len(seen) != 2 {
		t.Errorf("OpIndex uniqueness violated: %d distinct from 2 trades", len(seen))
	}
}

func TestDecoder_failedOp_emitsNothing(t *testing.T) {
	// An op whose result is anything other than Success produces
	// zero trades — not an error, just no trade signal. stellar-
	// core does let offers fail (insufficient balance, etc.).
	op := xdr.Operation{
		Body: xdr.OperationBody{Type: xdr.OperationTypeManageSellOffer},
	}
	result := xdr.OperationResult{
		Code: xdr.OperationResultCodeOpInner,
		Tr: &xdr.OperationResultTr{
			Type: xdr.OperationTypeManageSellOffer,
			ManageSellOfferResult: &xdr.ManageSellOfferResult{
				Code: xdr.ManageSellOfferResultCodeManageSellOfferLowReserve,
			},
		},
	}
	outs, err := NewDecoder().Decode(dispatcher.OpContext{Op: op, OpResult: result})
	if err != nil {
		t.Fatalf("Decode on failed op: %v", err)
	}
	if len(outs) != 0 {
		t.Errorf("got %d outputs from failed op, want 0", len(outs))
	}
}

// TestDecoder_v0ClaimAtom_decodedAsOrderBook pins F-1233 (codex
// audit-2026-05-12): V0 claim atoms (pre-CAP-27 legacy shape) are
// now decoded into a regular trade by deriving the seller G-strkey
// from the raw ed25519 bytes. Pre-fix the V0 branch returned
// ErrUnknownClaimAtomType and per-claim skip dropped the row,
// leaving since-inception SDEX history with a coverage hole on
// pre-P18 ledgers.
func TestDecoder_v0ClaimAtom_decodedAsOrderBook(t *testing.T) {
	var pub xdr.Uint256
	pub[0] = 0x30
	claim := xdr.ClaimAtom{
		Type: xdr.ClaimAtomTypeClaimAtomTypeV0,
		V0: &xdr.ClaimOfferAtomV0{
			SellerEd25519: pub,
			OfferId:       1,
			AssetSold:     xdr.Asset{Type: xdr.AssetTypeAssetTypeNative},
			AmountSold:    100,
			AssetBought:   mkAlphanum4Asset(t, "USDC", 0x10),
			AmountBought:  12,
		},
	}
	op, result := mkManageSellOfferOp([]xdr.ClaimAtom{claim})
	outs, err := NewDecoder().Decode(dispatcher.OpContext{Op: op, OpResult: result})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(outs) != 1 {
		t.Fatalf("got %d outputs, want 1 (V0 claim should decode)", len(outs))
	}
	// decodeClaimAtom should succeed too.
	tr, err := decodeClaimAtom(claim, 1, time.Now(), "tx", 0, 0, "GTAKER")
	if err != nil {
		t.Fatalf("decodeClaimAtom V0: %v", err)
	}
	if tr.Maker == "" || tr.Maker[0] != 'G' {
		t.Errorf("Maker = %q, want G-strkey", tr.Maker)
	}
}

// One-side-zero fills are captured (real trades Hubble records; pricing skips
// the zero leg separately); only both-zero no-op atoms are dropped.
func TestDecoder_zeroAmountPolicy(t *testing.T) {
	xlm := xdr.Asset{Type: xdr.AssetTypeAssetTypeNative}
	usdc := mkAlphanum4Asset(t, "USDC", 0x10)

	// one-side-zero (sold rounds to 0) → captured.
	oneSide := mkOrderBookClaim(t, 0x20, 1, xlm, usdc, 0, 12_000_000)
	op, result := mkManageSellOfferOp([]xdr.ClaimAtom{oneSide})
	outs, _ := NewDecoder().Decode(dispatcher.OpContext{Op: op, OpResult: result})
	if len(outs) != 1 {
		t.Errorf("got %d outputs, want 1 (one-side-zero captured)", len(outs))
	}

	// both-zero no-op → dropped.
	both := mkOrderBookClaim(t, 0x21, 2, xlm, usdc, 0, 0)
	op2, result2 := mkManageSellOfferOp([]xdr.ClaimAtom{both})
	outs2, _ := NewDecoder().Decode(dispatcher.OpContext{Op: op2, OpResult: result2})
	if len(outs2) != 0 {
		t.Errorf("got %d outputs, want 0 (both-zero dropped)", len(outs2))
	}
}
