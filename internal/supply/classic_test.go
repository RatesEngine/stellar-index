package supply_test

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
	"github.com/StellarAtlas/stellar-atlas/internal/supply"
)

// stubClassicReader is a minimal supply.ClassicSupplyReader for
// tests. comps controls the success-path return; err is the error
// path. Recorded inputs let tests assert what the computer queried.
type stubClassicReader struct {
	comps supply.ClassicSupplyComponents
	err   error
	calls int
	last  struct {
		asset  canonical.Asset
		locked supply.LockedSet
		ledger uint32
	}
}

func (s *stubClassicReader) ClassicSupplyAt(_ context.Context, asset canonical.Asset, locked supply.LockedSet, ledger uint32) (supply.ClassicSupplyComponents, error) {
	s.calls++
	s.last.asset = asset
	s.last.locked = locked
	s.last.ledger = ledger
	if s.err != nil {
		return supply.ClassicSupplyComponents{}, s.err
	}
	return s.comps, nil
}

func mustClassic(t *testing.T, code, issuer string) canonical.Asset {
	t.Helper()
	a, err := canonical.NewClassicAsset(code, issuer)
	if err != nil {
		t.Fatalf("NewClassicAsset: %v", err)
	}
	return a
}

// validIssuer is a real-shaped G-strkey used across the tests; the
// canonical package validates issuer format on construction so we
// can't pass placeholder gibberish.
const validIssuer = "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"

// bigInt is a tiny helper to keep the ClassicSupplyComponents
// construction readable in tests.
func bigInt(n int64) *big.Int { return big.NewInt(n) }

// TestNewClassicComputer_RejectsNilReader — same loud-misconfig
// stance as XLMComputer; nil reader means the operator hasn't wired
// the storage layer.
func TestNewClassicComputer_RejectsNilReader(t *testing.T) {
	_, err := supply.NewClassicComputer(supply.Policy{}, nil)
	if !errors.Is(err, supply.ErrNilReader) {
		t.Errorf("err = %v, want ErrNilReader", err)
	}
}

// TestClassic_Compute_HappyPath — total = sum of four components;
// circulating = total − issuer balance (default locked-set, no
// per-asset override).
func TestClassic_Compute_HappyPath(t *testing.T) {
	reader := &stubClassicReader{
		comps: supply.ClassicSupplyComponents{
			Trustline:              bigInt(800_000_000), // 80 USDC
			Claimable:              bigInt(50_000_000),  // 5 USDC
			LPReserve:              bigInt(100_000_000), // 10 USDC in LPs
			SACWrapped:             bigInt(50_000_000),  // 5 USDC in SAC
			IssuerBalance:          bigInt(10_000_000),  // 1 USDC stuck on issuer
			LockedAccountBalances:  bigInt(0),
			LockedContractBalances: bigInt(0),
		},
	}
	c, err := supply.NewClassicComputer(supply.Policy{}, reader)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}

	asset := mustClassic(t, "USDC", validIssuer)
	got, err := c.Compute(context.Background(), asset, 50_000_000, time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	wantTotal := bigInt(1_000_000_000) // 80+5+10+5 = 100 USDC
	wantCirculating := bigInt(990_000_000)
	if got.TotalSupply.Cmp(wantTotal) != 0 {
		t.Errorf("TotalSupply = %s, want %s", got.TotalSupply, wantTotal)
	}
	if got.CirculatingSupply.Cmp(wantCirculating) != 0 {
		t.Errorf("CirculatingSupply = %s, want %s", got.CirculatingSupply, wantCirculating)
	}
	if got.MaxSupply != nil {
		t.Errorf("MaxSupply = %s, want nil (no override, no SEP-1)", got.MaxSupply)
	}
	if got.Basis != supply.BasisIssuerExclusion {
		t.Errorf("Basis = %q, want %q", got.Basis, supply.BasisIssuerExclusion)
	}
	if got.AssetKey != "USDC:"+validIssuer {
		t.Errorf("AssetKey = %q, want %q", got.AssetKey, "USDC:"+validIssuer)
	}
}

