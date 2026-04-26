package v1

import (
	"math/big"
	"testing"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// tradeRowFrom: the existing handler-level history_test exercises
// the explicit-decimals path; this pins the default-on-non-positive
// branch (decimals <= 0 → 10 dp) without going through HTTP.

// ratToDecimal: VWAP/TWAP price formatter. Pin nil-input,
// negative-digits clamp, sign preservation, digits=0 fast-path,
// and the regular fractional path.

func TestRatToDecimal_nilReturnsZero(t *testing.T) {
	if got := ratToDecimal(nil, 10); got != "0" {
		t.Errorf("ratToDecimal(nil) = %q, want \"0\"", got)
	}
}

func TestRatToDecimal_negativeDigitsClamped(t *testing.T) {
	// digits<0 must be clamped to 0; output is the integer part
	// only with no fractional separator.
	r := big.NewRat(7, 2) // 3.5
	got := ratToDecimal(r, -3)
	if got != "3" {
		t.Errorf("ratToDecimal(7/2, -3) = %q, want \"3\" (clamp digits<0 → 0)", got)
	}
}

func TestRatToDecimal_digitsZeroFastPath(t *testing.T) {
	// digits=0 short-circuits to the integer-part string with no
	// decimal point.
	r := big.NewRat(123, 1)
	got := ratToDecimal(r, 0)
	if got != "123" {
		t.Errorf("ratToDecimal(123/1, 0) = %q, want \"123\"", got)
	}
}

func TestRatToDecimal_signPreserved(t *testing.T) {
	r := big.NewRat(-1, 4) // -0.25
	got := ratToDecimal(r, 4)
	if got[0] != '-' {
		t.Errorf("ratToDecimal(-1/4, 4) = %q, want leading \"-\"", got)
	}
	if got != "-0.2500" {
		t.Errorf("ratToDecimal(-1/4, 4) = %q, want \"-0.2500\"", got)
	}
}

func TestRatToDecimal_fractional(t *testing.T) {
	cases := []struct {
		num, den int64
		digits   int
		want     string
	}{
		{1, 4, 4, "0.2500"},
		{1, 3, 4, "0.3333"}, // truncating, not rounding
		{2, 1, 4, "2.0000"},
		{0, 1, 4, "0.0000"},
	}
	for _, tc := range cases {
		got := ratToDecimal(big.NewRat(tc.num, tc.den), tc.digits)
		if got != tc.want {
			t.Errorf("ratToDecimal(%d/%d, %d) = %q, want %q",
				tc.num, tc.den, tc.digits, got, tc.want)
		}
	}
}

// detailFromAsset shapes the wire payload for every Asset variant.
// Pin all four shapes plus the nullable-field wiring so a regression
// can't quietly drop Issuer/ContractID from the JSON response.

func TestDetailFromAsset_native(t *testing.T) {
	a := canonical.NativeAsset()
	d := detailFromAsset(a)
	if d.AssetID != "native" {
		t.Errorf("AssetID = %q, want \"native\"", d.AssetID)
	}
	if d.Issuer != nil {
		t.Errorf("Issuer = %v, want nil for native", d.Issuer)
	}
	if d.ContractID != nil {
		t.Errorf("ContractID = %v, want nil for native", d.ContractID)
	}
	if d.Decimals != 7 {
		t.Errorf("Decimals = %d, want 7 (XLM stroops)", d.Decimals)
	}
	if d.Sep1Status != "not_applicable" {
		t.Errorf("Sep1Status = %q, want \"not_applicable\"", d.Sep1Status)
	}
}

func TestDetailFromAsset_classic(t *testing.T) {
	a, err := canonical.NewClassicAsset("USDC",
		"GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")
	if err != nil {
		t.Fatalf("NewClassicAsset: %v", err)
	}
	d := detailFromAsset(a)
	if d.Code != "USDC" {
		t.Errorf("Code = %q, want \"USDC\"", d.Code)
	}
	if d.Issuer == nil || *d.Issuer == "" {
		t.Errorf("Issuer = %v, want populated", d.Issuer)
	}
	if d.ContractID != nil {
		t.Errorf("ContractID = %v, want nil for classic", d.ContractID)
	}
}

func TestDetailFromAsset_soroban(t *testing.T) {
	cid := "CCQXWMZVM3KRTXTUPTN53YHL272QGKF32L7XEDNZ2S6OSUFK3NFBGG5M"
	a, err := canonical.NewSorobanAsset(cid)
	if err != nil {
		t.Fatalf("NewSorobanAsset: %v", err)
	}
	d := detailFromAsset(a)
	if d.ContractID == nil || *d.ContractID != cid {
		t.Errorf("ContractID = %v, want %q", d.ContractID, cid)
	}
	if d.Issuer != nil {
		t.Errorf("Issuer = %v, want nil for soroban", d.Issuer)
	}
}

func TestDetailFromAsset_fiat(t *testing.T) {
	a, err := canonical.ParseAsset("fiat:USD")
	if err != nil {
		t.Fatalf("ParseAsset: %v", err)
	}
	d := detailFromAsset(a)
	if d.AssetID != "fiat:USD" {
		t.Errorf("AssetID = %q, want \"fiat:USD\"", d.AssetID)
	}
	if d.Issuer != nil {
		t.Errorf("Issuer = %v, want nil for fiat", d.Issuer)
	}
	if d.ContractID != nil {
		t.Errorf("ContractID = %v, want nil for fiat", d.ContractID)
	}
}

// mustParseAsset has a panic branch reached only when defaultPriceQuote
// (or any other compile-time constant the package builds atop it)
// drifts from the canonical allow-list. Pin both arms.

func TestMustParseAsset_validReturnsAsset(t *testing.T) {
	a := mustParseAsset("native")
	if a.Type != canonical.AssetNative {
		t.Errorf("Type = %q, want native", a.Type)
	}
}

func TestMustParseAsset_invalidPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Error("expected panic on garbage input, got none")
		}
	}()
	_ = mustParseAsset("definitely-not-a-real-asset-id")
}

func TestTradeRowFrom_defaultDecimalsOnZero(t *testing.T) {
	xlm, _ := canonical.ParseAsset("native")
	usd, _ := canonical.ParseAsset("fiat:USD")
	pair, _ := canonical.NewPair(xlm, usd)
	tr := canonical.Trade{
		Source:      "soroswap",
		Ledger:      52_000_000,
		TxHash:      "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		OpIndex:     0,
		Timestamp:   time.Unix(1_770_000_000, 0).UTC(),
		Pair:        pair,
		BaseAmount:  canonical.NewAmount(big.NewInt(1_000_000)),
		QuoteAmount: canonical.NewAmount(big.NewInt(2_000_000)),
	}
	// decimals=0 must trigger the default (10 dp) rather than emit
	// an integer-only price string.
	got := tradeRowFrom(tr, 0)
	if got.Price == "2" || got.Price == "" {
		t.Errorf("Price = %q on decimals=0; expected default 10-dp formatting (got the integer-only path)",
			got.Price)
	}
	// decimals=-3 (also <= 0) must take the same default path.
	gotNeg := tradeRowFrom(tr, -3)
	if gotNeg.Price != got.Price {
		t.Errorf("decimals<0 (%q) and decimals=0 (%q) should both apply the default",
			gotNeg.Price, got.Price)
	}
}
