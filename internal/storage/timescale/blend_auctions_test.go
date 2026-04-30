package timescale

import (
	"math/big"
	"strings"
	"testing"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/sources/blend"
)

// canonicalSorobanAsset is a small helper because the test
// fixtures are built with a known-good Soroban contract id (32-byte
// hex prefix → C-strkey).
func canonicalSorobanAsset(t *testing.T, strkeyAddr string) canonical.Asset {
	t.Helper()
	a, err := canonical.NewSorobanAsset(strkeyAddr)
	if err != nil {
		t.Fatalf("NewSorobanAsset(%q): %v", strkeyAddr, err)
	}
	return a
}

func TestEncodeBlendAssetAmounts_RoundTrip(t *testing.T) {
	// Two-token bid map.
	asset1 := canonicalSorobanAsset(t, "CAS3J7GYLGXMF6TDJBBYYSE3HQ6BBSMLNUQ34T6TZMYMW2EVH34XOWMA") // XLM SAC
	asset2 := canonicalSorobanAsset(t, "CCW67TSZV3SSS2HXMBQ5JFGCKJNXKZM7UQUWUZPUTHXSTZLEO7SJMI75") // some other Soroban contract
	in := []blend.AssetAmount{
		{Asset: asset1, Amount: big.NewInt(1_000_000_000_000)}, // 12-digit i128 — well over int64 ceiling for a stress test
		{Asset: asset2, Amount: big.NewInt(7)},
	}
	encoded, err := encodeBlendAssetAmounts(in)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if len(encoded) == 0 {
		t.Fatal("encoded is empty")
	}
	if !strings.Contains(string(encoded), "1000000000000") {
		t.Errorf("encoded JSON missing first amount: %s", encoded)
	}

	out, err := decodeBlendAssetAmounts(string(encoded))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("decoded len=%d want 2: %+v", len(out), out)
	}
	if out[0].Amount != "1000000000000" {
		t.Errorf("out[0].Amount=%q want \"1000000000000\"", out[0].Amount)
	}
	if out[1].Asset != asset2.String() {
		t.Errorf("out[1].Asset=%q want %q", out[1].Asset, asset2.String())
	}
}

func TestEncodeBlendAssetAmounts_EmptyReturnsNil(t *testing.T) {
	// Empty slice → SQL NULL on insert. encodeBlendAssetAmounts
	// returns (nil, nil) for empty input rather than empty JSON
	// array, so the column ends up NULL not '[]'. Important for
	// delete-event rows where the column should genuinely be
	// absent.
	encoded, err := encodeBlendAssetAmounts(nil)
	if err != nil {
		t.Fatalf("encode nil: %v", err)
	}
	if encoded != nil {
		t.Errorf("encode nil → %q, want nil", encoded)
	}
	encoded, err = encodeBlendAssetAmounts([]blend.AssetAmount{})
	if err != nil {
		t.Fatalf("encode empty: %v", err)
	}
	if encoded != nil {
		t.Errorf("encode empty → %q, want nil", encoded)
	}
}

func TestDecodeBlendAssetAmounts_EmptyString(t *testing.T) {
	out, err := decodeBlendAssetAmounts("")
	if err != nil {
		t.Fatalf("decode empty: %v", err)
	}
	if out != nil {
		t.Errorf("decode \"\" → %+v, want nil", out)
	}
}

func TestDecodeBlendAssetAmounts_HugeI128(t *testing.T) {
	// Validate full i128 precision through the JSON boundary —
	// 39 digit number is the max-i128 ceiling order (170141183…).
	huge := "170141183460469231731687303715884105727" // 2^127 - 1
	js := `[{"asset":"CAS3J7GYLGXMF6TDJBBYYSE3HQ6BBSMLNUQ34T6TZMYMW2EVH34XOWMA","amount":"` + huge + `"}]`
	out, err := decodeBlendAssetAmounts(js)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 1 || out[0].Amount != huge {
		t.Errorf("out=%+v, want one row with amount=%q", out, huge)
	}
}

func TestDecodeBlendAssetAmounts_BadJSON(t *testing.T) {
	_, err := decodeBlendAssetAmounts("{not valid json")
	if err == nil {
		t.Fatal("expected error on invalid JSON")
	}
	if !strings.Contains(err.Error(), "unmarshal") {
		t.Errorf("err=%v missing 'unmarshal'", err)
	}
}
