package kraken

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// newTestKrakenServer plays back a scripted Kraken v2 session:
//
//  1. Sends a status frame on connect.
//  2. Expects a subscribe request; reads it to validate the symbols
//     the Streamer sent.
//  3. Replies with a subscribe-ack.
//  4. Sends trade-channel updates.
//
// Exposed via httptest so we exercise the full JSON-subscribe
// handshake without hitting real Kraken infra.
func newTestKrakenServer(t *testing.T, tradeFrames []string, capturedSub *subscribeReq) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		defer func() { _ = conn.Close(websocket.StatusNormalClosure, "bye") }()

		// 1. Status frame.
		_ = conn.Write(r.Context(), websocket.MessageText,
			[]byte(`{"channel":"status","data":[{"system":"online","api_version":"v2"}]}`))

		// 2. Read the subscribe request the client sent.
		_, subRaw, err := conn.Read(r.Context())
		if err != nil {
			return
		}
		if capturedSub != nil {
			_ = json.Unmarshal(subRaw, capturedSub)
		}

		// 3. Ack.
		_ = conn.Write(r.Context(), websocket.MessageText,
			[]byte(`{"method":"subscribe","success":true,"result":{"channel":"trade"}}`))

		// 4. Trade frames.
		for _, f := range tradeFrames {
			if err := conn.Write(r.Context(), websocket.MessageText, []byte(f)); err != nil {
				return
			}
		}

		// Hold the connection open until client cancels.
		<-r.Context().Done()
	}))
}

func replaceScheme(httpURL string) string {
	if strings.HasPrefix(httpURL, "https://") {
		return "wss://" + strings.TrimPrefix(httpURL, "https://")
	}
	return "ws://" + strings.TrimPrefix(httpURL, "http://")
}

func TestStreamer_EndToEnd(t *testing.T) {
	frames := []string{
		`{"channel":"trade","type":"update","data":[{"symbol":"XLM/USD","side":"buy","qty":100,"price":0.17582,"ord_type":"market","trade_id":1,"timestamp":"2026-04-24T00:00:00Z"}]}`,
		`{"channel":"heartbeat"}`,
		`{"channel":"trade","type":"update","data":[{"symbol":"XLM/EUR","side":"sell","qty":50,"price":0.16,"ord_type":"limit","trade_id":2,"timestamp":"2026-04-24T00:00:01Z"}]}`,
	}
	var capturedSub subscribeReq
	srv := newTestKrakenServer(t, frames, &capturedSub)
	defer srv.Close()

	s := NewStreamer(mustPairMap(t))
	s.Endpoint = replaceScheme(srv.URL)

	pairs, err := DefaultPairList()
	if err != nil {
		t.Fatalf("DefaultPairList: %v", err)
	}
	// Scope the test to a couple of pairs the fixture emits — the
	// real DefaultPairList has 8 symbols, overkill for this test.
	xlm, _ := canonical.NewCryptoAsset("XLM")
	usd, _ := canonical.NewFiatAsset("USD")
	eur, _ := canonical.NewFiatAsset("EUR")
	xlmUsd, _ := canonical.NewPair(xlm, usd)
	xlmEur, _ := canonical.NewPair(xlm, eur)
	pairs = []canonical.Pair{xlmUsd, xlmEur}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := s.Start(ctx, pairs)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	got := []canonical.Trade{}
loop:
	for len(got) < 2 {
		select {
		case tr, ok := <-out:
			if !ok {
				break loop
			}
			got = append(got, tr)
		case <-ctx.Done():
			t.Fatalf("timeout waiting for trades; got %d", len(got))
		}
	}
	cancel()

	if len(got) != 2 {
		t.Fatalf("expected 2 trades (heartbeat suppressed), got %d", len(got))
	}
	if got[0].Source != "kraken" {
		t.Errorf("source = %q want kraken", got[0].Source)
	}
	// Subscribe request should have contained both symbols.
	if capturedSub.Method != "subscribe" {
		t.Errorf("subscribe method = %q want subscribe", capturedSub.Method)
	}
	if capturedSub.Params.Channel != "trade" {
		t.Errorf("subscribe channel = %q want trade", capturedSub.Params.Channel)
	}
	seen := map[string]bool{}
	for _, sym := range capturedSub.Params.Symbol {
		seen[sym] = true
	}
	if !seen["XLM/USD"] || !seen["XLM/EUR"] {
		t.Errorf("subscribe symbols missing XLM/USD or XLM/EUR; got %v",
			capturedSub.Params.Symbol)
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
	s := NewStreamer(mustPairMap(t))
	// LINK/USD isn't in DefaultPairs.
	link, _ := canonical.NewCryptoAsset("LINK")
	usd, _ := canonical.NewFiatAsset("USD")
	p, _ := canonical.NewPair(link, usd)
	_, err := s.Start(context.Background(), []canonical.Pair{p})
	if err == nil {
		t.Error("expected error for pair not in PairMap")
	}
}

func mustPairMap(t *testing.T) map[string]canonical.Pair {
	t.Helper()
	m, err := DefaultPairs()
	if err != nil {
		t.Fatalf("DefaultPairs: %v", err)
	}
	return m
}
