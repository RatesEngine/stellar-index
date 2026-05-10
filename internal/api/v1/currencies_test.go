package v1_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	v1 "github.com/RatesEngine/rates-engine/internal/api/v1"
)

// stubCurrenciesReader (snap field) is shared with price_test.go —
// defined there once for the package's tests. We just construct
// instances here.

// TestHandleCurrencies_NilReader returns the payload skeleton — keeps
// the wire shape stable so the explorer's /currencies page can
// render its empty state via the same JSON-decode path it uses on
// the populated case.
func TestHandleCurrencies_NilReader(t *testing.T) {
	srv := v1.New(v1.Options{})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/currencies")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := readAll(resp)
	for _, want := range []string{`"source":"massive"`, `"data":{`} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q: %s", want, body)
		}
	}
}

// TestHandleCurrencies_NilSnapshot — same skeleton when the reader
// is wired but its first fetch hasn't completed (warming up).
func TestHandleCurrencies_NilSnapshot(t *testing.T) {
	srv := v1.New(v1.Options{Currencies: &stubCurrenciesReader{snap: nil}})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/currencies")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := readAll(resp)
	if !strings.Contains(body, `"source":"massive"`) {
		t.Errorf("missing source pin: %s", body)
	}
}

// TestHandleCurrencies_HappyPath_PopulatesChange7d derives the
// 7-day change from the History7d series. Two-point series gives
// a single computable change. Pin the inverse-USD basis (positive
// = ticker strengthened) — the explorer's render reads this sign
// convention verbatim.
func TestHandleCurrencies_HappyPath_PopulatesChange7d(t *testing.T) {
	// EUR strengthened: 7d ago 1 USD = 0.90 EUR (1 EUR = 1.111 USD)
	// Today: 1 USD = 0.85 EUR (1 EUR = 1.176 USD)
	// Change in inverse-USD ≈ +5.88%
	day0 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	snap := &v1.CurrenciesSnapshot{
		PublishedAt: day0.Add(7 * 24 * time.Hour),
		FetchedAt:   day0.Add(7 * 24 * time.Hour),
		Currencies: []v1.CurrencyEntry{
			{Ticker: "EUR", Name: "Euro", RateUSD: 0.85},
		},
		History7d: map[string][]v1.CurrencyHistoryRaw{
			"EUR": {
				{Date: day0, RateUSD: 0.90},
				{Date: day0.Add(7 * 24 * time.Hour), RateUSD: 0.85},
			},
		},
	}
	srv := v1.New(v1.Options{Currencies: &stubCurrenciesReader{snap: snap}})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/currencies")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var env struct {
		Data v1.CurrenciesPayload `json:"data"`
	}
	body, _ := readAll(resp)
	if err := json.NewDecoder(strings.NewReader(body)).Decode(&env); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, body)
	}
	if len(env.Data.Currencies) != 1 {
		t.Fatalf("len = %d", len(env.Data.Currencies))
	}
	eur := env.Data.Currencies[0]
	if eur.Change7dPct == nil {
		t.Fatal("Change7dPct should be populated when ≥2 history points")
	}
	// Tolerate floating-point fuzz; pin the sign + ~order of magnitude.
	got := *eur.Change7dPct
	if got < 5.0 || got > 7.0 {
		t.Errorf("Change7dPct = %.4f, want ~+5.88%% (EUR strengthened vs USD)", got)
	}
	// Two-point history collapses to one yest/today pair so 24h
	// change uses the same delta — pin the pointer is non-nil.
	if eur.Change24hPct == nil {
		t.Errorf("Change24hPct should be populated when ≥2 history points")
	}
	// Sparkline NOT requested → field must be absent.
	if eur.History7dRates != nil {
		t.Errorf("History7dRates should be nil without ?include=sparkline; got %v", eur.History7dRates)
	}
}

