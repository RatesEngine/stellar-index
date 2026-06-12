package binance

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
)

// synthesiseKlines builds a slice of Binance-shape kline rows for
// the given count, starting at `startMs` with `intervalMs` gap.
// Price drifts upward by 0.00001 per bucket, volume is a flat 100
// XLM — enough signal to assert on ordering and field-mapping.
func synthesiseKlines(count int, startMs, intervalMs int64) []kline {
	out := make([]kline, count)
	for i := 0; i < count; i++ {
		openMs := startMs + int64(i)*intervalMs
		closeMs := openMs + intervalMs - 1
		closePrice := 0.17582 + 0.00001*float64(i)
		priceStr := strconv.FormatFloat(closePrice, 'f', 5, 64)
		baseVol := "100.00000000"
		quoteVol := strconv.FormatFloat(closePrice*100, 'f', 8, 64)
		out[i] = kline{
			float64(openMs),  // open time
			"0.17582",        // open
			"0.17600",        // high
			"0.17500",        // low
			priceStr,         // close
			baseVol,          // base vol
			float64(closeMs), // close time
			quoteVol,         // quote vol
			float64(150),     // trade count
			"50.0",           // taker buy base
			"8.8",            // taker buy quote
			"0",              // unused
		}
	}
	return out
}

// newTestREST serves the Binance klines endpoint. Replays the
// supplied candle sets in order — each GET consumes one set,
// matching Binance's paginate-by-startTime behaviour when the
// caller advances after a full 1000-row response.
func newTestREST(t *testing.T, responses [][]kline) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	call := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != klinesPath {
			t.Errorf("unexpected path %q", r.URL.Path)
			http.Error(w, "nope", http.StatusNotFound)
			return
		}
		mu.Lock()
		idx := call
		call++
		mu.Unlock()

		var payload []kline
		if idx < len(responses) {
			payload = responses[idx]
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
}

func TestBackfill_SinglePage(t *testing.T) {
	const startMs = int64(1_745_000_000_000)
	const hourMs = int64(3_600_000)

	candles := synthesiseKlines(5, startMs, hourMs)
	srv := newTestREST(t, [][]kline{candles, {}}) // 2nd page empty → stop
	defer srv.Close()

	s := NewStreamer(mustPairMapBF(t))
	s.Endpoint = srv.URL // non-ws URL → restBase uses it directly

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	xlm, _ := canonical.NewCryptoAsset("XLM")
	usdt, _ := canonical.NewCryptoAsset("USDT")
	pair, _ := canonical.NewPair(xlm, usdt)

	from := time.UnixMilli(startMs).UTC()
	to := from.Add(6 * time.Hour)

	trades, err := s.Backfill(ctx, pair, from, to, 1*time.Hour)
	if err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	if len(trades) != 5 {
		t.Fatalf("expected 5 trades, got %d", len(trades))
	}
	// First trade's close_time = startMs + hourMs - 1
	wantCloseMs := startMs + hourMs - 1
	if trades[0].Timestamp.UnixMilli() != wantCloseMs {
		t.Errorf("trade[0] close_ms = %d want %d",
			trades[0].Timestamp.UnixMilli(), wantCloseMs)
	}
	// Base volume = 100 × 10^8 = 10_000_000_000
	want := big.NewInt(10_000_000_000)
	if trades[0].BaseAmount.BigInt().Cmp(want) != 0 {
		t.Errorf("BaseAmount = %s want %s", trades[0].BaseAmount, want)
	}
	// tx_hash stable + 64-hex.
	if len(trades[0].TxHash) != 64 {
		t.Errorf("TxHash len = %d", len(trades[0].TxHash))
	}
	// Hash determinism: recomputing backfillTxHash with the same
	// inputs (symbol + close_ms) must match what Backfill stored.
	// This is the "rerunnable backfill hits the same primary key"
	// invariant — no need for a second HTTP call, the formula is
	// pure.
	wantHash := backfillTxHash("XLMUSDT", wantCloseMs)
	if trades[0].TxHash != wantHash {
		t.Errorf("tx_hash = %s want %s (formula drift)", trades[0].TxHash, wantHash)
	}
}

func TestBackfill_Pagination(t *testing.T) {
	// Simulate 1800 candles returned in two paginated responses of
	// 1000 and 800. The backfiller must advance startTime after
	// the first full page and issue the second request.
	const startMs = int64(1_745_000_000_000)
	const hourMs = int64(3_600_000)

	page1 := synthesiseKlines(1000, startMs, hourMs)
	page2 := synthesiseKlines(800, startMs+1000*hourMs, hourMs)
	srv := newTestREST(t, [][]kline{page1, page2})
	defer srv.Close()

	s := NewStreamer(mustPairMapBF(t))
	s.Endpoint = srv.URL

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	xlm, _ := canonical.NewCryptoAsset("XLM")
	usdt, _ := canonical.NewCryptoAsset("USDT")
	pair, _ := canonical.NewPair(xlm, usdt)

	trades, err := s.Backfill(ctx, pair, time.UnixMilli(startMs).UTC(),
		time.UnixMilli(startMs+1800*hourMs+1).UTC(),
		1*time.Hour)
	if err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	if len(trades) != 1800 {
		t.Fatalf("expected 1800 trades across two pages, got %d", len(trades))
	}
	// Timestamps must be monotonic ascending (synthesiseKlines
	// advances by intervalMs per row, both pages).
	for i := 1; i < len(trades); i++ {
		if !trades[i].Timestamp.After(trades[i-1].Timestamp) {
			t.Errorf("trades[%d] ts %v not > trades[%d] ts %v",
				i, trades[i].Timestamp, i-1, trades[i-1].Timestamp)
			break
		}
	}
}

