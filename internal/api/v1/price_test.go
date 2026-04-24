package v1_test

import (
	"context"
	"errors"
	"math/big"
	"net/http"
	"strings"
	"testing"
	"time"

	v1 "github.com/RatesEngine/rates-engine/internal/api/v1"
	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// stubPriceReader implements v1.PriceReader.
type stubPriceReader struct {
	// Lookup keyed on "<base>/<quote>".
	snapshots map[string]v1.PriceSnapshot
	stale     map[string]bool
	sources   map[string][]string
	err       error
}

func (r *stubPriceReader) LatestPrice(_ context.Context, a, q canonical.Asset) (v1.PriceSnapshot, []string, bool, error) {
	if r.err != nil {
		return v1.PriceSnapshot{}, nil, false, r.err
	}
	key := a.String() + "/" + q.String()
	snap, ok := r.snapshots[key]
	if !ok {
		return v1.PriceSnapshot{}, nil, false, v1.ErrPriceNotFound
	}
	return snap, r.sources[key], r.stale[key], nil
}

func TestPrice_NoReader_Returns503(t *testing.T) {
	srv := v1.New(v1.Options{})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price?asset=native&quote=fiat:USD")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
	body, _ := readAll(resp)
	if !strings.Contains(body, "price-unavailable") {
		t.Errorf("error type missing: %s", body)
	}
}

func TestPrice_MissingAssetParam(t *testing.T) {
	srv := v1.New(v1.Options{Prices: &stubPriceReader{}})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestPrice_InvalidAssetReturns400(t *testing.T) {
	srv := v1.New(v1.Options{Prices: &stubPriceReader{}})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price?asset=garbage-format")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestPrice_IdentityPairReturns400(t *testing.T) {
	// XLM / XLM is always 1 — reject as a bad request.
	srv := v1.New(v1.Options{Prices: &stubPriceReader{}})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price?asset=native&quote=native")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	body, _ := readAll(resp)
	if !strings.Contains(body, "identity-price") {
		t.Errorf("error type missing: %s", body)
	}
}

func TestPrice_HappyPath(t *testing.T) {
	snap := v1.PriceSnapshot{
		AssetID:    "native",
		Quote:      "fiat:USD",
		Price:      "0.1242",
		PriceType:  "last_trade",
		ObservedAt: time.Unix(1745000000, 0).UTC(),
	}
	reader := &stubPriceReader{
		snapshots: map[string]v1.PriceSnapshot{"native/fiat:USD": snap},
		sources:   map[string][]string{"native/fiat:USD": {"sdex"}},
	}
	srv := v1.New(v1.Options{Prices: reader})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price?asset=native&quote=fiat:USD")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body, _ := readAll(resp)
	// Envelope shape: {"data":{"price":"0.1242",...},"flags":{"stale":false,...},"sources":["sdex"]}
	for _, s := range []string{
		`"price":"0.1242"`,
		`"price_type":"last_trade"`,
		`"stale":false`,
		`"sources":["sdex"]`,
	} {
		if !strings.Contains(body, s) {
			t.Errorf("body missing %q: %s", s, body)
		}
	}
}

func TestPrice_StaleFlagSet(t *testing.T) {
	reader := &stubPriceReader{
		snapshots: map[string]v1.PriceSnapshot{
			"native/fiat:USD": {Price: "0.1242", PriceType: "last_trade"},
		},
		stale: map[string]bool{"native/fiat:USD": true},
	}
	srv := v1.New(v1.Options{Prices: reader})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price?asset=native&quote=fiat:USD")
	body, _ := readAll(resp)
	if !strings.Contains(body, `"stale":true`) {
		t.Errorf("stale flag not set: %s", body)
	}
}

func TestPrice_DefaultQuoteIsUSD(t *testing.T) {
	// Omit quote param — handler defaults to fiat:USD.
	reader := &stubPriceReader{
		snapshots: map[string]v1.PriceSnapshot{
			"native/fiat:USD": {Price: "0.12"},
		},
	}
	srv := v1.New(v1.Options{Prices: reader})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price?asset=native")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

func TestPrice_NotFoundReturns404(t *testing.T) {
	srv := v1.New(v1.Options{Prices: &stubPriceReader{err: v1.ErrPriceNotFound}})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price?asset=native&quote=fiat:USD")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestPrice_InternalErrorReturns500(t *testing.T) {
	srv := v1.New(v1.Options{Prices: &stubPriceReader{err: errors.New("db timeout")}})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price?asset=native&quote=fiat:USD")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
	// Body must NOT leak the underlying error message.
	body, _ := readAll(resp)
	if strings.Contains(body, "db timeout") {
		t.Errorf("internal error leaked to client: %s", body)
	}
}

// ─── LastTradeToSnapshot ─────────────────────────────────────────

func TestLastTradeToSnapshot(t *testing.T) {
	usdc, _ := canonical.NewClassicAsset("USDC", testUSDCIssuer)
	pair, _ := canonical.NewPair(canonical.NativeAsset(), usdc)

	// 100 XLM @ 12.42 USDC = 1e9 base stroops, 12_420_000 quote stroops.
	// Ratio = 12_420_000 / 1_000_000_000 = 0.01242 in stroop-units.
	// At decimals=7 we get a str with 7 fractional digits.
	tr := canonical.Trade{
		Source:      "sdex",
		Ledger:      52_430_001,
		TxHash:      "cafebabecafebabecafebabecafebabecafebabecafebabecafebabecafebabe",
		OpIndex:     0,
		Timestamp:   time.Unix(1745000000, 0).UTC(),
		Pair:        pair,
		BaseAmount:  canonical.NewAmount(big.NewInt(1_000_000_000)),
		QuoteAmount: canonical.NewAmount(big.NewInt(12_420_000)),
	}

	snap := v1.LastTradeToSnapshot(tr, 7)
	if snap.AssetID != "native" {
		t.Errorf("asset = %q", snap.AssetID)
	}
	if snap.PriceType != "last_trade" {
		t.Errorf("price_type = %q", snap.PriceType)
	}
	// 12_420_000 / 1_000_000_000 scaled to 7 decimals =
	// (12_420_000 * 10^7) / 1_000_000_000 = 12_420_000 / 100 = 124_200
	// → "0.0124200"
	if snap.Price != "0.0124200" {
		t.Errorf("price = %q, want 0.0124200", snap.Price)
	}
	if snap.ObservedAt != tr.Timestamp {
		t.Errorf("timestamp lost")
	}
}

func TestLastTradeToSnapshot_zeroDecimals(t *testing.T) {
	tr := canonical.Trade{
		Source: "sdex", Ledger: 1, TxHash: "cafebabecafebabecafebabecafebabecafebabecafebabecafebabecafebabe",
		OpIndex: 0, Timestamp: time.Now(),
		Pair:        mustPair(canonical.NativeAsset(), mustClassicTest("USDC", testUSDCIssuer)),
		BaseAmount:  canonical.NewAmount(big.NewInt(1_000)),
		QuoteAmount: canonical.NewAmount(big.NewInt(12_420)),
	}
	snap := v1.LastTradeToSnapshot(tr, 0)
	if snap.Price != "12" { // 12420 / 1000 = 12 with no decimals
		t.Errorf("price = %q, want 12", snap.Price)
	}
}

// helper
func mustPair(base, quote canonical.Asset) canonical.Pair {
	p, err := canonical.NewPair(base, quote)
	if err != nil {
		panic(err)
	}
	return p
}

func mustClassicTest(code, issuer string) canonical.Asset {
	a, err := canonical.NewClassicAsset(code, issuer)
	if err != nil {
		panic(err)
	}
	return a
}
