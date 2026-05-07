// Package forex provides a fiat-currency rates feed for the
// /v1/currencies surface. Source: massive.com REST API
// (Polygon-shape endpoints + auth) — hourly grain, ~200 currencies,
// requires an API key (env MASSIVE_API_KEY).
//
// The earlier currency-api/jsDelivr shim has been retired — that
// feed was daily-grain only, which made `change_1h_pct` /
// `change_24h_pct` on /v1/currencies impossible to compute as
// a real rolling-window. Massive serves hourly aggregates so the
// per-window change pcts are honest.
//
// Wire shape on /v1/currencies stays source-agnostic: this package
// exposes the same Snapshot / History7d / Currency / HistoryPoint
// types, so swapping providers later is a one-package change with
// no API or schema migrations.
package forex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// MassiveBase is the Massive REST API root. Same URL Polygon.io
// uses since the two are the same backend (Massive is Polygon's
// rebrand). Any Polygon documentation under /v2/aggs/... and
// /v3/reference/... applies verbatim.
const MassiveBase = "https://api.massive.com"

// fetchTimeout caps a single upstream call. Massive's edge typically
// responds in 100–300 ms; 10 s is generous and keeps a hung TCP
// from stalling the worker indefinitely.
const fetchTimeout = 10 * time.Second

// Client wraps the Massive REST endpoints we consume. Auth is
// Bearer-token; the same key works against api.polygon.io if the
// operator wants to point elsewhere.
type Client struct {
	http   *http.Client
	base   string
	apiKey string
}

// NewClient returns a [Client] hitting [MassiveBase] with the
// supplied API key. Empty key is allowed at construction time so
// `make build` doesn't require the secret; callers that need to
// fetch must check ApiKey() first or trust upstream 401s.
func NewClient(apiKey string) *Client {
	return &Client{
		http:   &http.Client{Timeout: fetchTimeout},
		base:   MassiveBase,
		apiKey: apiKey,
	}
}

// WithBase returns a copy of c with the base URL overridden — for
// tests pointing at an httptest.Server.
func (c *Client) WithBase(base string) *Client {
	cp := *c
	cp.base = base
	return &cp
}

// HasAPIKey reports whether the client was constructed with a
// non-empty API key. Callers can use this to skip the worker
// altogether when running unconfigured (e.g. local dev without
// MASSIVE_API_KEY exported).
func (c *Client) HasAPIKey() bool { return c.apiKey != "" }

// LatestUSDRates returns the current USD-base rate map plus the
// publication date the upstream stamped on the snapshot. Map keys
// are lower-case ISO-4217 currency codes (eur, jpy, gbp, …);
// values are the price of 1 USD denominated in that currency
// (e.g. usd→eur ≈ 0.92 means 1 USD buys 0.92 EUR).
//
// Backed by /v2/aggs/grouped/.../fx/{date} — one HTTP call returns
// the closing aggregate for every fx pair Massive tracks for the
// given date. We filter to USD-base tickers (`C:USD<TKR>`) and
// extract the close price.
func (c *Client) LatestUSDRates(ctx context.Context) (map[string]float64, time.Time, error) {
	// "Latest" is the most recent UTC date with a published bar.
	// We try today first; if 0 results (markets closed weekend /
	// holiday) walk back up to 4 days for the prior session.
	now := time.Now().UTC()
	for offset := 0; offset < 5; offset++ {
		date := now.AddDate(0, 0, -offset).Format("2006-01-02")
		rates, err := c.usdRatesAtDate(ctx, date)
		if err != nil {
			return nil, time.Time{}, err
		}
		if len(rates) > 0 {
			pub, _ := time.Parse("2006-01-02", date)
			return rates, pub, nil
		}
	}
	return nil, time.Time{}, fmt.Errorf("massive: no fx aggregates published in last 5 days")
}

// HistoricalUSDRates returns the USD-base rate map as of a specific
// date. `date` should be in YYYY-MM-DD form.
//
// Returns the same shape as [LatestUSDRates]. Days with no published
// fx bar (weekends, holidays) return an empty map + nil error so
// the worker can skip the day cleanly.
func (c *Client) HistoricalUSDRates(ctx context.Context, date string) (map[string]float64, time.Time, error) {
	if date == "" {
		return nil, time.Time{}, fmt.Errorf("date is required")
	}
	rates, err := c.usdRatesAtDate(ctx, date)
	if err != nil {
		return nil, time.Time{}, err
	}
	pub, _ := time.Parse("2006-01-02", date)
	return rates, pub, nil
}

