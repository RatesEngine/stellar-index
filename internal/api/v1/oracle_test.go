package v1_test

import (
	"context"
	"errors"
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
	lastAssets []string
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

func (r *stubOracleReader) LatestOracleUpdatesForAssets(_ context.Context, assets []canonical.Asset, src string) ([]canonical.OracleUpdate, error) {
	r.lastAssets = make([]string, len(assets))
	for i, a := range assets {
		r.lastAssets[i] = a.String()
	}
	if len(assets) > 0 {
		r.lastAsset = assets[0].String()
	}
	r.lastSource = src
	if r.err != nil {
		return nil, r.err
	}
	return r.updates, nil
}

func (r *stubOracleReader) LatestOracleStreams(_ context.Context) ([]canonical.OracleUpdate, error) {
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

// TestOracleLatest_UnknownSource400 — `?source=` with a name that
// isn't in the in-memory `external.Registry` returns 400 instead
// of falling through to an empty page (same silent-empty-page
// anti-pattern fix shipped on /v1/markets and /v1/observations).
func TestOracleLatest_UnknownSource400(t *testing.T) {
	srv := v1.New(v1.Options{Oracle: &stubOracleReader{}})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/oracle/latest?asset=native&source=fake-venue")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var p v1.Problem
	mustDecode(t, resp, &p)
	if p.Type != "https://api.ratesengine.net/errors/unknown-source" {
		t.Errorf("Type = %q", p.Type)
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

func TestOracleLatest_ReaderError500(t *testing.T) {
	reader := &stubOracleReader{err: errors.New("storage broke")}
	srv := v1.New(v1.Options{Oracle: reader})
	tsrv := httpTestServer(t, srv)

	resp := mustGet(t, tsrv.URL+"/v1/oracle/latest?asset=native")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
}

// Negative-Price rendering through the full HTTP handler — pins
// the sign-preserving scaledDecimalString path (the
// oracleReadingFrom helper is unexported, so this exercises it
// indirectly).
func TestOracleLatest_negativePricePreservesSign(t *testing.T) {
	reader := &stubOracleReader{
		updates: []canonical.OracleUpdate{
			mkReflectorUpdate("reflector-cex", "-12420000000000", 14),
		},
	}
	srv := v1.New(v1.Options{Oracle: reader})
	tsrv := httpTestServer(t, srv)

	resp := mustGet(t, tsrv.URL+"/v1/oracle/latest?asset=native")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var env struct {
		Data []v1.OracleReading `json:"data"`
	}
	mustDecode(t, resp, &env)
	if len(env.Data) != 1 {
		t.Fatalf("got %d rows, want 1", len(env.Data))
	}
	if env.Data[0].Price[0] != '-' {
		t.Errorf("Price = %q, want leading \"-\"", env.Data[0].Price)
	}
	if env.Data[0].Decimals != 14 {
		t.Errorf("Decimals = %d, want 14", env.Data[0].Decimals)
	}
}

// TestOracleLatest_NativeExpandsToCryptoXLM pins the user-facing
// → oracle-internal asset translation. /v1/oracle/latest?asset=native
// should also query against `crypto:XLM` because Reflector keys
// observations by the global crypto ticker, not by the per-network
// `native` form. Without this the endpoint returns an empty array
// even though Reflector publishes XLM continuously.
func TestOracleLatest_NativeExpandsToCryptoXLM(t *testing.T) {
	reader := &stubOracleReader{}
	srv := v1.New(v1.Options{Oracle: reader})
	tsrv := httpTestServer(t, srv)

	resp := mustGet(t, tsrv.URL+"/v1/oracle/latest?asset=native")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	wantKeys := []string{"native", "crypto:XLM"}
	if len(reader.lastAssets) != len(wantKeys) {
		t.Fatalf("lastAssets = %+v, want %+v", reader.lastAssets, wantKeys)
	}
	for i, k := range wantKeys {
		if reader.lastAssets[i] != k {
			t.Errorf("lastAssets[%d] = %q, want %q", i, reader.lastAssets[i], k)
		}
	}
}

// TestOracleLatest_ClassicExpandsToCryptoTicker pins the same
// translation for stablecoin classic credit assets — Reflector
// publishes USDC under the global `crypto:USDC` ticker rather
// than per-issuer.
func TestOracleLatest_ClassicExpandsToCryptoTicker(t *testing.T) {
	reader := &stubOracleReader{}
	srv := v1.New(v1.Options{Oracle: reader})
	tsrv := httpTestServer(t, srv)

	resp := mustGet(t, tsrv.URL+"/v1/oracle/latest?asset=USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	wantKeys := []string{
		"USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN",
		"crypto:USDC",
	}
	if len(reader.lastAssets) != len(wantKeys) {
		t.Fatalf("lastAssets = %+v, want %+v", reader.lastAssets, wantKeys)
	}
	for i, k := range wantKeys {
		if reader.lastAssets[i] != k {
			t.Errorf("lastAssets[%d] = %q, want %q", i, reader.lastAssets[i], k)
		}
	}
}

// ─── /v1/oracle/streams handler tests (F-1226, audit-2026-05-12) ───

// TestOracleStreams_EmptyArrayWhenReaderNil pins the degradation
// posture: nil OracleReader returns a 200 with `data: []`, not a
// 503. Consistent with /v1/oracle/latest's "nothing to report"
// branch — the explorer's /oracles page renders an empty table
// rather than an error state when no readings have arrived.
func TestOracleStreams_EmptyArrayWhenReaderNil(t *testing.T) {
	srv := v1.New(v1.Options{})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/oracle/streams")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var env struct {
		Data []v1.OracleReading `json:"data"`
	}
	mustDecode(t, resp, &env)
	if len(env.Data) != 0 {
		t.Errorf("data = %+v, want empty array", env.Data)
	}
}

// TestOracleStreams_FiltersToOracleClass pins the source-class
// filter that's baked into the handler: rows from sources that
// are NOT ClassOracle (e.g. CoinGecko = ClassAggregator,
// ECB = ClassAuthoritySanity) write into the same oracle_updates
// hypertable for divergence-comparison purposes, but they must
// not appear on the public /oracles page. The class is a registry
// fact, not a per-row flag — filtering happens at the wire boundary.
func TestOracleStreams_FiltersToOracleClass(t *testing.T) {
	reader := &stubOracleReader{
		updates: []canonical.OracleUpdate{
			mkReflectorUpdate("reflector-dex", "12000000000000", 14),
			mkReflectorUpdate("redstone", "12100000000000", 14),
			mkReflectorUpdate("band", "12200000000000", 14),
			// Non-oracle classes — must be filtered out.
			mkReflectorUpdate("coingecko", "12050000000000", 14),
			mkReflectorUpdate("ecb", "12060000000000", 14),
		},
	}
	srv := v1.New(v1.Options{Oracle: reader})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/oracle/streams")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var env struct {
		Data []v1.OracleReading `json:"data"`
	}
	mustDecode(t, resp, &env)

	// 3 oracle sources, 2 non-oracle dropped.
	if len(env.Data) != 3 {
		t.Fatalf("len(data) = %d, want 3 oracle rows", len(env.Data))
	}
	seen := map[string]bool{}
	for _, r := range env.Data {
		seen[r.Source] = true
	}
	wantOracles := []string{"reflector-dex", "redstone", "band"}
	for _, want := range wantOracles {
		if !seen[want] {
			t.Errorf("missing expected oracle %q in response", want)
		}
	}
	for _, notWant := range []string{"coingecko", "ecb"} {
		if seen[notWant] {
			t.Errorf("non-oracle %q leaked into /v1/oracle/streams response", notWant)
		}
	}
}

// TestOracleStreams_RendersPriceAtDeclaredDecimals pins the same
// scaling contract as /v1/oracle/latest — `price_raw` carries the
// integer + `price` carries the decimal-scaled human string. A
// regression that bypasses scaledDecimalString (e.g. raw passthrough
// on the streams path) would surface as a 14-decimal-off price.
func TestOracleStreams_RendersPriceAtDeclaredDecimals(t *testing.T) {
	reader := &stubOracleReader{
		updates: []canonical.OracleUpdate{
			mkReflectorUpdate("reflector-dex", "12000000000000", 14),
		},
	}
	srv := v1.New(v1.Options{Oracle: reader})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/oracle/streams")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var env struct {
		Data []v1.OracleReading `json:"data"`
	}
	mustDecode(t, resp, &env)
	if len(env.Data) != 1 {
		t.Fatalf("len(data) = %d, want 1", len(env.Data))
	}
	r := env.Data[0]
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

// TestOracleStreams_ReaderError500 — an unexpected error from the
// hypertable scan surfaces as a 500. Same posture as
// /v1/oracle/latest. (The timeout/clientAborted branches are
// covered by handler unit-tests where a context can be cancelled
// before the handler returns; here we only assert the generic
// error surface.)
func TestOracleStreams_ReaderError500(t *testing.T) {
	reader := &stubOracleReader{err: errors.New("scan failed")}
	srv := v1.New(v1.Options{Oracle: reader})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/oracle/streams")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
}
