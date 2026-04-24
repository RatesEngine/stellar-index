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
		// Non-alphanumeric codes must be rejected — these pass the
		// length check but aren't valid per Stellar's CREDIT_ALPHANUM
		// XDR definitions.
		{"code with emoji", "💩", usdcIssuer},
		{"code with space", "US D", usdcIssuer},
		{"code with hyphen", "USD-C", usdcIssuer},
		{"code with colon", "USD:C", usdcIssuer},
		{"code with null byte", "USD\x00", usdcIssuer},
		{"code with non-ASCII", "USDé", usdcIssuer},
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
		mustFiat("USD"),
		mustFiat("EUR"),
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

func TestAsset_JSON_rejectsNonStringNonObject(t *testing.T) {
	// UnmarshalJSON's two-stage fallback (try string, then object)
	// must reject numbers / bools / arrays — otherwise something like
	// `true` would fall through to the object path and happen to
	// decode as a zero-value Asset whose Validate() fails. Confirm we
	// get a real error, not a silent zero value.
	for name, body := range map[string]string{
		"number":       `123`,
		"bool true":    `true`,
		"bool false":   `false`,
		"array":        `["native"]`,
		"empty string": `""`,
		"empty object": `{}`,
	} {
		t.Run(name, func(t *testing.T) {
			var a c.Asset
			err := json.Unmarshal([]byte(body), &a)
			if err == nil {
				t.Errorf("expected error for %s input %q, got %+v", name, body, a)
			}
		})
	}
}

func TestAsset_JSON_rejectsNull(t *testing.T) {
	// JSON null parses the empty string via the `try string first`
	// branch, which then fails ParseAsset("") → empty-asset error.
	// Confirm the error path is taken rather than silently producing
	// a zero-value Asset.
	var a c.Asset
	if err := json.Unmarshal([]byte(`null`), &a); err == nil {
		t.Errorf("expected error for null input, got %+v", a)
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
	e := a
	if err := e.Scan(nil); err != nil {
		t.Fatal(err)
	}
	if !e.IsZero() {
		t.Fatalf("scan nil: got %+v, want zero", e)
	}
}

func TestAsset_Validate_bad(t *testing.T) {
	cases := []c.Asset{
		{Type: c.AssetNative, Code: "USDC"},                  // native + code
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

func TestAsset_Equal_identityAcrossVariants(t *testing.T) {
	// Reflexivity + cross-variant inequality. Pair.Validate rejects
	// a base==quote pair via this method, so a subtle regression
	// here (e.g. "native equals native returns false") would make
	// Pair.Validate accept native/native and break invariants way
	// downstream. Cheap guard.
	native := c.NativeAsset()
	if !native.Equal(native) {
		t.Error("NativeAsset().Equal(NativeAsset()) must be true")
	}
	usd := mustFiat("USD")
	if !usd.Equal(usd) {
		t.Error("fiat USD must equal itself")
	}
	// A classic asset and its would-be SAC wrap compare NOT equal
	// (documented — ADR-0010 promotes them as distinct representations).
	usdc := mustClassic("USDC", usdcIssuer)
	sac := mustSoroban(xlmSAC)
	if usdc.Equal(sac) {
		t.Error("classic and soroban variants must compare unequal")
	}
	// Native vs fiat USD: different types, not equal.
	if native.Equal(usd) {
		t.Error("native and fiat-USD must compare unequal")
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

func mustFiat(code string) c.Asset {
	a, err := c.NewFiatAsset(code)
	if err != nil {
		panic(err)
	}
	return a
}
