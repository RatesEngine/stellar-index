package polygonforex

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
)

func buildPairs(t *testing.T) []canonical.Pair {
	t.Helper()
	usd, _ := canonical.NewFiatAsset("USD")
	eur, _ := canonical.NewFiatAsset("EUR")
	gbp, _ := canonical.NewFiatAsset("GBP")
	eurUSD, _ := canonical.NewPair(eur, usd)
	gbpUSD, _ := canonical.NewPair(gbp, usd)
	return []canonical.Pair{eurUSD, gbpUSD}
}

func newTestServer(t *testing.T, body string, status int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != SnapshotPath {
			t.Errorf("path = %q want %q", r.URL.Path, SnapshotPath)
			http.NotFound(w, r)
			return
		}
		// G10-04: key travels in the Authorization header now, never
		// the query string (so it can't leak via a *url.Error).
		if got := r.Header.Get("Authorization"); !strings.HasPrefix(got, "Bearer ") {
			t.Errorf("Authorization header = %q, want Bearer-prefixed", got)
		}
		if r.URL.Query().Get("apiKey") != "" {
			t.Error("apiKey must NOT appear in the query string (G10-04)")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = fmt.Fprint(w, body)
	}))
}

func TestNewPoller_RejectsEmptyKey(t *testing.T) {
	_, err := NewPoller("")
	if !errors.Is(err, ErrAPIKeyRequired) {
		t.Errorf("expected ErrAPIKeyRequired, got %v", err)
	}
}

func TestPollOnce_HappyPath(t *testing.T) {
	// USD-EUR and USD-GBP tickers; we also include one USDJPY to
	// verify filter-to-configured-pairs works (we didn't ask for
	// JPY in buildPairs, so JPY must be dropped).
	srv := newTestServer(t, `{
      "status":"OK",
      "tickers":[
        {"ticker":"C:USDEUR","lastQuote":{"a":0.92380,"b":0.92320,"x":48,"t":1745000000000},"updated":1745000000000},
        {"ticker":"C:USDGBP","lastQuote":{"a":0.78460,"b":0.78440,"x":48,"t":1745000000000},"updated":1745000000000},
        {"ticker":"C:USDJPY","lastQuote":{"a":149.60,"b":149.50,"x":48,"t":1745000000000},"updated":1745000000000}
      ]
    }`, http.StatusOK)
	defer srv.Close()

	p, err := NewPoller("TEST_KEY")
	if err != nil {
		t.Fatalf("NewPoller: %v", err)
	}
	p.Endpoint = srv.URL

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	trades, updates, err := p.PollOnce(ctx, buildPairs(t))
	if err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	if len(trades) != 0 {
		t.Errorf("FX poller must emit 0 trades, got %d", len(trades))
	}
	if len(updates) != 2 {
		t.Fatalf("expected 2 updates (EUR, GBP; JPY filtered), got %d", len(updates))
	}

	eur, _ := canonical.NewFiatAsset("EUR")
	usd, _ := canonical.NewFiatAsset("USD")
	var eurU *canonical.OracleUpdate
	for i := range updates {
		if updates[i].Asset.Equal(eur) {
			eurU = &updates[i]
			break
		}
	}
	if eurU == nil {
		t.Fatal("missing EUR update")
	}
	if !eurU.Quote.Equal(usd) {
		t.Errorf("EUR quote = %+v want USD", eurU.Quote)
	}
	// Mid = (0.92380 + 0.92320) / 2 = 0.92350 (USD→EUR).
	// Inverted = 1 / 0.92350 ≈ 1.08283 (EUR in USD).
	// At 10^6 → 1_082_836 ± a few for division rounding.
	priceInt := eurU.Price.BigInt().Int64()
	if priceInt < 1_080_000 || priceInt > 1_085_000 {
		t.Errorf("EUR price = %d, want ~1082836", priceInt)
	}
	if len(eurU.TxHash) != 64 {
		t.Errorf("TxHash len = %d", len(eurU.TxHash))
	}
	if eurU.Decimals != 6 {
		t.Errorf("decimals = %d want 6", eurU.Decimals)
	}
}

func TestPollOnce_StatusNotOK(t *testing.T) {
	srv := newTestServer(t, `{"status":"ERROR","error":"invalid_api_key","message":"bad key"}`, http.StatusOK)
	defer srv.Close()
	p, _ := NewPoller("TEST")
	p.Endpoint = srv.URL
	_, _, err := p.PollOnce(context.Background(), buildPairs(t))
	if !errors.Is(err, ErrAPIRejected) {
		t.Errorf("expected ErrAPIRejected, got %v", err)
	}
}

