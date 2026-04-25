package v1_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	v1 "github.com/RatesEngine/rates-engine/internal/api/v1"
)

func TestPriceBatch_NoReader_Returns503(t *testing.T) {
	srv := v1.New(v1.Options{})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price/batch?asset_ids=native")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

func TestPriceBatch_MissingAssetIds400(t *testing.T) {
	srv := v1.New(v1.Options{Prices: &stubPriceReader{}})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price/batch")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestPriceBatch_EmptyAfterTrim400(t *testing.T) {
	// `?asset_ids=,,` parses to zero usable ids and must 400.
	srv := v1.New(v1.Options{Prices: &stubPriceReader{}})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price/batch?asset_ids=,,,")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestPriceBatch_TooManyAssets400(t *testing.T) {
	// 101 distinct asset_ids must 400 (limit fires after dedupe).
	// Synthesise unique 3-character fiat codes (A..Z + AA..) so each
	// entry is a distinct fiat:XYZ — dedupe will not collapse them
	// and the limit check is the only failure mode.
	ids := make([]string, 0, 101)
	for i := 0; i < 101; i++ {
		ids = append(ids, "fiat:"+string(rune('A'+i%26))+string(rune('A'+i/26))+"X")
	}
	srv := v1.New(v1.Options{Prices: &stubPriceReader{}})
	ts := startHTTPTest(t, srv.Handler())
	resp := mustGet(t, ts.URL+"/v1/price/batch?asset_ids="+strings.Join(ids, ","))
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestPriceBatch_InvalidAssetReturns400(t *testing.T) {
	srv := v1.New(v1.Options{Prices: &stubPriceReader{}})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price/batch?asset_ids=native,@@@")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestPriceBatch_IdentityRejected400(t *testing.T) {
	srv := v1.New(v1.Options{Prices: &stubPriceReader{}})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price/batch?asset_ids=fiat:USD&quote=fiat:USD")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestPriceBatch_OmitsMissingAssets(t *testing.T) {
	t0 := time.Unix(1_770_000_000, 0).UTC()
	reader := &stubPriceReader{
		snapshots: map[string]v1.PriceSnapshot{
			"native/fiat:USD": {
				AssetID: "native", Quote: "fiat:USD",
				Price: "0.12", PriceType: "last_trade", ObservedAt: t0,
			},
		},
		sources: map[string][]string{
			"native/fiat:USD": {"soroswap"},
		},
	}
	srv := v1.New(v1.Options{Prices: reader})
	ts := startHTTPTest(t, srv.Handler())

	// Two requested, one known. Response array should have one row.
	resp := mustGet(t, ts.URL+"/v1/price/batch?asset_ids=native,fiat:EUR&quote=fiat:USD")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var env struct {
		Data    []v1.PriceSnapshot `json:"data"`
		Sources []string           `json:"sources"`
	}
	mustDecode(t, resp, &env)
	if len(env.Data) != 1 {
		t.Fatalf("got %d entries, want 1", len(env.Data))
	}
	if env.Data[0].AssetID != "native" {
		t.Errorf("data[0].asset_id = %q, want native", env.Data[0].AssetID)
	}
	if len(env.Sources) != 1 || env.Sources[0] != "soroswap" {
		t.Errorf("sources = %v, want [soroswap]", env.Sources)
	}
}

func TestPriceBatch_DeduplicatesIds(t *testing.T) {
	t0 := time.Unix(1_770_000_000, 0).UTC()
	reader := &stubPriceReader{
		snapshots: map[string]v1.PriceSnapshot{
			"native/fiat:USD": {
				AssetID: "native", Quote: "fiat:USD",
				Price: "0.12", PriceType: "last_trade", ObservedAt: t0,
			},
		},
	}
	srv := v1.New(v1.Options{Prices: reader})
	ts := startHTTPTest(t, srv.Handler())

	// `native` appears 3 times; result must be a single row.
	resp := mustGet(t, ts.URL+"/v1/price/batch?asset_ids=native,native,native&quote=fiat:USD")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var env struct {
		Data []v1.PriceSnapshot `json:"data"`
	}
	mustDecode(t, resp, &env)
	if len(env.Data) != 1 {
		t.Errorf("got %d entries, want 1 (after dedupe)", len(env.Data))
	}
}

func TestPriceBatch_StaleFlagOR(t *testing.T) {
	t0 := time.Unix(1_770_000_000, 0).UTC()
	reader := &stubPriceReader{
		snapshots: map[string]v1.PriceSnapshot{
			"native/fiat:USD":   {AssetID: "native", Quote: "fiat:USD", Price: "0.12", PriceType: "last_trade", ObservedAt: t0},
			"fiat:EUR/fiat:USD": {AssetID: "fiat:EUR", Quote: "fiat:USD", Price: "1.08", PriceType: "last_trade", ObservedAt: t0},
		},
		stale: map[string]bool{
			"fiat:EUR/fiat:USD": true,
		},
	}
	srv := v1.New(v1.Options{Prices: reader})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price/batch?asset_ids=native,fiat:EUR&quote=fiat:USD")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var env struct {
		Flags v1.Flags `json:"flags"`
	}
	mustDecode(t, resp, &env)
	if !env.Flags.Stale {
		t.Errorf("expected envelope.flags.stale=true (any item stale)")
	}
}

func TestPriceBatch_ReaderError500(t *testing.T) {
	reader := &stubPriceReader{err: errors.New("boom")}
	srv := v1.New(v1.Options{Prices: reader})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price/batch?asset_ids=native&quote=fiat:USD")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
}
