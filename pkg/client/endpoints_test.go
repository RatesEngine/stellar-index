package client_test

import (
	"context"
	"errors"
	"net/http"
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

// TestOHLC_HappyPath — happy-path round-trip pinning required
// query params + decode of OHLCBar shape with all four price
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
