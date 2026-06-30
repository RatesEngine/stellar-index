package v1_test

import (
	"context"
	"errors"
	"math/big"
	"net/http"
	"strings"
	"testing"
	"time"

	v1 "github.com/StellarIndex/stellar-index/internal/api/v1"
	"github.com/StellarIndex/stellar-index/internal/canonical"
)

// stubPriceReader implements v1.PriceReader.
type stubPriceReader struct {
	// Lookup keyed on "<base>/<quote>".
	snapshots map[string]v1.PriceSnapshot
	stale     map[string]bool
	sources   map[string][]string
	// recent is the per-pair history returned by RecentClosedSnapshots.
	// Same key shape as `snapshots`. Empty/missing yields []v1.PriceSnapshot{}
	// (no observations) — matches the production reader's contract.
	recent map[string][]v1.PriceSnapshot
	err    error
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

func (r *stubPriceReader) RecentClosedSnapshots(_ context.Context, a, q canonical.Asset, n int) ([]v1.PriceSnapshot, error) {
	if r.err != nil {
		return nil, r.err
	}
	key := a.String() + "/" + q.String()
	rows, ok := r.recent[key]
	if !ok {
		return []v1.PriceSnapshot{}, nil
	}
	if n < len(rows) {
		rows = rows[:n]
	}
	return rows, nil
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

// stubTriangulatedPriceLooker implements v1.TriangulatedPriceLooker.
// It exists primarily to test the Redis-VWAP-fallback path: when
// Timescale has no row for the requested pair, the handler should
// honour cache hits regardless of whether the provenance marker is
// present (direct stablecoin-fiat-proxy rewrites have no marker but
// must still surface so the headline pair serves real data).
type stubTriangulatedPriceLooker struct {
	value          string
	isTriangulated bool
	found          bool
	err            error
}

func (s *stubTriangulatedPriceLooker) LookupTriangulatedVWAP(
	_ context.Context, _, _ canonical.Asset, _ time.Duration,
) (string, bool, bool, error) {
	return s.value, s.isTriangulated, s.found, s.err
}

// TestPrice_RedisVWAPFallback_DirectRewriteServes — when prices_1m
// has no row for the requested pair (typical for a stablecoin-fiat-
// proxy rewrite like XLM/fiat:USD synthesised from XLM/USDC-GA5Z…),
// the handler falls through to the Redis VWAP cache. With found=true
// AND isTriangulated=false the response is 200 with
// `flags.triangulated=false` — direct rewrites are NOT triangulated.
//
// Pre-2026-05-04 this returned 404 because the handler insisted on
// the provenance marker; the rewrite case has no marker by design.
func TestPrice_RedisVWAPFallback_DirectRewriteServes(t *testing.T) {
	reader := &stubPriceReader{
		err: v1.ErrPriceNotFound, // Timescale miss
	}
	looker := &stubTriangulatedPriceLooker{
		value:          "0.1242",
		isTriangulated: false, // direct (rewritten) VWAP — no marker
		found:          true,
	}
	srv := v1.New(v1.Options{Prices: reader, Triangulated: looker})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price?asset=native&quote=fiat:USD")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (Redis fallback should serve direct rewrites)", resp.StatusCode)
	}
	body, _ := readAll(resp)
	for _, s := range []string{
		`"price":"0.1242"`,
		`"price_type":"vwap"`,
		`"triangulated":false`,
	} {
		if !strings.Contains(body, s) {
			t.Errorf("body missing %q: %s", s, body)
		}
	}
}

// TestPrice_RedisVWAPFallback_TriangulatedSetsFlag — when the
// provenance marker IS present the response sets
// `flags.triangulated=true` so the customer can tell the value came
// from the triangulation worker (vs. a direct rewrite).
func TestPrice_RedisVWAPFallback_TriangulatedSetsFlag(t *testing.T) {
	reader := &stubPriceReader{err: v1.ErrPriceNotFound}
	looker := &stubTriangulatedPriceLooker{
		value:          "0.5500",
		isTriangulated: true,
		found:          true,
	}
	srv := v1.New(v1.Options{Prices: reader, Triangulated: looker})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price?asset=crypto:XLM&quote=fiat:EUR")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := readAll(resp)
	if !strings.Contains(body, `"triangulated":true`) {
		t.Errorf("triangulated flag not set: %s", body)
	}
}

