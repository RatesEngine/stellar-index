package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// ─── cross-region-check ─────────────────────────────────────────
//
// Queries each configured region's /v1/vwap (or /v1/twap, /v1/ohlc)
// for the same closed-bucket window and asserts the responses are
// equivalent on every stable user-visible field in the response
// contract.
//
// Per ADR-0015, closed-bucket aggregations are deterministic given
// the same trade inputs — once postgres replication has carried
// trades to all regions, every region computes the same VWAP for
// the same [from, to) window. Divergence here means one of:
//
//   1. Replication lag: a region hasn't caught up yet (transient,
//      should self-resolve within seconds-to-minutes).
//   2. Decoder version drift: regions disagree on what trades
//      exist for the window because they're running different
//      decoder logic against the same upstream bytes.
//   3. Upstream divergence: a region is reading a different
//      ledger-meta source that disagrees with the others (caught
//      by Tier D periodically, but this cross-region check finds
//      it faster via the indexer-output side effect).
//   4. Postgres replication broken: a region's trade-row corpus
//      genuinely differs.
//
// The tool is intentionally conservative — it samples a few recent
// closed buckets and asserts ALL regions agree on each. Any
// disagreement exits non-zero with a structured diff for ops to
// triage.
//
// Foundation for the periodic monitoring job that runs on the
// observability box (see docs/architecture/ha-plan.md §3.6 once
// that lands).

type crossRegionMetric string

const (
	metricVWAP crossRegionMetric = "vwap"
	metricTWAP crossRegionMetric = "twap"
	metricOHLC crossRegionMetric = "ohlc"
)

// crossRegionResponse is the shape we extract from each region's
// rate endpoint. We don't unmarshal every possible envelope member
// (for example `as_of` is intentionally excluded because handlers
// stamp it at response time), but we do extract every stable
// user-visible field that ADR-0015 expects to agree cross-region.
type crossRegionResponse struct {
	From    time.Time        `json:"from"`
	To      time.Time        `json:"to"`
	Price   string           `json:"price"`
	Sources []string         `json:"sources,omitempty"`
	Flags   crossRegionFlags `json:"flags"`
	// The discriminator fields below differ by endpoint. We keep
	// them in the same struct so a single comparison loop covers
	// vwap / twap / ohlc.
	Open             string `json:"open,omitempty"`  // OHLC only
	High             string `json:"high,omitempty"`  // OHLC only
	Low              string `json:"low,omitempty"`   // OHLC only
	Close            string `json:"close,omitempty"` // OHLC only
	BaseVolume       string `json:"base_volume,omitempty"`
	QuoteVolume      string `json:"quote_volume,omitempty"`
	TradeCount       int    `json:"trade_count,omitempty"`
	OutliersFiltered int    `json:"outliers_filtered,omitempty"` // VWAP only
	Truncated        bool   `json:"truncated,omitempty"`
}

type crossRegionFlags struct {
	Stale             bool `json:"stale"`
	ReducedRedundancy bool `json:"reduced_redundancy"`
	Triangulated      bool `json:"triangulated"`
	DivergenceWarning bool `json:"divergence_warning"`
	Frozen            bool `json:"frozen,omitempty"`
	SingleSource      bool `json:"single_source,omitempty"`
}

// regionResult pairs the region's name with what we got back from it.
type regionResult struct {
	Region   string
	Response *crossRegionResponse // nil when fetch failed
	Err      error
}

