package client_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/RatesEngine/rates-engine/pkg/client"
)

// TestHistorySinceInception_HappyPath — happy-path round-trip
// pinning the URL, the optional query parameters, and the
// Envelope[HistorySeries] decode shape.
func TestHistorySinceInception_HappyPath(t *testing.T) {
	_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/history/since-inception" {
			t.Errorf("path = %q, want /v1/history/since-inception", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("asset") != "crypto:XLM" {
			t.Errorf("asset = %q, want crypto:XLM", q.Get("asset"))
		}
		if q.Get("quote") != "fiat:USD" {
			t.Errorf("quote = %q, want fiat:USD", q.Get("quote"))
		}
		if q.Get("granularity") != "1d" {
			t.Errorf("granularity = %q, want 1d", q.Get("granularity"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": {
				"asset_id": "crypto:XLM",
				"quote": "fiat:USD",
				"price_type": "vwap",
				"granularity": "1d",
				"points": [
					{"t":"2024-01-01T00:00:00Z","p":"0.12345","v_usd":"100000"},
					{"t":"2024-01-02T00:00:00Z","p":"0.12500","v_usd":"95000"}
				]
			},
			"as_of": "2026-04-28T10:00:00Z",
			"flags": {"stale": false, "reduced_redundancy": false, "triangulated": false, "divergence_warning": false}
		}`))
	})

	got, err := c.HistorySinceInception(context.Background(), client.HistoryQuery{
		Asset: "crypto:XLM", Quote: "fiat:USD", Granularity: "1d",
	})
	if err != nil {
		t.Fatalf("HistorySinceInception: %v", err)
	}
	if got.Data.AssetID != "crypto:XLM" {
		t.Errorf("AssetID = %q", got.Data.AssetID)
	}
	if len(got.Data.Points) != 2 {
		t.Fatalf("len(Points) = %d, want 2", len(got.Data.Points))
	}
	if got.Data.Points[0].P != "0.12345" {
		t.Errorf("Points[0].P = %q, want 0.12345", got.Data.Points[0].P)
	}
}

// TestHistorySinceInception_AssetRequired — Asset is required;
// empty Asset short-circuits client-side without a network call.
func TestHistorySinceInception_AssetRequired(t *testing.T) {
	c := client.New(client.Options{BaseURL: "http://nope.invalid"})
	_, err := c.HistorySinceInception(context.Background(), client.HistoryQuery{})
	if err == nil {
		t.Fatal("expected error for empty Asset")
	}
	var apiErr *client.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %T, want *APIError", err)
	}
	if apiErr.Status != 400 {
		t.Errorf("Status = %d, want 400", apiErr.Status)
	}
}

// TestAssets_PaginationCarriesCursor — cursor + limit are forwarded
// as query params; missing values are omitted (no `cursor=` or
// `limit=` on a fresh-walk request).
func TestAssets_PaginationCarriesCursor(t *testing.T) {
	t.Run("with cursor + limit", func(t *testing.T) {
		_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/assets" {
				t.Errorf("path = %q", r.URL.Path)
			}
			q := r.URL.Query()
			if q.Get("cursor") != "opaque-xyz" {
				t.Errorf("cursor = %q", q.Get("cursor"))
			}
			if q.Get("limit") != "50" {
				t.Errorf("limit = %q", q.Get("limit"))
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data": [], "as_of": "2026-04-28T10:00:00Z", "flags": {}, "pagination": {"next":"next-cursor"}}`))
		})
		got, err := c.Assets(context.Background(), client.AssetsOptions{Cursor: "opaque-xyz", Limit: 50})
		if err != nil {
			t.Fatalf("Assets: %v", err)
		}
		if got.Pagination.Next != "next-cursor" {
			t.Errorf("Pagination = %+v, want {Next: next-cursor}", got.Pagination)
		}
	})

	t.Run("zero values omit query params", func(t *testing.T) {
		_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			if q.Has("cursor") {
				t.Errorf("cursor sent on fresh walk: %q", q.Get("cursor"))
			}
			if q.Has("limit") {
				t.Errorf("limit sent when 0: %q", q.Get("limit"))
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data": [], "as_of": "2026-04-28T10:00:00Z", "flags": {}}`))
		})
		_, err := c.Assets(context.Background(), client.AssetsOptions{})
		if err != nil {
			t.Fatalf("Assets: %v", err)
		}
	})
}

