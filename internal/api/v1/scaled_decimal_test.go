package v1

import (
	"math/big"
	"testing"
)

// scaledDecimalString renders integer/10^decimals as a decimal
// string. Used by the SEP-40 passthrough envelope to format
// oracle prices preserving full integer precision.

func TestScaledDecimalString(t *testing.T) {
	cases := []struct {
		name     string
		value    *big.Int
		decimals uint8
		want     string
	}{
		{"nil → \"0\"", nil, 14, "0"},
		{"decimals=0 returns raw integer", big.NewInt(12345), 0, "12345"},
		{"basic 14-decimals", big.NewInt(100_000_000_000_000), 14, "1.00000000000000"},
		{"fractional 14-decimals", big.NewInt(12_420_000_000_000), 14, "0.12420000000000"},
		{"negative", big.NewInt(-100_000_000_000_000), 14, "-1.00000000000000"},
		{"small fraction needs padding", big.NewInt(1), 14, "0.00000000000001"},
		{"zero with decimals", big.NewInt(0), 14, "0.00000000000000"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := scaledDecimalString(tc.value, tc.decimals)
			if got != tc.want {
				t.Errorf("scaledDecimalString(%v, %d) = %q, want %q",
					tc.value, tc.decimals, got, tc.want)
			}
		})
	}
}

func TestScaledDecimalString_doesNotMutateInput(t *testing.T) {
	// scaledDecimalString takes Abs internally; a regression that
	// mutated the caller's *big.Int would be a subtle data-corruption
	// bug. Verify with a negative input.
	in := big.NewInt(-42)
	want := big.NewInt(-42)
	_ = scaledDecimalString(in, 14)
	if in.Cmp(want) != 0 {
		t.Errorf("input mutated: got %s, want %s (Abs leaked through)", in, want)
	}
}
