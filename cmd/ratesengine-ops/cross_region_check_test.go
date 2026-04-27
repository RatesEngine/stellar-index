package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// stubServer returns a regions-style API that always answers with
// the supplied response payload. Used to simulate per-region API
// responses in unit tests without needing the full ratesengine-api
// stack.
func stubServer(t *testing.T, body any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": body})
	}))
}

// stubServerStatus answers with a fixed HTTP status + body.
func stubServerStatus(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

// TestAnalyseRegionResults_AllAgree is the happy path: every region
// returned the same price for the same closed bucket. analyse should
// emit "OK" and report no divergence.
func TestAnalyseRegionResults_AllAgree(t *testing.T) {
	from := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	to := from.Add(30 * time.Second)
	resp := &crossRegionResponse{From: from, To: to, Price: "0.1234567890"}
	results := []regionResult{
		{Region: "r1", Response: resp},
		{Region: "r2", Response: resp},
		{Region: "r3", Response: resp},
	}
	var out bytes.Buffer
	div := analyseRegionResults(metricVWAP, "native/fiat:USD", from, to, results, &out)
	if div {
		t.Fatalf("divergence flagged when all regions agreed; out=%s", out.String())
	}
	if !strings.HasPrefix(out.String(), "OK") {
		t.Errorf("expected OK line, got: %s", out.String())
	}
}

// TestAnalyseRegionResults_PriceDisagreement is the failure case:
// r2 returns a different price. analyse must emit a DIVERGENCE line
// with both values and return true.
func TestAnalyseRegionResults_PriceDisagreement(t *testing.T) {
	from := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	to := from.Add(30 * time.Second)
	results := []regionResult{
		{Region: "r1", Response: &crossRegionResponse{From: from, To: to, Price: "0.1234567890"}},
		{Region: "r2", Response: &crossRegionResponse{From: from, To: to, Price: "0.1234567899"}},
		{Region: "r3", Response: &crossRegionResponse{From: from, To: to, Price: "0.1234567890"}},
	}
	var out bytes.Buffer
	div := analyseRegionResults(metricVWAP, "native/fiat:USD", from, to, results, &out)
	if !div {
		t.Fatalf("divergence not flagged; out=%s", out.String())
	}
	body := out.String()
	if !strings.Contains(body, "DIVERGENCE") {
		t.Errorf("missing DIVERGENCE marker: %s", body)
	}
	if !strings.Contains(body, "r1=") || !strings.Contains(body, "r2=") || !strings.Contains(body, "r3=") {
		t.Errorf("expected per-region values in diff: %s", body)
	}
	if !strings.Contains(body, "0.1234567890") || !strings.Contains(body, "0.1234567899") {
		t.Errorf("expected both prices in diff: %s", body)
	}
}

// TestAnalyseRegionResults_FetchErrorTolerated covers the partial-
// failure case: one region's HTTP fetch failed (Err != nil). We
// should NOT flag divergence on that alone — it's reported as ERR
// but the comparison only runs across the regions we actually
// reached.
func TestAnalyseRegionResults_FetchErrorTolerated(t *testing.T) {
	from := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	to := from.Add(30 * time.Second)
	resp := &crossRegionResponse{From: from, To: to, Price: "1.00"}
	results := []regionResult{
		{Region: "r1", Response: resp},
		{Region: "r2", Err: fmt.Errorf("connection refused")},
		{Region: "r3", Response: resp},
	}
	var out bytes.Buffer
	div := analyseRegionResults(metricVWAP, "native/fiat:USD", from, to, results, &out)
	if div {
		t.Fatalf("divergence flagged on a partial fetch error; out=%s", out.String())
	}
	body := out.String()
	if !strings.Contains(body, "ERR") {
		t.Errorf("expected ERR line for r2: %s", body)
	}
	// r1 + r3 still agree — should also have OK
	if !strings.Contains(body, "OK") {
		t.Errorf("expected OK line for the agreeing regions: %s", body)
	}
}

// TestAnalyseRegionResults_OHLCAllFieldsCompared confirms that for
// the OHLC metric, we compare open/high/low/close in addition to
// price. A subtle divergence in `high` alone should still flag.
func TestAnalyseRegionResults_OHLCAllFieldsCompared(t *testing.T) {
	from := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	to := from.Add(30 * time.Second)
	results := []regionResult{
		{Region: "r1", Response: &crossRegionResponse{
			From: from, To: to, Open: "1.00", High: "1.05", Low: "0.95", Close: "1.02",
		}},
		{Region: "r2", Response: &crossRegionResponse{
			From: from, To: to, Open: "1.00", High: "1.06", Low: "0.95", Close: "1.02",
		}},
	}
	var out bytes.Buffer
	div := analyseRegionResults(metricOHLC, "native/fiat:USD", from, to, results, &out)
	if !div {
		t.Fatalf("OHLC high-field divergence not flagged; out=%s", out.String())
	}
	if !strings.Contains(out.String(), "high:") {
		t.Errorf("expected 'high:' in diff output: %s", out.String())
	}
}

// TestFetchOneRegion_RoundTrip confirms the HTTP path works against
// an httptest server using a wrapped envelope (matching the real
// API's {"data": ...} shape).
func TestFetchOneRegion_RoundTrip(t *testing.T) {
	from := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	to := from.Add(30 * time.Second)
	want := crossRegionResponse{From: from, To: to, Price: "1.23"}

	srv := stubServer(t, want)
	defer srv.Close()

	got, err := fetchOneRegion(t.Context(),
		&http.Client{Timeout: 5 * time.Second},
		regionEndpoint{name: "r-test", base: srv.URL},
		"native/fiat:USD", metricVWAP, from, to)
	if err != nil {
		t.Fatalf("fetch failed: %v", err)
	}
	if got.Price != want.Price {
		t.Errorf("price = %q, want %q", got.Price, want.Price)
	}
	if !got.From.Equal(want.From) || !got.To.Equal(want.To) {
		t.Errorf("from/to mismatch: got %v..%v want %v..%v",
			got.From, got.To, want.From, want.To)
	}
}

// TestFetchOneRegion_HTTPErrorPropagates confirms a non-200 status
// surfaces as a clear error (not silently treated as success).
func TestFetchOneRegion_HTTPErrorPropagates(t *testing.T) {
	srv := stubServerStatus(t, 500, `{"error":"backend down"}`)
	defer srv.Close()

	_, err := fetchOneRegion(t.Context(),
		&http.Client{Timeout: 5 * time.Second},
		regionEndpoint{name: "r-test", base: srv.URL},
		"native/fiat:USD", metricVWAP,
		time.Now().Add(-time.Minute), time.Now())
	if err == nil {
		t.Fatal("expected error on HTTP 500, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error should mention status code: %v", err)
	}
}

// TestParseRegionList covers the CLI input parsing.
func TestParseRegionList(t *testing.T) {
	for _, tc := range []struct {
		name    string
		input   string
		want    int
		wantErr bool
	}{
		{"empty", "", 0, false},
		{"single", "r1=https://r1.example.net", 1, false},
		{"multi", "r1=https://r1.example.net,r2=https://r2.example.net", 2, false},
		{"trailing spaces", " r1=https://r1.example.net , r2=https://r2.example.net ", 2, false},
		{"missing equals", "r1https://r1.example.net", 0, true},
		{"missing url", "r1=", 0, true},
		{"missing name", "=https://r1.example.net", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseRegionList(tc.input)
			if tc.wantErr && err == nil {
				t.Errorf("expected error, got %d entries", len(got))
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if !tc.wantErr && len(got) != tc.want {
				t.Errorf("entries = %d, want %d", len(got), tc.want)
			}
		})
	}
}

// TestResolveAnchor confirms the default anchor lands on a window
// boundary AND is one window in the past (so we sample CLOSED
// buckets, not the in-progress one).
func TestResolveAnchor(t *testing.T) {
	got, err := resolveAnchor("", 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if got.UnixNano()%(30*time.Second).Nanoseconds() != 0 {
		t.Errorf("anchor not on window boundary: %s", got)
	}
	// Should be ≥ 30 s in the past (bucket fully closed) and within
	// 60 s (otherwise we'd be sampling stale data unnecessarily).
	delta := now.Sub(got)
	if delta < 30*time.Second {
		t.Errorf("anchor too recent: now-anchor=%s, want >= 30s", delta)
	}
	if delta > 60*time.Second {
		t.Errorf("anchor too far back: now-anchor=%s, want < 60s", delta)
	}
}

// TestSplitPair covers pair parsing — base/quote with various
// canonical forms.
func TestSplitPair(t *testing.T) {
	for _, tc := range []struct {
		input     string
		wantBase  string
		wantQuote string
		wantErr   bool
	}{
		{"native/fiat:USD", "native", "fiat:USD", false},
		{"crypto:XLM/fiat:USD", "crypto:XLM", "fiat:USD", false},
		{"USDC-G.../native", "USDC-G...", "native", false},
		{"missing-slash", "", "", true},
		{"/empty-base", "", "", true},
		{"empty-quote/", "", "", true},
	} {
		base, quote, err := splitPair(tc.input)
		if tc.wantErr && err == nil {
			t.Errorf("%q: expected error", tc.input)
		}
		if !tc.wantErr {
			if base != tc.wantBase || quote != tc.wantQuote {
				t.Errorf("%q: got (%q, %q), want (%q, %q)",
					tc.input, base, quote, tc.wantBase, tc.wantQuote)
			}
		}
	}
}
