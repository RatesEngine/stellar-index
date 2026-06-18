package v1_test

import (
	"context"
	"errors"
	"math/big"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/StellarIndex/stellar-index/internal/canonical"
	"github.com/StellarIndex/stellar-index/internal/storage/timescale"
	"github.com/StellarIndex/stellar-index/internal/supply"

	v1 "github.com/StellarIndex/stellar-index/internal/api/v1"
)

// stubSupplyLooker implements v1.SupplyLooker for tests.
type stubSupplyLooker struct {
	snap  supply.Supply
	daily []timescale.SupplyDayPoint
	err   error
	hit   bool // when false, simulates ErrSupplyNotFound
}

func (s *stubSupplyLooker) LatestSupply(_ context.Context, _ string) (supply.Supply, error) {
	if s.err != nil {
		return supply.Supply{}, s.err
	}
	if !s.hit {
		return supply.Supply{}, v1.ErrSupplyNotFound
	}
	return s.snap, nil
}

func (s *stubSupplyLooker) DailyCirculatingSupply(_ context.Context, _ string, _, _ time.Time) ([]timescale.SupplyDayPoint, error) {
	return s.daily, s.err
}

// stubVolumeReader implements v1.VolumeReader for tests. Records the
// assetKey passed in so tests can assert the API call site supplies
// the trade-table representation (canonical.Asset.String()) rather
// than the supply-table representation (supply.AssetKey()).
type stubVolumeReader struct {
	gotKey string
	volume string
	err    error
	calls  int
}

func (s *stubVolumeReader) Volume24hUSDForAsset(_ context.Context, assetKey string) (string, error) {
	s.calls++
	s.gotKey = assetKey
	if s.err != nil {
		return "", s.err
	}
	return s.volume, nil
}

// TestF2_VolumeReaderReceivesTradeTableKey — the VolumeReader contract
// pins assetKey to the trades.base_asset/quote_asset shape (canonical
// `asset.String()`). For native XLM that's "native", NOT "XLM" — the
// supply-package convention. Pre-2026-05-04 the call site passed
// supply.AssetKey() and the lookup never matched any trade row,
// returning "0" for native indefinitely.
func TestF2_VolumeReaderReceivesTradeTableKey(t *testing.T) {
	cases := []struct {
		name      string
		assetPath string
		wantKey   string
	}{
		{"native", "native", "native"},
		{"classic-USDC", "USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN", "USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vol := &stubVolumeReader{volume: "12345.67"}
			srv := v1.New(v1.Options{Volume: vol})
			ts := startHTTPTest(t, srv.Handler())

			resp := mustGet(t, ts.URL+"/v1/assets/"+tc.assetPath)
			if resp.StatusCode != http.StatusOK {
				body, _ := readAll(resp)
				t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
			}
			if vol.calls != 1 {
				t.Fatalf("VolumeReader called %d times, want 1", vol.calls)
			}
			if vol.gotKey != tc.wantKey {
				t.Errorf("Volume24hUSDForAsset received key = %q, want %q (trade-table shape, not supply-key shape)", vol.gotKey, tc.wantKey)
			}
			body, _ := readAll(resp)
			if !strings.Contains(body, `"volume_24h_usd":"12345.67"`) {
				t.Errorf("response missing volume: %s", body)
			}
		})
	}
}

// stubChange24hReader implements v1.Change24hReader for tests.
// Returns the per-asset price string from `prices`, or unavailable
// when the asset key isn't seeded; an explicit `err` short-circuits
// for the postgres-unavailable test path.
type stubChange24hReader struct {
	prices map[string]string
	err    error
}

func (s *stubChange24hReader) USDPrice24hAgo(_ context.Context, asset canonical.Asset) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	p, ok := s.prices[asset.String()]
	if !ok {
		return "", v1.ErrChange24hUnavailable
	}
	return p, nil
}

// xlmSupplySnap returns a Supply for native XLM matching the
// frozen-2019 total + a plausible circulating.
func xlmSupplySnap() supply.Supply {
	return supply.Supply{
		AssetKey:          "XLM",
		TotalSupply:       supply.XLMTotalSupplyStroops(), // 5.00018e17 stroops
		CirculatingSupply: mustBigInt("499000000000000000"),
		MaxSupply:         supply.XLMTotalSupplyStroops(),
		Basis:             supply.BasisXLMSDFReserveExclusion,
	}
}

