package exchangeratesapi

import (
	"testing"
	"time"
)

// decimalStringToScaledInt is the precision-preserving converter
// for ExchangeRatesAPI quotes. Unlike the polygonforex/CMC
// variants, this one ACCEPTS scientific notation — small inverted
// rates ("2e-10") are normalised through ParseFloat. Pin both
// branches so a refactor can't accidentally drop scientific support
// (we'd start losing exotic-pair rates) or break the empty/garbage
// rejection.

func TestDecimalStringToScaledInt_edges(t *testing.T) {
	cases := []struct {
		in        string
		decimals  int
		want      string
		wantError bool
	}{
		{"1.0", 8, "100000000", false},
		{"-0.5", 8, "-50000000", false},
		{".75", 8, "75000000", false},
		{"1.123456789", 8, "112345678", false},
		{"42", 0, "42", false},
		{"2e10", 8, "2000000000000000000", false}, // sci-notation accepted
		{"", 8, "", true},
		{"NaN", 8, "", true},
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
