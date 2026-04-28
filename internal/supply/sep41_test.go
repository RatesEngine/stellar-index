package supply_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/supply"
)

// validContractID is a real, valid C-strkey we use across tests; the
// canonical package validates strkey CRC on construction so we
// can't pass placeholder values. (Reused from key_test happy-path
// fixture — the SAC contract address for native XLM on pubnet.)
const validContractID = "CAS3J7GYLGXMF6TDJBBYYSE3HQ6BBSMLNUQ34T6TZMYMW2EVH34XOWMA"

// stubSEP41Reader is a minimal supply.SEP41SupplyReader for tests.
type stubSEP41Reader struct {
	comps supply.SEP41SupplyComponents
	err   error
	calls int
	last  struct {
		asset  canonical.Asset
		locked supply.LockedSet
		ledger uint32
	}
}

func (s *stubSEP41Reader) SEP41SupplyAt(_ context.Context, asset canonical.Asset, locked supply.LockedSet, ledger uint32) (supply.SEP41SupplyComponents, error) {
	s.calls++
	s.last.asset = asset
	s.last.locked = locked
	s.last.ledger = ledger
	if s.err != nil {
		return supply.SEP41SupplyComponents{}, s.err
	}
	return s.comps, nil
}

func mustSoroban(t *testing.T, contractID string) canonical.Asset {
	t.Helper()
	a, err := canonical.NewSorobanAsset(contractID)
	if err != nil {
		t.Fatalf("NewSorobanAsset: %v", err)
	}
	return a
}

// TestNewSEP41Computer_RejectsNilReader — same loud-misconfig stance
// as the other algorithms.
func TestNewSEP41Computer_RejectsNilReader(t *testing.T) {
	_, err := supply.NewSEP41Computer(supply.Policy{}, nil)
	if !errors.Is(err, supply.ErrNilReader) {
		t.Errorf("err = %v, want ErrNilReader", err)
	}
}

// TestSEP41_Compute_HappyPath — total = mint − burn − clawback;
// circulating = total − admin − locked sums; default basis is
// AdminExclusion when no overrides apply.
func TestSEP41_Compute_HappyPath(t *testing.T) {
	reader := &stubSEP41Reader{
		comps: supply.SEP41SupplyComponents{
			MintTotal:              bigInt(10_000_000_000), // 1000 tokens
			BurnTotal:              bigInt(500_000_000),    //   50 burned
			ClawbackTotal:          bigInt(100_000_000),    //   10 clawed back
			AdminBalance:           bigInt(40_000_000),     //    4 sitting on admin
			LockedAccountBalances:  bigInt(0),
			LockedContractBalances: bigInt(0),
		},
	}
	c, err := supply.NewSEP41Computer(supply.Policy{}, reader)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}

	asset := mustSoroban(t, validContractID)
	got, err := c.Compute(context.Background(), asset, 50_000_000, time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	wantTotal := bigInt(9_400_000_000) // 10000 − 500 − 100 (millions of stroops)
	wantCirculating := bigInt(9_360_000_000)
	if got.TotalSupply.Cmp(wantTotal) != 0 {
		t.Errorf("TotalSupply = %s, want %s", got.TotalSupply, wantTotal)
	}
	if got.CirculatingSupply.Cmp(wantCirculating) != 0 {
		t.Errorf("CirculatingSupply = %s, want %s", got.CirculatingSupply, wantCirculating)
	}
	if got.MaxSupply != nil {
		t.Errorf("MaxSupply = %s, want nil", got.MaxSupply)
	}
	if got.Basis != supply.BasisAdminExclusion {
		t.Errorf("Basis = %q, want %q", got.Basis, supply.BasisAdminExclusion)
	}
	if got.AssetKey != validContractID {
		t.Errorf("AssetKey = %q, want %q", got.AssetKey, validContractID)
	}
}

// TestSEP41_Compute_RejectsNonSoroban — feeding a classic or native
// asset is a routing bug.
func TestSEP41_Compute_RejectsNonSoroban(t *testing.T) {
	c, _ := supply.NewSEP41Computer(supply.Policy{}, &stubSEP41Reader{})
	if _, err := c.Compute(context.Background(), canonical.NativeAsset(), 1, time.Now()); !errors.Is(err, supply.ErrNotSoroban) {
		t.Errorf("err = %v, want ErrNotSoroban", err)
	}
}

// TestSEP41_Compute_NegativeTotalRejected — burn > mint can never
// be a real on-chain state for a SEP-41 token; an indexer that
// produces this is mis-summing somewhere. Refuse to publish.
func TestSEP41_Compute_NegativeTotalRejected(t *testing.T) {
	reader := &stubSEP41Reader{
		comps: supply.SEP41SupplyComponents{
			MintTotal:              bigInt(100),
			BurnTotal:              bigInt(150), // burned more than minted — impossible
			ClawbackTotal:          bigInt(0),
			AdminBalance:           bigInt(0),
			LockedAccountBalances:  bigInt(0),
			LockedContractBalances: bigInt(0),
		},
	}
	c, _ := supply.NewSEP41Computer(supply.Policy{}, reader)
	asset := mustSoroban(t, validContractID)
	_, err := c.Compute(context.Background(), asset, 1, time.Now())
	if !errors.Is(err, supply.ErrNegativeTotalSupply) {
		t.Errorf("err = %v, want ErrNegativeTotalSupply", err)
	}
}

