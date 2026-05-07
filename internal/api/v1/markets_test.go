package v1_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	v1 "github.com/RatesEngine/rates-engine/internal/api/v1"
	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

type stubMarketsReader struct {
	pairs   []v1.Market
	nextCur string
	err     error

	// PairMarket stub state.
	pair      v1.Market
	pairFound bool
	pairErr   error
}

func (r *stubMarketsReader) DistinctPairsExt(_ context.Context, cursor string, limit int, _ timescale.MarketsOrder) ([]v1.Market, string, error) {
	if r.err != nil {
		return nil, "", r.err
	}
	return r.pairs, r.nextCur, nil
}

func (r *stubMarketsReader) SourceMarkets(_ context.Context, _, _ string, _ int, _ timescale.MarketsOrder) ([]v1.Market, string, error) {
	if r.err != nil {
		return nil, "", r.err
	}
	return r.pairs, r.nextCur, nil
}

func (r *stubMarketsReader) PairMarket(_ context.Context, _ canonical.Asset, _ canonical.Asset) (v1.Market, bool, error) {
	if r.pairErr != nil {
		return v1.Market{}, false, r.pairErr
	}
	return r.pair, r.pairFound, nil
}

func TestMarkets_EmptyWhenReaderNil(t *testing.T) {
	srv := v1.New(v1.Options{})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/markets")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var env struct {
		Data []v1.Market `json:"data"`
	}
	mustDecode(t, resp, &env)
	if len(env.Data) != 0 {
		t.Errorf("want empty list, got %d", len(env.Data))
	}
}

func TestMarkets_NilSliceFromReaderMarshalsAsEmptyArray(t *testing.T) {
	// Regression: reader returning (nil, "", nil) must not surface as
	// "data": null — OpenAPI MarketsEnvelope.data is `type: array`.
	reader := &stubMarketsReader{pairs: nil, nextCur: ""}
	srv := v1.New(v1.Options{Markets: reader})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/markets")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body, _ := readAll(resp)
	if !bytes.Contains([]byte(body), []byte(`"data":[]`)) {
		t.Errorf("expected \"data\":[] in body, got: %s", body)
	}
}

func TestMarkets_ReturnsPairsWithCursor(t *testing.T) {
	ts1 := time.Unix(1_772_000_000, 0).UTC()
	reader := &stubMarketsReader{
		pairs: []v1.Market{
			{Base: "native", Quote: "fiat:USD", LastTradeAt: ts1, TradeCount24h: 42},
			{Base: "native", Quote: "fiat:EUR", LastTradeAt: ts1, TradeCount24h: 10},
		},
		nextCur: "next-opaque",
	}
	srv := v1.New(v1.Options{Markets: reader})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/markets?limit=50")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var env struct {
		Data       []v1.Market `json:"data"`
		Pagination struct {
			Next string `json:"next"`
		} `json:"pagination"`
	}
	mustDecode(t, resp, &env)

	if len(env.Data) != 2 {
		t.Fatalf("got %d markets, want 2", len(env.Data))
	}
	if env.Data[0].Quote != "fiat:USD" {
		t.Errorf("quote = %q", env.Data[0].Quote)
	}
	if env.Data[0].TradeCount24h != 42 {
		t.Errorf("trade_count_24h = %d, want 42", env.Data[0].TradeCount24h)
	}
	if env.Pagination.Next != "next-opaque" {
		t.Errorf("next cursor = %q, want next-opaque", env.Pagination.Next)
	}
}

func TestMarkets_InvalidLimit400(t *testing.T) {
	srv := v1.New(v1.Options{Markets: &stubMarketsReader{}})
	ts := httpTestServer(t, srv)

	for _, bad := range []string{"0", "501", "-1", "xyz"} {
		resp := mustGet(t, ts.URL+"/v1/markets?limit="+bad)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("limit=%q: status = %d, want 400", bad, resp.StatusCode)
		}
	}
}

func TestMarkets_ReaderError500(t *testing.T) {
	reader := &stubMarketsReader{err: errors.New("storage broke")}
	srv := v1.New(v1.Options{Markets: reader})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/markets")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
}
