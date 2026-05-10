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

// ExampleClient_Status shows how to consume the customer-facing
// system-health rollup. /v1/status always returns 200; degraded
// state lives in the body's Overall field, so dashboards can poll
// it without alerting on 503s.
func ExampleClient_Status() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": {
				"overall": "ok",
				"region": {"name": "r1", "deployment": "production"},
				"services": [
					{"name":"api","status":"ok"},
					{"name":"indexer","status":"ok"},
					{"name":"aggregator","status":"ok"}
				],
				"latency": {"p50_ms": 0.6, "p95_ms": 3.85, "p99_ms": 4.77, "window_secs": 300},
				"freshness": {"active_sources": 13, "total_sources": 17},
				"incidents": {"active_count": 0}
			},
			"as_of": "2026-05-05T15:00:00Z",
			"flags": {"stale": false}
		}`))
	}))
	defer srv.Close()

	c := client.New(client.Options{BaseURL: srv.URL})
	got, err := c.Status(context.Background())
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Printf("%s — p95=%.2fms, %d/%d sources active\n",
		got.Data.Overall,
		got.Data.Latency.P95Ms,
		got.Data.Freshness.ActiveSources,
		got.Data.Freshness.TotalSources)

	// Output: ok — p95=3.85ms, 13/17 sources active
}

// ExampleClient_NetworkStats demonstrates fetching the home-page
// aggregate snapshot — total 24h USD volume, active markets count,
// indexed-assets count, latest live ledger, and source-count
// summary. One round trip replaces fan-out across /v1/coins,
// /v1/markets, /v1/sources, /v1/diagnostics/cursors.
//
// `Volume24hUSD` is `*string` per ADR-0003 (raw cents can exceed
// int64). nil means the rolling 24h window had no USD-equivalent
// trades — render `—` rather than fabricating a zero.
//
// Note: `total_sources` here counts entries in the static binary
// registry. /v1/status's `freshness.total_sources` counts sources
// the operator has ENABLED at runtime — typically a strict subset.
// See [docs/reference/api](https://docs.ratesengine.net) for the
// full semantic table.
func ExampleClient_NetworkStats() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": {
				"volume_24h_usd": "3176584845.46668496",
				"markets_count_24h": 23848,
				"assets_indexed": 86175,
				"latest_ledger": 62490950,
				"exchange_sources": 11,
				"total_sources": 21
			},
			"as_of": "2026-05-09T15:09:00Z",
			"flags": {"stale": false}
		}`))
	}))
	defer srv.Close()

	c := client.New(client.Options{BaseURL: srv.URL})
	got, err := c.NetworkStats(context.Background())
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	vol := "—"
	if got.Data.Volume24hUSD != nil {
		vol = *got.Data.Volume24hUSD
	}
	fmt.Printf("ledger=%d, markets=%d, assets=%d, exchange/total=%d/%d, vol24h_usd=%s\n",
		got.Data.LatestLedger,
		got.Data.MarketsCount24h,
		got.Data.AssetsIndexed,
		got.Data.ExchangeSources,
		got.Data.TotalSources,
		vol)

	// Output: ledger=62490950, markets=23848, assets=86175, exchange/total=11/21, vol24h_usd=3176584845.46668496
}