func TestBackfill_RejectsInvalidRange(t *testing.T) {
	s := NewStreamer(mustPairMapBF(t))
	now := time.Now()
	_, err := s.Backfill(context.Background(), mustPair(t), now, now, time.Hour)
	if err == nil {
		t.Error("expected error for from == to")
	}
	_, err = s.Backfill(context.Background(), mustPair(t), now.Add(time.Hour), now, time.Hour)
	if err == nil {
		t.Error("expected error for from > to")
	}
}

func TestBackfill_RejectsUnsupportedGranularity(t *testing.T) {
	s := NewStreamer(mustPairMapBF(t))
	_, err := s.Backfill(context.Background(), mustPair(t),
		time.Now(), time.Now().Add(time.Hour), 7*time.Minute) // not a Binance interval
	if err == nil {
		t.Error("expected error for unsupported granularity")
	}
}

func TestGranularityToInterval(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
		err  bool
	}{
		{1 * time.Minute, "1m", false},
		{15 * time.Minute, "15m", false},
		{1 * time.Hour, "1h", false},
		{4 * time.Hour, "4h", false},
		{24 * time.Hour, "1d", false},
		{7 * 24 * time.Hour, "1w", false},
		{10 * time.Second, "", true},
		{2 * time.Minute, "", true}, // not in Binance set
	}
	for _, tc := range cases {
		got, err := granularityToInterval(tc.in)
		if tc.err {
			if err == nil {
				t.Errorf("granularity %v: want err, got %q", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("granularity %v: unexpected err %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("granularity %v: got %q want %q", tc.in, got, tc.want)
		}
	}
}

func TestBackfill_EmptyResponse(t *testing.T) {
	srv := newTestREST(t, [][]kline{{}})
	defer srv.Close()

	s := NewStreamer(mustPairMapBF(t))
	s.Endpoint = srv.URL

	trades, err := s.Backfill(context.Background(), mustPair(t),
		time.UnixMilli(1_745_000_000_000).UTC(),
		time.UnixMilli(1_745_010_000_000).UTC(),
		1*time.Hour)
	if err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	if len(trades) != 0 {
		t.Errorf("empty response should yield 0 trades, got %d", len(trades))
	}
}

func TestBackfill_ZeroVolumeCandlesSkipped(t *testing.T) {
	// A candle with zero volume provides no signal — should be
	// dropped so downstream VWAP math doesn't divide by zero.
	const startMs = int64(1_745_000_000_000)
	const hourMs = int64(3_600_000)
	k := synthesiseKlines(3, startMs, hourMs)
	// Zero out volume on the middle candle.
	k[1][5] = "0.00000000"
	k[1][7] = "0.00000000"
	srv := newTestREST(t, [][]kline{k, {}})
	defer srv.Close()

	s := NewStreamer(mustPairMapBF(t))
	s.Endpoint = srv.URL
	trades, err := s.Backfill(context.Background(), mustPair(t),
		time.UnixMilli(startMs).UTC(),
		time.UnixMilli(startMs+5*hourMs).UTC(),
		1*time.Hour)
	if err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	if len(trades) != 2 {
		t.Errorf("expected 2 trades (zero-vol dropped), got %d", len(trades))
	}
}

func TestBackfill_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer srv.Close()

	s := NewStreamer(mustPairMapBF(t))
	s.Endpoint = srv.URL
	_, err := s.Backfill(context.Background(), mustPair(t),
		time.UnixMilli(1).UTC(),
		time.UnixMilli(2).UTC(),
		1*time.Hour)
	if err == nil {
		t.Error("expected error on HTTP 429")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("error should mention status code: %v", err)
	}
}

// mustPair / mustPairMapBF — rename-suffixed to avoid conflicts with
// helpers in streamer_test.go / parse_test.go.
func mustPair(t *testing.T) canonical.Pair {
	t.Helper()
	xlm, _ := canonical.NewCryptoAsset("XLM")
	usdt, _ := canonical.NewCryptoAsset("USDT")
	p, err := canonical.NewPair(xlm, usdt)
	if err != nil {
		t.Fatalf("NewPair: %v", err)
	}
	return p
}

func mustPairMapBF(t *testing.T) map[string]canonical.Pair {
	t.Helper()
	m, err := DefaultPairs()
	if err != nil {
		t.Fatalf("DefaultPairs: %v", err)
	}
	return m
}

// Shut up unused-import warnings when fmt isn't used in other paths.
var _ = fmt.Sprintf
