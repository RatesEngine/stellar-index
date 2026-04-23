package v1_test

import (
	"context"
	"errors"
	"testing"

	v1 "github.com/RatesEngine/rates-engine/internal/api/v1"
	"github.com/RatesEngine/rates-engine/internal/metadata"
)

// stubMetaResolver implements v1.MetadataResolver in-memory.
type stubMetaResolver struct {
	byDomain map[string]*metadata.SEP1
	err      error
}

func (r *stubMetaResolver) Resolve(_ context.Context, domain string) (*metadata.SEP1, error) {
	if r.err != nil {
		return nil, r.err
	}
	if sep, ok := r.byDomain[domain]; ok {
		return sep, nil
	}
	return nil, errors.New("no such domain")
}

func TestAssetGet_Sep1OverlayVerified(t *testing.T) {
	issuer := testUSDCIssuer
	domain := "circle.com"
	reader := &stubAssetReader{
		byID: map[string]v1.AssetDetail{
			"USDC-" + testUSDCIssuer: {
				AssetID:    "USDC-" + testUSDCIssuer,
				Type:       "classic",
				Code:       "USDC",
				Issuer:     &issuer,
				HomeDomain: &domain,
				Decimals:   7,
			},
		},
	}
	meta := &stubMetaResolver{
		byDomain: map[string]*metadata.SEP1{
			"circle.com": {
				OrgName: "Circle Internet Financial Limited",
				Currencies: []metadata.Currency{{
					Code:            "USDC",
					Issuer:          testUSDCIssuer,
					Name:            "USD Coin",
					Description:     "Dollar-denominated stablecoin",
					Image:           "https://circle.com/usdc-logo.svg",
					AnchorAsset:     "USD",
					AnchorAssetType: "fiat",
					Decimals:        7,
					DisplayDecimals: 2,
				}},
			},
		},
	}

	srv := v1.New(v1.Options{Assets: reader, Meta: meta})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/assets/USDC-"+testUSDCIssuer)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var env struct {
		Data v1.AssetDetail `json:"data"`
	}
	mustDecode(t, resp, &env)

	if env.Data.Sep1Status != "verified" {
		t.Errorf("sep1_status = %q, want verified", env.Data.Sep1Status)
	}
	if env.Data.OrgName == nil || *env.Data.OrgName != "Circle Internet Financial Limited" {
		t.Errorf("org_name not overlaid: %+v", env.Data.OrgName)
	}
	if env.Data.Name == nil || *env.Data.Name != "USD Coin" {
		t.Errorf("name not overlaid: %+v", env.Data.Name)
	}
	if env.Data.AnchorAsset == nil || *env.Data.AnchorAsset != "USD" {
		t.Errorf("anchor_asset not overlaid: %+v", env.Data.AnchorAsset)
	}
	if env.Data.Decimals != 2 {
		// display_decimals preferred over canonical default.
		t.Errorf("decimals = %d, want 2 (display_decimals)", env.Data.Decimals)
	}
}

func TestAssetGet_Sep1OverlayRejectsHostileImageURL(t *testing.T) {
	// An issuer's stellar.toml is attacker-controlled ground for any
	// asset on its domain. A hostile image like "javascript:alert(1)"
	// could be served back to browser-based API consumers. Verify
	// non-http(s) schemes are dropped, not propagated.
	issuer := testUSDCIssuer
	domain := "evil.example.com"
	reader := &stubAssetReader{
		byID: map[string]v1.AssetDetail{
			"USDC-" + testUSDCIssuer: {
				AssetID: "USDC-" + testUSDCIssuer, Type: "classic",
				Code: "USDC", Issuer: &issuer, HomeDomain: &domain,
				Decimals: 7,
			},
		},
	}

	for _, badImage := range []string{
		"javascript:alert(1)",
		"data:text/html,<script>alert(1)</script>",
		"file:///etc/passwd",
		"blob:abc",
		"//protocol-relative.example.com/x.png",
		"   not a url   ",
	} {
		meta := &stubMetaResolver{
			byDomain: map[string]*metadata.SEP1{
				domain: {Currencies: []metadata.Currency{{
					Code: "USDC", Issuer: testUSDCIssuer,
					Name:  "X", Image: badImage,
				}}},
			},
		}
		srv := v1.New(v1.Options{Assets: reader, Meta: meta})
		ts := httpTestServer(t, srv)

		resp := mustGet(t, ts.URL+"/v1/assets/USDC-"+testUSDCIssuer)
		var env struct {
			Data v1.AssetDetail `json:"data"`
		}
		mustDecode(t, resp, &env)
		if env.Data.Image != nil {
			t.Errorf("image = %q; hostile URL %q should have been dropped",
				*env.Data.Image, badImage)
		}
		// Other overlay fields should still land — the guard is
		// image-specific, not a full overlay bail-out.
		if env.Data.Name == nil {
			t.Errorf("name dropped alongside hostile image %q — guard should be image-only", badImage)
		}
	}
}

