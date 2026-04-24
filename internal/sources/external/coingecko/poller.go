// Package coingecko polls CoinGecko's public /simple/price endpoint
// for cross-check reference prices. First `ClassAggregator` connector
// in the fleet — excluded from VWAP by registry policy (mixing
// aggregated prices with raw trades double-counts upstream markets),
// but consumed by the future divergence-detection layer to flag
// when our computed VWAP drifts from the aggregator consensus.
//
// Free-tier friendly: CoinGecko's /simple/price has no auth requirement
// on public data and a generous ~10-30 req/min limit. One batched call
// per poll covers every (asset, quote) combo we care about.
//
// Wire shape (verified 2026-04-24):
//
//	GET https://api.coingecko.com/api/v3/simple/price?ids=stellar,bitcoin,ethereum&vs_currencies=usd,eur
//
//	{
//	  "stellar":  {"usd": 0.17582, "eur": 0.16230},
//	  "bitcoin":  {"usd": 50000.0, "eur": 46250.0},
//	  "ethereum": {"usd": 3500.0,  "eur": 3237.5}
//	}
//
// Symbol mapping: CoinGecko uses **slug IDs** ("stellar", "bitcoin")
// not tickers ("XLM", "BTC"). We maintain a small allow-listed
// ticker→id table covering the assets in our pair set. Unknown
// tickers skip per-entry.
package coingecko

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/sources/external"
)

// SourceName is stamped on every canonical.OracleUpdate this package
// emits. Must match the registry key.
const SourceName = "coingecko"

// DefaultEndpoint is the public REST base.
const DefaultEndpoint = "https://api.coingecko.com"

// SimplePricePath is the batch-price endpoint.
const SimplePricePath = "/api/v3/simple/price"

// DefaultPollInterval — 60s aligns with the free tier's quota.
const DefaultPollInterval = 60 * time.Second

// DefaultDecimals — 8dp matches the off-chain-source convention.
const DefaultDecimals uint8 = 8

// Compile-time assertion: aggregator class in registry.
var _ = external.ClassAggregator

// tickerToID maps upper-case crypto tickers to CoinGecko slugs.
// Slugs verified against https://api.coingecko.com/api/v3/coins/list.
// Extending is a one-line addition when a new asset enters our fleet.
var tickerToID = map[string]string{
	"BTC":   "bitcoin",
	"ETH":   "ethereum",
	"XLM":   "stellar",
	"USDT":  "tether",
	"USDC":  "usd-coin",
	"PYUSD": "paypal-usd",
	"SOL":   "solana",
	"ADA":   "cardano",
	"DOT":   "polkadot",
	"LINK":  "chainlink",
	"MATIC": "polygon",
	"AVAX":  "avalanche-2",
	"BNB":   "binancecoin",
}

var ErrMalformedResponse = errors.New("coingecko: malformed response")

// Poller implements external.Poller.
type Poller struct {
	Endpoint string
	Interval time.Duration
	// APIKey is optional. CoinGecko Pro lets you pass
	// x_cg_pro_api_key for higher rate limits. Empty → free tier.
	APIKey string
}

// NewPoller constructs a Poller with defaults. No key required.
func NewPoller() *Poller {
	return &Poller{
		Endpoint: DefaultEndpoint,
		Interval: DefaultPollInterval,
	}
}

// Name implements external.Connector.
func (p *Poller) Name() string { return SourceName }

// Class implements external.Connector.
func (p *Poller) Class() external.Class { return external.ClassAggregator }

// PollInterval implements external.Poller.
func (p *Poller) PollInterval() time.Duration {
	if p.Interval <= 0 {
		return DefaultPollInterval
	}
	return p.Interval
}

// simplePriceResponse is the nested map CoinGecko returns.
// Outer key = asset slug, inner key = fiat code (lowercase).
type simplePriceResponse map[string]map[string]float64

