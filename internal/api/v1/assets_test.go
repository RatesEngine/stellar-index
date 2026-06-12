package v1_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	v1 "github.com/StellarAtlas/stellar-atlas/internal/api/v1"
	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
	"github.com/StellarAtlas/stellar-atlas/internal/storage/timescale"
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

// TestAssetGet_BackfillsHomeDomainFromKnownIssuersMap exercises R-016.
// Prod live snapshot:
//
//	GET /v1/assets/USDC-G… → home_domain=null, sep1_status=not_applicable
//	GET /v1/issuers/G…     → home_domain="centre.io"
//
// Two surfaces disagreed on whether SEP-1 metadata existed for the
// same issuer. Root cause: the storage row for the asset doesn't
// carry a home_domain (the watched-set sep1-refresh worker
// populates it asynchronously and may not have run yet on a fresh
// deployment), so the SEP-1 overlay step short-circuited to
// "not_applicable". /v1/issuers, by contrast, runs every row
// through `enrichIssuer` which has a hand-curated fallback. The
// fix mirrors that policy on /v1/assets.
func TestAssetGet_BackfillsHomeDomainFromKnownIssuersMap(t *testing.T) {
	issuer := testUSDCIssuer
	reader := &stubAssetReader{
		byID: map[string]v1.AssetDetail{
			"USDC-" + testUSDCIssuer: {
				AssetID:    "USDC-" + testUSDCIssuer,
				Type:       "classic",
				Code:       "USDC",
				Issuer:     &issuer,
				HomeDomain: nil, // storage row didn't carry one
				Decimals:   7,
				// Sep1Status intentionally empty — we want the handler
				// path to compute it AFTER the known-issuers backfill.
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

	if env.Data.HomeDomain == nil || *env.Data.HomeDomain != "centre.io" {
		t.Errorf("HomeDomain = %v, want centre.io (from known_issuers map)", env.Data.HomeDomain)
	}
	// With no metadata resolver wired the status should advance from
	// the empty default to "not_fetched" — distinct from the
	// pre-fix "not_applicable" which incorrectly claimed the issuer
	// has no home-domain at all.
	if env.Data.Sep1Status != "not_fetched" {
		t.Errorf("Sep1Status = %q, want not_fetched (resolver not wired but home_domain known)", env.Data.Sep1Status)
	}
}

// TestAssetMetadata_BackfillsHomeDomainFromKnownIssuersMap is the
// /v1/assets/{id}/metadata variant of the same fix — the two
// surfaces share the same backfill so consumers see identical
// SEP-1 status across them.
func TestAssetMetadata_BackfillsHomeDomainFromKnownIssuersMap(t *testing.T) {
	issuer := testUSDCIssuer
	reader := &stubAssetReader{
		byID: map[string]v1.AssetDetail{
			"USDC-" + testUSDCIssuer: {
				AssetID: "USDC-" + testUSDCIssuer, Type: "classic", Code: "USDC",
				Issuer: &issuer, HomeDomain: nil, Decimals: 7,
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

	if env.Data.HomeDomain == nil || *env.Data.HomeDomain != "centre.io" {
		t.Errorf("HomeDomain = %v, want centre.io", env.Data.HomeDomain)
	}
	if env.Data.Sep1Status != "not_fetched" {
		t.Errorf("Sep1Status = %q, want not_fetched", env.Data.Sep1Status)
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

func TestAssetList_NetworkStellar_DefaultPath(t *testing.T) {
	// network=stellar should hit the existing reader path.
	xlm := v1.AssetDetail{AssetID: "native", Type: "native", Code: "XLM"}
	reader := &stubAssetReader{page: []v1.AssetDetail{xlm}, nextCur: ""}
	srv := v1.New(v1.Options{Assets: reader, VerifiedCurrencies: newTestCatalogue(t)})
	ts := httpTestServer(t, srv)
	resp := mustGet(t, ts.URL+"/v1/assets?network=stellar&limit=10")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var env struct {
		Data []v1.AssetDetail `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(env.Data) != 1 || env.Data[0].AssetID != "native" {
		t.Errorf("expected xlm row, got %+v", env.Data)
	}
}

func TestAssetList_NetworkEthereum_ProjectsCatalogue(t *testing.T) {
	// network=ethereum returns the verified-currency catalogue entries
	// that have an ethereum networks[] row. The seed catalogue has
	// USDC and several others on ethereum.
	srv := v1.New(v1.Options{VerifiedCurrencies: newTestCatalogue(t)})
	ts := httpTestServer(t, srv)
	resp := mustGet(t, ts.URL+"/v1/assets?network=ethereum&limit=500")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var env struct {
		Data []v1.AssetDetail `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(env.Data) == 0 {
		t.Fatal("expected ethereum rows from catalogue")
	}
	for _, d := range env.Data {
		if d.Type != "external" {
			t.Errorf("row %s type=%q want external", d.AssetID, d.Type)
		}
		if !strings.HasPrefix(d.AssetID, "ethereum:") {
			t.Errorf("row %s asset_id should start with 'ethereum:'", d.AssetID)
		}
	}
}

func TestAssetList_NetworkUnknown_400(t *testing.T) {
	srv := v1.New(v1.Options{VerifiedCurrencies: newTestCatalogue(t)})
	ts := httpTestServer(t, srv)
	resp := mustGet(t, ts.URL+"/v1/assets?network=potato")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d want 400", resp.StatusCode)
	}
}

func TestAssetList_FromCoinsReader_IncludesPrice(t *testing.T) {
	// When a CoinsReader is wired, the listing endpoint sources from
	// ListCoinsExt and projects each CoinRow into an AssetDetail
	// with the coin-overlay fields populated.
	price := "1.0008"
	vol := "1131827.32"
	coinRow := timescale.CoinRow{
		Slug:             "USDC",
		AssetID:          "USDC-" + testUSDCIssuer,
		Code:             "USDC",
		IssuerGStrkey:    testUSDCIssuer,
		ObservationCount: 41610630,
		FirstSeenLedger:  50457424,
		LastSeenLedger:   62523839,
		PriceUSD:         &price,
		Volume24hUSD:     &vol,
	}
	coins := &stubCoinsReaderExt{}
	// stubCoinsReaderExt.ListCoinsExt returns nil — override by
	// constructing a custom struct inline.
	listReader := &listingStub{rows: []timescale.CoinRow{coinRow}}
	srv := v1.New(v1.Options{Coins: listReader, Assets: &stubAssetReader{}})
	_ = coins
	ts := httpTestServer(t, srv)
	resp := mustGet(t, ts.URL+"/v1/assets?limit=10")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var env struct {
		Data []v1.AssetDetail `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(env.Data) != 1 {
		t.Fatalf("got %d rows, want 1", len(env.Data))
	}
	d := env.Data[0]
	if d.Slug != "USDC" {
		t.Errorf("slug=%q want USDC", d.Slug)
	}
	if d.PriceUSD == nil || *d.PriceUSD != "1.0008" {
		t.Errorf("price_usd=%v want 1.0008", d.PriceUSD)
	}
	if d.VolumeUSD24h == nil || *d.VolumeUSD24h != "1131827.32" {
		t.Errorf("volume_24h_usd=%v", d.VolumeUSD24h)
	}
	if d.ObservationCount == nil || *d.ObservationCount != 41610630 {
		t.Errorf("observation_count=%v", d.ObservationCount)
	}
}

func TestAssetList_FromCoinsReader_IssuerFilter(t *testing.T) {
	// ?issuer=G should pass through to the CoinsReader's Issuer
	// option. Stub records what was passed.
	listReader := &listingStub{}
	srv := v1.New(v1.Options{Coins: listReader, Assets: &stubAssetReader{}})
	ts := httpTestServer(t, srv)
	mustGet(t, ts.URL+"/v1/assets?issuer="+testUSDCIssuer)
	if listReader.lastOpts.Issuer != testUSDCIssuer {
		t.Errorf("ListCoinsExt called with Issuer=%q, want %q", listReader.lastOpts.Issuer, testUSDCIssuer)
	}
}

// listingStub is a tiny CoinsReader implementation tailored to the
// listing-endpoint tests. Each method returns the configured value;
// recording the most-recent ListCoinsExt opts lets tests assert
// what filter the handler passed through.
type listingStub struct {
	rows     []timescale.CoinRow
	lastOpts timescale.ListCoinsOptions
}

func (s *listingStub) ListCoinsExt(_ context.Context, opts timescale.ListCoinsOptions) ([]timescale.CoinRow, error) {
	s.lastOpts = opts
	return s.rows, nil
}

func (s *listingStub) GetCoinBySlug(_ context.Context, _ string) (timescale.CoinRow, error) {
	return timescale.CoinRow{}, nil
}

func (s *listingStub) GetCoinByAssetID(_ context.Context, _ string) (timescale.CoinRow, error) {
	return timescale.CoinRow{}, nil
}

func (s *listingStub) GetNativeCoinRow(_ context.Context) (timescale.CoinRow, error) {
	return timescale.CoinRow{}, nil
}

func (s *listingStub) GetCoinTopMarkets(_ context.Context, _ string, _ int) ([]timescale.CoinTopMarket, error) {
	return nil, nil
}

func (s *listingStub) GetCoinPriceHistory24h(_ context.Context, _ string) ([]timescale.CoinPricePoint, error) {
	return nil, nil
}

func (s *listingStub) GetCoinPriceHistory7d(_ context.Context, _ string) ([]timescale.CoinPricePoint, error) {
	return nil, nil
}

func (s *listingStub) GetCoinsPriceHistory24hBatch(_ context.Context, _ []string) (map[string][]timescale.CoinPricePoint, error) {
	return nil, nil
}

func (s *listingStub) GetCoinsPriceHistory7dBatch(_ context.Context, _ []string) (map[string][]timescale.CoinPricePoint, error) {
	return nil, nil
}

func (s *listingStub) GetCoinMarketsCount(_ context.Context, _ string) (int64, error) {
	return 0, nil
}

func (s *listingStub) GetCoinATH(_ context.Context, _ string) (*timescale.CoinATH, error) {
	return nil, nil
}

func (s *listingStub) GetCoinsATHBatch(_ context.Context, _ []string) (map[string]timescale.CoinATH, error) {
	return nil, nil
}

func (s *listingStub) GetCoinTradeCount24h(_ context.Context, _ string) (int64, error) {
	return 0, nil
}
