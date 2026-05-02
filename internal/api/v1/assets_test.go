package v1_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	v1 "github.com/RatesEngine/rates-engine/internal/api/v1"
	"github.com/RatesEngine/rates-engine/internal/canonical"
)

const testUSDCIssuer = "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"

// stubAssetReader implements v1.AssetReader in-memory. Each test
// instantiates one with the fixture data it needs.
type stubAssetReader struct {
	byID    map[string]v1.AssetDetail
	page    []v1.AssetDetail
	nextCur string
	err     error // non-nil → both methods return this; for the 500-path tests
}

func (r *stubAssetReader) GetAsset(_ context.Context, a canonical.Asset) (v1.AssetDetail, error) {
	if r.err != nil {
		return v1.AssetDetail{}, r.err
	}
	d, ok := r.byID[a.String()]
	if !ok {
		return v1.AssetDetail{}, v1.ErrAssetNotFound
	}
	return d, nil
}

func (r *stubAssetReader) ListAssets(_ context.Context, cursor string, limit int) ([]v1.AssetDetail, string, error) {
	if r.err != nil {
		return nil, "", r.err
	}
	return r.page, r.nextCur, nil
}

// ─── /v1/assets (list) ────────────────────────────────────────────

func TestAssetList_EmptyWhenReaderNil(t *testing.T) {
	// Prove the default "reader not wired yet" path is 200 + empty.
	srv := v1.New(v1.Options{})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/assets")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var env struct {
		Data []v1.AssetDetail `json:"data"`
	}
	mustDecode(t, resp, &env)
	if len(env.Data) != 0 {
		t.Errorf("expected empty list, got %d rows", len(env.Data))
	}
}

func TestAssetList_NilSliceFromReaderMarshalsAsEmptyArray(t *testing.T) {
	// Regression: a reader returning (nil, "", nil) must not leak
	// "data": null onto the wire — OpenAPI's AssetListEnvelope.data
	// is `type: array`, which rejects null. The handler's nil guard
	// converts nil → [].
	reader := &stubAssetReader{page: nil, nextCur: ""}
	srv := v1.New(v1.Options{Assets: reader})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/assets")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body, _ := readAll(resp)
	// Don't decode through []T — that hides null. Assert on the raw
	// bytes that the field is an empty array.
	if !bytes.Contains([]byte(body), []byte(`"data":[]`)) {
		t.Errorf("expected \"data\":[] in body, got: %s", body)
	}
}

func TestAssetList_ReturnsFixtureWithPagination(t *testing.T) {
	native := v1.AssetDetail{
		AssetID: "native", Type: "native", Code: "XLM",
		Decimals: 7, Sep1Status: "not_applicable",
	}
	reader := &stubAssetReader{
		page:    []v1.AssetDetail{native},
		nextCur: "opaque-next",
	}
	srv := v1.New(v1.Options{Assets: reader})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/assets?limit=50")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var env struct {
		Data       []v1.AssetDetail `json:"data"`
		Pagination struct {
			Next string `json:"next"`
		} `json:"pagination"`
	}
	mustDecode(t, resp, &env)
	if len(env.Data) != 1 || env.Data[0].AssetID != "native" {
		t.Fatalf("data wrong: %+v", env.Data)
	}
	if env.Pagination.Next != "opaque-next" {
		t.Errorf("pagination next = %q", env.Pagination.Next)
	}
}

func TestAssetList_InvalidLimitRejected(t *testing.T) {
	srv := v1.New(v1.Options{})
	ts := httpTestServer(t, srv)

	for _, raw := range []string{"0", "501", "abc", "-1"} {
		t.Run("limit="+raw, func(t *testing.T) {
			resp := mustGet(t, ts.URL+"/v1/assets?limit="+raw)
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", resp.StatusCode)
			}
			body, _ := readAll(resp)
			if !strings.Contains(body, "invalid-limit") {
				t.Errorf("error type missing: %s", body)
			}
		})
	}
}

// ─── /v1/assets/{asset_id} (single) ───────────────────────────────

func TestAssetGet_native(t *testing.T) {
	srv := v1.New(v1.Options{})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/assets/native")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var env struct {
		Data v1.AssetDetail `json:"data"`
	}
	mustDecode(t, resp, &env)
	if env.Data.AssetID != "native" || env.Data.Type != "native" {
		t.Errorf("wrong asset: %+v", env.Data)
	}
}

