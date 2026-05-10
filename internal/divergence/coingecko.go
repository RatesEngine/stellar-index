package divergence

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// CoinGeckoReference looks up prices via CoinGecko's public
// /api/v3/simple/price endpoint. Free tier has no API key but a
// modest rate limit (~30 req/min); the reference is best-effort —
// transient 429s bubble up as transport failures and the caller
// just treats this run's CoinGecko response as missing.
type CoinGeckoReference struct {
	httpClient *http.Client
	baseURL    string

	// idMap maps canonical asset_id strings to CoinGecko's own
	// asset slugs (e.g. "native" → "stellar"). Operator-curated;
	// any asset not in the map yields ErrAssetUnsupported.
	idMap map[string]string

	// quoteMap maps canonical quote currency to CoinGecko's
	// supported vs_currency code (e.g. "fiat:USD" → "usd",
	// "fiat:EUR" → "eur"). Limited set; CoinGecko supports the
	// common fiats + a few major cryptos.
	quoteMap map[string]string
}

// CoinGeckoOptions configures [NewCoinGeckoReference].
type CoinGeckoOptions struct {
	// HTTPClient — nil falls back to a 10s-timeout client.
	HTTPClient *http.Client

	// BaseURL overrides the API base. Empty defaults to
	// "https://api.coingecko.com/api/v3". Tests pass an
	// httptest.Server URL.
	BaseURL string

	// IDMap maps canonical asset_id → CoinGecko slug. At minimum
	// the operator should provide entries for every base asset
	// the aggregator publishes prices for. Empty map yields
	// ErrAssetUnsupported on every lookup.
	IDMap map[string]string

	// QuoteMap maps canonical quote string → CoinGecko vs_currency.
	// Empty falls back to a small built-in default covering
	// fiat:USD/EUR/GBP/JPY + crypto:BTC/ETH.
	QuoteMap map[string]string
}

// NewCoinGeckoReference constructs a CoinGecko-backed reference.
//
// When opts.IDMap is empty, the reference falls back to a built-in
// default that covers the canonical asset_id forms the aggregator
// computes by default (XLM in both `crypto:XLM` and `native` forms,
// BTC, ETH, LINK, plus the major USD stablecoins). Without this
// fallback every divergence-cross-check call returns
// `ErrAssetUnsupported` and `divergence_observations` stays empty
// for any operator who hasn't manually populated `[divergence.coingecko].id_map`
// — which the type-level docs already promised wouldn't happen.
// Operator-supplied entries merge OVER the defaults (operator wins),
// so an operator can still narrow the set or override a slug.
func NewCoinGeckoReference(opts CoinGeckoOptions) *CoinGeckoReference {
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	baseURL := opts.BaseURL
	if baseURL == "" {
		baseURL = "https://api.coingecko.com/api/v3"
	}
	baseURL = strings.TrimRight(baseURL, "/")

	idMap := defaultCoinGeckoIDMap()
	for k, v := range opts.IDMap {
		idMap[k] = v
	}
	quoteMap := opts.QuoteMap
	if len(quoteMap) == 0 {
		quoteMap = defaultCoinGeckoQuoteMap()
	}

	return &CoinGeckoReference{
		httpClient: httpClient,
		baseURL:    baseURL,
		idMap:      idMap,
		quoteMap:   quoteMap,
	}
}

// defaultCoinGeckoIDMap covers the canonical asset_id forms the
// aggregator computes by default (per cmd/ratesengine-aggregator/
// main.go::defaultPairs — XLM/BTC/ETH × USD/EUR/GBP, with XLM in
// both `crypto:XLM` and `native` forms). Major USD stablecoins are
// included so a deployment with stablecoin-fiat-proxy enabled
// (ADR-0026) can cross-check the underlying USDC/USDT path too.
//
// Slugs verified against https://api.coingecko.com/api/v3/coins/list.
// Mirrors the per-source coingecko poller's `tickerToID` map
// (internal/sources/external/coingecko/poller.go) — kept separate
// here because the divergence path keys on canonical asset_id
// strings (`crypto:XLM`, `native`) while the poller keys on bare
// upper-case tickers.
func defaultCoinGeckoIDMap() map[string]string {
	return map[string]string{
		"crypto:XLM":  "stellar",
		"native":      "stellar",
		"crypto:BTC":  "bitcoin",
		"crypto:ETH":  "ethereum",
		"crypto:LINK": "chainlink",
		"crypto:SOL":  "solana",
		"crypto:ADA":  "cardano",
		"crypto:DOT":  "polkadot",
		// Major USD stablecoins — useful when the aggregator's
		// stablecoin-fiat proxy (ADR-0026) is on and we want to
		// cross-check the underlying X/USDC or X/USDT path.
		"crypto:USDC":  "usd-coin",
		"crypto:USDT":  "tether",
		"crypto:PYUSD": "paypal-usd",
	}
}

