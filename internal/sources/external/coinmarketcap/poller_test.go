package coinmarketcap

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
	xlm, _ := canonical.NewCryptoAsset("XLM")
	btc, _ := canonical.NewCryptoAsset("BTC")
	usd, _ := canonical.NewFiatAsset("USD")
	xlmUSD, _ := canonical.NewPair(xlm, usd)
	btcUSD, _ := canonical.NewPair(btc, usd)
	return []canonical.Pair{xlmUSD, btcUSD}
}

func newTestServer(t *testing.T, body string, status int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != QuotesLatestPath {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get(APIKeyHeader) == "" {
			t.Errorf("missing %s header", APIKeyHeader)
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
	srv := newTestServer(t, `{
      "status": {"error_code": 0, "error_message": null},
      "data": {
        "XLM": [{"symbol": "XLM", "quote": {"USD": {"price": 0.17582, "last_updated": "2026-04-24T00:00:00.000Z"}}}],
        "BTC": [{"symbol": "BTC", "quote": {"USD": {"price": 50000.0,  "last_updated": "2026-04-24T00:00:00.000Z"}}}]
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
		t.Errorf("aggregator must emit 0 trades, got %d", len(trades))
	}
	if len(updates) != 2 {
		t.Fatalf("expected 2 updates (XLM/USD, BTC/USD), got %d", len(updates))
	}

	xlm, _ := canonical.NewCryptoAsset("XLM")
	var xlmU *canonical.OracleUpdate
	for i := range updates {
		if updates[i].Asset.Equal(xlm) {
			xlmU = &updates[i]
			break
		}
	}
	if xlmU == nil {
		t.Fatal("missing XLM update")
	}
	priceInt := xlmU.Price.BigInt().Int64()
	if priceInt < 17_580_000 || priceInt > 17_584_000 {
		t.Errorf("XLM price = %d want ~17582000", priceInt)
	}
	if xlmU.Source != "coinmarketcap" {
		t.Errorf("Source = %q", xlmU.Source)
	}
	// Timestamp parsed from last_updated.
	wantTs, _ := time.Parse(time.RFC3339Nano, "2026-04-24T00:00:00.000Z")
	if !xlmU.Timestamp.Equal(wantTs) {
		t.Errorf("Timestamp = %v want %v", xlmU.Timestamp, wantTs)
	}
}

func TestPollOnce_APIError(t *testing.T) {
	srv := newTestServer(t, `{"status":{"error_code":401,"error_message":"Invalid API key"},"data":{}}`, http.StatusOK)
	defer srv.Close()
	p, _ := NewPoller("WRONG")
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

func TestPollOnce_MultipleCoinsWithSameTicker_TakesFirst(t *testing.T) {
	// CMC sometimes returns multiple coins under one ticker; we
	// take the first entry (ranked canonical project).
	srv := newTestServer(t, `{
      "status": {"error_code": 0, "error_message": null},
      "data": {
        "XLM": [
          {"symbol": "XLM", "quote": {"USD": {"price": 0.175,  "last_updated": "2026-04-24T00:00:00Z"}}},
          {"symbol": "XLM", "quote": {"USD": {"price": 9999.0, "last_updated": "2026-04-24T00:00:00Z"}}}
        ]
      }
    }`, http.StatusOK)
	defer srv.Close()
	p, _ := NewPoller("TEST")
	p.Endpoint = srv.URL
	_, updates, err := p.PollOnce(context.Background(), buildPairs(t))
	if err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	if len(updates) != 1 {
		t.Fatalf("expected 1 XLM update, got %d", len(updates))
	}
	// Price from the FIRST entry, not the decoy second one.
	priceInt := updates[0].Price.BigInt().Int64()
	if priceInt < 17_450_000 || priceInt > 17_550_000 {
		t.Errorf("took wrong coin entry: price = %d want ~17500000", priceInt)
	}
}

// TestPollOnce_IDModeUsesNumericIDs — F-1237 (codex audit-
// 2026-05-13): when the poller is configured with `CMCIDs`,
// the upstream request must use `id=<numeric>,...` instead of
// `symbol=<TICKER>,...`. Captures the actual query the test
// server received and asserts the returned price for the
// numeric-ID-keyed entry maps back to the canonical asset
// (XLM in this fixture).
func TestPollOnce_IDModeUsesNumericIDs(t *testing.T) {
	var capturedQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != QuotesLatestPath {
			http.NotFound(w, r)
			return
		}
		capturedQuery = r.URL.RawQuery
		// CMC's /v2 quotes/latest returns a top-level key keyed
		// by the numeric ID (as a string) when queried by id.
		// The poller must thread the response back to the
		// canonical asset by ID, not by ticker.
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{
            "status": {"error_code": 0, "error_message": null},
            "data": {
                "512": [{"symbol": "XLM", "quote": {"USD": {"price": 0.18, "last_updated": "2026-04-24T00:00:00Z"}}}],
                "1":   [{"symbol": "BTC", "quote": {"USD": {"price": 60000.0, "last_updated": "2026-04-24T00:00:00Z"}}}]
            }
        }`)
	}))
	defer srv.Close()

	p, err := NewPoller("TEST_KEY")
	if err != nil {
		t.Fatalf("NewPoller: %v", err)
	}
	p.Endpoint = srv.URL
	p.CMCIDs = map[string]string{
		"XLM": "512",
		"BTC": "1",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, updates, err := p.PollOnce(ctx, buildPairs(t))
	if err != nil {
		t.Fatalf("PollOnce: %v", err)
	}

	// 1) Request must have used `id=`, not `symbol=`.
	if capturedQuery == "" {
		t.Fatal("test server did not capture the request URL")
	}
	if !strings.Contains(capturedQuery, "id=") {
		t.Errorf("captured query %q missing `id=` param — poller did not use ID-mode despite CMCIDs being set", capturedQuery)
	}
	if strings.Contains(capturedQuery, "symbol=") {
		t.Errorf("captured query %q still has `symbol=` — must use ID-mode exclusively when CMCIDs is populated", capturedQuery)
	}

	// 2) The response keyed by numeric ID must round-trip back
	//    to the canonical XLM/BTC assets the pairs requested.
	xlm, _ := canonical.NewCryptoAsset("XLM")
	btc, _ := canonical.NewCryptoAsset("BTC")
	gotByAsset := map[string]int64{}
	for i := range updates {
		gotByAsset[updates[i].Asset.String()] = updates[i].Price.BigInt().Int64()
	}
	if got := gotByAsset[xlm.String()]; got < 17_990_000 || got > 18_010_000 {
		t.Errorf("XLM price (id=512) = %d, want ~18000000", got)
	}
	if got := gotByAsset[btc.String()]; got < 5_999_990_000_000 || got > 6_000_010_000_000 {
		t.Errorf("BTC price (id=1) = %d, want ~6000000000000", got)
	}
}

func TestPollInterval_Default(t *testing.T) {
	p, _ := NewPoller("TEST")
	if p.PollInterval() != 60*time.Second {
		t.Errorf("default = %v", p.PollInterval())
	}
}
