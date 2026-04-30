package trustlines

import (
	"errors"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/RatesEngine/rates-engine/internal/dispatcher"
)

// Synthetic G-strkeys derived at test time — same approach as
// internal/sources/accounts/dispatcher_adapter_test.go.
const (
	gHolder = "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF"
	gIssuer = "GAAQCAIBAEAQCAIBAEAQCAIBAEAQCAIBAEAQCAIBAEAQCAIBAEAQDZ7H"
)

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

func makeTrustlineChange(t *testing.T, holder, issuer, code string, balance int64) xdr.LedgerEntryChange {
	t.Helper()
	holderPK := mustEd(t, holder)
	issuerPK := mustEd(t, issuer)
	holderAID := xdr.AccountId{Type: xdr.PublicKeyTypePublicKeyTypeEd25519, Ed25519: (*xdr.Uint256)(&holderPK)}
	issuerAID := xdr.AccountId{Type: xdr.PublicKeyTypePublicKeyTypeEd25519, Ed25519: (*xdr.Uint256)(&issuerPK)}

	var asset xdr.TrustLineAsset
	if len(code) <= 4 {
		var codeArr [4]byte
		copy(codeArr[:], code)
		asset = xdr.TrustLineAsset{
			Type:      xdr.AssetTypeAssetTypeCreditAlphanum4,
			AlphaNum4: &xdr.AlphaNum4{AssetCode: codeArr, Issuer: issuerAID},
		}
	} else {
		var codeArr [12]byte
		copy(codeArr[:], code)
		asset = xdr.TrustLineAsset{
			Type:       xdr.AssetTypeAssetTypeCreditAlphanum12,
			AlphaNum12: &xdr.AlphaNum12{AssetCode: codeArr, Issuer: issuerAID},
		}
	}

	return xdr.LedgerEntryChange{
		Type: xdr.LedgerEntryChangeTypeLedgerEntryUpdated,
		Updated: &xdr.LedgerEntry{
			Data: xdr.LedgerEntryData{
				Type: xdr.LedgerEntryTypeTrustline,
				TrustLine: &xdr.TrustLineEntry{
					AccountId: holderAID,
					Asset:     asset,
					Balance:   xdr.Int64(balance),
				},
			},
		},
	}
}

func makeNativeTrustlineChange(t *testing.T, holder string) xdr.LedgerEntryChange {
	t.Helper()
	holderPK := mustEd(t, holder)
	holderAID := xdr.AccountId{Type: xdr.PublicKeyTypePublicKeyTypeEd25519, Ed25519: (*xdr.Uint256)(&holderPK)}
	return xdr.LedgerEntryChange{
		Type: xdr.LedgerEntryChangeTypeLedgerEntryUpdated,
		Updated: &xdr.LedgerEntry{
			Data: xdr.LedgerEntryData{
				Type: xdr.LedgerEntryTypeTrustline,
				TrustLine: &xdr.TrustLineEntry{
					AccountId: holderAID,
					Asset:     xdr.TrustLineAsset{Type: xdr.AssetTypeAssetTypeNative},
					Balance:   1,
				},
			},
		},
	}
}

func makeAccountChange() xdr.LedgerEntryChange {
	return xdr.LedgerEntryChange{
		Type: xdr.LedgerEntryChangeTypeLedgerEntryCreated,
		Created: &xdr.LedgerEntry{
			Data: xdr.LedgerEntryData{
				Type:    xdr.LedgerEntryTypeAccount,
				Account: &xdr.AccountEntry{},
			},
		},
	}
}

func TestNewObserver_RejectsEmpty(t *testing.T) {
	if _, err := NewObserver(nil); !errors.Is(err, ErrEmptyWatchSet) {
		t.Errorf("nil: err=%v want ErrEmptyWatchSet", err)
	}
	if _, err := NewObserver([]string{}); !errors.Is(err, ErrEmptyWatchSet) {
		t.Errorf("empty: err=%v want ErrEmptyWatchSet", err)
	}
	if _, err := NewObserver([]string{""}); err == nil {
		t.Errorf("empty asset_key in list should error")
	}
}

func TestObserver_MatchesWatchedAsset(t *testing.T) {
	o, err := NewObserver([]string{"USDC:" + gIssuer})
	if err != nil {
		t.Fatalf("NewObserver: %v", err)
	}
	if !o.Matches(makeTrustlineChange(t, gHolder, gIssuer, "USDC", 100)) {
		t.Errorf("expected match on watched asset")
	}
}

func TestObserver_SkipsUnwatchedAsset(t *testing.T) {
	o, _ := NewObserver([]string{"USDC:" + gIssuer})
	if o.Matches(makeTrustlineChange(t, gHolder, gIssuer, "EURO", 100)) {
		t.Errorf("expected NO match on unwatched asset code")
	}
}

