package v1_test

import (
	"context"
	"errors"
	"math/big"
	"net/http"
	"strings"
	"testing"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/supply"

	v1 "github.com/RatesEngine/rates-engine/internal/api/v1"
)

// stubSupplyLooker implements v1.SupplyLooker for tests.
type stubSupplyLooker struct {
	snap supply.Supply
	err  error
	hit  bool // when false, simulates ErrSupplyNotFound
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
