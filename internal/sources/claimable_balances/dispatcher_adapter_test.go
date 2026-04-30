package claimable_balances

import (
	"errors"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/RatesEngine/rates-engine/internal/dispatcher"
)

const gIssuer = "GAAQCAIBAEAQCAIBAEAQCAIBAEAQCAIBAEAQCAIBAEAQCAIBAEAQDZ7H"

func mustEd(t *testing.T, gAddr string) [32]byte {
	t.Helper()
	raw, err := strkey.Decode(strkey.VersionByteAccountID, gAddr)
	if err != nil {
		t.Fatalf("strkey.Decode(%q): %v", gAddr, err)
	}
	var k [32]byte
	copy(k[:], raw)
	return k
}

func makeCBChange(t *testing.T, balanceIDByte byte, code, issuer string, amount int64) xdr.LedgerEntryChange {
	t.Helper()
	issuerPK := mustEd(t, issuer)
	issuerAID := xdr.AccountId{Type: xdr.PublicKeyTypePublicKeyTypeEd25519, Ed25519: (*xdr.Uint256)(&issuerPK)}
	var hash xdr.Hash
	hash[0] = balanceIDByte
	balanceID := xdr.ClaimableBalanceId{
		Type: xdr.ClaimableBalanceIdTypeClaimableBalanceIdTypeV0,
		V0:   &hash,
	}
	var asset xdr.Asset
	if len(code) <= 4 {
		var c [4]byte
		copy(c[:], code)
		asset = xdr.Asset{
			Type:      xdr.AssetTypeAssetTypeCreditAlphanum4,
			AlphaNum4: &xdr.AlphaNum4{AssetCode: c, Issuer: issuerAID},
		}
	} else {
		var c [12]byte
		copy(c[:], code)
		asset = xdr.Asset{
			Type:       xdr.AssetTypeAssetTypeCreditAlphanum12,
			AlphaNum12: &xdr.AlphaNum12{AssetCode: c, Issuer: issuerAID},
		}
	}
	return xdr.LedgerEntryChange{
		Type: xdr.LedgerEntryChangeTypeLedgerEntryCreated,
		Created: &xdr.LedgerEntry{
			Data: xdr.LedgerEntryData{
				Type: xdr.LedgerEntryTypeClaimableBalance,
				ClaimableBalance: &xdr.ClaimableBalanceEntry{
					BalanceId: balanceID,
					Asset:     asset,
					Amount:    xdr.Int64(amount),
				},
			},
		},
	}
}

func makeNativeCBChange(t *testing.T, balanceIDByte byte) xdr.LedgerEntryChange {
	t.Helper()
	var hash xdr.Hash
	hash[0] = balanceIDByte
	return xdr.LedgerEntryChange{
		Type: xdr.LedgerEntryChangeTypeLedgerEntryCreated,
		Created: &xdr.LedgerEntry{
			Data: xdr.LedgerEntryData{
				Type: xdr.LedgerEntryTypeClaimableBalance,
				ClaimableBalance: &xdr.ClaimableBalanceEntry{
					BalanceId: xdr.ClaimableBalanceId{
						Type: xdr.ClaimableBalanceIdTypeClaimableBalanceIdTypeV0,
						V0:   &hash,
					},
					Asset:  xdr.Asset{Type: xdr.AssetTypeAssetTypeNative},
					Amount: 1,
				},
			},
		},
	}
}

func makeRemovedCBChange() xdr.LedgerEntryChange {
	var hash xdr.Hash
	hash[0] = 1
	return xdr.LedgerEntryChange{
		Type: xdr.LedgerEntryChangeTypeLedgerEntryRemoved,
		Removed: &xdr.LedgerKey{
			Type: xdr.LedgerEntryTypeClaimableBalance,
			ClaimableBalance: &xdr.LedgerKeyClaimableBalance{
				BalanceId: xdr.ClaimableBalanceId{
					Type: xdr.ClaimableBalanceIdTypeClaimableBalanceIdTypeV0,
					V0:   &hash,
				},
			},
		},
	}
}