// Name implements [Reference].
func (c *CoinGeckoReference) Name() string { return "coingecko" }

// LookupPrice implements [Reference]. observedAt is ignored —
// CoinGecko's /simple/price returns the latest cached price; for
// closed-bucket comparison this is acceptable when the bucket is
// recent (within a few minutes).
func (c *CoinGeckoReference) LookupPrice(ctx context.Context, pair canonical.Pair, _ time.Time) (float64, error) {
	cgID, ok := c.idMap[pair.Base.String()]
	if !ok {
		return 0, fmt.Errorf("%w: base %q has no CoinGecko slug in idMap", ErrAssetUnsupported, pair.Base.String())
	}
	cgQuote, ok := c.quoteMap[pair.Quote.String()]
	if !ok {
		return 0, fmt.Errorf("%w: quote %q has no CoinGecko vs_currency", ErrAssetUnsupported, pair.Quote.String())
	}

	v := url.Values{}
	v.Set("ids", cgID)
	v.Set("vs_currencies", cgQuote)
	endpoint := c.baseURL + "/simple/price?" + v.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, fmt.Errorf("coingecko: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "ratesengine-divergence/0.1")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("coingecko: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusTooManyRequests {
		return 0, fmt.Errorf("%w: coingecko rate-limited (HTTP 429)", ErrPriceUnavailable)
	}
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("coingecko: HTTP %d", resp.StatusCode)
	}

	// Cap response size — /simple/price for one asset is < 1 KiB
	// in practice; bound at 64 KiB just in case the operator
	// overrides the URL with something exotic.
	const maxBody = 64 << 10
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return 0, fmt.Errorf("coingecko: read body: %w", err)
	}

	// Response shape: {"<id>": {"<vs_currency>": <price>}}
	var parsed map[string]map[string]float64
	if err := json.Unmarshal(body, &parsed); err != nil {
		return 0, fmt.Errorf("coingecko: decode: %w", err)
	}
	idEntry, ok := parsed[cgID]
	if !ok {
		return 0, fmt.Errorf("%w: coingecko id %q absent in response", ErrAssetUnsupported, cgID)
	}
	price, ok := idEntry[cgQuote]
	if !ok {
		return 0, fmt.Errorf("%w: coingecko vs_currency %q absent for id %q", ErrAssetUnsupported, cgQuote, cgID)
	}
	if !isFinitePositive(price) {
		return 0, fmt.Errorf("%w: coingecko returned non-positive price %g", ErrPriceUnavailable, price)
	}
	return price, nil
}

// defaultCoinGeckoQuoteMap covers the fiat/crypto pairs we
// commonly serve. Operator can override via [CoinGeckoOptions.QuoteMap].
func defaultCoinGeckoQuoteMap() map[string]string {
	return map[string]string{
		"fiat:USD":   "usd",
		"fiat:EUR":   "eur",
		"fiat:GBP":   "gbp",
		"fiat:JPY":   "jpy",
		"fiat:CHF":   "chf",
		"fiat:AUD":   "aud",
		"fiat:CAD":   "cad",
		"fiat:CNY":   "cny",
		"fiat:KRW":   "krw",
		"fiat:INR":   "inr",
		"crypto:BTC": "btc",
		"crypto:ETH": "eth",
	}
}

// Compile-time check.
var _ Reference = (*CoinGeckoReference)(nil)