func TestObserver_SkipsNativeTrustline(t *testing.T) {
	o, _ := NewObserver([]string{"USDC:" + gIssuer})
	if o.Matches(makeNativeTrustlineChange(t, gHolder)) {
		t.Errorf("expected NO match on native (XLM) trustline — Algorithm 1 covers XLM")
	}
}

func TestObserver_SkipsNonTrustlineEntry(t *testing.T) {
	o, _ := NewObserver([]string{"USDC:" + gIssuer})
	if o.Matches(makeAccountChange()) {
		t.Errorf("expected NO match on AccountEntry change")
	}
}

func TestObserver_DecodeBuildsObservation(t *testing.T) {
	o, _ := NewObserver([]string{"USDC:" + gIssuer})
	now := time.Unix(1_770_000_000, 0).UTC()
	change := makeTrustlineChange(t, gHolder, gIssuer, "USDC", 999_888_777)
	outs, err := o.Decode(dispatcher.LedgerEntryChangeContext{
		Ledger:   123_456,
		ClosedAt: now,
		Change:   change,
	})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(outs) != 1 {
		t.Fatalf("got %d outputs, want 1", len(outs))
	}
	obs := outs[0].(Observation)
	if obs.AccountID != gHolder {
		t.Errorf("AccountID=%q want %q", obs.AccountID, gHolder)
	}
	if obs.AssetKey != "USDC:"+gIssuer {
		t.Errorf("AssetKey=%q want USDC:...", obs.AssetKey)
	}
	if obs.Balance.Int64() != 999_888_777 {
		t.Errorf("Balance=%s want 999888777", obs.Balance)
	}
	if obs.Ledger != 123_456 {
		t.Errorf("Ledger=%d want 123456", obs.Ledger)
	}
	if !obs.ObservedAt.Equal(now) {
		t.Errorf("ObservedAt=%v want %v", obs.ObservedAt, now)
	}
	if obs.IsRemoval {
		t.Errorf("IsRemoval=true on Updated change, want false")
	}
}

// TestObserver_DecodeRemoval — Removed-variant changes set
// IsRemoval=true with zeroed Balance.
func TestObserver_DecodeRemoval(t *testing.T) {
	o, _ := NewObserver([]string{"USDC:" + gIssuer})
	holderPK := mustEd(t, gHolder)
	issuerPK := mustEd(t, gIssuer)
	holderAID := xdr.AccountId{Type: xdr.PublicKeyTypePublicKeyTypeEd25519, Ed25519: (*xdr.Uint256)(&holderPK)}
	issuerAID := xdr.AccountId{Type: xdr.PublicKeyTypePublicKeyTypeEd25519, Ed25519: (*xdr.Uint256)(&issuerPK)}
	var codeArr [4]byte
	copy(codeArr[:], "USDC")
	change := xdr.LedgerEntryChange{
		Type: xdr.LedgerEntryChangeTypeLedgerEntryRemoved,
		Removed: &xdr.LedgerKey{
			Type: xdr.LedgerEntryTypeTrustline,
			TrustLine: &xdr.LedgerKeyTrustLine{
				AccountId: holderAID,
				Asset: xdr.TrustLineAsset{
					Type:      xdr.AssetTypeAssetTypeCreditAlphanum4,
					AlphaNum4: &xdr.AlphaNum4{AssetCode: codeArr, Issuer: issuerAID},
				},
			},
		},
	}
	outs, err := o.Decode(dispatcher.LedgerEntryChangeContext{
		Ledger: 1,
		Change: change,
	})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	obs := outs[0].(Observation)
	if !obs.IsRemoval {
		t.Errorf("IsRemoval=false on Removed change, want true")
	}
	if obs.Balance.Sign() != 0 {
		t.Errorf("removal Balance=%s, want 0", obs.Balance)
	}
}

// TestObserver_RoundTripThroughDispatcher — verifies routing
// end-to-end via RouteEntryChange.
func TestObserver_RoundTripThroughDispatcher(t *testing.T) {
	o, _ := NewObserver([]string{"USDC:" + gIssuer})
	disp := dispatcher.New()
	disp.AddEntryDecoder(o)

	outs, err := disp.RouteEntryChange(dispatcher.LedgerEntryChangeContext{
		Ledger: 1,
		Change: makeTrustlineChange(t, gHolder, gIssuer, "USDC", 100),
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

func TestTrimTrailingNulls(t *testing.T) {
	cases := []struct {
		in   []byte
		want string
	}{
		{[]byte{'U', 'S', 'D', 'C'}, "USDC"},
		{[]byte{'U', 'S', 'D', 0}, "USD"},
		{[]byte{0, 0, 0, 0}, ""},
		{[]byte{'A', 0, 'B', 0}, "A\x00B"}, // null between non-nulls preserved
	}
	for _, tc := range cases {
		if got := trimTrailingNulls(tc.in); got != tc.want {
			t.Errorf("trimTrailingNulls(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
