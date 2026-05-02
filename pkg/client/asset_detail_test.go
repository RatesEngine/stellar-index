package client_test

import (
	"encoding/json"
	"testing"

	"github.com/RatesEngine/rates-engine/pkg/client"
)

// TestAssetDetail_DecodesFullWirePayload pins the SDK's [AssetDetail]
// against the full wire shape the v1 API returns on
// `/v1/assets/{id}`. Adding a field to the API should be additive
// (non-breaking under SemVer) and visible to SDK consumers without
// dropping to raw HTTP — this test fails if a documented JSON key
// stops landing on the struct.
func TestAssetDetail_DecodesFullWirePayload(t *testing.T) {
	t.Parallel()
	const body = `{
	  "asset_id": "USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN",
	  "type": "classic",
	  "code": "USDC",
	  "issuer": "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN",
	  "home_domain": "centre.io",
	  "decimals": 7,
	  "sep1_status": "verified",
	  "is_experimental": false,
	  "name": "USD Coin",
	  "description": "USD-pegged stablecoin issued by Circle.",
	  "image": "https://centre.io/assets/usdc.svg",
	  "org_name": "Centre Consortium",
	  "anchor_asset": "USD",
	  "anchor_asset_type": "fiat",
	  "circulating_supply": "1234567890000000",
	  "total_supply": "1234567890000000",
	  "max_supply": null,
	  "market_cap_usd": "1234567890.00",
	  "fdv_usd": null,
	  "supply_basis": "issuer_exclusion",
	  "volume_24h_usd": "987654.32"
	}`

	var got client.AssetDetail
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.AssetID == "" || got.Type != "classic" || got.Code != "USDC" {
		t.Fatalf("identity fields missing: %+v", got)
	}
	if got.Decimals != 7 || got.Sep1Status != "verified" {
		t.Errorf("decimals/sep1_status: got %d / %q, want 7 / verified", got.Decimals, got.Sep1Status)
	}
	if got.HomeDomain == nil || *got.HomeDomain != "centre.io" {
		t.Errorf("home_domain not decoded: %+v", got.HomeDomain)
	}
	if got.Name == nil || *got.Name != "USD Coin" {
		t.Errorf("name not decoded: %+v", got.Name)
	}
	if got.AnchorAsset == nil || *got.AnchorAsset != "USD" {
		t.Errorf("anchor_asset not decoded: %+v", got.AnchorAsset)
	}
	if got.CirculatingSupply == nil || *got.CirculatingSupply != "1234567890000000" {
		t.Errorf("circulating_supply not decoded: %+v", got.CirculatingSupply)
	}
	if got.TotalSupply == nil || *got.TotalSupply != "1234567890000000" {
		t.Errorf("total_supply not decoded: %+v", got.TotalSupply)
	}
	if got.MaxSupply != nil {
		t.Errorf("max_supply should be nil for null wire value, got %+v", got.MaxSupply)
	}
	if got.MarketCapUSD == nil || *got.MarketCapUSD != "1234567890.00" {
		t.Errorf("market_cap_usd not decoded: %+v", got.MarketCapUSD)
	}
	if got.FDVUSD != nil {
		t.Errorf("fdv_usd should be nil for null wire value, got %+v", got.FDVUSD)
	}
	if got.SupplyBasis == nil || *got.SupplyBasis != "issuer_exclusion" {
		t.Errorf("supply_basis not decoded: %+v", got.SupplyBasis)
	}
	if got.VolumeUSD24h == nil || *got.VolumeUSD24h != "987654.32" {
		t.Errorf("volume_24h_usd not decoded: %+v", got.VolumeUSD24h)
	}
}

// TestAssetDetail_OmitsNullsOnReencode verifies the SDK round-trips
// nil-pointer F2 / overlay fields back to omitted JSON keys (so a
// consumer that reads + re-emits doesn't accidentally publish
// `"max_supply": null` when the server didn't.
func TestAssetDetail_OmitsNullsOnReencode(t *testing.T) {
	t.Parallel()
	in := client.AssetDetail{
		AssetID:    "native",
		Type:       "native",
		Decimals:   7,
		Sep1Status: "not_applicable",
	}
	out, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got := string(out)
	for _, missing := range []string{"max_supply", "fdv_usd", "circulating_supply", "volume_24h_usd", "market_cap_usd", "name", "image"} {
		if containsKey(got, missing) {
			t.Errorf("encoded form contains %q for nil pointer; want omitted: %s", missing, got)
		}
	}
	for _, present := range []string{"asset_id", "type", "decimals", "sep1_status"} {
		if !containsKey(got, present) {
			t.Errorf("encoded form missing required key %q: %s", present, got)
		}
	}
}

// containsKey is a cheap substring check for `"<name>":` to avoid
// pulling in a JSON parser for the test.
func containsKey(s, name string) bool {
	needle := `"` + name + `":`
	for i := 0; i+len(needle) <= len(s); i++ {
		if s[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