// usdRatesAtDate hits the grouped-daily endpoint for the given UTC
// date and returns a lower-case-ticker → close-price map, filtered
// to USD-base pairs.
func (c *Client) usdRatesAtDate(ctx context.Context, date string) (map[string]float64, error) {
	url := fmt.Sprintf("%s/v2/aggs/grouped/locale/global/market/fx/%s", c.base, date)
	body, err := c.get(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", url, err)
	}
	var raw struct {
		ResultsCount int               `json:"resultsCount"`
		Results      []json.RawMessage `json:"results"`
		Status       string            `json:"status"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decode %s: %w", url, err)
	}
	// Decode rows one at a time. Massive occasionally emits a row
	// with a numeric/null T or non-float c for half-listed pairs;
	// a strict batch unmarshal would discard the whole 1200-row
	// snapshot for one bad entry.
	out := make(map[string]float64, len(raw.Results))
	const usdPrefix = "C:USD"
	for _, rowRaw := range raw.Results {
		var r struct {
			T string  `json:"T"`
			C float64 `json:"c"`
		}
		if err := json.Unmarshal(rowRaw, &r); err != nil {
			continue
		}
		if !strings.HasPrefix(r.T, usdPrefix) || r.C <= 0 {
			continue
		}
		ticker := strings.ToLower(strings.TrimPrefix(r.T, usdPrefix))
		out[ticker] = r.C
	}
	return out, nil
}

// CurrencyNames returns the ticker→display-name map (lower-case
// ticker keys → human-readable names). Used to render the "Name"
// column on /currencies. Massive's reference endpoint paginates;
// this method walks every page to assemble the full universe
// (caps at 5000 entries — orders of magnitude above the actual
// ~200 currencies but defensive against pathological pagination).
func (c *Client) CurrencyNames(ctx context.Context) (map[string]string, error) {
	const limit = 1000
	url := fmt.Sprintf("%s/v3/reference/tickers?market=fx&limit=%d&active=true", c.base, limit)
	out := make(map[string]string, 256)
	const maxPages = 5
	for page := 0; page < maxPages && url != ""; page++ {
		body, err := c.get(ctx, url)
		if err != nil {
			return nil, fmt.Errorf("fetch %s: %w", url, err)
		}
		var raw struct {
			Results []struct {
				Ticker             string `json:"ticker"`
				BaseCurrencySymbol string `json:"base_currency_symbol"`
				BaseCurrencyName   string `json:"base_currency_name"`
				CurrencySymbol     string `json:"currency_symbol"`
				CurrencyName       string `json:"currency_name"`
			} `json:"results"`
			NextURL string `json:"next_url"`
		}
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, fmt.Errorf("decode %s: %w", url, err)
		}
		// Each ticker is a pair like C:USDEUR which carries both
		// base + quote names. Index both sides so a one-shot pass
		// fills out our ticker→name map regardless of which side
		// USD is on.
		for _, r := range raw.Results {
			if r.BaseCurrencySymbol != "" && r.BaseCurrencyName != "" {
				out[strings.ToLower(r.BaseCurrencySymbol)] = r.BaseCurrencyName
			}
			if r.CurrencySymbol != "" && r.CurrencyName != "" {
				out[strings.ToLower(r.CurrencySymbol)] = r.CurrencyName
			}
		}
		url = raw.NextURL
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("massive: empty currency-name map")
	}
	return out, nil
}

func (c *Client) get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	const maxBody = 16 << 20 // 16 MiB — grouped FX is ~500 KB; tickers ref is ~1 MB
	buf := make([]byte, 0, 64<<10)
	tmp := make([]byte, 32<<10)
	for {
		n, rerr := resp.Body.Read(tmp)
		if n > 0 {
			if len(buf)+n > maxBody {
				return nil, fmt.Errorf("response exceeds %d bytes", maxBody)
			}
			buf = append(buf, tmp[:n]...)
		}
		if rerr != nil {
			if errIsEOF(rerr) {
				break
			}
			return nil, rerr
		}
	}
	return buf, nil
}

func errIsEOF(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "EOF")
}