// TestAsset_PathEscapesAssetID — asset IDs may contain `:` (Soroban
// fiat:USD form) or `+` (URL-special); url.PathEscape is on the hot
// path. Confirms the asset_id round-trips correctly.
func TestAsset_PathEscapesAssetID(t *testing.T) {
	cases := []struct {
		raw     string
		decoded string
	}{
		{"native", "native"},
		{"USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN", "USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"},
		{"fiat:USD", "fiat:USD"},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				want := "/v1/assets/" + tc.decoded
				if !strings.HasPrefix(r.URL.Path, "/v1/assets/") {
					t.Fatalf("path = %q", r.URL.Path)
				}
				// Path is already percent-decoded by net/http when
				// it lands in r.URL.Path.
				if r.URL.Path != want {
					t.Errorf("path = %q, want %q", r.URL.Path, want)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"data": {"asset_id":"` + tc.decoded + `","type":"classic"}, "as_of": "2026-04-28T10:00:00Z", "flags": {}}`))
			})
			got, err := c.Asset(context.Background(), tc.raw)
			if err != nil {
				t.Fatalf("Asset(%q): %v", tc.raw, err)
			}
			if got.Data.AssetID != tc.decoded {
				t.Errorf("Data.AssetID = %q, want %q", got.Data.AssetID, tc.decoded)
			}
		})
	}
}

// TestAsset_AssetIDRequired pins the empty-arg short-circuit.
func TestAsset_AssetIDRequired(t *testing.T) {
	c := client.New(client.Options{BaseURL: "http://nope.invalid"})
	_, err := c.Asset(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty asset_id")
	}
	var apiErr *client.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 400 {
		t.Errorf("err = %v, want *APIError with Status 400", err)
	}
}

// TestAssetMetadata_PathPrefix — the metadata endpoint reuses the
// same path-escape pattern as Asset, with /metadata appended.
func TestAssetMetadata_PathPrefix(t *testing.T) {
	_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		want := "/v1/assets/USDC-GA5Z.../metadata"
		if r.URL.Path != want {
			t.Errorf("path = %q, want %q", r.URL.Path, want)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data": {"asset_id":"USDC-GA5Z..."}, "as_of":"2026-04-28T10:00:00Z","flags":{}}`))
	})
	_, err := c.AssetMetadata(context.Background(), "USDC-GA5Z...")
	if err != nil {
		t.Fatalf("AssetMetadata: %v", err)
	}
}

// TestMe_PathOnly — Me has no parameters; just a path round-trip.
func TestMe_PathOnly(t *testing.T) {
	_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/account/me" {
			t.Errorf("path = %q, want /v1/account/me", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data": {"key_id":"k_abc","label":"prod","tier":"sep10","rate_limit_per_min":1000}, "as_of": "2026-04-28T10:00:00Z","flags":{}}`))
	})
	got, err := c.Me(context.Background())
	if err != nil {
		t.Fatalf("Me: %v", err)
	}
	if got.Data.KeyID != "k_abc" {
		t.Errorf("KeyID = %q, want k_abc", got.Data.KeyID)
	}
	if got.Data.Tier != "sep10" {
		t.Errorf("Tier = %q", got.Data.Tier)
	}
	if got.Data.RateLimitPerMin != 1000 {
		t.Errorf("RateLimitPerMin = %d, want 1000", got.Data.RateLimitPerMin)
	}
}

// TestPriceTip_HappyPath — round-trip pinning: URL,
// window_seconds query forwarding, decode of the rolling-window
// VWAP shape (price_type="vwap").
func TestPriceTip_HappyPath(t *testing.T) {
	_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/price/tip" {
			t.Errorf("path = %q, want /v1/price/tip", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("asset") != "native" {
			t.Errorf("asset = %q", q.Get("asset"))
		}
		if q.Get("quote") != "fiat:USD" {
			t.Errorf("quote = %q", q.Get("quote"))
		}
		if q.Get("window_seconds") != "10" {
			t.Errorf("window_seconds = %q, want 10", q.Get("window_seconds"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": {"asset_id":"native","quote":"fiat:USD","price":"0.07127","price_type":"vwap","observed_at":"2026-04-28T10:00:00Z","window_seconds":10},
			"as_of": "2026-04-28T10:00:00Z",
			"flags": {"stale": false, "single_source": false, "divergence_warning": false}
		}`))
	})
	got, err := c.PriceTip(context.Background(), client.PriceTipQuery{
		Asset: "native", Quote: "fiat:USD", WindowSeconds: 10,
	})
	if err != nil {
		t.Fatalf("PriceTip: %v", err)
	}
	if got.Data.PriceType != "vwap" {
		t.Errorf("PriceType = %q, want vwap", got.Data.PriceType)
	}
	if got.Data.WindowSeconds != 10 {
		t.Errorf("WindowSeconds = %d, want 10", got.Data.WindowSeconds)
	}
	if got.Flags.Stale {
		t.Error("Flags.Stale should be false on tip surface (ADR-0018)")
	}
}

// TestPriceTip_LastTradeBranch — empty-window branch returns
// last_trade with no window_seconds. Pinned because customers
// distinguishing the two branches via PriceType is the surface's
// main contract.
func TestPriceTip_LastTradeBranch(t *testing.T) {
	_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": {"asset_id":"native","quote":"fiat:USD","price":"0.07","price_type":"last_trade","observed_at":"2026-04-28T09:55:30Z"},
			"as_of": "2026-04-28T10:00:00Z",
			"flags": {"stale": false}
		}`))
	})
	got, err := c.PriceTip(context.Background(), client.PriceTipQuery{Asset: "native"})
	if err != nil {
		t.Fatalf("PriceTip: %v", err)
	}
	if got.Data.PriceType != "last_trade" {
		t.Errorf("PriceType = %q, want last_trade", got.Data.PriceType)
	}
	// last_trade branch has no window_seconds — JSON omitempty
	// elides the field entirely.
	if got.Data.WindowSeconds != 0 {
		t.Errorf("WindowSeconds = %d, want 0 on last_trade branch", got.Data.WindowSeconds)
	}
}