// ExampleClient_Currencies demonstrates the unified
// fiat / fiat-like currency listing — backs the explorer's
// /currencies page. RateUSD is "1 USD = N units of this currency"
// per the server contract, so for currencies stronger than USD
// (EUR, GBP) RateUSD < 1, and weaker (JPY, INR) RateUSD > 1. The
// `*float64` pointer fields preserve the "no data" vs "0"
// distinction on circulating-supply / market-cap, important for
// currencies the operator hasn't wired a circulation source for
// (about 70 of ~120 fiats today).
func ExampleClient_Currencies() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": {
				"currencies": [
					{
						"ticker": "EUR",
						"name": "Euro",
						"rate_usd": 0.8483,
						"updated_at": "2026-05-08T00:00:00Z",
						"circulating_supply": 15800000000000,
						"market_cap_usd": 18625486266650.95
					},
					{
						"ticker": "JPY",
						"name": "Japanese Yen",
						"rate_usd": 152.34,
						"updated_at": "2026-05-08T00:00:00Z"
					}
				],
				"published_at": "2026-05-08T00:00:00Z",
				"fetched_at":   "2026-05-08T01:00:00Z",
				"source":       "massive"
			},
			"as_of": "2026-05-09T10:00:00Z",
			"flags": {}
		}`))
	}))
	defer srv.Close()

	c := client.New(client.Options{BaseURL: srv.URL})
	got, err := c.Currencies(context.Background(), client.CurrenciesOptions{Limit: 50})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	for _, cur := range got.Data.Currencies {
		mcap := "—"
		if cur.MarketCapUSD != nil {
			mcap = fmt.Sprintf("$%.0fB", *cur.MarketCapUSD/1e9)
		}
		fmt.Printf("%s (%s): 1 USD = %.4f, market_cap=%s\n",
			cur.Ticker, cur.Name, cur.RateUSD, mcap)
	}

	// Output:
	// EUR (Euro): 1 USD = 0.8483, market_cap=$18625B
	// JPY (Japanese Yen): 1 USD = 152.3400, market_cap=—
}

// ExampleClient_Incidents demonstrates fetching the
// incident-postmortem corpus surfaced on status.ratesengine.net.
// Each entry has a SEV-N severity tier, a lifecycle status, and
// the full markdown body. ResolvedAt is `*time.Time` (nil for
// still-active incidents) so callers can distinguish "ongoing"
// from "resolved at zero time".
//
// The corpus ships with the binary (per-deploy), so two regions
// running different builds may return different lists — clients
// that need cross-region consistency should diff by Slug.
func ExampleClient_Incidents() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": {
				"incidents": [
					{
						"slug": "2026-05-06-postgres-lock-table-full",
						"title": "[SEV-3] Indexer dropping ~1% of trades",
						"severity": "SEV-3",
						"status": "resolved",
						"started_at": "2026-05-06T15:00:00Z",
						"resolved_at": "2026-05-06T22:39:00Z",
						"affected_components": ["indexer"],
						"body_markdown": "..."
					}
				],
				"count": 1
			},
			"as_of": "2026-05-09T15:00:00Z",
			"flags": {}
		}`))
	}))
	defer srv.Close()

	c := client.New(client.Options{BaseURL: srv.URL})
	got, err := c.Incidents(context.Background())
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	for _, inc := range got.Data.Incidents {
		state := "ongoing"
		if inc.ResolvedAt != nil {
			state = "resolved " + inc.ResolvedAt.Format("2006-01-02 15:04Z")
		}
		fmt.Printf("[%s/%s] %s — %s\n", inc.Severity, inc.Status, inc.Slug, state)
	}

	// Output: [SEV-3/resolved] 2026-05-06-postgres-lock-table-full — resolved 2026-05-06 22:39Z
}