// TestPrice_FallbackChainSetsStaleFlag pins the F-1254 contract:
// every priceFallback degradation MUST surface flags.stale=true.
//
// The May-10 SEV-2 (Redis BGSAVE blocked → cache empty → every
// closed-bucket read hit ErrPriceNotFound → priceFallback served
// last-known-good for ~9h) didn't surface stale=true to customers
// because the handler used to clear the flag after a successful
// fallback. Customers got stale data with stale=false, defeating
// the entire point of the contract.
//
// The fallback chain is itself the staleness signal — by definition
// any path that lands in priceFallback is below the surface's
// documented baseline contract. This test pins that semantic for
// every fallback the handler reaches.
//
// F-1254 (audit-2026-05-12).
func TestPrice_FallbackChainSetsStaleFlag(t *testing.T) {
	t.Run("triangulated fallback", func(t *testing.T) {
		reader := &stubPriceReader{err: v1.ErrPriceNotFound}
		looker := &stubTriangulatedPriceLooker{
			value:          "0.5500",
			isTriangulated: true,
			found:          true,
		}
		srv := v1.New(v1.Options{Prices: reader, Triangulated: looker})
		ts := startHTTPTest(t, srv.Handler())

		resp := mustGet(t, ts.URL+"/v1/price?asset=crypto:XLM&quote=fiat:EUR")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		body, _ := readAll(resp)
		if !strings.Contains(body, `"stale":true`) {
			t.Errorf("triangulated fallback must set stale=true; body: %s", body)
		}
	})
	t.Run("direct stablecoin-rewrite fallback", func(t *testing.T) {
		reader := &stubPriceReader{err: v1.ErrPriceNotFound}
		looker := &stubTriangulatedPriceLooker{
			value:          "0.1242",
			isTriangulated: false, // direct rewrite — no triangulation marker
			found:          true,
		}
		srv := v1.New(v1.Options{Prices: reader, Triangulated: looker})
		ts := startHTTPTest(t, srv.Handler())

		resp := mustGet(t, ts.URL+"/v1/price?asset=native&quote=fiat:USD")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		body, _ := readAll(resp)
		if !strings.Contains(body, `"stale":true`) {
			t.Errorf("direct-rewrite fallback must set stale=true; body: %s", body)
		}
	})
}

// TestPrice_RedisVWAPFallback_NotFoundPreserves404 — when the cache
// has no value for the requested pair, the handler still returns
// 404 just as if the looker weren't wired at all.
func TestPrice_RedisVWAPFallback_NotFoundPreserves404(t *testing.T) {
	reader := &stubPriceReader{err: v1.ErrPriceNotFound}
	looker := &stubTriangulatedPriceLooker{found: false}
	srv := v1.New(v1.Options{Prices: reader, Triangulated: looker})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price?asset=native&quote=fiat:USD")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

// TestPrice_StablecoinFiatProxy_FallsThroughToClassicPeg — the
// fix for the production regression where /v1/price?asset=native&quote=fiat:USD
// 404'd even though the aggregator had populated native/USDC-classic
// because the operator hadn't enabled
// [aggregate].enable_stablecoin_fiat_proxy. The handler-side fallback
// walks usdPeggedClassics and returns the first peg whose pair has a
// row, with flags.triangulated=true and the requested quote echoed back.
func TestPrice_StablecoinFiatProxy_FallsThroughToClassicPeg(t *testing.T) {
	usdcClassic, err := canonical.ParseAsset("USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")
	if err != nil {
		t.Fatalf("parse USDC: %v", err)
	}
	pegSnap := v1.PriceSnapshot{
		AssetID:    "native",
		Quote:      usdcClassic.String(),
		Price:      "0.1626",
		PriceType:  "vwap",
		ObservedAt: time.Unix(1745000000, 0).UTC(),
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

	resp := mustGet(t, ts.URL+"/v1/price?asset=native&quote=fiat:USD")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (stablecoin-fiat-proxy fallback should serve)", resp.StatusCode)
	}
	body, _ := readAll(resp)
	for _, want := range []string{
		`"price":"0.1626"`,
		`"price_type":"vwap"`,
		`"quote":"fiat:USD"`,
		`"triangulated":true`,
		`"sources":["sdex"]`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q: %s", want, body)
		}
	}
}

// TestPrice_StablecoinFiatProxy_NoPegsLeaves404 — when the operator
// hasn't declared any usd_pegged_classic_assets, the fallback skips
// silently and the handler still 404s. This pins the opt-in shape.
func TestPrice_StablecoinFiatProxy_NoPegsLeaves404(t *testing.T) {
	reader := &stubPriceReader{err: v1.ErrPriceNotFound}
	srv := v1.New(v1.Options{Prices: reader}) // no USDPeggedClassics
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price?asset=native&quote=fiat:USD")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

// TestPrice_StablecoinFiatProxy_PegItselfReturnsOne pins F-1232
// (codex audit-2026-05-12): querying the declared USD peg against
// fiat:USD must return ~$1, not 404. Pre-fix, /v1/price?asset=
// USDC-GA5Z…&quote=fiat:USD 404'd because the fallback loop
// skipped peg==asset; the asset-detail page meanwhile surfaced a
// real enrichment price. Now the peg-self branch returns a
// synthetic price=1.0 snapshot with PriceType=peg so the wire
// shape across single + batch + asset-detail is consistent.
func TestPrice_StablecoinFiatProxy_PegItselfReturnsOne(t *testing.T) {
	usdcClassic, err := canonical.ParseAsset("USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")
	if err != nil {
		t.Fatalf("parse USDC: %v", err)
	}
	reader := &stubPriceReader{err: v1.ErrPriceNotFound}
	srv := v1.New(v1.Options{
		Prices:            reader,
		USDPeggedClassics: []canonical.Asset{usdcClassic},
	})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price?asset=USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN&quote=fiat:USD")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var env struct {
		Data v1.PriceSnapshot `json:"data"`
	}
	mustDecode(t, resp, &env)
	if env.Data.Price != "1.000000000000" {
		t.Errorf("price = %q, want 1.000000000000 (peg-self should map to $1)", env.Data.Price)
	}
	if env.Data.PriceType != "peg" {
		t.Errorf("price_type = %q, want peg", env.Data.PriceType)
	}
	if env.Data.Quote != "fiat:USD" {
		t.Errorf("quote = %q, want fiat:USD", env.Data.Quote)
	}
}

