package v1

import (
	"math/big"
	"testing"
)

// TestUsdMarketValue covers the F2 market-cap math helper directly
// (internal access — usdMarketValue is unexported because nothing
// outside the package needs it). Integration coverage of the helper
// via /v1/assets/{id} lives in assets_f2_test.go.
func TestUsdMarketValue(t *testing.T) {
	tests := []struct {
		name     string
		stroops  string
		price    string
		decimals int
		want     string
	}{
		// 100 XLM (1_000_000_000 stroops, 7 decimals) × $0.07 = $7.00
		{"XLM 100×0.07", "1000000000", "0.07", 7, "7.00"},
		// 1 USDC (10_000_000 stroops, 7 decimals) × $1 = $1.00
		{"USDC 1×1", "10000000", "1", 7, "1.00"},
		// 0 stroops reads $0.00 (legitimate zero, not error).
		{"zero stroops", "0", "0.07", 7, "0.00"},
		// Sub-cent products truncate to $0.00 via FloatString(2).
		{"sub-cent truncates", "1", "0.0001", 7, "0.00"},
		// Very large numbers stay precise — no float underflow.
		// 500_018_068_120_000_000 stroops / 10^7 = 50_001_806_812 XLM
		// × $0.07 = $3,500,126,476.84.
		{"giant XLM", "500018068120000000", "0.0700000", 7, "3500126476.84"},
		// decimals=0 means "stroops are already asset units"
		// (as for some SEP-41 tokens that emit raw integers).
		{"decimals=0", "100", "1.50", 0, "150.00"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := usdMarketValue(mustBigIntInternal(tc.stroops), tc.price, tc.decimals)
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestUsdMarketValue_BadInputs(t *testing.T) {
	if _, err := usdMarketValue(nil, "1", 7); err == nil {
		t.Error("expected error for nil amountStroops")
	}
	if _, err := usdMarketValue(mustBigIntInternal("100"), "not-a-price", 7); err == nil {
		t.Error("expected error for unparseable price")
	}
	if _, err := usdMarketValue(mustBigIntInternal("100"), "1", -1); err == nil {
		t.Error("expected error for negative decimals")
	}
}

func mustBigIntInternal(s string) *big.Int {
	v, ok := new(big.Int).SetString(s, 10)
	if !ok {
		panic("mustBigIntInternal: bad input " + s)
	}
	return v
}
