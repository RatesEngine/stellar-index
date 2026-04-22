package canonical_test

import (
	"encoding/json"
	"errors"
	"testing"

	c "github.com/RatesEngine/rates-engine/internal/canonical"
)

const (
	// Real Circle USDC issuer on mainnet — useful as a round-trip fixture.
	usdcIssuer = "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"

	// Native XLM SAC contract ID from aquarius docs.
	xlmSAC = "CAS3J7GYLGXMF6TDJBBYYSE3HQ6BBSMLNUQ34T6TZMYMW2EVH34XOWMA"
)

func TestNativeAsset(t *testing.T) {
	a := c.NativeAsset()
	if a.Type != c.AssetNative {
		t.Fatalf("Type = %q, want native", a.Type)
	}
	if a.String() != "native" {
		t.Fatalf("String() = %q, want native", a.String())
	}
	if err := a.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
}

func TestNewClassicAsset_valid(t *testing.T) {
	a, err := c.NewClassicAsset("USDC", usdcIssuer)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.Type != c.AssetClassic {
		t.Fatalf("Type = %q", a.Type)
	}
	want := "USDC-" + usdcIssuer
	if a.String() != want {
		t.Fatalf("String() = %q, want %q", a.String(), want)
	}
}

func TestNewClassicAsset_errors(t *testing.T) {
	cases := []struct {
		name   string
		code   string
		issuer string
	}{
		{"empty code", "", usdcIssuer},
		{"too long code", "THIRTEEN_CHAR", usdcIssuer},
		{"bad issuer - too short", "USDC", "GSHORT"},
		{"bad issuer - wrong prefix", "USDC", "A" + usdcIssuer[1:]},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := c.NewClassicAsset(tc.code, tc.issuer)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

func TestNewSorobanAsset_valid(t *testing.T) {
	a, err := c.NewSorobanAsset(xlmSAC)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.Type != c.AssetSoroban {
		t.Fatalf("Type = %q", a.Type)
	}
	if a.String() != xlmSAC {
		t.Fatalf("String() = %q, want %q", a.String(), xlmSAC)
	}
}

func TestNewSorobanAsset_bad(t *testing.T) {
	_, err := c.NewSorobanAsset("GNOTACONTRACTID" + xlmSAC[1:])
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, c.ErrInvalidStrkey) {
		t.Fatalf("expected ErrInvalidStrkey, got %v", err)
	}
}

func TestParseAsset(t *testing.T) {
	cases := []struct {
		input   string
		want    c.Asset
		wantErr bool
	}{
		{"native", c.NativeAsset(), false},
		{"USDC-" + usdcIssuer, mustClassic("USDC", usdcIssuer), false},
		{"USDC:" + usdcIssuer, mustClassic("USDC", usdcIssuer), false}, // colon alias
		{xlmSAC, mustSoroban(xlmSAC), false},
		{"", c.Asset{}, true},
		{"garbage", c.Asset{}, true},
		{"USDC-", c.Asset{}, true},
		{"-" + usdcIssuer, c.Asset{}, true},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got, err := c.ParseAsset(tc.input)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tc.wantErr)
			}
			if err != nil {
				return
			}
			if !got.Equal(tc.want) {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestAsset_JSON_stringRoundTrip(t *testing.T) {
	for _, a := range []c.Asset{
		c.NativeAsset(),
		mustClassic("USDC", usdcIssuer),
		mustSoroban(xlmSAC),
	} {
		b, err := json.Marshal(a)
		if err != nil {
			t.Fatalf("Marshal(%v) = %v", a, err)
		}
		var got c.Asset
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatalf("Unmarshal(%s) = %v", b, err)
		}
		if !got.Equal(a) {
			t.Fatalf("round-trip: got %+v, want %+v (json %s)", got, a, b)
		}
		if got, want := string(b), `"`+a.String()+`"`; got != want {
			t.Fatalf("json = %s, want %s", got, want)
		}
	}
}

func TestAsset_JSON_objectForm(t *testing.T) {
	// Object form is accepted on unmarshal (for storage layers that keep
	// fields split) but we always emit string form.
	body := `{"type":"classic","code":"USDC","issuer":"` + usdcIssuer + `"}`
	var a c.Asset
	if err := json.Unmarshal([]byte(body), &a); err != nil {
		t.Fatalf("Unmarshal object form: %v", err)
	}
	if a.Code != "USDC" || a.Issuer != usdcIssuer {
		t.Fatalf("got %+v", a)
	}
}

func TestAsset_SQL_valueScan(t *testing.T) {
	a := mustClassic("USDC", usdcIssuer)
	v, err := a.Value()
	if err != nil {
		t.Fatal(err)
	}
	s, ok := v.(string)
	if !ok {
		t.Fatalf("Value returned %T, want string", v)
	}
	if s != a.String() {
		t.Fatalf("Value = %q, want %q", s, a.String())
	}

	// Scan from string
	var b c.Asset
	if err := b.Scan(s); err != nil {
		t.Fatal(err)
	}
	if !b.Equal(a) {
		t.Fatalf("scan: got %+v, want %+v", b, a)
	}

	// Scan from []byte
	var d c.Asset
	if err := d.Scan([]byte(s)); err != nil {
		t.Fatal(err)
	}
	if !d.Equal(a) {
		t.Fatalf("scan bytes: got %+v", d)
	}

	// Scan nil → zero
	var e c.Asset = a
	if err := e.Scan(nil); err != nil {
		t.Fatal(err)
	}
	if !e.IsZero() {
		t.Fatalf("scan nil: got %+v, want zero", e)
	}
}

func TestAsset_Validate_bad(t *testing.T) {
	cases := []c.Asset{
		{Type: c.AssetNative, Code: "USDC"},                 // native + code
		{Type: c.AssetClassic, Code: "USDC"},                 // classic no issuer
		{Type: c.AssetClassic, Code: "", Issuer: usdcIssuer}, // classic no code
		{Type: c.AssetSoroban, Code: "USDC"},                 // soroban + code
		{Type: "weird"},                                      // bad tag
	}
	for _, a := range cases {
		if err := a.Validate(); err == nil {
			t.Errorf("expected error for %+v", a)
		}
	}
}

// helpers

func mustClassic(code, issuer string) c.Asset {
	a, err := c.NewClassicAsset(code, issuer)
	if err != nil {
		panic(err)
	}
	return a
}

func mustSoroban(id string) c.Asset {
	a, err := c.NewSorobanAsset(id)
	if err != nil {
		panic(err)
	}
	return a
}
