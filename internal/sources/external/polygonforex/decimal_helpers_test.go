package polygonforex

import (
	"math/big"
	"strings"
	"testing"
	"time"
)

// decimalStringToScaledInt + intToDecimalString are inverses
// modulo truncation. Existing tests exercise the happy path via
// snapshotResponse parsing; pin the documented edge cases so a
// refactor that breaks negative-handling, fractional truncation,
// or the missing-integer-part normalisation gets caught.

func TestDecimalStringToScaledInt_edges(t *testing.T) {
	cases := []struct {
		in        string
		decimals  int
		want      string // *big.Int.String()
		wantError bool
	}{
		{"1.5", 8, "150000000", false},
		{"-2.25", 8, "-225000000", false},
		{".5", 8, "50000000", false},           // missing int part normalises to "0"
		{"1.123456789", 8, "112345678", false}, // truncates extra fraction
		{"1", 0, "1", false},
		{"", 8, "", true},    // empty string rejected
		{"1e9", 8, "", true}, // scientific notation rejected
		{"abc", 8, "", true}, // not-a-number rejected
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, err := decimalStringToScaledInt(c.in, c.decimals)
			if c.wantError {
				if err == nil {
					t.Errorf("expected error for %q, got %v", c.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.String() != c.want {
				t.Errorf("got %s, want %s", got.String(), c.want)
			}
		})
	}
}

// intToDecimalString must left-pad small integers with zeros so
// "1" at decimals=8 → "0.00000001", not ".00000001". This catches
// the regression where the leading-zero pad was dropped in a
// refactor and downstream parsers rejected the non-canonical form.
func TestIntToDecimalString_edges(t *testing.T) {
	cases := []struct {
		nStr     string
		decimals int
		want     string
	}{
		{"0", 8, "0.00000000"},
		{"0", 0, "0"},
		{"1", 8, "0.00000001"}, // requires left-pad
		{"100000000", 8, "1.00000000"},
		{"150000000", 8, "1.50000000"},
		{"99999999", 8, "0.99999999"},
	}
	for _, c := range cases {
		t.Run(c.nStr+"@"+strings.Repeat("d", c.decimals), func(t *testing.T) {
			n, _ := new(big.Int).SetString(c.nStr, 10)
			got := intToDecimalString(n, c.decimals)
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// PollInterval defaults to DefaultPollInterval when Interval is
// zero/negative; honours the operator override otherwise. Pin both
// branches — accidentally always returning the override (or
// always the default) would make the operator-tunable poll
// frequency a fiction.
func TestPoller_PollInterval_defaultAndOverride(t *testing.T) {
	p, err := NewPoller("k")
	if err != nil {
		t.Fatalf("NewPoller: %v", err)
	}
	p.Interval = 0
	if got := p.PollInterval(); got != DefaultPollInterval {
		t.Errorf("PollInterval(zero) = %v, want %v", got, DefaultPollInterval)
	}
	p.Interval = 7 * time.Second
	if got := p.PollInterval(); got != 7*time.Second {
		t.Errorf("PollInterval(7s) = %v, want 7s", got)
	}
}
