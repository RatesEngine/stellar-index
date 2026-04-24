package v1_test

import (
	"math/big"
	"net/http"
	"testing"
	"time"

	v1 "github.com/RatesEngine/rates-engine/internal/api/v1"
	"github.com/RatesEngine/rates-engine/internal/canonical"
)

func mkTWAPTrade(base, quote int64, ts time.Time) canonical.Trade {
	xlm, _ := canonical.ParseAsset("native")
	usd, _ := canonical.ParseAsset("fiat:USD")
	pair, _ := canonical.NewPair(xlm, usd)
	return canonical.Trade{
		Source: "soroswap", Ledger: uint32(ts.Unix()),
		TxHash:      "0000000000000000000000000000000000000000000000000000000000000001",
		OpIndex:     0,
		Timestamp:   ts,
		Pair:        pair,
		BaseAmount:  canonical.NewAmount(big.NewInt(base)),
		QuoteAmount: canonical.NewAmount(big.NewInt(quote)),
	}
}

func TestTWAP_503WhenReaderNil(t *testing.T) {
	srv := v1.New(v1.Options{})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/twap?base=native&quote=fiat:USD")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}

func TestTWAP_404WhenNoTrades(t *testing.T) {
	srv := v1.New(v1.Options{History: &stubHistoryReader{trades: nil}})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/twap?base=native&quote=fiat:USD")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestTWAP_TimeWeightsCorrectly(t *testing.T) {
	// Price 100 active 0..10s, price 200 active 10..40s. windowEnd = now.
	// TWAP = (100×10 + 200×30) / 40 = 175.
	t0 := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	trades := []canonical.Trade{
		mkTWAPTrade(1, 100, t0),
		mkTWAPTrade(1, 200, t0.Add(10*time.Second)),
	}
	reader := &stubHistoryReader{trades: trades}
	srv := v1.New(v1.Options{History: reader})
	ts := httpTestServer(t, srv)

	to := t0.Add(40 * time.Second).Format(time.RFC3339)
	from := t0.Format(time.RFC3339)
	resp := mustGet(t, ts.URL+"/v1/twap?base=native&quote=fiat:USD&from="+from+"&to="+to)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var env struct {
		Data v1.TWAPResult `json:"data"`
	}
	mustDecode(t, resp, &env)

	if env.Data.Price != "175.0000000000" {
		t.Errorf("Price = %q, want 175.0000000000", env.Data.Price)
	}
	if env.Data.TradeCount != 2 {
		t.Errorf("TradeCount = %d, want 2", env.Data.TradeCount)
	}
}
