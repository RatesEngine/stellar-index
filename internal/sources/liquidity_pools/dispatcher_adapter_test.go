package liquidity_pools

import (
	"errors"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/StellarAtlas/stellar-atlas/internal/dispatcher"
)

const (
	gIssuerA = "GAAQCAIBAEAQCAIBAEAQCAIBAEAQCAIBAEAQCAIBAEAQCAIBAEAQDZ7H"
	gIssuerB = "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF"
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

func makeAsset(t *testing.T, code, issuer string) xdr.Asset {
	t.Helper()
	issuerPK := mustEd(t, issuer)
	issuerAID := xdr.AccountId{Type: xdr.PublicKeyTypePublicKeyTypeEd25519, Ed25519: (*xdr.Uint256)(&issuerPK)}
	if len(code) <= 4 {
		var c [4]byte
		copy(c[:], code)
		return xdr.Asset{
			Type:      xdr.AssetTypeAssetTypeCreditAlphanum4,
			AlphaNum4: &xdr.AlphaNum4{AssetCode: c, Issuer: issuerAID},
		}
	}
	var c [12]byte
	copy(c[:], code)
	return xdr.Asset{
		Type:       xdr.AssetTypeAssetTypeCreditAlphanum12,
		AlphaNum12: &xdr.AlphaNum12{AssetCode: c, Issuer: issuerAID},
	}
}

func makeLPChange(t *testing.T, poolByte byte, assetA, assetB xdr.Asset, reserveA, reserveB int64) xdr.LedgerEntryChange {
	t.Helper()
	var pid xdr.PoolId
	pid[0] = poolByte
	return xdr.LedgerEntryChange{
		Type: xdr.LedgerEntryChangeTypeLedgerEntryUpdated,
		Updated: &xdr.LedgerEntry{
			Data: xdr.LedgerEntryData{
				Type: xdr.LedgerEntryTypeLiquidityPool,
				LiquidityPool: &xdr.LiquidityPoolEntry{
					LiquidityPoolId: pid,
					Body: xdr.LiquidityPoolEntryBody{
						Type: xdr.LiquidityPoolTypeLiquidityPoolConstantProduct,
						ConstantProduct: &xdr.LiquidityPoolEntryConstantProduct{
							Params: xdr.LiquidityPoolConstantProductParameters{
								AssetA: assetA,
								AssetB: assetB,
								Fee:    30,
							},
							ReserveA: xdr.Int64(reserveA),
							ReserveB: xdr.Int64(reserveB),
						},
					},
				},
			},
		},
	}
}

func TestNewObserver_RejectsEmpty(t *testing.T) {
	if _, err := NewObserver(nil); !errors.Is(err, ErrEmptyWatchSet) {
		t.Errorf("nil: err=%v want ErrEmptyWatchSet", err)
	}
}

// TestObserver_BothSidesWatchedEmitsTwo — when both sides of the
// pool are in the watched set, Decode emits two Observations.
func TestObserver_BothSidesWatchedEmitsTwo(t *testing.T) {
	o, _ := NewObserver([]string{
		"USDC:" + gIssuerA,
		"AQUA:" + gIssuerB,
	})
	change := makeLPChange(t, 1,
		makeAsset(t, "USDC", gIssuerA),
		makeAsset(t, "AQUA", gIssuerB),
		1_000_000, 2_000_000,
	)
	if !o.Matches(change) {
		t.Fatal("expected match (both sides watched)")
	}
	outs, err := o.Decode(dispatcher.LedgerEntryChangeContext{
		Ledger: 1, Change: change, ClosedAt: time.Unix(1, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(outs) != 2 {
		t.Fatalf("got %d outputs, want 2 (one per side)", len(outs))
	}
}

// TestObserver_OneSideWatchedEmitsOne — when only one side is
// watched, Decode emits one Observation.
func TestObserver_OneSideWatchedEmitsOne(t *testing.T) {
	o, _ := NewObserver([]string{"USDC:" + gIssuerA})
	change := makeLPChange(t, 1,
		makeAsset(t, "USDC", gIssuerA),
		makeAsset(t, "EURO", gIssuerB),
		1_000_000, 2_000_000,
	)
	if !o.Matches(change) {
		t.Fatal("expected match (one side watched)")
	}
	outs, err := o.Decode(dispatcher.LedgerEntryChangeContext{
		Ledger: 1, Change: change,
	})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(outs) != 1 {
		t.Fatalf("got %d outputs, want 1", len(outs))
	}
	obs := outs[0].(Observation)
	if obs.AssetKey != "USDC:"+gIssuerA {
		t.Errorf("AssetKey=%q, want USDC:...", obs.AssetKey)
	}
	if obs.Balance.Int64() != 1_000_000 {
		t.Errorf("Balance=%s, want 1000000 (ReserveA)", obs.Balance)
	}
}

// TestObserver_NeitherSideWatchedSkips — pools where neither
// side is watched don't match.
func TestObserver_NeitherSideWatchedSkips(t *testing.T) {
	o, _ := NewObserver([]string{"USDC:" + gIssuerA})
	change := makeLPChange(t, 1,
		makeAsset(t, "EURO", gIssuerB),
		makeAsset(t, "GBP", gIssuerB),
		1_000_000, 2_000_000,
	)
	if o.Matches(change) {
		t.Errorf("expected NO match (neither side watched)")
	}
}

// TestObserver_SkipsRemoved — Removed-variant changes don't have
// the entry body, so we can't determine watched-set membership.
// Same handling as claimable_balances.
func TestObserver_SkipsRemoved(t *testing.T) {
	o, _ := NewObserver([]string{"USDC:" + gIssuerA})
	var pid xdr.PoolId
	pid[0] = 1
	change := xdr.LedgerEntryChange{
		Type: xdr.LedgerEntryChangeTypeLedgerEntryRemoved,
		Removed: &xdr.LedgerKey{
			Type: xdr.LedgerEntryTypeLiquidityPool,
			LiquidityPool: &xdr.LedgerKeyLiquidityPool{
				LiquidityPoolId: pid,
			},
		},
	}
	if o.Matches(change) {
		t.Errorf("expected NO match on Removed LP at v1")
	}
}

func TestObserver_RoundTripThroughDispatcher(t *testing.T) {
	o, _ := NewObserver([]string{"USDC:" + gIssuerA})
	disp := dispatcher.New()
	disp.AddEntryDecoder(o)
	change := makeLPChange(t, 1,
		makeAsset(t, "USDC", gIssuerA),
		makeAsset(t, "EURO", gIssuerB),
		1_000_000, 2_000_000,
	)
	outs, err := disp.RouteEntryChange(dispatcher.LedgerEntryChangeContext{
		Ledger: 1, Change: change,
	})
	if err != nil {
		t.Fatalf("RouteEntryChange: %v", err)
	}
	if len(outs) != 1 || outs[0].EventKind() != ObservationKind {
		t.Errorf("dispatcher round-trip lost the observation")
	}
}
