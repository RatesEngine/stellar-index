package v1_test

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"testing"

	v1 "github.com/RatesEngine/rates-engine/internal/api/v1"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// stubSep1Cache implements v1.Sep1CachedReader in-memory. Mirrors
// `Store.GetIssuerSep1Cached` semantics: returns the cached payload
// for known issuers, (nil, nil) when the row exists but has no
// payload yet, (nil, sql.ErrNoRows) for unknown issuers, or a
// configured error to exercise the error branch.
type stubSep1Cache struct {
	byIssuer map[string]*timescale.IssuerSep1Cached
	err      error
}

func (s *stubSep1Cache) GetIssuerSep1Cached(_ context.Context, gStrkey string) (*timescale.IssuerSep1Cached, error) {
	if s.err != nil {
		return nil, s.err
	}
	if payload, ok := s.byIssuer[gStrkey]; ok {
		return payload, nil
	}
	return nil, sql.ErrNoRows
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
	sep1 := &stubSep1Cache{
		byIssuer: map[string]*timescale.IssuerSep1Cached{
			testUSDCIssuer: {
				OrgName: "Circle Internet Financial Limited",
				Currencies: []timescale.IssuerSep1Currency{{
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

	srv := v1.New(v1.Options{Assets: reader, Sep1Cache: sep1})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/assets/USDC-"+testUSDCIssuer)
	if resp.StatusCode != http.StatusOK {
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
	// F-1321: `decimals` is the on-chain unit scale (7 for classic) and
	// must NOT be overwritten by the issuer's display_decimals — doing so
	// inflated market-cap math by 10^5×. The rounding hint rides
	// display_decimals instead.
	if env.Data.Decimals != 7 {
		t.Errorf("decimals = %d, want 7 (on-chain scale, NOT display_decimals)", env.Data.Decimals)
	}
	if env.Data.DisplayDecimals == nil || *env.Data.DisplayDecimals != 2 {
		t.Errorf("display_decimals = %v, want 2", env.Data.DisplayDecimals)
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
		sep1 := &stubSep1Cache{
			byIssuer: map[string]*timescale.IssuerSep1Cached{
				testUSDCIssuer: {Currencies: []timescale.IssuerSep1Currency{{
					Code: "USDC", Issuer: testUSDCIssuer,
					Name: "X", Image: badImage,
				}}},
			},
		}
		srv := v1.New(v1.Options{Assets: reader, Sep1Cache: sep1})
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
	sep1 := &stubSep1Cache{
		byIssuer: map[string]*timescale.IssuerSep1Cached{
			testUSDCIssuer: {
				OrgName: "Someone Else",
				Currencies: []timescale.IssuerSep1Currency{{
					Code: "USDC", Issuer: "GSOMEONEELSEXXXXXXX", Name: "Fake",
				}},
			},
		},
	}

	srv := v1.New(v1.Options{Assets: reader, Sep1Cache: sep1})
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
		t.Errorf("org_name should be surfaced even on no_match: %+v", env.Data.OrgName)
	}
}

func TestAssetGet_Sep1OverlayRefusesNonClassicMatch(t *testing.T) {
	// Soroban / native / fiat assets must NOT match any SEP-1 currency
	// entry — the cached overlay short-circuits on non-classic types
	// before even hitting the lookup.
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
	sep1 := &stubSep1Cache{
		byIssuer: map[string]*timescale.IssuerSep1Cached{
			testUSDCIssuer: {
				OrgName: "Circle",
				Currencies: []timescale.IssuerSep1Currency{{
					Code: "USDC", Issuer: testUSDCIssuer, Name: "USD Coin",
				}},
			},
		},
	}

	srv := v1.New(v1.Options{Assets: reader, Sep1Cache: sep1})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/assets/"+sorobanContract)
	var env struct {
		Data v1.AssetDetail `json:"data"`
	}
	mustDecode(t, resp, &env)

	if env.Data.Sep1Status != "not_applicable" {
		t.Errorf("sep1_status = %q, want not_applicable (Soroban has no issuer to look up)", env.Data.Sep1Status)
	}
	if env.Data.Name != nil {
		t.Errorf("Soroban asset should NOT inherit USDC's Name: %v", env.Data.Name)
	}
}

func TestAssetGet_Sep1NotFetchedWhenIssuerNotInCache(t *testing.T) {
	// Issuer hasn't been visited by the sep1-refresh cron yet — handler
	// surfaces "not_fetched" without crashing or stalling.
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
	sep1 := &stubSep1Cache{err: errors.New("dns: nxdomain")}

	srv := v1.New(v1.Options{Assets: reader, Sep1Cache: sep1})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/assets/USDC-"+testUSDCIssuer)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (cache miss is not fatal)", resp.StatusCode)
	}
	var env struct {
		Data v1.AssetDetail `json:"data"`
	}
	mustDecode(t, resp, &env)
	if env.Data.Sep1Status != "not_fetched" {
		t.Errorf("sep1_status = %q, want not_fetched", env.Data.Sep1Status)
	}
}

func TestAssetGet_Sep1NotFetchedWhenCacheUnwired(t *testing.T) {
	// Reader provides HomeDomain but server has no Sep1Cache —
	// handler reports "not_fetched", same as the live-fetch era.
	issuer := testUSDCIssuer
	domain := "circle.com"
	reader := &stubAssetReader{
		byID: map[string]v1.AssetDetail{
			"USDC-" + testUSDCIssuer: {
				AssetID: "USDC-" + testUSDCIssuer, Type: "classic", Code: "USDC",
				Issuer: &issuer, HomeDomain: &domain, Decimals: 7,
			},
		},
	}

	srv := v1.New(v1.Options{Assets: reader}) // No Sep1Cache.
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
