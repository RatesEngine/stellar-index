package v1_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	v1 "github.com/RatesEngine/rates-engine/internal/api/v1"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// stubIssuersReader is the in-memory test seam.
type stubIssuersReader struct {
	row     timescale.IssuerRow
	rowErr  error
	assets  []timescale.IssuerAsset
	assetsE error
	list    []timescale.IssuerSummary
	listErr error

	lastGStrkey string
	lastLimit   int
}

func (r *stubIssuersReader) GetIssuer(_ context.Context, gStrkey string) (timescale.IssuerRow, error) {
	r.lastGStrkey = gStrkey
	if r.rowErr != nil {
		return timescale.IssuerRow{}, r.rowErr
	}
	return r.row, nil
}

func (r *stubIssuersReader) ListIssuerAssets(_ context.Context, gStrkey string) ([]timescale.IssuerAsset, error) {
	if r.assetsE != nil {
		return nil, r.assetsE
	}
	return r.assets, nil
}

func (r *stubIssuersReader) ListIssuers(_ context.Context, limit int) ([]timescale.IssuerSummary, error) {
	r.lastLimit = limit
	if r.listErr != nil {
		return nil, r.listErr
	}
	return r.list, nil
}

// ─── /v1/issuers (list) ───────────────────────────────────────────

// TestHandleIssuersList_503WhenReaderNil — feature-gated reader.
func TestHandleIssuersList_503WhenReaderNil(t *testing.T) {
	srv := v1.New(v1.Options{})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/issuers")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

// TestHandleIssuersList_DefaultLimit — no ?limit forwards as 100.
func TestHandleIssuersList_DefaultLimit(t *testing.T) {
	reader := &stubIssuersReader{}
	srv := v1.New(v1.Options{Issuers: reader})
	ts := startHTTPTest(t, srv.Handler())

	_ = mustGet(t, ts.URL+"/v1/issuers")
	if reader.lastLimit != 100 {
		t.Errorf("default limit = %d, want 100", reader.lastLimit)
	}
}

// TestHandleIssuersList_InvalidLimit400 — out-of-range / non-numeric
// values return 400 with the `invalid-limit` problem type. Mirrors
// the same guard pattern shipped on /v1/coins, /v1/markets, etc.
func TestHandleIssuersList_InvalidLimit400(t *testing.T) {
	srv := v1.New(v1.Options{Issuers: &stubIssuersReader{}})
	ts := startHTTPTest(t, srv.Handler())

	for _, bad := range []string{"0", "501", "-1", "xyz", "999999"} {
		resp := mustGet(t, ts.URL+"/v1/issuers?limit="+bad)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("limit=%q → %d, want 400", bad, resp.StatusCode)
		}
	}
}

// TestHandleIssuersList_HappyPath pins the wire shape — every field
// the explorer's /issuers table reads. ScamReason populates from
// the curated known_scams.go map; not asserted directly here
// (depends on whether the test G-strkey is in the map).
func TestHandleIssuersList_HappyPath(t *testing.T) {
	reader := &stubIssuersReader{
		list: []timescale.IssuerSummary{
			{
				GStrkey:               "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN",
				HomeDomain:            "centre.io",
				OrgName:               "Circle",
				AssetCount:            1,
				TotalObservationCount: 41610623,
			},
			{
				GStrkey:               "GBNZILSTVQZ4R7IKQDGHYGY2QXL5QOFJYQMXPKWRRM5PAV7Y4M67AQUA",
				HomeDomain:            "aqua.network",
				OrgName:               "Aquarius",
				AssetCount:            1,
				TotalObservationCount: 14764050,
			},
		},
	}
	srv := v1.New(v1.Options{Issuers: reader})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/issuers?limit=10")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if reader.lastLimit != 10 {
		t.Errorf("forwarded limit = %d, want 10", reader.lastLimit)
	}
	var env struct {
		Data []v1.IssuerListEntry `json:"data"`
	}
	body, _ := readAll(resp)
	if err := json.NewDecoder(strings.NewReader(body)).Decode(&env); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, body)
	}
	if len(env.Data) != 2 {
		t.Fatalf("len = %d, want 2", len(env.Data))
	}
	first := env.Data[0]
	if first.GStrkey != "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN" {
		t.Errorf("g_strkey drift: %q", first.GStrkey)
	}
	if first.OrgName != "Circle" || first.HomeDomain != "centre.io" {
		t.Errorf("first row metadata = %+v", first)
	}
	if first.AssetCount != 1 || first.TotalObservationCount != 41610623 {
		t.Errorf("counts = (%d, %d)", first.AssetCount, first.TotalObservationCount)
	}
}

// TestHandleIssuersList_TimeoutReturns503 — the 8s deadline fires
// when the issuer registry scan takes too long. Returns 503 with
// `issuers-timeout` problem type.
func TestHandleIssuersList_TimeoutReturns503(t *testing.T) {
	reader := &stubIssuersReader{listErr: context.DeadlineExceeded}
	srv := v1.New(v1.Options{Issuers: reader})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/issuers")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
	body, _ := readAll(resp)
	if !strings.Contains(body, "issuers-timeout") {
		t.Errorf("expected `issuers-timeout` problem type in body, got: %s", body)
	}
}