func TestAssetGet_classicEcho(t *testing.T) {
	srv := v1.New(v1.Options{}) // nil reader → canonical-echo path
	ts := httpTestServer(t, srv)

	url := ts.URL + "/v1/assets/USDC-" + testUSDCIssuer
	resp := mustGet(t, url)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var env struct {
		Data v1.AssetDetail `json:"data"`
	}
	mustDecode(t, resp, &env)
	if env.Data.Type != "classic" || env.Data.Code != "USDC" {
		t.Errorf("wrong decode: %+v", env.Data)
	}
	if env.Data.Issuer == nil || *env.Data.Issuer != testUSDCIssuer {
		t.Errorf("issuer missing: %+v", env.Data.Issuer)
	}
}

func TestAssetGet_fiatVariant(t *testing.T) {
	// ADR-0010: fiat:USD is a first-class asset.
	srv := v1.New(v1.Options{})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/assets/fiat:USD")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var env struct {
		Data v1.AssetDetail `json:"data"`
	}
	mustDecode(t, resp, &env)
	if env.Data.Type != "fiat" || env.Data.Code != "USD" {
		t.Errorf("fiat decode wrong: %+v", env.Data)
	}
}

func TestAssetGet_invalidIdReturns400(t *testing.T) {
	srv := v1.New(v1.Options{})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/assets/garbage-but-not-any-format")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("content-type = %q, want problem+json", ct)
	}
}

func TestAssetGet_notFound(t *testing.T) {
	reader := &stubAssetReader{byID: map[string]v1.AssetDetail{}}
	srv := v1.New(v1.Options{Assets: reader})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/assets/native")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	body, _ := readAll(resp)
	if !strings.Contains(body, "asset-not-found") {
		t.Errorf("body missing error type: %s", body)
	}
}

func TestAssetGet_readerPopulatesSep1Status(t *testing.T) {
	// When the reader returns a detail, we use its fields verbatim
	// (vs canonical-echo which fills defaults).
	issuer := testUSDCIssuer
	domain := "circle.com"
	reader := &stubAssetReader{
		byID: map[string]v1.AssetDetail{
			"USDC-" + testUSDCIssuer: {
				AssetID:    "USDC-" + testUSDCIssuer,
				Type:       "classic",
				Code:       "USDC",
				Issuer:     &issuer,
				HomeDomain: &domain,
				Decimals:   7,
				Sep1Status: "verified",
			},
		},
	}
	srv := v1.New(v1.Options{Assets: reader})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/assets/USDC-"+testUSDCIssuer)
	var env struct {
		Data v1.AssetDetail `json:"data"`
	}
	mustDecode(t, resp, &env)
	if env.Data.Sep1Status != "verified" || env.Data.HomeDomain == nil {
		t.Fatalf("reader fields lost: %+v", env.Data)
	}
}

// TestAssetMetadata_ReturnsOnlyOverlayFields checks the
// /v1/assets/{id}/metadata endpoint returns the SEP-1 slice
// without the canonical core (Code, Decimals, Issuer / ContractID).
// Same overlay path as /v1/assets/{id}; status field carries the
// resolution outcome.
func TestAssetMetadata_ReturnsOnlyOverlayFields(t *testing.T) {
	issuer := testUSDCIssuer
	domain := "circle.com"
	reader := &stubAssetReader{
		byID: map[string]v1.AssetDetail{
			"USDC-" + testUSDCIssuer: {
				AssetID:    "USDC-" + testUSDCIssuer,
				Type:       "classic",
				Code:       "USDC",
				Issuer:     &issuer,
				HomeDomain: &domain,
				Decimals:   7,
				Sep1Status: "verified",
				// Pre-populate name/desc to simulate the post-overlay state.
				Name:        ptr("USD Coin"),
				Description: ptr("Centre-issued USDC stablecoin"),
			},
		},
	}
	srv := v1.New(v1.Options{Assets: reader})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/assets/USDC-"+testUSDCIssuer+"/metadata")
	var env struct {
		Data v1.AssetMetadata `json:"data"`
	}
	mustDecode(t, resp, &env)

	if env.Data.AssetID != "USDC-"+testUSDCIssuer {
		t.Errorf("asset_id = %q, want USDC-%s", env.Data.AssetID, testUSDCIssuer)
	}
	if env.Data.Sep1Status != "verified" {
		t.Errorf("sep1_status = %q, want verified", env.Data.Sep1Status)
	}
	if env.Data.HomeDomain == nil || *env.Data.HomeDomain != "circle.com" {
		t.Errorf("home_domain mismatch: %+v", env.Data.HomeDomain)
	}
	if env.Data.Name == nil || *env.Data.Name != "USD Coin" {
		t.Errorf("name not populated: %+v", env.Data.Name)
	}
	if env.Data.Description == nil || *env.Data.Description != "Centre-issued USDC stablecoin" {
		t.Errorf("description not populated: %+v", env.Data.Description)
	}
}