func TestAssetGet_Sep1OverlayNoMatch(t *testing.T) {
	// SEP-1 loads, but the currency under a different issuer.
	issuer := testUSDCIssuer
	domain := "example.com"
	reader := &stubAssetReader{
		byID: map[string]v1.AssetDetail{
			"USDC-" + testUSDCIssuer: {
				AssetID: "USDC-" + testUSDCIssuer, Type: "classic", Code: "USDC",
				Issuer: &issuer, HomeDomain: &domain, Decimals: 7,
			},
		},
	}
	meta := &stubMetaResolver{
		byDomain: map[string]*metadata.SEP1{
			"example.com": {
				OrgName: "Someone Else",
				Currencies: []metadata.Currency{{
					Code: "USDC", Issuer: "GSOMEONEELSEXXXXXXX", Name: "Fake",
				}},
			},
		},
	}

	srv := v1.New(v1.Options{Assets: reader, Meta: meta})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/assets/USDC-"+testUSDCIssuer)
	var env struct {
		Data v1.AssetDetail `json:"data"`
	}
	mustDecode(t, resp, &env)

	if env.Data.Sep1Status != "no_match" {
		t.Errorf("sep1_status = %q, want no_match", env.Data.Sep1Status)
	}
	if env.Data.Name != nil {
		t.Errorf("should NOT overlay currency fields on issuer mismatch")
	}
	if env.Data.OrgName == nil || *env.Data.OrgName != "Someone Else" {
		// OrgName still surfaces — the domain resolved, we just can't
		// vouch for the specific issuer.
		t.Errorf("org_name should be surfaced even on no_match: %+v", env.Data.OrgName)
	}
}

func TestAssetGet_Sep1OverlayRefusesNonClassicMatch(t *testing.T) {
	// Regression: Soroban / native / fiat assets must NOT match any
	// SEP-1 currency entry by accident. Previously findMatchingCurrency
	// fell through the (empty code, empty issuer) checks and returned
	// the FIRST entry — silently attaching random USDC metadata to
	// any Soroban contract with the same home-domain.
	domain := "circle.com"
	sorobanContract := "CAS3J7GYLGXMF6TDJBBYYSE3HQ6BBSMLNUQ34T6TZMYMW2EVH34XOWMA"
	reader := &stubAssetReader{
		byID: map[string]v1.AssetDetail{
			sorobanContract: {
				AssetID:    sorobanContract,
				Type:       "soroban",
				ContractID: &sorobanContract,
				HomeDomain: &domain,
				Decimals:   7,
			},
		},
	}
	meta := &stubMetaResolver{
		byDomain: map[string]*metadata.SEP1{
			"circle.com": {
				OrgName: "Circle",
				Currencies: []metadata.Currency{{
					Code: "USDC", Issuer: testUSDCIssuer, Name: "USD Coin",
				}},
			},
		},
	}

	srv := v1.New(v1.Options{Assets: reader, Meta: meta})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/assets/"+sorobanContract)
	var env struct {
		Data v1.AssetDetail `json:"data"`
	}
	mustDecode(t, resp, &env)

	if env.Data.Sep1Status != "no_match" {
		t.Errorf("sep1_status = %q, want no_match (Soroban can't match classic entries)", env.Data.Sep1Status)
	}
	if env.Data.Name != nil {
		t.Errorf("Soroban asset should NOT inherit USDC's Name: %v", env.Data.Name)
	}
}

func TestAssetGet_Sep1OverlayUnreachable(t *testing.T) {
	issuer := testUSDCIssuer
	domain := "offline.example"
	reader := &stubAssetReader{
		byID: map[string]v1.AssetDetail{
			"USDC-" + testUSDCIssuer: {
				AssetID: "USDC-" + testUSDCIssuer, Type: "classic", Code: "USDC",
				Issuer: &issuer, HomeDomain: &domain, Decimals: 7,
			},
		},
	}
	meta := &stubMetaResolver{err: errors.New("dns: nxdomain")}

	srv := v1.New(v1.Options{Assets: reader, Meta: meta})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/assets/USDC-"+testUSDCIssuer)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200 (unreachable is not fatal)", resp.StatusCode)
	}
	var env struct {
		Data v1.AssetDetail `json:"data"`
	}
	mustDecode(t, resp, &env)
	if env.Data.Sep1Status != "unreachable" {
		t.Errorf("sep1_status = %q, want unreachable", env.Data.Sep1Status)
	}
}

func TestAssetGet_Sep1NotFetchedWhenMetaUnwired(t *testing.T) {
	// Reader provides HomeDomain but server has no Meta resolver —
	// sep1_status should say so rather than lying with "verified"
	// or "not_applicable".
	issuer := testUSDCIssuer
	domain := "circle.com"
	reader := &stubAssetReader{
		byID: map[string]v1.AssetDetail{
			"USDC-" + testUSDCIssuer: {
				AssetID: "USDC-" + testUSDCIssuer, Type: "classic", Code: "USDC",
				Issuer: &issuer, HomeDomain: &domain, Decimals: 7,
				// Sep1Status left blank — handler should populate.
			},
		},
	}

	srv := v1.New(v1.Options{Assets: reader}) // No Meta.
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/assets/USDC-"+testUSDCIssuer)
	var env struct {
		Data v1.AssetDetail `json:"data"`
	}
	mustDecode(t, resp, &env)
	if env.Data.Sep1Status != "not_fetched" {
		t.Errorf("sep1_status = %q, want not_fetched", env.Data.Sep1Status)
	}
}