// TestPrice_StablecoinFiatProxy_NonUSDQuoteSkips — the fallback only
// fires for quote=fiat:USD. Other fiat quotes (EUR, GBP) shouldn't
// pull the USD-pegged classic; if the user wants EUR pricing they
// need on-chain EUR-quoted trades or the fiat-cross-rate fallback.
func TestPrice_StablecoinFiatProxy_NonUSDQuoteSkips(t *testing.T) {
	usdcClassic, err := canonical.ParseAsset("USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")
	if err != nil {
		t.Fatalf("parse USDC: %v", err)
	}
	reader := &stubPriceReader{err: v1.ErrPriceNotFound}
	srv := v1.New(v1.Options{
		Prices:            reader,
		USDPeggedClassics: []canonical.Asset{usdcClassic},
	})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price?asset=native&quote=fiat:EUR")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (USDC peg should not fire for EUR quote)", resp.StatusCode)
	}
}

// TestPrice_StablecoinFiatProxy_CryptoTickerSelfPeg pins P2-4(b):
// the abstract global-ticker form of a stablecoin (crypto:USDC,
// crypto:EURC) priced in the fiat it tracks must return ~$1, not
// 404. Pre-fix, /v1/price?asset=crypto:USDC&quote=fiat:USD 404'd
// because tryStablecoinFiatProxy only recognised the classic-issued
// peg (USDC-GA5Z…) in usdPeggedClassics — the crypto:<TICKER> form
// the catalogue + explorer use fell through. The aggregate.FiatProxy
// arm now covers it (and the EUR/MXN pegs) WITHOUT any operator
// usd_pegged_classic_assets config, so this server wires none.
func TestPrice_StablecoinFiatProxy_CryptoTickerSelfPeg(t *testing.T) {
	reader := &stubPriceReader{err: v1.ErrPriceNotFound}
	srv := v1.New(v1.Options{Prices: reader}) // no USDPeggedClassics
	ts := startHTTPTest(t, srv.Handler())

	for _, tc := range []struct {
		asset, quote string
	}{
		{"crypto:USDC", "fiat:USD"},
		{"crypto:USDT", "fiat:USD"},
		{"crypto:EURC", "fiat:EUR"},
	} {
		resp := mustGet(t, ts.URL+"/v1/price?asset="+tc.asset+"&quote="+tc.quote)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s/%s: status = %d, want 200", tc.asset, tc.quote, resp.StatusCode)
		}
		var env struct {
			Data v1.PriceSnapshot `json:"data"`
		}
		mustDecode(t, resp, &env)
		if env.Data.Price != "1.000000000000" {
			t.Errorf("%s/%s: price = %q, want 1.000000000000", tc.asset, tc.quote, env.Data.Price)
		}
		if env.Data.PriceType != "peg" {
			t.Errorf("%s/%s: price_type = %q, want peg", tc.asset, tc.quote, env.Data.PriceType)
		}
		if env.Data.Quote != tc.quote {
			t.Errorf("%s/%s: quote = %q, want %s", tc.asset, tc.quote, env.Data.Quote, tc.quote)
		}
	}
}

// TestPrice_StablecoinFiatProxy_CrossPegQuoteSkips — the self-peg arm
// fires ONLY when the requested quote IS the fiat the stablecoin
// tracks. crypto:USDC priced in fiat:EUR is a real cross-rate (USD→
// EUR), not a $1 peg, so it must NOT synthesise 1.0 — it falls
// through to 404 (no cross-rate data wired here). Guards against the
// FiatProxy arm hiding a genuine FX conversion behind a flat peg.
func TestPrice_StablecoinFiatProxy_CrossPegQuoteSkips(t *testing.T) {
	reader := &stubPriceReader{err: v1.ErrPriceNotFound}
	srv := v1.New(v1.Options{Prices: reader})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price?asset=crypto:USDC&quote=fiat:EUR")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (USDC/EUR is a cross-rate, not a $1 peg)", resp.StatusCode)
	}
}

