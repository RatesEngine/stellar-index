package exchangeratesapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
)

// buildPairs returns a pair list that spans two fiat quotes (EUR
// and GBP) plus one crypto pair (XLM/USD) — verifies the poller
// extracts the fiat symbols and silently skips the crypto pair.
func buildPairs(t *testing.T) []canonical.Pair {
	t.Helper()
	xlm, _ := canonical.NewCryptoAsset("XLM")
	usd, _ := canonical.NewFiatAsset("USD")
	eur, _ := canonical.NewFiatAsset("EUR")
	gbp, _ := canonical.NewFiatAsset("GBP")

	xlmUSD, _ := canonical.NewPair(xlm, usd)
	eurUSD, _ := canonical.NewPair(eur, usd)
	gbpUSD, _ := canonical.NewPair(gbp, usd)
	return []canonical.Pair{xlmUSD, eurUSD, gbpUSD}
}

// newTestServer serves the /v1/latest shape.
func newTestServer(t *testing.T, response string, status int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != LatestPath {
			t.Errorf("unexpected path %q", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		// Validate the query includes our access_key + base.
		if r.URL.Query().Get("access_key") == "" {
			t.Error("missing access_key")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = fmt.Fprint(w, response)
	}))
}

func TestNewPoller_RejectsEmptyKey(t *testing.T) {
	_, err := NewPoller("")
	if !errors.Is(err, ErrAPIKeyRequired) {
		t.Errorf("expected ErrAPIKeyRequired, got %v", err)
	}
}

