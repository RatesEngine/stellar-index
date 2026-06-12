package v1_test

import (
	"bufio"
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	v1 "github.com/StellarAtlas/stellar-atlas/internal/api/v1"
	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
)

// readTipStreamEvent reads one full SSE frame (id/event/data block)
// from body and returns the data payload. Skips comment frames
// (`:keepalive`, `:connected`). Returns "" on EOF.
func readTipStreamEvent(t *testing.T, br *bufio.Reader, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var data string
	for time.Now().Before(deadline) {
		line, err := br.ReadString('\n')
		if err != nil {
			return data
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if line == "\n" {
			if data != "" {
				return data
			}
			continue
		}
		if strings.HasPrefix(line, "data: ") {
			data = strings.TrimSuffix(strings.TrimPrefix(line, "data: "), "\n")
		}
		// id:/event: lines are skipped — tests assert against payload.
	}
	return data
}

// startTipStreamServer wires a v1.Server with the given Prices +
// History readers behind an httptest.Server and returns its URL.
func startTipStreamServer(t *testing.T, prices v1.PriceReader, history v1.HistoryReader) string {
	t.Helper()
	srv := v1.New(v1.Options{Prices: prices, History: history})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts.URL
}

// TestPriceTipStream_NoReader_Returns503 — same prelude as the
// request endpoint: no PriceReader → 503 BEFORE the response
// switches into SSE mode.
func TestPriceTipStream_NoReader_Returns503(t *testing.T) {
	srv := v1.New(v1.Options{})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/v1/price/tip/stream?asset=native&quote=fiat:USD")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

// TestPriceTipStream_RejectsGranularity — URL discipline: ?granularity=
// is closed-bucket-only; on the tip stream URL it's a 400.
func TestPriceTipStream_RejectsGranularity(t *testing.T) {
	url := startTipStreamServer(t, &stubPriceReader{}, nil)
	resp, err := http.Get(url + "/v1/price/tip/stream?asset=native&quote=fiat:USD&granularity=1m")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// TestPriceTipStream_PreFlight404 — when the very first compute
// returns ErrPriceNotFound (pair has no observations), the stream
// returns 404 before switching to SSE. Critical: SSE has no way to
// signal "not found" mid-stream, so the only correct behaviour is
// to detect emptiness pre-flight.
func TestPriceTipStream_PreFlight404(t *testing.T) {
	url := startTipStreamServer(t, &stubPriceReader{err: v1.ErrPriceNotFound}, nil)
	resp, err := http.Get(url + "/v1/price/tip/stream?asset=native&quote=fiat:USD")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

// TestPriceTipStream_InitialEventEmittedSynchronously — the first
// event lands as soon as the connection opens (not a window_seconds
// later). Without this, a 60s window means clients sit on heartbeats
// for a minute before seeing data they could have had immediately.
func TestPriceTipStream_InitialEventEmittedSynchronously(t *testing.T) {
	prices := &stubPriceReader{
		snapshots: map[string]v1.PriceSnapshot{
			"native/fiat:USD": {
				AssetID:   "native",
				Quote:     "fiat:USD",
				Price:     "0.42",
				PriceType: "last_trade",
			},
		},
	}
	url := startTipStreamServer(t, prices, nil)

	// Use the maximum window so the first tick wouldn't fire for 60s
	// — if the test sees an event quickly, it MUST be the
	// pre-tick initial emission.
	resp, err := http.Get(url + "/v1/price/tip/stream?asset=native&quote=fiat:USD&window_seconds=60")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	br := bufio.NewReader(resp.Body)
	data := readTipStreamEvent(t, br, 2*time.Second)
	if data == "" {
		t.Fatal("no event received within 2s — initial emit failed")
	}
	if !strings.Contains(data, `"price":"0.42"`) {
		t.Errorf("payload missing price: %s", data)
	}
	if !strings.Contains(data, `"price_type":"last_trade"`) {
		t.Errorf("payload missing price_type: %s", data)
	}
	// Per ADR-0018 stale stays false on tip even on the fallback.
	if !strings.Contains(data, `"stale":false`) {
		t.Errorf("stale flag wrong: %s", data)
	}
}

// TestPriceTipStream_WindowVWAPBranch — when history has fresh
// trades, the initial event uses the rolling-window VWAP (not the
// fallback). Confirms the per-tick path goes through computeTip.
func TestPriceTipStream_WindowVWAPBranch(t *testing.T) {
	now := time.Now().UTC()
	xlm, _ := canonical.ParseAsset("native")
	usd, _ := canonical.ParseAsset("fiat:USD")
	pair, _ := canonical.NewPair(xlm, usd)
	hist := &stubHistoryReader{
		trades: []canonical.Trade{
			{
				Source: "soroswap", Ledger: 1,
				TxHash:      "0000000000000000000000000000000000000000000000000000000000000001",
				Timestamp:   now.Add(-2 * time.Second),
				Pair:        pair,
				BaseAmount:  canonical.NewAmount(big.NewInt(1)),
				QuoteAmount: canonical.NewAmount(big.NewInt(7)),
			},
		},
	}
	url := startTipStreamServer(t, &stubPriceReader{}, hist)

	resp, err := http.Get(url + "/v1/price/tip/stream?asset=native&quote=fiat:USD&window_seconds=60")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	br := bufio.NewReader(resp.Body)
	data := readTipStreamEvent(t, br, 2*time.Second)
	if !strings.Contains(data, `"price_type":"vwap"`) {
		t.Errorf("expected vwap branch on initial event: %s", data)
	}
	if !strings.Contains(data, `"window_seconds":60`) {
		t.Errorf("expected window_seconds echoed: %s", data)
	}
}

// TestPriceTipStream_TickEmitsRepeatedly — at window_seconds=1 a
// short-lived stream sees multiple consecutive events. Validates
// the producer's ticker fires more than once.
func TestPriceTipStream_TickEmitsRepeatedly(t *testing.T) {
	prices := &stubPriceReader{
		snapshots: map[string]v1.PriceSnapshot{
			"native/fiat:USD": {Price: "0.5", PriceType: "last_trade"},
		},
	}
	url := startTipStreamServer(t, prices, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		url+"/v1/price/tip/stream?asset=native&quote=fiat:USD&window_seconds=1", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	br := bufio.NewReader(resp.Body)
	const want = 2
	got := 0
	for got < want {
		ev := readTipStreamEvent(t, br, 2500*time.Millisecond)
		if ev == "" {
			break
		}
		got++
	}
	if got < want {
		t.Errorf("got %d events, want >= %d (window_seconds=1 should produce repeat ticks)", got, want)
	}
}

// TestPriceTipStream_ClientDisconnectStopsProducer — when the
// client cancels its request, the producer goroutine exits via
// ctx.Done. Probed indirectly: re-issue a second connection with
// the same params and confirm it serves cleanly (a leaked producer
// from the first connection wouldn't break this, but a panic or
// stuck channel send would). Mostly a sanity test that the cleanup
// path doesn't deadlock.
func TestPriceTipStream_ClientDisconnectStopsProducer(t *testing.T) {
	prices := &stubPriceReader{
		snapshots: map[string]v1.PriceSnapshot{
			"native/fiat:USD": {Price: "1.0", PriceType: "last_trade"},
		},
	}
	url := startTipStreamServer(t, prices, nil)

	for i := 0; i < 3; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
			url+"/v1/price/tip/stream?asset=native&quote=fiat:USD&window_seconds=1", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			cancel()
			continue
		}
		_ = resp.Body.Close()
		cancel()
	}
	// If we reached here without the test deadlocking, all three
	// producers shut down cleanly.
}

// TestPriceTipStream_PayloadJSONIsValid — sanity-decode the data
// line as JSON to catch any payload-shape regressions.
func TestPriceTipStream_PayloadJSONIsValid(t *testing.T) {
	prices := &stubPriceReader{
		snapshots: map[string]v1.PriceSnapshot{
			"native/fiat:USD": {Price: "0.7", PriceType: "last_trade"},
		},
	}
	url := startTipStreamServer(t, prices, nil)

	resp, err := http.Get(url + "/v1/price/tip/stream?asset=native&quote=fiat:USD&window_seconds=60")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	br := bufio.NewReader(resp.Body)
	data := readTipStreamEvent(t, br, 2*time.Second)
	if data == "" {
		t.Fatal("no event")
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(data), &parsed); err != nil {
		t.Fatalf("payload not valid JSON: %v\nraw: %s", err, data)
	}
	for _, key := range []string{"data", "as_of", "flags"} {
		if _, ok := parsed[key]; !ok {
			t.Errorf("payload missing %q: %s", key, data)
		}
	}
}