// TestPriceTip_OmitsZeroWindowSeconds — the SDK MUST NOT send
// `window_seconds=0` (the server treats 0 as "use default"; sending
// it explicitly is wasted bandwidth). Pinned because a regression
// that always sends the field would change the URL signature.
func TestPriceTip_OmitsZeroWindowSeconds(t *testing.T) {
	_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("window_seconds") {
			t.Errorf("window_seconds sent on zero: %q", r.URL.Query().Get("window_seconds"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"asset_id":"native","quote":"fiat:USD","price":"0.07","price_type":"vwap","observed_at":"2026-04-28T10:00:00Z"},"as_of":"2026-04-28T10:00:00Z","flags":{}}`))
	})
	if _, err := c.PriceTip(context.Background(), client.PriceTipQuery{Asset: "native"}); err != nil {
		t.Fatalf("PriceTip: %v", err)
	}
}

// TestPriceTip_AssetRequired — empty Asset short-circuits.
func TestPriceTip_AssetRequired(t *testing.T) {
	c := client.New(client.Options{BaseURL: "http://nope.invalid"})
	_, err := c.PriceTip(context.Background(), client.PriceTipQuery{})
	if err == nil {
		t.Fatal("expected error for empty Asset")
	}
	var apiErr *client.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 400 {
		t.Errorf("err = %v, want *APIError Status=400", err)
	}
}

// TestPriceBatch_GETUnder100 — a 3-asset batch routes via GET
// with the canonical comma-separated `asset_ids` param. Pinned
// because the GET-vs-POST routing is the SDK's main value-add
// over a hand-rolled curl.
func TestPriceBatch_GETUnder100(t *testing.T) {
	_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET (≤100 ids should not POST)", r.Method)
		}
		if r.URL.Path != "/v1/price/batch" {
			t.Errorf("path = %q, want /v1/price/batch", r.URL.Path)
		}
		ids := r.URL.Query().Get("asset_ids")
		if ids != "native,crypto:BTC,credit:USDC-GA5Z" {
			t.Errorf("asset_ids = %q, want comma-joined order-preserved", ids)
		}
		if q := r.URL.Query().Get("quote"); q != "fiat:USD" {
			t.Errorf("quote = %q, want fiat:USD", q)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": [
				{"asset_id":"native","quote":"fiat:USD","price":"0.07","price_type":"vwap","observed_at":"2026-04-28T10:00:00Z"},
				{"asset_id":"crypto:BTC","quote":"fiat:USD","price":"96000.0","price_type":"vwap","observed_at":"2026-04-28T10:00:00Z"}
			],
			"as_of": "2026-04-28T10:00:00Z",
			"flags": {"stale": false, "reduced_redundancy": false, "triangulated": false, "divergence_warning": false}
		}`))
	})
	// 3 ids in, 2 out — the third silently omitted by the server
	// (per the docstring's "missing observations are omitted").
	got, err := c.PriceBatch(context.Background(), client.PriceBatchQuery{
		AssetIDs: []string{"native", "crypto:BTC", "credit:USDC-GA5Z"},
		Quote:    "fiat:USD",
	})
	if err != nil {
		t.Fatalf("PriceBatch: %v", err)
	}
	if len(got.Data) != 2 {
		t.Fatalf("len(Data) = %d, want 2 (server omits unknown)", len(got.Data))
	}
	if got.Data[0].AssetID != "native" {
		t.Errorf("Data[0].AssetID = %q, want native", got.Data[0].AssetID)
	}
}

// TestPriceBatch_POSTOver100 — a 150-asset batch routes via POST
// with a JSON body (the GET form would blow past most reverse
// proxies' 8 KiB header limit). Pinned because the threshold
// crossing is the SDK's job, not the caller's.
func TestPriceBatch_POSTOver100(t *testing.T) {
	ids := make([]string, 150)
	for i := range ids {
		ids[i] = "credit:T" + strconv.Itoa(i) + "-G" + strings.Repeat("A", 56)
	}
	var sawMethod string
	var sawAssetIDsLen int
	_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		sawMethod = r.Method
		if r.URL.Path != "/v1/price/batch" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.URL.RawQuery != "" {
			t.Errorf("query string non-empty on POST: %q", r.URL.RawQuery)
		}
		// Decode the body to verify the asset_ids round-tripped.
		var body struct {
			AssetIDs []string `json:"asset_ids"`
			Quote    string   `json:"quote"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		sawAssetIDsLen = len(body.AssetIDs)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[],"as_of":"2026-04-28T10:00:00Z","flags":{}}`))
	})
	if _, err := c.PriceBatch(context.Background(), client.PriceBatchQuery{AssetIDs: ids}); err != nil {
		t.Fatalf("PriceBatch: %v", err)
	}
	if sawMethod != http.MethodPost {
		t.Errorf("method = %q, want POST (>100 ids must POST)", sawMethod)
	}
	if sawAssetIDsLen != 150 {
		t.Errorf("body asset_ids len = %d, want 150", sawAssetIDsLen)
	}
}

// TestPriceBatch_EmptyAssetIDs — empty batch short-circuits
// client-side without a network call. Mirrors the
// PriceQuery.Asset == "" check on the single-asset method.
func TestPriceBatch_EmptyAssetIDs(t *testing.T) {
	c := client.New(client.Options{BaseURL: "http://nope.invalid"})
	_, err := c.PriceBatch(context.Background(), client.PriceBatchQuery{})
	if err == nil {
		t.Fatal("expected error for empty AssetIDs")
	}
	var apiErr *client.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %T, want *APIError", err)
	}
	if apiErr.Status != 400 {
		t.Errorf("Status = %d, want 400", apiErr.Status)
	}
}

