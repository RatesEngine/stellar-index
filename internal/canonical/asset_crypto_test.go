package canonical

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestNewCryptoAsset_accepted(t *testing.T) {
	cases := []string{"BTC", "ETH", "USDT", "USDC", "SOL", "XRP", "ADA", "AVAX", "DOT", "LINK"}
	for _, code := range cases {
		t.Run(code, func(t *testing.T) {
			a, err := NewCryptoAsset(code)
			if err != nil {
				t.Fatalf("NewCryptoAsset(%q): %v", code, err)
			}
			if a.Type != AssetCrypto {
				t.Errorf("Type = %q", a.Type)
			}
			if a.Code != code {
				t.Errorf("Code = %q, want %q", a.Code, code)
			}
			if err := a.Validate(); err != nil {
				t.Errorf("Validate: %v", err)
			}
		})
	}
}

func TestNewCryptoAsset_rejected(t *testing.T) {
	cases := []string{"NOTACOIN", "", "btc" /* lowercase */, "SOMENEWTOKEN"}
	for _, code := range cases {
		t.Run(code, func(t *testing.T) {
			_, err := NewCryptoAsset(code)
			if !errors.Is(err, ErrInvalidAsset) {
				t.Errorf("expected ErrInvalidAsset for %q, got %v", code, err)
			}
		})
	}
}

func TestCryptoAsset_wireForm(t *testing.T) {
	// String round-trips through ParseAsset.
	a, err := NewCryptoAsset("BTC")
	if err != nil {
		t.Fatal(err)
	}
	s := a.String()
	if s != "crypto:BTC" {
		t.Errorf("String() = %q, want %q", s, "crypto:BTC")
	}
	parsed, err := ParseAsset(s)
	if err != nil {
		t.Fatalf("ParseAsset(%q): %v", s, err)
	}
	if !parsed.Equal(a) {
		t.Errorf("round-trip lost info: %+v vs %+v", parsed, a)
	}
}

func TestCryptoAsset_distinctFromClassicSameCode(t *testing.T) {
	// Intentionally different: crypto:USDC vs classic USDC-<issuer>.
	cryptoUSDC, _ := NewCryptoAsset("USDC")
	classicUSDC, _ := NewClassicAsset("USDC", "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")
	if cryptoUSDC.Equal(classicUSDC) {
		t.Error("crypto:USDC should not equal classic USDC — different semantics (global vs Circle's Stellar-issued)")
	}
}

func TestCryptoAsset_json(t *testing.T) {
	a, _ := NewCryptoAsset("ETH")

	// MarshalJSON emits the canonical string form.
	b, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `"crypto:ETH"` {
		t.Errorf("MarshalJSON = %q, want %q", b, `"crypto:ETH"`)
	}

	// String round-trips through UnmarshalJSON.
	var a2 Asset
	if err := json.Unmarshal(b, &a2); err != nil {
		t.Fatalf("UnmarshalJSON: %v", err)
	}
	if !a.Equal(a2) {
		t.Errorf("JSON round-trip lost info: %+v vs %+v", a, a2)
	}
}

func TestCryptoAsset_validateRejectsForbiddenFields(t *testing.T) {
	// If someone manually constructs {Type: AssetCrypto, Code: "BTC",
	// Issuer: "G…"}, Validate must reject — crypto carries only Code.
	a := Asset{Type: AssetCrypto, Code: "BTC", Issuer: "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"}
	if err := a.Validate(); !errors.Is(err, ErrInvalidAsset) {
		t.Errorf("expected ErrInvalidAsset for issuer on crypto asset, got %v", err)
	}
	a2 := Asset{Type: AssetCrypto, Code: "BTC", ContractID: "CA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"}
	if err := a2.Validate(); !errors.Is(err, ErrInvalidAsset) {
		t.Errorf("expected ErrInvalidAsset for contract_id on crypto asset, got %v", err)
	}
}

func TestIsKnownCrypto(t *testing.T) {
	if !IsKnownCrypto("BTC") {
		t.Error("BTC should be known")
	}
	if IsKnownCrypto("NOTAREALTICKER") {
		t.Error("NOTAREALTICKER should not be known")
	}
}