func TestNewObserver_RejectsEmpty(t *testing.T) {
	if _, err := NewObserver(nil); !errors.Is(err, ErrEmptyWatchSet) {
		t.Errorf("nil: err=%v want ErrEmptyWatchSet", err)
	}
	if _, err := NewObserver([]string{""}); err == nil {
		t.Errorf("empty asset_key in list should error")
	}
}

func TestObserver_MatchesWatchedAsset(t *testing.T) {
	o, _ := NewObserver([]string{"USDC:" + gIssuer})
	if !o.Matches(makeCBChange(t, 1, "USDC", gIssuer, 100)) {
		t.Errorf("expected match on watched claimable")
	}
}

func TestObserver_SkipsUnwatchedAsset(t *testing.T) {
	o, _ := NewObserver([]string{"USDC:" + gIssuer})
	if o.Matches(makeCBChange(t, 1, "EURO", gIssuer, 100)) {
		t.Errorf("expected NO match on unwatched asset code")
	}
}

func TestObserver_SkipsNativeClaimable(t *testing.T) {
	o, _ := NewObserver([]string{"USDC:" + gIssuer})
	if o.Matches(makeNativeCBChange(t, 1)) {
		t.Errorf("expected NO match on native claimable — Algorithm 1 path")
	}
}

// TestObserver_SkipsRemovedAtV1 — Removed claimable changes are
// filtered out at Match (see package doc). Asset-key-not-on-
// LedgerKey makes watched-set membership undetermined for these.
func TestObserver_SkipsRemovedAtV1(t *testing.T) {
	o, _ := NewObserver([]string{"USDC:" + gIssuer})
	if o.Matches(makeRemovedCBChange()) {
		t.Errorf("expected NO match on Removed claimable at v1")
	}
}

func TestObserver_DecodeBuildsObservation(t *testing.T) {
	o, _ := NewObserver([]string{"USDC:" + gIssuer})
	now := time.Unix(1_770_000_000, 0).UTC()
	change := makeCBChange(t, 0xab, "USDC", gIssuer, 999_888_777)
	outs, err := o.Decode(dispatcher.LedgerEntryChangeContext{
		Ledger:   123_456,
		ClosedAt: now,
		Change:   change,
	})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	obs := outs[0].(Observation)
	if obs.AssetKey != "USDC:"+gIssuer {
		t.Errorf("AssetKey=%q want USDC:...", obs.AssetKey)
	}
	if obs.Balance.Int64() != 999_888_777 {
		t.Errorf("Balance=%s want 999888777", obs.Balance)
	}
	if obs.IsRemoval {
		t.Errorf("IsRemoval=true on Created change, want false")
	}
	// Hash with first byte 0xab → hex starts "ab" then 62 zeros.
	if len(obs.ClaimableID) != 64 {
		t.Errorf("ClaimableID hex length=%d want 64", len(obs.ClaimableID))
	}
	if obs.ClaimableID[:2] != "ab" {
		t.Errorf("ClaimableID prefix=%q want ab", obs.ClaimableID[:2])
	}
}

func TestObserver_RoundTripThroughDispatcher(t *testing.T) {
	o, _ := NewObserver([]string{"USDC:" + gIssuer})
	disp := dispatcher.New()
	disp.AddEntryDecoder(o)

	outs, err := disp.RouteEntryChange(dispatcher.LedgerEntryChangeContext{
		Ledger: 1,
		Change: makeCBChange(t, 1, "USDC", gIssuer, 100),
	})
	if err != nil {
		t.Fatalf("RouteEntryChange: %v", err)
	}
	if len(outs) != 1 {
		t.Fatalf("got %d outputs, want 1", len(outs))
	}
	if outs[0].EventKind() != ObservationKind {
		t.Errorf("EventKind=%q want %q", outs[0].EventKind(), ObservationKind)
	}
}
