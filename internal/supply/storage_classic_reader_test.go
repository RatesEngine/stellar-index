package supply

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"testing"

	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// fakeClassicStore satisfies ClassicSupplyStore with in-memory
// values so the reader logic can be unit-tested without a real
// DB.
type fakeClassicStore struct {
	trustlineSum *big.Int
	claimableSum *big.Int
	lpSum        *big.Int
	sacSum       *big.Int
	// per-(account, asset) trustline lookup; key is "account:assetKey".
	// Used for issuer balance + LockedSet.Accounts.
	trustlinePerAccount map[string]*big.Int
	// per-(contract, asset) SAC lookup; key is "contract:assetKey".
	// Used for LockedSet.Contracts.
	sacPerContract map[string]*big.Int

	wantErrSum bool
}

func (f *fakeClassicStore) SumTrustlineBalancesAtOrBefore(_ context.Context, _ string, _ uint32) (*big.Int, error) {
	if f.wantErrSum {
		return nil, errors.New("trustline sum boom")
	}
	return f.trustlineSum, nil
}

func (f *fakeClassicStore) SumClaimableBalancesAtOrBefore(_ context.Context, _ string, _ uint32) (*big.Int, error) {
	return f.claimableSum, nil
}

func (f *fakeClassicStore) SumLPReservesAtOrBefore(_ context.Context, _ string, _ uint32) (*big.Int, error) {
	return f.lpSum, nil
}

func (f *fakeClassicStore) SumSACBalancesAtOrBefore(_ context.Context, _ string, _ uint32) (*big.Int, error) {
	return f.sacSum, nil
}

func (f *fakeClassicStore) TrustlineBalanceForAccountAtOrBefore(_ context.Context, accountID, assetKey string, _ uint32) (*big.Int, error) {
	key := accountID + ":" + assetKey
	if v, ok := f.trustlinePerAccount[key]; ok {
		return v, nil
	}
	return big.NewInt(0), nil
}

func (f *fakeClassicStore) SACBalanceForContractAtOrBefore(_ context.Context, contractID, assetKey string, _ uint32) (*big.Int, error) {
	key := contractID + ":" + assetKey
	if v, ok := f.sacPerContract[key]; ok {
		return v, nil
	}
	return big.NewInt(0), nil
}

// MinClassicComponentLedger — fakes a single per-asset value;
// tests that don't care leave it at 0 (gate-skip).
func (f *fakeClassicStore) MinClassicComponentLedger(_ context.Context, _ string, _ uint32) (uint32, error) {
	return 0, nil
}

func mustClassic(t *testing.T, code, issuer string) canonical.Asset {
	t.Helper()
	a, err := canonical.NewClassicAsset(code, issuer)
	if err != nil {
		t.Fatalf("NewClassicAsset: %v", err)
	}
	return a
}

const tIssuer = "GAAQCAIBAEAQCAIBAEAQCAIBAEAQCAIBAEAQCAIBAEAQCAIBAEAQDZ7H"

func TestStorageClassicSupplyReader_HappyPath(t *testing.T) {
	store := &fakeClassicStore{
		trustlineSum: big.NewInt(1000),
		claimableSum: big.NewInt(50),
		lpSum:        big.NewInt(20),
		sacSum:       big.NewInt(30),
	}
	r := NewStorageClassicSupplyReader(store)

	asset := mustClassic(t, "USDC", tIssuer)
	got, err := r.ClassicSupplyAt(context.Background(), asset, LockedSet{}, 100)
	if err != nil {
		t.Fatalf("ClassicSupplyAt: %v", err)
	}
	if got.Trustline.Int64() != 1000 {
		t.Errorf("Trustline=%s want 1000", got.Trustline)
	}
	if got.Claimable.Int64() != 50 {
		t.Errorf("Claimable=%s want 50", got.Claimable)
	}
	if got.LPReserve.Int64() != 20 {
		t.Errorf("LPReserve=%s want 20", got.LPReserve)
	}
	if got.SACWrapped.Int64() != 30 {
		t.Errorf("SACWrapped=%s want 30", got.SACWrapped)
	}
	if got.IssuerBalance.Sign() != 0 {
		t.Errorf("IssuerBalance=%s want 0 (no issuer trustline configured)", got.IssuerBalance)
	}
	if got.LockedAccountBalances.Sign() != 0 {
		t.Errorf("LockedAccountBalances=%s want 0 (empty LockedSet)", got.LockedAccountBalances)
	}
	if got.LockedContractBalances.Sign() != 0 {
		t.Errorf("LockedContractBalances=%s want 0 (empty LockedSet)", got.LockedContractBalances)
	}
}

