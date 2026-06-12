package v1_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/aggregate"
	v1 "github.com/StellarAtlas/stellar-atlas/internal/api/v1"
	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
)

// stubGlobalPriceReader implements aggregate.GlobalPriceReader for
// the handler tests. Configurable per-tier.
type stubGlobalPriceReader struct {
	vwap struct {
		price      string
		asOf       time.Time
		tradeCount int64
		sources    []string
		ok         bool
	}
	agg struct {
		rows []canonical.OracleUpdate
	}
	tri struct {
		price string
		asOf  time.Time
		ok    bool
	}
}

func (s *stubGlobalPriceReader) LatestVWAP(_ context.Context, _, _ canonical.Asset) (string, time.Time, int64, []string, bool, error) {
	return s.vwap.price, s.vwap.asOf, s.vwap.tradeCount, s.vwap.sources, s.vwap.ok, nil
}

func (s *stubGlobalPriceReader) LatestAggregatorPrices(_ context.Context, _, _ canonical.Asset, _ []string) ([]canonical.OracleUpdate, error) {
	return s.agg.rows, nil
}

func (s *stubGlobalPriceReader) LookupTriangulated(_ context.Context, _, _ canonical.Asset, _ time.Duration) (string, time.Time, bool, error) {
	return s.tri.price, s.tri.asOf, s.tri.ok, nil
}

func TestAssetGet_SlugDispatch_GlobalView(t *testing.T) {
	cat := newTestCatalogue(t)
	reader := &stubGlobalPriceReader{}
	reader.vwap.price = "1.00050000000000"
	reader.vwap.asOf = time.Now().UTC().Truncate(time.Second)
	reader.vwap.tradeCount = 12
	reader.vwap.sources = []string{"coinbase", "binance"}
	reader.vwap.ok = true

	srv := v1.New(v1.Options{
		VerifiedCurrencies: cat,
		GlobalPrice:        reader,
		GlobalPriceOpts: aggregate.GlobalPriceOptions{
			AggregatorSources: []string{"coingecko"},
		},
	})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/assets/usdc")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var env struct {
		Data v1.GlobalAssetView `json:"data"`
	}
	mustDecode(t, resp, &env)
	if env.Data.Ticker != "USDC" || env.Data.Slug != "usdc" {
		t.Errorf("wrong identity: %+v", env.Data)
	}
	if env.Data.Name != "USD Coin" {
		t.Errorf("name = %q, want USD Coin", env.Data.Name)
	}
	if env.Data.VerifiedIssuer == "" {
		t.Error("verified_issuer empty")
	}
	if env.Data.PriceUSD == nil || *env.Data.PriceUSD != "1.00050000000000" {
		t.Errorf("price_usd = %v, want 1.00050000000000", env.Data.PriceUSD)
	}
	if env.Data.PriceAuthority != aggregate.AuthorityVWAPNative {
		t.Errorf("price_authority = %q, want vwap_native", env.Data.PriceAuthority)
	}
	if len(env.Data.Networks) == 0 {
		t.Error("networks empty")
	}
	// Stellar entry must carry a deep_link.
	var foundStellar bool
	for _, n := range env.Data.Networks {
		if n.Network == "stellar" {
			foundStellar = true
			if n.DataQuality != "indexed" {
				t.Errorf("stellar data_quality = %q, want indexed", n.DataQuality)
			}
			if n.DeepLink == "" {
				t.Error("stellar entry missing deep_link")
			}
			if n.AssetID == "" {
				t.Error("stellar entry missing asset_id")
			}
		}
	}
	if !foundStellar {
		t.Error("USDC catalogue entry has no stellar network — expected one")
	}
	// Non-Stellar entries must be data_quality="external" with no deep_link.
	for _, n := range env.Data.Networks {
		if n.Network != "stellar" {
			if n.DataQuality != "external" {
				t.Errorf("%s data_quality = %q, want external", n.Network, n.DataQuality)
			}
			if n.DeepLink != "" {
				t.Errorf("%s has deep_link %q; non-Stellar should not", n.Network, n.DeepLink)
			}
		}
	}
}

func TestAssetGet_SlugDispatch_StellarOnlyTokenNoPrice(t *testing.T) {
	// AQUA is in the catalogue but `crypto:AQUA` won't be on the
	// canonical crypto allow-list (it's a Stellar-only token).
	// Global view still resolves: identity + networks populate;
	// price block stays nil. Consumers drill into the Stellar
	// deep_link for the per-asset price.
	cat := newTestCatalogue(t)
	reader := &stubGlobalPriceReader{}
	// No vwap.ok, no agg.rows, no tri.ok — every tier misses.

	srv := v1.New(v1.Options{
		VerifiedCurrencies: cat,
		GlobalPrice:        reader,
	})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/assets/aqua")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var env struct {
		Data v1.GlobalAssetView `json:"data"`
	}
	mustDecode(t, resp, &env)
	if env.Data.Ticker != "AQUA" {
		t.Errorf("ticker = %q", env.Data.Ticker)
	}
	if env.Data.PriceUSD != nil {
		t.Errorf("price_usd should be nil for AQUA (no global price), got %v", env.Data.PriceUSD)
	}
	// Stellar deep_link still present — consumer's path forward.
	if len(env.Data.Networks) == 0 || env.Data.Networks[0].DeepLink == "" {
		t.Error("stellar network/deep_link missing")
	}
}

