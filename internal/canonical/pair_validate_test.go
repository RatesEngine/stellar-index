package canonical_test

import (
	"errors"
	"strings"
	"testing"

	c "github.com/RatesEngine/rates-engine/internal/canonical"
)

// Pair.Validate has three branches (base error, quote error,
// identity collision) that the existing pair_test.go suite only
// hits transitively via NewPair. Direct tests pin each branch so
// a refactor that drops any of them surfaces here, not in a
// downstream caller's confusing stack trace.

func TestPair_Validate_zeroValueSurfacesBaseError(t *testing.T) {
	// Zero-value Pair: both Base and Quote are zero-value Asset.
	// Validate hits the Base branch first and fails there.
	var p c.Pair
	err := p.Validate()
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if !strings.Contains(err.Error(), "base") {
		t.Errorf("error %q missing \"base\" fragment", err.Error())
	}
}

func TestPair_Validate_validBaseInvalidQuote(t *testing.T) {
	// Base is valid (native XLM); Quote is zero-value → "quote"-
	// tagged error.
	p := c.Pair{
		Base:  c.NativeAsset(),
		Quote: c.Asset{}, // zero-value, fails Validate
	}
	err := p.Validate()
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if !strings.Contains(err.Error(), "quote") {
		t.Errorf("error %q missing \"quote\" fragment", err.Error())
	}
}

func TestPair_Validate_identityCollision(t *testing.T) {
	// Both sides are valid native XLM — Validate must catch the
	// identity collision and return ErrPairMismatch.
	p := c.Pair{Base: c.NativeAsset(), Quote: c.NativeAsset()}
	err := p.Validate()
	if err == nil {
		t.Fatal("expected validation error on identity pair, got nil")
	}
	if !errors.Is(err, c.ErrPairMismatch) {
		t.Errorf("error chain missing ErrPairMismatch: %v", err)
	}
}

// Asset.Value with a zero-value Asset must fail the inner Validate
// — the validation error propagates out through driver.Valuer.
func TestAsset_Value_zeroValueRejected(t *testing.T) {
	var a c.Asset
	_, err := a.Value()
	if err == nil {
		t.Error("expected error from Value() on zero-value Asset, got nil")
	}
}

// Asset.Scan against a typed-nil source must zero the receiver
// (a NULL column round-trips to a zero-value Asset). Documented
// behaviour at the Scan callsite.
func TestAsset_Scan_typedNilZeroesReceiver(t *testing.T) {
	a := c.NativeAsset()
	if err := a.Scan(nil); err != nil {
		t.Fatalf("Scan(nil): %v", err)
	}
	if !a.IsZero() {
		t.Errorf("after Scan(nil), Asset = %+v, want zero-value", a)
	}
}
