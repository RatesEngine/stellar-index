package marketcap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/currency"
)

// Default CG endpoint + path. Free tier — no auth required for
// /simple/price up to ~30 calls/min. We batch every catalogue entry
// into one request per refresh tick so the rate budget is generous.
const (
	defaultEndpoint     = "https://api.coingecko.com"
	simplePricePath     = "/api/v3/simple/price"
	defaultRefreshEvery = 5 * time.Minute
	defaultHTTPTimeout  = 30 * time.Second
)

// Catalogue is the read-only slice of the verified-currency catalogue
// the refresher needs. *currency.Catalogue satisfies it via All().
type Catalogue interface {
	All() []*currency.VerifiedCurrency
}

// Refresher polls CoinGecko's /simple/price endpoint on a fixed
// cadence and updates a Cache in place. One goroutine for the
// lifetime of the binary; stops on ctx.Done().
//
// Failure mode: a 4xx/5xx response or network error logs at Warn
// and skips the tick — the cache retains the prior snapshots
// indefinitely. On 429 (rate-limited) and 403 (demo-key-required
// post-2024), the refresher applies the Retry-After header value
// (or an exponential fallback up to 30 min) before the next call.
// Operators wanting authoritative freshness should pair this with
// a stale-snapshot alert.
type Refresher struct {
	Cache    *Cache
	Cat      Catalogue
	Logger   *slog.Logger
	Endpoint string        // defaults to https://api.coingecko.com
	Every    time.Duration // defaults to 5m
	APIKey   string        // optional Pro tier key
	DemoKey  string        // optional Demo tier key
	// HTTPClient defaults to a 30s timeout client built per-call;
	// override for tests.
	HTTPClient *http.Client

	mu               sync.Mutex
	currentBackoff   time.Duration
	nextEligibleCall time.Time
}

const (
	minBackoff = 5 * time.Minute
	maxBackoff = 30 * time.Minute
)

// Run drives the refresh loop. Fires once at start so the cache
// has values for the first user request, then on Every cadence.
// Returns nil when ctx is cancelled. Safe to call from a goroutine.
func (r *Refresher) Run(ctx context.Context) error {
	r.refresh(ctx)
	cadence := r.Every
	if cadence <= 0 {
		cadence = defaultRefreshEvery
	}
	t := time.NewTicker(cadence)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			r.refresh(ctx)
		}
	}
}

// refresh executes a single CG batch fetch and writes results to
// the cache. Idempotent on repeated calls (always overwrites).
// No-op when a prior 429/403 backoff is still in effect.
func (r *Refresher) refresh(ctx context.Context) {
	if r.Cat == nil || r.Cache == nil {
		return
	}
	if r.inBackoff() {
		return
	}
	ids, slugByID := r.cgIDsAndSlugs()
	if len(ids) == 0 {
		return
	}
	resp, err := r.fetch(ctx, ids)
	if err != nil {
		if r.Logger != nil {
			r.Logger.Warn("market_cap refresh failed", "err", err, "ids", len(ids))
		}
		return
	}
	r.clearBackoff()
	updated := r.applyResponse(resp, slugByID)
	if r.Logger != nil {
		r.Logger.Debug("market_cap refresh", "fetched", updated, "ids", len(ids))
	}
}

// inBackoff reports whether a prior 429/403 has set a future
// nextEligibleCall. Logs at Debug when skipping. Pulled out of
// refresh to keep that function under the gocognit ceiling.
func (r *Refresher) inBackoff() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if time.Now().Before(r.nextEligibleCall) {
		if r.Logger != nil {
			r.Logger.Debug("market_cap refresh skipped (in backoff)",
				"remaining", time.Until(r.nextEligibleCall).Truncate(time.Second))
		}
		return true
	}
	return false
}

// clearBackoff resets the backoff state on a successful fetch so
// the next 429 starts fresh from minBackoff rather than wherever
// we'd grown to.
func (r *Refresher) clearBackoff() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.currentBackoff = 0
	r.nextEligibleCall = time.Time{}
}

// applyResponse projects the CG response back into the Snapshot
// shape and writes each populated row to the cache. Returns the
// number of rows updated. Pulled out of refresh to keep the
// scanning loop's branches below the gocognit ceiling.
func (r *Refresher) applyResponse(resp simplePriceResponse, slugByID map[string]string) int {
	fetchedAt := time.Now().UTC()
	updated := 0
	for cgID, fields := range resp {
		slug, ok := slugByID[cgID]
		if !ok {
			continue
		}
		snap := snapshotFromFields(fields, fetchedAt)
		if snap.empty() {
			continue
		}
		r.Cache.Store(slug, snap)
		updated++
	}
	return updated
}

