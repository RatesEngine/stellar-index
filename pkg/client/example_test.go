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

// ExampleClient_HistorySinceInception demonstrates the historical
// time-series surface. Granularity is fixed per request; the API
// returns one bucket per granularity step from inception (or as
// far back as the trades hypertable goes for the asset) up to
// the most recent closed bucket.
func ExampleClient_HistorySinceInception() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": {
				"asset_id": "native",
				"quote": "fiat:USD",
				"granularity": "1d",
				"points": [
					{"t": "2026-04-01T00:00:00Z", "p": "0.13201"},
					{"t": "2026-04-02T00:00:00Z", "p": "0.13280"},
					{"t": "2026-04-03T00:00:00Z", "p": "0.13345"}
				]
			},
			"as_of": "2026-05-02T12:00:00Z",
			"flags": {"stale": false, "reduced_redundancy": false, "triangulated": false, "divergence_warning": false}
		}`))
	}))
	defer srv.Close()

	c := client.New(client.Options{BaseURL: srv.URL})
	resp, err := c.HistorySinceInception(context.Background(), client.HistoryQuery{
		Asset:       "native",
		Quote:       "fiat:USD",
		Granularity: "1d",
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Printf("XLM/USD daily points: %d (first %s)\n",
		len(resp.Data.Points), resp.Data.Points[0].T.Format("2006-01-02"))
	// Output: XLM/USD daily points: 3 (first 2026-04-01)
}

// ExampleClient_Assets demonstrates the paginated asset catalogue.
// Use Cursor/Limit for cursor-based pagination; the response's
// Pagination block carries `next_cursor` for the follow-up call.
func ExampleClient_Assets() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": [
				{"asset_id": "native", "type": "native", "decimals": 7},
				{"asset_id": "USDC-GA5...", "type": "classic", "code": "USDC", "decimals": 7}
			],
			"as_of": "2026-05-02T12:00:00Z",
			"flags": {"stale": false, "reduced_redundancy": false, "triangulated": false, "divergence_warning": false},
			"pagination": {"next": "eyJsYXN0IjoiVVNEQy1HQTUuLi4ifQ=="}
		}`))
	}))
	defer srv.Close()

	c := client.New(client.Options{BaseURL: srv.URL})
	resp, err := c.Assets(context.Background(), client.AssetsOptions{Limit: 2})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Printf("first page: %d assets, next=%t\n",
		len(resp.Data), resp.Pagination.Next != "")
	// Output: first page: 2 assets, next=true
}

// ExampleClient_Me demonstrates the authenticated-caller identity
// surface. Returns the API key's tier + remaining rate-limit
// budget so consumers can self-throttle without parsing
// `X-RateLimit-*` headers on every call.
func ExampleClient_Me() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": {
				"identifier": "GAVATAR...",
				"tier": "apikey",
				"key_id": "ak_demo_abc123",
				"label": "demo wallet",
				"rate_limit_per_min": 1000,
				"created_at": "2026-04-15T10:00:00Z"
			},
			"as_of": "2026-05-02T12:00:00Z",
			"flags": {"stale": false, "reduced_redundancy": false, "triangulated": false, "divergence_warning": false}
		}`))
	}))
	defer srv.Close()

	c := client.New(client.Options{BaseURL: srv.URL, APIKey: "ak_demo_abc123"})
	resp, err := c.Me(context.Background())
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Printf("tier=%s, budget=%d rpm, label=%q\n",
		resp.Data.Tier, resp.Data.RateLimitPerMin, resp.Data.Label)
	// Output: tier=apikey, budget=1000 rpm, label="demo wallet"
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