// stubDivergenceLooker is a minimal v1.DivergenceLooker for tests.
// firing controls what DivergenceFiringFor returns; err is the
// surfaced error (nil = clean response).
type stubDivergenceLooker struct {
	firing bool
	err    error
	calls  int
}

func (s *stubDivergenceLooker) DivergenceFiringFor(_ context.Context, _ canonical.Asset) (bool, error) {
	s.calls++
	return s.firing, s.err
}

// stubConfidenceLooker is a minimal v1.ConfidenceLooker for tests.
type stubConfidenceLooker struct {
	score v1.PriceSnapshotConfidence
	found bool
	err   error
	calls int
}

func (s *stubConfidenceLooker) LookupConfidence(_ context.Context, _, _ canonical.Asset, _ time.Duration) (v1.PriceSnapshotConfidence, bool, error) {
	s.calls++
	return s.score, s.found, s.err
}

// TestPrice_ConfidenceFlowsToWire — when the looker returns a
// cached score, the response includes both `confidence` and
// `confidence_factors` on the data object.
func TestPrice_ConfidenceFlowsToWire(t *testing.T) {
	reader := &stubPriceReader{
		snapshots: map[string]v1.PriceSnapshot{
			"native/fiat:USD": {Price: "0.07", PriceType: "vwap"},
		},
	}
	conf := &stubConfidenceLooker{
		score: v1.PriceSnapshotConfidence{
			Confidence: 0.92,
			Factors: v1.ConfidenceFactors{
				ZScore: 0.95, SourceCount: 0.95, Diversity: 1.0,
				Liquidity: 1.0, CrossOracle: 1.0, BaselineQuality: 1.0,
			},
		},
		found: true,
	}
	srv := v1.New(v1.Options{Prices: reader, Confidence: conf})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price?asset=native&quote=fiat:USD")
	body, _ := readAll(resp)
	if !strings.Contains(body, `"confidence":0.92`) {
		t.Errorf("confidence float not set: %s", body)
	}
	if !strings.Contains(body, `"confidence_factors"`) {
		t.Errorf("confidence_factors not present: %s", body)
	}
	if !strings.Contains(body, `"baseline_quality":1`) {
		t.Errorf("factor sub-fields missing: %s", body)
	}
	if conf.calls != 1 {
		t.Errorf("looker calls = %d, want 1", conf.calls)
	}
}

// TestPrice_ConfidenceCacheMissOmitsFields — looker returns
// (zero, false, nil): the snapshot has no confidence-related
// fields on the wire (omitempty hides them).
func TestPrice_ConfidenceCacheMissOmitsFields(t *testing.T) {
	reader := &stubPriceReader{
		snapshots: map[string]v1.PriceSnapshot{
			"native/fiat:USD": {Price: "0.07", PriceType: "vwap"},
		},
	}
	conf := &stubConfidenceLooker{found: false}
	srv := v1.New(v1.Options{Prices: reader, Confidence: conf})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price?asset=native&quote=fiat:USD")
	body, _ := readAll(resp)
	if strings.Contains(body, `"confidence"`) {
		t.Errorf("confidence field shouldn't appear on cache miss: %s", body)
	}
	if strings.Contains(body, `"confidence_factors"`) {
		t.Errorf("confidence_factors shouldn't appear on cache miss: %s", body)
	}
}

// TestPrice_ConfidenceLookerErrorIsBestEffort — a looker error
// must NOT cause the price endpoint to fail. Confidence fields
// stay unset; the price still flows.
func TestPrice_ConfidenceLookerErrorIsBestEffort(t *testing.T) {
	reader := &stubPriceReader{
		snapshots: map[string]v1.PriceSnapshot{
			"native/fiat:USD": {Price: "0.07", PriceType: "vwap"},
		},
	}
	conf := &stubConfidenceLooker{err: errors.New("redis exploded")}
	srv := v1.New(v1.Options{Prices: reader, Confidence: conf})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price?asset=native&quote=fiat:USD")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 — confidence error must NOT fail the price call", resp.StatusCode)
	}
	body, _ := readAll(resp)
	if strings.Contains(body, `"confidence"`) {
		t.Errorf("confidence shouldn't appear on lookup error: %s", body)
	}
}

// TestPrice_NoConfidenceLookerOmitsFields — when the binary
// hasn't wired a ConfidenceLooker, the field is silently absent.
// Same shape as a cache miss.
func TestPrice_NoConfidenceLookerOmitsFields(t *testing.T) {
	reader := &stubPriceReader{
		snapshots: map[string]v1.PriceSnapshot{
			"native/fiat:USD": {Price: "0.07", PriceType: "vwap"},
		},
	}
	srv := v1.New(v1.Options{Prices: reader})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price?asset=native&quote=fiat:USD")
	body, _ := readAll(resp)
	if strings.Contains(body, `"confidence"`) {
		t.Errorf("confidence shouldn't appear without a looker: %s", body)
	}
}

