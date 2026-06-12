package supply_test

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
	"github.com/StellarAtlas/stellar-atlas/internal/supply"
)

// stubMetadataResolver is a minimal supply.MetadataResolver for
// tests. raw + ok controls success; err controls error-path behaviour.
type stubMetadataResolver struct {
	raw   string
	ok    bool
	err   error
	calls int
}

func (s *stubMetadataResolver) SEP1MaxSupply(_ context.Context, _ canonical.Asset) (string, bool, error) {
	s.calls++
	return s.raw, s.ok, s.err
}

// TestOverlay_XLMNeverModified — Algorithm 1 sets MaxSupply on every
// snapshot; Overlay must not touch it (and must not call the
// resolver at all — XLM has no stellar.toml).
func TestOverlay_XLMNeverModified(t *testing.T) {
	resolver := &stubMetadataResolver{raw: "12345", ok: true}
	snap := supply.Supply{
		AssetKey:    "XLM",
		TotalSupply: supply.XLMTotalSupplyStroops(),
		MaxSupply:   supply.XLMTotalSupplyStroops(),
	}
	got, applied, err := supply.Overlay(context.Background(), snap, canonical.NativeAsset(), resolver)
	if err != nil {
		t.Fatalf("Overlay: %v", err)
	}
	if applied {
		t.Error("applied=true on XLM; expected false")
	}
	if resolver.calls != 0 {
		t.Errorf("resolver.calls = %d, want 0 — XLM should never query SEP-1", resolver.calls)
	}
	if got.MaxSupply.Cmp(supply.XLMTotalSupplyStroops()) != 0 {
		t.Errorf("XLM MaxSupply was modified: %s", got.MaxSupply)
	}
}

// TestOverlay_OperatorOverridePreserved — when Compute already
// fired an operator override (MaxSupply non-nil), Overlay must NOT
// consult SEP-1 (the operator's word beats the declaration).
func TestOverlay_OperatorOverridePreserved(t *testing.T) {
	resolver := &stubMetadataResolver{raw: "999999", ok: true}
	snap := supply.Supply{
		AssetKey:    "USDC:GA1",
		TotalSupply: big.NewInt(1000),
		MaxSupply:   big.NewInt(5000), // operator override
		Basis:       supply.BasisOverride,
	}
	usdc, _ := canonical.NewClassicAsset("USDC", validIssuer)
	got, applied, err := supply.Overlay(context.Background(), snap, usdc, resolver)
	if err != nil {
		t.Fatalf("Overlay: %v", err)
	}
	if applied {
		t.Error("applied=true with operator override present; expected false")
	}
	if resolver.calls != 0 {
		t.Errorf("resolver.calls = %d, want 0 — operator override must short-circuit", resolver.calls)
	}
	if got.MaxSupply.Cmp(big.NewInt(5000)) != 0 {
		t.Errorf("operator override clobbered: MaxSupply = %s", got.MaxSupply)
	}
}

// TestOverlay_AppliesSEP1WhenAvailable — happy path: no operator
// override, SEP-1 declaration present and parseable; MaxSupply gets
// populated and Basis upgrades from algorithm-default to Override.
func TestOverlay_AppliesSEP1WhenAvailable(t *testing.T) {
	resolver := &stubMetadataResolver{raw: "21000000000000000", ok: true}
	snap := supply.Supply{
		AssetKey:    "USDC:GA1",
		TotalSupply: big.NewInt(1000),
		MaxSupply:   nil, // no operator override
		Basis:       supply.BasisIssuerExclusion,
	}
	usdc, _ := canonical.NewClassicAsset("USDC", validIssuer)
	got, applied, err := supply.Overlay(context.Background(), snap, usdc, resolver)
	if err != nil {
		t.Fatalf("Overlay: %v", err)
	}
	if !applied {
		t.Fatal("applied=false; expected SEP-1 to be applied")
	}
	if got.MaxSupply == nil || got.MaxSupply.String() != "21000000000000000" {
		t.Errorf("MaxSupply = %v, want 21000000000000000", got.MaxSupply)
	}
	if got.Basis != supply.BasisOverride {
		t.Errorf("Basis = %q, want %q", got.Basis, supply.BasisOverride)
	}
}

// TestOverlay_AbsentDeclarationLeavesNil — no stellar.toml or no
// max_supply declaration: snap unchanged, applied=false, no error.
func TestOverlay_AbsentDeclarationLeavesNil(t *testing.T) {
	resolver := &stubMetadataResolver{ok: false}
	snap := supply.Supply{
		AssetKey:    "USDC:GA1",
		TotalSupply: big.NewInt(1000),
		MaxSupply:   nil,
		Basis:       supply.BasisIssuerExclusion,
	}
	usdc, _ := canonical.NewClassicAsset("USDC", validIssuer)
	got, applied, err := supply.Overlay(context.Background(), snap, usdc, resolver)
	if err != nil {
		t.Fatalf("Overlay: %v", err)
	}
	if applied {
		t.Error("applied=true with no declaration; expected false")
	}
	if got.MaxSupply != nil {
		t.Errorf("MaxSupply = %s, want nil", got.MaxSupply)
	}
	if got.Basis != supply.BasisIssuerExclusion {
		t.Errorf("Basis = %q, want unchanged %q", got.Basis, supply.BasisIssuerExclusion)
	}
}

