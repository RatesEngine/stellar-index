package bitstamp

import (
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
)

func mustPairs(t *testing.T) map[string]canonical.Pair {
	t.Helper()
	m, err := DefaultPairs()
	if err != nil {
		t.Fatalf("DefaultPairs: %v", err)
	}
	return m
}

func TestParseFrame_TradeHappyPath(t *testing.T) {
	raw := []byte(`{
      "event":"trade",
      "channel":"live_trades_xlmusd",
      "data":{
        "id":123456789,
        "timestamp":"1745000000",
        "microtimestamp":"1745000000123456",
        "amount":100.5,
        "amount_str":"100.5",
        "price":0.17582,
        "price_str":"0.17582",
        "type":0,
        "buy_order_id":1111,
        "sell_order_id":2222
      }
    }`)
	trade, isTrade, err := parseFrame(raw, mustPairs(t))
	if err != nil {
		t.Fatalf("parseFrame: %v", err)
	}
	if !isTrade {
		t.Fatal("expected isTrade=true")
	}
	if trade.Source != "bitstamp" {
		t.Errorf("Source = %q", trade.Source)
	}
	// Timestamp: 1745000000.123456s → UnixMicro
	want := time.UnixMicro(1_745_000_000_123_456).UTC()
	if !trade.Timestamp.Equal(want) {
		t.Errorf("Timestamp = %v want %v", trade.Timestamp, want)
	}
	// Base = 100.5 × 10^8 = 10_050_000_000
	wantBase := big.NewInt(10_050_000_000)
	if trade.BaseAmount.BigInt().Cmp(wantBase) != 0 {
		t.Errorf("BaseAmount = %s want %s", trade.BaseAmount, wantBase)
	}
	// Quote = 100.5 × 0.17582 = 17.670 → 10^8 = 1_767_039_000
	// 1005 × 17582 = 17,670,910 (at 10^(4+5)=10^9)
	// ... reconstruct carefully:
	// base integer-form = 10_050_000_000 (10^8)
	// price integer-form = 17_582_000 (10^8)
	// product = 176_699_100_000_000_000
	// ÷ 10^8 = 1_766_991_000
	wantQuote := big.NewInt(1_766_991_000)
	if trade.QuoteAmount.BigInt().Cmp(wantQuote) != 0 {
		t.Errorf("QuoteAmount = %s want %s", trade.QuoteAmount, wantQuote)
	}
	if len(trade.TxHash) != 64 {
		t.Errorf("TxHash len = %d want 64", len(trade.TxHash))
	}
	xlm, _ := canonical.NewCryptoAsset("XLM")
	usd, _ := canonical.NewFiatAsset("USD")
	if !trade.Pair.Base.Equal(xlm) || !trade.Pair.Quote.Equal(usd) {
		t.Errorf("Pair = %+v want XLM/USD", trade.Pair)
	}
}

func TestParseFrame_RequestReconnect(t *testing.T) {
	// Bitstamp's ~hourly request-reconnect event. parseFrame
	// must surface this as ErrRequestedReconnect so the streamer
	// closes + reconnects.
	raw := []byte(`{"event":"bts:request_reconnect","channel":"","data":""}`)
	_, _, err := parseFrame(raw, mustPairs(t))
	if !errors.Is(err, ErrRequestedReconnect) {
		t.Errorf("expected ErrRequestedReconnect, got %v", err)
	}
}

func TestParseFrame_SubscriptionSucceededIgnored(t *testing.T) {
	raw := []byte(`{"event":"bts:subscription_succeeded","channel":"live_trades_xlmusd","data":{}}`)
	trade, isTrade, err := parseFrame(raw, mustPairs(t))
	if err != nil {
		t.Errorf("should not err on subscription_succeeded, got %v", err)
	}
	if isTrade {
		t.Error("subscription_succeeded should not be treated as trade")
	}
	_ = trade
}

func TestParseFrame_UnknownEventIgnored(t *testing.T) {
	// Bitstamp occasionally adds new event types; unknown ones
	// must not be treated as errors.
	raw := []byte(`{"event":"bts:something_future","channel":"","data":{}}`)
	_, isTrade, err := parseFrame(raw, mustPairs(t))
	if err != nil {
		t.Errorf("unexpected err: %v", err)
	}
	if isTrade {
		t.Error("unknown event should not be treated as trade")
	}
}

func TestParseFrame_TradeOnUnknownChannel(t *testing.T) {
	// A trade arrived on a channel not in our PairMap — MATIC is in
	// the ADR-0014 allow-list but intentionally not in DefaultPairs
	// (skipped pending MATIC→POL migration), making it the stable
	// "known unknown" placeholder. Parser surfaces ErrUnknownChannel.
	raw := []byte(`{
      "event":"trade",
      "channel":"live_trades_maticusd",
      "data":{"id":1,"amount_str":"1","price_str":"1","microtimestamp":"1745000000000000","type":0}
    }`)
	_, _, err := parseFrame(raw, mustPairs(t))
	if !errors.Is(err, ErrUnknownChannel) {
		t.Errorf("expected ErrUnknownChannel, got %v", err)
	}
}

func TestParseFrame_MalformedJSON(t *testing.T) {
	_, _, err := parseFrame([]byte(`{not json`), mustPairs(t))
	if !errors.Is(err, ErrMalformedFrame) {
		t.Errorf("expected ErrMalformedFrame, got %v", err)
	}
}

func TestParseFrame_MissingStringFields(t *testing.T) {
	// Frame missing both amount_str and price_str — must reject.
	raw := []byte(`{
      "event":"trade","channel":"live_trades_xlmusd",
      "data":{"id":1,"amount":100.5,"price":0.1,"microtimestamp":"1","type":0}
    }`)
	_, _, err := parseFrame(raw, mustPairs(t))
	if !errors.Is(err, ErrMalformedFrame) {
		t.Errorf("expected ErrMalformedFrame for missing *_str, got %v", err)
	}
}

func TestParseMicrotimestamp_FallbackToSeconds(t *testing.T) {
	// When microtimestamp is absent, fall back to seconds.
	ts, err := parseMicrotimestamp("", "1745000000")
	if err != nil {
		t.Fatalf("parseMicrotimestamp: %v", err)
	}
	if ts.Unix() != 1_745_000_000 {
		t.Errorf("ts = %v, Unix = %d want 1745000000", ts, ts.Unix())
	}
}
