package coingecko

import (
	"testing"
	"time"
)

// decimalStringToScaledInt mirrors the helper in polygonforex /
// CMC / exchangeratesapi. CoinGecko prices come back as JSON
// numbers and we marshal them to decimal strings; this helper is
// the precision-preserving converter to scaled integers.

func TestDecimalStringToScaledInt_edges(t *testing.T) {
	cases := []struct {
		in        string
		decimals  int
		want      string
		wantError bool
	}{
		{"1.0", 8, "100000000", false},
		{"-0.5", 8, "-50000000", false},
		{".25", 8, "25000000", false},
		{"1.999999999", 8, "199999999", false},
		{"42", 0, "42", false},
		{"", 8, "", true},
		{"1e3", 8, "", true},
		{"abc", 8, "", true},
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
	p := NewPoller()
	p.Interval = 0
	if got := p.PollInterval(); got != DefaultPollInterval {
		t.Errorf("PollInterval(zero) = %v, want %v", got, DefaultPollInterval)
	}
	p.Interval = 7 * time.Second
	if got := p.PollInterval(); got != 7*time.Second {
		t.Errorf("PollInterval(7s) = %v, want 7s", got)
	}
}