// TestPriceBatch_OverPOSTCap — >1000 ids never round-trip; the
// SDK rejects client-side. Splitting into chunks would mask the
// envelope-wide flags.stale OR semantic on subsets the caller
// wouldn't see — that's a caller decision, not the SDK's.
func TestPriceBatch_OverPOSTCap(t *testing.T) {
	ids := make([]string, 1001)
	for i := range ids {
		ids[i] = "x" + strconv.Itoa(i)
	}
	c := client.New(client.Options{BaseURL: "http://nope.invalid"})
	_, err := c.PriceBatch(context.Background(), client.PriceBatchQuery{AssetIDs: ids})
	if err == nil {
		t.Fatal("expected error for >1000 ids")
	}
	var apiErr *client.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %T, want *APIError", err)
	}
	if apiErr.Status != 400 {
		t.Errorf("Status = %d, want 400", apiErr.Status)
	}
}

// TestOHLC_HappyPath — happy-path round-trip pinning required
// query params + decode of OHLCBar shape with all four price
// fields + the truncated flag.
// fields + the truncated flag.
func TestOHLC_HappyPath(t *testing.T) {
	from := time.Date(2026, 4, 28, 9, 0, 0, 0, time.UTC)
	to := time.Date(2026, 4, 28, 10, 0, 0, 0, time.UTC)
	_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/ohlc" {
			t.Errorf("path = %q", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("base") != "native" {
			t.Errorf("base = %q", q.Get("base"))
		}
		if q.Get("quote") != "fiat:USD" {
			t.Errorf("quote = %q", q.Get("quote"))
		}
		if q.Get("from") != "2026-04-28T09:00:00Z" {
			t.Errorf("from = %q, want RFC3339 UTC", q.Get("from"))
		}
		if q.Get("to") != "2026-04-28T10:00:00Z" {
			t.Errorf("to = %q", q.Get("to"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": {
				"from": "2026-04-28T09:00:00Z",
				"to":   "2026-04-28T10:00:00Z",
				"open": "0.0708000000",
				"high": "0.0715000000",
				"low":  "0.0701000000",
				"close":"0.0712700000",
				"base_volume":  "5000000000000",
				"quote_volume":"35635000000",
				"trade_count": 4123,
				"truncated": false
			},
			"as_of": "2026-04-28T10:00:01Z",
			"flags": {}
		}`))
	})
	got, err := c.OHLC(context.Background(), client.OHLCQuery{
		Base: "native", Quote: "fiat:USD",
		From: from, To: to,
	})
	if err != nil {
		t.Fatalf("OHLC: %v", err)
	}
	if got.Data.Open != "0.0708000000" {
		t.Errorf("Open = %q", got.Data.Open)
	}
	if got.Data.High != "0.0715000000" {
		t.Errorf("High = %q", got.Data.High)
	}
	if got.Data.Close != "0.0712700000" {
		t.Errorf("Close = %q", got.Data.Close)
	}
	if got.Data.TradeCount != 4123 {
		t.Errorf("TradeCount = %d", got.Data.TradeCount)
	}
	if got.Data.Truncated {
		t.Error("Truncated = true unexpectedly")
	}
}

// TestOHLC_OmitsZeroTimes — zero From/To means "use server
// defaults"; SDK MUST NOT send `from=` / `to=` empty (RFC3339
// of a zero time renders "0001-01-01T00:00:00Z" which the
// server would accept as a real 1AD timestamp).
func TestOHLC_OmitsZeroTimes(t *testing.T) {
	_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Has("from") {
			t.Errorf("from sent on zero-time: %q", q.Get("from"))
		}
		if q.Has("to") {
			t.Errorf("to sent on zero-time: %q", q.Get("to"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"from":"2026-04-28T09:00:00Z","to":"2026-04-28T10:00:00Z","open":"0","high":"0","low":"0","close":"0","base_volume":"0","quote_volume":"0","trade_count":0,"truncated":false},"as_of":"2026-04-28T10:00:00Z","flags":{}}`))
	})
	if _, err := c.OHLC(context.Background(), client.OHLCQuery{
		Base: "native", Quote: "fiat:USD",
	}); err != nil {
		t.Fatalf("OHLC: %v", err)
	}
}

// TestOHLC_BaseQuoteRequired — both Base and Quote must be set.
// /v1/ohlc deliberately doesn't default Quote to fiat:USD (unlike
// /v1/price) — candlestick charts pin a specific pair so the SDK
// must as well.
func TestOHLC_BaseQuoteRequired(t *testing.T) {
	c := client.New(client.Options{BaseURL: "http://nope.invalid"})
	for _, tc := range []struct {
		name string
		q    client.OHLCQuery
	}{
		{"empty Base", client.OHLCQuery{Quote: "fiat:USD"}},
		{"empty Quote", client.OHLCQuery{Base: "native"}},
		{"both empty", client.OHLCQuery{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := c.OHLC(context.Background(), tc.q)
			if err == nil {
				t.Fatal("expected error")
			}
			var apiErr *client.APIError
			if !errors.As(err, &apiErr) || apiErr.Status != 400 {
				t.Errorf("err = %v, want *APIError 400", err)
			}
		})
	}
}