// ExampleClient_LendingPools demonstrates fetching every Blend
// pool contract observed in the trailing 7d auction stream.
// Sorted by total auction count desc — first row is the most-
// active pool. AuctionsTotal is lifetime; Auctions24h is the
// recent activity slice; UniqueUsers30d is distinct addresses
// that landed an auction in the last 30 days.
//
// Field stability: the wire shape is auction-derived today;
// per-pool TVL, utilisation, and supply/borrow APYs land via
// additional fields once the pool-storage reader worker ships.
// JSON decode ignores unknown fields, so adding new fields is
// non-breaking — but callers should NOT switch on field count.
func ExampleClient_LendingPools() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": [
				{
					"protocol": "blend",
					"pool": "CAJJZSGMMM3PD7N33TAPHGBUGTB43OC73HVIK2L2G6BNGGGYOSSYBXBD",
					"auctions_24h": 29,
					"auctions_total": 5691,
					"unique_users_30d": 4,
					"last_seen": "2026-05-09T13:34:58Z"
				}
			],
			"as_of": "2026-05-09T15:00:00Z",
			"flags": {}
		}`))
	}))
	defer srv.Close()

	c := client.New(client.Options{BaseURL: srv.URL})
	got, err := c.LendingPools(context.Background())
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	for _, p := range got.Data {
		// Truncate the contract ID for display — full C-strkeys are
		// 56 chars and don't render well in tables.
		short := p.Pool
		if len(short) > 12 {
			short = short[:6] + "…" + short[len(short)-4:]
		}
		fmt.Printf("%s/%s: %d auctions/24h, %d total, %d users/30d\n",
			p.Protocol, short, p.Auctions24h, p.AuctionsTotal, p.UniqueUsers30d)
	}

	// Output: blend/CAJJZS…BXBD: 29 auctions/24h, 5691 total, 4 users/30d
}

// ExampleClient_SACWrappers demonstrates the SAC (Stellar Asset
// Contract) wrapper registry — a map from Soroban contract address
// to the "<CODE>:<G_STRKEY>" form of the underlying classic asset.
// Used to resolve `transfer` events on SAC contracts back to the
// classic asset they wrap.
//
// Returned as a plain Go map; iterate keys for contracts or look
// up a specific contract directly. The map is operator-config
// (loaded from /etc/ratesengine.toml at boot), so it's small —
// typically ~20 entries — and stable across regions running the
// same config.
func ExampleClient_SACWrappers() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": {
				"CAS3J7GYLGXMF6TDJBBYYSE3HQ6BBSMLNUQ34T6TZMYMW2EVH34XOWMA": "native",
				"CCW67TSZV3SSS2HXMBQ5JFGCKJNXKZM7UQUWUZPUTHXSTZLEO7SJMI75": "USDC:GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"
			},
			"as_of": "2026-05-09T15:00:00Z",
			"flags": {}
		}`))
	}))
	defer srv.Close()

	c := client.New(client.Options{BaseURL: srv.URL})
	got, err := c.SACWrappers(context.Background())
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	// Look up a specific Soroban contract — useful when a swap
	// event's base/quote came in as a C-strkey and the explorer
	// wants to render the underlying asset symbol.
	const xlmSAC = "CAS3J7GYLGXMF6TDJBBYYSE3HQ6BBSMLNUQ34T6TZMYMW2EVH34XOWMA"
	if asset, ok := got.Data[xlmSAC]; ok {
		fmt.Printf("XLM SAC (%s…) wraps: %s\n", xlmSAC[:6], asset)
	}

	// Output: XLM SAC (CAS3J7…) wraps: native
}

