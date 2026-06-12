package v1_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
	"github.com/StellarAtlas/stellar-atlas/internal/storage/timescale"

	v1 "github.com/StellarAtlas/stellar-atlas/internal/api/v1"
)

// stubCoinsReaderExt implements v1.CoinsReader for the coin-extension
// overlay test path. Each method returns the canned value its caller
// expects; everything else returns sql.ErrNoRows so behaviour
// degrades cleanly.
type stubCoinsReaderExt struct {
	row        timescale.CoinRow
	rowErr     error
	topMarkets []timescale.CoinTopMarket
	hist24     []timescale.CoinPricePoint
	hist7d     []timescale.CoinPricePoint
	marketsN   int64
	tradeN     int64
	ath        *timescale.CoinATH
}

func (s *stubCoinsReaderExt) ListCoinsExt(_ context.Context, _ timescale.ListCoinsOptions) ([]timescale.CoinRow, error) {
	return nil, nil
}

func (s *stubCoinsReaderExt) GetCoinBySlug(_ context.Context, _ string) (timescale.CoinRow, error) {
	return timescale.CoinRow{}, sql.ErrNoRows
}

func (s *stubCoinsReaderExt) GetCoinByAssetID(_ context.Context, _ string) (timescale.CoinRow, error) {
	return s.row, s.rowErr
}

func (s *stubCoinsReaderExt) GetNativeCoinRow(_ context.Context) (timescale.CoinRow, error) {
	return s.row, s.rowErr
}

func (s *stubCoinsReaderExt) GetCoinTopMarkets(_ context.Context, _ string, _ int) ([]timescale.CoinTopMarket, error) {
	return s.topMarkets, nil
}

func (s *stubCoinsReaderExt) GetCoinPriceHistory24h(_ context.Context, _ string) ([]timescale.CoinPricePoint, error) {
	return s.hist24, nil
}

func (s *stubCoinsReaderExt) GetCoinPriceHistory7d(_ context.Context, _ string) ([]timescale.CoinPricePoint, error) {
	return s.hist7d, nil
}

func (s *stubCoinsReaderExt) GetCoinsPriceHistory24hBatch(_ context.Context, _ []string) (map[string][]timescale.CoinPricePoint, error) {
	return nil, nil
}

func (s *stubCoinsReaderExt) GetCoinsPriceHistory7dBatch(_ context.Context, _ []string) (map[string][]timescale.CoinPricePoint, error) {
	return nil, nil
}

func (s *stubCoinsReaderExt) GetCoinMarketsCount(_ context.Context, _ string) (int64, error) {
	return s.marketsN, nil
}

func (s *stubCoinsReaderExt) GetCoinATH(_ context.Context, _ string) (*timescale.CoinATH, error) {
	return s.ath, nil
}

func (s *stubCoinsReaderExt) GetCoinsATHBatch(_ context.Context, _ []string) (map[string]timescale.CoinATH, error) {
	return nil, nil
}

func (s *stubCoinsReaderExt) GetCoinTradeCount24h(_ context.Context, _ string) (int64, error) {
	return s.tradeN, nil
}

// ptr is a tiny helper.
func sptr(s string) *string { return &s }

