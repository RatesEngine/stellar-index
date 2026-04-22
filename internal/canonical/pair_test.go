package canonical_test

import (
	"encoding/json"
	"testing"

	c "github.com/RatesEngine/rates-engine/internal/canonical"
)

func TestNewPair_validAndDirectional(t *testing.T) {
	xlm := c.NativeAsset()
	usdc := mustClassic("USDC", usdcIssuer)

	p, err := c.NewPair(xlm, usdc)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Base.Equal(xlm) || !p.Quote.Equal(usdc) {
		t.Fatalf("got %+v", p)
	}
	if p.String() != "native/USDC-"+usdcIssuer {
		t.Fatalf("String() = %q", p.String())
	}

	flipped := p.Flip()
	if !flipped.Base.Equal(usdc) || !flipped.Quote.Equal(xlm) {
		t.Fatalf("Flip wrong: %+v", flipped)
	}
	if p.Equal(flipped) {
		t.Fatal("directional pairs should not be equal to their flip")
	}
	if !p.EqualEitherWay(flipped) {
		t.Fatal("EqualEitherWay should accept flipped pair")
	}
}

func TestNewPair_sameAsset(t *testing.T) {
	xlm := c.NativeAsset()
	_, err := c.NewPair(xlm, xlm)
	if err == nil {
		t.Fatal("expected error for base == quote")
	}
}

func TestParsePair_roundTrip(t *testing.T) {
	xlm := c.NativeAsset()
	usdc := mustClassic("USDC", usdcIssuer)
	xlmSACasset := mustSoroban(xlmSAC)

	cases := []c.Pair{
		mustPair(xlm, usdc),
		mustPair(usdc, xlm),
		mustPair(xlmSACasset, usdc),
	}
	for _, p := range cases {
		t.Run(p.String(), func(t *testing.T) {
			got, err := c.ParsePair(p.String())
			if err != nil {
				t.Fatal(err)
			}
			if !got.Equal(p) {
				t.Fatalf("round-trip: got %+v, want %+v", got, p)
			}
		})
	}
}

func TestParsePair_bad(t *testing.T) {
	cases := []string{
		"",
		"/",
		"native/",
		"/native",
		"XLM/USD",           // neither side parses as an asset
		"native/native",     // same-asset
	}
	for _, s := range cases {
		t.Run(s, func(t *testing.T) {
			_, err := c.ParsePair(s)
			if err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestPair_JSON(t *testing.T) {
	p := mustPair(c.NativeAsset(), mustClassic("USDC", usdcIssuer))
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}

	// Object form on the wire: {"base":"native","quote":"USDC-G..."}
	var got c.Pair
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if !got.Equal(p) {
		t.Fatalf("round-trip: got %+v, want %+v (json %s)", got, p, b)
	}

	// String form on unmarshal also accepted
	str := []byte(`"native/USDC-` + usdcIssuer + `"`)
	var fromStr c.Pair
	if err := json.Unmarshal(str, &fromStr); err != nil {
		t.Fatalf("string-form unmarshal: %v", err)
	}
	if !fromStr.Equal(p) {
		t.Fatalf("string-form: got %+v, want %+v", fromStr, p)
	}
}

func mustPair(base, quote c.Asset) c.Pair {
	p, err := c.NewPair(base, quote)
	if err != nil {
		panic(err)
	}
	return p
}
