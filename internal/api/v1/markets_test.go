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

	// lastDistinctOrder captures the order arg the handler passed
	// on the most recent DistinctPairsExt call. Lets tests assert
	// the handler's default-resolution behaviour.
	lastDistinctOrder timescale.MarketsOrder

	// PairMarket stub state.
	pair      v1.Market
	pairFound bool
	pairErr   error
}

func (r *stubMarketsReader) DistinctPairsExt(_ context.Context, cursor string, limit int, order timescale.MarketsOrder) ([]v1.Market, string, error) {
	r.lastDistinctOrder = order
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

func (r *stubMarketsReader) AssetMarkets(_ context.Context, _, _ string, _ int, _ timescale.MarketsOrder) ([]v1.Market, string, error) {
	if r.err != nil {
		return nil, "", r.err
	}
	return r.pairs, r.nextCur, nil
}

func (r *stubMarketsReader) AllPools(_ context.Context, _ timescale.PoolsFilter, _ string, _ int, _ timescale.MarketsOrder) ([]v1.Pool, string, error) {
	if r.err != nil {
		return nil, "", r.err
	}
	out := make([]v1.Pool, len(r.pairs))
	for i, m := range r.pairs {
		out[i] = v1.Pool{
			Source:        "test",
			Base:          m.Base,
			Quote:         m.Quote,
			LastTradeAt:   m.LastTradeAt,
			TradeCount24h: m.TradeCount24h,
			Volume24hUSD:  m.Volume24hUSD,
		}
	}
	return out, r.nextCur, nil
}

func (r *stubMarketsReader) GetPairsVolumeHistory24hBatch(_ context.Context, _ [][2]string) (map[string][]timescale.PairVolumePoint, error) {
	return map[string][]timescale.PairVolumePoint{}, nil
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

// TestMarkets_UnknownSource400 — `?source=` with a name that isn't
// in the in-memory `external.Registry` returns 400 instead of an
// empty page. The silent-empty-page anti-pattern (a typo looking
// identical to "this source has no trades") sends callers chasing
// nonexistent data; failing fast is the contract on every other
// listing handler in this package.
func TestMarkets_UnknownSource400(t *testing.T) {
	srv := v1.New(v1.Options{Markets: &stubMarketsReader{}})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/markets?source=fake-source")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var p v1.Problem
	mustDecode(t, resp, &p)
	if p.Type != "https://api.ratesengine.net/errors/unknown-source" {
		t.Errorf("Type = %q", p.Type)
	}
}

// TestMarkets_KnownSource200 — guards the inverse: a registered
// source name passes the validation gate. We can't depend on a
// specific name surviving registry refactors, so iterate any-one
// from the known set ("binance" is registered for the lifetime of
// this codebase per docs/discovery/external-refs/cex-feeds.md).
func TestMarkets_KnownSource200(t *testing.T) {
	srv := v1.New(v1.Options{Markets: &stubMarketsReader{}})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/markets?source=binance")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

// TestMarkets_AssetFilter_HappyPath pins the `?asset=` query param
// dispatching to AssetMarkets. The stub returns the same fixture
// rows for every reader method, so we assert a 200 + non-empty body
// rather than per-asset filtering (which is the storage layer's job).
func TestMarkets_AssetFilter_HappyPath(t *testing.T) {
	srv := v1.New(v1.Options{Markets: &stubMarketsReader{
		pairs: []v1.Market{{Base: "USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN", Quote: "native"}},
	}})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/markets?asset=native")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var env struct {
		Data []v1.Market `json:"data"`
	}
	mustDecode(t, resp, &env)
	if len(env.Data) != 1 {
		t.Errorf("want 1 row, got %d", len(env.Data))
	}
}

// TestMarkets_InvalidAsset400 — unparseable asset_id values 400
// rather than silently returning empty. Mirrors the
// silent-empty-page guard family applied to `?source=`.
func TestMarkets_InvalidAsset400(t *testing.T) {
	srv := v1.New(v1.Options{Markets: &stubMarketsReader{}})
	ts := httpTestServer(t, srv)

	// "USDC" alone (no issuer) is not a canonical asset_id.
	resp := mustGet(t, ts.URL+"/v1/markets?asset=USDC")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var p v1.Problem
	mustDecode(t, resp, &p)
	if p.Type != "https://api.ratesengine.net/errors/invalid-asset-id" {
		t.Errorf("Type = %q", p.Type)
	}
}

// TestMarkets_SourceAndAssetTogether400 — combining the two
// filters has no defined semantics on the storage side; reject up
// front rather than silently picking one.
func TestMarkets_SourceAndAssetTogether400(t *testing.T) {
	srv := v1.New(v1.Options{Markets: &stubMarketsReader{}})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/markets?source=binance&asset=native")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var p v1.Problem
	mustDecode(t, resp, &p)
	if p.Type != "https://api.ratesengine.net/errors/conflicting-filters" {
		t.Errorf("Type = %q", p.Type)
	}
}

// TestPools_AssetFilter_HappyPath — `/v1/pools?asset=<id>` is the
// OR-shape filter (base = X OR quote = X) that lets asset-detail
// surfaces fetch every pool touching the asset in one request.
func TestPools_AssetFilter_HappyPath(t *testing.T) {
	srv := v1.New(v1.Options{Markets: &stubMarketsReader{
		pairs: []v1.Market{{Base: "native", Quote: "USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"}},
	}})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/pools?asset=native")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

// TestPools_InvalidAsset400 — silent-empty-page guard family.
func TestPools_InvalidAsset400(t *testing.T) {
	srv := v1.New(v1.Options{Markets: &stubMarketsReader{}})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/pools?asset=USDC")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var p v1.Problem
	mustDecode(t, resp, &p)
	if p.Type != "https://api.ratesengine.net/errors/invalid-asset-id" {
		t.Errorf("Type = %q", p.Type)
	}
}

// TestMarkets_DefaultOrderIsVolumeDesc pins the post-2026-05-10
// default. R-014 in `docs/review-2026-05-10.md` — the
// alphabetical default surfaced spam tokens (`0-…`, `0TAX-…`)
// at the top of every cold listing. The explorer always passed
// `?order_by=volume_24h_usd_desc` explicitly to work around it;
// now the implicit default matches what every consumer wants.
//
// Callers paginating the entire universe of pairs in lex order
// can still pass `?order_by=pair` explicitly.
func TestMarkets_DefaultOrderIsVolumeDesc(t *testing.T) {
	t.Run("no order_by → volume desc", func(t *testing.T) {
		reader := &stubMarketsReader{}
		srv := v1.New(v1.Options{Markets: reader})
		ts := httpTestServer(t, srv)

		resp := mustGet(t, ts.URL+"/v1/markets")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		if reader.lastDistinctOrder != timescale.MarketsOrderVolume24hDesc {
			t.Errorf("default order = %v, want MarketsOrderVolume24hDesc", reader.lastDistinctOrder)
		}
	})
	t.Run("explicit order_by=pair → MarketsOrderPair", func(t *testing.T) {
		reader := &stubMarketsReader{}
		srv := v1.New(v1.Options{Markets: reader})
		ts := httpTestServer(t, srv)

		resp := mustGet(t, ts.URL+"/v1/markets?order_by=pair")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		if reader.lastDistinctOrder != timescale.MarketsOrderPair {
			t.Errorf("explicit pair order = %v, want MarketsOrderPair", reader.lastDistinctOrder)
		}
	})
	t.Run("explicit order_by=volume_24h_usd_desc → MarketsOrderVolume24hDesc", func(t *testing.T) {
		reader := &stubMarketsReader{}
		srv := v1.New(v1.Options{Markets: reader})
		ts := httpTestServer(t, srv)

		resp := mustGet(t, ts.URL+"/v1/markets?order_by=volume_24h_usd_desc")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		if reader.lastDistinctOrder != timescale.MarketsOrderVolume24hDesc {
			t.Errorf("explicit volume order = %v, want MarketsOrderVolume24hDesc", reader.lastDistinctOrder)
		}
	})
}

// TestPools_AssetAndBaseTogether400 — asset (OR) + base/quote
// (AND) is rejected; combining the two filter shapes has no
// well-defined semantics.
func TestPools_AssetAndBaseTogether400(t *testing.T) {
	srv := v1.New(v1.Options{Markets: &stubMarketsReader{}})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/pools?asset=native&base=native")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var p v1.Problem
	mustDecode(t, resp, &p)
	if p.Type != "https://api.ratesengine.net/errors/conflicting-filters" {
		t.Errorf("Type = %q", p.Type)
	}
}