func TestPollOnce_HappyPath(t *testing.T) {
	srv := newTestServer(t, `{
      "success": true,
      "timestamp": 1745000000,
      "base": "USD",
      "date": "2026-04-24",
      "rates": {
        "EUR": 0.92350,
        "GBP": 0.78450
      }
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
		t.Errorf("expected 0 trades (FX poller emits updates only), got %d", len(trades))
	}
	if len(updates) != 2 {
		t.Fatalf("expected 2 updates (EUR, GBP), got %d", len(updates))
	}

	eur, _ := canonical.NewFiatAsset("EUR")
	usd, _ := canonical.NewFiatAsset("USD")

	var eurUpdate *canonical.OracleUpdate
	for i := range updates {
		if updates[i].Asset.Equal(eur) {
			eurUpdate = &updates[i]
			break
		}
	}
	if eurUpdate == nil {
		t.Fatal("missing EUR update")
	}
	if !eurUpdate.Quote.Equal(usd) {
		t.Errorf("EUR quote = %+v want USD", eurUpdate.Quote)
	}
	// Venue said USD→EUR = 0.9235. We invert to EUR→USD = 1 / 0.9235
	// ≈ 1.0828 at 10^6 → 1082836. Verify ±rounding tolerance.
	priceInt := eurUpdate.Price.BigInt().Int64()
	if priceInt < 1_080_000 || priceInt > 1_085_000 {
		t.Errorf("EUR price (inverted) = %d, want ~1082836", priceInt)
	}
	if eurUpdate.Timestamp.Unix() != 1_745_000_000 {
		t.Errorf("timestamp = %d want 1745000000", eurUpdate.Timestamp.Unix())
	}
	if eurUpdate.Decimals != 6 {
		t.Errorf("decimals = %d want 6", eurUpdate.Decimals)
	}
	if len(eurUpdate.TxHash) != 64 {
		t.Errorf("tx_hash len = %d want 64", len(eurUpdate.TxHash))
	}
}

func TestPollOnce_APIRejection(t *testing.T) {
	srv := newTestServer(t, `{
      "success": false,
      "error": {"code": 101, "type": "invalid_access_key", "info": "Invalid API key"}
    }`, http.StatusOK)
	defer srv.Close()

	p, _ := NewPoller("WRONG_KEY")
	p.Endpoint = srv.URL
	_, _, err := p.PollOnce(context.Background(), buildPairs(t))
	if !errors.Is(err, ErrAPIRejected) {
		t.Errorf("expected ErrAPIRejected, got %v", err)
	}
}

func TestPollOnce_BaseMismatch_Rejected(t *testing.T) {
	// Operator asked for USD base (paid tier) but venue returned
	// EUR base (free tier silently does this). Poller must refuse
	// to emit mislabelled rows.
	srv := newTestServer(t, `{
      "success": true, "timestamp": 1745000000, "base": "EUR", "date": "2026-04-24",
      "rates": {"USD": 1.0825, "GBP": 0.8562}
    }`, http.StatusOK)
	defer srv.Close()

	p, _ := NewPoller("TEST_KEY")
	p.Endpoint = srv.URL
	_, _, err := p.PollOnce(context.Background(), buildPairs(t))
	if !errors.Is(err, ErrAPIRejected) {
		t.Errorf("expected ErrAPIRejected for base mismatch, got %v", err)
	}
}

func TestPollOnce_UnknownCurrencySkipped(t *testing.T) {
	// Venue returns an obscure currency not on our allow-list;
	// skip per-entry, keep emitting the rest.
	srv := newTestServer(t, `{
      "success": true, "timestamp": 1745000000, "base": "USD", "date": "2026-04-24",
      "rates": {"EUR": 0.9235, "ZZZ": 99.0, "GBP": 0.78}
    }`, http.StatusOK)
	defer srv.Close()

	p, _ := NewPoller("TEST_KEY")
	p.Endpoint = srv.URL
	_, updates, err := p.PollOnce(context.Background(), buildPairs(t))
	if err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	if len(updates) != 2 {
		t.Errorf("expected 2 updates (ZZZ dropped), got %d", len(updates))
	}
}

func TestPollOnce_CryptoPairsSilentlySkipped(t *testing.T) {
	// All pairs are crypto-quoted — no fiat symbols to request.
	// Poller returns no-op rather than erroring.
	xlm, _ := canonical.NewCryptoAsset("XLM")
	usdt, _ := canonical.NewCryptoAsset("USDT")
	xlmUsdt, _ := canonical.NewPair(xlm, usdt)

	p, _ := NewPoller("TEST_KEY")
	// No server needed — we should never hit HTTP.
	p.Endpoint = "http://localhost:1" // would fail if reached

	_, updates, err := p.PollOnce(context.Background(), []canonical.Pair{xlmUsdt})
	if err != nil {
		t.Fatalf("should no-op silently, got err: %v", err)
	}
	if len(updates) != 0 {
		t.Errorf("expected 0 updates, got %d", len(updates))
	}
}

func TestPollOnce_HTTP5xxErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	p, _ := NewPoller("TEST_KEY")
	p.Endpoint = srv.URL
	_, _, err := p.PollOnce(context.Background(), buildPairs(t))
	if err == nil {
		t.Error("expected error on HTTP http.StatusServiceUnavailable")
	}
}

func TestPollInterval_DefaultsTo60s(t *testing.T) {
	p, _ := NewPoller("TEST_KEY")
	if p.PollInterval() != 60*time.Second {
		t.Errorf("default interval = %v want 60s", p.PollInterval())
	}
}

func TestResolveSymbols_ExcludesBase(t *testing.T) {
	p, _ := NewPoller("TEST_KEY")
	// Pairs mix: EUR/USD and GBP/USD → symbols should be {EUR, GBP},
	// not {EUR, GBP, USD} since USD is the base.
	symbols := p.resolveSymbols("USD", buildPairs(t))
	seen := map[string]bool{}
	for _, s := range symbols {
		seen[s] = true
	}
	if seen["USD"] {
		t.Error("base currency USD should not appear in symbols list")
	}
	if !seen["EUR"] || !seen["GBP"] {
		t.Errorf("missing EUR or GBP: %v", symbols)
	}
}