// snapshotFromFields projects CG's nested map[string]float64 into
// our Snapshot wire shape. Empty fields stay empty.
func snapshotFromFields(fields map[string]float64, fetchedAt time.Time) Snapshot {
	snap := Snapshot{FetchedAt: fetchedAt}
	if v, ok := fields["usd"]; ok && v > 0 {
		snap.PriceUSD = strconv.FormatFloat(v, 'f', -1, 64)
	}
	if v, ok := fields["usd_market_cap"]; ok && v > 0 {
		snap.MarketCapUSD = strconv.FormatFloat(v, 'f', 2, 64)
	}
	if v, ok := fields["usd_24h_change"]; ok {
		snap.Change24hPct = strconv.FormatFloat(v, 'f', 2, 64)
	}
	return snap
}

func (s Snapshot) empty() bool {
	return s.PriceUSD == "" && s.MarketCapUSD == "" && s.Change24hPct == ""
}

// cgIDsAndSlugs walks the catalogue and returns:
//   - ids: every non-empty coingecko_id, deduped
//   - slugByID: reverse map (cg_id → catalogue slug) for projecting
//     the response back to slug-keyed cache writes
//
// Only crypto + stablecoin classes are included. Fiat catalogue
// entries already get market_cap via the M2-×-FX path in
// assets_global.go and don't need a CG round-trip.
func (r *Refresher) cgIDsAndSlugs() ([]string, map[string]string) {
	seen := make(map[string]struct{})
	slugByID := make(map[string]string, 32)
	ids := make([]string, 0, 32)
	for _, vc := range r.Cat.All() {
		if vc.CoinGeckoID == "" {
			continue
		}
		if vc.Class != currency.ClassCrypto && vc.Class != currency.ClassStablecoin {
			continue
		}
		if _, dup := seen[vc.CoinGeckoID]; dup {
			continue
		}
		seen[vc.CoinGeckoID] = struct{}{}
		ids = append(ids, vc.CoinGeckoID)
		slugByID[vc.CoinGeckoID] = vc.Slug
	}
	return ids, slugByID
}

type simplePriceResponse map[string]map[string]float64

// fetch hits /simple/price with include_market_cap + include_24hr_change.
// Returns the raw nested-map response on 2xx; otherwise an error
// (refresh() logs + skips the tick).
func (r *Refresher) fetch(ctx context.Context, ids []string) (simplePriceResponse, error) {
	endpoint := r.Endpoint
	if endpoint == "" {
		endpoint = defaultEndpoint
	}
	q := url.Values{}
	q.Set("ids", strings.Join(ids, ","))
	q.Set("vs_currencies", "usd")
	q.Set("include_market_cap", "true")
	q.Set("include_24hr_change", "true")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+simplePricePath+"?"+q.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	// SECURITY (F-1337): pass the CoinGecko key via REQUEST HEADER,
	// not a URL query param. As a query param (?x_cg_pro_api_key=…)
	// the secret ends up embedded in the request URL, which leaks
	// verbatim through *url.Error on any transport/timeout failure
	// ("...?x_cg_pro_api_key=SECRET: context deadline exceeded") and
	// into any access log that records the full URL. CoinGecko
	// accepts the same keys as headers. Pro wins over Demo when both
	// are set (matches the prior query-param precedence).
	if r.APIKey != "" {
		req.Header.Set("x-cg-pro-api-key", r.APIKey)
	} else if r.DemoKey != "" {
		req.Header.Set("x-cg-demo-api-key", r.DemoKey)
	}

	client := r.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: defaultHTTPTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusForbidden {
		wait := r.backoffFromRetryAfter(resp.Header.Get("Retry-After"))
		r.applyBackoff(wait)
		return nil, fmt.Errorf("http %d (rate-limited — backing off %s): %s",
			resp.StatusCode, wait.Truncate(time.Second), truncate(string(body), 200))
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	var parsed simplePriceResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	if len(parsed) == 0 {
		return nil, errors.New("empty response")
	}
	return parsed, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// backoffFromRetryAfter parses CoinGecko's Retry-After response
// header. Accepts seconds-since-now (integer) or an HTTP-date.
// Clamps to [minBackoff, maxBackoff]. Empty header → doubles the
// current backoff (or minBackoff on first failure).
func (r *Refresher) backoffFromRetryAfter(header string) time.Duration {
	if header != "" {
		if secs, err := strconv.Atoi(strings.TrimSpace(header)); err == nil && secs > 0 {
			d := time.Duration(secs) * time.Second
			return clampBackoff(d)
		}
		if t, err := http.ParseTime(header); err == nil {
			d := time.Until(t)
			if d > 0 {
				return clampBackoff(d)
			}
		}
	}
	// No / unparseable header — exponential fallback.
	r.mu.Lock()
	cur := r.currentBackoff
	r.mu.Unlock()
	if cur == 0 {
		return minBackoff
	}
	doubled := cur * 2
	return clampBackoff(doubled)
}

// applyBackoff sets the in-memory deadline + remembers the current
// backoff for the next exponential step.
func (r *Refresher) applyBackoff(d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.currentBackoff = d
	r.nextEligibleCall = time.Now().Add(d)
}

func clampBackoff(d time.Duration) time.Duration {
	if d < minBackoff {
		return minBackoff
	}
	if d > maxBackoff {
		return maxBackoff
	}
	return d
}
