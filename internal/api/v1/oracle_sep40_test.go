package v1_test

import (
	"errors"
	"net/http"
	"testing"
	"time"

	v1 "github.com/RatesEngine/rates-engine/internal/api/v1"
	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// ─── /v1/oracle/lastprice ──────────────────────────────────────

func TestOracleLastPrice_NoReader_Returns503(t *testing.T) {
	srv := v1.New(v1.Options{})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/oracle/lastprice?asset=native")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

func TestOracleLastPrice_MissingAsset400(t *testing.T) {
	srv := v1.New(v1.Options{Prices: &stubPriceReader{}})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/oracle/lastprice")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestOracleLastPrice_InvalidAsset400(t *testing.T) {
	srv := v1.New(v1.Options{Prices: &stubPriceReader{}})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/oracle/lastprice?asset=garbage")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestOracleLastPrice_QueryingQuoteRejected400(t *testing.T) {
	// Asking for the price of fiat:USD itself is meaningless and
	// rejected the same way /v1/price's identity check works.
	srv := v1.New(v1.Options{Prices: &stubPriceReader{}})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/oracle/lastprice?asset=fiat:USD")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestOracleLastPrice_NotFound404(t *testing.T) {
	reader := &stubPriceReader{snapshots: map[string]v1.PriceSnapshot{}}
	srv := v1.New(v1.Options{Prices: reader})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/oracle/lastprice?asset=native")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

// TestOracleLastPrice_RedisVWAPFallback — when prices_1m has no
// row for native/fiat:USD (typical when XLM trades against USDC
// not direct USD, and the aggregator's stablecoin-proxy rewrite
// lives only in the Redis cache), the SEP-40 lastprice handler
// falls through to the same TriangulatedPriceLooker /v1/price
// uses. Pre-2026-05-08 the SEP-40 path skipped the fallback and
// 404'd in steady state — caught by the prod audit.
func TestOracleLastPrice_RedisVWAPFallback(t *testing.T) {
	reader := &stubPriceReader{err: v1.ErrPriceNotFound}
	looker := &stubTriangulatedPriceLooker{
		value:          "0.155",
		isTriangulated: false, // direct rewrite, no marker
		found:          true,
	}
	srv := v1.New(v1.Options{Prices: reader, Triangulated: looker})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/oracle/lastprice?asset=native")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 via fallback", resp.StatusCode)
	}
	var env struct {
		Data v1.SEP40Price `json:"data"`
	}
	mustDecode(t, resp, &env)
	if env.Data.Price != "0.155" {
		t.Errorf("price = %q, want \"0.155\"", env.Data.Price)
	}
}

// TestOracleLastPrice_StablecoinFiatProxyFallback — when the
// Redis VWAP cache also misses but the operator declared classic
// USD pegs, the SEP-40 lastprice handler walks the pegs and returns
// the rewritten X/<peg> snapshot. Mirrors the /v1/price fallback
// from #1217. Without this, an on-chain integrator drop-in-replacing
// SEP-40 lastprice() against XLM gets 404 even though /v1/coins
// shows $0.16 fine.
func TestOracleLastPrice_StablecoinFiatProxyFallback(t *testing.T) {
	usdcClassic, err := canonical.ParseAsset("USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")
	if err != nil {
		t.Fatalf("parse USDC: %v", err)
	}
	pegSnap := v1.PriceSnapshot{
		AssetID:    "native",
		Quote:      usdcClassic.String(),
		Price:      "0.1626",
		PriceType:  "vwap",
		ObservedAt: time.Unix(1_770_000_000, 0).UTC(),
	}
	reader := &stubPriceReader{
		snapshots: map[string]v1.PriceSnapshot{
			"native/" + usdcClassic.String(): pegSnap,
		},
		sources: map[string][]string{
			"native/" + usdcClassic.String(): {"sdex"},
		},
	}
	srv := v1.New(v1.Options{
		Prices:            reader,
		USDPeggedClassics: []canonical.Asset{usdcClassic},
	})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/oracle/lastprice?asset=native")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (peg fallback should serve)", resp.StatusCode)
	}
	var env struct {
		Data v1.SEP40Price `json:"data"`
	}
	mustDecode(t, resp, &env)
	if env.Data.Price != "0.1626" {
		t.Errorf("price = %q, want \"0.1626\"", env.Data.Price)
	}
	// SEP-40 surface always quotes in fiat:USD; the wire `asset` is
	// just the queried asset, so the fact we proxied through USDC
	// shouldn't leak into the wire response.
	if env.Data.Asset != "native" {
		t.Errorf("asset = %q, want \"native\"", env.Data.Asset)
	}
}

