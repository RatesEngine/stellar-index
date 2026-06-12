package cryptocompare

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
	eur, _ := canonical.NewFiatAsset("EUR")
	xlmUSD, _ := canonical.NewPair(xlm, usd)
	xlmEUR, _ := canonical.NewPair(xlm, eur)
	btcUSD, _ := canonical.NewPair(btc, usd)
	return []canonical.Pair{xlmUSD, xlmEUR, btcUSD}
}

func newTestServer(t *testing.T, body string, status int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != PriceMultiPath {
			http.NotFound(w, r)
			return
		}
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Apikey ") {
			t.Errorf("bad Authorization header %q (want 'Apikey ...')", auth)
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
      "XLM": {"USD": 0.17582, "EUR": 0.16230},
      "BTC": {"USD": 50000.0, "EUR": 46250.0}
    }`, http.StatusOK)
	defer srv.Close()

	p, err := NewPoller("TEST_KEY")
	if err != nil {
		t.Fatalf("NewPoller: %v", err)
	}
	p.Endpoint = srv.URL

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, updates, err := p.PollOnce(ctx, buildPairs(t))
	if err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	// XLM×2 + BTC×1 (no BTC/EUR pair asked for).
	// Actually our pair list has XLM/USD, XLM/EUR, BTC/USD.
	// Venue returns XLM/{USD,EUR} + BTC/{USD,EUR}.
	// We emit only configured combos → 3 updates.
	if len(updates) != 3 {
		t.Fatalf("expected 3 updates, got %d", len(updates))
	}
}

func TestPollOnce_ErrorResponse(t *testing.T) {
	// CryptoCompare returns 200 OK with error envelope on auth
	// failures and unknown symbols.
	srv := newTestServer(t, `{
      "Response": "Error",
      "Message": "cccagg_or_exchange market does not exist for this coin pair"
    }`, http.StatusOK)
	defer srv.Close()
	p, _ := NewPoller("TEST")
	p.Endpoint = srv.URL
	_, _, err := p.PollOnce(context.Background(), buildPairs(t))
	if !errors.Is(err, ErrAPIRejected) {
		t.Errorf("expected ErrAPIRejected, got %v", err)
	}
}

func TestPollOnce_HTTP5xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	p, _ := NewPoller("TEST")
	p.Endpoint = srv.URL
	_, _, err := p.PollOnce(context.Background(), buildPairs(t))
	if err == nil {
		t.Error("expected error on HTTP http.StatusServiceUnavailable")
	}
}

func TestPollOnce_CryptoOnlyPairsNoOp(t *testing.T) {
	xlm, _ := canonical.NewCryptoAsset("XLM")
	usdt, _ := canonical.NewCryptoAsset("USDT")
	p1, _ := canonical.NewPair(xlm, usdt)
	p, _ := NewPoller("TEST")
	p.Endpoint = "http://localhost:1" // would fail if reached
	_, updates, err := p.PollOnce(context.Background(), []canonical.Pair{p1})
	if err != nil {
		t.Fatalf("should no-op, got: %v", err)
	}
	if len(updates) != 0 {
		t.Errorf("expected 0 updates, got %d", len(updates))
	}
}

func TestPollInterval_Default(t *testing.T) {
	p, _ := NewPoller("TEST")
	if p.PollInterval() != 60*time.Second {
		t.Errorf("default = %v", p.PollInterval())
	}
}