func TestAssetGet_SlugDispatch_NoGlobalPriceReader(t *testing.T) {
	// When the binary doesn't wire GlobalPrice, the slug still
	// resolves to the catalogue identity + networks; the price
	// block is just empty.
	cat := newTestCatalogue(t)
	srv := v1.New(v1.Options{
		VerifiedCurrencies: cat,
		// No GlobalPrice.
	})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/assets/usdc")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var env struct {
		Data v1.GlobalAssetView `json:"data"`
	}
	mustDecode(t, resp, &env)
	if env.Data.PriceUSD != nil {
		t.Errorf("price_usd should be nil without a reader, got %v", env.Data.PriceUSD)
	}
	if env.Data.Ticker != "USDC" {
		t.Errorf("ticker = %q", env.Data.Ticker)
	}
}

func TestAssetGet_CanonicalIDStillWorksWithCatalogue(t *testing.T) {
	// With the catalogue wired, a canonical asset_id (USDC-G...)
	// must still route to the per-Stellar-asset view, NOT to the
	// global slug view. Slug dispatch only matches bare slugs.
	cat := newTestCatalogue(t)
	srv := v1.New(v1.Options{
		VerifiedCurrencies: cat,
	})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/assets/USDC-"+testUSDCIssuer)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var env struct {
		Data v1.AssetDetail `json:"data"`
	}
	mustDecode(t, resp, &env)
	if env.Data.AssetID != "USDC-"+testUSDCIssuer {
		t.Errorf("canonical id routed wrong; got asset_id = %q", env.Data.AssetID)
	}
	if env.Data.Type != "classic" {
		t.Errorf("type = %q, want classic", env.Data.Type)
	}
}

func TestAssetGet_UnknownSlug_FallsThroughToCanonicalParse(t *testing.T) {
	// A path that's not a known slug AND not a canonical id must
	// return 400 (the existing invalid-asset-id problem). Slug
	// dispatch doesn't change that behaviour.
	cat := newTestCatalogue(t)
	srv := v1.New(v1.Options{VerifiedCurrencies: cat})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/assets/notarealthing")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

// TestAssetGet_Fiat_USDIdentity — /v1/assets/us-dollar synthesises
// a 1.00 price (identity) without hitting the global-price reader,
// and computes market_cap_usd directly from the catalogue's M2 figure.
func TestAssetGet_Fiat_USDIdentity(t *testing.T) {
	cat := newTestCatalogue(t)
	srv := v1.New(v1.Options{
		VerifiedCurrencies: cat,
		// Provide a stub reader so the price block isn't short-
		// circuited by the nil-guard; the USD path doesn't call
		// the reader but the handler checks s.globalPrice != nil.
		GlobalPrice: &stubGlobalPriceReader{},
	})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/assets/us-dollar")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var env struct {
		Data v1.GlobalAssetView `json:"data"`
	}
	mustDecode(t, resp, &env)
	d := env.Data

	if d.Class != "fiat" {
		t.Errorf("class = %q, want fiat", d.Class)
	}
	if d.PriceUSD == nil || *d.PriceUSD != "1.00000000000000" {
		t.Errorf("price_usd = %v, want 1.00000000000000", d.PriceUSD)
	}
	if d.CirculatingSupply == nil {
		t.Errorf("circulating_supply missing for USD")
	}
	// USD M2 × 1.00 → "21700000000000.00" (seed M2 value × identity).
	if d.MarketCapUSD == nil {
		t.Fatalf("market_cap_usd missing for USD")
	}
	if *d.MarketCapUSD != "21700000000000.00" {
		t.Errorf("USD market_cap_usd = %q, want 21700000000000.00", *d.MarketCapUSD)
	}
	if len(d.Networks) != 0 {
		t.Errorf("fiat shouldn't have networks; got %d", len(d.Networks))
	}
}