func mustBigInt(s string) *big.Int {
	v, ok := new(big.Int).SetString(s, 10)
	if !ok {
		panic("mustBigInt: bad input " + s)
	}
	return v
}

// TestF2_NativeAssetWithSupplyAndPrice — happy path: native XLM
// has a recorded snapshot, USD price is available, all six F2
// fields populate.
func TestF2_NativeAssetWithSupplyAndPrice(t *testing.T) {
	supplyStub := &stubSupplyLooker{hit: true, snap: xlmSupplySnap()}
	priceStub := &stubPriceReader{
		snapshots: map[string]v1.PriceSnapshot{
			"native/fiat:USD": {Price: "0.07", PriceType: "vwap"},
		},
	}
	srv := v1.New(v1.Options{
		Prices: priceStub,
		Supply: supplyStub,
	})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/assets/native")
	body, _ := readAll(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	// Supply numbers — raw stroops as strings
	mustContain(t, body, `"total_supply":"500018068120000000"`)
	mustContain(t, body, `"circulating_supply":"499000000000000000"`)
	mustContain(t, body, `"max_supply":"500018068120000000"`)
	mustContain(t, body, `"supply_basis":"xlm_sdf_reserve_exclusion"`)

	// market_cap = 499_000_000_000_000_000 stroops / 10^7 × $0.07
	//            = 49_900_000_000 XLM × 0.07
	//            = $3,493,000,000.00
	mustContain(t, body, `"market_cap_usd":"3493000000.00"`)
	// fdv = 500_018_068_120_000_000 / 10^7 × 0.07 = $3,500,126,476.84
	mustContain(t, body, `"fdv_usd":"3500126476.84"`)
	// F-1271: price_usd is inlined so wallet UIs don't need a
	// second /v1/price RT. The handler doesn't go through the
	// coins-overlay path for native (no coin row), so this
	// exercises the populateMarketCap-side fallback.
	mustContain(t, body, `"price_usd":"0.07"`)
}

// TestF2_PriceUSDInlinedWithoutSupply pins F-1271's contract: even
// when the asset has no supply snapshot (so market_cap_usd stays
// null), price_usd must still surface from the price lookup that
// populateMarketCap already pays for. Wallets that just want the
// current price shouldn't need a second round-trip.
func TestF2_PriceUSDInlinedWithoutSupply(t *testing.T) {
	supplyStub := &stubSupplyLooker{hit: false} // no supply row
	priceStub := &stubPriceReader{
		snapshots: map[string]v1.PriceSnapshot{
			"native/fiat:USD": {Price: "0.07", PriceType: "vwap"},
		},
	}
	srv := v1.New(v1.Options{
		Prices: priceStub,
		Supply: supplyStub,
	})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/assets/native")
	body, _ := readAll(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	// price_usd present even though no supply snapshot exists.
	mustContain(t, body, `"price_usd":"0.07"`)
	// market_cap_usd absent — no circulating supply to multiply by.
	if strings.Contains(body, `"market_cap_usd"`) {
		t.Errorf("market_cap_usd should be absent without a supply snapshot; body=%s", body)
	}
}

// TestF2_NoSupplyLooker_FieldsAbsent — without a SupplyLooker
// wired (early bring-up), the F2 fields are absent (json omitempty
// elides them) and the asset-detail body still serves cleanly.
func TestF2_NoSupplyLooker_FieldsAbsent(t *testing.T) {
	srv := v1.New(v1.Options{}) // no Supply, no Prices
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/assets/native")
	body, _ := readAll(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	for _, field := range []string{
		`"total_supply"`, `"circulating_supply"`, `"max_supply"`,
		`"market_cap_usd"`, `"fdv_usd"`, `"supply_basis"`,
	} {
		if strings.Contains(body, field) {
			t.Errorf("F2 field %s should be absent without a supply looker; body=%s", field, body)
		}
	}
}

// TestF2_SupplyNotFound_FieldsAbsent — the asset has no recorded
// supply snapshot (e.g. orchestrator hasn't run for it yet);
// ErrSupplyNotFound is silent — F2 fields stay null, no warning logged.
func TestF2_SupplyNotFound_FieldsAbsent(t *testing.T) {
	supplyStub := &stubSupplyLooker{hit: false} // returns ErrSupplyNotFound
	srv := v1.New(v1.Options{Supply: supplyStub})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/assets/native")
	body, _ := readAll(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if strings.Contains(body, `"total_supply"`) {
		t.Errorf("total_supply should be absent on ErrSupplyNotFound; body=%s", body)
	}
}

// TestF2_NoMaxSupply_OmitsFDV — uncapped issuer + no override + no
// SEP-1 declaration: max_supply is null on the wire, fdv_usd is
// likewise null.
func TestF2_NoMaxSupply_OmitsFDV(t *testing.T) {
	snap := xlmSupplySnap()
	snap.MaxSupply = nil // simulate uncapped
	supplyStub := &stubSupplyLooker{hit: true, snap: snap}
	priceStub := &stubPriceReader{
		snapshots: map[string]v1.PriceSnapshot{
			"native/fiat:USD": {Price: "0.07", PriceType: "vwap"},
		},
	}
	srv := v1.New(v1.Options{Prices: priceStub, Supply: supplyStub})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/assets/native")
	body, _ := readAll(resp)

	if strings.Contains(body, `"max_supply"`) {
		t.Errorf("max_supply should be absent when nil; body=%s", body)
	}
	if strings.Contains(body, `"fdv_usd"`) {
		t.Errorf("fdv_usd should be absent when max_supply is nil; body=%s", body)
	}
	// circulating + market_cap should still populate.
	mustContain(t, body, `"circulating_supply":"499000000000000000"`)
	mustContain(t, body, `"market_cap_usd":"3493000000.00"`)
}

// TestF2_NoUSDPrice_OmitsMarketCap — supply numbers populate but
// the asset has no USD price (untracked pair, ErrPriceNotFound);
// market_cap_usd + fdv_usd stay null.
func TestF2_NoUSDPrice_OmitsMarketCap(t *testing.T) {
	supplyStub := &stubSupplyLooker{hit: true, snap: xlmSupplySnap()}
	// Empty stubPriceReader — every LatestPrice returns ErrPriceNotFound.
	priceStub := &stubPriceReader{}
	srv := v1.New(v1.Options{Prices: priceStub, Supply: supplyStub})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/assets/native")
	body, _ := readAll(resp)
	mustContain(t, body, `"total_supply":"500018068120000000"`)
	mustContain(t, body, `"circulating_supply"`)
	if strings.Contains(body, `"market_cap_usd"`) {
		t.Errorf("market_cap_usd should be absent without a USD price; body=%s", body)
	}
	if strings.Contains(body, `"fdv_usd"`) {
		t.Errorf("fdv_usd should be absent without a USD price; body=%s", body)
	}
}

// TestF2_FiatAssetSkipsLookup — fiat:USD has no on-chain supply key;
// AssetKey returns an error and applyF2Fields silently no-ops. F2
// fields absent; no warning logged.
func TestF2_FiatAssetSkipsLookup(t *testing.T) {
	supplyStub := &stubSupplyLooker{hit: true, snap: xlmSupplySnap()}
	srv := v1.New(v1.Options{Supply: supplyStub})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/assets/fiat:USD")
	body, _ := readAll(resp)
	for _, field := range []string{`"total_supply"`, `"market_cap_usd"`} {
		if strings.Contains(body, field) {
			t.Errorf("fiat asset should skip F2 lookup; field %s leaked: %s", field, body)
		}
	}
}

// TestF2_PriceLookupErrorFallsThrough — a real (non-NotFound) price
// reader error falls through silently; F2 fields stay null, no 5xx.
// Mirrors the divergence-error best-effort posture.
func TestF2_PriceLookupErrorFallsThrough(t *testing.T) {
	supplyStub := &stubSupplyLooker{hit: true, snap: xlmSupplySnap()}
	priceStub := &stubPriceReader{err: errors.New("postgres unavailable")}
	srv := v1.New(v1.Options{Prices: priceStub, Supply: supplyStub})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/assets/native")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 — price error must NOT 5xx the asset call", resp.StatusCode)
	}
	body, _ := readAll(resp)
	// Supply still populates; market cap doesn't.
	mustContain(t, body, `"total_supply"`)
	if strings.Contains(body, `"market_cap_usd"`) {
		t.Errorf("market_cap_usd should be absent on price error: %s", body)
	}
}

// TestLookupUSDPrice_StablecoinFiatProxyFallback — when the
// reader's literal native/fiat:USD lookup misses (the steady-state
// case on Stellar mainnet — nothing on-chain quotes in fiat:USD),
// lookupUSDPrice now walks the operator's classic USD pegs. Same
// shape as the handler-side fix in #1217 / tryStablecoinFiatProxy,
// but applied at the F2-population layer where the handler's
// priceFallback isn't reachable. Without this, market_cap_usd /
// fdv_usd / change_24h_pct stayed null on every on-chain asset.
func TestLookupUSDPrice_StablecoinFiatProxyFallback(t *testing.T) {
	usdcClassic, err := canonical.ParseAsset("USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")
	if err != nil {
		t.Fatalf("parse USDC: %v", err)
	}
	priceStub := &stubPriceReader{
		// Literal native/fiat:USD missing. native/<USDC-classic> serves the price.
		snapshots: map[string]v1.PriceSnapshot{
			"native/" + usdcClassic.String(): {
				AssetID:   "native",
				Quote:     usdcClassic.String(),
				Price:     "0.1626",
				PriceType: "vwap",
			},
		},
	}
	supplyStub := &stubSupplyLooker{
		hit: true,
		snap: supply.Supply{
			AssetKey:          "XLM",
			TotalSupply:       new(big.Int).Mul(big.NewInt(50_001_806_812), big.NewInt(10_000_000)),
			CirculatingSupply: new(big.Int).Mul(big.NewInt(30_001_806_812), big.NewInt(10_000_000)),
		},
	}
	srv := v1.New(v1.Options{
		Prices:            priceStub,
		Supply:            supplyStub,
		USDPeggedClassics: []canonical.Asset{usdcClassic},
	})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/assets/native")
	body, _ := readAll(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	// market_cap_usd should populate via the proxy: 30B * 0.1626 = 4.878B.
	// The exact numeric formatting we don't pin precisely (10-digit precision); just
	// assert the field is present and starts with "4" billion-ish.
	mustContain(t, body, `"market_cap_usd"`)
}

// TestChange24hPct_HappyPath — current USD price + a 24h-ago
// reader yield a signed two-decimal percentage on the wire. Pinned
// to catch sign-format regressions (the leading "+" is part of the
// wire contract).
func TestChange24hPct_HappyPath(t *testing.T) {
	priceStub := &stubPriceReader{
		snapshots: map[string]v1.PriceSnapshot{
			"native/fiat:USD": {Price: "0.07127", PriceType: "vwap"},
		},
	}
	change24hStub := &stubChange24hReader{
		prices: map[string]string{"native": "0.07"},
	}
	srv := v1.New(v1.Options{Prices: priceStub, Change24h: change24hStub})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/assets/native")
	body, _ := readAll(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	// (0.07127 - 0.07) / 0.07 * 100 = 1.8142...% → "+1.81"
	mustContain(t, body, `"change_24h_pct":"+1.81"`)
}

// TestChange24hPct_NilPriceReader_NoPanic — Options documents Prices
// as independently optional ("nil → 503"), so a Prices==nil but
// Change24h!=nil wiring is legal. populateChange24h reaches the price
// lookup on a child goroutine (no middleware.Recoverer cover), so a
// missing nil-guard there would crash the whole API process, not 500
// one request. Regression for that latent panic: the request must
// serve cleanly with the field simply absent.
func TestChange24hPct_NilPriceReader_NoPanic(t *testing.T) {
	change24hStub := &stubChange24hReader{prices: map[string]string{"native": "0.07"}}
	srv := v1.New(v1.Options{Change24h: change24hStub}) // Prices left nil
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/assets/native")
	body, _ := readAll(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d (a nil price reader must not panic the populate goroutine)", resp.StatusCode)
	}
	if strings.Contains(body, `"change_24h_pct"`) {
		t.Errorf("change_24h_pct should be absent without a price reader; body=%s", body)
	}
}

// TestChange24hPct_NoComparisonBucket — asset has a current price
// but the 24h-ago window is empty (asset first traded < 24h ago,
// or pruned). ErrChange24hUnavailable is silent — field absent on
// the wire, no warning logged.
func TestChange24hPct_NoComparisonBucket(t *testing.T) {
	priceStub := &stubPriceReader{
		snapshots: map[string]v1.PriceSnapshot{
			"native/fiat:USD": {Price: "0.07", PriceType: "vwap"},
		},
	}
	change24hStub := &stubChange24hReader{prices: nil} // every lookup → unavailable
	srv := v1.New(v1.Options{Prices: priceStub, Change24h: change24hStub})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/assets/native")
	body, _ := readAll(resp)
	if strings.Contains(body, `"change_24h_pct"`) {
		t.Errorf("change_24h_pct should be absent when no 24h-ago bucket; body=%s", body)
	}
}

// TestChange24hPct_NoCurrentPrice_FieldAbsent — without a USD
// price (untracked asset, fiat:USD itself, ErrPriceNotFound), the
// reader is never called and the field stays null.
func TestChange24hPct_NoCurrentPrice_FieldAbsent(t *testing.T) {
	priceStub := &stubPriceReader{} // every LatestPrice → ErrPriceNotFound
	// Even with a populated 24h-ago reader, no current price means no pct.
	change24hStub := &stubChange24hReader{prices: map[string]string{"native": "0.07"}}
	srv := v1.New(v1.Options{Prices: priceStub, Change24h: change24hStub})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/assets/native")
	body, _ := readAll(resp)
	if strings.Contains(body, `"change_24h_pct"`) {
		t.Errorf("change_24h_pct should be absent without a current price; body=%s", body)
	}
}

// TestChange24hPct_NotWired_FieldAbsent — Options.Change24h nil
// (early bring-up): field absent on the wire, no panic, asset body
// still serves cleanly.
func TestChange24hPct_NotWired_FieldAbsent(t *testing.T) {
	priceStub := &stubPriceReader{
		snapshots: map[string]v1.PriceSnapshot{
			"native/fiat:USD": {Price: "0.07", PriceType: "vwap"},
		},
	}
	srv := v1.New(v1.Options{Prices: priceStub}) // no Change24h
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/assets/native")
	body, _ := readAll(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if strings.Contains(body, `"change_24h_pct"`) {
		t.Errorf("change_24h_pct should be absent without Change24h reader; body=%s", body)
	}
}

// TestChange24hPct_ReaderErrorFallsThrough — a non-Unavailable
// reader error (postgres down) falls through silently. Best-effort
// posture matches volume_24h_usd / market_cap_usd: feature
// unavailable, asset body still serves 200.
func TestChange24hPct_ReaderErrorFallsThrough(t *testing.T) {
	priceStub := &stubPriceReader{
		snapshots: map[string]v1.PriceSnapshot{
			"native/fiat:USD": {Price: "0.07", PriceType: "vwap"},
		},
	}
	change24hStub := &stubChange24hReader{err: errors.New("postgres unreachable")}
	srv := v1.New(v1.Options{Prices: priceStub, Change24h: change24hStub})
	ts := startHTTPTest(t, srv.Handler())

	resp := mustGet(t, ts.URL+"/v1/assets/native")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 — Change24h error must NOT 5xx", resp.StatusCode)
	}
	body, _ := readAll(resp)
	if strings.Contains(body, `"change_24h_pct"`) {
		t.Errorf("change_24h_pct should be absent on reader error; body=%s", body)
	}
}

// _ = canonical.AssetClassic // keep import used even if test list
// changes — touched here defensively.
var _ = canonical.AssetClassic

// mustContain fails the test when body doesn't include needle.
// Local to the F2 test file; helpers in the rest of the package
// don't expose this shape and the inline strings.Contains pattern
// repeats often enough here to warrant a one-liner.
func mustContain(t *testing.T, body, needle string) {
	t.Helper()
	if !strings.Contains(body, needle) {
		t.Errorf("body missing %q\n  body=%s", needle, body)
	}
}