// ExampleClient_Chart demonstrates the binned price + USD-volume
// time series. Distinct from [Client.HistorySinceInception] (which
// returns the FULL series at one granularity) — Chart trims to a
// caller-chosen window and resolves a server-default granularity
// per timeframe (24h → 1h bins, 7d → 4h, 30d → 1d, etc.).
//
// Use Chart for "render this asset's last N hours/days"; use
// HistorySinceInception for full historical exports / analytical
// work that needs every bucket.
func ExampleClient_Chart() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": {
				"asset_id": "native",
				"quote": "fiat:USD",
				"granularity": "1h",
				"timeframe": "24h",
				"price_type": "vwap",
				"points": [
					{"t": "2026-05-08T22:00:00Z", "p": "0.158", "v_usd": "20808.05"},
					{"t": "2026-05-08T23:00:00Z", "p": "0.161", "v_usd": "31204.18"}
				]
			},
			"as_of": "2026-05-08T23:00:00Z",
			"flags": {}
		}`))
	}))
	defer srv.Close()

	c := client.New(client.Options{BaseURL: srv.URL})
	got, err := c.Chart(context.Background(), client.ChartQuery{
		Asset: "native", Timeframe: "24h",
	})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Printf("%s/%s @ %s, %d points\n",
		got.Data.AssetID, got.Data.Quote, got.Data.Granularity, len(got.Data.Points))
	for _, p := range got.Data.Points {
		fmt.Printf("  %s: %s\n", p.T.Format("15:04Z"), p.P)
	}

	// Output:
	// native/fiat:USD @ 1h, 2 points
	//   22:00Z: 0.158
	//   23:00Z: 0.161
}

// ExampleClient_Observations demonstrates the rawest per-source
// trade view per ADR-0018 — one row per source that has recorded
// a trade on (asset, quote). Use this when you want to see
// "where is this rate coming from?" without the aggregator layer.
//
// Empty array (NOT 404) when the pair has no observations.
// flags.stale is always false on this surface — there's no
// aggregation contract to fall short of.
//
// Aggregate="latest" collapses the per-source array to a 0/1-
// element slice of the most-recent observation across sources.
func ExampleClient_Observations() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": [
				{
					"source": "binance",
					"ledger": 0,
					"tx_hash": "",
					"ts": "2026-05-09T15:00:00Z",
					"base_asset": "native",
					"quote_asset": "fiat:USD",
					"base_amount": "100",
					"quote_amount": "16.0",
					"price": "0.16"
				},
				{
					"source": "sdex",
					"ledger": 62490950,
					"tx_hash": "abc",
					"ts": "2026-05-09T14:59:50Z",
					"base_asset": "native",
					"quote_asset": "fiat:USD",
					"base_amount": "1000000",
					"quote_amount": "159000",
					"price": "0.159"
				}
			],
			"as_of": "2026-05-09T15:00:00Z",
			"flags": {}
		}`))
	}))
	defer srv.Close()

	c := client.New(client.Options{BaseURL: srv.URL})
	got, err := c.Observations(context.Background(), client.ObservationsQuery{
		Asset: "native", Quote: "fiat:USD",
	})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	for _, row := range got.Data {
		fmt.Printf("%s @ %s: %s\n", row.Source, row.Timestamp.Format("15:04:05Z"), row.Price)
	}

	// Output:
	// binance @ 15:00:00Z: 0.16
	// sdex @ 14:59:50Z: 0.159
}

// ExampleClient_ChangeSummary demonstrates the per-entity
// multi-window delta rollup. The change-summary worker writes one
// row per (entity_type, entity_id) every 5 min; this method
// surfaces the latest. EntityType is one of "coin", "protocol",
// "pair", "source"; for coin entities the API expands friendly
// slugs (XLM, USDC) into canonical asset_id forms server-side.
//
// All H*/D* fields are *float64 — nil distinguishes "no value
// yet" (window opened recently) from "0% change". Render `—` on
// nil rather than fabricating a zero.
func ExampleClient_ChangeSummary() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": {
				"entity_type": "coin",
				"entity_id": "crypto:XLM",
				"refreshed_at": "2026-05-09T15:00:00Z",
				"current_value": 0.163,
				"h24_delta_pct": 3.21,
				"d7_delta_pct": -1.8,
				"streak_direction": "up",
				"acceleration": "increasing"
			},
			"as_of": "2026-05-09T15:00:00Z",
			"flags": {}
		}`))
	}))
	defer srv.Close()

	c := client.New(client.Options{BaseURL: srv.URL})
	got, err := c.ChangeSummary(context.Background(), client.ChangeSummaryQuery{
		EntityType: "coin", EntityID: "crypto:XLM",
	})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	pct := func(p *float64) string {
		if p == nil {
			return "—"
		}
		return fmt.Sprintf("%+.2f%%", *p)
	}
	fmt.Printf("%s: $%.4f, 24h=%s 7d=%s (%s, %s)\n",
		got.Data.EntityID,
		got.Data.CurrentValue,
		pct(got.Data.H24DeltaPct),
		pct(got.Data.D7DeltaPct),
		got.Data.StreakDirection,
		got.Data.Acceleration)

	// Output: crypto:XLM: $0.1630, 24h=+3.21% 7d=-1.80% (up, increasing)
}