// PollOnce implements external.Poller. One batched GET covers every
// (id, vs_currency) combo in a single JSON map. We derive id +
// vs_currency sets from the configured pair list, batch them,
// then emit one OracleUpdate per (ticker, currency) hit.
func (p *Poller) PollOnce(ctx context.Context, pairs []canonical.Pair) ([]canonical.Trade, []canonical.OracleUpdate, error) { //nolint:gocognit,gocyclo,funlen // dispatch-heavy; splitting would reduce linearity
	idSet := map[string]struct{}{}
	tickerForID := map[string]string{}
	cryptoAssets := map[string]canonical.Asset{}
	currencySet := map[string]struct{}{}
	fiatAssets := map[string]canonical.Asset{}
	// wantedCombos filters the venue's N×M response matrix down to
	// just the (crypto, fiat) pairs the operator configured —
	// avoids flooding oracle_updates with divergence-candidates
	// for pairs that have no corresponding aggregated output.
	wantedCombos := map[string]struct{}{}

	for _, pair := range pairs {
		if pair.Base.Type != canonical.AssetCrypto || pair.Quote.Type != canonical.AssetFiat {
			continue
		}
		ticker := strings.ToUpper(pair.Base.Code)
		id, ok := tickerToID[ticker]
		if !ok {
			continue // unknown crypto ticker; skip the whole pair
		}
		code := strings.ToUpper(pair.Quote.Code)
		idSet[id] = struct{}{}
		tickerForID[id] = ticker
		cryptoAssets[ticker] = pair.Base
		currencySet[strings.ToLower(code)] = struct{}{}
		fiatAssets[code] = pair.Quote
		wantedCombos[ticker+"/"+code] = struct{}{}
	}

	if len(idSet) == 0 || len(currencySet) == 0 {
		return nil, nil, nil
	}

	ids := make([]string, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}
	currencies := make([]string, 0, len(currencySet))
	for c := range currencySet {
		currencies = append(currencies, c)
	}

	q := url.Values{}
	q.Set("ids", strings.Join(ids, ","))
	q.Set("vs_currencies", strings.Join(currencies, ","))
	if p.APIKey != "" {
		q.Set("x_cg_pro_api_key", p.APIKey)
	}

	endpoint := p.Endpoint
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+SimplePricePath+"?"+q.Encode(), nil)
	if err != nil {
		return nil, nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("http: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
	if err != nil {
		return nil, nil, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("http %d: %s", resp.StatusCode, string(body))
	}

	var prices simplePriceResponse
	if err := json.Unmarshal(body, &prices); err != nil {
		return nil, nil, fmt.Errorf("%w: %w", ErrMalformedResponse, err)
	}

	ts := time.Now().UTC()
	updates := make([]canonical.OracleUpdate, 0, len(idSet)*len(currencySet))

	for id, cs := range prices {
		ticker, ok := tickerForID[id]
		if !ok {
			continue
		}
		cryptoAsset, ok := cryptoAssets[ticker]
		if !ok {
			continue
		}
		for currency, priceFloat := range cs {
			cUp := strings.ToUpper(currency)
			quoteAsset, ok := fiatAssets[cUp]
			if !ok {
				continue
			}
			if _, want := wantedCombos[ticker+"/"+cUp]; !want {
				continue
			}
			if priceFloat <= 0 {
				continue
			}
			scaled, err := floatToScaledInt(priceFloat, int(DefaultDecimals))
			if err != nil || scaled.Sign() <= 0 {
				continue
			}
			u := canonical.OracleUpdate{
				Source:     SourceName,
				ContractID: "",
				Ledger:     0,
				TxHash:     syntheticTxHash(ticker, currency, ts.Unix()),
				OpIndex:    0,
				Timestamp:  ts,
				Asset:      cryptoAsset,
				Quote:      quoteAsset,
				Price:      canonical.NewAmount(scaled),
				Decimals:   DefaultDecimals,
				Observer:   "",
			}
			updates = append(updates, u)
		}
	}
	return nil, updates, nil
}

// floatToScaledInt converts a float64 price to a scaled *big.Int.
// CoinGecko emits JSON numbers; we format via strconv at
// decimals+2 precision to preserve enough sig figs for integer math
// at our 10^8 scale before feeding decimalStringToScaledInt.
func floatToScaledInt(v float64, decimals int) (*big.Int, error) {
	if v < 0 || v != v {
		return nil, fmt.Errorf("bad value %v", v)
	}
	s := strconv.FormatFloat(v, 'f', decimals+2, 64)
	return decimalStringToScaledInt(s, decimals)
}

// decimalStringToScaledInt — per-package scaling helper, matches
// the convention used by every external source.
func decimalStringToScaledInt(s string, targetDecimals int) (*big.Int, error) {
	if s == "" {
		return nil, fmt.Errorf("empty decimal string")
	}
	if strings.ContainsAny(s, "eE") {
		return nil, fmt.Errorf("scientific notation %q not supported", s)
	}
	neg := false
	if s[0] == '-' {
		neg = true
		s = s[1:]
	}
	intPart, fracPart := s, ""
	if dot := strings.IndexByte(s, '.'); dot >= 0 {
		intPart = s[:dot]
		fracPart = s[dot+1:]
	}
	if intPart == "" {
		intPart = "0"
	}
	if len(fracPart) > targetDecimals {
		fracPart = fracPart[:targetDecimals]
	}
	for len(fracPart) < targetDecimals {
		fracPart += "0"
	}
	v, ok := new(big.Int).SetString(intPart+fracPart, 10)
	if !ok {
		return nil, fmt.Errorf("not a decimal: %q", s)
	}
	if neg {
		v.Neg(v)
	}
	return v, nil
}

// syntheticTxHash derives a 64-char hex from (ticker, currency, ts).
func syntheticTxHash(ticker, currency string, ts int64) string {
	s := fmt.Sprintf("COINGECKO-%s-%s-%020d", strings.ToUpper(ticker), strings.ToUpper(currency), ts)
	var hex strings.Builder
	hex.Grow(64)
	for _, b := range []byte(s) {
		fmt.Fprintf(&hex, "%02x", b)
		if hex.Len() >= 64 {
			break
		}
	}
	for hex.Len() < 64 {
		hex.WriteByte('0')
	}
	return hex.String()[:64]
}