// TestOHLC_TruncatedDecodes — when a window holds more trades
// than the server's cap, `truncated: true` is on the wire and
// the SDK round-trips it. Pinned because consumers building
// chart UIs need this signal to decide whether to narrow.
func TestOHLC_TruncatedDecodes(t *testing.T) {
	_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"from":"2026-04-28T09:00:00Z","to":"2026-04-28T10:00:00Z","open":"1","high":"2","low":"0.5","close":"1.5","base_volume":"100","quote_volume":"150","trade_count":10000,"truncated":true},"as_of":"2026-04-28T10:00:00Z","flags":{}}`))
	})
	got, err := c.OHLC(context.Background(), client.OHLCQuery{Base: "native", Quote: "fiat:USD"})
	if err != nil {
		t.Fatalf("OHLC: %v", err)
	}
	if !got.Data.Truncated {
		t.Error("Truncated = false, want true (server-side cap signal must survive round-trip)")
	}
	if got.Data.TradeCount != 10000 {
		t.Errorf("TradeCount = %d, want 10000", got.Data.TradeCount)
	}
}

// TestUsage_EmptyArrayDecodes — the placeholder usage endpoint
// returns an empty array today; client should decode that without
// panicking on a nil slice.
// TestHistory_HappyPath — round-trip pinning required params,
// optional from/to, decode of TradeRow.
func TestHistory_HappyPath(t *testing.T) {
	from := time.Date(2026, 4, 28, 9, 0, 0, 0, time.UTC)
	to := time.Date(2026, 4, 28, 10, 0, 0, 0, time.UTC)
	_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/history" {
			t.Errorf("path = %q, want /v1/history", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("base") != "native" {
			t.Errorf("base = %q", q.Get("base"))
		}
		if q.Get("quote") != "fiat:USD" {
			t.Errorf("quote = %q", q.Get("quote"))
		}
		if q.Get("from") != "2026-04-28T09:00:00Z" {
			t.Errorf("from = %q", q.Get("from"))
		}
		if q.Get("limit") != "500" {
			t.Errorf("limit = %q, want 500", q.Get("limit"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": [
				{"source":"sdex","ledger":50000000,"tx_hash":"abc","op_index":0,"ts":"2026-04-28T09:30:00Z","base_asset":"native","quote_asset":"fiat:USD","base_amount":"10000000","quote_amount":"700000","price":"0.0700000000"},
				{"source":"soroswap","ledger":50000010,"tx_hash":"def","op_index":1,"ts":"2026-04-28T09:45:00Z","base_asset":"native","quote_asset":"fiat:USD","base_amount":"5000000","quote_amount":"350000","price":"0.0700000000"}
			],
			"as_of": "2026-04-28T10:00:00Z",
			"flags": {},
			"pagination": {"next":"opaque-cursor"}
		}`))
	})
	got, err := c.History(context.Background(), client.HistoryRangeQuery{
		Base: "native", Quote: "fiat:USD",
		From: from, To: to, Limit: 500,
	})
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(got.Data) != 2 {
		t.Fatalf("len(Data) = %d, want 2", len(got.Data))
	}
	if got.Data[0].Source != "sdex" {
		t.Errorf("Data[0].Source = %q", got.Data[0].Source)
	}
	if got.Data[1].Ledger != 50000010 {
		t.Errorf("Data[1].Ledger = %d", got.Data[1].Ledger)
	}
	if got.Pagination.Next != "opaque-cursor" {
		t.Errorf("Pagination.Next = %q", got.Pagination.Next)
	}
}

// TestHistory_PaginationCarriesCursor — cursor walks forward.
// Pinned because the cursor field is the SDK's main value-add
// over a hand-rolled query string for multi-page exports.
func TestHistory_PaginationCarriesCursor(t *testing.T) {
	_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("cursor") != "page-2" {
			t.Errorf("cursor = %q, want page-2", q.Get("cursor"))
		}
		// from/to should NOT be sent when cursor is set —
		// well, actually the SDK should still forward them if the
		// caller set them; the server uses cursor as the lower
		// bound override. Don't assert absence.
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data": [], "as_of":"2026-04-28T10:00:00Z","flags":{}}`))
	})
	if _, err := c.History(context.Background(), client.HistoryRangeQuery{
		Base: "native", Quote: "fiat:USD", Cursor: "page-2",
	}); err != nil {
		t.Fatalf("History: %v", err)
	}
}

// TestHistory_OmitsZeroOptional — zero From/To/Limit/Cursor
// don't render on the URL. Pinned because a leaky zero would
// either send "0001-01-01T00:00:00Z" or `limit=0` (which the
// server treats as invalid).
func TestHistory_OmitsZeroOptional(t *testing.T) {
	_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		for _, k := range []string{"from", "to", "limit", "cursor"} {
			if q.Has(k) {
				t.Errorf("%s sent on zero value: %q", k, q.Get(k))
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data": [], "as_of":"2026-04-28T10:00:00Z","flags":{}}`))
	})
	if _, err := c.History(context.Background(), client.HistoryRangeQuery{
		Base: "native", Quote: "fiat:USD",
	}); err != nil {
		t.Fatalf("History: %v", err)
	}
}

// TestHistory_BaseQuoteRequired — both Base and Quote required;
// short-circuits client-side without a network call.
func TestHistory_BaseQuoteRequired(t *testing.T) {
	c := client.New(client.Options{BaseURL: "http://nope.invalid"})
	for _, tc := range []struct {
		name string
		q    client.HistoryRangeQuery
	}{
		{"empty Base", client.HistoryRangeQuery{Quote: "fiat:USD"}},
		{"empty Quote", client.HistoryRangeQuery{Base: "native"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := c.History(context.Background(), tc.q)
			if err == nil {
				t.Fatal("expected error")
			}
			var apiErr *client.APIError
			if !errors.As(err, &apiErr) || apiErr.Status != 400 {
				t.Errorf("err = %v, want *APIError 400", err)
			}
		})
	}
}