// TestOracleXLastPrice_StablecoinFiatProxyFallback — the
// /v1/oracle/x_last_price?base=native&quote=fiat:USD case mirrors
// the /v1/oracle/lastprice fix above. Without this, an integrator
// asking for XLM/USD via the SEP-40 cross-pair surface gets the
// same out-of-the-box 404.
func TestOracleXLastPrice_StablecoinFiatProxyFallback(t *testing.T) {
	usdcClassic, err := canonical.ParseAsset("USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")
	if err != nil {
		t.Fatalf("parse USDC: %v", err)
	}
	pegSnap := v1.PriceSnapshot{
		AssetID:    "native",
		Quote:      usdcClassic.String(),
		Price:      "0.1626",
		PriceType:  "vwap",
		ObservedAt: time.Unix(1_770_000_000, 0).UTC(),
	}
	reader := &stubPriceReader{
		snapshots: map[string]v1.PriceSnapshot{
			"native/" + usdcClassic.String(): pegSnap,
		},
		sources: map[string][]string{
			"native/" + usdcClassic.String(): {"sdex"},
		},
	}
	srv := v1.New(v1.Options{
		Prices:            reader,
		USDPeggedClassics: []canonical.Asset{usdcClassic},
	})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/oracle/x_last_price?base=native&quote=fiat:USD")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var env struct {
		Data v1.SEP40Price `json:"data"`
	}
	mustDecode(t, resp, &env)
	if env.Data.Price != "0.1626" {
		t.Errorf("price = %q, want \"0.1626\"", env.Data.Price)
	}
}

func TestOracleLastPrice_HappyPath(t *testing.T) {
	t0 := time.Unix(1_770_000_000, 0).UTC()
	reader := &stubPriceReader{
		snapshots: map[string]v1.PriceSnapshot{
			"native/fiat:USD": {
				AssetID: "native", Quote: "fiat:USD",
				Price: "0.12", PriceType: "vwap", ObservedAt: t0,
			},
		},
		sources: map[string][]string{
			"native/fiat:USD": {"soroswap", "phoenix"},
		},
	}
	srv := v1.New(v1.Options{Prices: reader})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/oracle/lastprice?asset=native")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var env struct {
		Data    v1.SEP40Price `json:"data"`
		Sources []string      `json:"sources"`
	}
	mustDecode(t, resp, &env)
	if env.Data.Asset != "native" {
		t.Errorf("asset = %q", env.Data.Asset)
	}
	if env.Data.Price != "0.12" {
		t.Errorf("price = %q", env.Data.Price)
	}
	if !env.Data.Timestamp.Equal(t0) {
		t.Errorf("timestamp = %v, want %v", env.Data.Timestamp, t0)
	}
	if len(env.Sources) != 2 {
		t.Errorf("sources len = %d, want 2", len(env.Sources))
	}
}

func TestOracleLastPrice_ReaderError500(t *testing.T) {
	reader := &stubPriceReader{err: errors.New("boom")}
	srv := v1.New(v1.Options{Prices: reader})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/oracle/lastprice?asset=native")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
}

// ─── /v1/oracle/x_last_price ───────────────────────────────────

func TestOracleXLastPrice_MissingBase400(t *testing.T) {
	srv := v1.New(v1.Options{Prices: &stubPriceReader{}})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/oracle/x_last_price?quote=fiat:USD")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestOracleXLastPrice_MissingQuote400(t *testing.T) {
	srv := v1.New(v1.Options{Prices: &stubPriceReader{}})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/oracle/x_last_price?base=native")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestOracleXLastPrice_IdentityPair400(t *testing.T) {
	srv := v1.New(v1.Options{Prices: &stubPriceReader{}})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/oracle/x_last_price?base=native&quote=native")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestOracleXLastPrice_NotFound404(t *testing.T) {
	reader := &stubPriceReader{snapshots: map[string]v1.PriceSnapshot{}}
	srv := v1.New(v1.Options{Prices: reader})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/oracle/x_last_price?base=native&quote=fiat:EUR")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

// TestOracleXLastPrice_RedisVWAPFallback — when prices_1m has no
// row for the requested cross pair, the SEP-40 x_last_price
// handler must fall through to the same TriangulatedPriceLooker
// /v1/price uses. Mirrors the lastprice fallback test.
func TestOracleXLastPrice_RedisVWAPFallback(t *testing.T) {
	reader := &stubPriceReader{err: v1.ErrPriceNotFound}
	looker := &stubTriangulatedPriceLooker{
		value:          "0.91",
		isTriangulated: true, // x_last_price is the cross-pair surface; triangulation is the headline use
		found:          true,
	}
	srv := v1.New(v1.Options{Prices: reader, Triangulated: looker})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/oracle/x_last_price?base=native&quote=fiat:EUR")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 via fallback", resp.StatusCode)
	}
	var env struct {
		Data v1.SEP40Price `json:"data"`
	}
	mustDecode(t, resp, &env)
	if env.Data.Price != "0.91" {
		t.Errorf("price = %q, want \"0.91\"", env.Data.Price)
	}
}

