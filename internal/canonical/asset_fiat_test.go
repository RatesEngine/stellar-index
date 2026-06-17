package canonical_test

import (
	"errors"
	"testing"

	c "github.com/StellarIndex/stellar-index/internal/canonical"
)

// TestIsKnownFiat_AllowList exercises the ADR-0010 allow-list. Each
// row must round-trip — adding a new code is a one-line append in
// asset_fiat.go's knownFiatCodes map; this test catches typos
// (lowercase, accidental whitespace, wrong length).
func TestIsKnownFiat_AllowList(t *testing.T) {
	t.Run("known codes pass", func(t *testing.T) {
		// Sample of the codes we explicitly committed to up front
		// + a couple added post-Reflector-FX (ARS, CLP, …).
		known := []string{"USD", "EUR", "GBP", "JPY", "CNY", "ARS", "MXN", "BRL"}
		for _, code := range known {
			if !c.IsKnownFiat(code) {
				t.Errorf("IsKnownFiat(%q) = false, want true", code)
			}
		}
	})

	t.Run("unknown codes fail", func(t *testing.T) {
		// Common foot-guns: lowercase, alias-style (USDT vs USD), and
		// codes we deliberately don't ship (SDR, XAU).
		unknown := []string{"usd", "Usd", "USDT", "SDR", "XAU", "XYZ", ""}
		for _, code := range unknown {
			if c.IsKnownFiat(code) {
				t.Errorf("IsKnownFiat(%q) = true, want false", code)
			}
		}
	})

	t.Run("wrong-length strings rejected", func(t *testing.T) {
		// ISO-4217 is 3-letter; 2- or 4-letter inputs should fail.
		bad := []string{"US", "USDX", "U", "USDOLLAR"}
		for _, code := range bad {
			if c.IsKnownFiat(code) {
				t.Errorf("IsKnownFiat(%q) = true, want false (wrong length)", code)
			}
		}
	})
}

// TestNewFiatAsset_HappyPath builds an Asset via the public
// constructor and pins the resulting Type/Code combination.
func TestNewFiatAsset_HappyPath(t *testing.T) {
	a, err := c.NewFiatAsset("USD")
	if err != nil {
		t.Fatalf("NewFiatAsset(USD): %v", err)
	}
	if a.Type != c.AssetFiat {
		t.Errorf("Type = %v, want AssetFiat", a.Type)
	}
	if a.Code != "USD" {
		t.Errorf("Code = %q, want USD", a.Code)
	}
}

// TestNewFiatAsset_RejectsUnknown — the constructor is the
// boundary that prevents an operator from configuring an
// off-allow-list code at startup. Errors MUST wrap
// ErrInvalidAsset so callers can classify via errors.Is.
func TestNewFiatAsset_RejectsUnknown(t *testing.T) {
	_, err := c.NewFiatAsset("XYZ")
	if err == nil {
		t.Fatal("NewFiatAsset(XYZ) returned nil error")
	}
	if !errors.Is(err, c.ErrInvalidAsset) {
		t.Errorf("err = %v; want errors.Is(_, ErrInvalidAsset) = true", err)
	}
}

// TestNewFiatAsset_RejectsCaseSensitive pins the contract: codes
// must be uppercase. ISO-4217 codes are uppercase; permitting
// case-insensitive input would mean two different config keys
// (`fiat:usd` vs `fiat:USD`) silently route to the same asset.
func TestNewFiatAsset_RejectsCaseSensitive(t *testing.T) {
	for _, code := range []string{"usd", "Usd", "USd"} {
		_, err := c.NewFiatAsset(code)
		if err == nil {
			t.Errorf("NewFiatAsset(%q) accepted lowercase/mixed case; allow-list is case-sensitive", code)
		}
	}
}