// TestPrice_DivergenceFires — when the lookup says the warning is
// firing for this asset, flags.divergence_warning is true.
func TestPrice_DivergenceFires(t *testing.T) {
	reader := &stubPriceReader{
		snapshots: map[string]v1.PriceSnapshot{
			"native/fiat:USD": {Price: "0.07", PriceType: "vwap"},
		},
	}
	div := &stubDivergenceLooker{firing: true}
	srv := v1.New(v1.Options{Prices: reader, Divergence: div})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price?asset=native&quote=fiat:USD")
	body, _ := readAll(resp)
	if !strings.Contains(body, `"divergence_warning":true`) {
		t.Errorf("divergence_warning not set: %s", body)
	}
	if div.calls != 1 {
		t.Errorf("divergence lookup calls = %d, want 1", div.calls)
	}
}

// TestPrice_DivergenceClean — when the lookup says no warning,
// flags.divergence_warning stays false.
func TestPrice_DivergenceClean(t *testing.T) {
	reader := &stubPriceReader{
		snapshots: map[string]v1.PriceSnapshot{
			"native/fiat:USD": {Price: "0.07", PriceType: "vwap"},
		},
	}
	div := &stubDivergenceLooker{firing: false}
	srv := v1.New(v1.Options{Prices: reader, Divergence: div})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price?asset=native&quote=fiat:USD")
	body, _ := readAll(resp)
	if !strings.Contains(body, `"divergence_warning":false`) {
		t.Errorf("divergence_warning expected false: %s", body)
	}
}

// TestPrice_DivergenceErrorIsBestEffort — a divergence lookup error
// must NOT cause the price endpoint to fail. Flag stays at default
// (false); the price still flows.
func TestPrice_DivergenceErrorIsBestEffort(t *testing.T) {
	reader := &stubPriceReader{
		snapshots: map[string]v1.PriceSnapshot{
			"native/fiat:USD": {Price: "0.07", PriceType: "vwap"},
		},
	}
	div := &stubDivergenceLooker{err: errors.New("redis exploded")}
	srv := v1.New(v1.Options{Prices: reader, Divergence: div})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price?asset=native&quote=fiat:USD")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 — divergence error must NOT fail the price call", resp.StatusCode)
	}
	body, _ := readAll(resp)
	if !strings.Contains(body, `"divergence_warning":false`) {
		t.Errorf("flag expected default false on lookup error: %s", body)
	}
}

