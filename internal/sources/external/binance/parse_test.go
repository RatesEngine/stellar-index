package binance

import (
	"errors"
	"math/big"
	"testing"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
)

// buildPairMap builds a small test pair map. XLMUSDT and BTCUSDT —
// the two most common Binance markets on spot for our use case.
func buildPairMap(t *testing.T) map[string]canonical.Pair {
	t.Helper()
	xlm, err := canonical.NewCryptoAsset("XLM")
	if err != nil {
		t.Fatalf("NewCryptoAsset(XLM): %v", err)
	}
	btc, err := canonical.NewCryptoAsset("BTC")
	if err != nil {
		t.Fatalf("NewCryptoAsset(BTC): %v", err)
	}
	usdt, err := canonical.NewCryptoAsset("USDT")
	if err != nil {
		t.Fatalf("NewCryptoAsset(USDT): %v", err)
	}
	xlmUsdt, err := canonical.NewPair(xlm, usdt)
	if err != nil {
		t.Fatalf("NewPair(XLM/USDT): %v", err)
	}
	btcUsdt, err := canonical.NewPair(btc, usdt)
	if err != nil {
		t.Fatalf("NewPair(BTC/USDT): %v", err)
	}
	return map[string]canonical.Pair{
		"XLMUSDT": xlmUsdt,
		"BTCUSDT": btcUsdt,
	}
}

func TestParseAggTradeFrame_HappyPath(t *testing.T) {
	// Real-shape frame: XLMUSDT at $0.17582 for 152.34 XLM.
	raw := []byte(`{
      "stream": "xlmusdt@aggTrade",
      "data": {
        "e": "aggTrade",
        "E": 1745000000000,
        "s": "XLMUSDT",
        "a": 987654321,
        "p": "0.17582",
        "q": "152.34",
        "f": 100,
        "l": 105,
        "T": 1745000000100,
        "m": true
      }
    }`)

	trade, err := parseAggTradeFrame(raw, buildPairMap(t))
	if err != nil {
		t.Fatalf("parseAggTradeFrame: %v", err)
	}

	if trade.Source != "binance" {
		t.Errorf("Source = %q want binance", trade.Source)
	}
	if trade.Timestamp.UnixMilli() != 1745000000100 {
		t.Errorf("Timestamp = %d want 1745000000100", trade.Timestamp.UnixMilli())
	}
	// Base = 152.34 at 10^8 = 15234000000
	want := big.NewInt(15234000000)
	if trade.BaseAmount.BigInt().Cmp(want) != 0 {
		t.Errorf("BaseAmount = %s want %s", trade.BaseAmount, want)
	}
	// Quote = base × price = 15234000000 × 17582000 / 10^8
	// 15234 × 17582 = 267,844,188 → × 10^9 / 10^8 = 2,678,441,880.
	// Verify the numeric relationship holds: VWAP = quote / base
	//   = 2678441880 / 15234000000 = 0.17582 ✓ (0.17582 × 152.34 = 26.7844188)
	wantQuote := big.NewInt(2678441880)
	if trade.QuoteAmount.BigInt().Cmp(wantQuote) != 0 {
		t.Errorf("QuoteAmount = %s want %s", trade.QuoteAmount, wantQuote)
	}
	// TxHash must be 64 hex chars for canonical.Trade.Validate().
	if len(trade.TxHash) != 64 {
		t.Errorf("TxHash length = %d want 64", len(trade.TxHash))
	}
	// Pair base = XLM crypto.
	xlm, _ := canonical.NewCryptoAsset("XLM")
	if !trade.Pair.Base.Equal(xlm) {
		t.Errorf("Pair.Base = %+v want XLM crypto", trade.Pair.Base)
	}
}

func TestParseAggTradeFrame_UnknownSymbol(t *testing.T) {
	// DOGE isn't in our PairMap — should surface ErrUnknownSymbol
	// without panicking.
	raw := []byte(`{"stream":"dogeusdt@aggTrade","data":{"e":"aggTrade","s":"DOGEUSDT","a":1,"p":"0.1","q":"1","T":1,"m":false}}`)
	_, err := parseAggTradeFrame(raw, buildPairMap(t))
	if !errors.Is(err, ErrUnknownSymbol) {
		t.Errorf("expected ErrUnknownSymbol, got %v", err)
	}
}

func TestParseAggTradeFrame_WrongStream(t *testing.T) {
	// Subscribing to @ticker by mistake — the dispatcher should
	// reject it before attempting to decode as aggTrade.
	raw := []byte(`{"stream":"xlmusdt@ticker","data":{}}`)
	_, err := parseAggTradeFrame(raw, buildPairMap(t))
	if !errors.Is(err, ErrMalformedFrame) {
		t.Errorf("expected ErrMalformedFrame, got %v", err)
	}
}

func TestParseAggTradeFrame_BrokenJSON(t *testing.T) {
	_, err := parseAggTradeFrame([]byte(`{not json`), buildPairMap(t))
	if !errors.Is(err, ErrMalformedFrame) {
		t.Errorf("expected ErrMalformedFrame, got %v", err)
	}
}

func TestDecimalStringToScaledInt(t *testing.T) {
	cases := []struct {
		in          string
		targetDP    int
		want        *big.Int
		expectError bool
	}{
		{"0.17582", 8, big.NewInt(17582000), false},
		{"152.34", 8, big.NewInt(15234000000), false},
		{"1", 8, big.NewInt(100000000), false},
		{"0", 8, big.NewInt(0), false},
		// Truncate beyond target precision (doesn't error — venues
		// sometimes publish more precision than our storage scale).
		{"0.123456789", 8, big.NewInt(12345678), false},
		// Integer-only input.
		{"42", 2, big.NewInt(4200), false},
		// Malformed inputs.
		{"", 8, nil, true},
		{"1.2e3", 8, nil, true}, // scientific notation banned
		{"abc", 8, nil, true},
		{"1.2.3", 8, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := decimalStringToScaledInt(tc.in, tc.targetDP)
			if tc.expectError {
				if err == nil {
					t.Errorf("want error, got nil (value=%s)", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Cmp(tc.want) != 0 {
				t.Errorf("decimalStringToScaledInt(%q, %d) = %s, want %s", tc.in, tc.targetDP, got, tc.want)
			}
		})
	}
}

func TestFormatTxHash_64CharsHex(t *testing.T) {
	// canonical.Trade.Validate() requires tx_hash to be exactly 64
	// lowercase hex characters. Off-chain trades have no real tx
	// hash, so formatTxHash synthesises one — assert the shape.
	h := formatTxHash("XLMUSDT", 987654321)
	if len(h) != 64 {
		t.Errorf("len = %d want 64", len(h))
	}
	for i, c := range h {
		isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')
		if !isHex {
			t.Errorf("char at %d = %q, not hex", i, c)
			break
		}
	}
	// Uniqueness: different aggIDs must produce different hashes.
	h2 := formatTxHash("XLMUSDT", 987654322)
	if h == h2 {
		t.Error("formatTxHash collided on adjacent aggIDs")
	}
}
