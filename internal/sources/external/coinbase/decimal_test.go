package coinbase

import (
	"strings"
	"testing"
)

// Error and edge-case tests for decimalStringToScaledInt. The
// existing parse_test.go's TestDecimalStringToScaledInt_CoinbasePrecision
// exercises only happy paths; this file fills in the error
// branches to match the shape of the binance + bitstamp suites.

func TestDecimalStringToScaledInt_emptyRejected(t *testing.T) {
	_, err := decimalStringToScaledInt("", 8)
	if err == nil {
		t.Error("expected error on empty string, got nil")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error %q missing \"empty\" fragment", err.Error())
	}
}

func TestDecimalStringToScaledInt_scientificRejected(t *testing.T) {
	for _, s := range []string{"1e3", "1.5E-3", "2e0"} {
		_, err := decimalStringToScaledInt(s, 8)
		if err == nil {
			t.Errorf("expected error on %q, got nil", s)
		}
		if !strings.Contains(err.Error(), "scientific") {
			t.Errorf("error %q missing \"scientific\" fragment", err.Error())
		}
	}
}

func TestDecimalStringToScaledInt_nonNumericRejected(t *testing.T) {
	for _, s := range []string{"not-a-number", "0xff", "abc"} {
		_, err := decimalStringToScaledInt(s, 8)
		if err == nil {
			t.Errorf("expected error on %q, got nil", s)
		}
	}
}

func TestDecimalStringToScaledInt_negative(t *testing.T) {
	got, err := decimalStringToScaledInt("-1.5", 8)
	if err != nil {
		t.Fatalf("decimalStringToScaledInt(-1.5): %v", err)
	}
	if got.Sign() >= 0 {
		t.Errorf("Sign() = %d, want -1", got.Sign())
	}
}

func TestDecimalStringToScaledInt_leadingDotAndTrailingDot(t *testing.T) {
	// ".5" — empty integer part, fractional only.
	got, err := decimalStringToScaledInt(".5", 8)
	if err != nil {
		t.Fatalf(".5: %v", err)
	}
	if got.Int64() != 50_000_000 {
		t.Errorf(".5 → %s, want 50000000", got)
	}
	// "5." — empty fractional part, integer only.
	got, err = decimalStringToScaledInt("5.", 8)
	if err != nil {
		t.Fatalf("5.: %v", err)
	}
	if got.Int64() != 500_000_000 {
		t.Errorf("5. → %s, want 500000000", got)
	}
}
