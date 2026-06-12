package forex

import (
	"math"
	"testing"
)

// TestIsFiniteFloat covers the large-magnitude case that the old
// `f != f+1` inf check got wrong: for |f| >= 2^53, f+1 rounds back
// to f, so a perfectly finite value used to read as non-finite.
func TestIsFiniteFloat(t *testing.T) {
	cases := []struct {
		name string
		in   float64
		want bool
	}{
		{"zero", 0, true},
		{"small", 1.2345, true},
		{"negative", -42.5, true},
		{"large_positive_above_2e53", 1e300, true},
		{"exactly_2e53", math.Pow(2, 53), true},
		{"max_float64", math.MaxFloat64, true},
		{"nan", math.NaN(), false},
		{"pos_inf", math.Inf(1), false},
		{"neg_inf", math.Inf(-1), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isFiniteFloat(tc.in); got != tc.want {
				t.Errorf("isFiniteFloat(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