func TestStorageClassicSupplyReader_RejectsNonClassic(t *testing.T) {
	r := NewStorageClassicSupplyReader(&fakeClassicStore{})
	_, err := r.ClassicSupplyAt(context.Background(), canonical.NativeAsset(), LockedSet{}, 1)
	if !errors.Is(err, ErrNotClassic) {
		t.Errorf("err=%v want wrapping ErrNotClassic", err)
	}
}

func TestStorageClassicSupplyReader_PropagatesSumError(t *testing.T) {
	store := &fakeClassicStore{wantErrSum: true}
	r := NewStorageClassicSupplyReader(store)
	asset := mustClassic(t, "USDC", tIssuer)
	_, err := r.ClassicSupplyAt(context.Background(), asset, LockedSet{}, 1)
	if err == nil || !strings.Contains(err.Error(), "trustline") {
		t.Errorf("err=%v should mention trustline failure", err)
	}
}

// TestStorageClassicSupplyReader_IssuerBalanceLookedUp — when the
// issuer is on their own trustline (rare but legal), the reader
// surfaces the balance for the algorithm to subtract.
func TestStorageClassicSupplyReader_IssuerBalanceLookedUp(t *testing.T) {
	assetKey := "USDC:" + tIssuer
	store := &fakeClassicStore{
		trustlineSum: big.NewInt(1000),
		claimableSum: big.NewInt(0),
		lpSum:        big.NewInt(0),
		sacSum:       big.NewInt(0),
		trustlinePerAccount: map[string]*big.Int{
			tIssuer + ":" + assetKey: big.NewInt(123),
		},
	}
	r := NewStorageClassicSupplyReader(store)
	asset := mustClassic(t, "USDC", tIssuer)
	got, err := r.ClassicSupplyAt(context.Background(), asset, LockedSet{}, 1)
	if err != nil {
		t.Fatalf("ClassicSupplyAt: %v", err)
	}
	if got.IssuerBalance.Int64() != 123 {
		t.Errorf("IssuerBalance=%s want 123", got.IssuerBalance)
	}
}

// TestStorageClassicSupplyReader_LockedSetSummed — operator-
// configured locked accounts and contracts contribute to the
// per-component sums.
func TestStorageClassicSupplyReader_LockedSetSummed(t *testing.T) {
	assetKey := "USDC:" + tIssuer
	store := &fakeClassicStore{
		trustlineSum: big.NewInt(0),
		claimableSum: big.NewInt(0),
		lpSum:        big.NewInt(0),
		sacSum:       big.NewInt(0),
		trustlinePerAccount: map[string]*big.Int{
			"G_LOCKED_1:" + assetKey: big.NewInt(100),
			"G_LOCKED_2:" + assetKey: big.NewInt(200),
		},
		sacPerContract: map[string]*big.Int{
			"C_LOCKED_1:" + assetKey: big.NewInt(50),
		},
	}
	r := NewStorageClassicSupplyReader(store)
	asset := mustClassic(t, "USDC", tIssuer)
	locked := LockedSet{
		Accounts:  []string{"G_LOCKED_1", "G_LOCKED_2"},
		Contracts: []string{"C_LOCKED_1"},
	}
	got, err := r.ClassicSupplyAt(context.Background(), asset, locked, 1)
	if err != nil {
		t.Fatalf("ClassicSupplyAt: %v", err)
	}
	if got.LockedAccountBalances.Int64() != 300 {
		t.Errorf("LockedAccountBalances=%s want 300", got.LockedAccountBalances)
	}
	if got.LockedContractBalances.Int64() != 50 {
		t.Errorf("LockedContractBalances=%s want 50", got.LockedContractBalances)
	}
}
