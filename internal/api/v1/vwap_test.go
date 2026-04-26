package v1_test

import (
	"errors"
	"math/big"
	"net/http"
	"strings"
	"testing"
	"time"

	v1 "github.com/RatesEngine/rates-engine/internal/api/v1"
	"github.com/RatesEngine/rates-engine/internal/canonical"
)

func mkVWAPTrade(base, quote int64) canonical.Trade {
	xlm, _ := canonical.ParseAsset("native")
	usd, _ := canonical.ParseAsset("fiat:USD")
	pair, _ := canonical.NewPair(xlm, usd)
	return canonical.Trade{
		Source: "soroswap", Ledger: 1,
		TxHash:      "0000000000000000000000000000000000000000000000000000000000000001",
		OpIndex:     0,
		Timestamp:   time.Unix(1_772_000_000, 0).UTC(),
		Pair:        pair,
		BaseAmount:  canonical.NewAmount(big.NewInt(base)),
		QuoteAmount: canonical.NewAmount(big.NewInt(quote)),
	}
}

func TestVWAP_503WhenReaderNil(t *testing.T) {
	srv := v1.New(v1.Options{})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/vwap?base=native&quote=fiat:USD")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}

func TestVWAP_404WhenNoTrades(t *testing.T) {
	srv := v1.New(v1.Options{History: &stubHistoryReader{trades: nil}})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/vwap?base=native&quote=fiat:USD")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestVWAP_ComputesVolumeWeightedPrice(t *testing.T) {
	// VWAP = Σ(Qi) / Σ(Bi). Two trades: 20@2 + 100@3 = total
	// quote 340 / base 120 = 17/6 ≈ 2.8333333333 (10 digits, floor).
	reader := &stubHistoryReader{
		trades: []canonical.Trade{
			mkVWAPTrade(20, 40),
			mkVWAPTrade(100, 300),
		},
	}
	srv := v1.New(v1.Options{History: reader})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/vwap?base=native&quote=fiat:USD")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var env struct {
		Data v1.VWAPResult `json:"data"`
	}
	mustDecode(t, resp, &env)

	if env.Data.Price != "2.8333333333" {
		t.Errorf("Price = %q, want 2.8333333333 (17/6 truncated to 10 digits)", env.Data.Price)
	}
	if env.Data.BaseVolume != "120" {
		t.Errorf("BaseVolume = %q, want 120", env.Data.BaseVolume)
	}
	if env.Data.QuoteVolume != "340" {
		t.Errorf("QuoteVolume = %q, want 340", env.Data.QuoteVolume)
	}
	if env.Data.TradeCount != 2 {
		t.Errorf("TradeCount = %d, want 2", env.Data.TradeCount)
	}
	if env.Data.OutliersFiltered != 0 {
		t.Errorf("OutliersFiltered = %d, want 0 (no sigma passed)", env.Data.OutliersFiltered)
	}
}

func TestVWAP_AppliesOutlierFilter(t *testing.T) {
	// 20 baseline trades around price 100, plus one at 10000.
	// sigma=3 should drop the outlier.
	baseline := []int64{
		100, 101, 99, 100, 102, 98, 101, 100, 99, 100,
		101, 100, 99, 101, 100, 102, 99, 100, 101, 100,
	}
	trades := make([]canonical.Trade, 0, len(baseline)+1)
	for _, p := range baseline {
		trades = append(trades, mkVWAPTrade(1, p))
	}
	trades = append(trades, mkVWAPTrade(1, 10_000))

	reader := &stubHistoryReader{trades: trades}
	srv := v1.New(v1.Options{History: reader})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/vwap?base=native&quote=fiat:USD&outlier_sigma=3")
	var env struct {
		Data v1.VWAPResult `json:"data"`
	}
	mustDecode(t, resp, &env)

	if env.Data.OutliersFiltered != 1 {
		t.Errorf("OutliersFiltered = %d, want 1", env.Data.OutliersFiltered)
	}
	if env.Data.TradeCount != 20 {
		t.Errorf("TradeCount = %d, want 20 (outlier dropped)", env.Data.TradeCount)
	}
	// Baseline mean is ~100; the outlier at 10000 would skew the
	// unfiltered VWAP to ~571. Filtered VWAP should be ~100.
	if env.Data.Price[:3] != "100" {
		t.Errorf("filtered Price = %q, want ~100 prefix", env.Data.Price)
	}
}

func TestVWAP_AllFilteredReturns422(t *testing.T) {
	// When every trade in the window is removed by the sigma filter,
	// the handler must distinguish "empty window" (404) from
	// "everything filtered out" (422 — client should relax sigma).
	// Construct a distribution where every trade is an "outlier"
	// relative to a near-zero sigma.
	baseline := []int64{100, 200, 300, 400, 500, 600, 700, 800, 900, 1000, 1100, 1200}
	trades := make([]canonical.Trade, 0, len(baseline))
	for _, p := range baseline {
		trades = append(trades, mkVWAPTrade(1, p))
	}
	reader := &stubHistoryReader{trades: trades}
	srv := v1.New(v1.Options{History: reader})
	ts := httpTestServer(t, srv)

	// sigma=0.01 → basically nothing survives; σ²≈120000 for this
	// range so 0.01σ is far tighter than any trade's deviation.
	resp := mustGet(t, ts.URL+"/v1/vwap?base=native&quote=fiat:USD&outlier_sigma=0.01")
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422 (all filtered)", resp.StatusCode)
	}
	body, _ := readAll(resp)
	if !strings.Contains(body, "all-filtered") {
		t.Errorf("body should cite all-filtered: %s", body)
	}
}

func TestVWAP_InvalidSigma400(t *testing.T) {
	srv := v1.New(v1.Options{History: &stubHistoryReader{}})
	ts := httpTestServer(t, srv)

	for _, bad := range []string{"-1", "abc", "NaN"} {
		resp := mustGet(t, ts.URL+"/v1/vwap?base=native&quote=fiat:USD&outlier_sigma="+bad)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("outlier_sigma=%q: status = %d, want 400", bad, resp.StatusCode)
		}
	}
}

// ─── error-path coverage to parity with TWAP/OHLC ───────────

func TestVWAP_InvalidTime400(t *testing.T) {
	srv := v1.New(v1.Options{History: &stubHistoryReader{}})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/vwap?base=native&quote=fiat:USD&from=bogus")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestVWAP_InvalidPair400(t *testing.T) {
	srv := v1.New(v1.Options{History: &stubHistoryReader{}})
	ts := httpTestServer(t, srv)

	// base == quote — NewPair rejects with invalid-pair.
	resp := mustGet(t, ts.URL+"/v1/vwap?base=native&quote=native")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestVWAP_ReaderError500(t *testing.T) {
	reader := &stubHistoryReader{err: errors.New("storage broke")}
	srv := v1.New(v1.Options{History: reader})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/vwap?base=native&quote=fiat:USD")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
}
