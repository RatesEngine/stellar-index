package supply

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
)

type fakeSEP41Store struct {
	totals          SEP41KindTotals
	totalsErr       error
	holderBalances  map[string]*big.Int // key: "holder:assetKey"
	holderLookupErr error
}

func (f *fakeSEP41Store) SEP41KindTotalsAtOrBefore(_ context.Context, _ string, _ uint32) (SEP41KindTotals, error) {
	if f.totalsErr != nil {
		return SEP41KindTotals{}, f.totalsErr
	}
	return f.totals, nil
}

func (f *fakeSEP41Store) SACBalanceForContractAtOrBefore(_ context.Context, holder, assetKey string, _ uint32) (*big.Int, error) {
	if f.holderLookupErr != nil {
		return nil, f.holderLookupErr
	}
	key := holder + ":" + assetKey
	if v, ok := f.holderBalances[key]; ok {
		return v, nil
	}
	return big.NewInt(0), nil
}

const tContract = "CAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABSC4"

func mustSorobanAsset(t *testing.T, contractID string) canonical.Asset {
	t.Helper()
	a, err := canonical.NewSorobanAsset(contractID)
	if err != nil {
		t.Fatalf("NewSorobanAsset: %v", err)
	}
	return a
}

func TestStorageSEP41SupplyReader_HappyPath(t *testing.T) {
	store := &fakeSEP41Store{
		totals: SEP41KindTotals{
			Mint:     big.NewInt(10_000),
			Burn:     big.NewInt(2_000),
			Clawback: big.NewInt(500),
		},
	}
	r := NewStorageSEP41SupplyReader(store)
	asset := mustSorobanAsset(t, tContract)
	got, err := r.SEP41SupplyAt(context.Background(), asset, LockedSet{}, 100)
	if err != nil {
		t.Fatalf("SEP41SupplyAt: %v", err)
	}
	if got.MintTotal.Int64() != 10_000 {
		t.Errorf("MintTotal=%s want 10000", got.MintTotal)
	}
	if got.BurnTotal.Int64() != 2_000 {
		t.Errorf("BurnTotal=%s want 2000", got.BurnTotal)
	}
	if got.ClawbackTotal.Int64() != 500 {
		t.Errorf("ClawbackTotal=%s want 500", got.ClawbackTotal)
	}
	if got.AdminBalance.Sign() != 0 {
		t.Errorf("AdminBalance=%s want 0 (operator-policy via locked-set)", got.AdminBalance)
	}
	if got.LockedAccountBalances.Sign() != 0 {
		t.Errorf("LockedAccountBalances=%s want 0 (empty LockedSet)", got.LockedAccountBalances)
	}
	if got.LockedContractBalances.Sign() != 0 {
		t.Errorf("LockedContractBalances=%s want 0 (empty LockedSet)", got.LockedContractBalances)
	}
}

func TestStorageSEP41SupplyReader_RejectsNonSoroban(t *testing.T) {
	r := NewStorageSEP41SupplyReader(&fakeSEP41Store{})
	_, err := r.SEP41SupplyAt(context.Background(), canonical.NativeAsset(), LockedSet{}, 1)
	if !errors.Is(err, ErrNotSoroban) {
		t.Errorf("err=%v want wrapping ErrNotSoroban", err)
	}
}

func TestStorageSEP41SupplyReader_PropagatesTotalsError(t *testing.T) {
	store := &fakeSEP41Store{totalsErr: errors.New("DB unreachable")}
	r := NewStorageSEP41SupplyReader(store)
	asset := mustSorobanAsset(t, tContract)
	_, err := r.SEP41SupplyAt(context.Background(), asset, LockedSet{}, 1)
	if err == nil || !strings.Contains(err.Error(), "kind totals") {
		t.Errorf("err=%v should mention kind totals failure", err)
	}
}

// TestStorageSEP41SupplyReader_LockedSetSummed — operator-
// configured locked accounts and contracts contribute to the
// per-component sums. Each holder's balance is looked up by
// (holder, contract_id) — because for SEP-41 contracts the
// asset_key IS the contract_id per supply.AssetKey.
func TestStorageSEP41SupplyReader_LockedSetSummed(t *testing.T) {
	store := &fakeSEP41Store{
		totals: SEP41KindTotals{
			Mint:     big.NewInt(0),
			Burn:     big.NewInt(0),
			Clawback: big.NewInt(0),
		},
		holderBalances: map[string]*big.Int{
			"G_LOCKED_1:" + tContract: big.NewInt(100),
			"G_LOCKED_2:" + tContract: big.NewInt(200),
			"C_LOCKED_1:" + tContract: big.NewInt(50),
		},
	}
	r := NewStorageSEP41SupplyReader(store)
	asset := mustSorobanAsset(t, tContract)
	locked := LockedSet{
		Accounts:  []string{"G_LOCKED_1", "G_LOCKED_2"},
		Contracts: []string{"C_LOCKED_1"},
	}
	got, err := r.SEP41SupplyAt(context.Background(), asset, locked, 1)
	if err != nil {
		t.Fatalf("SEP41SupplyAt: %v", err)
	}
	if got.LockedAccountBalances.Int64() != 300 {
		t.Errorf("LockedAccountBalances=%s want 300", got.LockedAccountBalances)
	}
	if got.LockedContractBalances.Int64() != 50 {
		t.Errorf("LockedContractBalances=%s want 50", got.LockedContractBalances)
	}
}

// TestAssetBoundSEP41Computer_HappyPath — the wrapper delegates
// Compute with the baked-in asset, returning a Supply for the
// per-asset Refresher.Tick path.
func TestAssetBoundSEP41Computer_HappyPath(t *testing.T) {
	store := &fakeSEP41Store{
		totals: SEP41KindTotals{
			Mint:     big.NewInt(1_000),
			Burn:     big.NewInt(0),
			Clawback: big.NewInt(0),
		},
	}
	reader := NewStorageSEP41SupplyReader(store)
	computer, err := NewSEP41Computer(Policy{}, reader)
	if err != nil {
		t.Fatalf("NewSEP41Computer: %v", err)
	}
	asset := mustSorobanAsset(t, tContract)
	bound, err := NewAssetBoundSEP41Computer(computer, asset)
	if err != nil {
		t.Fatalf("NewAssetBoundSEP41Computer: %v", err)
	}
	snap, err := bound.Compute(context.Background(), 1, time.Time{})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if snap.AssetKey != tContract {
		t.Errorf("AssetKey=%q want %q", snap.AssetKey, tContract)
	}
	if snap.TotalSupply.Int64() != 1_000 {
		t.Errorf("TotalSupply=%s want 1000", snap.TotalSupply)
	}
	if snap.Basis != BasisAdminExclusion {
		t.Errorf("Basis=%s want %s", snap.Basis, BasisAdminExclusion)
	}
}

func TestAssetBoundSEP41Computer_RejectsNonSoroban(t *testing.T) {
	reader := NewStorageSEP41SupplyReader(&fakeSEP41Store{})
	computer, _ := NewSEP41Computer(Policy{}, reader)
	_, err := NewAssetBoundSEP41Computer(computer, canonical.NativeAsset())
	if !errors.Is(err, ErrNotSoroban) {
		t.Errorf("err=%v want wrapping ErrNotSoroban", err)
	}
}