// TestHandleCurrencies_SparklineInclude attaches the inverse-rate
// series when ?include=sparkline. Each value is 1/RateUSD.
func TestHandleCurrencies_SparklineInclude(t *testing.T) {
	snap := &v1.CurrenciesSnapshot{
		Currencies: []v1.CurrencyEntry{
			{Ticker: "EUR", RateUSD: 0.85},
		},
		History7d: map[string][]v1.CurrencyHistoryRaw{
			"EUR": {
				{Date: time.Now(), RateUSD: 0.90},
				{Date: time.Now(), RateUSD: 0.85},
			},
		},
	}
	srv := v1.New(v1.Options{Currencies: &stubCurrenciesReader{snap: snap}})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/currencies?include=sparkline")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var env struct {
		Data v1.CurrenciesPayload `json:"data"`
	}
	body, _ := readAll(resp)
	if err := json.NewDecoder(strings.NewReader(body)).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(env.Data.Currencies) != 1 || len(env.Data.Currencies[0].History7dRates) != 2 {
		t.Fatalf("History7dRates not populated: %+v", env.Data.Currencies[0])
	}
	rates := env.Data.Currencies[0].History7dRates
	// First point: 1/0.90 ≈ 1.111
	if rates[0] < 1.10 || rates[0] > 1.12 {
		t.Errorf("history[0] = %.4f, want ≈ 1.111 (1/0.90)", rates[0])
	}
	// Last point: 1/0.85 ≈ 1.176
	if rates[1] < 1.17 || rates[1] > 1.18 {
		t.Errorf("history[1] = %.4f, want ≈ 1.176 (1/0.85)", rates[1])
	}
}

// TestHandleCurrencies_LimitClamp — reading the same surface fix
// shipped in #1151: invalid limit values return 400 with
// invalid-limit problem type. Pre-fix the handler silently
// ignored them.
func TestHandleCurrencies_LimitClamp(t *testing.T) {
	snap := &v1.CurrenciesSnapshot{
		Currencies: []v1.CurrencyEntry{{Ticker: "EUR", RateUSD: 0.85}},
	}
	srv := v1.New(v1.Options{Currencies: &stubCurrenciesReader{snap: snap}})
	ts := startHTTPTest(t, srv.Handler())

	for _, bad := range []string{"0", "501", "-1", "xyz"} {
		resp := mustGet(t, ts.URL+"/v1/currencies?limit="+bad)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("limit=%q → %d, want 400", bad, resp.StatusCode)
			continue
		}
		body, _ := readAll(resp)
		if !strings.Contains(body, "invalid-limit") {
			t.Errorf("limit=%q error type missing: %s", bad, body)
		}
	}
}

// TestHandleCurrencies_LimitTrimsList — a valid limit smaller than
// the snapshot trims the response.
func TestHandleCurrencies_LimitTrimsList(t *testing.T) {
	snap := &v1.CurrenciesSnapshot{
		Currencies: []v1.CurrencyEntry{
			{Ticker: "EUR"},
			{Ticker: "GBP"},
			{Ticker: "JPY"},
			{Ticker: "AUD"},
			{Ticker: "CAD"},
		},
	}
	srv := v1.New(v1.Options{Currencies: &stubCurrenciesReader{snap: snap}})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/currencies?limit=2")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var env struct {
		Data v1.CurrenciesPayload `json:"data"`
	}
	body, _ := readAll(resp)
	if err := json.NewDecoder(strings.NewReader(body)).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(env.Data.Currencies) != 2 {
		t.Errorf("len = %d, want 2", len(env.Data.Currencies))
	}
}

// TestHandleCurrencies_NoChangeFieldsWhenHistoryThin — when the
// snapshot has only one history point (or none), Change7dPct +
// Change24hPct stay nil. Callers branch on absence to render "—"
// rather than fabricate "0.00%".
func TestHandleCurrencies_NoChangeFieldsWhenHistoryThin(t *testing.T) {
	snap := &v1.CurrenciesSnapshot{
		Currencies: []v1.CurrencyEntry{{Ticker: "EUR", RateUSD: 0.85}},
		History7d: map[string][]v1.CurrencyHistoryRaw{
			"EUR": {{Date: time.Now(), RateUSD: 0.85}}, // only 1 point
		},
	}
	srv := v1.New(v1.Options{Currencies: &stubCurrenciesReader{snap: snap}})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/currencies")
	body, _ := readAll(resp)
	for _, forbidden := range []string{`"change_24h_pct"`, `"change_7d_pct"`} {
		if strings.Contains(body, forbidden) {
			t.Errorf("body should NOT contain %q with only 1 history point: %s", forbidden, body)
		}
	}
}
