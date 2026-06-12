package v1

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/StellarAtlas/stellar-atlas/internal/storage/timescale"
)

// stubSEP41TransfersReader is a no-op reader that lets us exercise
// the handler's request-side validation without a real database.
// If a validation case fails to short-circuit and reaches the
// reader, the test will see calledN > 0 and complain.
type stubSEP41TransfersReader struct {
	calledN int
}

func (r *stubSEP41TransfersReader) ListSEP41Transfers(
	_ context.Context, _, _, _ string, _ int,
) ([]timescale.SEP41TransferRow, error) {
	r.calledN++
	return nil, nil
}

func serverWithSEP41Reader(reader SEP41TransfersReader) *Server {
	s := &Server{}
	s.sep41Transfers = reader
	return s
}

// Valid strkeys for testing — both real-looking. The contract is
// an actual mainnet contract (DeFindex USDC vault); the account is
// from the asset_registry test fixtures.
const (
	validContractID = "CDB2WMKQQNVZMEBY7Q7GZ5C7E7IAFSNMZ7GGVD6WKTCEWK7XOIAVZSAP"
	validAccountG   = "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"
)

func runSEP41Request(t *testing.T, contractPath, query string) *httptest.ResponseRecorder {
	t.Helper()
	reader := &stubSEP41TransfersReader{}
	srv := serverWithSEP41Reader(reader)

	// Use Go 1.22's path-value mux so PathValue("contract_id")
	// resolves the same way it does in production.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/contracts/{contract_id}/transfers", srv.handleSEP41Transfers)

	url := "/v1/contracts/" + contractPath + "/transfers"
	if query != "" {
		url += "?" + query
	}
	req := httptest.NewRequest(http.MethodGet, url, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestSEP41Transfers_InvalidContractID_Returns400(t *testing.T) {
	cases := []struct {
		name    string
		path    string
		wantSub string
	}{
		{"plain garbage", "FOO", "must be a 56-char C-strkey"},
		{"G-strkey instead of C-", validAccountG, "must be a 56-char C-strkey"},
		{"correct length but bad CRC", "CDB2WMKQQNVZMEBY7Q7GZ5C7E7IAFSNMZ7GGVD6WKTCEWK7XOIAVZSAQ", "must be a 56-char C-strkey"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := runSEP41Request(t, tc.path, "")
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.wantSub) {
				t.Errorf("body missing %q; got: %s", tc.wantSub, rec.Body.String())
			}
		})
	}
}

func TestSEP41Transfers_InvalidFromOrTo_Returns400(t *testing.T) {
	// Post-CAP-67 the SEP-41 from/to accepts every valid holder strkey
	// (G/C/M/B/L). The 400-rejected set is now narrower: only
	// genuinely-invalid strings are bad input; a C-strkey is a
	// legitimate Soroban-contract destination per the rc.88 SEP-41
	// fix bundle.
	cases := []struct {
		name string
		q    string
		want string
	}{
		{"from garbage", "from=NOTAGSTRKEY", "from must be a valid Stellar address"},
		{"to garbage", "to=NOTAGSTRKEY", "to must be a valid Stellar address"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := runSEP41Request(t, validContractID, tc.q)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.want) {
				t.Errorf("body missing %q; got: %s", tc.want, rec.Body.String())
			}
		})
	}
}

// TestSEP41Transfers_AllHolderTypesAccepted_ReachReader ensures
// the post-CAP-67 widening (G/C/M/B/L) reaches the reader rather
// than 400-ing at the validation boundary. Each valid strkey gets
// a query that the stub reader returns empty for; what matters is
// that we *get* to the reader.
func TestSEP41Transfers_AllHolderTypesAccepted_ReachReader(t *testing.T) {
	// One representative of each post-CAP-67 holder type — the
	// canonical.Is*-test fixtures pin the underlying strkey
	// validators; this test pins the handler-side acceptance.
	cases := []struct {
		name string
		addr string
	}{
		{"G account", validAccountG},
		{"C contract", validContractID},
		// M/B/L: SDK strkey-decode-tested fixtures so we know the CRC
		// is right. Compact base32 forms taken from the SDK's
		// strkey/decode_test.go.
		{"M muxed", "MA7QYNF7SOWQ3GLR2BGMZEHXAVIRZA4KVWLTJJFC7MGXUA74P7UJVAAAAAAAAAAAAAJLK"},
		{"L liquidity pool", "LA7QYNF7SOWQ3GLR2BGMZEHXAVIRZA4KVWLTJJFC7MGXUA74P7UJUPJN"},
		{"B claimable balance", "BAAD6DBUX6J22DMZOHIEZTEQ64CVCHEDRKWZONFEUL5Q26QD7R76RGR4TU"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reader := &stubSEP41TransfersReader{}
			srv := serverWithSEP41Reader(reader)
			mux := http.NewServeMux()
			mux.HandleFunc("GET /v1/contracts/{contract_id}/transfers", srv.handleSEP41Transfers)
			url := "/v1/contracts/" + validContractID + "/transfers?from=" + tc.addr + "&limit=10"
			req := httptest.NewRequest(http.MethodGet, url, nil)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Errorf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
			}
			if reader.calledN != 1 {
				t.Errorf("reader called %d times, want 1", reader.calledN)
			}
		})
	}
}

func TestSEP41Transfers_ValidInputs_ReachReader(t *testing.T) {
	// Sanity: known-good inputs must NOT short-circuit at the
	// validation layer — they reach the reader, which (in the test
	// stub) returns an empty list. Verifies the validation isn't
	// over-strict.
	reader := &stubSEP41TransfersReader{}
	srv := serverWithSEP41Reader(reader)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/contracts/{contract_id}/transfers", srv.handleSEP41Transfers)

	url := "/v1/contracts/" + validContractID + "/transfers?from=" + validAccountG + "&to=" + validAccountG + "&limit=10"
	req := httptest.NewRequest(http.MethodGet, url, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if reader.calledN != 1 {
		t.Errorf("reader called %d times, want 1 (validation should not short-circuit valid input)", reader.calledN)
	}
}
