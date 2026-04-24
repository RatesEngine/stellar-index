package coingecko

import (
	"context"
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
	eur, _ := canonical.NewFiatAsset("EUR")
	xlmUSD, _ := canonical.NewPair(xlm, usd)
	xlmEUR, _ := canonical.NewPair(xlm, eur)
	btcUSD, _ := canonical.NewPair(btc, usd)
	return []canonical.Pair{xlmUSD, xlmEUR, btcUSD}
}

func newTestServer(t *testing.T, body string, status int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != SimplePricePath {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = fmt.Fprint(w, body)
	}))
}

func TestPollOnce_HappyPath(t *testing.T) {
	srv := newTestServer(t, `{
      "stellar":  {"usd": 0.17582, "eur": 0.16230},
      "bitcoin":  {"usd": 50000.0, "eur": 46250.0}
    }`, http.StatusOK)
	defer srv.Close()

	p := NewPoller()
	p.Endpoint = srv.URL

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	trades, updates, err := p.PollOnce(ctx, buildPairs(t))
	if err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	if len(trades) != 0 {
		t.Errorf("expected 0 trades (aggregator emits updates only), got %d", len(trades))
	}
	// Exact-combo filter: we asked for XLM/USD, XLM/EUR, BTC/USD.
	// BTC/EUR is returned by the venue but not emitted because
	// no operator-configured pair targets that combo.
	if len(updates) != 3 {
		t.Fatalf("expected 3 updates (XLM/USD, XLM/EUR, BTC/USD — BTC/EUR filtered), got %d", len(updates))
	}

	// Verify XLM/USD specifically.
	xlm, _ := canonical.NewCryptoAsset("XLM")
	usd, _ := canonical.NewFiatAsset("USD")
	var xlmU *canonical.OracleUpdate
	for i := range updates {
		if updates[i].Asset.Equal(xlm) && updates[i].Quote.Equal(usd) {
			xlmU = &updates[i]
			break
		}
	}
	if xlmU == nil {
		t.Fatal("missing XLM/USD update")
	}
	// 0.17582 × 10^8 = 17_582_000 (with ±rounding tolerance).
	priceInt := xlmU.Price.BigInt().Int64()
	if priceInt < 17_580_000 || priceInt > 17_584_000 {
		t.Errorf("XLM/USD price = %d want ~17582000", priceInt)
	}
	if xlmU.Decimals != 8 {
		t.Errorf("decimals = %d want 8", xlmU.Decimals)
	}
	if len(xlmU.TxHash) != 64 {
		t.Errorf("tx_hash len = %d", len(xlmU.TxHash))
	}
	if xlmU.Source != "coingecko" {
		t.Errorf("Source = %q", xlmU.Source)
	}
}

func TestPollOnce_UnknownTickerSkipped(t *testing.T) {
	// A pair with DOT (not in tickerToID default) shouldn't cause
	// errors; CoinGecko will just not receive the id, and the
	// matching response won't contain it.
	dot, err := canonical.NewCryptoAsset("DOT")
	if err != nil {
		t.Skip("DOT not on crypto allow-list; skipping")
	}
	usd, _ := canonical.NewFiatAsset("USD")
	dotUSD, _ := canonical.NewPair(dot, usd)

	srv := newTestServer(t, `{"stellar":{"usd":0.17}}`, http.StatusOK)
	defer srv.Close()
	p := NewPoller()
	p.Endpoint = srv.URL

	// DOT is in tickerToID but "polkadot" not in fixture response.
	// Just XLM/USD should come back if the pair also included XLM.
	xlm, _ := canonical.NewCryptoAsset("XLM")
	xlmUSD, _ := canonical.NewPair(xlm, usd)
	_, updates, err := p.PollOnce(context.Background(), []canonical.Pair{dotUSD, xlmUSD})
	if err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	// Venue returned only stellar/usd; we emit 1 update.
	if len(updates) != 1 {
		t.Errorf("expected 1 update, got %d", len(updates))
	}
}

func TestPollOnce_CryptoOnlyPairs_NoOp(t *testing.T) {
	// Both sides of the pair are crypto — no fiat quote to request.
	// Poller should no-op silently (no HTTP call).
	xlm, _ := canonical.NewCryptoAsset("XLM")
	usdt, _ := canonical.NewCryptoAsset("USDT")
	xlmUsdt, _ := canonical.NewPair(xlm, usdt)

	p := NewPoller()
	p.Endpoint = "http://localhost:1" // would fail if reached
	_, updates, err := p.PollOnce(context.Background(), []canonical.Pair{xlmUsdt})
	if err != nil {
		t.Fatalf("should no-op, got err: %v", err)
	}
	if len(updates) != 0 {
		t.Errorf("expected 0 updates, got %d", len(updates))
	}
}

func TestPollOnce_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer srv.Close()
	p := NewPoller()
	p.Endpoint = srv.URL
	_, _, err := p.PollOnce(context.Background(), buildPairs(t))
	if err == nil {
		t.Error("expected error on HTTP 429")
	}
}

func TestPollOnce_MalformedJSON(t *testing.T) {
	srv := newTestServer(t, `{not json`, http.StatusOK)
	defer srv.Close()
	p := NewPoller()
	p.Endpoint = srv.URL
	_, _, err := p.PollOnce(context.Background(), buildPairs(t))
	if err == nil {
		t.Error("expected error on malformed JSON")
	}
}

func TestPollInterval_Default(t *testing.T) {
	p := NewPoller()
	if p.PollInterval() != 60*time.Second {
		t.Errorf("default = %v want 60s", p.PollInterval())
	}
}

func TestFloatToScaledInt_Precision(t *testing.T) {
	// 0.17582 at 10^8 should round to 17_582_000 (±1).
	got, err := floatToScaledInt(0.17582, 8)
	if err != nil {
		t.Fatalf("floatToScaledInt: %v", err)
	}
	n := got.Int64()
	if n < 17_581_999 || n > 17_582_001 {
		t.Errorf("0.17582 → %d, expected ≈17582000", n)
	}
	// Negative rejected.
	if _, err := floatToScaledInt(-1, 8); err == nil {
		t.Error("expected error for negative")
	}
}