// TestPrice_NoDivergenceLookerLeavesFlagFalse — when no
// DivergenceLooker is wired (typical pre-launch / no-Redis state),
// the flag never fires and no lookup is attempted.
func TestPrice_NoDivergenceLookerLeavesFlagFalse(t *testing.T) {
	reader := &stubPriceReader{
		snapshots: map[string]v1.PriceSnapshot{
			"native/fiat:USD": {Price: "0.07", PriceType: "vwap"},
		},
	}
	srv := v1.New(v1.Options{Prices: reader})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price?asset=native&quote=fiat:USD")
	body, _ := readAll(resp)
	if !strings.Contains(body, `"divergence_warning":false`) {
		t.Errorf("flag should default to false without a looker: %s", body)
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

// TestVWAP1mToSnapshot is the CAGG-served counterpart to
// TestLastTradeToSnapshot. Confirms the snapshot's ObservedAt
// reflects the END of the 1-minute window (not its start) and the
// VWAP string is passed through unchanged from the prices_1m row.
func TestVWAP1mToSnapshot(t *testing.T) {
	bucketStart := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	got := v1.VWAP1mToSnapshot("native", "fiat:USD", "0.123456789", bucketStart)

	if got.AssetID != "native" {
		t.Errorf("AssetID = %q, want native", got.AssetID)
	}
	if got.Quote != "fiat:USD" {
		t.Errorf("Quote = %q, want fiat:USD", got.Quote)
	}
	if got.Price != "0.123456789" {
		t.Errorf("Price = %q, want pass-through of NUMERIC text 0.123456789", got.Price)
	}
	if got.PriceType != "vwap" {
		t.Errorf("PriceType = %q, want vwap (CAGG-served path)", got.PriceType)
	}
	if got.WindowSeconds != 60 {
		t.Errorf("WindowSeconds = %d, want 60 (1-minute CAGG)", got.WindowSeconds)
	}
	wantObserved := bucketStart.Add(60 * time.Second)
	if !got.ObservedAt.Equal(wantObserved) {
		t.Errorf("ObservedAt = %v, want %v (END of window, not start)", got.ObservedAt, wantObserved)
	}
}

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

// stubFrozenLooker implements v1.FrozenLooker for tests. `frozen`
// controls FrozenForPair's bool return; `err` is the surfaced error.
type stubFrozenLooker struct {
	frozen bool
	err    error
	calls  int
}

func (s *stubFrozenLooker) FrozenForPair(_ context.Context, _, _ canonical.Asset) (bool, error) {
	s.calls++
	return s.frozen, s.err
}

// TestPrice_FrozenSetsBothFlags — when the looker says frozen, the
// envelope carries flags.frozen=true AND flags.single_source=true,
// regardless of how many sources the snapshot reports. (Per
// anomaly.ActionFreeze a frozen response IS the LKG, which is by
// definition single-sourced.)
func TestPrice_FrozenSetsBothFlags(t *testing.T) {
	reader := &stubPriceReader{
		snapshots: map[string]v1.PriceSnapshot{
			"native/fiat:USD": {Price: "0.07", PriceType: "vwap"},
		},
		// Multi-source on the underlying snapshot to prove the freeze
		// override forces single_source=true even so.
		sources: map[string][]string{
			"native/fiat:USD": {"sdex", "soroswap", "binance"},
		},
	}
	frz := &stubFrozenLooker{frozen: true}
	srv := v1.New(v1.Options{Prices: reader, Freeze: frz})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price?asset=native&quote=fiat:USD")
	body, _ := readAll(resp)
	if !strings.Contains(body, `"frozen":true`) {
		t.Errorf("frozen flag not set: %s", body)
	}
	if !strings.Contains(body, `"single_source":true`) {
		t.Errorf("single_source flag should be forced true on freeze: %s", body)
	}
	if frz.calls != 1 {
		t.Errorf("freeze lookup calls = %d, want 1", frz.calls)
	}
}

// TestPrice_NotFrozenSingleSourceFromSourceCount — when not frozen,
// single_source mirrors len(sources)==1.
func TestPrice_NotFrozenSingleSourceFromSourceCount(t *testing.T) {
	reader := &stubPriceReader{
		snapshots: map[string]v1.PriceSnapshot{
			"native/fiat:USD": {Price: "0.07", PriceType: "last_trade"},
		},
		sources: map[string][]string{
			"native/fiat:USD": {"sdex"}, // single source
		},
	}
	frz := &stubFrozenLooker{frozen: false}
	srv := v1.New(v1.Options{Prices: reader, Freeze: frz})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price?asset=native&quote=fiat:USD")
	body, _ := readAll(resp)
	if strings.Contains(body, `"frozen":true`) {
		t.Errorf("frozen flag should not fire: %s", body)
	}
	if !strings.Contains(body, `"single_source":true`) {
		t.Errorf("single_source should be true with 1 source: %s", body)
	}
}

// TestPrice_NotFrozenMultiSourceLeavesSingleSourceFalse — multi-source
// + not-frozen leaves single_source absent (omitempty).
func TestPrice_NotFrozenMultiSourceLeavesSingleSourceFalse(t *testing.T) {
	reader := &stubPriceReader{
		snapshots: map[string]v1.PriceSnapshot{
			"native/fiat:USD": {Price: "0.07", PriceType: "vwap"},
		},
		sources: map[string][]string{
			"native/fiat:USD": {"sdex", "soroswap"},
		},
	}
	frz := &stubFrozenLooker{frozen: false}
	srv := v1.New(v1.Options{Prices: reader, Freeze: frz})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price?asset=native&quote=fiat:USD")
	body, _ := readAll(resp)
	if strings.Contains(body, `"single_source":true`) {
		t.Errorf("single_source should not fire on multi-source: %s", body)
	}
	if strings.Contains(body, `"frozen":true`) {
		t.Errorf("frozen should not fire when looker says false: %s", body)
	}
}

// TestPrice_FreezeErrorIsBestEffort — a freeze lookup error must NOT
// cause the price call to fail. Flag stays false; the price still
// flows. Mirrors the divergence-error contract.
func TestPrice_FreezeErrorIsBestEffort(t *testing.T) {
	reader := &stubPriceReader{
		snapshots: map[string]v1.PriceSnapshot{
			"native/fiat:USD": {Price: "0.07", PriceType: "vwap"},
		},
		sources: map[string][]string{
			"native/fiat:USD": {"sdex", "soroswap"},
		},
	}
	frz := &stubFrozenLooker{err: errors.New("redis exploded")}
	srv := v1.New(v1.Options{Prices: reader, Freeze: frz})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price?asset=native&quote=fiat:USD")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 — freeze lookup error must NOT fail the price call", resp.StatusCode)
	}
	body, _ := readAll(resp)
	if strings.Contains(body, `"frozen":true`) {
		t.Errorf("frozen should default false on lookup error: %s", body)
	}
}

