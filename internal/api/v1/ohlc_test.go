package v1_test

import (
	"context"
	"errors"
	"math/big"
	"net/http"
	"strings"
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

// ─── error-path coverage to parity with TWAP ─────────────────

func TestOHLC_InvalidPair400(t *testing.T) {
	srv := v1.New(v1.Options{History: &stubHistoryReader{}})
	ts := httpTestServer(t, srv)

	// base == quote — NewPair rejects with invalid-pair.
	resp := mustGet(t, ts.URL+"/v1/ohlc?base=native&quote=native")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestOHLC_ReaderError500(t *testing.T) {
	reader := &stubHistoryReader{err: errors.New("storage broke")}
	srv := v1.New(v1.Options{History: reader})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/ohlc?base=native&quote=fiat:USD")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
}

// ohlcPairAwareReader is a per-pair history reader scoped to this
// test file. The shared stubHistoryReader returns the same trade
// slice regardless of pair, which the stablecoin-fiat fallback
// can't exercise. Mirrors the pairAwareHistoryReader in vwap_test.go
// (PR #1219); kept colocated until that helper merges.
type ohlcPairAwareReader struct {
	stubHistoryReader
	tradesByPair map[string][]canonical.Trade
}

func (r *ohlcPairAwareReader) TradesInRange(_ context.Context, pair canonical.Pair, _, _ time.Time, _ int) ([]canonical.Trade, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.tradesByPair[pair.Base.String()+"/"+pair.Quote.String()], nil
}

// TestOHLC_StablecoinFiatProxyFallback — when the literal
// X/fiat:USD pair has zero trades but the operator declared a
// USDC peg, the OHLC handler retries against X/<USDC-classic> and
// returns the bar with flags.triangulated=true. Mirrors
// /v1/chart's chartStablecoinFallback (#1015) and the same family
// of fixes shipped this session for /v1/price (#1217), /v1/price/tip
// (#1218), /v1/vwap + /v1/twap (#1219), /v1/oracle/lastprice (#1220).
//
// Without this, /v1/ohlc?base=native&quote=fiat:USD 404s with
// "no trades in window" out of the box — Freighter RFP §3 names
// /v1/ohlc as a launch-blocking surface for the asset-detail page.
func TestOHLC_StablecoinFiatProxyFallback(t *testing.T) {
	usdcClassic, err := canonical.ParseAsset("USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")
	if err != nil {
		t.Fatalf("parse USDC: %v", err)
	}
	xlm, _ := canonical.ParseAsset("native")
	classicPair, _ := canonical.NewPair(xlm, usdcClassic)

	t0 := time.Now().UTC().Add(-30 * time.Minute)
	pegTrades := []canonical.Trade{
		{
			Source: "sdex", Ledger: 1,
			TxHash:      "0000000000000000000000000000000000000000000000000000000000000001",
			Timestamp:   t0,
			Pair:        classicPair,
			BaseAmount:  canonical.NewAmount(big.NewInt(100)),
			QuoteAmount: canonical.NewAmount(big.NewInt(16)),
		},
		{
			Source: "sdex", Ledger: 2,
			TxHash:      "0000000000000000000000000000000000000000000000000000000000000002",
			Timestamp:   t0.Add(5 * time.Minute),
			Pair:        classicPair,
			BaseAmount:  canonical.NewAmount(big.NewInt(100)),
			QuoteAmount: canonical.NewAmount(big.NewInt(17)),
		},
	}
	reader := &ohlcPairAwareReader{
		// Literal native/fiat:USD missing. native/<USDC-classic>
		// has two trades — open=0.16, close=0.17.
		tradesByPair: map[string][]canonical.Trade{
			"native/" + usdcClassic.String(): pegTrades,
		},
	}
	srv := v1.New(v1.Options{
		History:           reader,
		USDPeggedClassics: []canonical.Asset{usdcClassic},
	})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/ohlc?base=native&quote=fiat:USD")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (peg fallback should serve)", resp.StatusCode)
	}
	body, _ := readAll(resp)
	for _, want := range []string{
		`"open":"0.1600000000"`,
		`"close":"0.1700000000"`,
		`"trade_count":2`,
		`"triangulated":true`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q: %s", want, body)
		}
	}
}

// TestOHLC_StablecoinFiatProxy_NoPegLeaves404 — without
// USDPeggedClassics the fallback skips silently and the 404
// "no trades" path still serves.
func TestOHLC_StablecoinFiatProxy_NoPegLeaves404(t *testing.T) {
	reader := &ohlcPairAwareReader{tradesByPair: map[string][]canonical.Trade{}}
	srv := v1.New(v1.Options{History: reader})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/ohlc?base=native&quote=fiat:USD")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}
