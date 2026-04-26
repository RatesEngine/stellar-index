package coinmarketcap

import (
	"testing"
	"time"
)

// decimalStringToScaledInt mirrors the helper in polygonforex /
// exchangeratesapi. CMC's prices come from /v2 quotes/latest as
// floats; the helper is the back-stop converter when we already
// have a decimal string. Pin the edge cases so a refactor that
// drops scientific-notation rejection (silently producing an
// invalid integer) is caught.

func TestDecimalStringToScaledInt_edges(t *testing.T) {
	cases := []struct {
		in        string
		decimals  int
		want      string
		wantError bool
	}{
		{"0.5", 8, "50000000", false},
		{"-1.0", 8, "-100000000", false},
		{".25", 8, "25000000", false},
		{"100", 0, "100", false},
		{"1.999999999", 8, "199999999", false}, // truncates
		{"", 8, "", true},
		{"1e3", 8, "", true},
		{"1.2.3", 8, "", true},
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
