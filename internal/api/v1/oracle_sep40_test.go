package v1_test

import (
	"errors"
	"net/http"
	"testing"
	"time"

	v1 "github.com/RatesEngine/rates-engine/internal/api/v1"
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
