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
	"sync"
	"time"
	"unicode/utf8"

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

// MinBackoff is the floor for the post-429 cooldown. Even if the
// venue's Retry-After is shorter (or absent), the next poll
// waits at least this long.
const MinBackoff = 60 * time.Second

// MaxBackoff caps the exponential growth per the package comment
// — at sustained denial we still re-attempt once an hour to
// auto-recover when an operator provisions a key or the
// per-IP cap resets.
const MaxBackoff = 1 * time.Hour

// Poller implements external.Poller.
type Poller struct {
	Endpoint string
	Interval time.Duration
	// APIKey is the CoinGecko Pro key (param x_cg_pro_api_key).
	// Empty → no Pro auth.
	APIKey string
	// DemoAPIKey is the free-tier "demo" key (param
	// x_cg_demo_api_key). CoinGecko's public-no-auth tier was
	// tightened in late 2024; demo keys are a free signup that
	// raises per-IP throttling. Set this OR APIKey, not both.
	// When both are set Pro wins.
	DemoAPIKey string

	// mu guards the cooldown state. The runner serialises calls per
	// source so contention is nil; the lock makes concurrent reads
	// safe under future test fixtures and metrics endpoints.
	mu             sync.Mutex
	nextAllowedAt  time.Time     // earliest UTC time the poller is allowed to hit the venue again
	currentBackoff time.Duration // last applied backoff, doubled on consecutive 429s
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
//
// 429 handling: when the venue returns HTTP 429 (or 403 — CoinGecko
// emits 403 for demo-key-required paths post-2024), the poller skips
// subsequent calls until `nextAllowedAt` to avoid hammering the venue
// with refusals. Cooldown duration is `Retry-After` (capped at
// `MaxBackoff`) when the header is present, otherwise an exponential
// backoff doubling from `MinBackoff` to `MaxBackoff`. The first
// successful response resets the backoff to zero.
//
// Returning (nil, nil, nil) during cooldown — distinct from an error
// — keeps the runner happy without adding "poller error" log spam
// for the obvious "we're respecting cooldown" case.
func (p *Poller) PollOnce(ctx context.Context, pairs []canonical.Pair) ([]canonical.Trade, []canonical.OracleUpdate, error) { //nolint:gocognit,gocyclo,funlen // dispatch-heavy; splitting would reduce linearity
	if cooldownLeft := p.cooldownRemaining(); cooldownLeft > 0 {
		return nil, nil, nil
	}
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
	} else if p.DemoAPIKey != "" {
		q.Set("x_cg_demo_api_key", p.DemoAPIKey)
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
	// Treat 429 (Too Many Requests) and 403 (post-2024 demo-key-required
	// path) as throttling. Apply backoff and bail without spamming the
	// venue or polluting logs every minute.
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusForbidden {
		wait := backoffFromRetryAfter(resp.Header.Get("Retry-After"))
		p.applyBackoff(wait)
		return nil, nil, fmt.Errorf("http %d (throttled — backing off %s): %s",
			resp.StatusCode, p.cooldownRemaining().Truncate(time.Second), truncate(string(body), 200))
	}
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("http %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	// Successful response — clear any prior backoff so the next 429
	// starts fresh from MinBackoff rather than wherever we'd grown to.
	p.resetBackoff()

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

// cooldownRemaining returns how much longer the poller must wait
// before hitting the venue again. Zero (or negative) means polling
// is allowed. Lock-protected so the runner + tests can read state
// safely.
func (p *Poller) cooldownRemaining() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.nextAllowedAt.IsZero() {
		return 0
	}
	return time.Until(p.nextAllowedAt)
}

// applyBackoff arms the cooldown. If `hint` (parsed Retry-After) is
// positive, use it (clamped to [MinBackoff, MaxBackoff]). Otherwise
// double the previous backoff up to MaxBackoff.
func (p *Poller) applyBackoff(hint time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var wait time.Duration
	if hint > 0 {
		wait = hint
		if wait < MinBackoff {
			wait = MinBackoff
		}
		if wait > MaxBackoff {
			wait = MaxBackoff
		}
	} else {
		wait = p.currentBackoff * 2
		if wait < MinBackoff {
			wait = MinBackoff
		}
		if wait > MaxBackoff {
			wait = MaxBackoff
		}
	}
	p.currentBackoff = wait
	p.nextAllowedAt = time.Now().Add(wait)
}

// resetBackoff clears the cooldown after a successful poll.
func (p *Poller) resetBackoff() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.currentBackoff = 0
	p.nextAllowedAt = time.Time{}
}

// backoffFromRetryAfter parses the Retry-After header per RFC 7231
// section 7.1.3 — either a non-negative integer of seconds or an
// HTTP-date. Returns 0 when the header is absent or unparseable
// (caller falls through to the exponential branch).
func backoffFromRetryAfter(h string) time.Duration {
	if h == "" {
		return 0
	}
	if secs, err := strconv.Atoi(strings.TrimSpace(h)); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(h); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

// truncate keeps log lines bounded — CoinGecko's HTML error pages
// can be 50KB+ which would be useless noise in the indexer log.
// truncate cuts `s` to at most `n` bytes plus a trailing "…",
// walking back to the nearest UTF-8 rune boundary at or before
// byte n. Used in error messages for HTTP response bodies; raw
// byte slicing produced invalid UTF-8 in operator log output
// when CoinGecko's error pages contained Unicode (e.g. their
// "rate limit" copy or proxy injection).
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	end := n
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end] + "…"
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
