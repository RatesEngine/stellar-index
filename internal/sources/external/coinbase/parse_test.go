package coinbase

import (
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
)

func mustPairs(t *testing.T) map[string]canonical.Pair {
	t.Helper()
	m, err := DefaultPairs()
	if err != nil {
		t.Fatalf("DefaultPairs: %v", err)
	}
	return m
}

func TestParseFrame_MatchHappyPath(t *testing.T) {
	raw := []byte(`{
      "type":"match",
      "trade_id":123456,
      "maker_order_id":"m",
      "taker_order_id":"t",
      "side":"buy",
      "size":"100.00000000",
      "price":"0.17582000",
      "product_id":"XLM-USD",
      "sequence":12345,
      "time":"2026-04-24T00:00:00.123456Z"
    }`)
	trade, isTrade, err := parseFrame(raw, mustPairs(t))
	if err != nil {
		t.Fatalf("parseFrame: %v", err)
	}
	if !isTrade {
		t.Fatal("expected isTrade=true")
	}
	if trade.Source != "coinbase" {
		t.Errorf("Source = %q", trade.Source)
	}
	wantBase := big.NewInt(10_000_000_000) // 100 × 10^8
	if trade.BaseAmount.BigInt().Cmp(wantBase) != 0 {
		t.Errorf("BaseAmount = %s want %s", trade.BaseAmount, wantBase)
	}
	// price at 10^8 = 17_582_000; quote = 10^10 × 17_582_000 / 10^8 = 1_758_200_000
	wantQuote := big.NewInt(1_758_200_000)
	if trade.QuoteAmount.BigInt().Cmp(wantQuote) != 0 {
		t.Errorf("QuoteAmount = %s want %s", trade.QuoteAmount, wantQuote)
	}
	wantTime, _ := time.Parse(time.RFC3339Nano, "2026-04-24T00:00:00.123456Z")
	if !trade.Timestamp.Equal(wantTime) {
		t.Errorf("Timestamp = %v want %v", trade.Timestamp, wantTime)
	}
	if len(trade.TxHash) != 64 {
		t.Errorf("TxHash len = %d", len(trade.TxHash))
	}
}

func TestParseFrame_LastMatchEmittedLikeMatch(t *testing.T) {
	// last_match is Coinbase's on-subscribe prime frame — one per
	// product, with a real historical trade. Emitted same as match.
	raw := []byte(`{
      "type":"last_match","trade_id":1,"side":"sell","size":"1","price":"50000",
      "product_id":"BTC-USD","sequence":1,"time":"2026-04-24T00:00:00Z"
    }`)
	_, isTrade, err := parseFrame(raw, mustPairs(t))
	if err != nil {
		t.Fatalf("parseFrame: %v", err)
	}
	if !isTrade {
		t.Fatal("last_match should emit as trade")
	}
}

func TestParseFrame_SubscriptionsIgnored(t *testing.T) {
	// Ack frame Coinbase sends after we subscribe.
	raw := []byte(`{"type":"subscriptions","channels":[{"name":"matches","product_ids":["XLM-USD"]}]}`)
	_, isTrade, err := parseFrame(raw, mustPairs(t))
	if err != nil {
		t.Errorf("should not err on subscriptions ack: %v", err)
	}
	if isTrade {
		t.Error("subscriptions ack should not be treated as trade")
	}
}

func TestParseFrame_ErrorFrameSurfacesRejection(t *testing.T) {
	// A bad product_id in the subscribe request yields an error
	// frame. Must surface ErrSubscriptionRejected so the streamer
	// logs loudly instead of tight-looping on a bad config.
	raw := []byte(`{"type":"error","message":"Invalid product_id","reason":"BOGUS-USD"}`)
	_, _, err := parseFrame(raw, mustPairs(t))
	if !errors.Is(err, ErrSubscriptionRejected) {
		t.Errorf("expected ErrSubscriptionRejected, got %v", err)
	}
}

func TestParseFrame_UnknownProduct(t *testing.T) {
	// A match on a product not in PairMap — MATIC-USD is in the
	// ADR-0014 allow-list but intentionally not in DefaultPairs
	// (skipped pending MATIC→POL migration), the stable "known
	// unknown" placeholder. Surfaces ErrUnknownProduct.
	raw := []byte(`{
      "type":"match","trade_id":1,"side":"buy","size":"1","price":"0.5",
      "product_id":"MATIC-USD","sequence":1,"time":"2026-04-24T00:00:00Z"
    }`)
	_, _, err := parseFrame(raw, mustPairs(t))
	if !errors.Is(err, ErrUnknownProduct) {
		t.Errorf("expected ErrUnknownProduct, got %v", err)
	}
}

func TestParseFrame_MalformedJSON(t *testing.T) {
	_, _, err := parseFrame([]byte(`{bad`), mustPairs(t))
	if !errors.Is(err, ErrMalformedFrame) {
		t.Errorf("expected ErrMalformedFrame, got %v", err)
	}
}

func TestParseFrame_UnknownTypeIgnored(t *testing.T) {
	// Ticker / heartbeat / l2update — types we don't subscribe
	// to but could show up in mis-configured accounts. Must not
	// fail parsing.
	raw := []byte(`{"type":"heartbeat","sequence":123}`)
	_, isTrade, err := parseFrame(raw, mustPairs(t))
	if err != nil {
		t.Errorf("unexpected err: %v", err)
	}
	if isTrade {
		t.Error("heartbeat should not be treated as trade")
	}
}

func TestFormatTxHash(t *testing.T) {
	h := formatTxHash("XLM-USD", 987654321)
	if len(h) != 64 {
		t.Errorf("len = %d want 64", len(h))
	}
	// Dash-stripped normalisation — "XLM-USD" and "XLMUSD" yield
	// the same hash for the same trade_id.
	if h != formatTxHash("XLMUSD", 987654321) {
		t.Error("dash-stripping not working")
	}
}

func TestDecimalStringToScaledInt_CoinbasePrecision(t *testing.T) {
	// Coinbase publishes up to 8 dp. Verify round-trip at that
	// precision + 9-dp truncation.
	cases := []struct {
		in   string
		want *big.Int
	}{
		{"100.00000000", big.NewInt(10_000_000_000)},
		{"0.17582000", big.NewInt(17_582_000)},
		{"0.123456789", big.NewInt(12_345_678)}, // truncated to 8dp
	}
	for _, tc := range cases {
		got, err := decimalStringToScaledInt(tc.in, externalAmountDecimals)
		if err != nil {
			t.Errorf("%q: err %v", tc.in, err)
			continue
		}
		if got.Cmp(tc.want) != 0 {
			t.Errorf("%q → %s want %s", tc.in, got, tc.want)
		}
	}
}

// quiet canonical unused import warning when tests don't touch it.
var _ = canonical.Asset{}