// TestSources_HappyPath — happy-path round-trip pinning the
// optional class filter + decode of the registry shape.
func TestSources_HappyPath(t *testing.T) {
	_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/sources" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.URL.Query().Get("class") != "exchange" {
			t.Errorf("class = %q, want exchange", r.URL.Query().Get("class"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": [
				{"name":"binance","class":"exchange","subclass":"cex","include_in_vwap":true,"paid":false,"backfill_available":true,"backfill_safe":true,"default_weight":100},
				{"name":"soroswap","class":"exchange","subclass":"dex","include_in_vwap":true,"paid":false,"backfill_available":true,"backfill_safe":true,"default_weight":100}
			],
			"as_of":"2026-04-28T10:00:00Z","flags":{}
		}`))
	})
	got, err := c.Sources(context.Background(), client.SourcesOptions{Class: "exchange"})
	if err != nil {
		t.Fatalf("Sources: %v", err)
	}
	if len(got.Data) != 2 {
		t.Fatalf("len(Data) = %d, want 2", len(got.Data))
	}
	if got.Data[0].Subclass != "cex" {
		t.Errorf("Data[0].Subclass = %q", got.Data[0].Subclass)
	}
	if !got.Data[1].BackfillSafe {
		t.Error("Data[1].BackfillSafe = false, want true")
	}
}

// TestSources_EmptyClassOmitsParam — empty Class doesn't render
// `class=` on the URL (server would reject empty). Pinned because
// a leaky zero would 400 every "list everything" call.
func TestSources_EmptyClassOmitsParam(t *testing.T) {
	_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("class") {
			t.Errorf("class sent on empty filter: %q", r.URL.Query().Get("class"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[],"as_of":"2026-04-28T10:00:00Z","flags":{}}`))
	})
	if _, err := c.Sources(context.Background(), client.SourcesOptions{}); err != nil {
		t.Fatalf("Sources: %v", err)
	}
}

// TestMarkets_PaginationCarriesCursor — same paginating shape as
// Assets. Pinned because catalogue-walking is the SDK's main
// value-add over rolling cursor handling by hand.
func TestMarkets_PaginationCarriesCursor(t *testing.T) {
	_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/markets" {
			t.Errorf("path = %q", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("cursor") != "page-2" {
			t.Errorf("cursor = %q", q.Get("cursor"))
		}
		if q.Get("limit") != "200" {
			t.Errorf("limit = %q", q.Get("limit"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": [
				{"base":"native","quote":"fiat:USD","last_trade_at":"2026-04-28T09:55:00Z","trade_count_24h":12345}
			],
			"as_of":"2026-04-28T10:00:00Z","flags":{},
			"pagination":{"next":"page-3"}
		}`))
	})
	got, err := c.Markets(context.Background(), client.MarketsOptions{Cursor: "page-2", Limit: 200})
	if err != nil {
		t.Fatalf("Markets: %v", err)
	}
	if got.Data[0].TradeCount24h != 12345 {
		t.Errorf("TradeCount24h = %d", got.Data[0].TradeCount24h)
	}
	if got.Pagination.Next != "page-3" {
		t.Errorf("Pagination.Next = %q", got.Pagination.Next)
	}
}

// TestPair_HappyPath — single-pair lookup returns 0-or-1 array.
func TestPair_HappyPath(t *testing.T) {
	_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/pairs" {
			t.Errorf("path = %q", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("base") != "native" || q.Get("quote") != "fiat:USD" {
			t.Errorf("base=%q quote=%q", q.Get("base"), q.Get("quote"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": [{"base":"native","quote":"fiat:USD","last_trade_at":"2026-04-28T09:55:00Z","trade_count_24h":4123}],
			"as_of":"2026-04-28T10:00:00Z","flags":{}
		}`))
	})
	got, err := c.Pair(context.Background(), "native", "fiat:USD")
	if err != nil {
		t.Fatalf("Pair: %v", err)
	}
	if len(got.Data) != 1 {
		t.Fatalf("len(Data) = %d, want 1", len(got.Data))
	}
	if got.Data[0].Base != "native" {
		t.Errorf("Base = %q", got.Data[0].Base)
	}
}

// TestPair_EmptyArrayOnUnknownPair — server's 0-or-1 array shape
// returns an empty array for unknown pairs, not a 404. Pinned
// because the SDK's behaviour MUST mirror that — branching on
// status code would defeat the design.
func TestPair_EmptyArrayOnUnknownPair(t *testing.T) {
	_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[],"as_of":"2026-04-28T10:00:00Z","flags":{}}`))
	})
	got, err := c.Pair(context.Background(), "credit:UNKNOWN", "fiat:USD")
	if err != nil {
		t.Fatalf("Pair: %v (unknown pair must NOT error)", err)
	}
	if len(got.Data) != 0 {
		t.Errorf("len(Data) = %d, want 0", len(got.Data))
	}
}

// TestPair_BaseQuoteRequired — both arguments are required.
func TestPair_BaseQuoteRequired(t *testing.T) {
	c := client.New(client.Options{BaseURL: "http://nope.invalid"})
	for _, tc := range []struct {
		name        string
		base, quote string
	}{
		{"empty base", "", "fiat:USD"},
		{"empty quote", "native", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := c.Pair(context.Background(), tc.base, tc.quote)
			if err == nil {
				t.Fatal("expected error")
			}
			var apiErr *client.APIError
			if !errors.As(err, &apiErr) || apiErr.Status != 400 {
				t.Errorf("err = %v, want *APIError 400", err)
			}
		})
	}
}

func TestUsage_EmptyArrayDecodes(t *testing.T) {
	_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/account/usage" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data": [], "as_of": "2026-04-28T10:00:00Z","flags":{}}`))
	})
	got, err := c.Usage(context.Background())
	if err != nil {
		t.Fatalf("Usage: %v", err)
	}
	if len(got.Data) != 0 {
		t.Errorf("len(Data) = %d, want 0", len(got.Data))
	}
}