// TestPrice_NoFreezeLooker_DerivesFromSources — without a FrozenLooker
// wired, frozen never fires and single_source comes from the
// observation count.
func TestPrice_NoFreezeLooker_DerivesFromSources(t *testing.T) {
	reader := &stubPriceReader{
		snapshots: map[string]v1.PriceSnapshot{
			"native/fiat:USD": {Price: "0.07", PriceType: "last_trade"},
		},
		sources: map[string][]string{
			"native/fiat:USD": {"sdex"},
		},
	}
	srv := v1.New(v1.Options{Prices: reader}) // no Freeze
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price?asset=native&quote=fiat:USD")
	body, _ := readAll(resp)
	if strings.Contains(body, `"frozen":true`) {
		t.Errorf("frozen should not fire without a looker: %s", body)
	}
	if !strings.Contains(body, `"single_source":true`) {
		t.Errorf("single_source should derive from len(sources)==1: %s", body)
	}
}

// TestPriceBatch_FrozenORedAcrossRows — a batch where one row freezes
// and others don't sets envelope.flags.frozen=true (and
// single_source=true). Envelope flags are OR over per-row signals,
// matching the Stale flag's contract.
func TestPriceBatch_FrozenORedAcrossRows(t *testing.T) {
	reader := &stubPriceReader{
		snapshots: map[string]v1.PriceSnapshot{
			"native/fiat:USD":   {Price: "0.07", PriceType: "vwap"},
			"fiat:EUR/fiat:USD": {Price: "1.10", PriceType: "vwap"},
		},
		sources: map[string][]string{
			"native/fiat:USD":   {"sdex", "soroswap"},
			"fiat:EUR/fiat:USD": {"sdex", "soroswap"},
		},
	}
	// Looker freezes only the EUR row, not native.
	frz := &batchFreezeLooker{frozenForBase: "EUR"}
	srv := v1.New(v1.Options{Prices: reader, Freeze: frz})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price/batch?asset_ids=native,fiat:EUR&quote=fiat:USD")
	body, _ := readAll(resp)
	if !strings.Contains(body, `"frozen":true`) {
		t.Errorf("envelope frozen should fire when ANY row is frozen: %s", body)
	}
	if !strings.Contains(body, `"single_source":true`) {
		t.Errorf("envelope single_source should follow frozen: %s", body)
	}
}

// batchFreezeLooker freezes any pair whose base.Code matches the
// configured value. Lets a single test cover "freeze fires for asset
// X but not asset Y".
type batchFreezeLooker struct {
	frozenForBase string
}

func (b *batchFreezeLooker) FrozenForPair(_ context.Context, asset, _ canonical.Asset) (bool, error) {
	return asset.Code == b.frozenForBase, nil
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

// stubCurrenciesReader is the test seam for the fiat-cross-rate
// fallback on /v1/price. Returns whatever Snapshot it was
// configured with; nil → "warming up" branch.
type stubCurrenciesReader struct {
	snap *v1.CurrenciesSnapshot
}

func (s *stubCurrenciesReader) Latest() *v1.CurrenciesSnapshot { return s.snap }

// TestPrice_FiatCrossRate_EURUSD — when the asset and quote are
// both fiat and direct trade data is absent (the steady state for
// fiat conversions on Stellar), the handler synthesises a cross
// rate from the forex snapshot. EUR rate_usd=0.92 means
// 1 USD = 0.92 EUR, so 1 EUR = 1/0.92 = ~1.0869565 USD.
//
// flags.triangulated=true documents the synthesis to the caller —
// this isn't a direct on-chain trade.
func TestPrice_FiatCrossRate_EURUSD(t *testing.T) {
	reader := &stubPriceReader{err: v1.ErrPriceNotFound}
	currencies := &stubCurrenciesReader{
		snap: &v1.CurrenciesSnapshot{
			Currencies: []v1.CurrencyEntry{
				{Ticker: "EUR", Name: "Euro", RateUSD: 0.92},
			},
			PublishedAt: time.Unix(1_770_000_000, 0).UTC(),
		},
	}
	srv := v1.New(v1.Options{Prices: reader, Currencies: currencies})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price?asset=fiat:EUR&quote=fiat:USD")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (fiat cross-rate fallback)", resp.StatusCode)
	}
	body, _ := readAll(resp)
	for _, s := range []string{
		`"asset_id":"fiat:EUR"`,
		`"quote":"fiat:USD"`,
		`"price_type":"vwap"`,
		`"triangulated":true`,
	} {
		if !strings.Contains(body, s) {
			t.Errorf("body missing %q: %s", s, body)
		}
	}
	// 1/0.92 ≈ 1.0869565… — assert the price is in the right range
	// without pinning every digit. The exact serialisation depends
	// on Go's strconv.FormatFloat shortest-round-trip output.
	if !strings.Contains(body, `"price":"1.086`) {
		t.Errorf("price not ~1.086…: %s", body)
	}
}