func TestAssetGet_CoinExtension_Populates(t *testing.T) {
	price := sptr("1.0008")
	ch1h := sptr("+0.11")
	ch7d := sptr("-0.04")
	coins := &stubCoinsReaderExt{
		row: timescale.CoinRow{
			Slug:        "USDC",
			AssetID:     "USDC-" + testUSDCIssuer,
			Code:        "USDC",
			PriceUSD:    price,
			Change1hPct: ch1h,
			Change7dPct: ch7d,
		},
		topMarkets: []timescale.CoinTopMarket{
			{Counterparty: "native", Side: "quote", Volume24hUSD: sptr("1000.0"), TradeCount24h: 5},
		},
		hist24:   []timescale.CoinPricePoint{{T: "2026-05-11T00:00:00Z", P: sptr("1.0001")}},
		hist7d:   []timescale.CoinPricePoint{{T: "2026-05-05T00:00:00Z", P: sptr("1.0010")}},
		marketsN: 12,
		tradeN:   456,
		ath:      &timescale.CoinATH{USD: "6.39", At: "2026-05-04T00:00:00Z"},
	}
	usdc, err := canonical.NewClassicAsset("USDC", testUSDCIssuer)
	if err != nil {
		t.Fatalf("NewClassicAsset: %v", err)
	}
	reader := &stubAssetReader{
		byID: map[string]v1.AssetDetail{
			usdc.String(): {AssetID: usdc.String(), Type: "classic", Code: "USDC"},
		},
	}
	srv := v1.New(v1.Options{Assets: reader, Coins: coins})
	ts := httpTestServer(t, srv)
	resp := mustGet(t, ts.URL+"/v1/assets/"+usdc.String())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var env struct {
		Data v1.AssetDetail `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	d := env.Data
	if d.PriceUSD == nil || *d.PriceUSD != "1.0008" {
		t.Errorf("price_usd: got %v want 1.0008", d.PriceUSD)
	}
	if d.Change1hPct == nil || *d.Change1hPct != "+0.11" {
		t.Errorf("change_1h_pct: got %v", d.Change1hPct)
	}
	if d.Change7dPct == nil || *d.Change7dPct != "-0.04" {
		t.Errorf("change_7d_pct: got %v", d.Change7dPct)
	}
	if len(d.TopMarkets) != 1 || d.TopMarkets[0].Counterparty != "native" {
		t.Errorf("top_markets unexpected: %+v", d.TopMarkets)
	}
	if len(d.PriceHistory24h) != 1 {
		t.Errorf("price_history_24h: %d points want 1", len(d.PriceHistory24h))
	}
	if len(d.PriceHistory7d) != 1 {
		t.Errorf("price_history_7d: %d points want 1", len(d.PriceHistory7d))
	}
	if d.MarketsCount == nil || *d.MarketsCount != 12 {
		t.Errorf("markets_count: %v", d.MarketsCount)
	}
	if d.TradeCount24h == nil || *d.TradeCount24h != 456 {
		t.Errorf("trade_count_24h: %v", d.TradeCount24h)
	}
	if d.ATH == nil || d.ATH.USD != "6.39" {
		t.Errorf("ath: %+v", d.ATH)
	}
}

func TestAssetGet_CoinExtension_NoCoinReader_NoOp(t *testing.T) {
	// No CoinsReader wired — coin-extension fields stay nil; rest
	// of the response is unchanged.
	usdc, _ := canonical.NewClassicAsset("USDC", testUSDCIssuer)
	reader := &stubAssetReader{
		byID: map[string]v1.AssetDetail{
			usdc.String(): {AssetID: usdc.String(), Type: "classic", Code: "USDC"},
		},
	}
	srv := v1.New(v1.Options{Assets: reader})
	ts := httpTestServer(t, srv)
	resp := mustGet(t, ts.URL+"/v1/assets/"+usdc.String())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var env struct {
		Data v1.AssetDetail `json:"data"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&env)
	if env.Data.PriceUSD != nil || len(env.Data.TopMarkets) != 0 {
		t.Errorf("expected nil extension fields, got %+v", env.Data)
	}
}

func TestAssetGet_CoinExtension_FiatAsset_Skipped(t *testing.T) {
	// fiat:* assets have no coin row; the extension is a no-op even
	// when a CoinsReader is wired.
	coins := &stubCoinsReaderExt{
		row:        timescale.CoinRow{PriceUSD: sptr("999")}, // would leak through if not gated
		topMarkets: []timescale.CoinTopMarket{{Counterparty: "should_not_appear"}},
	}
	usd, _ := canonical.ParseAsset("fiat:USD")
	reader := &stubAssetReader{
		byID: map[string]v1.AssetDetail{
			usd.String(): {AssetID: "fiat:USD", Type: "fiat", Code: "USD"},
		},
	}
	srv := v1.New(v1.Options{Assets: reader, Coins: coins})
	ts := httpTestServer(t, srv)
	resp := mustGet(t, ts.URL+"/v1/assets/fiat:USD")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var env struct {
		Data v1.AssetDetail `json:"data"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&env)
	if env.Data.PriceUSD != nil {
		t.Errorf("fiat asset should NOT get coin extension; got price_usd=%v", env.Data.PriceUSD)
	}
	if len(env.Data.TopMarkets) != 0 {
		t.Errorf("fiat asset should NOT get top_markets; got %+v", env.Data.TopMarkets)
	}
}
