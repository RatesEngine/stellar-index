package timescale

import (
	"strings"
	"testing"
)

// ValidateCoinsCursor pins the per-shape rejection. Garbage that
// matched the old loose parser (returns 0, "") would fall through
// silently and produce an empty page that looked like end-of-
// pagination — see the package comment on parseCoinCursor.

func TestValidateCoinsCursor_emptyAlwaysOK(t *testing.T) {
	for _, order := range []CoinsOrder{
		CoinsOrderObservationCountDesc,
		CoinsOrderVolume24hUSDDesc,
	} {
		if err := ValidateCoinsCursor("", order); err != nil {
			t.Errorf("empty cursor must be valid for order %v, got %v", order, err)
		}
	}
}

func TestValidateCoinsCursor_obsCountOrder(t *testing.T) {
	cases := []struct {
		in        string
		wantErr   bool
		wantTagIn string // substring expected in error message
	}{
		{"100:USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN", false, ""},
		{"0:native", false, ""},
		{"99999999999999:XLM-GABC", false, ""},
		{"missing-colon", true, "separator"},
		{"100:", true, "asset_id"},
		{":native", true, "observation_count"},
		{"abc:native", true, "observation_count"},
		{"-5:native", true, "observation_count"}, // negative not produced; reject
		{"1.5:native", true, "observation_count"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			err := ValidateCoinsCursor(tc.in, CoinsOrderObservationCountDesc)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if !strings.Contains(err.Error(), tc.wantTagIn) {
					t.Errorf("expected error containing %q, got %v", tc.wantTagIn, err)
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidateCoinsCursor_volumeOrder(t *testing.T) {
	cases := []struct {
		in      string
		wantErr bool
	}{
		// Empty volume prefix is what nextCoinCursor emits when the
		// last row had a null vol_usd — must round-trip cleanly.
		{":USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN", false},
		{"123.45:native", false},
		{"0:native", false},
		{"1234567890:native", false},
		{"missing-colon", true},
		{"123.45:", true},      // empty asset_id
		{"abc:native", true},   // non-numeric prefix
		{"1.2.3:native", true}, // two dots
		{"-5.0:native", true},  // leading minus
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			err := ValidateCoinsCursor(tc.in, CoinsOrderVolume24hUSDDesc)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}