func crossRegionCheck(args []string) error { //nolint:funlen,gocognit,gocyclo // linear diagnostic; splitting reduces readability
	fs := flag.NewFlagSet("cross-region-check", flag.ContinueOnError)
	regionsCSV := fs.String("regions", "",
		"Comma-separated `name=url` pairs, e.g. r1=https://r1.example.net,r2=https://r2.example.net (required)")
	pairsCSV := fs.String("pairs", "native/fiat:USD",
		"Comma-separated canonical pair strings to check (default: XLM/USD)")
	metric := fs.String("metric", "vwap",
		"Which endpoint to compare across regions: vwap, twap, ohlc")
	window := fs.Duration("window", 30*time.Second,
		"Closed-bucket window size to sample. The check walks back from `to` in window-sized steps.")
	samples := fs.Int("samples", 3,
		"How many recent closed buckets to compare across regions")
	timeout := fs.Duration("timeout", 10*time.Second,
		"Per-region HTTP request timeout")
	to := fs.String("to", "",
		"Optional anchor for the most-recent bucket boundary (RFC 3339). Defaults to now-1*window so we always sample CLOSED buckets even if a region's clock is slightly ahead.")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *regionsCSV == "" {
		return fmt.Errorf("-regions is required (e.g. r1=https://r1.example.net,r2=https://r2.example.net)")
	}
	if *samples < 1 {
		return fmt.Errorf("-samples must be >= 1")
	}

	regions, err := parseRegionList(*regionsCSV)
	if err != nil {
		return err
	}
	if len(regions) < 2 {
		// F-1234 (audit-2026-05-12): pre-R2/R3-bringup posture has
		// only R1 deployed; treating that as an error trains
		// operators to ignore the command. Return nil so the
		// pre-launch smoke flow can call this unconditionally and
		// get a meaningful no-op until multi-region lands.
		_, _ = fmt.Fprintf(os.Stdout,
			"cross-region-check: only %d region configured; consistency check needs ≥ 2 regions.\n"+
				"This is the pre-launch posture (R2/R3 not yet deployed). Returning success.\n"+
				"Track R2/R3 bringup in docs/architecture/r2-r3-bringup.md.\n",
			len(regions))
		return nil
	}

	pairs := splitCSV(*pairsCSV)
	if len(pairs) == 0 {
		return fmt.Errorf("-pairs parsed to empty list")
	}

	metricKind := crossRegionMetric(strings.ToLower(*metric))
	switch metricKind {
	case metricVWAP, metricTWAP, metricOHLC:
	default:
		return fmt.Errorf("-metric must be one of vwap|twap|ohlc; got %q", *metric)
	}

	anchor, err := resolveAnchor(*to, *window)
	if err != nil {
		return err
	}

	httpClient := &http.Client{Timeout: *timeout}

	var totalDivergences int
	for _, pair := range pairs {
		for i := 0; i < *samples; i++ {
			// Bucket [n] is [anchor - (i+1)*window, anchor - i*window).
			bucketTo := anchor.Add(-time.Duration(i) * *window)
			bucketFrom := bucketTo.Add(-*window)

			results := fetchAllRegions(context.Background(), httpClient,
				regions, pair, metricKind, bucketFrom, bucketTo)

			if div := analyseRegionResults(metricKind, pair, bucketFrom, bucketTo, results, os.Stdout); div {
				totalDivergences++
			}
		}
	}

	if totalDivergences > 0 {
		return fmt.Errorf("found %d divergence(s) across %d region(s) — see diff above",
			totalDivergences, len(regions))
	}
	_, _ = fmt.Fprintf(os.Stderr,
		"cross-region-check: OK — %d region(s) × %d pair(s) × %d sample(s), all consistent\n",
		len(regions), len(pairs), *samples)
	return nil
}

// regionEndpoint pairs a friendly name with the API base URL.
type regionEndpoint struct {
	name string
	base string
}

func parseRegionList(csv string) ([]regionEndpoint, error) {
	out := []regionEndpoint{}
	for _, kv := range splitCSV(csv) {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return nil, fmt.Errorf("region entry %q must be name=url", kv)
		}
		if _, err := url.Parse(parts[1]); err != nil {
			return nil, fmt.Errorf("region %q has invalid URL %q: %w", parts[0], parts[1], err)
		}
		out = append(out, regionEndpoint{name: parts[0], base: strings.TrimRight(parts[1], "/")})
	}
	return out, nil
}

func splitCSV(s string) []string {
	out := []string{}
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// resolveAnchor picks the upper bound of the most-recent closed bucket
// to start sampling from. Default = now() − window, truncated down
// to a window boundary. The −window step protects against clock skew
// between the local box and the regions' "now"; we always want a
// bucket that's CLOSED everywhere.
func resolveAnchor(toFlag string, window time.Duration) (time.Time, error) {
	if toFlag != "" {
		t, err := time.Parse(time.RFC3339, toFlag)
		if err != nil {
			return time.Time{}, fmt.Errorf("-to must be RFC 3339: %w", err)
		}
		return t.UTC().Truncate(window), nil
	}
	return time.Now().UTC().Add(-window).Truncate(window), nil
}

// fetchAllRegions hits every region's endpoint in parallel for the
// same (pair, from, to). Returns a slice in the same order as the
// regions input.
func fetchAllRegions(
	ctx context.Context,
	client *http.Client,
	regions []regionEndpoint,
	pair string,
	metric crossRegionMetric,
	from, to time.Time,
) []regionResult {
	results := make([]regionResult, len(regions))
	var wg sync.WaitGroup
	for i, r := range regions {
		i, r := i, r
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := fetchOneRegion(ctx, client, r, pair, metric, from, to)
			results[i] = regionResult{Region: r.name, Response: resp, Err: err}
		}()
	}
	wg.Wait()
	return results
}