// TestOverlay_PropagatesResolverError — a TOML resolver error
// surfaces as (snap-unchanged, applied=false, err). Caller decides
// whether to log + continue or 5xx — but the snap itself stays
// usable.
func TestOverlay_PropagatesResolverError(t *testing.T) {
	resolver := &stubMetadataResolver{err: errors.New("redis blip")}
	snap := supply.Supply{
		AssetKey:    "USDC:GA1",
		TotalSupply: big.NewInt(1000),
		MaxSupply:   nil,
		Basis:       supply.BasisIssuerExclusion,
	}
	usdc, _ := canonical.NewClassicAsset("USDC", validIssuer)
	got, applied, err := supply.Overlay(context.Background(), snap, usdc, resolver)
	if err == nil {
		t.Fatal("expected error from failing resolver; got nil")
	}
	if applied {
		t.Error("applied=true on resolver error; expected false")
	}
	if got.MaxSupply != nil {
		t.Errorf("MaxSupply mutated on error path: %s", got.MaxSupply)
	}
}

// TestOverlay_RejectsNegativeDeclaration — a negative max_supply in
// stellar.toml is nonsensical (asserts "supply can never exist");
// Overlay must NOT apply it. Operators see the junk via their own
// stellar.toml monitoring path; supply silently leaves nil.
func TestOverlay_RejectsNegativeDeclaration(t *testing.T) {
	resolver := &stubMetadataResolver{raw: "-100", ok: true}
	snap := supply.Supply{
		AssetKey: "USDC:GA1", TotalSupply: big.NewInt(1000),
		MaxSupply: nil, Basis: supply.BasisIssuerExclusion,
	}
	usdc, _ := canonical.NewClassicAsset("USDC", validIssuer)
	got, applied, err := supply.Overlay(context.Background(), snap, usdc, resolver)
	if err != nil {
		t.Fatalf("Overlay: %v", err)
	}
	if applied {
		t.Error("applied=true for negative declaration; expected false")
	}
	if got.MaxSupply != nil {
		t.Errorf("MaxSupply set from negative declaration: %s", got.MaxSupply)
	}
}

// TestOverlay_RejectsUnparseableDeclaration — a non-decimal value
// (e.g. "TBD" or "~21M") in stellar.toml falls through with
// applied=false, no error.
func TestOverlay_RejectsUnparseableDeclaration(t *testing.T) {
	resolver := &stubMetadataResolver{raw: "TBD", ok: true}
	snap := supply.Supply{
		AssetKey: "USDC:GA1", TotalSupply: big.NewInt(1000),
		MaxSupply: nil, Basis: supply.BasisIssuerExclusion,
	}
	usdc, _ := canonical.NewClassicAsset("USDC", validIssuer)
	_, applied, err := supply.Overlay(context.Background(), snap, usdc, resolver)
	if err != nil {
		t.Errorf("unparseable declaration should fall through silently; got %v", err)
	}
	if applied {
		t.Error("applied=true for unparseable declaration; expected false")
	}
}

// TestOverlay_NilResolverIsNoop — a deployment without SEP-1
// resolution wired (e.g. early bring-up) should still be safe to
// call Overlay against. No-op, no error.
func TestOverlay_NilResolverIsNoop(t *testing.T) {
	snap := supply.Supply{
		AssetKey: "USDC:GA1", TotalSupply: big.NewInt(1000),
		MaxSupply: nil, Basis: supply.BasisIssuerExclusion,
	}
	usdc, _ := canonical.NewClassicAsset("USDC", validIssuer)
	got, applied, err := supply.Overlay(context.Background(), snap, usdc, nil)
	if err != nil {
		t.Errorf("nil resolver should not error; got %v", err)
	}
	if applied {
		t.Error("applied=true with nil resolver; expected false")
	}
	if got.MaxSupply != nil {
		t.Errorf("MaxSupply set with nil resolver: %s", got.MaxSupply)
	}
}

// TestOverlay_BasisAlreadyOverridePreserved — when an asset already
// has Basis=Override (e.g. operator extended the locked-set but
// didn't supply a max_supply), the Basis stays Override after the
// overlay applies. Same lane, no need to flip.
func TestOverlay_BasisAlreadyOverridePreserved(t *testing.T) {
	resolver := &stubMetadataResolver{raw: "1000000", ok: true}
	snap := supply.Supply{
		AssetKey: "USDC:GA1", TotalSupply: big.NewInt(1000),
		MaxSupply: nil, Basis: supply.BasisOverride,
	}
	usdc, _ := canonical.NewClassicAsset("USDC", validIssuer)
	got, applied, err := supply.Overlay(context.Background(), snap, usdc, resolver)
	if err != nil || !applied {
		t.Fatalf("expected overlay applied; err=%v applied=%v", err, applied)
	}
	if got.Basis != supply.BasisOverride {
		t.Errorf("Basis = %q, want preserved %q", got.Basis, supply.BasisOverride)
	}
}