// TestCoins_IssuerFilter — exercises the optional ?issuer= deep-
// link the explorer /issuers table relies on.
func TestCoins_IssuerFilter(t *testing.T) {
	const issuer = "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"

	t.Run("with issuer + limit", func(t *testing.T) {
		_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/coins" {
				t.Errorf("path = %q", r.URL.Path)
			}
			q := r.URL.Query()
			if q.Get("issuer") != issuer {
				t.Errorf("issuer = %q, want %q", q.Get("issuer"), issuer)
			}
			if q.Get("limit") != "50" {
				t.Errorf("limit = %q", q.Get("limit"))
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data": {"coins": [{"slug":"USDC","asset_id":"USDC-` + issuer + `","code":"USDC","issuer":"` + issuer + `","first_seen_ledger":50457424,"last_seen_ledger":62413938,"observation_count":41610618}], "next_cursor": "", "limit": 50}, "as_of": "2026-05-04T15:00:00Z", "flags": {}}`))
		})
		got, err := c.Coins(context.Background(), client.CoinsOptions{Issuer: issuer, Limit: 50})
		if err != nil {
			t.Fatalf("Coins: %v", err)
		}
		if len(got.Data.Coins) != 1 || got.Data.Coins[0].Code != "USDC" {
			t.Errorf("Data.Coins = %+v", got.Data.Coins)
		}
	})

	t.Run("zero values omit query params", func(t *testing.T) {
		_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			if q.Has("issuer") {
				t.Errorf("issuer sent unfiltered: %q", q.Get("issuer"))
			}
			if q.Has("limit") {
				t.Errorf("limit sent when 0: %q", q.Get("limit"))
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data": {"coins": [], "next_cursor": "", "limit": 100}, "as_of": "2026-05-04T15:00:00Z", "flags": {}}`))
		})
		_, err := c.Coins(context.Background(), client.CoinsOptions{})
		if err != nil {
			t.Fatalf("Coins: %v", err)
		}
	})
}

// TestIssuers_Limit — issuer directory respects Limit and unwraps
// the {data:[…]} envelope correctly.
func TestIssuers_Limit(t *testing.T) {
	_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/issuers" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.URL.Query().Get("limit") != "10" {
			t.Errorf("limit = %q", r.URL.Query().Get("limit"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data": [{"g_strkey":"GA5Z","home_domain":"centre.io","asset_count":1,"total_observation_count":41610618}], "as_of": "2026-05-04T15:00:00Z", "flags": {}}`))
	})
	got, err := c.Issuers(context.Background(), client.IssuersOptions{Limit: 10})
	if err != nil {
		t.Fatalf("Issuers: %v", err)
	}
	if len(got.Data) != 1 || got.Data[0].HomeDomain != "centre.io" {
		t.Errorf("Data = %+v", got.Data)
	}
}

// TestIssuer_PathEscapes — G-strkeys are 56 chars of base32 so
// PathEscape isn't strictly load-bearing today, but the test
// pins the escape path so a future identifier scheme that uses
// special characters doesn't silently break.
func TestIssuer_PathEscapes(t *testing.T) {
	const g = "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"
	_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		want := "/v1/issuers/" + g
		if r.URL.Path != want {
			t.Errorf("path = %q, want %q", r.URL.Path, want)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data": {"g_strkey":"` + g + `","home_domain":"centre.io"}, "as_of": "2026-05-04T15:00:00Z", "flags": {}}`))
	})
	got, err := c.Issuer(context.Background(), g)
	if err != nil {
		t.Fatalf("Issuer: %v", err)
	}
	if got.Data.GStrkey != g {
		t.Errorf("GStrkey = %q, want %q", got.Data.GStrkey, g)
	}
}

// TestIssuer_GStrkeyRequired — the SDK rejects empty G-strkey at
// the boundary instead of round-tripping a 404 — saves a network
// hop and surfaces the real bug at the call site.
func TestIssuer_GStrkeyRequired(t *testing.T) {
	c := client.New(client.Options{BaseURL: "http://nope.invalid"})
	_, err := c.Issuer(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty g_strkey")
	}
	var apiErr *client.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 400 {
		t.Errorf("err = %v, want *APIError with Status 400", err)
	}
}