// TestClassic_Compute_RejectsNonClassic — feeding a native or
// soroban asset to the classic computer is a routing bug; surface
// a typed error so the upstream dispatcher catches it loudly.
func TestClassic_Compute_RejectsNonClassic(t *testing.T) {
	c, _ := supply.NewClassicComputer(supply.Policy{}, &stubClassicReader{})
	if _, err := c.Compute(context.Background(), canonical.NativeAsset(), 1, time.Now()); !errors.Is(err, supply.ErrNotClassic) {
		t.Errorf("err = %v, want ErrNotClassic", err)
	}
}

// TestClassic_Compute_PassesLockedSetToReader — operator extended
// locked-set must be forwarded so the reader can compute the right
// LockedAccountBalances / LockedContractBalances rather than the
// computer making per-account follow-up queries.
func TestClassic_Compute_PassesLockedSetToReader(t *testing.T) {
	reader := &stubClassicReader{
		comps: supply.ClassicSupplyComponents{
			Trustline: bigInt(1_000), Claimable: bigInt(0),
			LPReserve: bigInt(0), SACWrapped: bigInt(0),
			IssuerBalance:          bigInt(0),
			LockedAccountBalances:  bigInt(100),
			LockedContractBalances: bigInt(50),
		},
	}
	policy := supply.Policy{
		PerAsset: map[string]supply.LockedSet{
			"USDC:" + validIssuer: {
				Accounts:  []string{"GTREASURY..."},
				Contracts: []string{"CVESTING..."},
			},
		},
	}
	c, _ := supply.NewClassicComputer(policy, reader)

	asset := mustClassic(t, "USDC", validIssuer)
	got, err := c.Compute(context.Background(), asset, 1, time.Now())
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	if reader.last.locked.IsEmpty() {
		t.Error("reader received empty locked-set; expected forwarded operator override")
	}
	wantCirculating := bigInt(1_000 - 100 - 50) // 850
	if got.CirculatingSupply.Cmp(wantCirculating) != 0 {
		t.Errorf("CirculatingSupply = %s, want %s (locked deducted)", got.CirculatingSupply, wantCirculating)
	}
	// Non-empty locked-set → basis upgrades to Override.
	if got.Basis != supply.BasisOverride {
		t.Errorf("Basis = %q, want %q", got.Basis, supply.BasisOverride)
	}
}

// TestClassic_Compute_MaxSupplyOverride — operator-supplied max
// flips Basis to Override regardless of locked-set.
func TestClassic_Compute_MaxSupplyOverride(t *testing.T) {
	reader := &stubClassicReader{
		comps: supply.ClassicSupplyComponents{
			Trustline: bigInt(0), Claimable: bigInt(0),
			LPReserve: bigInt(0), SACWrapped: bigInt(0),
			IssuerBalance:          bigInt(0),
			LockedAccountBalances:  bigInt(0),
			LockedContractBalances: bigInt(0),
		},
	}
	policy := supply.Policy{
		MaxSupplyOverrides: map[string]string{
			"USDC:" + validIssuer: "21000000000000000",
		},
	}
	c, _ := supply.NewClassicComputer(policy, reader)
	asset := mustClassic(t, "USDC", validIssuer)
	got, err := c.Compute(context.Background(), asset, 1, time.Now())
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if got.MaxSupply == nil {
		t.Fatal("MaxSupply = nil, want override value")
	}
	if got.MaxSupply.String() != "21000000000000000" {
		t.Errorf("MaxSupply = %s, want 21000000000000000", got.MaxSupply)
	}
	if got.Basis != supply.BasisOverride {
		t.Errorf("Basis = %q, want %q", got.Basis, supply.BasisOverride)
	}
}

