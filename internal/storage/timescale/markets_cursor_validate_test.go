package timescale

import (
	"strings"
	"testing"
)

// ValidateMarketsCursor pins the per-shape rejection. Without
// validation, MarketsOrderPair garbage falls through to a
// collation-dependent lexicographic skip; MarketsOrderVolume24hDesc
// garbage hits a Postgres `numeric` cast error and 500s.

func TestValidateMarketsCursor_emptyAlwaysOK(t *testing.T) {
	for _, order := range []MarketsOrder{
		MarketsOrderPair,
		MarketsOrderVolume24hDesc,
	} {
		if err := ValidateMarketsCursor("", order); err != nil {
			t.Errorf("empty cursor must be valid for order %v, got %v", order, err)
		}
	}
}

func TestValidateMarketsCursor_pairOrder(t *testing.T) {
	cases := []struct {
		in        string
		wantErr   bool
		wantTagIn string
	}{
		{"native|USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN", false, ""},
		{"fiat:USD|fiat:EUR", false, ""},
		{"crypto:BTC|native", false, ""},
		// Bare contract-strkey base/quote are valid asset IDs.
		{"CDLZFC3SYJYDZT7K67VZ75HPJVIEUVNIXF47ZG2FB2RMQQVU2HHGCYSC|native", false, ""},
		{"missing-pipe", true, "separator in pair"},
		{"|native", true, "missing base or quote"},
		{"native|", true, "missing base or quote"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			err := ValidateMarketsCursor(tc.in, MarketsOrderPair)
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

func TestValidateMarketsCursor_volumeOrder(t *testing.T) {
	cases := []struct {
		in        string
		wantErr   bool
		wantTagIn string
	}{
		{"123.45:native|USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN", false, ""},
		// Empty volume prefix is what encodeMarketsCursor emits when
		// the last row had a null vol_usd.
		{":native|USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN", false, ""},
		// fiat:USD pair with multiple colons — first ':' is
		// vol/pair sep, suffix retains its colons.
		{"100:fiat:USD|fiat:EUR", false, ""},
		{"missing-colon", true, "':' separator"},
		{"100:", true, "missing pair suffix"},
		{"100:nopipehere", true, "separator in pair"},
		{"abc:native|USDC-G", true, "non-numeric volume prefix"},
		{"1.2.3:native|USDC-G", true, "non-numeric volume prefix"},
		{"-5.0:native|USDC-G", true, "non-numeric volume prefix"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			err := ValidateMarketsCursor(tc.in, MarketsOrderVolume24hDesc)
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