// TestHandleIssuersList_ReaderError500 — generic storage error.
func TestHandleIssuersList_ReaderError500(t *testing.T) {
	reader := &stubIssuersReader{listErr: errors.New("storage broke")}
	srv := v1.New(v1.Options{Issuers: reader})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/issuers")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
}

// ─── /v1/issuers/{g_strkey} (detail) ──────────────────────────────

// TestHandleIssuer_503WhenReaderNil — same gate as the listing.
func TestHandleIssuer_503WhenReaderNil(t *testing.T) {
	srv := v1.New(v1.Options{})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/issuers/GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

// TestHandleIssuer_NotFound404 — sql.ErrNoRows surfaces as 404
// with `issuer-not-found` problem type. Pre-fix this would 500
// because the handler didn't distinguish ErrNoRows from generic
// storage failures.
func TestHandleIssuer_NotFound404(t *testing.T) {
	reader := &stubIssuersReader{rowErr: sql.ErrNoRows}
	srv := v1.New(v1.Options{Issuers: reader})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/issuers/GFAKEUNKNOWNUNKNOWNUNKNOWNUNKNOWNUNKNOWNUNKNOWNUNKNOWNUNKNOWN")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
	body, _ := readAll(resp)
	if !strings.Contains(body, "issuer-not-found") {
		t.Errorf("expected `issuer-not-found` problem type, got: %s", body)
	}
}

// TestHandleIssuer_LowercaseInputUppercased — the handler upper-
// cases the path segment before hitting storage so URL clients
// that auto-lowercase don't dead-end. Pre-fix
// `/v1/issuers/ga5zsejyb...` 404'd while the uppercase form
// returned the row. Verified by checking what the storage stub
// received.
func TestHandleIssuer_LowercaseInputUppercased(t *testing.T) {
	reader := &stubIssuersReader{
		row: timescale.IssuerRow{
			GStrkey: "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN",
		},
	}
	srv := v1.New(v1.Options{Issuers: reader})
	ts := startHTTPTest(t, srv.Handler())

	_ = mustGet(t, ts.URL+"/v1/issuers/ga5zsejyb37jrc5avcia5mop4rhtm335x2kgx3ihojapp5re34k4kzvn")
	if reader.lastGStrkey != "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN" {
		t.Errorf("storage saw %q, want uppercased form", reader.lastGStrkey)
	}
}

// TestHandleIssuer_HappyPath_WithAssets — full row decode, plus
// the per-issuer assets list flowing through.
func TestHandleIssuer_HappyPath_WithAssets(t *testing.T) {
	reader := &stubIssuersReader{
		row: timescale.IssuerRow{
			GStrkey:    "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN",
			HomeDomain: "centre.io",
			OrgName:    "Circle",
		},
		assets: []timescale.IssuerAsset{
			{
				AssetID:          "USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN",
				Code:             "USDC",
				Slug:             "USDC",
				FirstSeenLedger:  10000000,
				LastSeenLedger:   62500000,
				ObservationCount: 41610623,
			},
		},
	}
	srv := v1.New(v1.Options{Issuers: reader})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/issuers/GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var env struct {
		Data v1.Issuer `json:"data"`
	}
	body, _ := readAll(resp)
	if err := json.NewDecoder(strings.NewReader(body)).Decode(&env); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, body)
	}
	if env.Data.OrgName != "Circle" {
		t.Errorf("OrgName = %q", env.Data.OrgName)
	}
	if len(env.Data.Assets) != 1 {
		t.Fatalf("len(assets) = %d, want 1", len(env.Data.Assets))
	}
	a := env.Data.Assets[0]
	if a.Code != "USDC" || a.ObservationCount != 41610623 {
		t.Errorf("asset = %+v", a)
	}
}

// TestHandleIssuer_AssetsSoftFail — when ListIssuerAssets fails,
// the issuer card still renders without it. The handler logs at
// WARN and proceeds with assets = nil. Critical for explorer UX:
// a failure to load the per-asset list shouldn't 500 the whole
// issuer detail page.
func TestHandleIssuer_AssetsSoftFail(t *testing.T) {
	reader := &stubIssuersReader{
		row: timescale.IssuerRow{
			GStrkey:    "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN",
			HomeDomain: "centre.io",
		},
		assetsE: errors.New("assets fetch broke"),
	}
	srv := v1.New(v1.Options{Issuers: reader})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/issuers/GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 (asset-list failure should soft-fail)", resp.StatusCode)
	}
	body, _ := readAll(resp)
	if !strings.Contains(body, `"home_domain":"centre.io"`) {
		t.Errorf("issuer body missing despite soft-fail path: %s", body)
	}
}