func fetchOneRegion(
	ctx context.Context,
	client *http.Client,
	r regionEndpoint,
	pair string,
	metric crossRegionMetric,
	from, to time.Time,
) (*crossRegionResponse, error) {
	base, quote, err := splitPair(pair)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("base", base)
	q.Set("quote", quote)
	q.Set("from", from.Format(time.RFC3339))
	q.Set("to", to.Format(time.RFC3339))
	endpoint := fmt.Sprintf("%s/v1/%s?%s", r.base, metric, q.Encode())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpResp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer func() { _ = httpResp.Body.Close() }()
	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if httpResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d, body=%s", httpResp.StatusCode, truncate(string(body), 200))
	}

	// The API wraps payloads in an envelope: {"data": {...}, "meta": {...}}.
	// We just need the data field; tolerate both wrapped and unwrapped
	// shapes (test stubs may return unwrapped).
	var envelope struct {
		Data    crossRegionResponse `json:"data"`
		Sources []string            `json:"sources"`
		Flags   crossRegionFlags    `json:"flags"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil && !envelope.Data.From.IsZero() {
		envelope.Data.Sources = append([]string(nil), envelope.Sources...)
		envelope.Data.Flags = envelope.Flags
		return &envelope.Data, nil
	}
	var direct crossRegionResponse
	if err := json.Unmarshal(body, &direct); err != nil {
		return nil, fmt.Errorf("decode body: %w (body=%s)", err, truncate(string(body), 200))
	}
	return &direct, nil
}

// splitPair turns "native/fiat:USD" into ("native", "fiat:USD").
// Pair strings use a single forward slash separator between base
// and quote.
func splitPair(p string) (base, quote string, err error) {
	parts := strings.SplitN(p, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("pair %q must be of the form base/quote", p)
	}
	return parts[0], parts[1], nil
}

// analyseRegionResults examines a single (pair, bucket) sample
// across regions and writes a divergence report (or "OK" line) to w.
// Returns true when the regions disagreed.
//
// functions would obscure the simple "compare → report" flow.
//
//nolint:gocognit,gocyclo // linear diagnostic; splitting into smaller
func analyseRegionResults(
	metric crossRegionMetric,
	pair string,
	from, to time.Time,
	results []regionResult,
	w io.Writer,
) bool {
	// Surface fetch errors: any region we couldn't reach is a partial
	// failure. We treat fetch errors as non-divergence (operator
	// chooses whether to alert) but log them so triage can see them.
	for _, r := range results {
		if r.Err != nil {
			_, _ = fmt.Fprintf(w, "ERR  %s/%s/[%s, %s) %s: %v\n",
				metric, pair, from.Format(time.RFC3339), to.Format(time.RFC3339),
				r.Region, r.Err)
		}
	}
	// Compute the canonical "agreement" set: all the responses that
	// successfully decoded. If any of those disagree on the comparison
	// fields, that's a divergence.
	good := []regionResult{}
	for _, r := range results {
		if r.Response != nil {
			good = append(good, r)
		}
	}
	if len(good) < 2 {
		// Not enough successful regions to compare. This happens when
		// only one region is online or the data isn't replicated yet.
		// Don't flag as divergence; leave it to the caller to decide
		// based on the ERR lines above.
		return false
	}

	// Compare the stable contract fields. Equality is exact
	// byte-string/value match per ADR-0015 once the trade corpus
	// agrees; intentionally ephemeral fields such as `as_of` are not
	// part of the comparison set.
	keys := comparableKeys(metric)
	disagreements := map[string]map[string]string{} // key → region → value
	for _, k := range keys {
		seen := map[string][]string{} // value → regions reporting it
		for _, r := range good {
			v := extractField(r.Response, k)
			seen[v] = append(seen[v], r.Region)
		}
		if len(seen) > 1 {
			disagreements[k] = map[string]string{}
			for v, regions := range seen {
				for _, region := range regions {
					disagreements[k][region] = v
				}
			}
		}
	}

	if len(disagreements) == 0 {
		// Agreement among all reachable regions — emit OK regardless of
		// whether we hit fetch errors on other regions. The fetch error
		// was already reported above as ERR; OK reflects the success of
		// the comparison itself.
		_, _ = fmt.Fprintf(w, "OK   %s/%s/[%s, %s) — %d regions agree\n",
			metric, pair, from.Format(time.RFC3339), to.Format(time.RFC3339), len(good))
		return false
	}
	if len(disagreements) > 0 {
		_, _ = fmt.Fprintf(w, "DIVERGENCE  %s/%s/[%s, %s)\n",
			metric, pair, from.Format(time.RFC3339), to.Format(time.RFC3339))
		// Sorted output for stable test assertions.
		sortedKeys := []string{}
		for k := range disagreements {
			sortedKeys = append(sortedKeys, k)
		}
		sort.Strings(sortedKeys)
		for _, k := range sortedKeys {
			perRegion := disagreements[k]
			regionNames := []string{}
			for r := range perRegion {
				regionNames = append(regionNames, r)
			}
			sort.Strings(regionNames)
			parts := []string{}
			for _, r := range regionNames {
				parts = append(parts, fmt.Sprintf("%s=%q", r, perRegion[r]))
			}
			_, _ = fmt.Fprintf(w, "    %s: %s\n", k, strings.Join(parts, " "))
		}
		return true
	}
	return false
}

// comparableKeys returns the stable user-visible fields the
// divergence check should compare for the given metric.
func comparableKeys(m crossRegionMetric) []string {
	common := []string{
		"from", "to", "sources",
		"flags.stale", "flags.reduced_redundancy", "flags.triangulated",
		"flags.divergence_warning", "flags.frozen", "flags.single_source",
	}
	switch m {
	case metricOHLC:
		return append(common,
			"open", "high", "low", "close",
			"base_volume", "quote_volume", "trade_count", "truncated",
		)
	case metricVWAP, metricTWAP:
		keys := append(common, "price", "trade_count", "truncated")
		if m == metricVWAP {
			keys = append(keys, "base_volume", "quote_volume", "outliers_filtered")
		}
		return keys
	}
	return append(common, "price")
}

// extractField reads a named field's stringified value off the
// response. Used for divergence diff output.
func extractField(r *crossRegionResponse, name string) string {
	switch name {
	case "from":
		return r.From.UTC().Format(time.RFC3339Nano)
	case "to":
		return r.To.UTC().Format(time.RFC3339Nano)
	case "price":
		return r.Price
	case "open":
		return r.Open
	case "high":
		return r.High
	case "low":
		return r.Low
	case "close":
		return r.Close
	case "base_volume":
		return r.BaseVolume
	case "quote_volume":
		return r.QuoteVolume
	case "trade_count":
		return fmt.Sprintf("%d", r.TradeCount)
	case "outliers_filtered":
		return fmt.Sprintf("%d", r.OutliersFiltered)
	case "truncated":
		return fmt.Sprintf("%t", r.Truncated)
	case "sources":
		return mustJSONString(r.Sources)
	case "flags.stale":
		return fmt.Sprintf("%t", r.Flags.Stale)
	case "flags.reduced_redundancy":
		return fmt.Sprintf("%t", r.Flags.ReducedRedundancy)
	case "flags.triangulated":
		return fmt.Sprintf("%t", r.Flags.Triangulated)
	case "flags.divergence_warning":
		return fmt.Sprintf("%t", r.Flags.DivergenceWarning)
	case "flags.frozen":
		return fmt.Sprintf("%t", r.Flags.Frozen)
	case "flags.single_source":
		return fmt.Sprintf("%t", r.Flags.SingleSource)
	default:
		return ""
	}
}

func mustJSONString(v any) string {
	buf, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("<json-error:%v>", err)
	}
	return string(buf)
}

// truncate caps a string for error-message inclusion. Body dumps in
// errors should be readable but bounded. Walks back to the nearest
// UTF-8 rune boundary at or before byte n so multi-byte codepoints
// aren't sliced in half — region-diff output routinely contains
// UTF-8 (vendor error pages, asset names with accented characters).
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	end := n
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end] + "...(truncated)"
}