// TestKeys_HappyPath — list keys for the authenticated caller;
// pins the wire shape (Account[]) and that order is preserved.
func TestKeys_HappyPath(t *testing.T) {
	_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/account/keys" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data": [
			{"key_id":"k_old","label":"signup","tier":"apikey","rate_limit_per_min":1000,"created_at":"2026-01-01T00:00:00Z"},
			{"key_id":"k_new","label":"rotation","tier":"apikey","rate_limit_per_min":1000,"created_at":"2026-04-01T00:00:00Z"}
		], "as_of": "2026-05-04T14:35:42Z", "flags": {}}`))
	})
	got, err := c.Keys(context.Background())
	if err != nil {
		t.Fatalf("Keys: %v", err)
	}
	if len(got.Data) != 2 {
		t.Fatalf("len = %d, want 2", len(got.Data))
	}
	if got.Data[0].KeyID != "k_old" || got.Data[1].KeyID != "k_new" {
		t.Errorf("ordering broken: %+v", got.Data)
	}
}

// TestRevokeKey_HappyPath — DELETE returns 204 No Content; the
// SDK call returns nil error.
func TestRevokeKey_HappyPath(t *testing.T) {
	_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/account/keys/k_target" {
			t.Errorf("path = %q, want /v1/account/keys/k_target", r.URL.Path)
		}
		if r.Method != http.MethodDelete {
			t.Errorf("method = %q, want DELETE", r.Method)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	if err := c.RevokeKey(context.Background(), "k_target"); err != nil {
		t.Fatalf("RevokeKey: %v", err)
	}
}

// TestRevokeKey_EmptyKeyID — argument validation runs client-side
// without a network round trip, returning a 400-classed APIError.
func TestRevokeKey_EmptyKeyID(t *testing.T) {
	_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("server hit for empty keyID — should validate client-side")
	})
	err := c.RevokeKey(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty keyID")
	}
	var apiErr *client.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 400 {
		t.Errorf("err = %v, want *APIError with Status 400", err)
	}
}

// TestRevokeKey_404 — server says the key doesn't exist (or was
// already revoked); SDK surfaces it as *APIError so callers can
// branch on the status without parsing the message.
func TestRevokeKey_404(t *testing.T) {
	_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"type":"https://api.ratesengine.net/errors/key-not-found","title":"Key not found","status":404}`))
	})
	err := c.RevokeKey(context.Background(), "k_missing")
	if err == nil {
		t.Fatal("expected error from 404 response")
	}
	var apiErr *client.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 404 {
		t.Errorf("err = %v, want *APIError with Status 404", err)
	}
}

// TestStatus_HappyPath — pins the wire contract for /v1/status,
// including the nested Region / Latency / Freshness / Incidents
// shapes.
func TestStatus_HappyPath(t *testing.T) {
	_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/status" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": {
				"overall": "ok",
				"region": {"name": "r1", "deployment": "production"},
				"services": [{"name":"api","status":"ok","last_seen":"2026-05-05T15:00:00Z"}],
				"latency": {"p50_ms": 5.2, "p95_ms": 89.1, "p99_ms": 240.0, "window_secs": 300},
				"freshness": {"active_sources": 13, "total_sources": 17},
				"incidents": {"active_count": 0, "page_count": 0, "ticket_count": 0, "informational_count": 0}
			},
			"as_of": "2026-05-05T15:00:00.001Z",
			"flags": {"stale": false}
		}`))
	})
	got, err := c.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if got.Data.Overall != "ok" {
		t.Errorf("Overall = %q", got.Data.Overall)
	}
	if got.Data.Region.Name != "r1" {
		t.Errorf("Region.Name = %q", got.Data.Region.Name)
	}
	if got.Data.Latency.P95Ms != 89.1 {
		t.Errorf("P95Ms = %v", got.Data.Latency.P95Ms)
	}
	if got.Data.Freshness.ActiveSources != 13 {
		t.Errorf("ActiveSources = %d", got.Data.Freshness.ActiveSources)
	}
}

// TestHealthz / TestReadyz / TestVersion — operational helpers.
func TestHealthz_HappyPath(t *testing.T) {
	_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/healthz" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"status":"ok","uptime":"22m"},"as_of":"2026-05-05T15:00:00Z","flags":{}}`))
	})
	got, err := c.Healthz(context.Background())
	if err != nil || got.Data.Status != "ok" {
		t.Fatalf("Healthz = %v, err = %v", got, err)
	}
}

func TestVersion_HappyPath(t *testing.T) {
	_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/version" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"version":"v0.0.0-rc.1","build_date":"2026-05-05","commit":"abc123","dirty":"false","go_version":"go1.25"},"as_of":"2026-05-05T15:00:00Z","flags":{}}`))
	})
	got, err := c.Version(context.Background())
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if got.Data.Version != "v0.0.0-rc.1" || got.Data.GoVersion != "go1.25" {
		t.Errorf("Data = %+v", got.Data)
	}
}

// TestCursors_HappyPath — diagnostics endpoint returns
// non-paginated array; test pins the wire shape.
func TestCursors_HappyPath(t *testing.T) {
	_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/diagnostics/cursors" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data": [{"source":"sdex","sub_source":"","last_ledger":62413938,"last_updated":"2026-05-04T14:35:00Z","lag_seconds":42}], "as_of": "2026-05-04T14:35:42Z", "flags": {}}`))
	})
	got, err := c.Cursors(context.Background())
	if err != nil {
		t.Fatalf("Cursors: %v", err)
	}
	if len(got.Data) != 1 || got.Data[0].Source != "sdex" || got.Data[0].LagSeconds != 42 {
		t.Errorf("Data = %+v", got.Data)
	}
}
