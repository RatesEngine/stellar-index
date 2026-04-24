package binance

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// newTestWSServer spins up an httptest server that accepts a single
// WebSocket connection, writes a fixed set of aggTrade frames, then
// holds the connection open until ctx cancellation. Exposed so tests
// can assert on the Streamer's end-to-end behaviour without hitting
// real Binance infra.
func newTestWSServer(t *testing.T, frames []string) *httptest.Server {
	t.Helper()

	var mu sync.Mutex
	var connectionCount int

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		mu.Lock()
		connectionCount++
		mu.Unlock()
		defer func() { _ = conn.Close(websocket.StatusNormalClosure, "bye") }()

		// Dump the fixture frames.
		for _, f := range frames {
			if err := conn.Write(r.Context(), websocket.MessageText, []byte(f)); err != nil {
				return
			}
		}
		// Keep the connection open until the client cancels. The
		// real Binance holds connections open for days; we mimic
		// that so the test's Start goroutine is naturally driven
		// by ctx cancellation, not by server-side EOF.
		<-r.Context().Done()
	}))
}

// replaceScheme swaps http → ws (or https → wss) since httptest
// serves on http:// but coder/websocket expects ws://.
func replaceScheme(httpURL string) string {
	if strings.HasPrefix(httpURL, "https://") {
		return "wss://" + strings.TrimPrefix(httpURL, "https://")
	}
	return "ws://" + strings.TrimPrefix(httpURL, "http://")
}

func TestStreamer_EndToEnd(t *testing.T) {
	// Two fixture frames simulate a busy second on XLMUSDT.
	frames := []string{
		`{"stream":"xlmusdt@aggTrade","data":{"e":"aggTrade","E":1745000000000,"s":"XLMUSDT","a":1,"p":"0.17582","q":"152.34","f":1,"l":2,"T":1745000000100,"m":true}}`,
		`{"stream":"xlmusdt@aggTrade","data":{"e":"aggTrade","E":1745000001000,"s":"XLMUSDT","a":2,"p":"0.17590","q":"200.00","f":3,"l":4,"T":1745000001100,"m":false}}`,
	}
	srv := newTestWSServer(t, frames)
	defer srv.Close()

	s := NewStreamer(buildPairMap(t))
	s.Endpoint = replaceScheme(srv.URL)

	xlm, _ := canonical.NewCryptoAsset("XLM")
	usdt, _ := canonical.NewCryptoAsset("USDT")
	pair, _ := canonical.NewPair(xlm, usdt)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := s.Start(ctx, []canonical.Pair{pair})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Read two trades, then cancel.
	got := []canonical.Trade{}
loop:
	for len(got) < 2 {
		select {
		case trade, ok := <-out:
			if !ok {
				break loop
			}
			got = append(got, trade)
		case <-ctx.Done():
			t.Fatalf("timeout waiting for trades; got %d", len(got))
		}
	}
	cancel()

	if len(got) != 2 {
		t.Fatalf("expected 2 trades, got %d", len(got))
	}
	// Verify trade ordering + stamped fields.
	if got[0].Timestamp.UnixMilli() != 1745000000100 {
		t.Errorf("trade[0] ts = %d want 1745000000100", got[0].Timestamp.UnixMilli())
	}
	if got[1].Timestamp.UnixMilli() != 1745000001100 {
		t.Errorf("trade[1] ts = %d want 1745000001100", got[1].Timestamp.UnixMilli())
	}
	if got[0].Source != "binance" {
		t.Errorf("source = %q want binance", got[0].Source)
	}
	if !got[0].Pair.Base.Equal(xlm) {
		t.Errorf("trade[0] base = %+v want XLM", got[0].Pair.Base)
	}
}

func TestStreamer_RejectsEmptyPairs(t *testing.T) {
	s := NewStreamer(map[string]canonical.Pair{})
	_, err := s.Start(context.Background(), nil)
	if err == nil {
		t.Error("expected error on empty pairs")
	}
}

func TestStreamer_RejectsUnconfiguredPair(t *testing.T) {
	s := NewStreamer(buildPairMap(t))
	// Construct a pair that's NOT in the map (e.g. DOGE/USDT —
	// DOGE is allow-listed as a crypto ticker but we didn't add
	// DOGEUSDT to buildPairMap).
	doge, _ := canonical.NewCryptoAsset("DOGE")
	usdt, _ := canonical.NewCryptoAsset("USDT")
	pair, _ := canonical.NewPair(doge, usdt)
	_, err := s.Start(context.Background(), []canonical.Pair{pair})
	if err == nil {
		t.Error("expected error for pair not in PairMap")
	}
}

func TestStreamer_BuildStreamURL(t *testing.T) {
	s := NewStreamer(buildPairMap(t))
	s.Endpoint = "wss://stream.binance.com:9443/stream"
	u, err := s.buildStreamURL([]string{"XLMUSDT", "BTCUSDT"})
	if err != nil {
		t.Fatalf("buildStreamURL: %v", err)
	}
	// Order of the streams query param is preserved; case lowered.
	if !strings.Contains(u, "streams=xlmusdt%40aggTrade%2Fbtcusdt%40aggTrade") {
		t.Errorf("URL missing expected streams query: %s", u)
	}
}
