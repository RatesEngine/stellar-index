package v1_test

import (
	"context"
	"math/big"
	"net/http"
	"testing"
	"time"

	v1 "github.com/RatesEngine/rates-engine/internal/api/v1"
	"github.com/RatesEngine/rates-engine/internal/canonical"
)

type stubOracleReader struct {
	updates    []canonical.OracleUpdate
	lastAsset  string
	lastSource string
	err        error
}

func (r *stubOracleReader) LatestOracleUpdatesForAsset(_ context.Context, asset canonical.Asset, src string) ([]canonical.OracleUpdate, error) {
	r.lastAsset = asset.String()
	r.lastSource = src
	if r.err != nil {
		return nil, r.err
	}
	return r.updates, nil
}

func mkReflectorUpdate(source string, priceRaw string, decimals uint8) canonical.OracleUpdate {
	usdc, _ := canonical.ParseAsset("fiat:USD")
	price, _ := new(big.Int).SetString(priceRaw, 10)
	return canonical.OracleUpdate{
		Source:     source,
		ContractID: "CAS3J7GYLGXMF6TDJBBYYSE3HQ6BBSMLNUQ34T6TZMYMW2EVH34XOWMA",
		Ledger:     52_430_001,
		TxHash:     "cafebabecafebabecafebabecafebabecafebabecafebabecafebabecafebabe",
		OpIndex:    0,
		Timestamp:  time.Unix(1_772_000_000, 0).UTC(),
		Asset:      canonical.NativeAsset(),
		Quote:      usdc,
		Price:      canonical.NewAmount(price),
		Decimals:   decimals,
		Confidence: 0.95,
		Observer:   "GRELAYER123",
	}
}

func TestOracleLatest_503WhenReaderNil(t *testing.T) {
	srv := v1.New(v1.Options{})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/oracle/latest?asset=native")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}

func TestOracleLatest_MissingAsset400(t *testing.T) {
	srv := v1.New(v1.Options{Oracle: &stubOracleReader{}})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/oracle/latest")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestOracleLatest_InvalidAsset400(t *testing.T) {
	srv := v1.New(v1.Options{Oracle: &stubOracleReader{}})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/oracle/latest?asset=not-an-asset")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestOracleLatest_ReturnsReadings(t *testing.T) {
	reader := &stubOracleReader{
		updates: []canonical.OracleUpdate{
			// 14-decimal price — Reflector's canonical scale.
			// 12000000000000 at 14 decimals → 0.12000000000000
			mkReflectorUpdate("reflector-dex", "12000000000000", 14),
			mkReflectorUpdate("reflector-cex", "12500000000000", 14),
		},
	}
	srv := v1.New(v1.Options{Oracle: reader})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/oracle/latest?asset=native")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var env struct {
		Data []v1.OracleReading `json:"data"`
	}
	mustDecode(t, resp, &env)

	if len(env.Data) != 2 {
		t.Fatalf("got %d readings, want 2", len(env.Data))
	}
	r := env.Data[0]
	if r.Source != "reflector-dex" {
		t.Errorf("source = %q", r.Source)
	}
	if r.Price != "0.12000000000000" {
		t.Errorf("price = %q, want 0.12000000000000 (14-decimal scaling)", r.Price)
	}
	if r.PriceRaw != "12000000000000" {
		t.Errorf("price_raw = %q, want the integer value", r.PriceRaw)
	}
	if r.Decimals != 14 {
		t.Errorf("decimals = %d, want 14", r.Decimals)
	}
}

func TestOracleLatest_SourceFilterThreaded(t *testing.T) {
	reader := &stubOracleReader{
		updates: []canonical.OracleUpdate{mkReflectorUpdate("reflector-dex", "12000000000000", 14)},
	}
	srv := v1.New(v1.Options{Oracle: reader})
	ts := httpTestServer(t, srv)

	_ = mustGet(t, ts.URL+"/v1/oracle/latest?asset=native&source=reflector-dex")

	if reader.lastSource != "reflector-dex" {
		t.Errorf("source filter = %q, want reflector-dex", reader.lastSource)
	}
	if reader.lastAsset != "native" {
		t.Errorf("asset = %q, want native", reader.lastAsset)
	}
}

func TestOracleLatest_EmptyIsEmptyArray(t *testing.T) {
	srv := v1.New(v1.Options{Oracle: &stubOracleReader{updates: nil}})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/oracle/latest?asset=native")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (empty != error)", resp.StatusCode)
	}
	var env struct {
		Data []v1.OracleReading `json:"data"`
	}
	mustDecode(t, resp, &env)
	if env.Data == nil {
		t.Error("empty should serialise as [] not null")
	}
}