// TestClassic_Compute_PropagatesReaderError — a reader failure must
// surface (no soft fallback that hides storage outages).
func TestClassic_Compute_PropagatesReaderError(t *testing.T) {
	reader := &stubClassicReader{err: errors.New("postgres unavailable")}
	c, _ := supply.NewClassicComputer(supply.Policy{}, reader)
	asset := mustClassic(t, "USDC", validIssuer)
	if _, err := c.Compute(context.Background(), asset, 1, time.Now()); err == nil {
		t.Error("expected error from failing reader; got nil")
	}
}

// TestClassic_Compute_RejectsNilComponents — defensive guard against
// a misbehaving reader returning nil pointers in the components
// struct (would otherwise nil-pointer the Add chain).
func TestClassic_Compute_RejectsNilComponents(t *testing.T) {
	reader := &stubClassicReader{
		comps: supply.ClassicSupplyComponents{
			Trustline: nil, // sentinel — should be caught
			Claimable: bigInt(0), LPReserve: bigInt(0), SACWrapped: bigInt(0),
			IssuerBalance: bigInt(0), LockedAccountBalances: bigInt(0), LockedContractBalances: bigInt(0),
		},
	}
	c, _ := supply.NewClassicComputer(supply.Policy{}, reader)
	asset := mustClassic(t, "USDC", validIssuer)
	if _, err := c.Compute(context.Background(), asset, 1, time.Now()); err == nil {
		t.Error("expected error for nil component; got nil")
	}
}

// TestClassic_Compute_RejectsNegativeComponents — a negative
// component is a sign that the indexer's running-total math
// mis-summed; we'd rather refuse to publish than emit a
// physically-impossible reading.
func TestClassic_Compute_RejectsNegativeComponents(t *testing.T) {
	reader := &stubClassicReader{
		comps: supply.ClassicSupplyComponents{
			Trustline: bigInt(-1), // sentinel
			Claimable: bigInt(0), LPReserve: bigInt(0), SACWrapped: bigInt(0),
			IssuerBalance: bigInt(0), LockedAccountBalances: bigInt(0), LockedContractBalances: bigInt(0),
		},
	}
	c, _ := supply.NewClassicComputer(supply.Policy{}, reader)
	asset := mustClassic(t, "USDC", validIssuer)
	if _, err := c.Compute(context.Background(), asset, 1, time.Now()); err == nil {
		t.Error("expected error for negative component; got nil")
	}
}

// TestAssetKey_AllShapes — the supply key-builder covers every
// canonical asset shape per ADR-0011's storage convention.
func TestAssetKey_AllShapes(t *testing.T) {
	classic, _ := canonical.NewClassicAsset("USDC", validIssuer)
	// SAC contract id for native XLM on pubnet — a real, valid
	// C-strkey that satisfies the canonical package's CRC check.
	const sorobanID = "CAS3J7GYLGXMF6TDJBBYYSE3HQ6BBSMLNUQ34T6TZMYMW2EVH34XOWMA"
	soroban, err := canonical.NewSorobanAsset(sorobanID)
	if err != nil {
		t.Fatalf("NewSorobanAsset: %v", err)
	}

	tests := []struct {
		name    string
		asset   canonical.Asset
		want    string
		wantErr bool
	}{
		{"native", canonical.NativeAsset(), "XLM", false},
		{"classic", classic, "USDC:" + validIssuer, false},
		{"soroban", soroban, sorobanID, false},
		{"fiat is rejected", mustFiat(t, "USD"), "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := supply.AssetKey(tc.asset)
			if tc.wantErr && err == nil {
				t.Errorf("expected error; got key %q", got)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("AssetKey = %q, want %q", got, tc.want)
			}
		})
	}
}

func mustFiat(t *testing.T, code string) canonical.Asset {
	t.Helper()
	a, err := canonical.ParseAsset("fiat:" + code)
	if err != nil {
		t.Fatalf("ParseAsset(fiat:%s): %v", code, err)
	}
	return a
}
