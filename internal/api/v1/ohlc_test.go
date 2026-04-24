package v1_test

import (
	"math/big"
	"net/http"
	"testing"
	"time"

	v1 "github.com/RatesEngine/rates-engine/internal/api/v1"
	"github.com/RatesEngine/rates-engine/internal/canonical"
)

func mkOHLCTrade(base, quote int64, ts time.Time) canonical.Trade {
	xlm, _ := canonical.ParseAsset("native")
	usd, _ := canonical.ParseAsset("fiat:USD")
	pair, _ := canonical.NewPair(xlm, usd)
	return canonical.Trade{
		Source: "soroswap", Ledger: 1,
		TxHash:      "0000000000000000000000000000000000000000000000000000000000000001",
		OpIndex:     0,
		Timestamp:   ts,
		Pair:        pair,
		BaseAmount:  canonical.NewAmount(big.NewInt(base)),
		QuoteAmount: canonical.NewAmount(big.NewInt(quote)),
	}
}

func TestOHLC_503WhenReaderNil(t *testing.T) {
	srv := v1.New(v1.Options{})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/ohlc?base=native&quote=fiat:USD")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}

func TestOHLC_404WhenNoTradesInWindow(t *testing.T) {
	srv := v1.New(v1.Options{History: &stubHistoryReader{trades: nil}})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/ohlc?base=native&quote=fiat:USD")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestOHLC_ComputesBarFromTrades(t *testing.T) {
	base := time.Unix(1_772_000_000, 0).UTC()
	reader := &stubHistoryReader{
		trades: []canonical.Trade{
			// Base=1, so price = quote. 100, 150, 80, 120 in order.
			mkOHLCTrade(1, 100, base),
			mkOHLCTrade(1, 150, base.Add(1*time.Second)),
			mkOHLCTrade(1, 80, base.Add(2*time.Second)),
			mkOHLCTrade(1, 120, base.Add(3*time.Second)),
		},
	}
	srv := v1.New(v1.Options{History: reader})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/ohlc?base=native&quote=fiat:USD")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var env struct {
		Data v1.OHLCBar `json:"data"`
	}
	mustDecode(t, resp, &env)

	// 10-digit precision → "100.0000000000"
	wantOpen := "100.0000000000"
	wantClose := "120.0000000000"
	wantHigh := "150.0000000000"
	wantLow := "80.0000000000"
	if env.Data.Open != wantOpen {
		t.Errorf("Open = %q, want %q", env.Data.Open, wantOpen)
	}
	if env.Data.Close != wantClose {
		t.Errorf("Close = %q, want %q", env.Data.Close, wantClose)
	}
	if env.Data.High != wantHigh {
		t.Errorf("High = %q, want %q", env.Data.High, wantHigh)
	}
	if env.Data.Low != wantLow {
		t.Errorf("Low = %q, want %q", env.Data.Low, wantLow)
	}
	if env.Data.BaseVolume != "4" {
		t.Errorf("BaseVolume = %q, want 4", env.Data.BaseVolume)
	}
	if env.Data.QuoteVolume != "450" {
		t.Errorf("QuoteVolume = %q, want 450", env.Data.QuoteVolume)
	}
	if env.Data.TradeCount != 4 {
		t.Errorf("TradeCount = %d, want 4", env.Data.TradeCount)
	}
}

func TestOHLC_FractionalPrice(t *testing.T) {
	// base=3, quote=1 → price = 1/3 = 0.3333... → truncated to 10
	// digits = "0.3333333333".
	base := time.Unix(1_772_000_000, 0).UTC()
	reader := &stubHistoryReader{
		trades: []canonical.Trade{mkOHLCTrade(3, 1, base)},
	}
	srv := v1.New(v1.Options{History: reader})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/ohlc?base=native&quote=fiat:USD")
	var env struct {
		Data v1.OHLCBar `json:"data"`
	}
	mustDecode(t, resp, &env)

	if env.Data.Open != "0.3333333333" {
		t.Errorf("Open = %q, want 0.3333333333 (truncated 1/3)", env.Data.Open)
	}
}

func TestOHLC_InvalidTime400(t *testing.T) {
	srv := v1.New(v1.Options{History: &stubHistoryReader{}})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/ohlc?base=native&quote=fiat:USD&from=bogus")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}