// TestAssetGet_Fiat_CNY_MarketCap — non-USD fiat: handler reads
// the FX rate from PriceReader (s.prices) for the fiat:CNY/fiat:USD
// pair, then multiplies M2 by the result. PriceReader is used
// rather than ComputeGlobalPrice because the FX-rate Redis
// triangulated fallback (which prices_1m doesn't carry for
// fiat:fiat) is in scope of the production PriceReader.
func TestAssetGet_Fiat_CNY_MarketCap(t *testing.T) {
	cat := newTestCatalogue(t)
	priceStub := &stubPriceReader{
		snapshots: map[string]v1.PriceSnapshot{
			"fiat:CNY/fiat:USD": {
				AssetID: "fiat:CNY", Quote: "fiat:USD",
				Price: "0.14000000000000", PriceType: "vwap",
			},
		},
	}

	srv := v1.New(v1.Options{
		VerifiedCurrencies: cat,
		Prices:             priceStub,
	})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/assets/chinese-yuan")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var env struct {
		Data v1.GlobalAssetView `json:"data"`
	}
	mustDecode(t, resp, &env)
	d := env.Data

	if d.Class != "fiat" {
		t.Errorf("class = %q, want fiat", d.Class)
	}
	if d.PriceUSD == nil || *d.PriceUSD != "0.14000000000000" {
		t.Errorf("price_usd = %v, want 0.14000000000000", d.PriceUSD)
	}
	// CNY M2 = 302_000_000_000_000; × 0.14 = 42_280_000_000_000.00
	if d.MarketCapUSD == nil || *d.MarketCapUSD != "42280000000000.00" {
		t.Errorf("CNY market_cap_usd = %v, want 42280000000000.00", d.MarketCapUSD)
	}
}

// TestAssetByNetwork_StellarRedirect — /v1/assets/usdc/stellar
// 303-redirects to the canonical /v1/assets/USDC-G… asset_id.
// Consumers that follow the redirect get the full AssetDetail
// (SEP-1 overlay + F2 fields + unverified-warning machinery).
func TestAssetByNetwork_StellarRedirect(t *testing.T) {
	cat := newTestCatalogue(t)
	srv := v1.New(v1.Options{VerifiedCurrencies: cat})
	ts := httpTestServer(t, srv)

	// httptest's default client follows redirects. Strip the client
	// of its follower to inspect the 303 itself.
	cli := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := cli.Get(ts.URL + "/v1/assets/usdc/stellar")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if loc != "/v1/assets/USDC-"+testUSDCIssuer {
		t.Errorf("Location = %q, want /v1/assets/USDC-…", loc)
	}
}

// TestAssetByNetwork_NonStellar — /v1/assets/usdc/ethereum returns
// a PerNetworkAssetView with the catalogue's Ethereum contract.
func TestAssetByNetwork_NonStellar(t *testing.T) {
	cat := newTestCatalogue(t)
	srv := v1.New(v1.Options{VerifiedCurrencies: cat})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/assets/usdc/ethereum")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var env struct {
		Data v1.PerNetworkAssetView `json:"data"`
	}
	mustDecode(t, resp, &env)
	d := env.Data
	if d.Ticker != "USDC" || d.Slug != "usdc" {
		t.Errorf("identity mismatch: %+v", d)
	}
	if d.Network != "ethereum" {
		t.Errorf("network = %q", d.Network)
	}
	if d.DataQuality != "external" {
		t.Errorf("data_quality = %q, want external", d.DataQuality)
	}
	if d.Contract == "" {
		t.Errorf("contract empty — USDC Ethereum entry should have a contract")
	}
}

// TestAssetByNetwork_UnknownNetwork — slug exists, network doesn't.
func TestAssetByNetwork_UnknownNetwork(t *testing.T) {
	cat := newTestCatalogue(t)
	srv := v1.New(v1.Options{VerifiedCurrencies: cat})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/assets/usdc/dogecoin")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (USDC isn't on dogecoin)", resp.StatusCode)
	}
}

// TestAssetByNetwork_UnknownSlug — slug isn't in the catalogue.
func TestAssetByNetwork_UnknownSlug(t *testing.T) {
	cat := newTestCatalogue(t)
	srv := v1.New(v1.Options{VerifiedCurrencies: cat})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/assets/not-a-real-slug/stellar")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

// TestAssetByNetwork_MetadataRouteStillWorks — verifies the
// /v1/assets/{asset_id}/metadata route isn't shadowed by the new
// /v1/assets/{asset_id}/{network} catch-all. Go 1.22+ mux picks
// the literal "metadata" segment over the wildcard {network}.
func TestAssetByNetwork_MetadataRouteStillWorks(t *testing.T) {
	cat := newTestCatalogue(t)
	srv := v1.New(v1.Options{VerifiedCurrencies: cat})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/assets/native/metadata")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/v1/assets/native/metadata status = %d (network route may be shadowing)", resp.StatusCode)
	}
	// Body shape: AssetMetadata, not PerNetworkAssetView.
	var env struct {
		Data map[string]any `json:"data"`
	}
	mustDecode(t, resp, &env)
	if _, ok := env.Data["sep1_status"]; !ok {
		t.Errorf("body missing sep1_status — looks like the network route swallowed /metadata: %+v", env.Data)
	}
}

// /v1/coins removed (no production consumers); deprecation-header
// test deleted along with the routes.
