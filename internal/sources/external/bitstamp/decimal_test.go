package bitstamp

import (
	"math/big"
	"strings"
	"testing"
)

// decimalStringToScaledInt converts a decimal string (e.g. "0.123")
// to an integer scaled by 10^targetDecimals. The implementation
// has a long branch tree (sign, dot-position, fractional padding /
// truncation) — table-driven test mirrors the binance + coinbase
// suites for symmetry.

func TestDecimalStringToScaledInt(t *testing.T) {
	cases := []struct {
		name        string
		in          string
		targetDP    int
		want        *big.Int
		expectError bool
	}{
		{"integer only at 8 dp", "1", 8, big.NewInt(100_000_000), false},
		{"with fraction", "0.12", 8, big.NewInt(12_000_000), false},
		{"max-precision fraction", "0.12345678", 8, big.NewInt(12_345_678), false},
		{"truncate over max precision", "0.123456789999", 8, big.NewInt(12_345_678), false},
		{"negative", "-1.5", 8, big.NewInt(-150_000_000), false},
		{"leading dot (no integer part)", ".5", 8, big.NewInt(50_000_000), false},
		{"trailing dot (no fraction)", "5.", 8, big.NewInt(500_000_000), false},
		{"zero", "0", 8, big.NewInt(0), false},
		{"large integer", "1000000", 0, big.NewInt(1_000_000), false},
		{"different dp = 2", "1.234", 2, big.NewInt(123), false},

		// Error cases.
		{"empty", "", 8, nil, true},
		{"scientific lowercase", "1e3", 8, nil, true},
		{"scientific uppercase", "1E3", 8, nil, true},
		{"non-numeric", "not-a-number", 8, nil, true},
		{"hex-looking", "0xff", 8, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := decimalStringToScaledInt(tc.in, tc.targetDP)
			if tc.expectError {
				if err == nil {
					t.Errorf("expected error, got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("decimalStringToScaledInt(%q, %d): %v", tc.in, tc.targetDP, err)
			}
			if got.Cmp(tc.want) != 0 {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestDecimalStringToScaledInt_scientificNotationRejected(t *testing.T) {
	// The function deliberately rejects scientific notation rather
	// than silently producing the wrong answer — Bitstamp's REST
	// shouldn't ever serialize that way, but a future API change
	// must surface as a parse failure not a wrong-value bug.
	for _, s := range []string{"1e10", "1.5E-3", "2e0"} {
		_, err := decimalStringToScaledInt(s, 8)
		if err == nil {
			t.Errorf("decimalStringToScaledInt(%q) returned nil error; want rejection", s)
		}
		if !strings.Contains(err.Error(), "scientific") {
			t.Errorf("error %q missing \"scientific\" fragment", err.Error())
		}
	}
}
