package v1_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	v1 "github.com/StellarAtlas/stellar-atlas/internal/api/v1"
	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
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

func TestPriceBatch_InvalidQuote400(t *testing.T) {
	srv := v1.New(v1.Options{Prices: &stubPriceReader{}})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price/batch?asset_ids=native&quote=garbage")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (invalid quote id)", resp.StatusCode)
	}
}

func TestPriceBatchPost_InvalidQuote400(t *testing.T) {
	srv := v1.New(v1.Options{Prices: &stubPriceReader{}})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustPostJSON(t, ts.URL+"/v1/price/batch",
		`{"asset_ids":["native"],"quote":"garbage"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (invalid quote id)", resp.StatusCode)
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

// TestPriceBatch_AliasResolvesXLM pins F-1340 on the batch surface:
// asset_ids=native must resolve a snapshot published under the
// crypto:XLM alias key, exactly like handlePrice's primary read.
// Pre-fix the batch path queried the literal form only, silently
// dropping the row while /v1/price?asset=native served fresh.
func TestPriceBatch_AliasResolvesXLM(t *testing.T) {
	t0 := time.Unix(1_770_000_000, 0).UTC()
	reader := &stubPriceReader{
		// Only the crypto:XLM form is populated; native is absent.
		snapshots: map[string]v1.PriceSnapshot{
			"crypto:XLM/fiat:USD": {
				AssetID: "crypto:XLM", Quote: "fiat:USD",
				Price: "0.12", PriceType: "vwap", ObservedAt: t0,
			},
		},
		sources: map[string][]string{"crypto:XLM/fiat:USD": {"binance"}},
	}
	srv := v1.New(v1.Options{Prices: reader})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price/batch?asset_ids=native&quote=fiat:USD")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var env struct {
		Data []v1.PriceSnapshot `json:"data"`
	}
	mustDecode(t, resp, &env)
	if len(env.Data) != 1 {
		t.Fatalf("got %d entries, want 1 (crypto:XLM alias should resolve)", len(env.Data))
	}
	if env.Data[0].Price != "0.12" {
		t.Errorf("data[0].price = %q, want 0.12 (resolved via crypto:XLM alias)", env.Data[0].Price)
	}
}

// TestPriceBatch_RedisFallbackForRewrittenPair — when the
// PriceReader returns ErrPriceNotFound (typical for an aggregator-
// rewritten pair like XLM/fiat:USD whose literal form isn't in
// prices_1m), the batch path falls through to the Redis VWAP cache.
// Without this fix the batch endpoint silently omitted the headline
// pair even though /v1/price served it just fine.
func TestPriceBatch_RedisFallbackForRewrittenPair(t *testing.T) {
	reader := &stubPriceReader{err: v1.ErrPriceNotFound}
	looker := &stubTriangulatedPriceLooker{
		value:          "0.157384502084",
		isTriangulated: false,
		found:          true,
	}
	srv := v1.New(v1.Options{Prices: reader, Triangulated: looker})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price/batch?asset_ids=native&quote=fiat:USD")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var env struct {
		Data []v1.PriceSnapshot `json:"data"`
	}
	mustDecode(t, resp, &env)
	if len(env.Data) != 1 {
		t.Fatalf("got %d entries, want 1 (Redis fallback should populate)", len(env.Data))
	}
	if env.Data[0].Price != "0.157384502084" {
		t.Errorf("data[0].price = %q, want 0.157384502084", env.Data[0].Price)
	}
}

// TestPriceBatch_StablecoinFallback exercises the X / fiat:USD →
// X / <USD-pegged classic> retry inside fetchBatchRow. Mirrors the
// /v1/price behaviour shipped in #1217 / tryStablecoinFiatProxy.
//
// Pre-2026-05-10 the batch path inlined only the Redis-VWAP and
// fiat-cross-rate fallbacks, so an asset_id whose only price came
// via the stablecoin-proxy chain (e.g. USDT-G… / USDC-G… on a
// deployment with [aggregate].enable_stablecoin_fiat_proxy=false)
// returned 200 from /v1/price but was silently dropped from the
// batch envelope. R-005 in docs/review-2026-05-10.md.
func TestPriceBatch_StablecoinFallback(t *testing.T) {
	usdc, err := canonical.NewClassicAsset(
		"USDC",
		"GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN",
	)
	if err != nil {
		t.Fatal(err)
	}
	usdt, err := canonical.NewClassicAsset(
		"USDT",
		"GCQTGZQQ5G4PTM2GL7CDIFKUBIPEC52BROAQIAPW53XBRJVN6ZJVTG6V",
	)
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Unix(1_770_000_000, 0).UTC()
	reader := &stubPriceReader{
		// USDT/fiat:USD missing — the literal pair never has rows on
		// mainnet because no on-chain trades quote in fiat:USD.
		// USDT/USDC present — provides the proxy value.
		snapshots: map[string]v1.PriceSnapshot{
			usdt.String() + "/" + usdc.String(): {
				AssetID: usdt.String(), Quote: usdc.String(),
				Price: "1.001", PriceType: "vwap", ObservedAt: t0,
			},
		},
		sources: map[string][]string{
			usdt.String() + "/" + usdc.String(): {"sdex"},
		},
	}
	srv := v1.New(v1.Options{
		Prices:            reader,
		USDPeggedClassics: []canonical.Asset{usdc},
	})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price/batch?asset_ids="+usdt.String()+"&quote=fiat:USD")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var env struct {
		Data []v1.PriceSnapshot `json:"data"`
	}
	mustDecode(t, resp, &env)
	if len(env.Data) != 1 {
		t.Fatalf("got %d entries, want 1 (stablecoin proxy should populate)", len(env.Data))
	}
	if env.Data[0].Price != "1.001" {
		t.Errorf("data[0].price = %q, want 1.001", env.Data[0].Price)
	}
	if env.Data[0].AssetID != usdt.String() {
		t.Errorf("data[0].asset_id = %q, want %q", env.Data[0].AssetID, usdt.String())
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

// ─── POST /v1/price/batch ──────────────────────────────────────

func mustPostJSON(t *testing.T, url, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func TestPriceBatchPost_NoReader_Returns503(t *testing.T) {
	srv := v1.New(v1.Options{})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustPostJSON(t, ts.URL+"/v1/price/batch", `{"asset_ids":["native"]}`)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

func TestPriceBatchPost_InvalidJSON400(t *testing.T) {
	srv := v1.New(v1.Options{Prices: &stubPriceReader{}})
	ts := startHTTPTest(t, srv.Handler())

	for _, body := range []string{
		`not-json`,
		`{`,
		`{"asset_ids":[1,2]}`, // ints, not strings
		`{"asset_ids":["native"],"quote":"x","unknown":42}`, // DisallowUnknownFields
	} {
		resp := mustPostJSON(t, ts.URL+"/v1/price/batch", body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("body %q status = %d, want 400", body, resp.StatusCode)
		}
	}
}

func TestPriceBatchPost_EmptyArray400(t *testing.T) {
	srv := v1.New(v1.Options{Prices: &stubPriceReader{}})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustPostJSON(t, ts.URL+"/v1/price/batch", `{"asset_ids":[]}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestPriceBatchPost_TooManyAssets400(t *testing.T) {
	// 1001 distinct ids must 400 (POST cap is 1000).
	ids := make([]string, 0, 1001)
	for i := 0; i < 1001; i++ {
		// Synthesise unique fiat:XYZ codes.
		ids = append(ids, fmt.Sprintf("fiat:%c%c%c",
			'A'+i%26, 'A'+(i/26)%26, 'A'+(i/26/26)%26))
	}
	body := `{"asset_ids":["` + strings.Join(ids, `","`) + `"]}`
	srv := v1.New(v1.Options{Prices: &stubPriceReader{}})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustPostJSON(t, ts.URL+"/v1/price/batch", body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestPriceBatchPost_OmitsMissingAssets(t *testing.T) {
	t0 := time.Unix(1_770_000_000, 0).UTC()
	reader := &stubPriceReader{
		snapshots: map[string]v1.PriceSnapshot{
			"native/fiat:USD": {AssetID: "native", Quote: "fiat:USD", Price: "0.12", PriceType: "last_trade", ObservedAt: t0},
		},
	}
	srv := v1.New(v1.Options{Prices: reader})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustPostJSON(t, ts.URL+"/v1/price/batch",
		`{"asset_ids":["native","fiat:EUR"],"quote":"fiat:USD"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var env struct {
		Data []v1.PriceSnapshot `json:"data"`
	}
	mustDecode(t, resp, &env)
	if len(env.Data) != 1 {
		t.Errorf("got %d entries, want 1 (one missing)", len(env.Data))
	}
}

func TestPriceBatchPost_LargeBatchAccepted(t *testing.T) {
	// 200 distinct Classic assets, all known. Verifies the POST
	// ceiling is genuinely larger than GET's 100 (200 > 100, would
	// have 400'd on the GET path) and that the shared core logic
	// handles the larger batch without bottlenecking.
	//
	// Synthesise alphanumeric 4-char codes "BAAA"..."BHRR" (200
	// unique combinations) paired with one well-known issuer; that
	// passes both validateClassicAssetCode and the strkey CRC check.
	const issuer = "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"
	t0 := time.Unix(1_770_000_000, 0).UTC()
	snapshots := make(map[string]v1.PriceSnapshot, 200)
	ids := make([]string, 0, 200)
	for i := 0; i < 200; i++ {
		// Codes start with 'B' to avoid colliding with real fiat
		// allow-list (USD/EUR/...) at the prefix; remaining 3 chars
		// vary i across A..Z.
		code := fmt.Sprintf("B%c%c%c",
			'A'+i/(26*26)%26,
			'A'+(i/26)%26,
			'A'+i%26)
		id := code + "-" + issuer
		ids = append(ids, id)
		snapshots[id+"/fiat:JPY"] = v1.PriceSnapshot{
			AssetID: id, Quote: "fiat:JPY",
			Price: "150", PriceType: "last_trade", ObservedAt: t0,
		}
	}
	reader := &stubPriceReader{snapshots: snapshots}
	srv := v1.New(v1.Options{Prices: reader})
	ts := startHTTPTest(t, srv.Handler())

	body := `{"asset_ids":["` + strings.Join(ids, `","`) + `"],"quote":"fiat:JPY"}`
	resp := mustPostJSON(t, ts.URL+"/v1/price/batch", body)
	if resp.StatusCode != http.StatusOK {
		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, buf.String())
	}
	var env struct {
		Data []v1.PriceSnapshot `json:"data"`
	}
	mustDecode(t, resp, &env)
	if len(env.Data) != 200 {
		t.Errorf("got %d entries, want 200", len(env.Data))
	}
}

func TestPriceBatchPost_DefaultQuoteFiatUSD(t *testing.T) {
	t0 := time.Unix(1_770_000_000, 0).UTC()
	reader := &stubPriceReader{
		snapshots: map[string]v1.PriceSnapshot{
			"native/fiat:USD": {AssetID: "native", Quote: "fiat:USD", Price: "0.12", PriceType: "last_trade", ObservedAt: t0},
		},
	}
	srv := v1.New(v1.Options{Prices: reader})
	ts := startHTTPTest(t, srv.Handler())

	// No quote field — should default to fiat:USD.
	resp := mustPostJSON(t, ts.URL+"/v1/price/batch", `{"asset_ids":["native"]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var env struct {
		Data []v1.PriceSnapshot `json:"data"`
	}
	mustDecode(t, resp, &env)
	if len(env.Data) != 1 || env.Data[0].Quote != "fiat:USD" {
		t.Errorf("default quote not applied; got data=%+v", env.Data)
	}
}

// F-0073 closure (2026-05-28): /v1/price/batch accepts `pairs=` as
// alias for `asset_ids=` so CG-style callers using `pairs` reach
// the endpoint without a 400 detour.
func TestPriceBatch_PairsAcceptedAsAssetIdsAlias(t *testing.T) {
	srv := v1.New(v1.Options{Prices: &stubPriceReader{}})
	ts := startHTTPTest(t, srv.Handler())
	resp := mustGet(t, ts.URL+"/v1/price/batch?pairs=native")
	if resp.StatusCode == http.StatusBadRequest {
		t.Errorf("pairs= alias rejected (400); want it accepted as asset_ids= alias")
	}
}

func TestPriceBatch_AssetIdsAndPairsBoth_Returns400(t *testing.T) {
	srv := v1.New(v1.Options{Prices: &stubPriceReader{}})
	ts := startHTTPTest(t, srv.Handler())
	resp := mustGet(t, ts.URL+"/v1/price/batch?asset_ids=native&pairs=native")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d want 400 (both asset_ids+pairs)", resp.StatusCode)
	}
}