// TestAssetMetadata_ProjectsSEP1IssuanceFields confirms the four
// SEP-1 issuance declarations (conditions / fixed_number / max_number
// / is_unlimited) round-trip from AssetDetail through the metadata
// projection. Pre-overlay state — pretends `applySep1Overlay` has
// already populated AssetDetail; here we just exercise the
// projection.
func TestAssetMetadata_ProjectsSEP1IssuanceFields(t *testing.T) {
	issuer := testUSDCIssuer
	domain := "circle.com"
	yes := false // issuer asserts a bounded supply
	reader := &stubAssetReader{
		byID: map[string]v1.AssetDetail{
			"USDC-" + testUSDCIssuer: {
				AssetID:     "USDC-" + testUSDCIssuer,
				Type:        "classic",
				Code:        "USDC",
				Issuer:      &issuer,
				HomeDomain:  &domain,
				Decimals:    7,
				Sep1Status:  "verified",
				Conditions:  ptr("Issuer terms of service: https://centre.io/terms"),
				FixedNumber: ptr("100000000000000"),
				MaxNumber:   ptr("100000000000000"),
				IsUnlimited: &yes,
			},
		},
	}
	srv := v1.New(v1.Options{Assets: reader})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/assets/USDC-"+testUSDCIssuer+"/metadata")
	var env struct {
		Data v1.AssetMetadata `json:"data"`
	}
	mustDecode(t, resp, &env)

	if env.Data.Conditions == nil || *env.Data.Conditions == "" {
		t.Errorf("conditions not projected: %+v", env.Data.Conditions)
	}
	if env.Data.FixedNumber == nil || *env.Data.FixedNumber != "100000000000000" {
		t.Errorf("fixed_number not projected: %+v", env.Data.FixedNumber)
	}
	if env.Data.MaxNumber == nil || *env.Data.MaxNumber != "100000000000000" {
		t.Errorf("max_number not projected: %+v", env.Data.MaxNumber)
	}
	if env.Data.IsUnlimited == nil {
		t.Errorf("is_unlimited not projected (nil)")
	} else if *env.Data.IsUnlimited != false {
		t.Errorf("is_unlimited = %v, want false", *env.Data.IsUnlimited)
	}
}

// TestAssetMetadata_NotFoundOn404 confirms that an unknown asset
// surfaces as 404 even on the metadata endpoint — same shape as
// /v1/assets/{id}, not a 200-with-empty-overlay.
func TestAssetMetadata_NotFoundOn404(t *testing.T) {
	reader := &stubAssetReader{byID: map[string]v1.AssetDetail{}} // empty
	srv := v1.New(v1.Options{Assets: reader})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/assets/USDC-"+testUSDCIssuer+"/metadata")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for unknown asset", resp.StatusCode)
	}
}

func ptr[T any](v T) *T { return &v }

// ─── helpers ──────────────────────────────────────────────────────

func httpTestServer(t *testing.T, srv *v1.Server) *testServer {
	t.Helper()
	ts := newTestServerFromHandler(t, srv.Handler())
	return ts
}

// Tiny wrapper around httptest.NewServer for readable test code.
type testServer = testServerImpl

func newTestServerFromHandler(t *testing.T, h http.Handler) *testServerImpl {
	t.Helper()
	return startHTTPTest(t, h)
}

func mustGet(t *testing.T, url string) *http.Response {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func mustDecode(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode: %v", err)
	}
}

// ─── 500 error paths ─────────────────────────────────────────

func TestAssetList_ReaderError500(t *testing.T) {
	reader := &stubAssetReader{err: errors.New("storage broke")}
	srv := v1.New(v1.Options{Assets: reader})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/assets")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
}

func TestAssetGet_ReaderError500(t *testing.T) {
	// Reader returning a non-NotFound error → 500 with the
	// internal error-type URL. Previously the only reader-returning
	// test path returned ErrAssetNotFound.
	reader := &stubAssetReader{err: errors.New("storage broke")}
	srv := v1.New(v1.Options{Assets: reader})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/assets/native")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
}