func TestOracleXLastPrice_InvalidBase400(t *testing.T) {
	srv := v1.New(v1.Options{Prices: &stubPriceReader{}})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/oracle/x_last_price?base=garbage&quote=fiat:USD")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestOracleXLastPrice_InvalidQuote400(t *testing.T) {
	srv := v1.New(v1.Options{Prices: &stubPriceReader{}})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/oracle/x_last_price?base=native&quote=garbage")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestOracleXLastPrice_ReaderError500(t *testing.T) {
	reader := &stubPriceReader{err: errors.New("boom")}
	srv := v1.New(v1.Options{Prices: reader})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/oracle/x_last_price?base=native&quote=fiat:EUR")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
}

func TestOracleXLastPrice_NoReader503(t *testing.T) {
	srv := v1.New(v1.Options{})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/oracle/x_last_price?base=native&quote=fiat:EUR")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

func TestOracleXLastPrice_HappyPath(t *testing.T) {
	t0 := time.Unix(1_770_000_000, 0).UTC()
	reader := &stubPriceReader{
		snapshots: map[string]v1.PriceSnapshot{
			"native/fiat:EUR": {
				AssetID: "native", Quote: "fiat:EUR",
				Price: "0.10", PriceType: "vwap", ObservedAt: t0,
			},
		},
	}
	srv := v1.New(v1.Options{Prices: reader})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/oracle/x_last_price?base=native&quote=fiat:EUR")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var env struct {
		Data v1.SEP40Price `json:"data"`
	}
	mustDecode(t, resp, &env)
	if env.Data.Asset != "native" {
		t.Errorf("asset = %q, want native (the base)", env.Data.Asset)
	}
	if env.Data.Price != "0.10" {
		t.Errorf("price = %q, want 0.10", env.Data.Price)
	}
}

// ─── /v1/oracle/prices ─────────────────────────────────────────

func TestOraclePrices_NoReader_Returns503(t *testing.T) {
	srv := v1.New(v1.Options{})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/oracle/prices?asset=native")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

func TestOraclePrices_MissingAsset400(t *testing.T) {
	srv := v1.New(v1.Options{Prices: &stubPriceReader{}})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/oracle/prices")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestOraclePrices_BadRecords400(t *testing.T) {
	srv := v1.New(v1.Options{Prices: &stubPriceReader{}})
	ts := startHTTPTest(t, srv.Handler())

	for _, raw := range []string{"0", "201", "abc", "-1"} {
		resp := mustGet(t, ts.URL+"/v1/oracle/prices?asset=native&records="+raw)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("records=%q: status = %d, want 400", raw, resp.StatusCode)
		}
	}
}

// TestOraclePrices_HappyPath exercises the SEP-40 prices() passthrough
// — verifies the response is an array of {price, timestamp} records,
// newest first, and that the records cap is honoured.
func TestOraclePrices_HappyPath(t *testing.T) {
	t0 := time.Unix(1_770_000_000, 0).UTC()
	reader := &stubPriceReader{
		recent: map[string][]v1.PriceSnapshot{
			"native/fiat:USD": {
				{AssetID: "native", Quote: "fiat:USD", Price: "0.12", PriceType: "vwap", ObservedAt: t0},
				{AssetID: "native", Quote: "fiat:USD", Price: "0.13", PriceType: "vwap", ObservedAt: t0.Add(-1 * time.Minute)},
				{AssetID: "native", Quote: "fiat:USD", Price: "0.14", PriceType: "vwap", ObservedAt: t0.Add(-2 * time.Minute)},
			},
		},
	}
	srv := v1.New(v1.Options{Prices: reader})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/oracle/prices?asset=native&records=2")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var env struct {
		Data []v1.SEP40Price `json:"data"`
	}
	mustDecode(t, resp, &env)
	if len(env.Data) != 2 {
		t.Fatalf("returned %d records, want 2 (records= cap)", len(env.Data))
	}
	if env.Data[0].Price != "0.12" {
		t.Errorf("data[0].price = %q, want newest-first 0.12", env.Data[0].Price)
	}
}

// TestOraclePrices_EmptyAsArray is the "no closed buckets yet"
// case — should return 200 with an empty array, not 404. Distinct
// from /v1/oracle/lastprice which 404s on a bare unknown asset.
func TestOraclePrices_EmptyAsArray(t *testing.T) {
	reader := &stubPriceReader{recent: map[string][]v1.PriceSnapshot{}}
	srv := v1.New(v1.Options{Prices: reader})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/oracle/prices?asset=native")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var env struct {
		Data []v1.SEP40Price `json:"data"`
	}
	mustDecode(t, resp, &env)
	if env.Data == nil {
		t.Errorf("data = null, want []  — empty array != null per OpenAPI")
	}
	if len(env.Data) != 0 {
		t.Errorf("expected empty array, got %d entries", len(env.Data))
	}
}

// (errors import retained — used by other tests in this file.)
var _ = errors.New