// TestPrice_FiatCrossRate_NotFiatBothSides — the cross-rate fallback
// only fires when BOTH asset and quote are fiat. native/fiat:USD
// stays on the Redis/Triangulated path so this branch doesn't
// silently shadow the stablecoin proxy.
func TestPrice_FiatCrossRate_NotFiatBothSides(t *testing.T) {
	reader := &stubPriceReader{err: v1.ErrPriceNotFound}
	currencies := &stubCurrenciesReader{
		snap: &v1.CurrenciesSnapshot{
			Currencies: []v1.CurrencyEntry{
				{Ticker: "EUR", RateUSD: 0.92},
			},
		},
	}
	srv := v1.New(v1.Options{Prices: reader, Currencies: currencies})
	ts := startHTTPTest(t, srv.Handler())

	// native is AssetType=Native, not fiat — fiat cross fallback
	// must not fire here. Without a Triangulated looker either,
	// the original 404 stands.
	resp := mustGet(t, ts.URL+"/v1/price?asset=native&quote=fiat:USD")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (fiat fallback shouldn't fire for native/fiat:USD)", resp.StatusCode)
	}
}

// TestPrice_XLMAlias_NativeFallsThroughToCryptoXLM verifies that
// /v1/price?asset=native&quote=fiat:USD picks up a VWAP published
// under crypto:XLM/fiat:USD when no native/fiat:USD key exists.
// This is the F-1308 / #87 customer-visible 39h-stale bug on
// 2026-05-29: SDEX writes `native`, CEX writes `crypto:XLM`; the
// aggregator's pair-set published under crypto:XLM only, and the
// public surface queried by `native` and missed.
func TestPrice_XLMAlias_NativeFallsThroughToCryptoXLM(t *testing.T) {
	reader := &stubPriceReader{
		snapshots: map[string]v1.PriceSnapshot{
			"crypto:XLM/fiat:USD": {
				AssetID:    "crypto:XLM",
				Quote:      "fiat:USD",
				Price:      "0.1500",
				PriceType:  "vwap",
				ObservedAt: time.Now().UTC(),
			},
		},
		stale:   map[string]bool{"crypto:XLM/fiat:USD": false},
		sources: map[string][]string{"crypto:XLM/fiat:USD": {"binance", "bitstamp", "coinbase"}},
	}
	srv := v1.New(v1.Options{Prices: reader})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price?asset=native&quote=fiat:USD")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	body, _ := readAll(resp)
	if !strings.Contains(body, `"price":"0.1500"`) {
		t.Errorf("expected price 0.1500 from crypto:XLM alias; got: %s", body)
	}
	if strings.Contains(body, `"stale":true`) {
		t.Errorf("alias-served price should not flag stale=true; got: %s", body)
	}
	if strings.Contains(body, `"triangulated":true`) {
		t.Errorf("alias-served price should not flag triangulated=true; got: %s", body)
	}
}

// TestPrice_XLMAlias_CryptoXLMFallsThroughToNative verifies the
// symmetric case — a customer querying with crypto:XLM picks up
// VWAPs published under native.
func TestPrice_XLMAlias_CryptoXLMFallsThroughToNative(t *testing.T) {
	reader := &stubPriceReader{
		snapshots: map[string]v1.PriceSnapshot{
			"native/fiat:USD": {
				AssetID:    "native",
				Quote:      "fiat:USD",
				Price:      "0.1500",
				PriceType:  "vwap",
				ObservedAt: time.Now().UTC(),
			},
		},
		stale:   map[string]bool{"native/fiat:USD": false},
		sources: map[string][]string{"native/fiat:USD": {"sdex"}},
	}
	srv := v1.New(v1.Options{Prices: reader})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price?asset=crypto:XLM&quote=fiat:USD")
	if resp.StatusCode != http.StatusOK {
		body, _ := readAll(resp)
		t.Fatalf("status %d, want 200 (body: %s)", resp.StatusCode, body)
	}
}

// TestPrice_XLMAlias_PrefersFreshOverStale checks the alias loop's
// staleness ordering: literal-asset has a stale VWAP, alias has a
// fresh one → return the fresh alias.
func TestPrice_XLMAlias_PrefersFreshOverStale(t *testing.T) {
	reader := &stubPriceReader{
		snapshots: map[string]v1.PriceSnapshot{
			"native/fiat:USD":     {AssetID: "native", Quote: "fiat:USD", Price: "0.1000", PriceType: "vwap", ObservedAt: time.Now().Add(-48 * time.Hour).UTC()},
			"crypto:XLM/fiat:USD": {AssetID: "crypto:XLM", Quote: "fiat:USD", Price: "0.1500", PriceType: "vwap", ObservedAt: time.Now().UTC()},
		},
		stale: map[string]bool{
			"native/fiat:USD":     true,
			"crypto:XLM/fiat:USD": false,
		},
		sources: map[string][]string{
			"native/fiat:USD":     {"sdex"},
			"crypto:XLM/fiat:USD": {"binance"},
		},
	}
	srv := v1.New(v1.Options{Prices: reader})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/price?asset=native&quote=fiat:USD")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	body, _ := readAll(resp)
	if !strings.Contains(body, `"price":"0.1500"`) {
		t.Errorf("expected fresh crypto:XLM 0.1500; got: %s", body)
	}
}
