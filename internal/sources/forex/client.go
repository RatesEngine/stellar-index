// Package forex provides a free fiat-currency rates feed for the
// /v1/currencies surface. Source: Fawaz Ahmed's currency-api
// hosted on jsDelivr — daily-updated, 200+ currencies, no API key,
// MIT-licensed, ECB / FRBNY / open exchange rates aggregated
// upstream.
//
// This is the unblocked-now implementation while the operator
// negotiates a paid forex feed (massive.com or equivalent). The
// wire shape on /v1/currencies is designed to be source-agnostic
// — swapping in a paid feed is a one-package change with no API
// or schema migrations.
package forex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// CurrencyAPIBase is the jsDelivr-hosted base. The `@latest` tag
// rolls forward; the API contract has been stable for years per
// the upstream README. See https://github.com/fawazahmed0/exchange-api.
const CurrencyAPIBase = "https://cdn.jsdelivr.net/npm/@fawazahmed0/currency-api@latest/v1"

// fetchTimeout caps a single upstream call. The CDN is fast (<200ms
// global) so 10s is generous; we just don't want a hung TCP to stall
// the worker indefinitely.
const fetchTimeout = 10 * time.Second

// Client wraps the currency-api endpoints we consume. Holds a
// dedicated *http.Client so the timeout policy is independent of
// the rest of the binary's HTTP usage.
type Client struct {
	http *http.Client
	base string
}

// NewClient returns a [Client] with the default timeout + the
// pinned [CurrencyAPIBase]. base override is intended for tests
// (point at an httptest.Server). Production code should use the
// zero-arg form of NewClient.
func NewClient() *Client {
	return &Client{
		http: &http.Client{Timeout: fetchTimeout},
		base: CurrencyAPIBase,
	}
}

// WithBase returns a copy of c with the base URL overridden — for
// tests or alternative mirrors.
func (c *Client) WithBase(base string) *Client {
	cp := *c
	cp.base = base
	return &cp
}

// LatestUSDRates returns the current USD-base rate map plus the
// publication date the upstream stamped on the snapshot. Map keys
// are lower-case ISO-4217 currency codes (eur, jpy, gbp, …);
// values are the price of 1 USD denominated in that currency
// (e.g. usd→eur ≈ 0.92 means 1 USD buys 0.92 EUR).
func (c *Client) LatestUSDRates(ctx context.Context) (map[string]float64, time.Time, error) {
	url := c.base + "/currencies/usd.min.json"
	body, err := c.get(ctx, url)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("fetch %s: %w", url, err)
	}
	var raw struct {
		Date string             `json:"date"`
		USD  map[string]float64 `json:"usd"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, time.Time{}, fmt.Errorf("decode %s: %w", url, err)
	}
	if len(raw.USD) == 0 {
		return nil, time.Time{}, fmt.Errorf("upstream %s: empty rates map", url)
	}
	publishedAt, err := time.Parse("2006-01-02", raw.Date)
	if err != nil {
		// Tolerate undated snapshots — surface "unknown" via zero
		// time rather than fail the whole fetch.
		publishedAt = time.Time{}
	}
	return raw.USD, publishedAt, nil
}

// CurrencyNames returns the ticker→display-name map (lower-case
// ticker keys → name strings). Used to render the "Name" column on
// /currencies. Cached forever in practice — names don't change.
func (c *Client) CurrencyNames(ctx context.Context) (map[string]string, error) {
	url := c.base + "/currencies.min.json"
	body, err := c.get(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", url, err)
	}
	var out map[string]string
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode %s: %w", url, err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("upstream %s: empty names map", url)
	}
	return out, nil
}

func (c *Client) get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	const maxBody = 4 << 20 // 4 MiB — full currencies.min.json is ~25 KB
	buf := make([]byte, 0, 32<<10)
	tmp := make([]byte, 16<<10)
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
