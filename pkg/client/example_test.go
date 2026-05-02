package client_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/RatesEngine/rates-engine/pkg/client"
)

// ExampleNew demonstrates the canonical SDK construction. The
// Options shape is intentionally small — anonymous use needs only
// a BaseURL; authenticated use adds an APIKey.
func ExampleNew() {
	// Construct against the public production endpoint:
	c := client.New(client.Options{
		BaseURL: "https://api.ratesengine.net",
		APIKey:  "rek_…", // optional; anonymous works at low rate limit
	})
	_ = c // silence "declared and not used" in this snippet

	// For self-hosted or staging, point BaseURL at the deployment:
	staging := client.New(client.Options{BaseURL: "https://api.staging.ratesengine.net"})
	_ = staging
}

// ExampleClient_Price demonstrates a current-price lookup. The
// returned Envelope carries the price plus advisory flags; the
// caller decides whether to act on `flags.stale` /
// `flags.divergence_warning` / `flags.frozen`.
func ExampleClient_Price() {
	// Stand up a fake server returning a representative response so
	// the example is self-contained + verified at build time.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": {
				"asset_id": "native",
				"quote": "fiat:USD",
				"price": "0.13245",
				"price_type": "vwap",
				"observed_at": "2026-05-02T12:00:00Z",
				"window_seconds": 60
			},
			"as_of": "2026-05-02T12:00:00Z",
			"sources": ["binance", "kraken"],
			"flags": {
				"stale": false,
				"reduced_redundancy": false,
				"triangulated": false,
				"divergence_warning": false
			}
		}`))
	}))
	defer srv.Close()

	c := client.New(client.Options{BaseURL: srv.URL})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	resp, err := c.Price(ctx, client.PriceQuery{
		Asset: "native",
		Quote: "fiat:USD",
	})
	if err != nil {
		// Production handlers should distinguish APIError shape
		// (status-typed) from network errors.
		var apiErr *client.APIError
		if errors.As(err, &apiErr) {
			fmt.Printf("api error %d: %s\n", apiErr.Status, apiErr.Title)
			return
		}
		fmt.Println("request failed:", err)
		return
	}

	fmt.Printf("XLM/USD = %s (sources: %v, stale: %v)\n",
		resp.Data.Price, resp.Sources, resp.Flags.Stale)

	// Output: XLM/USD = 0.13245 (sources: [binance kraken], stale: false)
}

// ExampleClient_Asset demonstrates fetching the rich asset detail
// surface — everything wallet UIs need for the Freighter V2
// asset-detail page (decimals, SEP-1 overlay, F2 supply fields,
// and SEP-1 issuance declarations).
func ExampleClient_Asset() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": {
				"asset_id": "USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN",
				"type": "classic",
				"code": "USDC",
				"issuer": "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN",
				"home_domain": "centre.io",
				"decimals": 7,
				"sep1_status": "verified",
				"name": "USD Coin",
				"description": "Centre-issued USDC stablecoin",
				"image": "https://centre.io/assets/usdc.svg",
				"org_name": "Centre Consortium",
				"anchor_asset": "USD",
				"anchor_asset_type": "fiat",
				"circulating_supply": "12345678900000000",
				"total_supply": "12345678900000000",
				"market_cap_usd": "1234567890.00",
				"supply_basis": "issuer_exclusion",
				"volume_24h_usd": "987654.32",
				"is_unlimited": false
			},
			"as_of": "2026-05-02T12:00:00Z",
			"flags": {
				"stale": false,
				"reduced_redundancy": false,
				"triangulated": false,
				"divergence_warning": false
			}
		}`))
	}))
	defer srv.Close()

	c := client.New(client.Options{BaseURL: srv.URL})
	resp, err := c.Asset(context.Background(), "USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")
	if err != nil {
		fmt.Println(err)
		return
	}

	asset := resp.Data
	fmt.Printf("%s (%s) — sep1=%s, circulating=%s, market_cap=$%s\n",
		asset.Code, *asset.Name, asset.Sep1Status,
		*asset.CirculatingSupply, *asset.MarketCapUSD)

	// Output: USDC (USD Coin) — sep1=verified, circulating=12345678900000000, market_cap=$1234567890.00
}

// ExampleAPIError demonstrates the status-typed error helpers the
// SDK exposes — wallet integrators handle 404 / 429 / 5xx with
// explicit predicates rather than parsing error strings.
func ExampleAPIError() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{
			"type": "https://api.ratesengine.net/errors/asset-not-found",
			"title": "Asset not found",
			"status": 404,
			"detail": "No trades observed for asset_id=XYZ-G..."
		}`))
	}))
	defer srv.Close()

	c := client.New(client.Options{BaseURL: srv.URL})
	_, err := c.Asset(context.Background(), "XYZ-GUNKNOWNISSUER")

	var apiErr *client.APIError
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.IsNotFound():
			fmt.Println("asset is not indexed")
		case apiErr.IsRateLimited():
			fmt.Println("back off — server rate-limited us")
		case apiErr.IsServerError():
			fmt.Println("server-side issue; consider retry with backoff")
		default:
			fmt.Println("client-side error:", apiErr.Title)
		}
	}

	// Output: asset is not indexed
}