// TestSEP41_Compute_LockedSetForwarded — operator-extended locked-set
// is passed to the reader so it can compute the LockedAccount /
// LockedContract sums in a single query. Basis upgrades to Override.
func TestSEP41_Compute_LockedSetForwarded(t *testing.T) {
	reader := &stubSEP41Reader{
		comps: supply.SEP41SupplyComponents{
			MintTotal:              bigInt(1_000),
			BurnTotal:              bigInt(0),
			ClawbackTotal:          bigInt(0),
			AdminBalance:           bigInt(0),
			LockedAccountBalances:  bigInt(100),
			LockedContractBalances: bigInt(50),
		},
	}
	policy := supply.Policy{
		PerAsset: map[string]supply.LockedSet{
			validContractID: {
				Accounts:  []string{"GTREASURY..."},
				Contracts: []string{"CVESTING..."},
			},
		},
	}
	c, _ := supply.NewSEP41Computer(policy, reader)

	asset := mustSoroban(t, validContractID)
	got, err := c.Compute(context.Background(), asset, 1, time.Now())
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if reader.last.locked.IsEmpty() {
		t.Error("reader received empty locked-set; expected forwarded operator override")
	}
	wantCirculating := bigInt(1_000 - 100 - 50)
	if got.CirculatingSupply.Cmp(wantCirculating) != 0 {
		t.Errorf("CirculatingSupply = %s, want %s", got.CirculatingSupply, wantCirculating)
	}
	if got.Basis != supply.BasisOverride {
		t.Errorf("Basis = %q, want %q", got.Basis, supply.BasisOverride)
	}
}

// TestSEP41_Compute_MaxSupplyOverride — operator-supplied max
// becomes the published value; basis is Override.
func TestSEP41_Compute_MaxSupplyOverride(t *testing.T) {
	reader := &stubSEP41Reader{
		comps: supply.SEP41SupplyComponents{
			MintTotal: bigInt(0), BurnTotal: bigInt(0), ClawbackTotal: bigInt(0),
			AdminBalance: bigInt(0), LockedAccountBalances: bigInt(0), LockedContractBalances: bigInt(0),
		},
	}
	policy := supply.Policy{
		MaxSupplyOverrides: map[string]string{validContractID: "1000000000"},
	}
	c, _ := supply.NewSEP41Computer(policy, reader)
	asset := mustSoroban(t, validContractID)
	got, err := c.Compute(context.Background(), asset, 1, time.Now())
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if got.MaxSupply == nil || got.MaxSupply.String() != "1000000000" {
		t.Errorf("MaxSupply = %v, want 1000000000", got.MaxSupply)
	}
	if got.Basis != supply.BasisOverride {
		t.Errorf("Basis = %q, want %q", got.Basis, supply.BasisOverride)
	}
}

// TestSEP41_Compute_PropagatesReaderError — reader failure must surface.
func TestSEP41_Compute_PropagatesReaderError(t *testing.T) {
	reader := &stubSEP41Reader{err: errors.New("postgres unavailable")}
	c, _ := supply.NewSEP41Computer(supply.Policy{}, reader)
	asset := mustSoroban(t, validContractID)
	if _, err := c.Compute(context.Background(), asset, 1, time.Now()); err == nil {
		t.Error("expected error from failing reader; got nil")
	}
}

// TestSEP41_Compute_RejectsNilComponents — defensive guard against a
// reader returning nil pointers.
func TestSEP41_Compute_RejectsNilComponents(t *testing.T) {
	reader := &stubSEP41Reader{
		comps: supply.SEP41SupplyComponents{
			MintTotal: nil, // sentinel
			BurnTotal: bigInt(0), ClawbackTotal: bigInt(0),
			AdminBalance: bigInt(0), LockedAccountBalances: bigInt(0), LockedContractBalances: bigInt(0),
		},
	}
	c, _ := supply.NewSEP41Computer(supply.Policy{}, reader)
	asset := mustSoroban(t, validContractID)
	if _, err := c.Compute(context.Background(), asset, 1, time.Now()); err == nil {
		t.Error("expected error for nil component; got nil")
	}
}

// TestSEP41_Compute_ZeroSupplyTokenIsValid — a fully-burned token
// (mint == burn) reports total=0 / circulating=0, NOT an error.
// Distinct from the negative-total case.
func TestSEP41_Compute_ZeroSupplyTokenIsValid(t *testing.T) {
	reader := &stubSEP41Reader{
		comps: supply.SEP41SupplyComponents{
			MintTotal:              bigInt(1_000),
			BurnTotal:              bigInt(1_000),
			ClawbackTotal:          bigInt(0),
			AdminBalance:           bigInt(0),
			LockedAccountBalances:  bigInt(0),
			LockedContractBalances: bigInt(0),
		},
	}
	c, _ := supply.NewSEP41Computer(supply.Policy{}, reader)
	asset := mustSoroban(t, validContractID)
	got, err := c.Compute(context.Background(), asset, 1, time.Now())
	if err != nil {
		t.Fatalf("fully-burned token should compute cleanly; got %v", err)
	}
	if got.TotalSupply.Sign() != 0 {
		t.Errorf("TotalSupply = %s, want 0", got.TotalSupply)
	}
	if got.CirculatingSupply.Sign() != 0 {
		t.Errorf("CirculatingSupply = %s, want 0", got.CirculatingSupply)
	}
}
