package ecb

import (
	"math"
	"testing"
	"time"
)

// floatToScaledInt rejects NaN and negative values; ECB rates are
// always positive (USD per EUR, etc.). decimalStringToScaledInt is
// the precision-preserving worker for the formatted JSON value.

func TestFloatToScaledInt_rejectsNaN(t *testing.T) {
	if _, err := floatToScaledInt(math.NaN(), 8); err == nil {
		t.Error("expected error for NaN, got nil")
	}
}

func TestFloatToScaledInt_rejectsNegative(t *testing.T) {
	if _, err := floatToScaledInt(-1.5, 8); err == nil {
		t.Error("expected error for negative, got nil")
	}
}

func TestFloatToScaledInt_happyPath(t *testing.T) {
	got, err := floatToScaledInt(1.5, 8)
	if err != nil {
		t.Fatalf("floatToScaledInt(1.5, 8): %v", err)
	}
	if got.String() != "150000000" {
		t.Errorf("got %s, want 150000000", got.String())
	}
}

func TestDecimalStringToScaledInt_edges(t *testing.T) {
	cases := []struct {
		in        string
		decimals  int
		want      string
		wantError bool
	}{
		{"1.5", 8, "150000000", false},
		{"0.0001", 8, "10000", false},
		{".25", 8, "25000000", false},
		{"-1.0", 8, "-100000000", false},
		{"1.999999999", 8, "199999999", false}, // truncates
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
	p.Interval = 5 * time.Minute
	if got := p.PollInterval(); got != 5*time.Minute {
		t.Errorf("PollInterval(5m) = %v, want 5m", got)
	}
}