func TestPollOnce_401Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad key", http.StatusUnauthorized)
	}))
	defer srv.Close()
	p, _ := NewPoller("BAD")
	p.Endpoint = srv.URL
	_, _, err := p.PollOnce(context.Background(), buildPairs(t))
	if !errors.Is(err, ErrAPIRejected) {
		t.Errorf("expected ErrAPIRejected on 401, got %v", err)
	}
}

func TestPollOnce_429RateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "slow down", http.StatusTooManyRequests)
	}))
	defer srv.Close()
	p, _ := NewPoller("TEST")
	p.Endpoint = srv.URL
	_, _, err := p.PollOnce(context.Background(), buildPairs(t))
	if !errors.Is(err, ErrAPIRejected) {
		t.Errorf("expected ErrAPIRejected on 429, got %v", err)
	}
}

func TestPollOnce_MalformedTickerSkipped(t *testing.T) {
	srv := newTestServer(t, `{
      "status":"OK",
      "tickers":[
        {"ticker":"NOT_A_TICKER","lastQuote":{"a":1,"b":1,"x":1,"t":1745000000000}},
        {"ticker":"C:USDEUR","lastQuote":{"a":0.9235,"b":0.9234,"x":1,"t":1745000000000}}
      ]
    }`, http.StatusOK)
	defer srv.Close()
	p, _ := NewPoller("TEST")
	p.Endpoint = srv.URL
	_, updates, err := p.PollOnce(context.Background(), buildPairs(t))
	if err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	if len(updates) != 1 {
		t.Errorf("expected 1 update (malformed skipped), got %d", len(updates))
	}
}

func TestParseCurrencyTicker(t *testing.T) {
	cases := []struct {
		in    string
		base  string
		quote string
		err   bool
	}{
		{"C:USDEUR", "USD", "EUR", false},
		{"C:EURJPY", "EUR", "JPY", false},
		{"C:usdeur", "USD", "EUR", false}, // case normalised
		{"USDEUR", "", "", true},          // missing prefix
		{"C:USD", "", "", true},           // too short
		{"C:USDEURX", "", "", true},       // too long
		{"", "", "", true},
	}
	for _, tc := range cases {
		b, q, err := parseCurrencyTicker(tc.in)
		if tc.err {
			if err == nil {
				t.Errorf("%q: want err, got (%q, %q)", tc.in, b, q)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: unexpected err %v", tc.in, err)
			continue
		}
		if b != tc.base || q != tc.quote {
			t.Errorf("%q: got (%q, %q) want (%q, %q)", tc.in, b, q, tc.base, tc.quote)
		}
	}
}

func TestMidPriceString(t *testing.T) {
	// Both sides present: mean.
	m, err := midPriceString("1.02000", "1.00000")
	if err != nil {
		t.Fatalf("mid both: %v", err)
	}
	if m != "1.010000" {
		t.Errorf("both: got %q want 1.010000", m)
	}
	// Only ask present.
	m, err = midPriceString("1.02000", "0")
	if err != nil || m == "0.000000" {
		t.Errorf("ask-only: got %q err %v", m, err)
	}
	// Only bid present.
	m, err = midPriceString("0", "1.01000")
	if err != nil || m == "0.000000" {
		t.Errorf("bid-only: got %q err %v", m, err)
	}
	// Both zero → error.
	_, err = midPriceString("0", "0")
	if err == nil {
		t.Error("expected error for both zero")
	}
}

func TestPollOnce_WrongBaseTickersSkipped(t *testing.T) {
	// Configured base = USD. Snapshot includes a EUR/GBP ticker
	// (not USD-based) which must be skipped.
	srv := newTestServer(t, `{
      "status":"OK",
      "tickers":[
        {"ticker":"C:EURGBP","lastQuote":{"a":0.85,"b":0.84,"x":1,"t":1}},
        {"ticker":"C:USDEUR","lastQuote":{"a":0.9235,"b":0.9234,"x":1,"t":1}}
      ]
    }`, http.StatusOK)
	defer srv.Close()
	p, _ := NewPoller("TEST")
	p.Endpoint = srv.URL
	_, updates, err := p.PollOnce(context.Background(), buildPairs(t))
	if err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	if len(updates) != 1 {
		t.Errorf("expected 1 update (EUR/GBP wrong-base skipped), got %d", len(updates))
	}
}

func TestPollInterval_Default(t *testing.T) {
	p, _ := NewPoller("TEST")
	if p.PollInterval() != 60*time.Second {
		t.Errorf("default interval = %v want 60s", p.PollInterval())
	}
}
