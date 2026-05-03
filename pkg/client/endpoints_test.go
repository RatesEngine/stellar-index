package client_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"

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
