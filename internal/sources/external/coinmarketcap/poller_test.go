package coinmarketcap

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
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

func TestPollInterval_Default(t *testing.T) {
	p, _ := NewPoller("TEST")
	if p.PollInterval() != 60*time.Second {
		t.Errorf("default = %v", p.PollInterval())
	}
}
