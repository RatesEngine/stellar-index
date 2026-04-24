package coinbase

import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// synthesiseCoinbaseCandles builds LHOC-order candle rows. Coinbase
// returns newest-first, so we build that way.
func synthesiseCoinbaseCandles(count int, startSec, granSec int64) []coinbaseCandle {
	out := make([]coinbaseCandle, count)
	// Newest-first = reverse order.
	for i := 0; i < count; i++ {
		idx := count - 1 - i // oldest at end
		t := startSec + int64(idx)*granSec
		closePrice := 0.17582 + 0.00001*float64(idx)
		out[i] = coinbaseCandle{
			float64(t), // time_sec
			0.17500,    // low
			0.17600,    // high
			0.17582,    // open
			closePrice, // close
			100.0,      // volume
		}
	}
	return out
}

// newTestCoinbaseREST serves the fixture on the first call then
// empty arrays on subsequent calls — matches the real venue's
// "no more data past the newest emitted candle" behaviour when
// our paginator advances its `start` parameter.
func newTestCoinbaseREST(t *testing.T, product string, candles []coinbaseCandle) *httptest.Server {
	t.Helper()
	expectedPath := "/products/" + product + "/candles"
	var called bool
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/products/") {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path != expectedPath {
			t.Errorf("path = %q want %q", r.URL.Path, expectedPath)
		}
		if r.Header.Get("User-Agent") == "" {
			t.Error("missing User-Agent — Coinbase rejects empty UA")
		}
		w.Header().Set("Content-Type", "application/json")
		if called {
			_ = json.NewEncoder(w).Encode([]coinbaseCandle{})
			return
		}
		called = true
		_ = json.NewEncoder(w).Encode(candles)
	}))
}

func TestCoinbaseBackfill_HappyPath(t *testing.T) {
	const startSec = int64(1_745_000_000)
	const hourSec = int64(3_600)

	candles := synthesiseCoinbaseCandles(5, startSec, hourSec)
	srv := newTestCoinbaseREST(t, "XLM-USD", candles)
	defer srv.Close()

	m, err := DefaultPairs()
	if err != nil {
		t.Fatalf("DefaultPairs: %v", err)
	}
	s := NewStreamer(m)
	s.Endpoint = srv.URL

	xlm, _ := canonical.NewCryptoAsset("XLM")
	usd, _ := canonical.NewFiatAsset("USD")
	pair, _ := canonical.NewPair(xlm, usd)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	trades, err := s.Backfill(ctx, pair,
		time.Unix(startSec, 0).UTC(),
		time.Unix(startSec+6*hourSec, 0).UTC(),
		1*time.Hour)
	if err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	if len(trades) != 5 {
		t.Fatalf("expected 5 trades, got %d", len(trades))
	}
	// Coinbase returns newest-first but we emit oldest-first →
	// trades[0] should be the oldest.
	if !trades[0].Timestamp.Before(trades[4].Timestamp) {
		t.Errorf("trades not chronological: [0]=%v [4]=%v",
			trades[0].Timestamp, trades[4].Timestamp)
	}
	// Close time = open + granSec - 1 for the first (oldest) candle.
	wantCloseSec := startSec + hourSec - 1
	if trades[0].Timestamp.Unix() != wantCloseSec {
		t.Errorf("close = %d want %d", trades[0].Timestamp.Unix(), wantCloseSec)
	}
	// Base = 100 × 10^8
	wantBase := big.NewInt(10_000_000_000)
	if trades[0].BaseAmount.BigInt().Cmp(wantBase) != 0 {
		t.Errorf("BaseAmount = %s want %s", trades[0].BaseAmount, wantBase)
	}
	if trades[0].Source != "coinbase" {
		t.Errorf("Source = %q", trades[0].Source)
	}
}

func TestCoinbaseBackfill_UnsupportedGranularity(t *testing.T) {
	m, _ := DefaultPairs()
	s := NewStreamer(m)
	xlm, _ := canonical.NewCryptoAsset("XLM")
	usd, _ := canonical.NewFiatAsset("USD")
	p, _ := canonical.NewPair(xlm, usd)
	// 4h isn't in Coinbase's set (only 1m/5m/15m/1h/6h/1d).
	_, err := s.Backfill(context.Background(), p,
		time.Unix(1, 0), time.Unix(10000, 0), 4*time.Hour)
	if err == nil {
		t.Error("expected error for 4h (not in Coinbase set)")
	}
}

func TestCoinbaseGranularity(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want int
		err  bool
	}{
		{1 * time.Minute, 60, false},
		{5 * time.Minute, 300, false},
		{15 * time.Minute, 900, false},
		{1 * time.Hour, 3600, false},
		{6 * time.Hour, 21600, false},
		{24 * time.Hour, 86400, false},
		{30 * time.Minute, 0, true}, // not in set
		{4 * time.Hour, 0, true},    // not in set
	}
	for _, tc := range cases {
		got, err := granularityToSeconds(tc.in)
		if tc.err {
			if err == nil {
				t.Errorf("%v: want err", tc.in)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("%v: (%d, %v) want %d", tc.in, got, err, tc.want)
		}
	}
}

// TestCoinbaseCandleToTrade_LHOC_Ordering guards against the
// positional-field-order trap: Coinbase returns LHOC (low/high/open/
// close) while every other CEX uses OHLC. A parser assuming the
// wrong order would use LOW price as close — lower than reality.
func TestCoinbaseCandleToTrade_LHOC_Ordering(t *testing.T) {
	// Synthesise a candle with a clear low vs close gap so mis-
	// ordering surfaces as a price divergence. Close = 100, Low = 50.
	c := coinbaseCandle{
		float64(1745000000), // time
		50.0,                // low (slot 1)
		100.0,               // high (slot 2)
		90.0,                // open (slot 3)
		100.0,               // close (slot 4 — should drive price)
		10.0,                // volume
	}
	xlm, _ := canonical.NewCryptoAsset("XLM")
	usd, _ := canonical.NewFiatAsset("USD")
	pair, _ := canonical.NewPair(xlm, usd)
	trade, err := coinbaseCandleToTrade(c, "XLM-USD", pair, 3600)
	if err != nil {
		t.Fatalf("coinbaseCandleToTrade: %v", err)
	}
	// Quote = base × close / 10^8 = 10 × 100 = 1000
	// At 10^8 scale: 1000 × 10^8 = 100_000_000_000
	wantQuote := big.NewInt(100_000_000_000)
	if trade.QuoteAmount.BigInt().Cmp(wantQuote) != 0 {
		t.Errorf("QuoteAmount = %s want %s (close=100 × vol=10). "+
			"If this failed with quote ≈ 5e10, LHOC ordering is being read as OHLC (low instead of close).",
			trade.QuoteAmount, wantQuote)
	}
}
