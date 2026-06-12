package kraken

import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
)

// synthesiseKrakenCandles builds a slice of Kraken-shape OHLC rows
// starting at `startSec` with `intervalSec` gap.
func synthesiseKrakenCandles(count int, startSec, intervalSec int64) []krakenCandle {
	out := make([]krakenCandle, count)
	for i := 0; i < count; i++ {
		t := startSec + int64(i)*intervalSec
		priceStr := strconv.FormatFloat(0.17582+0.00001*float64(i), 'f', 5, 64)
		out[i] = krakenCandle{
			float64(t),  // open time
			"0.17582",   // open
			"0.17600",   // high
			"0.17500",   // low
			priceStr,    // close
			priceStr,    // vwap
			"100.5",     // volume (base)
			float64(50), // count
		}
	}
	return out
}

// newTestKrakenREST wraps a fixture OHLC result in the Kraken
// response envelope and serves it.
func newTestKrakenREST(t *testing.T, pair string, candles []krakenCandle, last int64) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != ohlcPath {
			http.NotFound(w, r)
			return
		}
		result := map[string]any{
			pair:   candles,
			"last": last,
		}
		body := map[string]any{
			"error":  []string{},
			"result": result,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}))
}

func TestKrakenBackfill_HappyPath(t *testing.T) {
	const startSec = int64(1_745_000_000)
	const hourSec = int64(3_600)

	candles := synthesiseKrakenCandles(5, startSec, hourSec)
	lastTs := startSec + 4*hourSec
	srv := newTestKrakenREST(t, "XLMUSD", candles, lastTs)
	defer srv.Close()

	m, err := DefaultPairs()
	if err != nil {
		t.Fatalf("DefaultPairs: %v", err)
	}
	// Add XLMUSD mapping → XLM/USD canonical pair.
	xlm, _ := canonical.NewCryptoAsset("XLM")
	usd, _ := canonical.NewFiatAsset("USD")
	xlmUSD, _ := canonical.NewPair(xlm, usd)
	m["XLMUSD"] = xlmUSD

	s := NewStreamer(m)
	s.Endpoint = srv.URL

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	from := time.Unix(startSec, 0).UTC()
	to := from.Add(10 * time.Hour)

	trades, err := s.Backfill(ctx, xlmUSD, from, to, 1*time.Hour)
	if err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	if len(trades) != 5 {
		t.Fatalf("expected 5 trades, got %d", len(trades))
	}
	// First trade: close_time = startSec + hourSec - 1.
	wantCloseSec := startSec + hourSec - 1
	if trades[0].Timestamp.Unix() != wantCloseSec {
		t.Errorf("trade[0] close_sec = %d want %d",
			trades[0].Timestamp.Unix(), wantCloseSec)
	}
	// Base = 100.5 × 10^8 = 10_050_000_000
	wantBase := big.NewInt(10_050_000_000)
	if trades[0].BaseAmount.BigInt().Cmp(wantBase) != 0 {
		t.Errorf("BaseAmount = %s want %s", trades[0].BaseAmount, wantBase)
	}
	if trades[0].Source != "kraken" {
		t.Errorf("Source = %q", trades[0].Source)
	}
	if len(trades[0].TxHash) != 64 {
		t.Errorf("TxHash len = %d", len(trades[0].TxHash))
	}
}

func TestKrakenBackfill_RejectsInvalidRange(t *testing.T) {
	s := NewStreamer(map[string]canonical.Pair{})
	xlm, _ := canonical.NewCryptoAsset("XLM")
	usd, _ := canonical.NewFiatAsset("USD")
	p, _ := canonical.NewPair(xlm, usd)
	_, err := s.Backfill(context.Background(), p, time.Now(), time.Now(), time.Hour)
	if err == nil {
		t.Error("expected error for from==to")
	}
}

func TestKrakenBackfill_UnsupportedGranularity(t *testing.T) {
	m, _ := DefaultPairs()
	s := NewStreamer(m)
	xlm, _ := canonical.NewCryptoAsset("XLM")
	usd, _ := canonical.NewFiatAsset("USD")
	p, _ := canonical.NewPair(xlm, usd)
	// 7m isn't in Kraken's set.
	_, err := s.Backfill(context.Background(), p,
		time.Now().Add(-time.Hour), time.Now(), 7*time.Minute)
	if err == nil {
		t.Error("expected error for unsupported granularity")
	}
}

func TestGranularityToMinutes(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want int
		err  bool
	}{
		{1 * time.Minute, 1, false},
		{15 * time.Minute, 15, false},
		{1 * time.Hour, 60, false},
		{4 * time.Hour, 240, false},
		{24 * time.Hour, 1440, false},
		{7 * 24 * time.Hour, 10080, false},
		{15 * 24 * time.Hour, 21600, false},
		{2 * time.Minute, 0, true},
		{6 * time.Hour, 0, true}, // not in Kraken's set
	}
	for _, tc := range cases {
		got, err := granularityToMinutes(tc.in)
		if tc.err {
			if err == nil {
				t.Errorf("%v: want err, got %d", tc.in, got)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("%v: got (%d, %v) want %d", tc.in, got, err, tc.want)
		}
	}
}

func TestKrakenBackfill_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"error":["EGeneral:Invalid arguments"]}`))
	}))
	defer srv.Close()
	m, _ := DefaultPairs()
	s := NewStreamer(m)
	s.Endpoint = srv.URL
	xlm, _ := canonical.NewCryptoAsset("XLM")
	usd, _ := canonical.NewFiatAsset("USD")
	p, _ := canonical.NewPair(xlm, usd)
	// Must have a mapping for XLM/USD, use the existing "XLM/USD" →
	// pair in DefaultPairs.
	_, err := s.Backfill(context.Background(), p,
		time.Unix(1, 0), time.Unix(100, 0), time.Hour)
	if err == nil {
		t.Error("expected error from Kraken API error array")
	}
}
